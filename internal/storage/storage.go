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

	"minicloud/internal/config"
	"minicloud/internal/database"
)

// Store 对象存储接口。将来接 S3 只需换实现，应用代码不用改。
type Store interface {
	Put(projectID, appName, fileName, contentType string, data []byte) (string, error)
	Get(projectID, appName, id string) ([]byte, string, error)
	Delete(projectID, appName, id string) error
	List(projectID, appName string) ([]*database.Object, error)
}

// LocalStore 本机磁盘实现。
type LocalStore struct {
	cfg   *config.Config
	store *database.Store
}

// NewLocalStore 创建本机存储。
func NewLocalStore(cfg *config.Config, store *database.Store) *LocalStore {
	return &LocalStore{cfg: cfg, store: store}
}

func (l *LocalStore) dir(projectID, appName string) string {
	return filepath.Join(l.cfg.DataDir, "objects", projectID, appName)
}

// bannedExt 禁止的可执行文件扩展名。
var bannedExt = map[string]bool{
	".exe": true, ".sh": true, ".bash": true, ".bin": true,
	".so": true, ".dll": true, ".app": true, ".deb": true,
	".rpm": true, ".msi": true, ".bat": true, ".cmd": true,
	".ps1": true, ".com": true, ".scr": true,
}

// bannedMagic 禁止的文件头魔数（ELF / PE / shebang 脚本）。
var bannedMagic = [][]byte{
	{0x7f, 'E', 'L', 'F'},
	{'M', 'Z'},
	{'#', '!', '/'},
}

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

func (l *LocalStore) Put(projectID, appName, fileName, contentType string, data []byte) (string, error) {
	if err := allowedFile(fileName, data); err != nil {
		return "", err
	}
	if int64(len(data)) > l.cfg.StorageMaxFileMB*1024*1024 {
		return "", fmt.Errorf("文件大小超过上限 %d MB", l.cfg.StorageMaxFileMB)
	}
	appSize, err := l.store.AppObjectSize(projectID, appName)
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
	dir := l.dir(projectID, appName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, id)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}

	obj := &database.Object{
		ID: id, ProjectID: projectID, AppName: appName,
		FileName: fileName, Size: int64(len(data)), ContentType: contentType,
		CreatedAt: nowUnix(),
	}
	if err := l.store.InsertObject(obj); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return id, nil
}

func (l *LocalStore) Get(projectID, appName, id string) ([]byte, string, error) {
	obj, err := l.store.GetObject(projectID, appName, id)
	if err != nil {
		return nil, "", err
	}
	if obj == nil {
		return nil, "", fmt.Errorf("文件不存在")
	}
	data, err := os.ReadFile(filepath.Join(l.dir(projectID, appName), id))
	if err != nil {
		return nil, "", err
	}
	return data, obj.ContentType, nil
}

func (l *LocalStore) Delete(projectID, appName, id string) error {
	obj, err := l.store.GetObject(projectID, appName, id)
	if err != nil {
		return err
	}
	if obj == nil {
		return fmt.Errorf("文件不存在")
	}
	if err := os.Remove(filepath.Join(l.dir(projectID, appName), id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return l.store.DeleteObject(projectID, appName, id)
}

func (l *LocalStore) List(projectID, appName string) ([]*database.Object, error) {
	return l.store.ListObjects(projectID, appName)
}

func nowUnix() int64 { return time.Now().Unix() }
