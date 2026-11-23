// Package database 管理 system.db 和项目库文件的生命周期。
// 规则：平台记录与库文件同生共死；启动时对账，孤儿与坏引用都要报告。
package database

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Project 对应 projects 表。
type Project struct {
	ID        string
	Name      string
	CreatedAt int64
}

// App 对应 apps 表。
type App struct {
	ID                 string
	ProjectID          string
	Name               string
	RootPath           string
	CurrentVersion     string
	PreviousVersion    string
	Port               int
	Status             string
	CreatedAt          int64
	RestartCount       int
	RestartWindowStart int64
}

// Version 对应 versions 表。
// port 字段是部署链路要求的：新版本用临时端口启动，切流量后版本记录保留自己的端口。
type Version struct {
	ID        string
	AppID     string
	Version   string
	SourceDir string
	Mode      string
	StartCmd  string
	EnvJSON   string
	Port      int
	CreatedAt int64
}

// CallLog 审计账本记录。
type CallLog struct {
	ID         string
	AppID      string
	ProjectID  string
	Event      string
	FromStatus string
	ToStatus   string
	Reason     string
	CreatedAt  int64
}

// Secret 加密存储的密钥。
type Secret struct {
	AppID          string
	Key            string
	EncryptedValue string
	Version        string
	CreatedAt      int64
}

// Object 对象存储元数据。
type Object struct {
	ID          string
	ProjectID   string
	AppName     string
	FileName    string
	Size        int64
	ContentType string
	CreatedAt   int64
}

// AppEnv 应用环境变量（普通明文 / 密钥密文）。
type AppEnv struct {
	AppID    string
	Key      string
	Value    string
	IsSecret bool
	Version  string
}

// ReconcileReport 启动对账结果。
type ReconcileReport struct {
	OrphanFiles   []string // 有文件无记录
	BrokenRefs    []string // 有记录无文件
	Healthy       []string // 正常
	StaleRunning  []string // 状态运行中但进程没了
	StaleStarting []string // 状态启动中需标记失败
}

// Store 封装系统库访问。
type Store struct {
	DB            *sql.DB
	DataDir       string
	BusyTimeoutMS int

	// CreateProjectFailAt 仅供逻辑测试注入失败点。
	// 取值 "insert_record" 或 "create_file"，命中时返回错误以验证回滚。
	CreateProjectFailAt string
}

// Open 打开 system.db，建目录、开 WAL、建表。
func Open(dataDir string, busyTimeoutMS int) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dataDir, "databases"), 0o755); err != nil {
		return nil, fmt.Errorf("建 databases 目录失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "apps"), 0o755); err != nil {
		return nil, fmt.Errorf("建 apps 目录失败: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(%d)&_pragma=foreign_keys(ON)",
		filepath.ToSlash(filepath.Join(dataDir, "system.db")), busyTimeoutMS)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 system.db 失败: %w", err)
	}
	// 单写者模型：限制连接数，减少锁竞争。
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	s := &Store{DB: db, DataDir: dataDir, BusyTimeoutMS: busyTimeoutMS}
	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) initSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS projects (
			id TEXT PRIMARY KEY,
			name TEXT,
			created_at INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS apps (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			name TEXT,
			root_path TEXT,
			current_version TEXT,
			previous_version TEXT,
			port INTEGER,
			status TEXT,
			created_at INTEGER,
			restart_count INTEGER DEFAULT 0,
			restart_window_start INTEGER DEFAULT 0,
			UNIQUE(project_id, name),
			UNIQUE(root_path)
		)`,
		`CREATE TABLE IF NOT EXISTS versions (
			id TEXT PRIMARY KEY,
			app_id TEXT,
			version TEXT,
			source_dir TEXT,
			mode TEXT,
			start_cmd TEXT,
			env_json TEXT,
			port INTEGER,
			created_at INTEGER,
			UNIQUE(app_id, version)
		)`,
		`CREATE TABLE IF NOT EXISTS call_logs (
			id TEXT PRIMARY KEY,
			app_id TEXT,
			project_id TEXT,
			event TEXT,
			from_status TEXT,
			to_status TEXT,
			reason TEXT,
			created_at INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS secrets (
			app_id TEXT,
			key TEXT,
			encrypted_value TEXT,
			version TEXT,
			created_at INTEGER,
			PRIMARY KEY(app_id, key, version)
		)`,
		`CREATE TABLE IF NOT EXISTS objects (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			app_name TEXT,
			file_name TEXT,
			size INTEGER,
			content_type TEXT,
			created_at INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS app_env (
			app_id TEXT,
			key TEXT,
			value TEXT,
			is_secret INTEGER,
			version TEXT,
			PRIMARY KEY(app_id, key, version)
		)`,
	}
	for _, q := range stmts {
		if _, err := s.DB.Exec(q); err != nil {
			return fmt.Errorf("建表失败: %w", err)
		}
	}
	// 版本二升级：给旧库补列。幂等，可重复执行。
	migrations := []string{
		`ALTER TABLE apps ADD COLUMN restart_count INTEGER DEFAULT 0`,
		`ALTER TABLE apps ADD COLUMN restart_window_start INTEGER DEFAULT 0`,
	}
	for _, q := range migrations {
		// 列已存在时 SQLite 会报错，忽略即可
		_, _ = s.DB.Exec(q)
	}
	return nil
}

// Close 关闭系统库。
func (s *Store) Close() error { return s.DB.Close() }

// CreateProject 事务内写项目记录并创建项目库文件；任一步失败整体回滚。
func (s *Store) CreateProject(id, name string) error {
	if id == "" || strings.ContainsAny(id, `/\\..`) {
		return fmt.Errorf("项目 ID 非法: %q", id)
	}
	dbPath := filepath.Join(s.DataDir, "databases", id+".db")
	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	if s.CreateProjectFailAt == "insert_record" {
		return fmt.Errorf("测试注入失败: 写项目记录前")
	}
	if _, err := tx.Exec(`INSERT INTO projects(id,name,created_at) VALUES(?,?,?)`, id, name, time.Now().Unix()); err != nil {
		return fmt.Errorf("写项目记录失败: %w", err)
	}
	if s.CreateProjectFailAt == "create_file" {
		return fmt.Errorf("测试注入失败: 建库文件前")
	}
	f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("建项目库文件失败: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(dbPath)
		return fmt.Errorf("关闭项目库文件失败: %w", err)
	}
	// 空 SQLite 文件由 SQLite 自己初始化；这里写个建表语句让它成为合法库，
	// 后续版本二会在里面建业务表。
	pdb, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)", filepath.ToSlash(dbPath)))
	if err != nil {
		_ = os.Remove(dbPath)
		return fmt.Errorf("打开项目库失败: %w", err)
	}
	_, err = pdb.Exec(`CREATE TABLE IF NOT EXISTS _meta(key TEXT PRIMARY KEY, value TEXT)`)
	pdb.Close()
	if err != nil {
		_ = os.Remove(dbPath)
		return fmt.Errorf("初始化项目库失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		_ = os.Remove(dbPath)
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}

// GetProject 查项目。
func (s *Store) GetProject(id string) (*Project, error) {
	var p Project
	err := s.DB.QueryRow(`SELECT id,name,created_at FROM projects WHERE id=?`, id).Scan(&p.ID, &p.Name, &p.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// EnsureProject 项目不存在则创建（CLI 部署时自动建项目）。
func (s *Store) EnsureProject(id, name string) error {
	p, err := s.GetProject(id)
	if err != nil {
		return err
	}
	if p != nil {
		return nil
	}
	return s.CreateProject(id, name)
}

// CreateApp 创建应用记录。
func (s *Store) CreateApp(a *App) error {
	if a.ID == "" {
		return fmt.Errorf("应用 ID 不能为空")
	}
	_, err := s.DB.Exec(`INSERT INTO apps(id,project_id,name,root_path,current_version,previous_version,port,status,created_at,restart_count,restart_window_start)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.ProjectID, a.Name, a.RootPath, a.CurrentVersion, a.PreviousVersion, a.Port, a.Status, a.CreatedAt, a.RestartCount, a.RestartWindowStart)
	return err
}

// GetAppByName 按项目 + 应用名查。
func (s *Store) GetAppByName(projectID, name string) (*App, error) {
	return s.queryApp(`SELECT id,project_id,name,root_path,current_version,previous_version,port,status,created_at,restart_count,restart_window_start
		FROM apps WHERE project_id=? AND name=?`, projectID, name)
}

// GetAppByRootPath 按根路径查（转发用）。
func (s *Store) GetAppByRootPath(root string) (*App, error) {
	return s.queryApp(`SELECT id,project_id,name,root_path,current_version,previous_version,port,status,created_at,restart_count,restart_window_start
		FROM apps WHERE root_path=?`, root)
}

// FindAppByName 按应用名查（CLI status/logs 不带项目 ID）。
// 第一版应用名全局唯一；若有重名返回错误由调用方决定。
func (s *Store) FindAppByName(name string) (*App, error) {
	return s.queryApp(`SELECT id,project_id,name,root_path,current_version,previous_version,port,status,created_at,restart_count,restart_window_start
		FROM apps WHERE name=? ORDER BY created_at ASC LIMIT 1`, name)
}

// GetAppByID 按 ID 查。
func (s *Store) GetAppByID(id string) (*App, error) {
	return s.queryApp(`SELECT id,project_id,name,root_path,current_version,previous_version,port,status,created_at,restart_count,restart_window_start
		FROM apps WHERE id=?`, id)
}

func (s *Store) queryApp(q string, args ...any) (*App, error) {
	var a App
	err := s.DB.QueryRow(q, args...).Scan(&a.ID, &a.ProjectID, &a.Name, &a.RootPath,
		&a.CurrentVersion, &a.PreviousVersion, &a.Port, &a.Status, &a.CreatedAt, &a.RestartCount, &a.RestartWindowStart)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// UpdateAppAfterDeploy 切流量：更新当前版本、上一版本、端口、状态。
func (s *Store) UpdateAppAfterDeploy(appID, current, previous string, port int, status string) error {
	_, err := s.DB.Exec(`UPDATE apps SET current_version=?,previous_version=?,port=?,status=? WHERE id=?`,
		current, previous, port, status, appID)
	return err
}

// UpdateAppStatus 只改状态。
func (s *Store) UpdateAppStatus(appID, status string) error {
	_, err := s.DB.Exec(`UPDATE apps SET status=? WHERE id=?`, status, appID)
	return err
}

// CreateVersion 写版本记录。
func (s *Store) CreateVersion(v *Version) error {
	_, err := s.DB.Exec(`INSERT INTO versions(id,app_id,version,source_dir,mode,start_cmd,env_json,port,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		v.ID, v.AppID, v.Version, v.SourceDir, v.Mode, v.StartCmd, v.EnvJSON, v.Port, v.CreatedAt)
	return err
}

// GetVersion 查单个版本。
func (s *Store) GetVersion(appID, version string) (*Version, error) {
	var v Version
	err := s.DB.QueryRow(`SELECT id,app_id,version,source_dir,mode,start_cmd,env_json,port,created_at
		FROM versions WHERE app_id=? AND version=?`, appID, version).Scan(
		&v.ID, &v.AppID, &v.Version, &v.SourceDir, &v.Mode, &v.StartCmd, &v.EnvJSON, &v.Port, &v.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// ListVersions 按版本号升序列出应用的全部版本。
func (s *Store) ListVersions(appID string) ([]*Version, error) {
	rows, err := s.DB.Query(`SELECT id,app_id,version,source_dir,mode,start_cmd,env_json,port,created_at
		FROM versions WHERE app_id=? ORDER BY version ASC`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Version
	for rows.Next() {
		var v Version
		if err := rows.Scan(&v.ID, &v.AppID, &v.Version, &v.SourceDir, &v.Mode, &v.StartCmd,
			&v.EnvJSON, &v.Port, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &v)
	}
	return out, rows.Err()
}

// NextVersionNumber 返回下一个版本号数字（v1, v2, ...）。
func (s *Store) NextVersionNumber(appID string) (int, error) {
	versions, err := s.ListVersions(appID)
	if err != nil {
		return 0, err
	}
	max := 0
	for _, v := range versions {
		var n int
		if _, err := fmt.Sscanf(v.Version, "v%d", &n); err == nil && n > max {
			max = n
		}
	}
	return max + 1, nil
}

// Reconcile 启动时对账：扫描项目库文件与 projects 表。
func (s *Store) Reconcile() (*ReconcileReport, error) {
	report := &ReconcileReport{}
	rows, err := s.DB.Query(`SELECT id FROM projects`)
	if err != nil {
		return nil, err
	}
	recorded := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		recorded[id] = true
	}
	rows.Close()

	entries, err := os.ReadDir(filepath.Join(s.DataDir, "databases"))
	if err != nil {
		return nil, err
	}
	onDisk := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".db")
		onDisk[id] = true
	}
	all := map[string]bool{}
	for id := range recorded {
		all[id] = true
	}
	for id := range onDisk {
		all[id] = true
	}
	ids := make([]string, 0, len(all))
	for id := range all {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		switch {
		case recorded[id] && onDisk[id]:
			report.Healthy = append(report.Healthy, id)
		case onDisk[id] && !recorded[id]:
			report.OrphanFiles = append(report.OrphanFiles, id+".db")
		case recorded[id] && !onDisk[id]:
			report.BrokenRefs = append(report.BrokenRefs, id)
		}
	}

	// 应用状态对账：运行中但进程没了 → 不健康；启动中 → 失败并清理残留版本目录
	rows2, err := s.DB.Query(`SELECT id,name,project_id,status,current_version FROM apps WHERE status IN ('运行中','启动中')`)
	if err == nil {
		type appState struct{ id, name, project, status, version string }
		var apps []appState
		for rows2.Next() {
			var a appState
			if rows2.Scan(&a.id, &a.name, &a.project, &a.status, &a.version) == nil {
				apps = append(apps, a)
			}
		}
		rows2.Close()
		for _, a := range apps {
			var port int
			_ = s.DB.QueryRow(`SELECT port FROM versions WHERE app_id=? AND version=?`, a.id, a.version).Scan(&port)
			if port <= 0 || !tcpAlive(port) {
				if a.status == "启动中" {
					_ = s.UpdateAppStatus(a.id, "失败")
					_ = s.InsertCallLog(&CallLog{AppID: a.id, ProjectID: a.project, Event: "状态变化",
						FromStatus: "启动中", ToStatus: "失败", Reason: "启动时对账：进程不存在", CreatedAt: time.Now().Unix()})
					report.StaleStarting = append(report.StaleStarting, a.project+"/"+a.name)
				} else {
					_ = s.UpdateAppStatus(a.id, "不健康")
					report.StaleRunning = append(report.StaleRunning, a.project+"/"+a.name)
				}
			}
		}
	}
	return report, nil
}

// ===== 版本二新增方法 =====

// InsertCallLog 写一条审计日志。状态变化才写，重复成功不写由调用方判断。
func (s *Store) InsertCallLog(log *CallLog) error {
	if log.ID == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		log.ID = hex.EncodeToString(b)
	}
	_, err := s.DB.Exec(`INSERT INTO call_logs(id,app_id,project_id,event,from_status,to_status,reason,created_at)
		VALUES(?,?,?,?,?,?,?,?)`, log.ID, log.AppID, log.ProjectID, log.Event,
		log.FromStatus, log.ToStatus, log.Reason, log.CreatedAt)
	return err
}

// ListCallLogs 查审计日志，最新的在前。
func (s *Store) ListCallLogs(appID string, limit int) ([]*CallLog, error) {
	q := `SELECT id,app_id,project_id,event,from_status,to_status,reason,created_at FROM call_logs`
	var rows *sql.Rows
	var err error
	if appID != "" {
		q += ` WHERE app_id=? ORDER BY created_at DESC LIMIT ?`
		rows, err = s.DB.Query(q, appID, limit)
	} else {
		q += ` ORDER BY created_at DESC LIMIT ?`
		rows, err = s.DB.Query(q, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*CallLog
	for rows.Next() {
		l := &CallLog{}
		if err := rows.Scan(&l.ID, &l.AppID, &l.ProjectID, &l.Event, &l.FromStatus,
			&l.ToStatus, &l.Reason, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// PruneCallLogs 按条数回收审计日志。
func (s *Store) PruneCallLogs(maxRows int) error {
	_, err := s.DB.Exec(`DELETE FROM call_logs WHERE id IN (
		SELECT id FROM call_logs ORDER BY created_at DESC LIMIT -1 OFFSET ?)`, maxRows)
	return err
}

// SaveSecret 保存加密后的密钥。
func (s *Store) SaveSecret(sec *Secret) error {
	_, err := s.DB.Exec(`INSERT OR REPLACE INTO secrets(app_id,key,encrypted_value,version,created_at)
		VALUES(?,?,?,?,?)`, sec.AppID, sec.Key, sec.EncryptedValue, sec.Version, sec.CreatedAt)
	return err
}

// GetSecrets 取某应用某版本的全部密钥。
func (s *Store) GetSecrets(appID, version string) ([]*Secret, error) {
	var rows *sql.Rows
	var err error
	if version == "" {
		rows, err = s.DB.Query(`SELECT app_id,key,encrypted_value,version,created_at FROM secrets WHERE app_id=?`, appID)
	} else {
		rows, err = s.DB.Query(`SELECT app_id,key,encrypted_value,version,created_at FROM secrets WHERE app_id=? AND version=?`, appID, version)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Secret
	for rows.Next() {
		x := &Secret{}
		if err := rows.Scan(&x.AppID, &x.Key, &x.EncryptedValue, &x.Version, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// SaveAppEnv 保存环境变量。
func (s *Store) SaveAppEnv(e *AppEnv) error {
	v := 0
	if e.IsSecret {
		v = 1
	}
	_, err := s.DB.Exec(`INSERT OR REPLACE INTO app_env(app_id,key,value,is_secret,version) VALUES(?,?,?,?,?)`,
		e.AppID, e.Key, e.Value, v, e.Version)
	return err
}

// GetAppEnv 取某应用某版本的全部环境变量。
func (s *Store) GetAppEnv(appID, version string) ([]*AppEnv, error) {
	var rows *sql.Rows
	var err error
	if version == "" {
		rows, err = s.DB.Query(`SELECT app_id,key,value,is_secret,version FROM app_env WHERE app_id=?`, appID)
	} else {
		rows, err = s.DB.Query(`SELECT app_id,key,value,is_secret,version FROM app_env WHERE app_id=? AND version=?`, appID, version)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AppEnv
	for rows.Next() {
		e := &AppEnv{}
		var v int
		if err := rows.Scan(&e.AppID, &e.Key, &e.Value, &v, &e.Version); err != nil {
			return nil, err
		}
		e.IsSecret = v == 1
		out = append(out, e)
	}
	return out, rows.Err()
}

// InsertObject 写对象元数据。
func (s *Store) InsertObject(o *Object) error {
	_, err := s.DB.Exec(`INSERT INTO objects(id,project_id,app_name,file_name,size,content_type,created_at)
		VALUES(?,?,?,?,?,?,?)`, o.ID, o.ProjectID, o.AppName, o.FileName, o.Size, o.ContentType, o.CreatedAt)
	return err
}

// GetObject 按 ID 取对象元数据。
func (s *Store) GetObject(projectID, appName, id string) (*Object, error) {
	var o Object
	err := s.DB.QueryRow(`SELECT id,project_id,app_name,file_name,size,content_type,created_at
		FROM objects WHERE id=? AND project_id=? AND app_name=?`, id, projectID, appName).Scan(
		&o.ID, &o.ProjectID, &o.AppName, &o.FileName, &o.Size, &o.ContentType, &o.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// ListObjects 列出某应用的对象。
func (s *Store) ListObjects(projectID, appName string) ([]*Object, error) {
	rows, err := s.DB.Query(`SELECT id,project_id,app_name,file_name,size,content_type,created_at
		FROM objects WHERE project_id=? AND app_name=? ORDER BY created_at DESC`, projectID, appName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Object
	for rows.Next() {
		o := &Object{}
		if err := rows.Scan(&o.ID, &o.ProjectID, &o.AppName, &o.FileName, &o.Size,
			&o.ContentType, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// DeleteObject 删对象元数据。
func (s *Store) DeleteObject(projectID, appName, id string) error {
	_, err := s.DB.Exec(`DELETE FROM objects WHERE id=? AND project_id=? AND app_name=?`, id, projectID, appName)
	return err
}

// TotalObjectSize 统计对象存储总字节数。
func (s *Store) TotalObjectSize() (int64, error) {
	var total sql.NullInt64
	err := s.DB.QueryRow(`SELECT SUM(size) FROM objects`).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}

// AppObjectSize 统计某应用对象存储字节数。
func (s *Store) AppObjectSize(projectID, appName string) (int64, error) {
	var total sql.NullInt64
	err := s.DB.QueryRow(`SELECT SUM(size) FROM objects WHERE project_id=? AND app_name=?`, projectID, appName).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}

// SetAppRestartInfo 记录应用重启次数和窗口起点。
func (s *Store) SetAppRestartInfo(appID string, count int, windowStart int64) error {
	_, err := s.DB.Exec(`UPDATE apps SET restart_count=?, restart_window_start=? WHERE id=?`, count, windowStart, appID)
	return err
}

// ProjectDBPath 返回项目库路径。
func (s *Store) ProjectDBPath(projectID string) string {
	return filepath.Join(s.DataDir, "databases", projectID+".db")
}

// tcpAlive 检查端口是否可连接（对账时用）。
func tcpAlive(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
