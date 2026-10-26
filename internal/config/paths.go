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

// AccountsFile 返回 DPAPI 加密的账号文件路径。
func AccountsFile() string {
	return filepath.Join(AppDir(), "accounts.dat")
}

// DefaultInstanceDir 返回启动器所在目录下的 .minecraft，便于整包便携分发。
func DefaultInstanceDir(exeDir string) string {
	return filepath.Join(filepath.Clean(exeDir), ".minecraft")
}
