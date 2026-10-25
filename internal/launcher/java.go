package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RequiredJavaMajor 是整合包（MC 1.20.1 + Forge 47.4.23）的目标 Java 版本。
// 整合包内含 Java 21 编译的 mod（farrender），Java 17 会在加载 Entity 时抛
// UnsupportedClassVersionError，因此基线为 21。
const RequiredJavaMajor = 21

// JavaInfo 描述一次 Java 探测结果。
type JavaInfo struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Major   int    `json:"major"`
	Source  string `json:"source"`
}

// Compatible 表示与整合包要求的主版本完全一致。
func (j JavaInfo) Compatible() bool { return j.Major == RequiredJavaMajor }

// Usable 表示主版本不低于要求；更高版本可能有 mod 兼容风险。
func (j JavaInfo) Usable() bool { return j.Major >= RequiredJavaMajor }

type javaCandidate struct {
	path   string
	source string
}

// FindJava 按优先级探测本机 Java：设置指定 → 启动器内置 → 实例 runtime →
// JAVA_HOME → PATH → 常见安装目录。返回列表顺序即优先级顺序。
// 若设置里显式指定了路径但不可用，直接返回错误，避免静默回退。
func FindJava(ctx context.Context, settingsJavaPath, instanceDir, appDir string) ([]JavaInfo, error) {
	var explicitErr error
	var result []JavaInfo
	seen := make(map[string]bool)

	for _, c := range javaCandidates(settingsJavaPath, instanceDir, appDir) {
		key := strings.ToLower(filepath.Clean(c.path))
		if seen[key] {
			continue
		}
		seen[key] = true
		info, err := ProbeJava(ctx, c.path)
		if err != nil {
			if c.source == "设置指定" {
				explicitErr = err
			}
			continue
		}
		info.Source = c.source
		result = append(result, info)
	}

	if explicitErr != nil {
		return result, fmt.Errorf("设置中的 Java 不可用: %w", explicitErr)
	}
	if len(result) == 0 {
		return nil, errors.New("未找到可用的 Java 21")
	}
	sortJava(result)
	return result, nil
}

// PickJava 返回优先级最高的可用 Java，并报告其是否与整合包版本完全匹配。
func PickJava(list []JavaInfo) (JavaInfo, bool) {
	for _, j := range list {
		if j.Compatible() {
			return j, true
		}
	}
	if len(list) > 0 {
		return list[0], false
	}
	return JavaInfo{}, false
}

func sortJava(list []JavaInfo) {
	// 保持候选来源优先级：显式设置与内置 runtime 优先于系统安装。
	rank := map[string]int{
		"设置指定":        0,
		"启动器内置":       1,
		"实例 runtime":  2,
		"启动器 runtime": 3,
		"JAVA_HOME":   4,
		"PATH":        5,
		"系统安装":        6,
		"其他启动器":       7,
	}
	sort.SliceStable(list, func(i, k int) bool {
		ri, rk := rank[list[i].Source], rank[list[k].Source]
		return ri < rk
	})
}

func javaCandidates(settingsJavaPath, instanceDir, appDir string) []javaCandidate {
	var out []javaCandidate

	if p := settingsJavaPath; p != "" {
		out = append(out, javaCandidate{resolveJavaExe(p), "设置指定"})
	}
	if instanceDir != "" {
		out = append(out, javaCandidate{
			filepath.Join(instanceDir, "runtime", TemurinDirName, "bin", "javaw.exe"), "启动器内置",
		})
		out = append(out, globJava(filepath.Join(instanceDir, "runtime", "*", "bin", "javaw.exe"), "实例 runtime")...)
	}
	if appDir != "" {
		out = append(out, javaCandidate{
			filepath.Join(appDir, "runtime", TemurinDirName, "bin", "javaw.exe"), "启动器 runtime",
		})
	}
	if home := os.Getenv("JAVA_HOME"); home != "" {
		out = append(out, javaCandidate{resolveJavaExe(home), "JAVA_HOME"})
	}
	if p, err := exec.LookPath("java"); err == nil {
		out = append(out, javaCandidate{p, "PATH"})
	}
	out = append(out, windowsJavaGlobs()...)
	if base, err := os.UserConfigDir(); err == nil {
		out = append(out, globJava(filepath.Join(base, ".minecraft", "runtime", "*", "bin", "javaw.exe"), "其他启动器")...)
	}
	return out
}

func windowsJavaGlobs() []javaCandidate {
	roots := []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA") + `\Programs`}
	patterns := []string{
		`Eclipse Adoptium\jdk-17*\bin\javaw.exe`,
		`Java\jdk-17*\bin\javaw.exe`,
		`Zulu\zulu-17*\bin\javaw.exe`,
		`Microsoft\jdk-17*\bin\javaw.exe`,
		`BellSoft\LibericaJDK-17*\bin\javaw.exe`,
		`Amazon Corretto\jdk17*\bin\javaw.exe`,
	}
	var out []javaCandidate
	for _, root := range roots {
		if root == "" || root == `\Programs` {
			continue
		}
		for _, pattern := range patterns {
			out = append(out, globJava(filepath.Join(root, pattern), "系统安装")...)
		}
	}
	return out
}

func globJava(pattern, source string) []javaCandidate {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	out := make([]javaCandidate, 0, len(matches))
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil && !st.IsDir() {
			out = append(out, javaCandidate{m, source})
		}
	}
	return out
}

// resolveJavaExe 接受 java.exe/javaw.exe 或 JDK 根目录。
func resolveJavaExe(p string) string {
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		for _, name := range []string{"javaw.exe", "java.exe"} {
			c := filepath.Join(p, "bin", name)
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
		return filepath.Join(p, "bin", "javaw.exe")
	}
	if !strings.HasSuffix(strings.ToLower(p), ".exe") {
		if ext := filepath.Ext(p); ext == "" {
			return filepath.Join(p, "bin", "javaw.exe")
		}
	}
	return p
}

// ProbeJava 执行 java -XshowSettings:properties -version 获取版本。
// javaw 无控制台输出，探测时优先使用同目录的 java.exe。
func ProbeJava(ctx context.Context, exe string) (JavaInfo, error) {
	probe := exe
	if strings.EqualFold(filepath.Base(exe), "javaw.exe") {
		if sibling := filepath.Join(filepath.Dir(exe), "java.exe"); fileExists(sibling) {
			probe = sibling
		}
	}

	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, probe, "-XshowSettings:properties", "-version")
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if cctx.Err() != nil {
		return JavaInfo{}, fmt.Errorf("%s 探测超时", filepath.Base(exe))
	}

	info, parseErr := parseJavaVersion(string(out))
	if parseErr != nil {
		if err != nil {
			return JavaInfo{}, fmt.Errorf("%s 无法执行: %w", filepath.Base(exe), err)
		}
		return JavaInfo{}, parseErr
	}
	info.Path = exe
	return info, nil
}

func parseJavaVersion(output string) (JavaInfo, error) {
	var spec, version string
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "java.specification.version":
			spec = strings.TrimSpace(value)
		case "java.version":
			version = strings.TrimSpace(value)
		}
	}
	major := parseJavaMajor(spec)
	if major == 0 {
		major = parseJavaMajor(version)
	}
	if major == 0 {
		return JavaInfo{}, errors.New("无法解析 Java 版本")
	}
	return JavaInfo{Version: version, Major: major}, nil
}

func parseJavaMajor(v string) int {
	v = strings.TrimSpace(strings.Trim(v, `"`))
	if v == "" {
		return 0
	}
	v = strings.TrimPrefix(v, "1.")
	if i := strings.IndexAny(v, ".-+_"); i >= 0 {
		v = v[:i]
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
