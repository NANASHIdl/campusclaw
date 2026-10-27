package mcp

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Repo 负责三张表的读写。所有查询都带 class_id，跨班按「不存在」处理。
type Repo struct{ DB *sql.DB }

// serverColumns 是列表/详情共用的列，has_auth 由令牌长度推出，不返回令牌本身。
const serverColumns = `s.id, s.name, s.base_url, s.connected,
	CHAR_LENGTH(s.auth_token) > 0,
	(SELECT COUNT(*) FROM mcp_tools t WHERE t.server_id = s.id),
	COALESCE(s.last_error, ''), s.created_at, s.updated_at`

func scanServer(scan func(dest ...any) error) (Server, error) {
	var s Server
	var created, updated time.Time
	if err := scan(&s.ID, &s.Name, &s.BaseURL, &s.Connected, &s.HasAuth,
		&s.ToolCount, &s.LastError, &created, &updated); err != nil {
		return Server{}, err
	}
	s.CreatedAt = FormatTime(created)
	s.UpdatedAt = FormatTime(updated)
	return s, nil
}

// ListServers 列出本班服务器（按编号升序）。
func (r *Repo) ListServers(classID int64) ([]Server, error) {
	rows, err := r.DB.Query(
		`SELECT `+serverColumns+` FROM mcp_servers s WHERE s.class_id = ? ORDER BY s.id`, classID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Server{}
	for rows.Next() {
		s, err := scanServer(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetServer 取本班一台服务器；跨班或不存在返回 ErrNotFound。
func (r *Repo) GetServer(classID, id int64) (Server, error) {
	row := r.DB.QueryRow(
		`SELECT `+serverColumns+` FROM mcp_servers s WHERE s.class_id = ? AND s.id = ?`, classID, id)
	s, err := scanServer(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	return s, err
}

// FindServerByName 按名称取本班服务器（技能引用校验用）。
func (r *Repo) FindServerByName(classID int64, name string) (Server, error) {
	row := r.DB.QueryRow(
		`SELECT `+serverColumns+` FROM mcp_servers s WHERE s.class_id = ? AND s.name = ?`, classID, name)
	s, err := scanServer(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	return s, err
}

// ServerToken 单独取令牌，只有发起连接与调用时才用得到。
func (r *Repo) ServerToken(classID, id int64) (string, error) {
	var token string
	err := r.DB.QueryRow(
		`SELECT auth_token FROM mcp_servers WHERE class_id = ? AND id = ?`, classID, id).Scan(&token)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return token, err
}

// CreateServer 登记一台服务器，状态为未连接。
func (r *Repo) CreateServer(classID int64, in Input) (int64, error) {
	res, err := r.DB.Exec(
		`INSERT INTO mcp_servers (class_id, name, base_url, auth_token, connected)
		 VALUES (?, ?, ?, ?, 0)`,
		classID, in.Name, in.BaseURL, in.AuthToken)
	if err != nil {
		if isDuplicate(err) {
			return 0, ErrDuplicateName
		}
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateServer 修改服务器。地址或令牌变化时清空连接状态与工具清单。
func (r *Repo) UpdateServer(classID, id int64, in Input) error {
	old, err := r.GetServer(classID, id)
	if err != nil {
		return err
	}
	oldToken, err := r.ServerToken(classID, id)
	if err != nil {
		return err
	}

	token := oldToken
	if !in.KeepToken {
		token = in.AuthToken
	}
	changed := old.BaseURL != in.BaseURL || token != oldToken

	tx, err := r.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`UPDATE mcp_servers SET name = ?, base_url = ?, auth_token = ? WHERE class_id = ? AND id = ?`,
		in.Name, in.BaseURL, token, classID, id); err != nil {
		if isDuplicate(err) {
			return ErrDuplicateName
		}
		return err
	}

	if changed {
		if _, err := tx.Exec(`DELETE FROM mcp_tools WHERE server_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`UPDATE mcp_servers SET connected = 0, connected_at = NULL, last_error = NULL
			  WHERE class_id = ? AND id = ?`, classID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteServer 删除服务器（工具清单级联删除，历史调用记录保留）。
func (r *Repo) DeleteServer(classID, id int64) error {
	res, err := r.DB.Exec(`DELETE FROM mcp_servers WHERE class_id = ? AND id = ?`, classID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReplaceTools 用本次发现结果**整体替换**工具清单，并把服务器标记为已连接。
func (r *Repo) ReplaceTools(serverID int64, tools []Tool) error {
	tx, err := r.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM mcp_tools WHERE server_id = ?`, serverID); err != nil {
		return err
	}
	for _, t := range tools {
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		if _, err := tx.Exec(
			`INSERT INTO mcp_tools (server_id, name, description, input_schema) VALUES (?, ?, ?, ?)`,
			serverID, t.Name, t.Description, string(schema)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(
		`UPDATE mcp_servers SET connected = 1, connected_at = CURRENT_TIMESTAMP, last_error = NULL
		  WHERE id = ?`, serverID); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkConnectFailed 把连接失败写进 last_error，并保持未连接；不写入本次发现结果。
func (r *Repo) MarkConnectFailed(serverID int64, reason string) error {
	_, err := r.DB.Exec(
		`UPDATE mcp_servers SET connected = 0, connected_at = NULL, last_error = ?
		  WHERE id = ?`, truncate(reason, 250), serverID)
	return err
}

// MarkDisconnected 断开：清空工具清单并置为未连接。
func (r *Repo) MarkDisconnected(serverID int64) error {
	tx, err := r.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM mcp_tools WHERE server_id = ?`, serverID); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE mcp_servers SET connected = 0, connected_at = NULL WHERE id = ?`, serverID); err != nil {
		return err
	}
	return tx.Commit()
}

// ListTools 返回最近一次发现结果。
func (r *Repo) ListTools(serverID int64) ([]Tool, error) {
	rows, err := r.DB.Query(
		`SELECT name, description, input_schema FROM mcp_tools WHERE server_id = ? ORDER BY name`,
		serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Tool{}
	for rows.Next() {
		var t Tool
		var schema string
		if err := rows.Scan(&t.Name, &t.Description, &schema); err != nil {
			return nil, err
		}
		t.InputSchema = json.RawMessage(schema)
		out = append(out, t)
	}
	return out, rows.Err()
}

// FindTool 在清单里找一个工具。
func (r *Repo) FindTool(serverID int64, name string) (Tool, bool, error) {
	var t Tool
	var schema string
	err := r.DB.QueryRow(
		`SELECT name, description, input_schema FROM mcp_tools WHERE server_id = ? AND name = ?`,
		serverID, name).Scan(&t.Name, &t.Description, &schema)
	if errors.Is(err, sql.ErrNoRows) {
		return Tool{}, false, nil
	}
	if err != nil {
		return Tool{}, false, err
	}
	t.InputSchema = json.RawMessage(schema)
	return t, true, nil
}

// ValidateRef 是保存技能时的四类校验（配置期从严）。
//
// 返回的错误文案直接给教师看，所以每一句都要说清「哪里不对、怎么改」。
func (r *Repo) ValidateRef(classID int64, raw string) error {
	ref, err := ParseRef(raw)
	if err != nil {
		return err
	}
	server, err := r.FindServerByName(classID, ref.Server)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("服务器「%s」未登记，请先在「工具服务」里登记并连接", ref.Server)
	}
	if err != nil {
		return err
	}
	if !server.Connected {
		return fmt.Errorf("服务器「%s」当前未连接，请先连接再保存引用", ref.Server)
	}
	tools, err := r.ListTools(server.ID)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if t.Name == ref.Tool {
			return nil
		}
		names = append(names, t.Name)
	}
	return fmt.Errorf("工具「%s」不在服务器「%s」的发现清单里，可用工具：%s",
		ref.Tool, ref.Server, strings.Join(names, "、"))
}

// InsertCall 写一条调用记录。只增不改。
func (r *Repo) InsertCall(rec CallRecord) error {
	var convID any
	if rec.ConversationID > 0 {
		convID = rec.ConversationID
	}
	_, err := r.DB.Exec(
		`INSERT INTO mcp_tool_calls
		   (class_id, server_id, server_name, tool_name, tool_ref, status, user_id,
		    conversation_id, round_no, detail)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ClassID, nullableID(rec.ServerID), rec.ServerName, rec.ToolName, rec.ToolRef,
		rec.Status, rec.UserID, convID, rec.RoundNo, rec.Detail)
	return err
}

// ListCalls 按时间倒序列出本班最近的调用记录。
func (r *Repo) ListCalls(classID int64, limit int) ([]CallRecord, error) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	rows, err := r.DB.Query(
		`SELECT id, tool_ref, server_name, tool_name, status, user_id,
		        COALESCE(conversation_id, 0), round_no, COALESCE(detail, ''), created_at
		   FROM mcp_tool_calls WHERE class_id = ?
		  ORDER BY created_at DESC, id DESC LIMIT ?`, classID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []CallRecord{}
	for rows.Next() {
		var rec CallRecord
		var created time.Time
		if err := rows.Scan(&rec.ID, &rec.ToolRef, &rec.ServerName, &rec.ToolName, &rec.Status,
			&rec.UserID, &rec.ConversationID, &rec.RoundNo, &rec.Detail, &created); err != nil {
			return nil, err
		}
		rec.CreatedAt = FormatTime(created)
		out = append(out, rec)
	}
	return out, rows.Err()
}

func nullableID(id int64) any {
	if id <= 0 {
		return nil
	}
	return id
}

func isDuplicate(err error) bool {
	return strings.Contains(err.Error(), "1062") || strings.Contains(err.Error(), "Duplicate entry")
}
