package launcher

import (
	"regexp"
	"runtime"
)

// OS 描述 Mojang 规则中的操作系统条件。
type OS struct {
	Name    string
	Arch    string
	Version string
}

// Rule 对应版本 json 中的 rules 条目。
type Rule struct {
	Action   string          `json:"action"`
	OS       *RuleOS         `json:"os,omitempty"`
	Features map[string]bool `json:"features,omitempty"`
}

type RuleOS struct {
	Name    string `json:"name,omitempty"`
	Arch    string `json:"arch,omitempty"`
	Version string `json:"version,omitempty"`
}

// HostOS 返回当前系统在 Mojang 规则中的名称与架构。
func HostOS() OS {
	name := runtime.GOOS
	switch name {
	case "darwin":
		name = "osx"
	}
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x86_64"
	case "386":
		arch = "x86"
	}
	return OS{Name: name, Arch: arch}
}

// Allowed 按 Mojang 规则语义求值：无 rules 默认允许；有 rules 时按顺序匹配，
// 最后一条匹配规则决定结果，未匹配任何规则则拒绝。
func Allowed(rules []Rule, host OS, features map[string]bool) bool {
	if len(rules) == 0 {
		return true
	}
	allowed := false
	for _, rule := range rules {
		if rule.matches(host, features) {
			allowed = rule.Action == "allow"
		}
	}
	return allowed
}

func (r Rule) matches(host OS, features map[string]bool) bool {
	if r.OS != nil {
		if r.OS.Name != "" && r.OS.Name != host.Name {
			return false
		}
		if r.OS.Arch != "" && r.OS.Arch != host.Arch {
			return false
		}
		if r.OS.Version != "" {
			re, err := regexp.Compile(r.OS.Version)
			if err != nil || !re.MatchString(host.Version) {
				return false
			}
		}
	}
	for key, want := range r.Features {
		if features[key] != want {
			return false
		}
	}
	return true
}
