package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"cdr-launcher/internal/config"
	"cdr-launcher/internal/launcher"
	"cdr-launcher/internal/logs"
	"cdr-launcher/internal/mods"
	"cdr-launcher/internal/updater"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const packVersionID = "Create-Delight-Remake"

// App 是暴露给前端的启动器后端。
type App struct {
	ctx          context.Context
	mu           sync.Mutex
	exeDir       string
	settingsPath string
	cfg          config.Settings
	java         []launcher.JavaInfo
	javaErr      string
	javaScanning bool
	running      bool
	proc         *launcher.GameProcess
	updating     bool
	updateCancel context.CancelFunc
	rollingBack  bool
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	exeDir := "."
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	a.settingsPath = config.SettingsFile()
	cfg, err := config.Load(a.settingsPath, exeDir)
	a.mu.Lock()
	a.exeDir = exeDir
	a.cfg = cfg
	a.mu.Unlock()
	if err != nil {
		a.emit("launcher:warning", err.Error())
	}
	gameDir := launcher.GameDir(cfg.InstanceDir, packVersionID)
	if err := updater.Recover(gameDir); err != nil {
		a.emit("launcher:warning", "回滚未完成的更新失败: "+err.Error())
	}
	go func() { _ = a.refreshJava() }()
}

// ---------- 前端视图 ----------

type SettingsView struct {
	InstanceDir string `json:"instanceDir"`
	JavaPath    string `json:"javaPath"`
	PlayerName  string `json:"playerName"`
	MemoryMB    int    `json:"memoryMb"`
	Channel     string `json:"channel"`
	UpdateURL   string `json:"updateUrl"`
}

type JavaView struct {
	Path       string `json:"path"`
	Version    string `json:"version"`
	Major      int    `json:"major"`
	Source     string `json:"source"`
	Compatible bool   `json:"compatible"`
}

type InstanceView struct {
	Path         string `json:"path"`
	GameDir      string `json:"gameDir"`
	VersionID    string `json:"versionId"`
	MCVersion    string `json:"mcVersion"`
	ForgeVersion string `json:"forgeVersion"`
	ModCount     int    `json:"modCount"`
	PackVersion  string `json:"packVersion"`
	Error        string `json:"error,omitempty"`
}

type StateView struct {
	Settings     SettingsView `json:"settings"`
	Java         []JavaView   `json:"java"`
	JavaScanning bool         `json:"javaScanning"`
	JavaError    string       `json:"javaError,omitempty"`
	Instance     InstanceView `json:"instance"`
	Running      bool         `json:"running"`
	Updating     bool         `json:"updating"`
	RollingBack  bool         `json:"rollingBack"`
}

type SettingsInput struct {
	InstanceDir string `json:"instanceDir"`
	JavaPath    string `json:"javaPath"`
	PlayerName  string `json:"playerName"`
	MemoryMB    int    `json:"memoryMb"`
	UpdateURL   string `json:"updateUrl"`
}

type ModView struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Size    int64  `json:"size"`
	Sha256  string `json:"sha256,omitempty"`
}

type LogView struct {
	Kind       string   `json:"kind"`
	Path       string   `json:"path"`
	Size       int64    `json:"size"`
	ModifiedAt string   `json:"modifiedAt"`
	Lines      []string `json:"lines"`
	Truncated  bool     `json:"truncated"`
	Message    string   `json:"message,omitempty"`
}

type DiagnosticsView struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

func (a *App) GetState() StateView {
	a.mu.Lock()
	cfg := a.cfg
	java := append([]launcher.JavaInfo(nil), a.java...)
	scanning, javaErr, running, updating, rollingBack := a.javaScanning, a.javaErr, a.running, a.updating, a.rollingBack
	a.mu.Unlock()

	state := StateView{
		Settings: SettingsView{
			InstanceDir: cfg.InstanceDir,
			JavaPath:    cfg.JavaPath,
			PlayerName:  cfg.PlayerName,
			MemoryMB:    cfg.MemoryMB,
			Channel:     cfg.Channel,
			UpdateURL:   cfg.UpdateURL,
		},
		Java:         make([]JavaView, 0, len(java)),
		JavaScanning: scanning,
		JavaError:    javaErr,
		Running:      running,
		Updating:     updating,
		RollingBack:  rollingBack,
	}
	for _, j := range java {
		state.Java = append(state.Java, JavaView{
			Path:       j.Path,
			Version:    j.Version,
			Major:      j.Major,
			Source:     j.Source,
			Compatible: j.Compatible(),
		})
	}
	state.Instance = a.instanceView(cfg.InstanceDir, cfg.PackVersion)
	return state
}

func (a *App) instanceView(instanceDir, fallbackVersion string) InstanceView {
	gameDir := launcher.GameDir(instanceDir, packVersionID)
	view := InstanceView{Path: instanceDir, GameDir: gameDir, VersionID: packVersionID}
	if idx, err := updater.LoadIndex(gameDir); err == nil && idx.Version != "" {
		view.PackVersion = idx.Version
	} else {
		view.PackVersion = fallbackVersion
	}
	v, err := launcher.LoadVersion(instanceDir, packVersionID)
	if err != nil {
		view.Error = err.Error()
		return view
	}
	view.MCVersion, view.ForgeVersion = versionMeta(v)
	view.ModCount = countMods(filepath.Join(gameDir, "mods"))
	return view
}

func versionMeta(v *launcher.Version) (mc, forge string) {
	var flat []string
	for _, arg := range v.Arguments.Game {
		flat = append(flat, arg.Value...)
	}
	for i, value := range flat {
		switch value {
		case "--fml.mcVersion":
			if i+1 < len(flat) {
				mc = flat[i+1]
			}
		case "--fml.forgeVersion":
			if i+1 < len(flat) {
				forge = flat[i+1]
			}
		}
	}
	return mc, forge
}

func countMods(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if strings.HasSuffix(name, ".jar") || strings.HasSuffix(name, ".jar.disabled") {
			count++
		}
	}
	return count
}

// ---------- 设置与 Java ----------

func (a *App) SaveSettings(in SettingsInput) error {
	a.mu.Lock()
	if a.updating {
		a.mu.Unlock()
		return errors.New("更新进行中，无法修改设置")
	}
	s := a.cfg
	a.mu.Unlock()

	s.InstanceDir = in.InstanceDir
	s.JavaPath = strings.TrimSpace(in.JavaPath)
	s.PlayerName = in.PlayerName
	s.MemoryMB = in.MemoryMB
	s.UpdateURL = in.UpdateURL
	s.Normalize(a.exeDir)
	if err := s.Validate(); err != nil {
		return err
	}
	if s.JavaPath != "" {
		if _, err := os.Stat(s.JavaPath); err != nil {
			return fmt.Errorf("Java 路径不存在: %s", s.JavaPath)
		}
	}
	if err := config.Save(a.settingsPath, s); err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg = s
	a.mu.Unlock()
	go func() { _ = a.refreshJava() }()
	a.emit("launcher:changed", nil)
	return nil
}

func (a *App) RescanJava() error {
	return a.refreshJava()
}

func (a *App) refreshJava() error {
	a.mu.Lock()
	if a.javaScanning {
		a.mu.Unlock()
		return nil
	}
	a.javaScanning = true
	cfg := a.cfg
	a.mu.Unlock()
	a.emit("launcher:changed", nil)

	list, err := launcher.FindJava(a.ctx, cfg.JavaPath, cfg.InstanceDir, config.AppDir())

	a.mu.Lock()
	a.java = list
	a.javaScanning = false
	if err != nil {
		a.javaErr = err.Error()
	} else {
		a.javaErr = ""
	}
	a.mu.Unlock()

	a.emit("launcher:changed", nil)
	return err
}

// DownloadJava 下载并安装内置 Temurin 17，供设置页手动触发。
func (a *App) DownloadJava() error {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	_, err := launcher.EnsureJava(a.ctx, cfg.InstanceDir, a.javaProgress)
	if err != nil {
		return err
	}
	return a.refreshJava()
}

func (a *App) javaProgress(done, total int64) {
	a.emit("launcher:java-progress", map[string]int64{"done": done, "total": total})
}

// ---------- 启动 ----------

func (a *App) StartGame() error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return errors.New("游戏已在运行")
	}
	if a.updating {
		a.mu.Unlock()
		return errors.New("更新进行中，无法启动游戏")
	}
	if a.rollingBack {
		a.mu.Unlock()
		return errors.New("版本回滚进行中，无法启动游戏")
	}
	cfg := a.cfg
	a.mu.Unlock()

	if err := cfg.Validate(); err != nil {
		return err
	}
	gameDir := launcher.GameDir(cfg.InstanceDir, packVersionID)
	if err := updater.Recover(gameDir); err != nil {
		return fmt.Errorf("回滚未完成的更新失败: %w", err)
	}
	player, err := launcher.OfflinePlayer(cfg.PlayerName)
	if err != nil {
		return err
	}

	javaPath, err := a.pickJava(cfg)
	if err != nil {
		return err
	}
	v, err := launcher.LoadVersion(cfg.InstanceDir, packVersionID)
	if err != nil {
		return err
	}
	spec, err := launcher.BuildLaunch(v, cfg.InstanceDir, gameDir, javaPath, player, cfg.MemoryMB)
	if err != nil {
		return err
	}

	proc, err := launcher.StartGameProcess(spec, filepath.Join(gameDir, "logs"))
	if err != nil {
		return err
	}

	a.mu.Lock()
	a.running = true
	a.proc = proc
	a.mu.Unlock()
	a.emit("launcher:changed", nil)

	go func() {
		exit := proc.Wait()
		a.mu.Lock()
		a.running = false
		a.proc = nil
		a.mu.Unlock()
		a.emit("game:exited", map[string]any{
			"code":       exit.Code,
			"durationMs": exit.Duration.Milliseconds(),
			"logPath":    proc.LogPath,
		})
		a.emit("launcher:changed", nil)
	}()
	return nil
}

// pickJava 优先使用设置指定的 Java；自动模式只接受 17，缺失则下载内置版本。
func (a *App) pickJava(cfg config.Settings) (string, error) {
	if cfg.JavaPath != "" {
		info, err := launcher.ProbeJava(a.ctx, cfg.JavaPath)
		if err != nil {
			return "", fmt.Errorf("指定的 Java 不可用: %w", err)
		}
		if !info.Usable() {
			return "", fmt.Errorf("需要 Java %d 或更高，当前为 %d", launcher.RequiredJavaMajor, info.Major)
		}
		if !info.Compatible() {
			a.emit("launcher:warning", fmt.Sprintf("当前使用 Java %d，整合包目标为 Java 21，可能出现兼容问题", info.Major))
		}
		return info.Path, nil
	}

	a.mu.Lock()
	cached := append([]launcher.JavaInfo(nil), a.java...)
	a.mu.Unlock()
	for _, j := range cached {
		if j.Compatible() {
			return j.Path, nil
		}
	}

	path, err := launcher.EnsureJava(a.ctx, cfg.InstanceDir, a.javaProgress)
	if err != nil {
		return "", err
	}
	go func() { _ = a.refreshJava() }()
	return path, nil
}

// ---------- 更新 ----------

// CheckUpdate 拉取远端 manifest 并计算差异。
func (a *App) CheckUpdate() (updater.Info, error) {
	a.mu.Lock()
	if a.updating {
		a.mu.Unlock()
		return updater.Info{}, errors.New("更新正在进行中")
	}
	if a.rollingBack {
		a.mu.Unlock()
		return updater.Info{}, errors.New("版本回滚进行中")
	}
	cfg := a.cfg
	a.mu.Unlock()
	if cfg.UpdateURL == "" {
		return updater.Info{}, errors.New("未配置更新地址，请在启动器设置中填写")
	}
	gameDir := launcher.GameDir(cfg.InstanceDir, packVersionID)
	return updater.Check(a.ctx, updater.Options{
		Root:           gameDir,
		ManifestURL:    cfg.UpdateURL,
		CurrentVersion: cfg.PackVersion,
		PackID:         "create-delight-remake",
		Channel:        cfg.Channel,
		Progress:       a.emitUpdateProgress,
	})
}

// InstallUpdate 在后台执行更新，进度通过 update:* 事件推送。
func (a *App) InstallUpdate() error {
	a.mu.Lock()
	if a.updating {
		a.mu.Unlock()
		return errors.New("更新正在进行中")
	}
	if a.running {
		a.mu.Unlock()
		return errors.New("游戏运行中，无法更新")
	}
	if a.rollingBack {
		a.mu.Unlock()
		return errors.New("版本回滚进行中")
	}
	cfg := a.cfg
	if cfg.UpdateURL == "" {
		a.mu.Unlock()
		return errors.New("未配置更新地址，请在启动器设置中填写")
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.updating = true
	a.updateCancel = cancel
	a.mu.Unlock()
	a.emit("launcher:changed", nil)

	gameDir := launcher.GameDir(cfg.InstanceDir, packVersionID)
	go func() {
		res, err := updater.Update(ctx, updater.Options{
			Root:           gameDir,
			ManifestURL:    cfg.UpdateURL,
			CurrentVersion: cfg.PackVersion,
			PackID:         "create-delight-remake",
			Channel:        cfg.Channel,
			Progress:       a.emitUpdateProgress,
		})
		cancel()
		a.mu.Lock()
		a.updating = false
		a.updateCancel = nil
		a.mu.Unlock()
		if err != nil {
			msg := err.Error()
			if errors.Is(err, context.Canceled) {
				msg = "更新已取消"
			}
			a.emit("update:error", map[string]string{"message": msg})
			a.emit("launcher:warning", msg)
		} else {
			a.emit("update:done", res)
		}
		a.emit("launcher:changed", nil)
	}()
	return nil
}

// CancelUpdate 取消正在进行的更新；已进入替换阶段时不会中断。
func (a *App) CancelUpdate() error {
	a.mu.Lock()
	cancel := a.updateCancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (a *App) emitUpdateProgress(p updater.Progress) {
	a.emit("update:progress", p)
}

// ---------- 模组 ----------

func (a *App) gameDir() string {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	return launcher.GameDir(cfg.InstanceDir, packVersionID)
}

// ListMods 列出 mods 目录中的模组与启停状态。
func (a *App) ListMods() ([]ModView, error) {
	list, err := mods.List(a.gameDir())
	if err != nil {
		return nil, err
	}
	out := make([]ModView, 0, len(list))
	for _, m := range list {
		out = append(out, ModView{Name: m.Name, Enabled: m.Enabled, Size: m.Size, Sha256: m.Sha256})
	}
	return out, nil
}

// SetModEnabled 切换模组启停，禁用态通过 .jar.disabled 后缀表示。
func (a *App) SetModEnabled(name string, enabled bool) error {
	a.mu.Lock()
	switch {
	case a.running:
		a.mu.Unlock()
		return errors.New("游戏运行中，无法修改模组")
	case a.updating:
		a.mu.Unlock()
		return errors.New("更新进行中，无法修改模组")
	case a.rollingBack:
		a.mu.Unlock()
		return errors.New("版本回滚进行中，无法修改模组")
	}
	cfg := a.cfg
	a.mu.Unlock()

	gameDir := launcher.GameDir(cfg.InstanceDir, packVersionID)
	if err := mods.SetEnabled(gameDir, name, enabled); err != nil {
		return err
	}
	a.emit("launcher:changed", nil)
	return nil
}

// ---------- 日志与诊断 ----------

// TailLog 读取日志尾部：latest 为游戏日志，launcher 为最近一次启动日志，crash 为最新崩溃报告。
func (a *App) TailLog(kind string) (LogView, error) {
	gameDir := a.gameDir()
	logDir := filepath.Join(gameDir, "logs")
	view := LogView{Kind: kind}
	var path string
	var err error
	switch kind {
	case "latest":
		path = filepath.Join(logDir, "latest.log")
	case "launcher":
		path, err = logs.LatestLauncherLog(logDir)
	case "crash":
		path, err = logs.LatestCrashReport(filepath.Join(gameDir, "crash-reports"))
	default:
		return LogView{}, errors.New("未知日志类型")
	}
	if err != nil {
		return LogView{}, fmt.Errorf("查找日志失败: %w", err)
	}
	if path == "" {
		view.Message = "尚未生成该日志"
		return view, nil
	}
	tail, tailErr := logs.TailFile(path, 500, 512<<10)
	if tailErr != nil {
		if errors.Is(tailErr, os.ErrNotExist) {
			view.Path = path
			view.Message = "日志文件不存在"
			return view, nil
		}
		return LogView{}, fmt.Errorf("读取日志失败: %w", tailErr)
	}
	view.Path = tail.Path
	view.Size = tail.Size
	view.ModifiedAt = tail.ModTime.Format("2006-01-02 15:04:05")
	view.Lines = tail.Lines
	view.Truncated = tail.Truncated
	if len(tail.Lines) == 0 {
		view.Message = "日志为空"
	}
	return view, nil
}

// ExportDiagnostics 打包最新日志、崩溃报告与实例状态到启动器目录。
func (a *App) ExportDiagnostics() (DiagnosticsView, error) {
	path, err := logs.Export(logs.Options{GameDir: a.gameDir(), AppDir: config.AppDir()})
	if err != nil {
		return DiagnosticsView{}, err
	}
	view := DiagnosticsView{Path: path}
	if info, statErr := os.Stat(path); statErr == nil {
		view.Size = info.Size()
	}
	return view, nil
}

// ---------- 版本历史 ----------

// ListBackups 返回本实例的更新备份历史。
func (a *App) ListBackups() ([]updater.BackupView, error) {
	return updater.ListBackups(a.gameDir())
}

// Rollback 把实例回滚到指定备份。
func (a *App) Rollback(stamp string) (updater.RollbackResult, error) {
	a.mu.Lock()
	switch {
	case a.running:
		a.mu.Unlock()
		return updater.RollbackResult{}, errors.New("游戏运行中，无法回滚")
	case a.updating:
		a.mu.Unlock()
		return updater.RollbackResult{}, errors.New("更新进行中，无法回滚")
	case a.rollingBack:
		a.mu.Unlock()
		return updater.RollbackResult{}, errors.New("回滚正在进行中")
	}
	a.rollingBack = true
	cfg := a.cfg
	a.mu.Unlock()
	a.emit("launcher:changed", nil)

	defer func() {
		a.mu.Lock()
		a.rollingBack = false
		a.mu.Unlock()
		a.emit("launcher:changed", nil)
	}()

	gameDir := launcher.GameDir(cfg.InstanceDir, packVersionID)
	return updater.Rollback(a.ctx, gameDir, stamp)
}

// ---------- 进程与目录 ----------

// KillGame 强制结束正在运行的游戏进程。
func (a *App) KillGame() error {
	a.mu.Lock()
	proc, running := a.proc, a.running
	a.mu.Unlock()
	if !running || proc == nil {
		return errors.New("游戏未在运行")
	}
	return proc.Kill()
}

// OpenFolder 在资源管理器中打开实例相关目录。
func (a *App) OpenFolder(kind string) error {
	gameDir := a.gameDir()
	var target string
	switch kind {
	case "instance":
		target = gameDir
	case "mods":
		target = filepath.Join(gameDir, "mods")
	case "logs":
		target = filepath.Join(gameDir, "logs")
	case "crash":
		target = filepath.Join(gameDir, "crash-reports")
	case "backups":
		target = filepath.Join(updater.StateDir(gameDir), "backups")
	case "settings":
		target = config.AppDir()
	case "diagnostics":
		target = filepath.Join(config.AppDir(), "diagnostics")
	default:
		return errors.New("未知目录类型")
	}
	if kind == "mods" || kind == "logs" || kind == "crash" {
		_ = os.MkdirAll(target, 0o755)
	}
	return openDirectory(target)
}

// PickDirectory 弹出目录选择框，用于修改实例目录。
func (a *App) PickDirectory(defaultDir string) (string, error) {
	if strings.TrimSpace(defaultDir) == "" {
		defaultDir = a.gameDir()
	}
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "选择 .minecraft 目录",
		DefaultDirectory: defaultDir,
	})
}

func (a *App) emit(event string, data any) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, event, data)
	}
}
