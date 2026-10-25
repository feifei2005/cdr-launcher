package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cdr-launcher/internal/manifest"
)

// backupMeta 是写入 backups/<stamp>/backup.json 的版本历史信息。
type backupMeta struct {
	Version   int            `json:"version"`
	From      string         `json:"from"`
	To        string         `json:"to"`
	CreatedAt string         `json:"createdAt"`
	Consumed  bool           `json:"consumed,omitempty"`
	Entries   []journalEntry `json:"entries"`
}

// BackupView 是展示给前端的版本历史条目。
type BackupView struct {
	Stamp       string `json:"stamp"`
	From        string `json:"from"`
	To          string `json:"to"`
	CreatedAt   string `json:"createdAt"`
	Files       int    `json:"files"`
	Deletes     int    `json:"deletes"`
	Bytes       int64  `json:"bytes"`
	CanRollback bool   `json:"canRollback"`
}

// RollbackResult 是回滚结果。
type RollbackResult struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Restored int    `json:"restored"`
	Removed  int    `json:"removed"`
}

func metaPath(dir string) string {
	return filepath.Join(dir, "backup.json")
}

func loadBackupMeta(dir string) (*backupMeta, error) {
	raw, err := os.ReadFile(metaPath(dir))
	if err != nil {
		return nil, err
	}
	var meta backupMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("备份信息格式错误: %w", err)
	}
	return &meta, nil
}

func writeBackupMeta(dir string, meta backupMeta) error {
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".backup-*.tmp")
	if err != nil {
		return fmt.Errorf("创建备份信息失败: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
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
	return os.Rename(name, metaPath(dir))
}

// createBackupDir 在 backups/ 下创建唯一的版本目录。
func createBackupDir(stateDir string) (string, string, error) {
	parent := filepath.Join(stateDir, backupsDirName)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", "", fmt.Errorf("创建备份目录失败: %w", err)
	}
	base := nowStamp()
	for i := 1; i < 100; i++ {
		stamp := base
		if i > 1 {
			stamp = fmt.Sprintf("%s-%d", base, i)
		}
		dir := filepath.Join(parent, stamp)
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			return stamp, dir, nil
		}
		if !os.IsExist(err) {
			return "", "", fmt.Errorf("创建备份目录失败: %w", err)
		}
	}
	return "", "", errors.New("创建备份目录失败: 名称冲突")
}

// ListBackups 返回版本历史，按时间从新到旧排列。
func ListBackups(root string) ([]BackupView, error) {
	parent := filepath.Join(StateDir(root), backupsDirName)
	entries, err := os.ReadDir(parent)
	if errors.Is(err, os.ErrNotExist) {
		return []BackupView{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取版本历史失败: %w", err)
	}
	out := make([]BackupView, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(parent, entry.Name())
		view := BackupView{Stamp: entry.Name(), Bytes: dirSize(dir)}
		meta, metaErr := loadBackupMeta(dir)
		if metaErr == nil && len(meta.Entries) > 0 {
			view.From = meta.From
			view.To = meta.To
			view.CreatedAt = meta.CreatedAt
			view.CanRollback = !meta.Consumed
			for _, e := range meta.Entries {
				if e.Op == "delete" {
					view.Deletes++
				} else {
					view.Files++
				}
			}
		}
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Stamp > out[j].Stamp })
	return out, nil
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, statErr := d.Info(); statErr == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}

func validStamp(stamp string) bool {
	if stamp == "" || stamp == "." || stamp == ".." {
		return false
	}
	if stamp != filepath.Base(stamp) || strings.ContainsAny(stamp, `/\`) {
		return false
	}
	return true
}

// Rollback 把实例文件恢复到指定备份的快照，并为回滚前状态再生成一份备份，可再次回滚回去。
func Rollback(ctx context.Context, root, stamp string) (RollbackResult, error) {
	if err := Recover(root); err != nil {
		return RollbackResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return RollbackResult{}, err
	}
	if !validStamp(stamp) {
		return RollbackResult{}, errors.New("备份标识非法")
	}
	stateDir := StateDir(root)
	srcRoot := filepath.Join(stateDir, backupsDirName, stamp)
	meta, err := loadBackupMeta(srcRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RollbackResult{}, errors.New("该备份缺少版本信息，无法自动回滚")
		}
		return RollbackResult{}, err
	}
	if len(meta.Entries) == 0 {
		return RollbackResult{}, errors.New("该备份没有可回滚的文件")
	}
	if meta.Consumed {
		return RollbackResult{}, errors.New("该备份已用于回滚，不能重复使用")
	}
	for _, e := range meta.Entries {
		rel, err := manifest.CleanRelPath(e.Path)
		if err != nil {
			return RollbackResult{}, err
		}
		if e.Op == "delete" || e.HadBackup {
			if _, err := os.Stat(filepath.Join(srcRoot, filepath.FromSlash(rel))); err != nil {
				return RollbackResult{}, fmt.Errorf("备份文件缺失，无法回滚: %s", rel)
			}
		}
	}
	idx, err := LoadIndex(root)
	if err != nil {
		return RollbackResult{}, err
	}
	current := idx.Version

	newStamp, newRoot, err := createBackupDir(stateDir)
	if err != nil {
		return RollbackResult{}, err
	}
	newRel := filepath.ToSlash(filepath.Join(backupsDirName, newStamp))

	entries := make([]journalEntry, 0, len(meta.Entries))
	for _, e := range meta.Entries {
		rel, err := manifest.CleanRelPath(e.Path)
		if err != nil {
			_ = os.RemoveAll(newRoot)
			return RollbackResult{}, err
		}
		switch e.Op {
		case "replace", "delete":
		default:
			_ = os.RemoveAll(newRoot)
			return RollbackResult{}, fmt.Errorf("备份包含未知操作: %s", e.Op)
		}
		// 更新新增的文件在回滚前存在，需要先备份；被更新删除的文件回滚前不存在。
		had := e.Op == "replace"
		if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); statErr != nil {
			if os.IsNotExist(statErr) {
				had = false
			} else {
				_ = os.RemoveAll(newRoot)
				return RollbackResult{}, statErr
			}
		}
		entries = append(entries, journalEntry{Op: "replace", Path: rel, Backup: rel, HadBackup: had})
	}

	newMeta := backupMeta{
		Version:   1,
		From:      current,
		To:        meta.From,
		CreatedAt: time.Now().Format(time.RFC3339),
		Entries:   entries,
	}
	if err := writeBackupMeta(newRoot, newMeta); err != nil {
		_ = os.RemoveAll(newRoot)
		return RollbackResult{}, err
	}
	header := journalHeader{Version: 1, BackupDir: newRel, From: current, To: meta.From}
	if err := writeJournal(stateDir, header, entries); err != nil {
		_ = os.RemoveAll(newRoot)
		return RollbackResult{}, err
	}

	fail := func(cause error) (RollbackResult, error) {
		if rollbackErr := Recover(root); rollbackErr != nil {
			return RollbackResult{}, fmt.Errorf("%w；恢复回滚前状态失败: %v", cause, rollbackErr)
		}
		return RollbackResult{}, cause
	}

	restored, removed := 0, 0
	for _, e := range meta.Entries {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		rel, err := manifest.CleanRelPath(e.Path)
		if err != nil {
			return fail(err)
		}
		dst := filepath.Join(root, filepath.FromSlash(rel))
		src := filepath.Join(srcRoot, filepath.FromSlash(rel))
		forward := filepath.Join(newRoot, filepath.FromSlash(rel))

		if _, err := os.Lstat(dst); err == nil {
			if err := os.MkdirAll(filepath.Dir(forward), 0o755); err != nil {
				return fail(err)
			}
			if err := os.Rename(dst, forward); err != nil {
				return fail(fmt.Errorf("%s: 备份当前文件失败: %w", rel, err))
			}
		} else if !os.IsNotExist(err) {
			return fail(err)
		}

		if _, err := os.Stat(src); err == nil {
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return fail(err)
			}
			if err := os.Rename(src, dst); err != nil {
				return fail(fmt.Errorf("%s: 恢复备份文件失败: %w", rel, err))
			}
			restored++
		} else if !os.IsNotExist(err) {
			return fail(err)
		} else if e.Op == "replace" && !e.HadBackup {
			removed++
		}
	}

	if err := os.Remove(filepath.Join(stateDir, journalName)); err != nil && !os.IsNotExist(err) {
		return fail(err)
	}
	_ = removeState(root)
	_ = os.RemoveAll(filepath.Join(stateDir, stagingDirName))

	next, err := Refresh(root, nil, nil)
	if err != nil {
		return RollbackResult{}, fmt.Errorf("回滚完成，但重建本地索引失败: %w", err)
	}
	next.Version = meta.From
	next.InstalledAt = time.Now().Format(time.RFC3339)
	if err := SaveIndex(root, next); err != nil {
		return RollbackResult{}, fmt.Errorf("回滚完成，但保存本地索引失败: %w", err)
	}
	pruneBackups(root)
	meta.Consumed = true
	_ = writeBackupMeta(srcRoot, *meta)
	return RollbackResult{From: current, To: meta.From, Restored: restored, Removed: removed}, nil
}
