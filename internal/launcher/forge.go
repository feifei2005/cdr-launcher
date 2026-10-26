package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"cdr-launcher/internal/netproxy"
)

// ensureForge 调用 Forge 官方安装器，并生成启动器使用的包装版本。
// 安装器与依赖均来自 maven.minecraftforge.net，不随启动器分发。
func (in *installer) ensureForge(ctx context.Context) error {
	packID := in.opts.PackID
	mc := in.opts.MCVersion
	forge := in.opts.ForgeVersion
	packDir := filepath.Join(in.opts.InstanceDir, "versions", packID)
	profile := filepath.Join(packDir, packID+".json")
	clientJar := filepath.Join(packDir, packID+".jar")
	forgeID := fmt.Sprintf("%s-forge-%s", mc, forge)
	forgeJSON := filepath.Join(in.opts.InstanceDir, "versions", forgeID, forgeID+".json")
	if fileExists(profile) && fileExists(clientJar) && fileExists(forgeJSON) {
		return nil
	}

	if !fileExists(forgeJSON) {
		if in.opts.JavaPath == "" {
			return errors.New("安装 Forge 需要 Java")
		}
		installerJar, err := in.forgeInstaller(ctx, mc, forge)
		if err != nil {
			return err
		}
		if err := in.runForgeInstaller(ctx, installerJar, forgeID); err != nil {
			return err
		}
		if !fileExists(forgeJSON) {
			return fmt.Errorf("Forge 安装未生成版本文件: %s", forgeJSON)
		}
	}

	if err := os.MkdirAll(packDir, 0o755); err != nil {
		return err
	}
	if err := writeWrapperVersion(profile, packID, forgeID); err != nil {
		return err
	}
	vanillaJar := filepath.Join(in.opts.InstanceDir, "versions", mc, mc+".jar")
	if err := copyFile(vanillaJar, clientJar); err != nil {
		return fmt.Errorf("复制客户端 jar 失败: %w", err)
	}
	return nil
}

func (in *installer) forgeInstaller(ctx context.Context, mc, forge string) (string, error) {
	dir := filepath.Join(in.opts.InstanceDir, "runtime", "downloads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("forge-%s-%s-installer.jar", mc, forge)
	dest := filepath.Join(dir, name)
	if fileExists(dest) {
		return dest, nil
	}
	url := fmt.Sprintf("%s/net/minecraftforge/forge/%s-%s/forge-%s-%s-installer.jar", ForgeMavenBaseURL, mc, forge, mc, forge)
	in.emit("下载 Forge", "Forge "+forge+" 安装器", true)
	if err := downloadFile(ctx, url, dest, 0, 1, nil); err != nil {
		return "", fmt.Errorf("下载 Forge 安装器失败: %w", err)
	}
	return dest, nil
}

func (in *installer) runForgeInstaller(ctx context.Context, installerJar, forgeID string) error {
	profiles := filepath.Join(in.opts.InstanceDir, "launcher_profiles.json")
	if !fileExists(profiles) {
		_ = os.WriteFile(profiles, []byte("{\"profiles\":{},\"settings\":{},\"version\":3}\n"), 0o644)
	}
	logDir := filepath.Join(in.opts.InstanceDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(logDir, "forge-install.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer logFile.Close()

	args := append(netproxy.JavaArgs(), "-jar", installerJar, "--installClient", in.opts.InstanceDir)
	cmd := exec.CommandContext(ctx, in.opts.JavaPath, args...)
	cmd.Dir = in.opts.InstanceDir
	hideWindow(cmd)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	in.emit("安装 Forge", "运行 Forge 官方安装器", true)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("Forge 安装器失败: %w（日志：%s）\n%s", err, logPath, tailFile(logPath, 1200))
	}
	if !fileExists(filepath.Join(in.opts.InstanceDir, "versions", forgeID, forgeID+".json")) {
		return fmt.Errorf("Forge 安装器未生成版本文件（日志：%s）", logPath)
	}
	return nil
}

func writeWrapperVersion(path, packID, forgeID string) error {
	body := map[string]string{
		"id":           packID,
		"inheritsFrom": forgeID,
		"type":         "release",
	}
	raw, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

func copyFile(src, dst string) error {
	if fileExists(dst) {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func tailFile(path string, limit int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	if offset := st.Size() - limit; offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return ""
		}
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
