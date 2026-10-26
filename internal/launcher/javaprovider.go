package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cdr-launcher/internal/netproxy"
)

// 可选的内置 Java 来源；auto 表示优先使用已安装版本，缺失时下载 Zulu。
const (
	JavaProviderAuto      = "auto"
	JavaProviderZulu      = "zulu"
	JavaProviderGraalVM   = "graalvm"
	JavaProviderMicrosoft = "microsoft"
)

const zuluAPI = "https://api.azul.com/metadata/v1/zulu/packages/?java_version=21&os=windows&arch=x64&java_package_type=jdk&archive_type=zip&release_status=ga&latest=true"

// NormalizeJavaProvider 把未知取值收敛为 auto。
func NormalizeJavaProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case JavaProviderZulu:
		return JavaProviderZulu
	case JavaProviderGraalVM:
		return JavaProviderGraalVM
	case JavaProviderMicrosoft:
		return JavaProviderMicrosoft
	default:
		return JavaProviderAuto
	}
}

// JavaProviderDir 返回该来源在 runtime 下的目录名。
func JavaProviderDir(provider string) string {
	switch NormalizeJavaProvider(provider) {
	case JavaProviderGraalVM:
		return "graalvm-21"
	case JavaProviderMicrosoft:
		return "microsoft-21"
	default:
		return "zulu-21"
	}
}

type javaSource struct {
	url      string
	sha256   string
	filename string
}

// EnsureJavaProvider 确保 runtime 下存在所选来源的 Java 21 JDK，缺失时下载安装。
func EnsureJavaProvider(ctx context.Context, instanceDir, provider string, segments int, progress func(done, total int64)) (string, error) {
	dir := JavaProviderDir(provider)
	target := filepath.Join(instanceDir, "runtime", dir, "bin", "javaw.exe")
	if fileExists(target) {
		return target, nil
	}

	client := netproxy.NewClient(0)
	src, size, err := resolveJavaSource(ctx, client, provider)
	if err != nil {
		return "", err
	}

	downloads := filepath.Join(instanceDir, "runtime", "downloads")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		return "", fmt.Errorf("创建 runtime 目录失败: %w", err)
	}
	archive := filepath.Join(downloads, src.filename)
	if st, err := os.Stat(archive); err != nil || (size > 0 && st.Size() != size) {
		os.Remove(archive)
		os.Remove(archive + ".part")
		if err := downloadFile(ctx, src.url, archive, size, segments, progress); err != nil {
			return "", fmt.Errorf("下载 Java 失败: %w", err)
		}
	}
	if src.sha256 != "" {
		sum, err := fileSHA256(archive)
		if err != nil || !strings.EqualFold(sum, src.sha256) {
			os.Remove(archive)
			return "", errors.New("Java 安装包 SHA256 校验失败")
		}
	}
	if progress != nil && size > 0 {
		progress(size, size)
	}

	tmp, err := os.MkdirTemp(filepath.Join(instanceDir, "runtime"), ".java-extract-*")
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

	dest := filepath.Join(instanceDir, "runtime", dir)
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
	return target, nil
}

func resolveJavaSource(ctx context.Context, client *http.Client, provider string) (javaSource, int64, error) {
	switch NormalizeJavaProvider(provider) {
	case JavaProviderGraalVM:
		return probeSource(ctx, client, javaSource{
			url:      "https://download.oracle.com/graalvm/21/latest/graalvm-jdk-21_windows-x64_bin.zip",
			filename: "graalvm-jdk-21_windows-x64_bin.zip",
		})
	case JavaProviderMicrosoft:
		return probeSource(ctx, client, javaSource{
			url:      "https://aka.ms/download-jdk/microsoft-jdk-21-windows-x64.zip",
			filename: "microsoft-jdk-21-windows-x64.zip",
		})
	default:
		src, err := zuluSource(ctx, client)
		if err != nil {
			return javaSource{}, 0, err
		}
		size, err := remoteSize(ctx, client, src.url)
		if err != nil {
			return javaSource{}, 0, err
		}
		return src, size, nil
	}
}

func zuluSource(ctx context.Context, client *http.Client) (javaSource, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, zuluAPI, nil)
	if err != nil {
		return javaSource{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "CDRLauncher/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return javaSource{}, fmt.Errorf("查询 Zulu 版本失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return javaSource{}, fmt.Errorf("查询 Zulu 版本失败: HTTP %s", resp.Status)
	}
	var list []struct {
		Name       string `json:"name"`
		Download   string `json:"download_url"`
		SHA256Hash string `json:"sha256_hash"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return javaSource{}, fmt.Errorf("解析 Zulu 版本信息失败: %w", err)
	}
	for _, item := range list {
		if item.Download == "" {
			continue
		}
		name := item.Name
		if name == "" {
			name = filepath.Base(strings.Split(item.Download, "?")[0])
		}
		return javaSource{url: item.Download, sha256: item.SHA256Hash, filename: name}, nil
	}
	return javaSource{}, errors.New("未找到可用的 Zulu 21 JDK")
}

func probeSource(ctx context.Context, client *http.Client, src javaSource) (javaSource, int64, error) {
	size, err := remoteSize(ctx, client, src.url)
	if err != nil {
		return javaSource{}, 0, err
	}
	return src, size, nil
}

// remoteSize 通过 Range 请求读取文件总大小；部分 CDN 不响应 HEAD，因此以 Range 为主。
func remoteSize(ctx context.Context, client *http.Client, rawURL string) (int64, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("User-Agent", "CDRLauncher/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("探测下载大小失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		if resp.ContentLength > 0 {
			return resp.ContentLength, nil
		}
		return 0, fmt.Errorf("探测下载大小失败: HTTP %s", resp.Status)
	}
	value := resp.Header.Get("Content-Range")
	if idx := strings.LastIndexByte(value, '/'); idx >= 0 {
		if total, err := strconv.ParseInt(value[idx+1:], 10, 64); err == nil {
			return total, nil
		}
	}
	return 0, errors.New("无法解析 Content-Range")
}
