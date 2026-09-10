// Package kv 提供并发安全的内存 KV 数据库，支持 string、hash、list 和 JSON 快照。
package kv

import (
	"errors"
	"maps"
	"slices"
	"sync"
)

// ErrNotFound 表示 key、hash 字段不存在，或列表已空。
var ErrNotFound = errors.New("kv: not found")

// ErrWrongType 表示 key 的现有类型与操作要求不符。
var ErrWrongType = errors.New("kv: wrong type")

// DB 是独立的内存数据库。零值可用，所有方法并发安全，使用后不得复制。
type DB struct {
	mu   sync.RWMutex
	data snapshot
	// 容量和 LRU 元数据、sweeper 生命周期若以后需要，在此添加；当前无后台任务。
}

type snapshot struct {
	Types   map[string]string            `json:"types"`
	Strings map[string]string            `json:"strings"`
	Hashes  map[string]map[string]string `json:"hashes"`
	Lists   map[string][]string          `json:"lists"`
}

func (db *DB) init() {
	if db.data.Types == nil {
		db.data.Types = make(map[string]string)
	}
	if db.data.Strings == nil {
		db.data.Strings = make(map[string]string)
	}
	if db.data.Hashes == nil {
		db.data.Hashes = make(map[string]map[string]string)
	}
	if db.data.Lists == nil {
		db.data.Lists = make(map[string][]string)
	}
}

func (db *DB) check(key, kind string) error {
	if actual, ok := db.data.Types[key]; ok && actual != kind {
		return ErrWrongType
	}
	return nil
}

func (db *DB) remove(key string) {
	delete(db.data.Types, key)
	delete(db.data.Strings, key)
	delete(db.data.Hashes, key)
	delete(db.data.Lists, key)
}

// Set 设置字符串，覆盖 key 的任意原有类型和值。
func (db *DB) Set(key, value string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.init()
	db.remove(key)
	db.data.Types[key] = "string"
	db.data.Strings[key] = value
}

// Get 读取字符串；不存在返回 ErrNotFound，类型不符返回 ErrWrongType。
func (db *DB) Get(key string) (string, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.check(key, "string"); err != nil {
		return "", err
	}
	value, ok := db.data.Strings[key]
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

// Keys 返回全部类型的 key，按字典序排列，不支持模式匹配。
func (db *DB) Keys() []string {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return sortedKeys(db.data.Types)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// Delete 删除任意类型的 key，返回实际删除数量；忽略重复和不存在的 key。
func (db *DB) Delete(keys ...string) int {
	db.mu.Lock()
	defer db.mu.Unlock()
	count := 0
	for _, key := range keys {
		if _, ok := db.data.Types[key]; ok {
			db.remove(key)
			count++
		}
	}
	return count
}

// HSet 设置一个 hash 字段；key 不存在时创建，类型不符返回 ErrWrongType。
func (db *DB) HSet(key, field, value string) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.check(key, "hash"); err != nil {
		return err
	}
	db.init()
	if db.data.Hashes[key] == nil {
		db.data.Hashes[key] = make(map[string]string)
	}
	db.data.Types[key] = "hash"
	db.data.Hashes[key][field] = value
	return nil
}

// HGet 读取 hash 字段；key 或字段不存在返回 ErrNotFound，类型不符返回 ErrWrongType。
func (db *DB) HGet(key, field string) (string, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.check(key, "hash"); err != nil {
		return "", err
	}
	value, ok := db.data.Hashes[key][field]
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

// HGetAll 返回 hash 的副本；key 不存在返回空 map，类型不符返回 ErrWrongType。
func (db *DB) HGetAll(key string) (map[string]string, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.check(key, "hash"); err != nil {
		return nil, err
	}
	result := make(map[string]string, len(db.data.Hashes[key]))
	maps.Copy(result, db.data.Hashes[key])
	return result, nil
}

// HKeys 返回按字典序排列的 hash 字段；key 不存在返回空切片，类型不符返回 ErrWrongType。
func (db *DB) HKeys(key string) ([]string, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.check(key, "hash"); err != nil {
		return nil, err
	}
	return sortedKeys(db.data.Hashes[key]), nil
}

// RPush 将值依次追加到列表右侧，返回长度；无值时不创建 key，类型不符返回 ErrWrongType。
func (db *DB) RPush(key string, values ...string) (int, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.check(key, "list"); err != nil {
		return 0, err
	}
	if len(values) > 0 {
		db.init()
		db.data.Types[key] = "list"
		db.data.Lists[key] = append(db.data.Lists[key], values...)
	}
	return len(db.data.Lists[key]), nil
}

// LPop 弹出左侧元素，弹空时删除 key；不存在返回 ErrNotFound，类型不符返回 ErrWrongType。
func (db *DB) LPop(key string) (string, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.check(key, "list"); err != nil {
		return "", err
	}
	list := db.data.Lists[key]
	if len(list) == 0 {
		return "", ErrNotFound
	}
	value := list[0]
	list[0] = ""
	if len(list) == 1 {
		db.remove(key)
	} else {
		db.data.Lists[key] = list[1:]
	}
	return value, nil
}

// LRange 返回包含 start、stop 的区间副本；负下标从末尾计数，越界裁剪，空区间返回空切片。
// key 不存在返回空切片，类型不符返回 ErrWrongType。
func (db *DB) LRange(key string, start, stop int) ([]string, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.check(key, "list"); err != nil {
		return nil, err
	}
	list := db.data.Lists[key]
	n := len(list)
	if start < 0 {
		start += n
	}
	if stop < 0 {
		stop += n
	}
	if start < 0 {
		start = 0
	}
	if stop >= n {
		stop = n - 1
	}
	if start >= n || stop < 0 || start > stop {
		return []string{}, nil
	}
	return append([]string{}, list[start:stop+1]...), nil
}
