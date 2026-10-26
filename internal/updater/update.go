package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"cdr-launcher/internal/manifest"
	"cdr-launcher/internal/netproxy"
)

const (
	stagingDirName = "staging"
	filesDirName   = "files"
	stateFileName  = "state.json"
	journalName    = "journal.jsonl"
	backupsDirName = "backups"
	keepBackups    = 3
)

// Progress 描述更新阶段进度。
type Progress struct {
	Phase      string `json:"phase"`
	Done       int64  `json:"done"`
	Total      int64  `json:"total"`
	FilesDone  int    `json:"filesDone"`
	FilesTotal int    `json:"filesTotal"`
	Message    string `json:"message,omitempty"`
}

// Options 是检查与安装更新的参数。
type Options struct {
	Root           string
	ManifestURL    string
	CurrentVersion string
	PackID         string
	Channel        string
	Workers        int
	Segments       int
	Progress       func(Progress)
}

// Info 是检查更新的结果。
type Info struct {
	Available      bool   `json:"available"`
	UpToDate       bool   `json:"upToDate"`
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	PublishedAt    string `json:"publishedAt,omitempty"`
	ChangedFiles   int    `json:"changedFiles"`
	Patches        int    `json:"patches"`
	Deletes        int    `json:"deletes"`
	DownloadBytes  int64  `json:"downloadBytes"`
}

// Result 是安装更新的结果。
type Result struct {
	Version       string `json:"version"`
	Updated       bool   `json:"updated"`
	Files         int    `json:"files"`
	Patches       int    `json:"patches"`
	Deletes       int    `json:"deletes"`
	DownloadBytes int64  `json:"downloadBytes"`
}

// FetchManifest 下载并解析 manifest。
func FetchManifest(ctx context.Context, rawURL string) (*manifest.Manifest, error) {
	client := netproxy.NewClient(60 * time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构造 manifest 请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("User-Agent", "CDRLauncher/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("获取更新清单失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("获取更新清单失败: HTTP %s", resp.Status)
	}
	const limit = 64 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("读取更新清单失败: %w", err)
	}
	if len(raw) > limit {
		return nil, errors.New("更新清单过大")
	}
	m, err := manifest.Parse(raw)
	if err != nil {
		return nil, err
	}
	if m.Version == "" {
		return nil, errors.New("更新清单缺少版本号")
	}
	if m.BaseURL == "" {
		return nil, errors.New("更新清单缺少 baseUrl")
	}
	if _, err := validateRemoteURL(m.BaseURL); err != nil {
		return nil, fmt.Errorf("更新清单 baseUrl 非法: %w", err)
	}
	return m, nil
}

// Check 检查远端是否有新版本。
func Check(ctx context.Context, opts Options) (Info, error) {
	m, idx, actions, err := prepare(ctx, opts)
	if err != nil {
		return Info{}, err
	}
	files, patches, deletes, bytes := PlanStats(actions)
	current := idx.Version
	if current == "" {
		current = opts.CurrentVersion
	}
	info := Info{
		CurrentVersion: current,
		LatestVersion:  m.Version,
		PublishedAt:    m.PublishedAt,
		ChangedFiles:   files,
		Patches:        patches,
		Deletes:        deletes,
		DownloadBytes:  bytes,
	}
	if files == 0 && deletes == 0 {
		if current == "" || current != m.Version {
			idx.Version = m.Version
			idx.InstalledAt = time.Now().Format(time.RFC3339)
			if err := SaveIndex(opts.Root, idx); err != nil {
				return Info{}, err
			}
			info.CurrentVersion = m.Version
		}
		info.UpToDate = true
		return info, nil
	}
	if err := checkDowngrade(current, m.Version); err != nil {
		return Info{}, err
	}
	info.Available = true
	return info, nil
}

// Update 执行更新；下载、补丁、替换与回滚均在实例目录内完成。
func Update(ctx context.Context, opts Options) (Result, error) {
	m, idx, actions, err := prepare(ctx, opts)
	if err != nil {
		return Result{}, err
	}
	files, patches, deletes, bytes := PlanStats(actions)
	result := Result{
		Version:       m.Version,
		Files:         files,
		Patches:       patches,
		Deletes:       deletes,
		DownloadBytes: bytes,
	}
	if files == 0 && deletes == 0 {
		if idx.Version != m.Version {
			idx.Version = m.Version
			idx.InstalledAt = time.Now().Format(time.RFC3339)
			if err := SaveIndex(opts.Root, idx); err != nil {
				return Result{}, err
			}
			result.Updated = true
		}
		return result, nil
	}
	current := idx.Version
	if current == "" {
		current = opts.CurrentVersion
	}
	if err := checkDowngrade(current, m.Version); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	staging := filepath.Join(StateDir(opts.Root), stagingDirName)
	blobDir := filepath.Join(staging, "blobs")
	fileDir := filepath.Join(staging, filesDirName)
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("创建下载目录失败: %w", err)
	}
	if err := os.MkdirAll(fileDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("创建暂存目录失败: %w", err)
	}
	_ = saveState(opts.Root, updateState{From: current, To: m.Version, Phase: "download"})

	required, err := RequiredBlobs(actions)
	if err != nil {
		return Result{}, err
	}
	progress := opts.Progress
	dl, err := NewDownloader(m.BaseURL, blobDir, func(done, total int64) {
		if progress != nil {
			progress(Progress{Phase: "download", Done: done, Total: total, Message: "下载更新文件"})
		}
	})
	if err != nil {
		return Result{}, err
	}
	dl.SetConcurrency(opts.Workers, opts.Segments)
	if err := dl.Fetch(ctx, required); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	_ = saveState(opts.Root, updateState{From: current, To: m.Version, Phase: "apply"})
	if err := applyActions(ctx, opts.Root, actions, blobDir, fileDir, dl, progress); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	_ = saveState(opts.Root, updateState{From: current, To: m.Version, Phase: "commit"})
	if progress != nil {
		progress(Progress{Phase: "commit", FilesTotal: files + deletes, Message: "替换文件"})
	}
	next, err := commit(opts.Root, m, actions, idx, fileDir)
	if err != nil {
		return Result{}, err
	}
	_ = next
	result.Updated = true
	_ = removeState(opts.Root)
	_ = os.RemoveAll(staging)
	pruneBackups(opts.Root)
	if progress != nil {
		progress(Progress{Phase: "done", Message: "更新完成"})
	}
	return result, nil
}

func prepare(ctx context.Context, opts Options) (*manifest.Manifest, *Index, []Action, error) {
	if strings.TrimSpace(opts.Root) == "" {
		return nil, nil, nil, errors.New("实例目录不能为空")
	}
	if err := Recover(opts.Root); err != nil {
		return nil, nil, nil, err
	}
	rawURL, err := ManifestURL(opts.ManifestURL)
	if err != nil {
		return nil, nil, nil, err
	}
	m, err := FetchManifest(ctx, rawURL)
	if err != nil {
		return nil, nil, nil, err
	}
	if opts.PackID != "" && m.PackID != "" && !strings.EqualFold(m.PackID, opts.PackID) {
		return nil, nil, nil, fmt.Errorf("更新清单属于其他整合包: %s", m.PackID)
	}
	if opts.Channel != "" && m.Channel != "" && !strings.EqualFold(m.Channel, opts.Channel) {
		return nil, nil, nil, fmt.Errorf("更新清单通道为 %s，当前为 %s", m.Channel, opts.Channel)
	}
	prev, err := LoadIndex(opts.Root)
	if err != nil {
		return nil, nil, nil, err
	}
	if progress := opts.Progress; progress != nil {
		progress(Progress{Phase: "scan", Message: "校验本地文件"})
	}
	var lastScan time.Time
	idx, err := Refresh(opts.Root, prev, func(done int64) {
		if opts.Progress == nil {
			return
		}
		if done%100 != 0 && time.Since(lastScan) < 250*time.Millisecond {
			return
		}
		lastScan = time.Now()
		opts.Progress(Progress{Phase: "scan", FilesDone: int(done), Message: "校验本地文件"})
	})
	if err != nil {
		return nil, nil, nil, err
	}
	actions, err := BuildPlan(m, idx)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := SaveIndex(opts.Root, idx); err != nil {
		return nil, nil, nil, err
	}
	return m, idx, actions, nil
}

func checkDowngrade(current, latest string) error {
	if current == "" || latest == "" {
		return nil
	}
	if compareVersions(latest, current) < 0 {
		return fmt.Errorf("服务器版本 %s 低于本地 %s，已拒绝降级", latest, current)
	}
	return nil
}

func compareVersions(a, b string) int {
	as := versionParts(a)
	bs := versionParts(b)
	for i := 0; i < len(as) || i < len(bs); i++ {
		var av, bv int
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		if av != bv {
			if av > bv {
				return 1
			}
			return -1
		}
	}
	return 0
}

func versionParts(v string) []int {
	v = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(v), "v"))
	var out []int
	for _, part := range strings.Split(v, ".") {
		digits := strings.Builder{}
		for _, c := range part {
			if c >= '0' && c <= '9' {
				digits.WriteRune(c)
			} else {
				break
			}
		}
		n, _ := strconv.Atoi(digits.String())
		out = append(out, n)
	}
	return out
}

func applyActions(ctx context.Context, root string, actions []Action, blobDir, fileDir string, dl *Downloader, progress func(Progress)) error {
	total := 0
	for _, action := range actions {
		if action.Kind == KindDownload || action.Kind == KindPatch {
			total++
		}
	}
	done := 0
	for _, action := range actions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if action.Kind != KindDownload && action.Kind != KindPatch {
			continue
		}
		if progress != nil && (done == 0 || done%20 == 0) {
			progress(Progress{Phase: "apply", FilesDone: done, FilesTotal: total, Message: "应用 " + action.Path})
		}
		dest := filepath.Join(fileDir, filepath.FromSlash(action.Path))
		if verifyFile(dest, action.Target.Size, action.Target.Sha256) == nil {
			done++
			continue
		}
		switch action.Kind {
		case KindDownload:
			blob := filepath.Join(blobDir, action.Target.Sha256)
			if err := stageBlob(blob, dest, action.Target.Size, action.Target.Sha256); err != nil {
				return fmt.Errorf("%s: %w", action.Path, err)
			}
		case KindPatch:
			err := applyPatchAction(root, action, blobDir, dest)
			if err != nil {
				blob := filepath.Join(blobDir, action.Target.Sha256)
				if verifyFile(blob, action.Target.Size, action.Target.Sha256) != nil {
					quiet := *dl
					quiet.Progress = nil
					if fetchErr := quiet.Fetch(ctx, []Blob{{Sha256: action.Target.Sha256, Size: action.Target.Size}}); fetchErr != nil {
						return fmt.Errorf("%s: 补丁失败且整文件下载失败: %w", action.Path, fetchErr)
					}
				}
				if stageErr := stageBlob(blob, dest, action.Target.Size, action.Target.Sha256); stageErr != nil {
					return fmt.Errorf("%s: 补丁失败后回退下载失败: %w", action.Path, stageErr)
				}
			}
		}
		done++
	}
	if progress != nil {
		progress(Progress{Phase: "apply", FilesDone: done, FilesTotal: total, Message: "文件已就绪"})
	}
	return nil
}

func applyPatchAction(root string, action Action, blobDir, dest string) error {
	if action.Patch == nil {
		return errors.New("补丁信息缺失")
	}
	localPath := filepath.Join(root, filepath.FromSlash(action.Path))
	old, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("读取旧文件失败: %w", err)
	}
	if int64(len(old)) != action.Patch.SourceSize {
		return fmt.Errorf("旧文件大小不符: %d != %d", len(old), action.Patch.SourceSize)
	}
	sum, err := hashBytes(old)
	if err != nil {
		return err
	}
	if !strings.EqualFold(sum, action.Patch.SourceSha256) {
		return fmt.Errorf("旧文件已被修改: %s", action.Path)
	}
	patchPath := filepath.Join(blobDir, action.Patch.Sha256)
	if err := verifyFile(patchPath, action.Patch.Size, action.Patch.Sha256); err != nil {
		return fmt.Errorf("补丁文件校验失败: %w", err)
	}
	f, err := os.Open(patchPath)
	if err != nil {
		return err
	}
	out, err := manifest.ApplyPatch(f, old)
	f.Close()
	if err != nil {
		return err
	}
	if int64(len(out)) != action.Target.Size {
		return fmt.Errorf("补丁输出大小不符: %d != %d", len(out), action.Target.Size)
	}
	outSum, err := hashBytes(out)
	if err != nil {
		return err
	}
	if !strings.EqualFold(outSum, action.Target.Sha256) {
		return fmt.Errorf("补丁输出哈希不符: %s", action.Path)
	}
	return writeFileAtomic(dest, out)
}

func stageBlob(blob, dest string, size int64, sum string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Link(blob, dest); err == nil {
		return nil
	}
	in, err := os.Open(blob)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dest + ".cdrtmp"
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return err
	}
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	buf := make([]byte, 128<<10)
	if _, err := io.CopyBuffer(out, in, buf); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := verifyFile(tmp, size, sum); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cdr-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

type journalHeader struct {
	Version   int    `json:"version"`
	BackupDir string `json:"backupDir"`
	From      string `json:"from"`
	To        string `json:"to"`
}

type journalEntry struct {
	Op        string `json:"op"`
	Path      string `json:"path"`
	Backup    string `json:"backup,omitempty"`
	HadBackup bool   `json:"hadBackup"`
}

type updateState struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Phase string `json:"phase"`
}

func saveState(root string, st updateState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(StateDir(root), stateFileName), raw)
}

func removeState(root string) error {
	err := os.Remove(filepath.Join(StateDir(root), stateFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func commit(root string, m *manifest.Manifest, actions []Action, idx *Index, fileDir string) (*Index, error) {
	stateDir := StateDir(root)
	var entries []journalEntry
	for _, action := range actions {
		switch action.Kind {
		case KindDownload, KindPatch:
			dst := filepath.Join(root, filepath.FromSlash(action.Path))
			_, err := os.Lstat(dst)
			had := err == nil
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			entries = append(entries, journalEntry{Op: "replace", Path: action.Path, Backup: action.Path, HadBackup: had})
		case KindDelete:
			dst := filepath.Join(root, filepath.FromSlash(action.Path))
			if _, err := os.Lstat(dst); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}
			entries = append(entries, journalEntry{Op: "delete", Path: action.Path, Backup: action.Path, HadBackup: true})
		}
	}
	if len(entries) == 0 {
		return idx, nil
	}
	stamp, backupRoot, err := createBackupDir(stateDir)
	if err != nil {
		return nil, err
	}
	backupRel := filepath.ToSlash(filepath.Join(backupsDirName, stamp))
	meta := backupMeta{
		Version:   1,
		From:      idx.Version,
		To:        m.Version,
		CreatedAt: time.Now().Format(time.RFC3339),
		Entries:   entries,
	}
	if err := writeBackupMeta(backupRoot, meta); err != nil {
		_ = os.RemoveAll(backupRoot)
		return nil, err
	}
	header := journalHeader{Version: 1, BackupDir: backupRel, From: idx.Version, To: m.Version}
	if err := writeJournal(stateDir, header, entries); err != nil {
		_ = os.RemoveAll(backupRoot)
		return nil, err
	}

	fail := func(cause error) (*Index, error) {
		rollbackErr := Recover(root)
		if rollbackErr != nil {
			return nil, fmt.Errorf("%w；回滚失败: %v", cause, rollbackErr)
		}
		_ = os.RemoveAll(backupRoot)
		return nil, cause
	}
	for _, entry := range entries {
		dst := filepath.Join(root, filepath.FromSlash(entry.Path))
		switch entry.Op {
		case "replace":
			src := filepath.Join(fileDir, filepath.FromSlash(entry.Path))
			if _, err := os.Stat(src); err != nil {
				return fail(fmt.Errorf("%s: 暂存文件缺失: %w", entry.Path, err))
			}
			if entry.HadBackup {
				backup := filepath.Join(backupRoot, filepath.FromSlash(entry.Backup))
				if err := os.MkdirAll(filepath.Dir(backup), 0o755); err != nil {
					return fail(err)
				}
				if err := os.Rename(dst, backup); err != nil {
					return fail(fmt.Errorf("%s: 备份旧文件失败: %w", entry.Path, err))
				}
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return fail(err)
			}
			if err := os.Rename(src, dst); err != nil {
				return fail(fmt.Errorf("%s: 替换文件失败: %w", entry.Path, err))
			}
		case "delete":
			backup := filepath.Join(backupRoot, filepath.FromSlash(entry.Backup))
			if err := os.MkdirAll(filepath.Dir(backup), 0o755); err != nil {
				return fail(err)
			}
			if err := os.Rename(dst, backup); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return fail(fmt.Errorf("%s: 删除文件失败: %w", entry.Path, err))
			}
		}
	}

	next := buildNextIndex(root, m, actions, idx, fileDir)
	tmpIndex, err := writeIndexTemp(root, next)
	if err != nil {
		return fail(err)
	}
	if err := os.Remove(filepath.Join(stateDir, journalName)); err != nil && !os.IsNotExist(err) {
		os.Remove(tmpIndex)
		return fail(err)
	}
	if err := os.Rename(tmpIndex, indexPath(root)); err != nil {
		return nil, fmt.Errorf("保存本地索引失败: %w", err)
	}
	return next, nil
}

func buildNextIndex(root string, m *manifest.Manifest, actions []Action, idx *Index, fileDir string) *Index {
	next := &Index{Version: m.Version, InstalledAt: time.Now().Format(time.RFC3339), Files: map[string]FileState{}}
	if idx != nil {
		for p, st := range idx.Files {
			next.Files[p] = st
		}
	}
	for _, action := range actions {
		switch action.Kind {
		case KindKeep:
			if action.Local.Sha256 != "" {
				next.Files[action.Path] = action.Local
			}
		case KindSkip:
			// 保留玩家文件在索引中的原始状态。
		case KindDelete:
			delete(next.Files, action.Path)
		case KindDownload, KindPatch:
			dst := filepath.Join(root, filepath.FromSlash(action.Path))
			fi, err := os.Stat(dst)
			if err != nil {
				continue
			}
			next.Files[action.Path] = FileState{
				Size:    action.Target.Size,
				ModTime: fi.ModTime().UnixNano(),
				Sha256:  action.Target.Sha256,
			}
		}
	}
	return next
}

func writeJournal(stateDir string, header journalHeader, entries []journalEntry) error {
	tmp, err := os.CreateTemp(stateDir, ".journal-*.tmp")
	if err != nil {
		return fmt.Errorf("创建更新日志失败: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	enc := json.NewEncoder(tmp)
	if err := enc.Encode(header); err != nil {
		tmp.Close()
		return err
	}
	for _, entry := range entries {
		if err := enc.Encode(entry); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(stateDir, journalName))
}

func writeIndexTemp(root string, idx *Index) (string, error) {
	raw, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(StateDir(root), ".installed-*.tmp")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		os.Remove(name)
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// Recover 在启动或更新前回滚未完成的替换。
func Recover(root string) error {
	stateDir := StateDir(root)
	journalPath := filepath.Join(stateDir, journalName)
	raw, err := os.ReadFile(journalPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取更新日志失败: %w", err)
	}
	lines := bytes.Split(raw, []byte("\n"))
	var header journalHeader
	if len(lines) == 0 || len(bytes.TrimSpace(lines[0])) == 0 {
		return errors.New("更新日志为空")
	}
	if err := json.Unmarshal(bytes.TrimSpace(lines[0]), &header); err != nil {
		return fmt.Errorf("更新日志头部损坏: %w", err)
	}
	backupRel, err := manifest.CleanRelPath(header.BackupDir)
	if err != nil {
		return fmt.Errorf("更新日志备份路径非法: %w", err)
	}
	backupRoot := filepath.Join(stateDir, filepath.FromSlash(backupRel))
	var entries []journalEntry
	for _, line := range lines[1:] {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var entry journalEntry
		if err := json.Unmarshal(bytes.TrimSpace(line), &entry); err != nil {
			return fmt.Errorf("更新日志条目损坏: %w", err)
		}
		entries = append(entries, entry)
	}

	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		rel, err := manifest.CleanRelPath(entry.Path)
		if err != nil {
			return err
		}
		dst := filepath.Join(root, filepath.FromSlash(rel))
		backup := filepath.Join(backupRoot, filepath.FromSlash(rel))
		if _, err := os.Stat(backup); err == nil {
			if err := os.RemoveAll(dst); err != nil && !os.IsNotExist(err) {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := os.Rename(backup, dst); err != nil {
				return fmt.Errorf("%s: 恢复备份失败: %w", rel, err)
			}
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if entry.HadBackup {
			if _, err := os.Stat(dst); err == nil {
				continue
			}
			return fmt.Errorf("%s: 备份缺失，无法回滚", rel)
		}
		if entry.Op == "replace" {
			if err := os.RemoveAll(dst); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	if err := os.Remove(journalPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	// 备份文件已全部移回实例目录，这份快照不再有效，清理掉避免版本历史出现脏条目。
	_ = os.RemoveAll(backupRoot)
	_ = removeState(root)
	_ = os.RemoveAll(filepath.Join(stateDir, stagingDirName))
	return nil
}

func pruneBackups(root string) {
	dir := filepath.Join(StateDir(root), backupsDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for len(names) > keepBackups {
		_ = os.RemoveAll(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}

func hashBytes(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
