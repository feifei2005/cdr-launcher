package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// 协议版本与默认值。
const (
	FormatVersion = 2
	PatchAlgo     = "zstd-raw-dict"
	DefaultPackID = "create-delight-remake"
)

// 默认排除的玩家状态与运行时文件，发布基线不应包含它们。
var DefaultProtected = []string{
	"saves/**",
	"logs/**",
	"crash-reports/**",
	"options.txt",
	"servers.dat",
	"servers.dat_old",
	"screenshots/**",
	"simplebackups/**",
	"xaero/**",
	"usercache.json",
	"usernamecache.json",
	"launcher_profiles.json",
	"ops.json",
	"patchouli_data.json",
	".hmcl/**",
	".cdrlauncher/**",
	".mixin.out/**",
	"natives-*/**",
	"**/*.jar.disabled",
	"**/*.part",
	// 玩家本地生成的缓存、备份与界面状态，发布基线不包含、更新时也不触碰。
	"CustomSkinLoader/**",
	"farsightpreview/**",
	"modernfix/**",
	"local/**",
	"tacz_backup/**",
	"XaeroWaypoints_BACKUP*/**",
	"_modbackup-*/**",
	"TrashSlotSaveState.json",
	"rhino.local.properties",
	// 启动器维护的玩家侧记录与暂存目录，不属于发布基线。
	"user/**",
	"mods-removed/**",
}

// 允许玩家改动、更新时默认跳过的目录。
var DefaultUserEditable = []string{
	"config/**",
	"defaultconfigs/**",
}

type GameInfo struct {
	MC        string `json:"mc"`
	Forge     string `json:"forge"`
	JavaMajor int    `json:"javaMajor"`
}

type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Sha256 string `json:"sha256"`
}

type Patch struct {
	From         string `json:"from"`
	To           string `json:"to"`
	Path         string `json:"path"`
	Size         int64  `json:"size"`
	Sha256       string `json:"sha256"`
	TargetSize   int64  `json:"targetSize"`
	TargetSha256 string `json:"targetSha256"`
	SourceSize   int64  `json:"sourceSize"`
	SourceSha256 string `json:"sourceSha256"`
	Algo         string `json:"algo"`
}

type Manifest struct {
	Format       int      `json:"format"`
	PackID       string   `json:"packId"`
	Channel      string   `json:"channel"`
	Version      string   `json:"version"`
	PublishedAt  string   `json:"publishedAt"`
	MinLauncher  string   `json:"minLauncher,omitempty"`
	BaseURL      string   `json:"baseUrl"`
	Game         GameInfo `json:"game"`
	Files        []File   `json:"files"`
	Patches      []Patch  `json:"patches,omitempty"`
	Deletes      []string `json:"deletes,omitempty"`
	Protected    []string `json:"protected,omitempty"`
	UserEditable []string `json:"userEditable,omitempty"`
	Signature    string   `json:"sig,omitempty"`
}

// Load 读取 manifest 文件。
func Load(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 manifest 失败: %w", err)
	}
	return Parse(raw)
}

// Parse 解析 manifest 字节内容。
func Parse(raw []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("解析 manifest 失败: %w", err)
	}
	if m.Format != FormatVersion {
		return nil, fmt.Errorf("不支持的 manifest 格式: %d（需要 %d）", m.Format, FormatVersion)
	}
	return &m, nil
}

// Write 以原子方式写入 manifest。
func (m *Manifest) Write(path string) error {
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 manifest 失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("写入 manifest 失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("刷新 manifest 失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭 manifest 失败: %w", err)
	}
	return os.Rename(name, path)
}

// Match 判断斜杠分隔的相对路径是否匹配 glob（支持 *、**、?）。
func Match(pattern, rel string) bool {
	return globRegexp(pattern).MatchString(rel)
}

var globCache = map[string]*regexp.Regexp{}

func globRegexp(pattern string) *regexp.Regexp {
	if re, ok := globCache[pattern]; ok {
		return re
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteString("$")
	re := regexp.MustCompile(b.String())
	globCache[pattern] = re
	return re
}

type BuildOptions struct {
	Root      string
	Exclude   []string
	BlobsDir  string
	SkipBlobs bool
	// Progress 在每个文件处理完成后回调，可为 nil。
	Progress func(rel string, size int64)
}

// Build 遍历 Root 计算文件清单；Exclude 合并默认排除项。
// BlobsDir 非空时把文件按内容哈希写入（优先硬链接，失败回退复制）。
func Build(opts BuildOptions) ([]File, error) {
	if opts.Root == "" {
		return nil, errors.New("root 不能为空")
	}
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, fmt.Errorf("解析 root 失败: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("root 不可访问: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("root 不是目录")
	}

	excludes := append(append([]string{}, DefaultProtected...), opts.Exclude...)
	var files []File
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == root {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("拒绝符号链接: %s", p)
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = path.Clean(filepath.ToSlash(rel))
		for _, pattern := range excludes {
			if Match(pattern, rel) {
				return nil
			}
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		sum, err := fileSHA256(p)
		if err != nil {
			return err
		}
		if !opts.SkipBlobs && opts.BlobsDir != "" {
			if err := storeBlob(opts.BlobsDir, p, sum, fi.Size()); err != nil {
				return fmt.Errorf("写入 blob %s 失败: %w", rel, err)
			}
		}
		files = append(files, File{Path: rel, Size: fi.Size(), Sha256: sum})
		if opts.Progress != nil {
			opts.Progress(rel, fi.Size())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// Deletes 返回旧清单中已被移除的路径。
func Deletes(old, next []File) []string {
	current := make(map[string]bool, len(next))
	for _, f := range next {
		current[f.Path] = true
	}
	var out []string
	for _, f := range old {
		if !current[f.Path] {
			out = append(out, f.Path)
		}
	}
	sort.Strings(out)
	return out
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func storeBlob(dir, src, sum string, size int64) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(dir, sum)
	if fi, err := os.Stat(dst); err == nil {
		if fi.Size() == size {
			return nil
		}
		if err := os.Remove(dst); err != nil {
			return err
		}
	}
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	// 跨卷或文件系统不支持硬链接时复制。
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dir, ".blob-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	buf := make([]byte, 1<<20)
	if _, err := io.CopyBuffer(tmp, in, buf); err != nil {
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
	return os.Rename(name, dst)
}

// Verify 校验 manifest 引用的所有 blob 是否存在且大小、哈希一致。
func Verify(m *Manifest, blobDir string) []error {
	var problems []error
	seen := map[string]bool{}
	for _, f := range m.Files {
		if f.Path == "" || seen[f.Path] {
			problems = append(problems, fmt.Errorf("文件路径为空或重复: %q", f.Path))
			continue
		}
		seen[f.Path] = true
		if err := verifyBlob(blobDir, f.Sha256, f.Size); err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", f.Path, err))
		}
	}
	for _, p := range m.Patches {
		if p.Algo != PatchAlgo {
			problems = append(problems, fmt.Errorf("补丁 %s 算法未知: %s", p.Path, p.Algo))
			continue
		}
		if err := verifyBlob(blobDir, p.Sha256, p.Size); err != nil {
			problems = append(problems, fmt.Errorf("补丁 %s: %w", p.Path, err))
		}
	}
	return problems
}

func verifyBlob(dir, sum string, size int64) error {
	if sum == "" {
		return errors.New("缺少 sha256")
	}
	p := filepath.Join(dir, sum)
	fi, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("缺少 blob %s", sum)
	}
	if fi.Size() != size {
		return fmt.Errorf("blob %s 大小不符: %d != %d", sum, fi.Size(), size)
	}
	got, err := fileSHA256(p)
	if err != nil {
		return err
	}
	if got != sum {
		return fmt.Errorf("blob %s 哈希不符: %s", sum, got)
	}
	return nil
}
