package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cdr-launcher/internal/manifest"
)

const (
	// StateDirName 是启动器在游戏目录内维护的更新状态目录。
	StateDirName  = ".cdrlauncher"
	indexFileName = "installed.json"
)

// FileState 是本地文件的缓存索引，size+modTime 未变时复用 sha256。
type FileState struct {
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"`
	Sha256  string `json:"sha256"`
}

// Index 记录本地实例当前版本与文件哈希。
type Index struct {
	Version     string               `json:"version"`
	InstalledAt string               `json:"installedAt"`
	Files       map[string]FileState `json:"files"`
}

// StateDir 返回实例内的启动器状态目录。
func StateDir(root string) string {
	return filepath.Join(root, StateDirName)
}

func indexPath(root string) string {
	return filepath.Join(StateDir(root), indexFileName)
}

// LoadIndex 读取本地索引，文件不存在时返回空索引。
func LoadIndex(root string) (*Index, error) {
	raw, err := os.ReadFile(indexPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return &Index{Files: map[string]FileState{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取本地索引失败: %w", err)
	}
	var idx Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("本地索引格式错误: %w", err)
	}
	if idx.Files == nil {
		idx.Files = map[string]FileState{}
	}
	return &idx, nil
}

// SaveIndex 以原子方式写入本地索引。
func SaveIndex(root string, idx *Index) error {
	if idx.Files == nil {
		idx.Files = map[string]FileState{}
	}
	dir := StateDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建状态目录失败: %w", err)
	}
	raw, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化本地索引失败: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".installed-*.tmp")
	if err != nil {
		return fmt.Errorf("创建索引临时文件失败: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("写入本地索引失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("刷新本地索引失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭本地索引失败: %w", err)
	}
	if err := os.Rename(name, indexPath(root)); err != nil {
		return fmt.Errorf("保存本地索引失败: %w", err)
	}
	return nil
}

// Refresh 遍历实例目录刷新文件索引。受保护路径不进入索引。
func Refresh(root string, prev *Index, progress func(done int64)) (*Index, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("解析实例目录失败: %w", err)
	}
	if info, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("实例目录不可访问: %w", err)
	} else if !info.IsDir() {
		return nil, errors.New("实例目录不是文件夹")
	}

	next := &Index{}
	if prev != nil {
		next.Version = prev.Version
		next.InstalledAt = prev.InstalledAt
	}
	next.Files = map[string]FileState{}
	patterns := manifest.DefaultProtected

	var done int64
	err = filepath.WalkDir(abs, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == abs {
			return nil
		}
		rel, relErr := filepath.Rel(abs, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == StateDirName || isProtected(rel, patterns) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if isProtected(rel, patterns) {
			return nil
		}
		fi, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		st := FileState{Size: fi.Size(), ModTime: fi.ModTime().UnixNano()}
		if prev != nil {
			if cached, ok := prev.Files[rel]; ok && cached.Size == st.Size && cached.ModTime == st.ModTime && cached.Sha256 != "" {
				st.Sha256 = cached.Sha256
				next.Files[rel] = st
				done++
				if progress != nil {
					progress(done)
				}
				return nil
			}
		}
		sum, hashErr := hashFile(p)
		if hashErr != nil {
			return fmt.Errorf("计算 %s 哈希失败: %w", rel, hashErr)
		}
		st.Sha256 = sum
		next.Files[rel] = st
		done++
		if progress != nil {
			progress(done)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return next, nil
}

func isProtected(rel string, patterns []string) bool {
	for _, pattern := range patterns {
		if manifest.Match(pattern, rel) || manifest.Match(pattern, rel+"/x") {
			return true
		}
	}
	return false
}

func isUserEditable(rel string, base []string, extra []string) bool {
	for _, pattern := range base {
		if manifest.Match(pattern, rel) {
			return true
		}
	}
	for _, pattern := range extra {
		if manifest.Match(pattern, rel) {
			return true
		}
	}
	return false
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 1<<20)
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// verifyFile 校验文件存在且大小、sha256 与期望一致。
func verifyFile(path string, size int64, sum string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("文件不可访问: %w", err)
	}
	if fi.IsDir() {
		return errors.New("路径是文件夹")
	}
	if size >= 0 && fi.Size() != size {
		return fmt.Errorf("大小不符: %d != %d", fi.Size(), size)
	}
	got, err := hashFile(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, sum) {
		return fmt.Errorf("哈希不符: %s != %s", got, sum)
	}
	return nil
}

func validSHA(sum string) bool {
	if len(sum) != 64 {
		return false
	}
	for _, c := range sum {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

func nowStamp() string {
	return time.Now().Format("20060102-150405")
}
