package launcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Version 是启动所需的版本 json 子集（Forge 合并格式，忽略 patches 字段）。
type Version struct {
	ID           string      `json:"id"`
	Type         string      `json:"type"`
	MainClass    string      `json:"mainClass"`
	Jar          string      `json:"jar"`
	Assets       string      `json:"assets"`
	AssetIndex   AssetIndex  `json:"assetIndex"`
	JavaVersion  JavaVersion `json:"javaVersion"`
	Libraries    []Library   `json:"libraries"`
	Arguments    Arguments   `json:"arguments"`
	InheritsFrom string      `json:"inheritsFrom"`
}

type AssetIndex struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	SHA1      string `json:"sha1"`
	Size      int64  `json:"size"`
	TotalSize int64  `json:"totalSize"`
}

type JavaVersion struct {
	Component    string `json:"component"`
	MajorVersion int    `json:"majorVersion"`
}

type Arguments struct {
	Game []Argument `json:"game"`
	JVM  []Argument `json:"jvm"`
}

// Argument 同时兼容纯字符串与 {rules, value} 两种形式。
type Argument struct {
	Value []string
	Rules []Rule
}

func (a *Argument) UnmarshalJSON(raw []byte) error {
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		a.Value = []string{single}
		return nil
	}
	var obj struct {
		Value json.RawMessage `json:"value"`
		Rules []Rule          `json:"rules"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	a.Rules = obj.Rules
	if len(obj.Value) == 0 {
		return nil
	}
	var list []string
	if err := json.Unmarshal(obj.Value, &list); err == nil {
		a.Value = list
		return nil
	}
	if err := json.Unmarshal(obj.Value, &single); err != nil {
		return err
	}
	a.Value = []string{single}
	return nil
}

type Library struct {
	Name      string           `json:"name"`
	Downloads LibraryDownloads `json:"downloads"`
	Rules     []Rule           `json:"rules"`
}

type LibraryDownloads struct {
	Artifact *LibraryFile `json:"artifact,omitempty"`
}

type LibraryFile struct {
	Path string `json:"path"`
	URL  string `json:"url"`
	SHA1 string `json:"sha1"`
	Size int64  `json:"size"`
}

// GameDir 返回游戏工作目录。HMCL/PCL 的版本隔离实例把 mods、config、saves
// 放在 versions/<id>/ 下，此时必须以该目录为工作目录，否则模组不会加载。
func GameDir(instanceDir, id string) string {
	versionDir := filepath.Join(instanceDir, "versions", id)
	for _, marker := range []string{"mods", "config", "options.txt", "saves", "resourcepacks"} {
		if _, err := os.Stat(filepath.Join(versionDir, marker)); err == nil {
			return versionDir
		}
	}
	return instanceDir
}

// LoadVersion 读取 versions/<id>/<id>.json，并按 inheritsFrom 链合并父版本。
func LoadVersion(instanceDir, id string) (*Version, error) {
	return loadVersion(instanceDir, id, 0)
}

func loadVersion(instanceDir, id string, depth int) (*Version, error) {
	if id == "" {
		return nil, errors.New("版本 ID 为空")
	}
	if depth > 4 {
		return nil, fmt.Errorf("版本继承层级过深: %s", id)
	}
	path := filepath.Join(instanceDir, "versions", id, id+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取版本 json 失败: %w", err)
	}
	var v Version
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("解析版本 json 失败: %w", err)
	}
	if v.ID == "" {
		v.ID = id
	}
	if v.InheritsFrom != "" {
		parent, err := loadVersion(instanceDir, v.InheritsFrom, depth+1)
		if err != nil {
			return nil, fmt.Errorf("加载父版本 %s 失败: %w", v.InheritsFrom, err)
		}
		mergeParent(&v, parent)
	}
	if v.MainClass == "" {
		return nil, errors.New("版本 json 缺少 mainClass")
	}
	if v.Assets == "" {
		v.Assets = v.AssetIndex.ID
	}
	if v.Type == "" {
		v.Type = "release"
	}
	return &v, nil
}

// mergeParent 把父版本并入子版本：子版本库在前，父版本参数在前（与 Forge 官方合并顺序一致）。
func mergeParent(child, parent *Version) {
	libs := make([]Library, 0, len(child.Libraries)+len(parent.Libraries))
	seen := make(map[string]bool, len(child.Libraries))
	for _, lib := range child.Libraries {
		seen[lib.Name] = true
		libs = append(libs, lib)
	}
	for _, lib := range parent.Libraries {
		if seen[lib.Name] {
			continue
		}
		libs = append(libs, lib)
	}
	child.Libraries = libs

	child.Arguments.JVM = append(append([]Argument(nil), parent.Arguments.JVM...), child.Arguments.JVM...)
	child.Arguments.Game = append(append([]Argument(nil), parent.Arguments.Game...), child.Arguments.Game...)

	if child.MainClass == "" {
		child.MainClass = parent.MainClass
	}
	if child.Assets == "" {
		child.Assets = parent.Assets
	}
	if child.AssetIndex.ID == "" {
		child.AssetIndex = parent.AssetIndex
	}
	if child.JavaVersion.MajorVersion == 0 {
		child.JavaVersion = parent.JavaVersion
	}
	if child.Type == "" {
		child.Type = parent.Type
	}
}

// ClientJar 返回版本主 jar 路径。
func (v *Version) ClientJar(instanceDir string) string {
	name := v.Jar
	if name == "" {
		name = v.ID
	}
	if !strings.HasSuffix(strings.ToLower(name), ".jar") {
		name += ".jar"
	}
	return filepath.Join(instanceDir, "versions", v.ID, name)
}

// ClassPath 返回 classpath 条目（主 jar 在前，含原生库 jar）。
func (v *Version) ClassPath(instanceDir string, host OS, features map[string]bool) []string {
	libDir := filepath.Join(instanceDir, "libraries")
	entries := []string{v.ClientJar(instanceDir)}
	seen := map[string]bool{strings.ToLower(entries[0]): true}
	for _, lib := range v.Libraries {
		if !Allowed(lib.Rules, host, features) {
			continue
		}
		file := lib.Downloads.Artifact
		if file == nil || file.Path == "" {
			continue
		}
		p := filepath.Join(libDir, filepath.FromSlash(file.Path))
		key := strings.ToLower(p)
		if seen[key] {
			continue
		}
		seen[key] = true
		entries = append(entries, p)
	}
	return entries
}

// NativeJars 返回当前平台需要解压的原生库 jar。
func (v *Version) NativeJars(instanceDir string, host OS, features map[string]bool) []string {
	libDir := filepath.Join(instanceDir, "libraries")
	want := "natives-" + host.Name
	var jars []string
	for _, lib := range v.Libraries {
		if !Allowed(lib.Rules, host, features) || lib.classifier() != want {
			continue
		}
		file := lib.Downloads.Artifact
		if file == nil || file.Path == "" {
			continue
		}
		jars = append(jars, filepath.Join(libDir, filepath.FromSlash(file.Path)))
	}
	return jars
}

func (l Library) classifier() string {
	parts := strings.Split(l.Name, ":")
	if len(parts) < 4 {
		return ""
	}
	return parts[3]
}
