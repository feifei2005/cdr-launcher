package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AuthlibInjectorFile 是内置注入器的文件名。
const AuthlibInjectorFile = "authlib-injector.jar"

// authlibMetaSources 官方源与 BMCLAPI 镜像的版本元数据，按顺序尝试。
var authlibMetaSources = []string{
	"https://authlib-injector.yushi.moe/artifact/latest.json",
	"https://bmclapi2.bangbang93.com/mirrors/authlib-injector/artifact/latest.json",
}

const minAuthlibInjectorSize = 64 << 10

type authlibRelease struct {
	Version     string `json:"version"`
	DownloadURL string `json:"download_url"`
	Checksums   struct {
		SHA256 string `json:"sha256"`
	} `json:"checksums"`
}

type authlibTarget struct {
	url    string
	sha256 string
}

// EnsureAuthlibInjector 确保 dir 下存在可用的 authlib-injector.jar，返回其路径。
func EnsureAuthlibInjector(ctx context.Context, dir string, progress func(done, total int64)) (string, error) {
	path := filepath.Join(dir, AuthlibInjectorFile)
	if info, err := os.Stat(path); err == nil && info.Size() >= minAuthlibInjectorSize && isJar(path) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建注入器目录失败: %w", err)
	}

	targets := authlibTargets(ctx)
	if len(targets) == 0 {
		return "", errors.New("下载 authlib-injector 失败: 无法获取版本信息")
	}
	var lastErr error
	for _, target := range targets {
		if err := downloadAuthlibInjector(ctx, target, path, progress); err != nil {
			lastErr = err
			continue
		}
		return path, nil
	}
	return "", fmt.Errorf("下载 authlib-injector 失败: %w", lastErr)
}

// AuthlibInjectorAgent 返回启动参数中的 javaagent 值。
func AuthlibInjectorAgent(jarPath, serverURL string) string {
	return "-javaagent:" + jarPath + "=" + serverURL
}

func authlibTargets(ctx context.Context) []authlibTarget {
	var targets []authlibTarget
	for _, metaURL := range authlibMetaSources {
		status, raw, err := getJSON(ctx, metaURL, nil)
		if err != nil || status != http.StatusOK {
			continue
		}
		var release authlibRelease
		if err := json.Unmarshal(raw, &release); err != nil || release.DownloadURL == "" {
			continue
		}
		targets = append(targets, authlibTarget{url: release.DownloadURL, sha256: release.Checksums.SHA256})
	}
	return targets
}

func downloadAuthlibInjector(ctx context.Context, target authlibTarget, path string, progress func(done, total int64)) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", target.url, resp.StatusCode)
	}

	tmp := path + ".part"
	file, err := os.Create(tmp)
	if err != nil {
		return err
	}
	writer := &progressWriter{writer: file, total: resp.ContentLength, progress: progress}
	_, copyErr := io.Copy(writer, resp.Body)
	closeErr := file.Close()
	if copyErr != nil {
		os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		os.Remove(tmp)
		return closeErr
	}
	if err := verifyJar(tmp, target.sha256); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func verifyJar(path, expectedSHA256 string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() < minAuthlibInjectorSize {
		return errors.New("下载内容过小，不是有效的 jar")
	}
	if expectedSHA256 != "" {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		file.Close()
		if copyErr != nil {
			return copyErr
		}
		if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, expectedSHA256) {
			return fmt.Errorf("校验和不匹配: 期望 %s，实际 %s", expectedSHA256, got)
		}
	}
	if !isJar(path) {
		return errors.New("下载内容不是有效的 jar")
	}
	return nil
}

type progressWriter struct {
	writer   io.Writer
	total    int64
	done     int64
	progress func(done, total int64)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.done += int64(n)
	if w.progress != nil {
		w.progress(w.done, w.total)
	}
	return n, err
}

func isJar(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	header := make([]byte, 4)
	if _, err := io.ReadFull(file, header); err != nil {
		return false
	}
	return header[0] == 'P' && header[1] == 'K' && header[2] == 3 && header[3] == 4
}
