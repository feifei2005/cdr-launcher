package logs

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const maxCaptureBytes = 4 << 20

func writeArchive(dest string, opts Options) error {
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("创建诊断包失败: %w", err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)

	gameDir := opts.GameDir
	logDir := filepath.Join(gameDir, "logs")
	crashDir := filepath.Join(gameDir, "crash-reports")

	add := func(name string, data []byte) error {
		if len(data) == 0 {
			return nil
		}
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:     name,
			Method:   zip.Deflate,
			Modified: time.Now(),
		})
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}

	modsTotal, modsEnabled := countMods(filepath.Join(gameDir, "mods"))
	summary := fmt.Sprintf(
		"Create: Delight Remake 启动器诊断包\n"+
			"生成时间: %s\n"+
			"操作系统: %s/%s\n"+
			"Go: %s\n"+
			"实例目录: %s\n"+
			"启动器目录: %s\n"+
			"模组: 共 %d 个，启用 %d 个\n",
		time.Now().Format("2006-01-02 15:04:05"),
		runtime.GOOS, runtime.GOARCH, runtime.Version(),
		gameDir, opts.AppDir, modsTotal, modsEnabled,
	)
	if err := add("summary.txt", []byte(summary)); err != nil {
		return err
	}

	readCapped := func(path string, limit int64) []byte {
		if path == "" {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return nil
		}
		size := info.Size()
		if size > limit {
			size = limit
		}
		buf := make([]byte, size)
		n, _ := f.ReadAt(buf, 0)
		buf = buf[:n]
		if info.Size() > limit {
			buf = append(buf, []byte("\n…（文件过大，仅保留开头部分）\n")...)
		}
		return buf
	}

	if raw, err := os.ReadFile(filepath.Join(opts.AppDir, "settings.json")); err == nil {
		_ = add("settings.json", []byte(redactSettings(raw)))
	}
	if raw, err := os.ReadFile(filepath.Join(gameDir, ".cdrlauncher", "installed.json")); err == nil {
		_ = add("installed.json", truncate(raw, maxCaptureBytes))
	}

	if tail, err := TailFile(filepath.Join(logDir, "latest.log"), 4000, maxCaptureBytes); err == nil {
		body := strings.Join(tail.Lines, "\n")
		if tail.Truncated {
			body = "…（仅保留日志尾部）\n" + body
		}
		_ = add("logs/latest.log", []byte(body))
	}
	if path, err := LatestLauncherLog(logDir); err == nil && path != "" {
		if tail, tailErr := TailFile(path, 3000, maxCaptureBytes); tailErr == nil {
			body := strings.Join(tail.Lines, "\n")
			if tail.Truncated {
				body = "…（仅保留日志尾部）\n" + body
			}
			_ = add("logs/launcher.log", []byte(fmt.Sprintf("# %s\n%s", filepath.Base(path), body)))
		}
	}
	if path, err := LatestCrashReport(crashDir); err == nil && path != "" {
		if raw := readCapped(path, maxCaptureBytes); raw != nil {
			_ = add("logs/crash-report.txt", []byte(fmt.Sprintf("# %s\n%s", filepath.Base(path), raw)))
		}
	}
	_ = add("mods.txt", []byte(modsListing(filepath.Join(gameDir, "mods"))))

	if err := zw.Close(); err != nil {
		return fmt.Errorf("写入诊断包失败: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("刷新诊断包失败: %w", err)
	}
	return nil
}

func truncate(raw []byte, limit int64) []byte {
	if int64(len(raw)) <= limit {
		return raw
	}
	return append(append([]byte{}, raw[:limit]...), []byte("\n…（内容过长已截断）\n")...)
}

// redactSettings 移除更新地址中的用户名密码，避免诊断包泄露凭据。
func redactSettings(raw []byte) string {
	text := string(raw)
	for _, scheme := range []string{"http://", "https://"} {
		from := 0
		for {
			idx := strings.Index(text[from:], scheme)
			if idx < 0 {
				break
			}
			start := from + idx + len(scheme)
			end := start
			for end < len(text) && text[end] != '/' && text[end] != '"' && text[end] != '\\' && text[end] != ' ' {
				end++
			}
			if at := strings.Index(text[start:end], "@"); at >= 0 {
				text = text[:start] + "***" + text[start+at:]
			}
			from = end
			if from >= len(text) {
				break
			}
		}
	}
	return text
}

func countMods(dir string) (total, enabled int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		switch {
		case strings.HasSuffix(name, ".jar.disabled"):
			total++
		case strings.HasSuffix(name, ".jar"):
			total++
			enabled++
		}
	}
	return total, enabled
}

func modsListing(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "（无法读取 mods 目录）\n"
	}
	type row struct {
		name    string
		enabled bool
		size    int64
	}
	var rows []row
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		lower := strings.ToLower(name)
		enabled := strings.HasSuffix(lower, ".jar")
		if !enabled && !strings.HasSuffix(lower, ".jar.disabled") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		rows = append(rows, row{name: name, enabled: enabled, size: info.Size()})
	}
	sort.Slice(rows, func(i, j int) bool { return strings.ToLower(rows[i].name) < strings.ToLower(rows[j].name) })
	var b strings.Builder
	b.WriteString("状态 | 大小 | 文件名\n")
	for _, r := range rows {
		mark := "[禁用]"
		if r.enabled {
			mark = "[启用]"
		}
		fmt.Fprintf(&b, "%s %10d  %s\n", mark, r.size, r.name)
	}
	return b.String()
}
