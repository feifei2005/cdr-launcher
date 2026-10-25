package launcher

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PrepareNatives 把当前平台的原生库解压到 versions/<id>/natives-<os>-<arch>，
// 先解压到临时目录再整体替换，避免残留旧 DLL。
func PrepareNatives(instanceDir string, v *Version, host OS, features map[string]bool) (string, error) {
	dest := filepath.Join(instanceDir, "versions", v.ID, "natives-"+host.Name+"-"+host.Arch)
	jars := v.NativeJars(instanceDir, host, features)
	if len(jars) == 0 {
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return "", fmt.Errorf("创建 natives 目录失败: %w", err)
		}
		return dest, nil
	}

	tmp := dest + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return "", fmt.Errorf("清理临时 natives 目录失败: %w", err)
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return "", fmt.Errorf("创建临时 natives 目录失败: %w", err)
	}
	for _, jar := range jars {
		if err := extractNativeJar(jar, tmp); err != nil {
			os.RemoveAll(tmp)
			return "", err
		}
	}

	backup := dest + ".old"
	os.RemoveAll(backup)
	if isDir(dest) {
		if err := os.Rename(dest, backup); err != nil {
			os.RemoveAll(tmp)
			return "", fmt.Errorf("替换旧 natives 失败: %w", err)
		}
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Rename(backup, dest)
		os.RemoveAll(tmp)
		return "", fmt.Errorf("安装 natives 失败: %w", err)
	}
	os.RemoveAll(backup)
	return dest, nil
}

func extractNativeJar(jar, dest string) error {
	zr, err := zip.OpenReader(jar)
	if err != nil {
		return fmt.Errorf("读取原生库失败: %w", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, `\`, "/")
		if f.FileInfo().IsDir() || strings.HasPrefix(name, "META-INF/") {
			continue
		}
		if !f.Mode().IsRegular() || !safeZipPath(name) {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := writeZipFile(f, target); err != nil {
			return fmt.Errorf("解压 %s 失败: %w", filepath.Base(jar), err)
		}
	}
	return nil
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
