package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"minibase/internal/database"
)

var identifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validIdentifier(s string) bool { return identifierRE.MatchString(s) }
func quoteIdent(s string) string    { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

var allowedColumnTypes = map[string]bool{
	"TEXT": true, "INTEGER": true, "REAL": true, "BLOB": true,
}

// openAppDB 打开应用库，带 WAL 与 busy_timeout。
func (s *Server) openAppDB(appName, name string) (*sql.DB, error) {
	d, err := s.store.GetDatabase(appName, name)
	if err != nil || d == nil {
		return nil, fmt.Errorf("库不存在")
	}
	dbPath := d.Path
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(%d)",
		strings.ReplaceAll(dbPath, "\\", "/"), s.cfg.BusyTimeoutMS)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	return db, nil
}

// handleDatabase 分发 /api/apps/{app}/databases[...]
func (s *Server) handleDatabase(w http.ResponseWriter, r *http.Request, appName string, rest []string) {

	// /databases — 列表 / 新建
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			s.dbList(w, r, appName)
		case http.MethodPost:
			s.dbCreate(w, r, appName)
		default:
			writeErr(w, http.StatusMethodNotAllowed, fmt.Errorf("不支持的方法"))
		}
		return
	}
	name := rest[0]
	if !database.ValidDatabaseName(name) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("库名不合法"))
		return
	}

	// /databases/{db} — 详情 / 删除
	if len(rest) == 1 {
		switch r.Method {
		case http.MethodGet:
			s.dbInfo(w, r, appName, name)
		case http.MethodDelete:
			s.dbDrop(w, r, appName, name)
		default:
			writeErr(w, http.StatusMethodNotAllowed, fmt.Errorf("不支持的方法"))
		}
		return
	}
	// /databases/{db}/tables[...]
	if rest[1] == "tables" {
		s.handleTables(w, r, appName, name, rest[2:])
		return
	}
	writeErr(w, http.StatusNotFound, fmt.Errorf("资源不存在"))
}

func (s *Server) handleTables(w http.ResponseWriter, r *http.Request, appName, dbName string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			s.dbListTables(w, r, appName, dbName)
			return
		case http.MethodPost:
			s.dbCreateTable(w, r, appName, dbName)
			return
		}
		writeErr(w, http.StatusMethodNotAllowed, fmt.Errorf("不支持的方法"))
		return
	}
	table := rest[0]
	if !validIdentifier(table) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("表名不合法"))
		return
	}

	if len(rest) == 1 {
		switch r.Method {
		case http.MethodGet:
			s.dbTableInfo(w, r, appName, dbName, table)
			return
		case http.MethodPut:
			s.dbRenameTable(w, r, appName, dbName, table)
			return
		case http.MethodDelete:
			s.dbDropTable(w, r, appName, dbName, table)
			return
		}
		writeErr(w, http.StatusMethodNotAllowed, fmt.Errorf("不支持的方法"))
		return
	}
	if len(rest) == 2 && rest[1] == "indexes" && r.Method == http.MethodPost {
		s.dbCreateIndex(w, r, appName, dbName, table)
		return
	}
	if len(rest) == 3 && rest[1] == "indexes" && r.Method == http.MethodDelete {
		if !validIdentifier(rest[2]) {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("索引名不合法"))
			return
		}
		s.dbDropIndex(w, r, appName, dbName, table, rest[2])
		return
	}
	writeErr(w, http.StatusNotFound, fmt.Errorf("资源不存在"))
}

// GET /database — 库信息
func (s *Server) dbInfo(w http.ResponseWriter, r *http.Request, appName, name string) {
	db, err := s.openAppDB(appName, name)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.cfg.SQLTimeoutMS)*time.Millisecond)
	defer cancel()

	var n int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&n)
	d, _ := s.store.GetDatabase(appName, name)
	path := ""
	if d != nil {
		path = d.Path
	}
	var size int64
	if st, e := os.Stat(path); e == nil {
		size = st.Size()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app_name": appName, "path": path,
		"size_bytes": size, "table_count": n,
	})
}

// GET /database/tables — 表列表
func (s *Server) dbListTables(w http.ResponseWriter, r *http.Request, appName, dbName string) {
	db, err := s.openAppDB(appName, dbName)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.cfg.SQLTimeoutMS)*time.Millisecond)
	defer cancel()

	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	var names []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			names = append(names, n)
		}
	}
	rows.Close()

	out := []map[string]any{}
	for _, n := range names {
		var count int64
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteIdent(n)).Scan(&count)
		out = append(out, map[string]any{"name": n, "row_count": count})
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /database/tables — 建表
func (s *Server) dbCreateTable(w http.ResponseWriter, r *http.Request, appName, dbName string) {
	var req struct {
		Name   string `json:"name"`
		Fields []struct {
			Name    string `json:"name"`
			Type    string `json:"type"`
			NotNull bool   `json:"not_null"`
		} `json:"fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("请求体解析失败: %w", err))
		return
	}
	if !validIdentifier(req.Name) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("表名不合法"))
		return
	}
	if len(req.Fields) == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("至少需要一个字段"))
		return
	}

	cols := []string{`"id" TEXT PRIMARY KEY`}
	seen := map[string]bool{"id": true, "created_at": true}
	for _, f := range req.Fields {
		if !validIdentifier(f.Name) {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("字段名不合法: %q", f.Name))
			return
		}
		if seen[f.Name] {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("字段重复或保留: %q", f.Name))
			return
		}
		seen[f.Name] = true
		t := strings.ToUpper(f.Type)
		if !allowedColumnTypes[t] {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("字段类型不支持: %q", f.Type))
			return
		}
		col := quoteIdent(f.Name) + " " + t
		if f.NotNull {
			col += " NOT NULL"
		}
		cols = append(cols, col)
	}
	cols = append(cols, `"created_at" INTEGER`)

	db, err := s.openAppDB(appName, dbName)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.cfg.SQLTimeoutMS)*time.Millisecond)
	defer cancel()

	q := `CREATE TABLE IF NOT EXISTS ` + quoteIdent(req.Name) + ` (` + strings.Join(cols, ",") + `)`
	if _, err := db.ExecContext(ctx, q); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "name": req.Name})
}

// GET /database/tables/{t} — 表结构详情
func (s *Server) dbTableInfo(w http.ResponseWriter, r *http.Request, appName, dbName, table string) {
	db, err := s.openAppDB(appName, dbName)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.cfg.SQLTimeoutMS)*time.Millisecond)
	defer cancel()

	var name string
	err = db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
	if err == sql.ErrNoRows {
		writeErr(w, http.StatusNotFound, fmt.Errorf("表不存在"))
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+quoteIdent(table)+`)`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	cols := []map[string]any{}
	for rows.Next() {
		var cid, notnull, pk int
		var cname, ctype string
		var dflt sql.NullString
		if rows.Scan(&cid, &cname, &ctype, &notnull, &dflt, &pk) == nil {
			cols = append(cols, map[string]any{
				"name": cname, "type": ctype, "not_null": notnull == 1, "primary_key": pk == 1,
			})
		}
	}
	rows.Close()

	irows, err := db.QueryContext(ctx, `PRAGMA index_list(`+quoteIdent(table)+`)`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	indexes := []map[string]any{}
	for irows.Next() {
		var seq, unique, origin, partial int
		var iname string
		if irows.Scan(&seq, &iname, &unique, &origin, &partial) == nil {
			indexes = append(indexes, map[string]any{"name": iname, "unique": unique == 1})
		}
	}
	irows.Close()

	var count int64
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteIdent(table)).Scan(&count)
	writeJSON(w, http.StatusOK, map[string]any{
		"name": table, "columns": cols, "indexes": indexes, "row_count": count,
	})
}

// PUT /database/tables/{t} — 重命名表
func (s *Server) dbRenameTable(w http.ResponseWriter, r *http.Request, appName, dbName, table string) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !validIdentifier(req.Name) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("新表名不合法"))
		return
	}
	db, err := s.openAppDB(appName, dbName)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.cfg.SQLTimeoutMS)*time.Millisecond)
	defer cancel()
	q := `ALTER TABLE ` + quoteIdent(table) + ` RENAME TO ` + quoteIdent(req.Name)
	if _, err := db.ExecContext(ctx, q); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "name": req.Name})
}

// DELETE /database/tables/{t} — 删表
func (s *Server) dbDropTable(w http.ResponseWriter, r *http.Request, appName, dbName, table string) {
	db, err := s.openAppDB(appName, dbName)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.cfg.SQLTimeoutMS)*time.Millisecond)
	defer cancel()
	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS `+quoteIdent(table)); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// POST /database/tables/{t}/indexes — 建索引
func (s *Server) dbCreateIndex(w http.ResponseWriter, r *http.Request, appName, dbName, table string) {
	var req struct {
		Name   string   `json:"name"`
		Fields []string `json:"fields"`
		Unique bool     `json:"unique"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !validIdentifier(req.Name) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("索引名不合法"))
		return
	}
	if len(req.Fields) == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("至少需要一个字段"))
		return
	}
	quoted := make([]string, len(req.Fields))
	for i, f := range req.Fields {
		if !validIdentifier(f) {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("字段名不合法: %q", f))
			return
		}
		quoted[i] = quoteIdent(f)
	}
	db, err := s.openAppDB(appName, dbName)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.cfg.SQLTimeoutMS)*time.Millisecond)
	defer cancel()
	unique := ""
	if req.Unique {
		unique = "UNIQUE "
	}
	q := `CREATE ` + unique + `INDEX IF NOT EXISTS ` + quoteIdent(req.Name) + ` ON ` + quoteIdent(table) + ` (` + strings.Join(quoted, ",") + `)`
	if _, err := db.ExecContext(ctx, q); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "name": req.Name})
}

// DELETE /database/tables/{t}/indexes/{i} — 删索引
func (s *Server) dbDropIndex(w http.ResponseWriter, r *http.Request, appName, dbName, table, index string) {
	db, err := s.openAppDB(appName, dbName)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.cfg.SQLTimeoutMS)*time.Millisecond)
	defer cancel()
	if _, err := db.ExecContext(ctx, `DROP INDEX IF EXISTS `+quoteIdent(index)); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// DELETE /databases/{db} — 删库（默认库不可删）
func (s *Server) dbDrop(w http.ResponseWriter, r *http.Request, appName, name string) {
	if err := s.store.DeleteDatabase(appName, name); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	_ = s.store.InsertCallLog(&database.CallLog{
		Event: "database_deleted", Reason: appName + " 删除库 " + name,
		CreatedAt: time.Now().Unix(),
	})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// POST /databases — 新建库
func (s *Server) dbCreate(w http.ResponseWriter, r *http.Request, appName string) {
	var req struct {
		Name      string `json:"name"`
		IsDefault bool   `json:"is_default"`
	}
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("需要库名"))
		return
	}
	d, err := s.store.CreateDatabase(appName, req.Name, req.IsDefault)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	_ = s.store.InsertCallLog(&database.CallLog{
		Event: "database_created", Reason: appName + " 创建库 " + req.Name,
		CreatedAt: time.Now().Unix(),
	})
	writeJSON(w, http.StatusOK, d)
}

// GET /databases — 库列表
func (s *Server) dbList(w http.ResponseWriter, r *http.Request, appName string) {
	list, err := s.store.ListDatabases(appName)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
