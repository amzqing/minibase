package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var identifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validIdentifier 表名/字段名白名单：字母或下划线开头，只包含字母数字下划线。
func validIdentifier(s string) bool { return identifierRE.MatchString(s) }

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// handleDataAPI 处理 /api/data/<项目ID>/<表名>[/<ID>] 四个操作。
func (s *Server) handleDataAPI(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/data/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("路径格式错误，应为 /api/data/<项目ID>/<表名>[/<ID>]"))
		return
	}
	projectID, table := parts[0], parts[1]
	rowID := ""
	if len(parts) >= 3 {
		rowID = parts[2]
	}

	if !validIdentifier(table) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("表名不合法"))
		return
	}
	if rowID != "" && !validRowID(rowID) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("记录 ID 不合法"))
		return
	}

	p, err := s.store.GetProject(projectID)
	if err != nil || p == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("项目不存在"))
		return
	}

	dbPath := s.store.ProjectDBPath(projectID)
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)", dbPath, s.cfg.BusyTimeoutMS))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.cfg.SQLTimeoutMS)*time.Millisecond)
	defer cancel()

	switch r.Method {
	case http.MethodGet:
		s.dataGet(ctx, w, r, db, table, rowID)
	case http.MethodPost:
		s.dataPost(ctx, w, r, db, table)
	case http.MethodPut:
		s.dataPut(ctx, w, r, db, table, rowID)
	case http.MethodDelete:
		s.dataDelete(ctx, w, db, table, rowID)
	default:
		http.NotFound(w, r)
	}
}

func isNumeric(s string) bool {
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}

// validRowID 记录 ID 允许字母数字（hex 随机 ID 可能以数字开头）。
func validRowID(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return s != ""
}

// ensureTable 建表：字段来自 JSON 的 key，全部白名单校验。
func ensureTable(db *sql.DB, table string, fields []string) error {
	for _, f := range fields {
		if !validIdentifier(f) {
			return fmt.Errorf("字段名不合法: %q", f)
		}
	}
	if len(fields) == 0 {
		return fmt.Errorf("至少需要一个字段")
	}
	cols := make([]string, 0, len(fields)+1)
	cols = append(cols, `"id" TEXT PRIMARY KEY`)
	for _, f := range fields {
		cols = append(cols, quoteIdent(f)+" TEXT")
	}
	cols = append(cols, `"created_at" INTEGER`)
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS ` + quoteIdent(table) + ` (` + strings.Join(cols, ",") + `)`)
	return err
}

func (s *Server) dataGet(ctx context.Context, w http.ResponseWriter, r *http.Request, db *sql.DB, table, rowID string) {
	// 检查表是否存在
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	q := `SELECT * FROM ` + quoteIdent(table)
	var args []any
	if rowID != "" {
		q += ` WHERE id=?`
		args = append(args, rowID)
	} else {
		// 查询参数作为等值条件，字段名白名单校验
		conds := []string{}
		for k, v := range r.URL.Query() {
			if k == "limit" || k == "offset" {
				continue
			}
			if !validIdentifier(k) {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("查询字段不合法: %q", k))
				return
			}
			conds = append(conds, quoteIdent(k)+`=?`)
			args = append(args, v[0])
		}
		if len(conds) > 0 {
			q += ` WHERE ` + strings.Join(conds, ` AND `)
		}
		if v := r.URL.Query().Get("limit"); v != "" {
			n, e := strconv.Atoi(v)
			if e != nil || n < 1 {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("limit 无效"))
				return
			}
			q += ` LIMIT ?`
			args = append(args, n)
		}
	}
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		m := map[string]any{}
		for i, c := range cols {
			switch v := vals[i].(type) {
			case []byte:
				m[c] = string(v)
			default:
				m[c] = v
			}
		}
		out = append(out, m)
	}
	if out == nil {
		out = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) dataPost(ctx context.Context, w http.ResponseWriter, r *http.Request, db *sql.DB, table string) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("请求体必须是 JSON 对象"))
		return
	}
	fields := make([]string, 0, len(body))
	for k := range body {
		if !validIdentifier(k) {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("字段名不合法: %q", k))
			return
		}
		fields = append(fields, k)
	}
	if err := ensureTable(db, table, fields); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	fields = append(fields, "id", "created_at")
	placeholders := make([]string, len(fields))
	args := make([]any, len(fields))
	for i, f := range fields {
		placeholders[i] = "?"
		switch f {
		case "id":
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			args[i] = hex.EncodeToString(b)
		case "created_at":
			args[i] = time.Now().Unix()
		default:
			args[i] = fmt.Sprint(body[f])
		}
	}
	cols := make([]string, len(fields))
	for i, f := range fields {
		cols[i] = quoteIdent(f)
	}
	q := `INSERT INTO ` + quoteIdent(table) + ` (` + strings.Join(cols, ",") + `) VALUES (` + strings.Join(placeholders, ",") + `)`
	if _, err := db.ExecContext(ctx, q, args...); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "id": args[len(fields)-2]})
}

func (s *Server) dataPut(ctx context.Context, w http.ResponseWriter, r *http.Request, db *sql.DB, table, rowID string) {
	if rowID == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("缺少记录 ID"))
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sets := []string{}
	args := []any{}
	for k, v := range body {
		if !validIdentifier(k) {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("字段名不合法: %q", k))
			return
		}
		sets = append(sets, quoteIdent(k)+"=?")
		args = append(args, fmt.Sprint(v))
	}
	if len(sets) == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("没有要更新的字段"))
		return
	}
	args = append(args, rowID)
	q := `UPDATE ` + quoteIdent(table) + ` SET ` + strings.Join(sets, ",") + ` WHERE id=?`
	res, err := db.ExecContext(ctx, q, args...)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	n, _ := res.RowsAffected()
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "affected": n})
}

func (s *Server) dataDelete(ctx context.Context, w http.ResponseWriter, db *sql.DB, table, rowID string) {
	if rowID == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("缺少记录 ID"))
		return
	}
	_, err := db.ExecContext(ctx, `DELETE FROM `+quoteIdent(table)+` WHERE id=?`, rowID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
