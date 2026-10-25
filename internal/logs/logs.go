// Package logs 读取游戏与启动器日志、收集崩溃报告并导出诊断包。
package logs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Tail 是日志尾部读取结果。
type Tail struct {
	Path      string
	Size      int64
	ModTime   time.Time
	Lines     []string
	Truncated bool
}

var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

// TailFile 读取文件尾部，最多 maxBytes 字节、maxLines 行。
func TailFile(path string, maxLines int, maxBytes int64) (Tail, error) {
	f, err := os.Open(path)
	if err != nil {
		return Tail{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Tail{}, err
	}
	size := info.Size()
	start := int64(0)
	if maxBytes > 0 && size > maxBytes {
		start = size - maxBytes
	}
	if start > 0 {
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return Tail{}, err
		}
	}
	const maxRead = 8 << 20
	data, err := io.ReadAll(io.LimitReader(f, maxRead))
	if err != nil {
		return Tail{}, err
	}
	truncated := start > 0
	if start > 0 {
		if idx := bytes.IndexByte(data, '\n'); idx >= 0 {
			data = data[idx+1:]
		} else {
			data = nil
		}
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	var lines []string
	if text != "" {
		lines = strings.Split(text, "\n")
	}
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
		truncated = true
	}
	for i := range lines {
		lines[i] = ansiPattern.ReplaceAllString(lines[i], "")
	}
	return Tail{
		Path:      path,
		Size:      size,
		ModTime:   info.ModTime(),
		Lines:     lines,
		Truncated: truncated,
	}, nil
}

// LatestLauncherLog 返回 logs 目录中最新的 launcher-*.log。
func LatestLauncherLog(logDir string) (string, error) {
	entries, err := os.ReadDir(logDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var best string
	var bestTime time.Time
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if !strings.HasPrefix(name, "launcher-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if best == "" || info.ModTime().After(bestTime) {
			best = filepath.Join(logDir, entry.Name())
			bestTime = info.ModTime()
		}
	}
	return best, nil
}

// LatestCrashReport 返回 crash-reports 目录中最新的崩溃报告。
func LatestCrashReport(crashDir string) (string, error) {
	entries, err := os.ReadDir(crashDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	type candidate struct {
		path string
		time time.Time
	}
	var list []candidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".txt") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		list = append(list, candidate{path: filepath.Join(crashDir, entry.Name()), time: info.ModTime()})
	}
	if len(list) == 0 {
		return "", nil
	}
	sort.Slice(list, func(i, j int) bool { return list[i].time.After(list[j].time) })
	return list[0].path, nil
}

// Options 是导出诊断包的参数。
type Options struct {
	GameDir string
	AppDir  string
}

// Export 生成诊断 zip，返回其路径。
func Export(opts Options) (string, error) {
	if strings.TrimSpace(opts.GameDir) == "" {
		return "", errors.New("实例目录不能为空")
	}
	if _, err := os.Stat(opts.GameDir); err != nil {
		return "", fmt.Errorf("实例目录不可访问: %w", err)
	}
	outDir := filepath.Join(opts.AppDir, "diagnostics")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("创建诊断目录失败: %w", err)
	}
	name := "cdr-diagnostics-" + time.Now().Format("20060102-150405") + ".zip"
	dest := filepath.Join(outDir, name)
	if err := writeArchive(dest, opts); err != nil {
		os.Remove(dest)
		return "", err
	}
	return dest, nil
}
