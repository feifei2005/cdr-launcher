package manifest

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// 补丁使用“旧文件内容作为原始字典”的 zstd 帧，效果等价于 zstd --patch-from。
// 字典 ID 固定，解码端必须提供同一份旧文件内容。
const PatchDictID = 0x43445231

const (
	// 小于 1MB 的文件不值得做差分。
	DefaultMinPatchSize = 1 << 20
	// 补丁小于新文件的 70% 才记录。
	DefaultMaxPatchRatio = 0.7
	patchWindow          = 1 << 26
)

type DiffOptions struct {
	FromRoot string
	ToRoot   string
	From     string
	To       string
	BlobsDir string
	Exclude  []string
	MinSize  int64
	MaxRatio float64
}

// Diff 生成从旧版本到新版本的补丁，并返回补丁列表与删除列表。
// 补丁按内容哈希写入 BlobsDir（文件名即补丁 sha256）。
func Diff(opts DiffOptions) ([]Patch, []string, error) {
	if opts.FromRoot == "" || opts.ToRoot == "" {
		return nil, nil, errors.New("from-root 与 to-root 不能为空")
	}
	if opts.BlobsDir == "" {
		return nil, nil, errors.New("补丁输出目录不能为空")
	}
	minSize := opts.MinSize
	if minSize <= 0 {
		minSize = DefaultMinPatchSize
	}
	maxRatio := opts.MaxRatio
	if maxRatio <= 0 || maxRatio >= 1 {
		maxRatio = DefaultMaxPatchRatio
	}

	oldFiles, err := Build(BuildOptions{Root: opts.FromRoot, Exclude: opts.Exclude, SkipBlobs: true})
	if err != nil {
		return nil, nil, fmt.Errorf("扫描旧版本失败: %w", err)
	}
	newFiles, err := Build(BuildOptions{Root: opts.ToRoot, Exclude: opts.Exclude, SkipBlobs: true})
	if err != nil {
		return nil, nil, fmt.Errorf("扫描新版本失败: %w", err)
	}
	oldByPath := make(map[string]File, len(oldFiles))
	for _, f := range oldFiles {
		oldByPath[f.Path] = f
	}

	if err := os.MkdirAll(opts.BlobsDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("创建补丁目录失败: %w", err)
	}

	var patches []Patch
	for _, next := range newFiles {
		prev, ok := oldByPath[next.Path]
		if !ok || prev.Sha256 == next.Sha256 || prev.Size < minSize {
			continue
		}
		patch, err := makePatch(opts, prev, next)
		if err != nil {
			return nil, nil, err
		}
		if patch == nil {
			continue
		}
		if float64(patch.Size) >= float64(next.Size)*maxRatio {
			os.Remove(filepath.Join(opts.BlobsDir, patch.Sha256))
			continue
		}
		patches = append(patches, *patch)
	}
	sort.Slice(patches, func(i, j int) bool { return patches[i].Path < patches[j].Path })
	return patches, Deletes(oldFiles, newFiles), nil
}

func makePatch(opts DiffOptions, prev, next File) (*Patch, error) {
	oldPath := filepath.Join(opts.FromRoot, filepath.FromSlash(prev.Path))
	newPath := filepath.Join(opts.ToRoot, filepath.FromSlash(next.Path))
	oldContent, err := os.ReadFile(oldPath)
	if err != nil {
		return nil, fmt.Errorf("读取旧文件 %s 失败: %w", prev.Path, err)
	}
	src, err := os.Open(newPath)
	if err != nil {
		return nil, fmt.Errorf("读取新文件 %s 失败: %w", next.Path, err)
	}
	defer src.Close()

	tmp, err := os.CreateTemp(opts.BlobsDir, ".patch-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("创建补丁临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := EncodePatch(oldContent, src, tmp); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("压缩 %s 失败: %w", next.Path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}

	sum, err := fileSHA256(tmpName)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(tmpName)
	if err != nil {
		return nil, err
	}
	dst := filepath.Join(opts.BlobsDir, sum)
	if _, err := os.Stat(dst); err != nil {
		if err := os.Rename(tmpName, dst); err != nil {
			return nil, fmt.Errorf("保存补丁失败: %w", err)
		}
	}
	return &Patch{
		From:         opts.From,
		To:           opts.To,
		Path:         next.Path,
		Size:         fi.Size(),
		Sha256:       sum,
		TargetSize:   next.Size,
		TargetSha256: next.Sha256,
		SourceSize:   prev.Size,
		SourceSha256: prev.Sha256,
		Algo:         PatchAlgo,
	}, nil
}

// EncodePatch 以 oldContent 为原始字典压缩新文件内容。
func EncodePatch(oldContent []byte, src io.Reader, dst io.Writer) error {
	enc, err := zstd.NewWriter(dst,
		zstd.WithEncoderLevel(zstd.SpeedBestCompression),
		zstd.WithEncoderCRC(true),
		zstd.WithWindowSize(patchWindow),
		zstd.WithEncoderDictRaw(PatchDictID, oldContent),
	)
	if err != nil {
		return fmt.Errorf("初始化补丁编码器失败: %w", err)
	}
	if _, err := io.Copy(enc, src); err != nil {
		enc.Close()
		return err
	}
	return enc.Close()
}

// ApplyPatch 用旧文件内容还原新文件，返回解压后的字节。
func ApplyPatch(patch io.Reader, oldContent []byte) ([]byte, error) {
	dec, err := zstd.NewReader(patch,
		zstd.WithDecoderDictRaw(PatchDictID, oldContent),
		zstd.WithDecoderMaxMemory(uint64(1)<<31),
		zstd.WithDecoderMaxWindow(uint64(patchWindow)*2),
	)
	if err != nil {
		return nil, fmt.Errorf("初始化补丁解码器失败: %w", err)
	}
	defer dec.Close()
	out, err := io.ReadAll(dec)
	if err != nil {
		return nil, fmt.Errorf("解压补丁失败: %w", err)
	}
	return out, nil
}

// CleanRelPath 规范化清单中的路径，拒绝绝对路径、盘符与越界路径。
func CleanRelPath(p string) (string, error) {
	clean := path.Clean(filepath.ToSlash(p))
	if clean == "." || clean == ".." || path.IsAbs(clean) || len(clean) > 1 && clean[0] == '/' {
		return "", fmt.Errorf("非法路径: %q", p)
	}
	if len(clean) > 3 && clean[:3] == "../" {
		return "", fmt.Errorf("非法路径: %q", p)
	}
	// 拒绝盘符（C:）与 NTFS 备用数据流（file:stream）。
	if strings.ContainsRune(clean, ':') {
		return "", fmt.Errorf("非法路径: %q", p)
	}
	return clean, nil
}
