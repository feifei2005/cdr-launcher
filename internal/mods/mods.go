// Package mods 管理 mods 目录的启停状态，并把玩家改动记录到 user/overlay.json。
package mods

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	modsDirName    = "mods"
	overlayRelPath = "user/overlay.json"
	disabledSuffix = ".disabled"
	jarSuffix      = ".jar"
)

// Entry 是 overlay 中单个模组的记录。
type Entry struct {
	Sha256  string `json:"sha256"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"`
	Enabled bool   `json:"enabled"`
}

// Overlay 是玩家侧的模组状态基线。
type Overlay struct {
	Version int              `json:"version"`
	Mods    map[string]Entry `json:"mods"`
}

// Mod 是 mods 目录中的一个模组。
type Mod struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Size    int64  `json:"size"`
	Sha256  string `json:"sha256,omitempty"`
}

// Dir 返回实例的 mods 目录。
func Dir(root string) string {
	return filepath.Join(root, modsDirName)
}

// OverlayPath 返回 overlay 文件路径。
func OverlayPath(root string) string {
	return filepath.Join(root, filepath.FromSlash(overlayRelPath))
}

// LoadOverlay 读取 overlay；文件不存在时返回空记录。
func LoadOverlay(root string) (*Overlay, error) {
	raw, err := os.ReadFile(OverlayPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return &Overlay{Version: 1, Mods: map[string]Entry{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取模组记录失败: %w", err)
	}
	var o Overlay
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, fmt.Errorf("模组记录格式错误: %w", err)
	}
	if o.Mods == nil {
		o.Mods = map[string]Entry{}
	}
	if o.Version == 0 {
		o.Version = 1
	}
	return &o, nil
}

func saveOverlay(root string, o *Overlay) error {
	path := OverlayPath(root)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建 user 目录失败: %w", err)
	}
	raw, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化模组记录失败: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".overlay-*.tmp")
	if err != nil {
		return fmt.Errorf("创建模组记录临时文件失败: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("写入模组记录失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("刷新模组记录失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭模组记录失败: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("保存模组记录失败: %w", err)
	}
	return nil
}

// splitName 拆分模组文件名，返回启用状态下的文件名与是否仍启用。
func splitName(name string) (base string, enabled bool, ok bool) {
	if len(name) > len(jarSuffix)+len(disabledSuffix) && strings.EqualFold(name[len(name)-len(jarSuffix)-len(disabledSuffix):], jarSuffix+disabledSuffix) {
		return name[:len(name)-len(disabledSuffix)], false, true
	}
	if len(name) > len(jarSuffix) && strings.EqualFold(name[len(name)-len(jarSuffix):], jarSuffix) {
		return name, true, true
	}
	return "", false, false
}

// List 列出 mods 目录中的模组。overlay 中 size/modTime 未变的记录直接复用哈希。
func List(root string) ([]Mod, error) {
	overlay, err := LoadOverlay(root)
	if err != nil {
		overlay = nil
	}
	entries, err := os.ReadDir(Dir(root))
	if errors.Is(err, os.ErrNotExist) {
		return []Mod{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取 mods 目录失败: %w", err)
	}

	found := map[string]Mod{}
	order := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		base, enabled, ok := splitName(entry.Name())
		if !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("读取 %s 信息失败: %w", entry.Name(), err)
		}
		key := strings.ToLower(base)
		if prev, exists := found[key]; exists && prev.Enabled && !enabled {
			continue
		}
		mod := Mod{Name: base, Enabled: enabled, Size: info.Size()}
		if overlay != nil {
			if record, exists := overlay.Mods[base]; exists && record.Size == mod.Size && record.ModTime == info.ModTime().UnixNano() {
				mod.Sha256 = record.Sha256
			}
		}
		if _, exists := found[key]; !exists {
			order = append(order, key)
		}
		found[key] = mod
	}

	list := make([]Mod, 0, len(found))
	for _, key := range order {
		list = append(list, found[key])
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Enabled != list[j].Enabled {
			return list[i].Enabled
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
	return list, nil
}

// SetEnabled 通过 x.jar ↔ x.jar.disabled 原子改名切换模组，并更新 overlay。
func SetEnabled(root, name string, enabled bool) error {
	name = strings.TrimSpace(name)
	base, _, ok := splitName(name)
	if !ok || base == "" || base == jarSuffix {
		return errors.New("模组名称需以 .jar 结尾")
	}
	if base != filepath.Base(base) || strings.ContainsAny(base, `/\:`) {
		return errors.New("模组名称包含非法字符")
	}
	dir := Dir(root)
	active := filepath.Join(dir, base)
	inactive := active + disabledSuffix

	var from, to string
	if enabled {
		from, to = inactive, active
	} else {
		from, to = active, inactive
	}
	info, err := os.Stat(from)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("模组不存在或已处于目标状态: %s", filepath.Base(from))
		}
		return fmt.Errorf("读取模组失败: %w", err)
	}
	if info.IsDir() {
		return errors.New("目标不是文件")
	}
	if _, err := os.Stat(to); err == nil {
		return fmt.Errorf("已存在同名文件: %s", filepath.Base(to))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(from, to); err != nil {
		return fmt.Errorf("切换模组失败: %w", err)
	}

	overlay, err := LoadOverlay(root)
	if err != nil {
		return err
	}
	record := overlay.Mods[base]
	record.Enabled = enabled
	if record.Sha256 == "" || record.Size != info.Size() || record.ModTime != info.ModTime().UnixNano() {
		sum, hashErr := hashFile(to)
		if hashErr != nil {
			return hashErr
		}
		record.Sha256 = sum
	}
	record.Size = info.Size()
	record.ModTime = info.ModTime().UnixNano()
	overlay.Mods[base] = record
	return saveOverlay(root, overlay)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("计算模组哈希失败: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("计算模组哈希失败: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
