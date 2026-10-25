//go:build !windows

package launcher

import "os/exec"

func hideWindow(*exec.Cmd) {}
