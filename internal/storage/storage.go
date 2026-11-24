// Package storage 提供对象存储抽象，第一版只有本机磁盘实现。
// 应用只认上传/下载/删除/列表四个操作，不直接接触存储细节。
package storage

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"minibase/internal/config"
	"minibase/internal/database"
)

// Store 对象存储接口。将来接 S3 只需换实现，应用代码不用改。
type Store interface {
	Put(appName, fileName, contentType string, data []byte) (string, error)
	Get(appName, id string) ([]byte, string, error)
	Delete(appName, id string) error
	List(appName string) ([]*database.Object, error)
}

// LocalStore 本机磁盘实现。
type LocalStore struct {
	cfg   *config.Config
	store *database.Store
}

func NewLocalStore(cfg *config.Config, store *database.Store) *LocalStore {
	return &LocalStore{cfg: cfg, store: store}
}

func (l *LocalStore) dir(appName string) string {
	return filepath.Join(l.cfg.DataDir, "objects", appName)
}

var bannedExt = map[string]bool{
	".exe": true, ".sh": true, ".bash": true, ".bin": true,
	".so": true, ".dll": true, ".app": true, ".deb": true,
	".rpm": true, ".msi": true, ".bat": true, ".cmd": true,
	".ps1": true, ".com": true, ".scr": true,
}

var bannedMagic = [][]byte{{0x7f, 'E', 'L', 'F'}, {'M', 'Z'}, {'#', '!', '/'}}

func allowedFile(fileName string, data []byte) error {
	ext := strings.ToLower(filepath.Ext(fileName))
	if bannedExt[ext] {
		return fmt.Errorf("禁止上传可执行文件: %s", ext)
	}
	for _, m := range bannedMagic {
		if len(data) >= len(m) && string(data[:len(m)]) == string(m) {
			return fmt.Errorf("禁止上传可执行文件（文件头）")
		}
	}
	return nil
}

func randomFileID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (l *LocalStore) Put(appName, fileName, contentType string, data []byte) (string, error) {
	if err := allowedFile(fileName, data); err != nil {
		return "", err
	}
	if int64(len(data)) > l.cfg.StorageMaxFileMB*1024*1024 {
		return "", fmt.Errorf("文件大小超过上限 %d MB", l.cfg.StorageMaxFileMB)
	}
	appSize, err := l.store.AppObjectSize(appName)
	if err != nil {
		return "", err
	}
	if appSize+int64(len(data)) > l.cfg.StorageMaxMBPerApp*1024*1024 {
		return "", fmt.Errorf("应用存储容量已满")
	}
	total, err := l.store.TotalObjectSize()
	if err != nil {
		return "", err
	}
	if total+int64(len(data)) > l.cfg.StorageMaxMBTotal*1024*1024 {
		return "", fmt.Errorf("系统存储容量已满")
	}
	id := randomFileID()
	dir := l.dir(appName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, id), data, 0o644); err != nil {
		return "", err
	}
	obj := &database.Object{ID: id, AppName: appName, FileName: fileName,
		Size: int64(len(data)), ContentType: contentType, CreatedAt: time.Now().Unix()}
	if err := l.store.InsertObject(obj); err != nil {
		_ = os.Remove(filepath.Join(dir, id))
		return "", err
	}
	return id, nil
}

func (l *LocalStore) Get(appName, id string) ([]byte, string, error) {
	obj, err := l.store.GetObject(appName, id)
	if err != nil || obj == nil {
		return nil, "", fmt.Errorf("文件不存在")
	}
	data, err := os.ReadFile(filepath.Join(l.dir(appName), id))
	return data, obj.ContentType, err
}

func (l *LocalStore) Delete(appName, id string) error {
	obj, err := l.store.GetObject(appName, id)
	if err != nil || obj == nil {
		return fmt.Errorf("文件不存在")
	}
	path := filepath.Join(l.dir(appName), id)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return l.store.DeleteObject(appName, id)
}

func (l *LocalStore) List(appName string) ([]*database.Object, error) {
	return l.store.ListObjects(appName)
}
