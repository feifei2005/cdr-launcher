package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	LauncherName    = "cdr-launcher"
	LauncherVersion = "1.0.0"
)

// LaunchSpec 是组装好的启动命令，由进程层执行。
type LaunchSpec struct {
	JavaPath  string   `json:"javaPath"`
	WorkDir   string   `json:"workDir"`
	Args      []string `json:"args"`
	ClassPath []string `json:"classPath"`
	NativeDir string   `json:"nativeDir"`
	MainClass string   `json:"mainClass"`
}

var placeholderRe = regexp.MustCompile(`\$\{[^}]*\}`)

// BuildLaunch 求值规则、解压 natives、替换占位符，生成完整启动参数。
// instanceDir 用于定位 libraries/assets，gameDir 是游戏工作目录（版本隔离实例为 versions/<id>）。
// extraJVMArgs 追加在版本自带 JVM 参数之后、主类之前，用于皮肤站注入等场景。
func BuildLaunch(v *Version, instanceDir, gameDir, javaPath string, player Player, memoryMB int, extraJVMArgs ...string) (*LaunchSpec, error) {
	absDir, err := filepath.Abs(instanceDir)
	if err != nil {
		return nil, fmt.Errorf("解析实例目录失败: %w", err)
	}
	absGameDir, err := filepath.Abs(gameDir)
	if err != nil {
		return nil, fmt.Errorf("解析游戏目录失败: %w", err)
	}
	host := HostOS()
	features := map[string]bool{}

	classPath := v.ClassPath(absDir, host, features)
	if missing := missingLaunchFiles(v, absDir, host, features, classPath); len(missing) > 0 {
		return nil, fmt.Errorf("实例文件不完整，缺少 %d 个文件（如 %s）", len(missing), missing[0])
	}
	nativeDir, err := PrepareNatives(absDir, v, host, features)
	if err != nil {
		return nil, err
	}

	vars := map[string]string{
		"natives_directory":   nativeDir,
		"classpath":           strings.Join(classPath, string(os.PathListSeparator)),
		"classpath_separator": string(os.PathListSeparator),
		"library_directory":   filepath.Join(absDir, "libraries"),
		"launcher_name":       LauncherName,
		"launcher_version":    LauncherVersion,
		"auth_player_name":    player.Name,
		"version_name":        v.ID,
		"game_directory":      absGameDir,
		"assets_root":         filepath.Join(absDir, "assets"),
		"assets_index_name":   v.Assets,
		"auth_uuid":           player.UUID,
		"auth_access_token":   player.AccessToken,
		"auth_xuid":           player.XUID,
		"clientid":            player.ClientID,
		"user_type":           player.UserType,
		"version_type":        v.Type,
	}

	jvmArgs, err := expandArguments(v.Arguments.JVM, host, features, vars)
	if err != nil {
		return nil, err
	}
	gameArgs, err := expandArguments(v.Arguments.Game, host, features, vars)
	if err != nil {
		return nil, err
	}
	if len(gameArgs) == 0 {
		return nil, errors.New("版本 json 缺少 game 启动参数")
	}

	args := make([]string, 0, len(jvmArgs)+len(gameArgs)+2)
	if memoryMB > 0 {
		args = append(args, fmt.Sprintf("-Xmx%dM", memoryMB))
	}
	args = append(args, jvmArgs...)
	for _, extra := range extraJVMArgs {
		if strings.TrimSpace(extra) != "" {
			args = append(args, extra)
		}
	}
	args = append(args, v.MainClass)
	args = append(args, gameArgs...)

	return &LaunchSpec{
		JavaPath:  javaPath,
		WorkDir:   absGameDir,
		Args:      args,
		ClassPath: classPath,
		NativeDir: nativeDir,
		MainClass: v.MainClass,
	}, nil
}

func missingLaunchFiles(v *Version, instanceDir string, host OS, features map[string]bool, classPath []string) []string {
	var missing []string
	for _, p := range classPath {
		if !fileExists(p) {
			missing = append(missing, p)
		}
	}
	for _, jar := range v.NativeJars(instanceDir, host, features) {
		if !fileExists(jar) {
			missing = append(missing, jar)
		}
	}
	if index := filepath.Join(instanceDir, "assets", "indexes", v.Assets+".json"); !fileExists(index) {
		missing = append(missing, index)
	}
	return missing
}

func expandArguments(list []Argument, host OS, features map[string]bool, vars map[string]string) ([]string, error) {
	out := make([]string, 0, len(list))
	for _, arg := range list {
		if !Allowed(arg.Rules, host, features) {
			continue
		}
		for _, raw := range arg.Value {
			value := raw
			for key, replacement := range vars {
				value = strings.ReplaceAll(value, "${"+key+"}", replacement)
			}
			if left := placeholderRe.FindString(value); left != "" {
				return nil, fmt.Errorf("版本 json 含未知占位符: %s", left)
			}
			out = append(out, value)
		}
	}
	return out, nil
}
