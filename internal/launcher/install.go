package launcher

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cdr-launcher/internal/netproxy"
)

const (
	// VersionManifestURL 是 Mojang 官方版本清单，游戏文件只从这里与官方 CDN 获取。
	VersionManifestURL = "https://piston-meta.mojang.com/mc/game/version_manifest_v2.json"
	ResourcesBaseURL   = "https://resources.download.minecraft.net"
	ForgeMavenBaseURL  = "https://maven.minecraftforge.net"
	DefaultMCVersion   = "1.20.1"
	DefaultForgeVer    = "47.4.23"
	// officialWorkers 限制官方 CDN 的并发，避免被限流；整合包 blobs 仍使用用户配置值。
	officialWorkers = 64
)

// InstallProgress 是游戏文件安装进度。
type InstallProgress struct {
	Phase      string `json:"phase"`
	Message    string `json:"message"`
	Done       int64  `json:"done"`
	Total      int64  `json:"total"`
	FilesDone  int    `json:"filesDone"`
	FilesTotal int    `json:"filesTotal"`
}

// InstallOptions 描述一次官方游戏文件安装。
type InstallOptions struct {
	InstanceDir  string
	PackID       string
	MCVersion    string
	ForgeVersion string
	JavaPath     string
	Workers      int
	Segments     int
	Progress     func(InstallProgress)
}

type versionManifest struct {
	Versions []manifestEntry `json:"versions"`
}

type manifestEntry struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	URL  string `json:"url"`
	SHA1 string `json:"sha1"`
}

type assetIndexFile struct {
	Objects map[string]struct {
		Hash string `json:"hash"`
		Size int64  `json:"size"`
	} `json:"objects"`
}

type fileJob struct {
	URL  string
	Dest string
	Size int64
	SHA1 string
}

// EnsureGame 确保实例内存在可启动的 Minecraft + Forge 文件。
// 所有游戏文件均从 Mojang 官方 CDN 与 Forge 官方 Maven 下载，不随启动器分发。
func EnsureGame(ctx context.Context, opts InstallOptions) error {
	if opts.InstanceDir == "" {
		return errors.New("实例目录为空")
	}
	if opts.PackID == "" {
		return errors.New("整合包版本 ID 为空")
	}
	if opts.MCVersion == "" {
		opts.MCVersion = DefaultMCVersion
	}
	if opts.ForgeVersion == "" {
		opts.ForgeVersion = DefaultForgeVer
	}
	if opts.Workers <= 0 {
		opts.Workers = officialWorkers
	}
	if opts.Workers > officialWorkers {
		opts.Workers = officialWorkers
	}

	in := &installer{opts: opts, client: netproxy.NewClient(0)}
	in.emit("检查游戏文件", "正在核对版本与资源", true)
	if err := in.ensureVanilla(ctx); err != nil {
		return err
	}
	if err := in.ensureForge(ctx); err != nil {
		return err
	}
	in.emit("完成", "游戏文件已就绪", true)
	return nil
}

type installer struct {
	opts   InstallOptions
	client *http.Client

	mu         sync.Mutex
	lastEmit   time.Time
	doneFiles  int64
	totalFiles int64
	doneBytes  int64
	totalBytes int64
}

func (in *installer) emit(phase, message string, force bool) {
	if in.opts.Progress == nil {
		return
	}
	in.mu.Lock()
	if !force && time.Since(in.lastEmit) < 120*time.Millisecond {
		in.mu.Unlock()
		return
	}
	in.lastEmit = time.Now()
	p := InstallProgress{
		Phase:      phase,
		Message:    message,
		Done:       atomic.LoadInt64(&in.doneBytes),
		Total:      atomic.LoadInt64(&in.totalBytes),
		FilesDone:  int(atomic.LoadInt64(&in.doneFiles)),
		FilesTotal: int(atomic.LoadInt64(&in.totalFiles)),
	}
	in.mu.Unlock()
	in.opts.Progress(p)
}

func (in *installer) ensureVanilla(ctx context.Context) error {
	mc := in.opts.MCVersion
	versionDir := filepath.Join(in.opts.InstanceDir, "versions", mc)
	jsonPath := filepath.Join(versionDir, mc+".json")

	var missingJSON bool
	if _, err := os.Stat(jsonPath); err != nil {
		missingJSON = true
	}

	var v Version
	if missingJSON {
		in.emit("下载版本清单", "获取 Minecraft "+mc+" 版本信息", true)
		entry, err := in.versionEntry(ctx, mc)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(versionDir, 0o755); err != nil {
			return err
		}
		if err := fetchToFile(ctx, in.client, entry.URL, jsonPath, 0, entry.SHA1); err != nil {
			return fmt.Errorf("下载版本 json 失败: %w", err)
		}
	}
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		return fmt.Errorf("读取版本 json 失败: %w", err)
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("解析版本 json 失败: %w", err)
	}
	if v.ID == "" {
		v.ID = mc
	}

	if err := in.ensureClientJar(ctx, versionDir, mc, raw); err != nil {
		return err
	}
	if err := in.ensureLibraries(ctx, &v); err != nil {
		return err
	}
	return in.ensureAssets(ctx, &v)
}

func (in *installer) versionEntry(ctx context.Context, mc string) (manifestEntry, error) {
	manifestPath := filepath.Join(in.opts.InstanceDir, "versions", "version_manifest_v2.json")
	if err := fetchToFile(ctx, in.client, VersionManifestURL, manifestPath, 0, ""); err != nil {
		return manifestEntry{}, fmt.Errorf("下载版本清单失败: %w", err)
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return manifestEntry{}, err
	}
	var m versionManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return manifestEntry{}, fmt.Errorf("解析版本清单失败: %w", err)
	}
	for _, entry := range m.Versions {
		if entry.ID == mc {
			return entry, nil
		}
	}
	return manifestEntry{}, fmt.Errorf("版本清单中不存在 %s", mc)
}

func (in *installer) ensureClientJar(ctx context.Context, versionDir, mc string, raw []byte) error {
	var meta struct {
		Downloads struct {
			Client struct {
				URL  string `json:"url"`
				SHA1 string `json:"sha1"`
				Size int64  `json:"size"`
			} `json:"client"`
		} `json:"downloads"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return fmt.Errorf("解析客户端信息失败: %w", err)
	}
	if meta.Downloads.Client.URL == "" {
		return errors.New("版本 json 缺少客户端下载地址")
	}
	dest := filepath.Join(versionDir, mc+".jar")
	if fileSizeMatches(dest, meta.Downloads.Client.Size) {
		return nil
	}
	in.emit("下载客户端", "Minecraft "+mc+" 客户端 jar", true)
	if err := downloadFile(ctx, meta.Downloads.Client.URL, dest, meta.Downloads.Client.Size, in.segments(), nil); err != nil {
		return fmt.Errorf("下载客户端 jar 失败: %w", err)
	}
	return verifySHA1(dest, meta.Downloads.Client.SHA1)
}

func (in *installer) ensureLibraries(ctx context.Context, v *Version) error {
	host := HostOS()
	features := map[string]bool{"has_custom_resolution": false, "is_demo_user": false}
	var jobs []fileJob
	for _, lib := range v.Libraries {
		if !Allowed(lib.Rules, host, features) {
			continue
		}
		file := lib.Downloads.Artifact
		if file == nil || file.Path == "" || file.URL == "" {
			continue
		}
		jobs = append(jobs, fileJob{
			URL:  file.URL,
			Dest: filepath.Join(in.opts.InstanceDir, "libraries", filepath.FromSlash(file.Path)),
			Size: file.Size,
			SHA1: file.SHA1,
		})
	}
	in.emit("下载依赖库", fmt.Sprintf("%d 个依赖库", len(jobs)), true)
	return in.runJobs(ctx, jobs, "下载依赖库")
}

func (in *installer) ensureAssets(ctx context.Context, v *Version) error {
	index := v.AssetIndex
	if index.URL == "" {
		return errors.New("版本 json 缺少资源索引地址")
	}
	indexPath := filepath.Join(in.opts.InstanceDir, "assets", "indexes", index.ID+".json")
	if index.ID == "" {
		indexPath = filepath.Join(in.opts.InstanceDir, "assets", "indexes", v.Assets+".json")
	}
	in.emit("下载资源索引", "Minecraft 资源索引", true)
	if err := fetchToFile(ctx, in.client, index.URL, indexPath, index.Size, index.SHA1); err != nil {
		return fmt.Errorf("下载资源索引失败: %w", err)
	}
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		return err
	}
	var assets assetIndexFile
	if err := json.Unmarshal(raw, &assets); err != nil {
		return fmt.Errorf("解析资源索引失败: %w", err)
	}
	jobs := make([]fileJob, 0, len(assets.Objects))
	for _, obj := range assets.Objects {
		if obj.Hash == "" {
			continue
		}
		prefix := obj.Hash[:2]
		jobs = append(jobs, fileJob{
			URL:  ResourcesBaseURL + "/" + prefix + "/" + obj.Hash,
			Dest: filepath.Join(in.opts.InstanceDir, "assets", "objects", prefix, obj.Hash),
			Size: obj.Size,
			SHA1: obj.Hash,
		})
	}
	in.emit("下载资源文件", fmt.Sprintf("%d 个资源对象", len(jobs)), true)
	return in.runJobs(ctx, jobs, "下载资源文件")
}

func (in *installer) segments() int {
	if in.opts.Segments > 0 {
		return in.opts.Segments
	}
	return 4
}

// runJobs 并发下载缺失文件；已存在且大小一致的文件直接跳过。
func (in *installer) runJobs(ctx context.Context, jobs []fileJob, phase string) error {
	pending := make([]fileJob, 0, len(jobs))
	var bytes int64
	for _, job := range jobs {
		if fileSizeMatches(job.Dest, job.Size) {
			continue
		}
		pending = append(pending, job)
		bytes += job.Size
	}
	atomic.StoreInt64(&in.totalBytes, bytes)
	atomic.StoreInt64(&in.totalFiles, int64(len(jobs)))
	atomic.StoreInt64(&in.doneFiles, int64(len(jobs)-len(pending)))
	atomic.StoreInt64(&in.doneBytes, 0)
	in.emit(phase, fmt.Sprintf("需要下载 %d/%d 个文件", len(pending), len(jobs)), true)
	if len(pending) == 0 {
		return nil
	}

	workers := in.opts.Workers
	if workers < 1 {
		workers = 1
	}
	if workers > len(pending) {
		workers = len(pending)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	work := make(chan fileJob)
	var wg sync.WaitGroup
	var firstErr error
	var mu sync.Mutex
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range work {
				if ctx.Err() != nil {
					return
				}
				if err := fetchToFile(ctx, in.client, job.URL, job.Dest, job.Size, job.SHA1); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					cancel()
					return
				}
				atomic.AddInt64(&in.doneFiles, 1)
				atomic.AddInt64(&in.doneBytes, job.Size)
				in.emit(phase, filepath.Base(job.Dest), false)
			}
		}()
	}
	for _, job := range pending {
		select {
		case work <- job:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(work)
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	in.emit(phase, "完成", true)
	return nil
}

func fileSizeMatches(path string, size int64) bool {
	if size <= 0 {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && st.Size() == size
}

func verifySHA1(path, want string) error {
	if want == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), want) {
		return fmt.Errorf("SHA1 校验失败: %s", filepath.Base(path))
	}
	return nil
}

// fetchToFile 下载小文件到目标路径，带重试与 SHA1 校验。
// 已存在且大小一致的文件直接跳过：下载过程总是先写 .part 再改名，不会留下半截文件。
func fetchToFile(ctx context.Context, client *http.Client, rawURL, dest string, size int64, sha1sum string) error {
	if st, err := os.Stat(dest); err == nil && st.Size() == size && size > 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	part := dest + ".part"
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(400*(1<<attempt)) * time.Millisecond):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		err := fetchOnce(ctx, client, rawURL, part, size, sha1sum)
		if err == nil {
			if err := os.Rename(part, dest); err != nil {
				return err
			}
			return nil
		}
		lastErr = err
		_ = os.Remove(part)
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return lastErr
}

func fetchOnce(ctx context.Context, client *http.Client, rawURL, part string, size int64, sha1sum string) error {
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "CDRLauncher/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s: %s", resp.Status, filepath.Base(rawURL))
	}
	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(f, resp.Body)
	syncErr := f.Sync()
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if size > 0 && written != size {
		return fmt.Errorf("文件大小不符: %d/%d", written, size)
	}
	return verifySHA1(part, sha1sum)
}
