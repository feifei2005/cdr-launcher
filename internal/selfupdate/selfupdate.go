// Package selfupdate 实现启动器自身的检查、下载与替换。
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"cdr-launcher/internal/fetch"
	"cdr-launcher/internal/netproxy"
)

// ManifestURL 是启动器自更新清单地址，固定在代码中，不展示给用户。
const ManifestURL = "https://mc-api.xn--suki-uf1gk54ba.cn/pack/launcher/latest.json"

const maxBinarySize = 256 << 20

// Manifest 是远端启动器更新清单。
type Manifest struct {
	Version     string `json:"version"`
	PublishedAt string `json:"publishedAt,omitempty"`
	Notes       string `json:"notes,omitempty"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
}

// Info 是检查更新的结果。
type Info struct {
	Available      bool   `json:"available"`
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	Notes          string `json:"notes,omitempty"`
	Size           int64  `json:"size"`
}

// Check 拉取更新清单并比较版本。远端尚无清单（404）时视为无更新。
func Check(ctx context.Context, current string) (Info, *Manifest, error) {
	client := netproxy.NewClient(30 * time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ManifestURL, nil)
	if err != nil {
		return Info{}, nil, fmt.Errorf("构造启动器更新请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("User-Agent", "CDRLauncher/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return Info{}, nil, fmt.Errorf("获取启动器更新清单失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Info{CurrentVersion: current}, nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return Info{}, nil, fmt.Errorf("获取启动器更新清单失败: HTTP %s", resp.Status)
	}
	const limit = 64 << 10
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return Info{}, nil, fmt.Errorf("读取启动器更新清单失败: %w", err)
	}
	if len(raw) > limit {
		return Info{}, nil, errors.New("启动器更新清单过大")
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Info{}, nil, fmt.Errorf("解析启动器更新清单失败: %w", err)
	}
	m.Version = strings.TrimSpace(m.Version)
	if m.Version == "" {
		return Info{}, nil, errors.New("启动器更新清单缺少版本号")
	}
	info := Info{
		CurrentVersion: current,
		LatestVersion:  m.Version,
		Notes:          m.Notes,
		Size:           m.Size,
	}
	if CompareVersions(m.Version, current) <= 0 {
		return info, nil, nil
	}
	if m.Size <= 0 || m.Size > maxBinarySize {
		return Info{}, nil, errors.New("启动器更新包大小非法")
	}
	if !validSHA(m.SHA256) {
		return Info{}, nil, errors.New("启动器更新包 sha256 非法")
	}
	if _, err := resolveDownloadURL(m.URL); err != nil {
		return Info{}, nil, err
	}
	info.Available = true
	return info, &m, nil
}

// Download 下载新版本到目标程序旁的 .new 文件，校验后返回路径。
// segments 大于 1 且文件足够大时使用多连接分片，服务端不支持则自动回退单连接。
func Download(ctx context.Context, m Manifest, exePath string, segments int, progress func(done, total int64)) (string, error) {
	rawURL, err := resolveDownloadURL(m.URL)
	if err != nil {
		return "", err
	}
	final := exePath + ".new"
	part := final + ".part"
	_ = os.Remove(part)
	client := netproxy.NewClient(0)

	if segments > 1 && m.Size >= fetch.MinSegmentSize {
		err := downloadSegmented(ctx, client, rawURL, part, m.Size, segments, progress)
		if err == nil {
			sum, hashErr := hashFile(part)
			if hashErr != nil {
				_ = os.Remove(part)
				return "", fmt.Errorf("校验启动器更新失败: %w", hashErr)
			}
			if !strings.EqualFold(sum, m.SHA256) {
				_ = os.Remove(part)
				return "", errors.New("启动器更新包校验失败（sha256 不符）")
			}
			if err := os.Remove(final); err != nil && !os.IsNotExist(err) {
				_ = os.Remove(part)
				return "", fmt.Errorf("清理旧文件失败: %w", err)
			}
			if err := os.Rename(part, final); err != nil {
				return "", fmt.Errorf("保存启动器更新失败: %w", err)
			}
			return final, nil
		}
		_ = os.Remove(part)
		if !errors.Is(err, fetch.ErrRangeUnsupported) {
			return "", err
		}
	}

	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", fmt.Errorf("创建下载文件失败: %w", err)
	}
	defer f.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "CDRLauncher/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载启动器更新失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载启动器更新失败: HTTP %s", resp.Status)
	}
	total := m.Size
	if resp.ContentLength > 0 {
		total = resp.ContentLength
	}
	hasher := sha256.New()
	reader := &progressReader{
		r:     io.TeeReader(io.LimitReader(resp.Body, maxBinarySize+1), hasher),
		total: total,
		cb:    progress,
	}
	written, err := io.Copy(f, reader)
	if err != nil {
		_ = os.Remove(part)
		return "", fmt.Errorf("写入启动器更新失败: %w", err)
	}
	if written != m.Size {
		_ = os.Remove(part)
		return "", fmt.Errorf("启动器更新包大小不符：期望 %d，实际 %d", m.Size, written)
	}
	sum := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(sum, m.SHA256) {
		_ = os.Remove(part)
		return "", errors.New("启动器更新包校验失败（sha256 不符）")
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(part)
		return "", err
	}
	if err := os.Remove(final); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(part)
		return "", fmt.Errorf("清理旧文件失败: %w", err)
	}
	if err := os.Rename(part, final); err != nil {
		return "", fmt.Errorf("保存启动器更新失败: %w", err)
	}
	return final, nil
}

func downloadSegmented(ctx context.Context, client *http.Client, rawURL, part string, size int64, connections int, progress func(done, total int64)) error {
	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("创建下载文件失败: %w", err)
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		_ = os.Remove(part)
		return err
	}
	var reported atomic.Int64
	err = fetch.Segmented(ctx, client, rawURL, f, size, connections, func(n int64) {
		done := reported.Add(n)
		if progress != nil {
			progress(done, size)
		}
	})
	if err != nil {
		f.Close()
		_ = os.Remove(part)
		return fmt.Errorf("下载启动器更新失败: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(part)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(part)
		return err
	}
	return nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Cleanup 删除上次更新残留的临时文件。
func Cleanup(exePath string) {
	for _, suffix := range []string{".new", ".new.part", ".old"} {
		_ = os.Remove(exePath + suffix)
	}
}

// CompareVersions 比较形如 1.2.3 的版本号。
func CompareVersions(a, b string) int {
	as := versionParts(a)
	bs := versionParts(b)
	for i := 0; i < len(as) || i < len(bs); i++ {
		var av, bv int
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		if av != bv {
			if av > bv {
				return 1
			}
			return -1
		}
	}
	return 0
}

func versionParts(v string) []int {
	v = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(v), "v"))
	var out []int
	for _, part := range strings.Split(v, ".") {
		digits := strings.Builder{}
		for _, c := range part {
			if c >= '0' && c <= '9' {
				digits.WriteRune(c)
			}
		}
		n := 0
		if digits.Len() > 0 {
			fmt.Sscanf(digits.String(), "%d", &n)
		}
		out = append(out, n)
	}
	return out
}

func validSHA(sum string) bool {
	if len(sum) != 64 {
		return false
	}
	_, err := hex.DecodeString(sum)
	return err == nil
}

func resolveDownloadURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("启动器下载地址格式错误: %w", err)
	}
	if !u.IsAbs() {
		base, err := url.Parse(ManifestURL)
		if err != nil {
			return "", err
		}
		u = base.ResolveReference(u)
	}
	if u.Scheme != "https" {
		return "", errors.New("启动器下载地址必须使用 HTTPS")
	}
	if u.Host == "" {
		return "", errors.New("启动器下载地址缺少主机名")
	}
	return u.String(), nil
}

type progressReader struct {
	r     io.Reader
	done  int64
	total int64
	cb    func(done, total int64)
	last  time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if p.cb != nil && (p.done == p.total || time.Since(p.last) >= 100*time.Millisecond) {
		p.last = time.Now()
		p.cb(p.done, p.total)
	}
	return n, err
}
