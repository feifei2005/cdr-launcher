package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	DefaultChannel  = "stable"
	DefaultMemoryMB = 8192
	MinMemoryMB     = 1024
	MaxMemoryMB     = 32768
)

var playerNameRe = regexp.MustCompile(`^[A-Za-z0-9_]{3,16}$`)

// Settings 是持久化到 %APPDATA%\CDRLauncher\settings.json 的启动器设置。
type Settings struct {
	InstanceDir string `json:"instanceDir"`
	JavaPath    string `json:"javaPath"`
	PlayerName  string `json:"playerName"`
	MemoryMB    int    `json:"memoryMb"`
	Channel     string `json:"channel"`
	PackVersion string `json:"packVersion"`
	UpdateURL   string `json:"updateUrl"`
}

// Defaults 返回以 exeDir 为基准的默认设置。
func Defaults(exeDir string) Settings {
	return Settings{
		InstanceDir: DefaultInstanceDir(exeDir),
		PlayerName:  "Player",
		MemoryMB:    DefaultMemoryMB,
		Channel:     DefaultChannel,
	}
}

// Load 读取设置文件；文件不存在时返回默认值。
func Load(path, exeDir string) (Settings, error) {
	defaults := Defaults(exeDir)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaults, nil
	}
	if err != nil {
		return defaults, fmt.Errorf("读取设置失败: %w", err)
	}
	s := defaults
	if err := json.Unmarshal(raw, &s); err != nil {
		return defaults, fmt.Errorf("设置文件格式错误: %w", err)
	}
	s.Normalize(exeDir)
	return s, nil
}

// Save 以原子方式写入设置文件。
func Save(path string, s Settings) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建设置目录失败: %w", err)
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化设置失败: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return fmt.Errorf("创建设置临时文件失败: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("写入设置失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("刷新设置失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭设置文件失败: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("保存设置失败: %w", err)
	}
	return nil
}

// Normalize 修正空值与越界值，不校验玩家名合法性。
func (s *Settings) Normalize(exeDir string) {
	s.InstanceDir = strings.TrimSpace(s.InstanceDir)
	if s.InstanceDir == "" {
		s.InstanceDir = DefaultInstanceDir(exeDir)
	}
	s.InstanceDir = filepath.Clean(s.InstanceDir)
	s.JavaPath = strings.TrimSpace(s.JavaPath)
	if s.PlayerName == "" {
		s.PlayerName = "Player"
	}
	if s.MemoryMB < MinMemoryMB || s.MemoryMB > MaxMemoryMB {
		s.MemoryMB = DefaultMemoryMB
	}
	if s.Channel == "" {
		s.Channel = DefaultChannel
	}
	s.UpdateURL = strings.TrimSpace(s.UpdateURL)
}

// Validate 校验用户可修改字段，供保存与启动前调用。
func (s Settings) Validate() error {
	if !playerNameRe.MatchString(s.PlayerName) {
		return errors.New("游戏昵称需为 3-16 位字母、数字或下划线")
	}
	if s.MemoryMB < MinMemoryMB || s.MemoryMB > MaxMemoryMB {
		return fmt.Errorf("内存需在 %d-%d MB 之间", MinMemoryMB, MaxMemoryMB)
	}
	if s.InstanceDir == "" {
		return errors.New("实例目录不能为空")
	}
	if s.UpdateURL != "" {
		u, err := url.Parse(s.UpdateURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("更新地址需为完整的 http(s) 地址")
		}
	}
	return nil
}

// ValidPlayerName 供前端即时校验使用。
func ValidPlayerName(name string) bool {
	return playerNameRe.MatchString(name)
}
