//go:build !windows

package selfupdate

import "errors"

// Apply 仅在 Windows 上支持原地替换。
func Apply(exePath, newPath string) error {
	return errors.New("当前平台不支持启动器自更新")
}
