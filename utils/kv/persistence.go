package kv

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Save 将整个数据库保存为 JSON，通过同目录临时文件替换目标文件。
// 保存期间阻塞此 DB 的其他操作；不创建父目录，文件系统错误返回 error。
func (db *DB) Save(path string) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	content, err := json.Marshal(db.data)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".kv-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(content); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// Load 从 JSON 文件整体替换数据库；读取、解析或数据校验失败时保留原数据。
// 加载期间阻塞此 DB 的其他操作；文件不存在返回包装 os.ErrNotExist 的错误。
func (db *DB) Load(path string) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var next *snapshot
	if err = json.Unmarshal(content, &next); err != nil {
		return err
	}
	if next == nil {
		return fmt.Errorf("kv: null snapshot")
	}
	if err = next.validate(); err != nil {
		return err
	}
	db.data = *next
	return nil
}

func (s *snapshot) validate() error {
	for key, kind := range s.Types {
		valid := false
		switch kind {
		case "string":
			_, valid = s.Strings[key]
		case "hash":
			valid = len(s.Hashes[key]) > 0
		case "list":
			valid = len(s.Lists[key]) > 0
		}
		if !valid {
			return fmt.Errorf("kv: invalid type or missing value for key %q", key)
		}
	}
	for key := range s.Strings {
		if s.Types[key] != "string" {
			return fmt.Errorf("kv: inconsistent string key %q", key)
		}
	}
	for key := range s.Hashes {
		if s.Types[key] != "hash" {
			return fmt.Errorf("kv: inconsistent hash key %q", key)
		}
	}
	for key := range s.Lists {
		if s.Types[key] != "list" {
			return fmt.Errorf("kv: inconsistent list key %q", key)
		}
	}
	return nil
}
