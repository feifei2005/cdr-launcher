package launcher

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"cdr-launcher/internal/fetch"
	"cdr-launcher/internal/netproxy"
)

// 固定 Temurin JRE 版本，不跟随 latest；升级由启动器版本显式携带。
const (
	TemurinVersion = "21.0.12.1+1"
	TemurinDirName = "temurin-21"

	temurinArchive = "OpenJDK21U-jre_x64_windows_hotspot_21.0.12.1_1.zip"
	TemurinSHA256  = "d35f31e712f0fcf6ac5a093edc90204fbff22f720ba3950bd09d331d5e621636"
	temurinSize    = 48999141
)

// TemurinURLs 按可用性排序：南京大学镜像优先（国内可直连），GitHub 作为兜底。
var TemurinURLs = []string{
	"https://mirror.nju.edu.cn/adoptium/21/jre/x64/windows/" + temurinArchive,
	"https://github.com/adoptium/temurin21-binaries/releases/download/jdk-21.0.12.1%2B1/" + temurinArchive,
}

// InstalledJavaPath 返回内置 Java 的 javaw.exe 路径。
func InstalledJavaPath(instanceDir string) string {
	return filepath.Join(instanceDir, "runtime", TemurinDirName, "bin", "javaw.exe")
}

// EnsureJava 确保实例目录下存在固定版本的 Temurin JRE，缺失时下载并校验安装。
// segments 是单个文件的并发分片连接数；progress 以 (已下载字节, 总字节) 形式上报，可为 nil。
func EnsureJava(ctx context.Context, instanceDir string, segments int, progress func(done, total int64)) (string, error) {
	target := InstalledJavaPath(instanceDir)
	if fileExists(target) {
		return target, nil
	}

	runtimeDir := filepath.Join(instanceDir, "runtime")
	downloads := filepath.Join(runtimeDir, "downloads")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		return "", fmt.Errorf("创建 runtime 目录失败: %w", err)
	}

	archive := filepath.Join(downloads, temurinArchive)
	if sum, err := fileSHA256(archive); err != nil || sum != TemurinSHA256 {
		os.Remove(archive)
		if err := fetchTemurinArchive(ctx, archive, segments, progress); err != nil {
			return "", fmt.Errorf("下载 Java 失败: %w", err)
		}
	}
	if progress != nil {
		progress(temurinSize, temurinSize)
	}

	tmp, err := os.MkdirTemp(runtimeDir, ".temurin-extract-*")
	if err != nil {
		return "", fmt.Errorf("创建解压目录失败: %w", err)
	}
	defer os.RemoveAll(tmp)
	if err := extractJavaZip(archive, tmp); err != nil {
		return "", fmt.Errorf("解压 Java 失败: %w", err)
	}
	if !fileExists(filepath.Join(tmp, "bin", "javaw.exe")) {
		return "", errors.New("Java 安装包结构异常：缺少 bin/javaw.exe")
	}

	dest := filepath.Join(runtimeDir, TemurinDirName)
	backup := dest + ".old"
	os.RemoveAll(backup)
	if _, err := os.Stat(dest); err == nil {
		if err := os.Rename(dest, backup); err != nil {
			return "", fmt.Errorf("替换旧 Java 失败: %w", err)
		}
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Rename(backup, dest)
		return "", fmt.Errorf("安装 Java 失败: %w", err)
	}
	os.RemoveAll(backup)
	return InstalledJavaPath(instanceDir), nil
}

// fetchTemurinArchive 依次尝试各下载源，以 SHA256 判定成功，失败则换源重试。
func fetchTemurinArchive(ctx context.Context, archive string, segments int, progress func(done, total int64)) error {
	var lastErr error
	for _, url := range TemurinURLs {
		if err := downloadFile(ctx, url, archive, temurinSize, segments, progress); err != nil {
			lastErr = err
			continue
		}
		if sum, err := fileSHA256(archive); err == nil && sum == TemurinSHA256 {
			return nil
		}
		lastErr = errors.New("SHA256 校验失败")
		os.Remove(archive)
		os.Remove(archive + ".part")
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的下载源")
	}
	return lastErr
}

// downloadFile 支持 HTTP Range 断点续传与多连接分片，成功后把 .part 改名为目标文件。
func downloadFile(ctx context.Context, url, dest string, expected int64, segments int, progress func(done, total int64)) error {
	part := dest + ".part"
	var offset int64
	if st, err := os.Stat(part); err == nil {
		offset = st.Size()
	}
	if expected > 0 && offset > expected {
		os.Remove(part)
		offset = 0
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	// 优先 IPv4：部分网络到 GitHub 的 IPv6 路由不可用，直接 v6 会长时间挂起。
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if network != "tcp" {
			return dialer.DialContext(ctx, network, addr)
		}
		if conn, err := dialer.DialContext(ctx, "tcp4", addr); err == nil {
			return conn, nil
		}
		return dialer.DialContext(ctx, network, addr)
	}
	netproxy.Apply(transport)
	client := &http.Client{Transport: transport}

	// 大文件优先多连接分片；服务端不支持 Range 时回退单连接续传。
	if offset == 0 && expected >= fetch.MinSegmentSize && segments > 1 {
		err := downloadSegmented(ctx, client, url, part, expected, segments, progress)
		if err == nil {
			return os.Rename(part, dest)
		}
		_ = os.Remove(part)
		if !errors.Is(err, fetch.ErrRangeUnsupported) {
			return err
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "cdr-launcher")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		offset = 0
	case http.StatusPartialContent:
	case http.StatusRequestedRangeNotSatisfiable:
		os.Remove(part)
		return downloadFile(ctx, url, dest, expected, segments, progress)
	default:
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	total := expected
	if resp.ContentLength > 0 {
		total = offset + resp.ContentLength
	}

	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			f.Close()
			return err
		}
	}

	writer := &progressWriter{w: f, done: offset, total: total, cb: progress}
	_, copyErr := io.Copy(writer, resp.Body)
	if copyErr == nil {
		copyErr = f.Sync()
	}
	closeErr := f.Close()
	writer.flush()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if total > 0 && writer.done != total {
		return fmt.Errorf("下载不完整: %d/%d 字节", writer.done, total)
	}
	return os.Rename(part, dest)
}

// downloadSegmented 用多条 Range 连接并发下载归档到 .part。
func downloadSegmented(ctx context.Context, client *http.Client, url, part string, size int64, connections int, progress func(done, total int64)) error {
	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		_ = os.Remove(part)
		return err
	}
	var reported atomic.Int64
	err = fetch.Segmented(ctx, client, url, f, size, connections, func(n int64) {
		done := reported.Add(n)
		if progress != nil {
			progress(done, size)
		}
	})
	if err != nil {
		f.Close()
		_ = os.Remove(part)
		return err
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

type progressWriter struct {
	w     io.Writer
	done  int64
	total int64
	cb    func(done, total int64)
	last  time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.done += int64(n)
	if p.cb != nil && (time.Since(p.last) > 200*time.Millisecond) {
		p.last = time.Now()
		p.cb(p.done, p.total)
	}
	return n, err
}

func (p *progressWriter) flush() {
	if p.cb != nil {
		p.cb(p.done, p.total)
	}
}

func fileSHA256(file string) (string, error) {
	f, err := os.Open(file)
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

// extractJavaZip 解压 Adoptium zip，剥离单一顶层目录，拒绝越界路径与符号链接。
func extractJavaZip(archive, dest string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()

	root := zipCommonRoot(zr.File)
	for _, f := range zr.File {
		name := strings.TrimPrefix(strings.ReplaceAll(f.Name, `\`, "/"), root)
		name = strings.TrimPrefix(name, "/")
		if name == "" || strings.HasSuffix(name, "/") {
			continue
		}
		if !safeZipPath(name) {
			return fmt.Errorf("压缩包含非法路径: %s", f.Name)
		}
		if !f.Mode().IsRegular() {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := writeZipFile(f, target); err != nil {
			return err
		}
	}
	return nil
}

func writeZipFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	perm := f.Mode().Perm()
	if perm == 0 {
		perm = 0o644
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func zipCommonRoot(files []*zip.File) string {
	var root string
	for _, f := range files {
		name := strings.ReplaceAll(f.Name, `\`, "/")
		if name == "" || strings.HasSuffix(name, "/") {
			// 目录条目（含顶层 root 自身）不参与根目录判定。
			continue
		}
		first, _, nested := strings.Cut(name, "/")
		if !nested {
			return ""
		}
		if root == "" {
			root = first
		} else if root != first {
			return ""
		}
	}
	if root == "" {
		return ""
	}
	return root + "/"
}

func safeZipPath(name string) bool {
	if path.IsAbs(name) || strings.Contains(name, ":") {
		return false
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}
