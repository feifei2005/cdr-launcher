package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Store 读写加密后的账号文件。
type Store struct {
	path string
}

// NewStore 创建账号存储，path 通常为 %APPDATA%\CDRLauncher\accounts.dat。
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Path 返回账号文件路径。
func (s *Store) Path() string {
	return s.path
}

// Load 读取并解密账号列表；文件不存在时返回空列表。
func (s *Store) Load() ([]Account, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取账号文件失败: %w", err)
	}
	plain, err := unprotect(raw)
	if err != nil {
		return nil, fmt.Errorf("解密账号文件失败: %w", err)
	}
	var list []Account
	if err := json.Unmarshal(plain, &list); err != nil {
		return nil, fmt.Errorf("账号文件已损坏: %w", err)
	}
	return list, nil
}

// Save 加密并原子写入账号列表。
func (s *Store) Save(list []Account) error {
	if list == nil {
		list = []Account{}
	}
	plain, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化账号失败: %w", err)
	}
	sealed, err := protect(plain)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建账号目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".accounts-*.tmp")
	if err != nil {
		return fmt.Errorf("创建账号临时文件失败: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(sealed); err != nil {
		tmp.Close()
		return fmt.Errorf("写入账号文件失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("刷新账号文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭账号文件失败: %w", err)
	}
	if err := os.Rename(name, s.path); err != nil {
		return fmt.Errorf("保存账号文件失败: %w", err)
	}
	return nil
}
