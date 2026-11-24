// Package database 管理 system.db 和应用库文件的生命周期。
// 规则：应用是顶层资源；平台记录与库文件同生共死；启动时对账，孤儿与坏引用都要报告。
package database

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// App 对应 apps 表。应用是唯一顶层资源。
type App struct {
	ID                 string
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

// Database 应用下的一个数据库。
type Database struct {
	ID        string `json:"id"`
	AppName   string `json:"app_name"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	IsDefault bool   `json:"is_default"`
	CreatedAt int64  `json:"created_at"`
}

// AppConfig 是按应用覆盖的运行时配置。nil 字段表示用全局配置。
type AppConfig struct {
	AppID           string
	MemoryLimitMB   *int
	CPUQuotaPercent *int
	StartCmd        *string
	HealthPath      *string
	HealthTimeoutMS *int
}

// Version 对应 versions 表。
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
	ID          string `json:"id"`
	AppName     string `json:"app_name"`
	FileName    string `json:"file_name"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	CreatedAt   int64  `json:"created_at"`
}

// AppEnv 应用环境变量（普通明文 / 密钥密文）。
type AppEnv struct {
	AppID    string
	Key      string
	Value    string
	IsSecret bool
	Version  string
}

// CorsDomain 应用级 CORS 白名单条目。
type CorsDomain struct {
	ID        string `json:"id"`
	AppName   string `json:"app_name"`
	Domain    string `json:"domain"`
	CreatedAt int64  `json:"created_at"`
}

// ReconcileReport 启动对账结果。
type ReconcileReport struct {
	OrphanFiles   []string
	BrokenRefs    []string
	Healthy       []string
	StaleRunning  []string
	StaleStarting []string
}

// Store 封装系统库访问。
type Store struct {
	DB            *sql.DB
	DataDir       string
	BusyTimeoutMS int

	// CreateAppFailAt 仅供逻辑测试注入失败点。
	// 取值 "insert_record" 或 "create_file"。
	CreateAppFailAt string
}

// Open 打开 system.db，建目录、开 WAL、建表。
func Open(dataDir string, busyTimeoutMS int) (*Store, error) {
	for _, sub := range []string{"databases", "apps", "objects"} {
		if err := os.MkdirAll(filepath.Join(dataDir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("建 %s 目录失败: %w", sub, err)
		}
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(%d)&_pragma=foreign_keys(ON)",
		filepath.ToSlash(filepath.Join(dataDir, "system.db")), busyTimeoutMS)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 system.db 失败: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	s := &Store{DB: db, DataDir: dataDir, BusyTimeoutMS: busyTimeoutMS}
	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrateProjectLayer(); err != nil {
		log.Printf("[migrate] 旧项目层迁移失败: %v", err)
	}
	return s, nil
}

func schemaStatements(prefix string) []string {
	return []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %sapps (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			root_path TEXT NOT NULL,
			current_version TEXT,
			previous_version TEXT,
			port INTEGER,
			status TEXT,
			created_at INTEGER,
			restart_count INTEGER DEFAULT 0,
			restart_window_start INTEGER DEFAULT 0,
			UNIQUE(name),
			UNIQUE(root_path)
		)`, prefix),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %sversions (
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
		)`, prefix),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %scall_logs (
			id TEXT PRIMARY KEY,
			app_id TEXT,
			event TEXT,
			from_status TEXT,
			to_status TEXT,
			reason TEXT,
			created_at INTEGER
		)`, prefix),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %ssecrets (
			app_id TEXT,
			key TEXT,
			encrypted_value TEXT,
			version TEXT,
			created_at INTEGER,
			PRIMARY KEY(app_id, key, version)
		)`, prefix),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %sobjects (
			id TEXT PRIMARY KEY,
			app_name TEXT NOT NULL,
			file_name TEXT,
			size INTEGER,
			content_type TEXT,
			created_at INTEGER
		)`, prefix),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %sapp_env (
			app_id TEXT,
			key TEXT,
			value TEXT,
			is_secret INTEGER,
			version TEXT,
			PRIMARY KEY(app_id, key, version)
		)`, prefix),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %sapp_config (
			app_id TEXT PRIMARY KEY,
			memory_limit_mb INTEGER,
			cpu_quota_percent INTEGER,
			start_cmd TEXT,
			health_path TEXT,
			health_timeout_ms INTEGER
		)`, prefix),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %sdatabases (
			id TEXT PRIMARY KEY,
			app_name TEXT NOT NULL,
			name TEXT NOT NULL,
			path TEXT NOT NULL,
			is_default INTEGER DEFAULT 0,
			created_at INTEGER NOT NULL,
			UNIQUE(app_name, name)
		)`, prefix),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %scors_domains (
			id TEXT PRIMARY KEY,
			app_name TEXT NOT NULL,
			domain TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			UNIQUE(app_name, domain)
		)`, prefix),
	}
}

func (s *Store) initSchema() error {
	for _, q := range schemaStatements("") {
		if _, err := s.DB.Exec(q); err != nil {
			return fmt.Errorf("建表失败: %w", err)
		}
	}
	return nil
}

func tableHasColumn(db *sql.DB, table, column string) bool {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk) == nil && name == column {
			return true
		}
	}
	return false
}

type oldAppRow struct {
	id, project, name, root, current, previous, status string
	port, createdAt, restarts, restartWindow           int
}

type pathMove struct{ from, to string }

// migrateProjectLayer 把旧 schema 的 project_id 去掉，并把数据目录迁到应用名下。
func (s *Store) migrateProjectLayer() error {
	if !tableHasColumn(s.DB, "apps", "project_id") {
		return nil
	}
	log.Printf("[migrate] 检测到旧项目层，开始迁移为应用顶层模型")
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`SELECT id,project_id,name,root_path,current_version,previous_version,port,status,created_at,restart_count,restart_window_start FROM apps`)
	if err != nil {
		return err
	}
	projectApps := map[string][]string{}
	var oldApps []oldAppRow
	for rows.Next() {
		var a oldAppRow
		if err := rows.Scan(&a.id, &a.project, &a.name, &a.root, &a.current, &a.previous,
			&a.port, &a.status, &a.createdAt, &a.restarts, &a.restartWindow); err != nil {
			rows.Close()
			return err
		}
		oldApps = append(oldApps, a)
		projectApps[a.project] = append(projectApps[a.project], a.name)
	}
	rows.Close()

	for _, q := range schemaStatements("mig_") {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`INSERT INTO mig_versions SELECT * FROM versions`); err != nil {
		return err
	}
	moves := []pathMove{}
	for _, a := range oldApps {
		from := filepath.Join(s.DataDir, "apps", a.project, a.name)
		to := filepath.Join(s.DataDir, "apps", a.name)
		moves = append(moves, pathMove{from: from, to: to})
		if _, err := tx.Exec(`INSERT INTO mig_apps(id,name,root_path,current_version,previous_version,port,status,created_at,restart_count,restart_window_start)
			VALUES(?,?,?,?,?,?,?,?,?,?)`, a.id, a.name, "/apps/"+a.name, a.current, a.previous,
			a.port, a.status, a.createdAt, a.restarts, a.restartWindow); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE mig_versions SET source_dir=REPLACE(source_dir,?,?)`, from, to); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO mig_call_logs(id,app_id,event,from_status,to_status,reason,created_at)
		SELECT id,app_id,event,from_status,to_status,reason,created_at FROM call_logs`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO mig_secrets SELECT * FROM secrets`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO mig_app_env SELECT * FROM app_env`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO mig_app_config SELECT * FROM app_config`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO mig_objects(id,app_name,file_name,size,content_type,created_at)
		SELECT id,app_name,file_name,size,content_type,created_at FROM objects`); err != nil {
		return err
	}

	dbRows, err := tx.Query(`SELECT id,project_id,name,path,is_default,created_at FROM databases`)
	if err != nil {
		return err
	}
	for dbRows.Next() {
		var id, project, name, oldPath string
		var def int
		var created int64
		if err := dbRows.Scan(&id, &project, &name, &oldPath, &def, &created); err != nil {
			dbRows.Close()
			return err
		}
		apps := projectApps[project]
		if len(apps) == 0 {
			continue
		}
		appName := apps[0]
		newPath := filepath.Join(s.DataDir, "databases", appName, name+".db")
		moves = append(moves, pathMove{from: oldPath, to: newPath})
		if _, err := tx.Exec(`INSERT INTO mig_databases(id,app_name,name,path,is_default,created_at) VALUES(?,?,?,?,?,?)`,
			id, appName, name, newPath, def, created); err != nil {
			dbRows.Close()
			return err
		}
	}
	dbRows.Close()

	corsRows, err := tx.Query(`SELECT id,project_id,domain,created_at FROM cors_domains`)
	if err != nil {
		return err
	}
	for corsRows.Next() {
		var id, project, domain string
		var created int64
		if err := corsRows.Scan(&id, &project, &domain, &created); err != nil {
			corsRows.Close()
			return err
		}
		for _, appName := range projectApps[project] {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO mig_cors_domains(id,app_name,domain,created_at) VALUES(?,?,?,?)`,
				randomID("cors_"), appName, domain, created); err != nil {
				corsRows.Close()
				return err
			}
		}
	}
	corsRows.Close()

	for _, table := range []string{"apps", "versions", "call_logs", "secrets", "objects", "app_env", "app_config", "databases", "cors_domains", "projects"} {
		if _, err := tx.Exec(`DROP TABLE IF EXISTS ` + table); err != nil {
			return err
		}
	}
	for _, table := range []string{"apps", "versions", "call_logs", "secrets", "objects", "app_env", "app_config", "databases", "cors_domains"} {
		if _, err := tx.Exec(`ALTER TABLE mig_` + table + ` RENAME TO ` + table); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, a := range oldApps {
		moves = append(moves, pathMove{
			from: filepath.Join(s.DataDir, "objects", a.project, a.name),
			to:   filepath.Join(s.DataDir, "objects", a.name),
		})
	}
	for _, m := range moves {
		movePathQuiet(m.from, m.to)
	}
	log.Printf("[migrate] 旧项目层迁移完成")
	return nil
}

func movePathQuiet(from, to string) {
	fromAbs, _ := filepath.Abs(from)
	toAbs, _ := filepath.Abs(to)
	if fromAbs == toAbs {
		return
	}
	if _, err := os.Stat(fromAbs); err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(toAbs), 0o755); err != nil {
		log.Printf("[migrate] 建目标目录失败 %s: %v", toAbs, err)
		return
	}
	if err := os.Rename(fromAbs, toAbs); err != nil {
		log.Printf("[migrate] 移动 %s 到 %s 失败: %v", fromAbs, toAbs, err)
	}
}

func randomID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func ValidDatabaseName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

// ===== 应用 =====

func (s *Store) CreateApp(a *App) error {
	if a.ID == "" || a.Name == "" {
		return fmt.Errorf("应用 ID 和名称不能为空")
	}
	_, err := s.DB.Exec(`INSERT INTO apps(id,name,root_path,current_version,previous_version,port,status,created_at,restart_count,restart_window_start)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, a.ID, a.Name, a.RootPath, a.CurrentVersion, a.PreviousVersion,
		a.Port, a.Status, a.CreatedAt, a.RestartCount, a.RestartWindowStart)
	return err
}

func (s *Store) GetAppByName(name string) (*App, error) {
	return s.queryApp(`SELECT id,name,root_path,current_version,previous_version,port,status,created_at,restart_count,restart_window_start
		FROM apps WHERE name=?`, name)
}

func (s *Store) GetAppByRootPath(root string) (*App, error) {
	return s.queryApp(`SELECT id,name,root_path,current_version,previous_version,port,status,created_at,restart_count,restart_window_start
		FROM apps WHERE root_path=?`, root)
}

func (s *Store) FindAppByName(name string) (*App, error) { return s.GetAppByName(name) }

func (s *Store) GetAppByID(id string) (*App, error) {
	return s.queryApp(`SELECT id,name,root_path,current_version,previous_version,port,status,created_at,restart_count,restart_window_start
		FROM apps WHERE id=?`, id)
}

func (s *Store) ListApps() ([]*App, error) {
	rows, err := s.DB.Query(`SELECT id,name,root_path,current_version,previous_version,port,status,created_at,restart_count,restart_window_start FROM apps ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*App
	for rows.Next() {
		a := &App{}
		if err := rows.Scan(&a.ID, &a.Name, &a.RootPath, &a.CurrentVersion, &a.PreviousVersion,
			&a.Port, &a.Status, &a.CreatedAt, &a.RestartCount, &a.RestartWindowStart); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) queryApp(q string, args ...any) (*App, error) {
	var a App
	err := s.DB.QueryRow(q, args...).Scan(&a.ID, &a.Name, &a.RootPath, &a.CurrentVersion,
		&a.PreviousVersion, &a.Port, &a.Status, &a.CreatedAt, &a.RestartCount, &a.RestartWindowStart)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) UpdateAppAfterDeploy(appID, current, previous string, port int, status string) error {
	_, err := s.DB.Exec(`UPDATE apps SET current_version=?,previous_version=?,port=?,status=? WHERE id=?`,
		current, previous, port, status, appID)
	return err
}

func (s *Store) UpdateAppStatus(appID, status string) error {
	_, err := s.DB.Exec(`UPDATE apps SET status=? WHERE id=?`, status, appID)
	return err
}

func (s *Store) SetAppRestartInfo(appID string, count int, windowStart int64) error {
	_, err := s.DB.Exec(`UPDATE apps SET restart_count=?, restart_window_start=? WHERE id=?`, count, windowStart, appID)
	return err
}

func (s *Store) DeleteAppCascade(appName string) error {
	app, err := s.GetAppByName(appName)
	if err != nil || app == nil {
		return fmt.Errorf("应用不存在")
	}
	dbs, _ := s.ListDatabases(appName)
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"versions", "secrets", "app_env", "app_config", "databases", "cors_domains", "objects"} {
		col := "app_id"
		if table == "databases" || table == "cors_domains" || table == "objects" {
			col = "app_name"
		}
		if _, err := tx.Exec(fmt.Sprintf(`DELETE FROM %s WHERE %s=?`, table, col), appName); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM apps WHERE id=?`, app.ID); err != nil {
		return err
	}
	_ = s.InsertCallLog(&CallLog{AppID: app.ID, Event: "app_deleted", FromStatus: app.Status,
		ToStatus: "已删除", Reason: "用户彻底删除应用", CreatedAt: time.Now().Unix()})
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, d := range dbs {
		_ = os.Remove(d.Path)
	}
	_ = os.RemoveAll(filepath.Join(s.DataDir, "apps", appName))
	_ = os.RemoveAll(filepath.Join(s.DataDir, "objects", appName))
	return nil
}

// ===== 版本 =====

func (s *Store) CreateVersion(v *Version) error {
	_, err := s.DB.Exec(`INSERT INTO versions(id,app_id,version,source_dir,mode,start_cmd,env_json,port,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, v.ID, v.AppID, v.Version, v.SourceDir, v.Mode, v.StartCmd, v.EnvJSON, v.Port, v.CreatedAt)
	return err
}

func (s *Store) GetVersion(appID, version string) (*Version, error) {
	var v Version
	err := s.DB.QueryRow(`SELECT id,app_id,version,source_dir,mode,start_cmd,env_json,port,created_at
		FROM versions WHERE app_id=? AND version=?`, appID, version).Scan(&v.ID, &v.AppID, &v.Version,
		&v.SourceDir, &v.Mode, &v.StartCmd, &v.EnvJSON, &v.Port, &v.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

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

// ===== 数据库 =====

func (s *Store) DatabasePath(appName, name string) string {
	return filepath.Join(s.DataDir, "databases", appName, name+".db")
}

func (s *Store) EnsureDefaultDatabase(appName string) (*Database, error) {
	d, err := s.GetDefaultDatabase(appName)
	if err != nil || d != nil {
		return d, err
	}
	return s.CreateDatabase(appName, "main", true)
}

func (s *Store) CreateDatabase(appName, name string, isDefault bool) (*Database, error) {
	if !ValidDatabaseName(name) {
		return nil, fmt.Errorf("库名不合法")
	}
	if s.CreateAppFailAt == "insert_record" {
		return nil, fmt.Errorf("测试注入失败: 写库记录前")
	}
	app, err := s.GetAppByName(appName)
	if err != nil || app == nil {
		return nil, fmt.Errorf("应用不存在")
	}
	var defaultCount int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM databases WHERE app_name=? AND is_default=1`, appName).Scan(&defaultCount)
	if defaultCount > 0 && isDefault {
		return nil, fmt.Errorf("应用已有默认库")
	}
	var exists int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM databases WHERE app_name=? AND name=?`, appName, name).Scan(&exists)
	if exists > 0 {
		return nil, fmt.Errorf("库名已存在")
	}
	dbPath := s.DatabasePath(appName, name)
	d := &Database{ID: "db_" + randomHex(6), AppName: appName, Name: name, Path: dbPath,
		IsDefault: isDefault, CreatedAt: time.Now().Unix()}
	if _, err := s.DB.Exec(`INSERT INTO databases(id,app_name,name,path,is_default,created_at) VALUES(?,?,?,?,?,?)`,
		d.ID, d.AppName, d.Name, d.Path, boolToInt(d.IsDefault), d.CreatedAt); err != nil {
		return nil, err
	}
	if s.CreateAppFailAt == "create_file" {
		_, _ = s.DB.Exec(`DELETE FROM databases WHERE id=?`, d.ID)
		return nil, fmt.Errorf("测试注入失败: 建库文件前")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		_, _ = s.DB.Exec(`DELETE FROM databases WHERE id=?`, d.ID)
		return nil, err
	}
	pdb, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)", filepath.ToSlash(dbPath)))
	if err != nil {
		_, _ = s.DB.Exec(`DELETE FROM databases WHERE id=?`, d.ID)
		return nil, err
	}
	_, err = pdb.Exec(`CREATE TABLE IF NOT EXISTS _meta(key TEXT PRIMARY KEY, value TEXT)`)
	pdb.Close()
	if err != nil {
		_, _ = s.DB.Exec(`DELETE FROM databases WHERE id=?`, d.ID)
		return nil, err
	}
	return d, nil
}

func (s *Store) ListDatabases(appName string) ([]*Database, error) {
	rows, err := s.DB.Query(`SELECT id,app_name,name,path,is_default,created_at FROM databases WHERE app_name=? ORDER BY is_default DESC,name`, appName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Database{}
	for rows.Next() {
		d := &Database{}
		var def int
		if err := rows.Scan(&d.ID, &d.AppName, &d.Name, &d.Path, &def, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.IsDefault = def == 1
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetDatabase(appName, name string) (*Database, error) {
	d := &Database{}
	var def int
	err := s.DB.QueryRow(`SELECT id,app_name,name,path,is_default,created_at FROM databases WHERE app_name=? AND name=?`, appName, name).
		Scan(&d.ID, &d.AppName, &d.Name, &d.Path, &def, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.IsDefault = def == 1
	return d, nil
}

func (s *Store) GetDefaultDatabase(appName string) (*Database, error) {
	d := &Database{}
	var def int
	err := s.DB.QueryRow(`SELECT id,app_name,name,path,is_default,created_at FROM databases WHERE app_name=? AND is_default=1`, appName).
		Scan(&d.ID, &d.AppName, &d.Name, &d.Path, &def, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.IsDefault = true
	return d, nil
}

func (s *Store) DeleteDatabase(appName, name string) error {
	d, err := s.GetDatabase(appName, name)
	if err != nil || d == nil {
		return fmt.Errorf("库不存在")
	}
	if d.IsDefault {
		return fmt.Errorf("不能删除默认库")
	}
	if _, err := s.DB.Exec(`DELETE FROM databases WHERE id=?`, d.ID); err != nil {
		return err
	}
	return os.Remove(d.Path)
}

// ===== CORS、配置、审计、密钥、环境变量、对象存储 =====

func (s *Store) ListCorsDomains(appName string) ([]*CorsDomain, error) {
	rows, err := s.DB.Query(`SELECT id,app_name,domain,created_at FROM cors_domains WHERE app_name=? ORDER BY created_at`, appName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*CorsDomain{}
	for rows.Next() {
		c := &CorsDomain{}
		if err := rows.Scan(&c.ID, &c.AppName, &c.Domain, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) AddCorsDomain(appName, domain string) (*CorsDomain, error) {
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM cors_domains WHERE app_name=?`, appName).Scan(&n); err != nil {
		return nil, err
	}
	if n >= 10 {
		return nil, fmt.Errorf("CORS 白名单最多 10 条")
	}
	c := &CorsDomain{ID: randomID("cors_"), AppName: appName, Domain: domain, CreatedAt: time.Now().Unix()}
	if _, err := s.DB.Exec(`INSERT INTO cors_domains(id,app_name,domain,created_at) VALUES(?,?,?,?)`,
		c.ID, c.AppName, c.Domain, c.CreatedAt); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Store) DeleteCorsDomain(appName, id string) error {
	_, err := s.DB.Exec(`DELETE FROM cors_domains WHERE app_name=? AND id=?`, appName, id)
	return err
}

func (s *Store) GetCorsDomainsForApp(appName string) ([]*CorsDomain, error) {
	return s.ListCorsDomains(appName)
}

func (s *Store) GetAppConfig(appID string) (*AppConfig, error) {
	c := &AppConfig{AppID: appID}
	var mem, cpu, timeout sql.NullInt64
	var startCmd, healthPath sql.NullString
	err := s.DB.QueryRow(`SELECT memory_limit_mb,cpu_quota_percent,start_cmd,health_path,health_timeout_ms FROM app_config WHERE app_id=?`, appID).
		Scan(&mem, &cpu, &startCmd, &healthPath, &timeout)
	if err == sql.ErrNoRows {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if mem.Valid {
		v := int(mem.Int64)
		c.MemoryLimitMB = &v
	}
	if cpu.Valid {
		v := int(cpu.Int64)
		c.CPUQuotaPercent = &v
	}
	if startCmd.Valid {
		v := startCmd.String
		c.StartCmd = &v
	}
	if healthPath.Valid {
		v := healthPath.String
		c.HealthPath = &v
	}
	if timeout.Valid {
		v := int(timeout.Int64)
		c.HealthTimeoutMS = &v
	}
	return c, nil
}

func (s *Store) SetAppConfig(appID string, c *AppConfig) error {
	var mem, cpu, timeout any
	var startCmd, healthPath any
	if c.MemoryLimitMB != nil {
		mem = *c.MemoryLimitMB
	}
	if c.CPUQuotaPercent != nil {
		cpu = *c.CPUQuotaPercent
	}
	if c.StartCmd != nil {
		startCmd = *c.StartCmd
	}
	if c.HealthPath != nil {
		healthPath = *c.HealthPath
	}
	if c.HealthTimeoutMS != nil {
		timeout = *c.HealthTimeoutMS
	}
	_, err := s.DB.Exec(`INSERT INTO app_config(app_id,memory_limit_mb,cpu_quota_percent,start_cmd,health_path,health_timeout_ms)
		VALUES(?,?,?,?,?,?) ON CONFLICT(app_id) DO UPDATE SET memory_limit_mb=excluded.memory_limit_mb,
		cpu_quota_percent=excluded.cpu_quota_percent,start_cmd=excluded.start_cmd,
		health_path=excluded.health_path,health_timeout_ms=excluded.health_timeout_ms`,
		appID, mem, cpu, startCmd, healthPath, timeout)
	return err
}

func (s *Store) DeleteAppConfig(appID string) error {
	_, err := s.DB.Exec(`DELETE FROM app_config WHERE app_id=?`, appID)
	return err
}

func (s *Store) InsertCallLog(l *CallLog) error {
	if l.ID == "" {
		l.ID = randomHex(8)
	}
	_, err := s.DB.Exec(`INSERT INTO call_logs(id,app_id,event,from_status,to_status,reason,created_at)
		VALUES(?,?,?,?,?,?,?)`, l.ID, l.AppID, l.Event, l.FromStatus, l.ToStatus, l.Reason, l.CreatedAt)
	return err
}

func (s *Store) ListCallLogs(appID string, limit int) ([]*CallLog, error) {
	q := `SELECT id,app_id,event,from_status,to_status,reason,created_at FROM call_logs`
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
		if err := rows.Scan(&l.ID, &l.AppID, &l.Event, &l.FromStatus, &l.ToStatus, &l.Reason, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) PruneCallLogs(maxRows int) error {
	_, err := s.DB.Exec(`DELETE FROM call_logs WHERE id IN (
		SELECT id FROM call_logs ORDER BY created_at DESC LIMIT -1 OFFSET ?)`, maxRows)
	return err
}

func (s *Store) SaveSecret(sec *Secret) error {
	_, err := s.DB.Exec(`INSERT OR REPLACE INTO secrets(app_id,key,encrypted_value,version,created_at)
		VALUES(?,?,?,?,?)`, sec.AppID, sec.Key, sec.EncryptedValue, sec.Version, sec.CreatedAt)
	return err
}

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

func (s *Store) SaveAppEnv(e *AppEnv) error {
	v := 0
	if e.IsSecret {
		v = 1
	}
	_, err := s.DB.Exec(`INSERT OR REPLACE INTO app_env(app_id,key,value,is_secret,version) VALUES(?,?,?,?,?)`,
		e.AppID, e.Key, e.Value, v, e.Version)
	return err
}

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

func (s *Store) InsertObject(o *Object) error {
	_, err := s.DB.Exec(`INSERT INTO objects(id,app_name,file_name,size,content_type,created_at)
		VALUES(?,?,?,?,?,?)`, o.ID, o.AppName, o.FileName, o.Size, o.ContentType, o.CreatedAt)
	return err
}

func (s *Store) GetObject(appName, id string) (*Object, error) {
	var o Object
	err := s.DB.QueryRow(`SELECT id,app_name,file_name,size,content_type,created_at
		FROM objects WHERE id=? AND app_name=?`, id, appName).Scan(&o.ID, &o.AppName, &o.FileName,
		&o.Size, &o.ContentType, &o.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func (s *Store) ListObjects(appName string) ([]*Object, error) {
	rows, err := s.DB.Query(`SELECT id,app_name,file_name,size,content_type,created_at
		FROM objects WHERE app_name=? ORDER BY created_at DESC`, appName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Object, 0)
	for rows.Next() {
		o := &Object{}
		if err := rows.Scan(&o.ID, &o.AppName, &o.FileName, &o.Size, &o.ContentType, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) DeleteObject(appName, id string) error {
	_, err := s.DB.Exec(`DELETE FROM objects WHERE id=? AND app_name=?`, id, appName)
	return err
}

func (s *Store) TotalObjectSize() (int64, error) {
	var total sql.NullInt64
	err := s.DB.QueryRow(`SELECT SUM(size) FROM objects`).Scan(&total)
	return total.Int64, err
}

func (s *Store) AppObjectSize(appName string) (int64, error) {
	var total sql.NullInt64
	err := s.DB.QueryRow(`SELECT SUM(size) FROM objects WHERE app_name=?`, appName).Scan(&total)
	return total.Int64, err
}

// ===== 对账 =====

func (s *Store) Reconcile() (*ReconcileReport, error) {
	report := &ReconcileReport{}
	rows, err := s.DB.Query(`SELECT d.id,d.app_name,d.name,d.path FROM databases d`)
	if err != nil {
		return nil, err
	}
	type dbRec struct{ id, app, name, path string }
	var records []dbRec
	for rows.Next() {
		var d dbRec
		if err := rows.Scan(&d.id, &d.app, &d.name, &d.path); err != nil {
			rows.Close()
			return nil, err
		}
		records = append(records, d)
	}
	rows.Close()
	for _, d := range records {
		label := d.app + "/" + d.name
		if _, err := os.Stat(d.path); err == nil {
			report.Healthy = append(report.Healthy, label)
		} else {
			report.BrokenRefs = append(report.BrokenRefs, label)
		}
	}
	root := filepath.Join(s.DataDir, "databases")
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".db") {
			return nil
		}
		abs, _ := filepath.Abs(path)
		for _, d := range records {
			dpath, _ := filepath.Abs(d.path)
			if abs == dpath {
				return nil
			}
		}
		rel, _ := filepath.Rel(root, path)
		report.OrphanFiles = append(report.OrphanFiles, rel)
		return nil
	})
	sort.Strings(report.Healthy)
	sort.Strings(report.BrokenRefs)
	sort.Strings(report.OrphanFiles)

	rows2, err := s.DB.Query(`SELECT id,name,status,current_version FROM apps WHERE status IN ('运行中','启动中')`)
	if err == nil {
		type appState struct{ id, name, status, version string }
		var apps []appState
		for rows2.Next() {
			var a appState
			if rows2.Scan(&a.id, &a.name, &a.status, &a.version) == nil {
				apps = append(apps, a)
			}
		}
		rows2.Close()
		for _, a := range apps {
			var port int
			_ = s.DB.QueryRow(`SELECT port FROM versions WHERE app_id=? AND version=?`, a.id, a.version).Scan(&port)
			if port > 0 && tcpAlive(port) {
				continue
			}
			if a.status == "启动中" {
				_ = s.UpdateAppStatus(a.id, "失败")
				_ = s.InsertCallLog(&CallLog{AppID: a.id, Event: "状态变化", FromStatus: "启动中",
					ToStatus: "失败", Reason: "启动时对账：进程不存在", CreatedAt: time.Now().Unix()})
				report.StaleStarting = append(report.StaleStarting, a.name)
			} else {
				_ = s.UpdateAppStatus(a.id, "不健康")
				report.StaleRunning = append(report.StaleRunning, a.name)
			}
		}
	}
	return report, nil
}

func tcpAlive(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// Close 关闭系统库。
func (s *Store) Close() error { return s.DB.Close() }
