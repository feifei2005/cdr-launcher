//go:build windows

package selfupdate

import (
	"fmt"
	"os"
)

// Apply 用下载好的新程序替换当前 exe；旧文件留待下次启动清理。
func Apply(exePath, newPath string) error {
	oldPath := exePath + ".old"
	_ = os.Remove(oldPath)
	if err := os.Rename(exePath, oldPath); err != nil {
		return fmt.Errorf("无法重命名当前程序: %w", err)
	}
	if err := os.Rename(newPath, exePath); err != nil {
		_ = os.Rename(oldPath, exePath)
		return fmt.Errorf("替换启动器失败: %w", err)
	}
	return nil
}
