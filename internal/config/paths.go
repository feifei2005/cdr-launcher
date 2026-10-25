package config

import (
	"os"
	"path/filepath"
)

const appDirName = "CDRLauncher"

// AppDir 返回启动器配置目录：%APPDATA%\CDRLauncher。
func AppDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, appDirName)
	}
	return appDirName
}

// SettingsFile 返回设置文件路径。
func SettingsFile() string {
	return filepath.Join(AppDir(), "settings.json")
}

// CacheDir 返回启动器缓存目录。
func CacheDir() string {
	return filepath.Join(AppDir(), "cache")
}

// DefaultInstanceDir 选择整合包实例目录：优先启动器所在目录及上层的 .minecraft / instance，
// 其次 %APPDATA%\.minecraft，最后回退到启动器旁的 .minecraft。
func DefaultInstanceDir(exeDir string) string {
	dir := filepath.Clean(exeDir)
	for i := 0; i < 4; i++ {
		for _, name := range []string{".minecraft", "instance"} {
			candidate := filepath.Join(dir, name)
			if isDir(candidate) {
				return candidate
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if base, err := os.UserConfigDir(); err == nil {
		if dir := filepath.Join(base, ".minecraft"); isDir(dir) {
			return dir
		}
	}
	return filepath.Join(exeDir, ".minecraft")
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
