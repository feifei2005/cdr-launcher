package launcher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// GameExit 是游戏进程退出结果。
type GameExit struct {
	Code     int
	Duration time.Duration
	Err      error
}

// GameProcess 持有运行中的游戏进程及其日志文件。
type GameProcess struct {
	cmd     *exec.Cmd
	log     *os.File
	LogPath string
	started time.Time
}

// StartGameProcess 用 javaw 启动游戏，stdout/stderr 写入 logs/launcher-<ts>.log。
func StartGameProcess(spec *LaunchSpec, logDir string) (*GameProcess, error) {
	if spec == nil || spec.JavaPath == "" {
		return nil, errors.New("启动参数不完整")
	}
	if _, err := os.Stat(spec.JavaPath); err != nil {
		return nil, fmt.Errorf("Java 不存在: %w", err)
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", err)
	}
	name := "launcher-" + time.Now().Format("20060102-150405") + ".log"
	logPath := filepath.Join(logDir, name)
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("创建日志文件失败: %w", err)
	}

	cmd := exec.Command(spec.JavaPath, spec.Args...)
	cmd.Dir = spec.WorkDir
	hideWindow(cmd)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("启动游戏失败: %w", err)
	}
	return &GameProcess{cmd: cmd, log: logFile, LogPath: logPath, started: time.Now()}, nil
}

// Wait 等待游戏退出并关闭日志文件。
func (p *GameProcess) Wait() GameExit {
	err := p.cmd.Wait()
	duration := time.Since(p.started)
	p.log.Close()

	code := 0
	if err != nil {
		code = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
	}
	return GameExit{Code: code, Duration: duration, Err: err}
}

// Kill 强制结束游戏进程。
func (p *GameProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}
