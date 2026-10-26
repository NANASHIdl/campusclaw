// Package conversation 负责会话与消息的存取。
//
// 会话是私有资源：读取一律按 user_id + class_id 双重过滤，
// 他人会话与不存在的会话对外表现完全一致。
package conversation

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"campusclaw/api/internal/retrieval"
)

// 对外错误。
var (
	ErrNotFound = errors.New("会话不存在")
)

// PlaceholderTitle 是新会话的占位标题。
const PlaceholderTitle = "新的提问"

// TitleRunes 是首轮提问改标题时截取的字数。
const TitleRunes = 24

// Conversation 是 conversations 表的一行。
type Conversation struct {
	ID        int64  `json:"id"`
	ClassID   int64  `json:"class_id"`
	UserID    int64  `json:"user_id"`
	AgentID   int64  `json:"agent_id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Message 是 messages 表的一行。
type Message struct {
	ID        int64           `json:"id"`
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	Citations []retrieval.Hit `json:"citations,omitempty"`
	SkillName string          `json:"skill_name,omitempty"`
	CreatedAt string          `json:"created_at"`
}

// Repo 负责 conversations 与 messages。
type Repo struct{ DB *sql.DB }

// List 列出某用户在本班的会话，按 updated_at 倒序。
func (r *Repo) List(userID, classID int64) ([]Conversation, error) {
	rows, err := r.DB.Query(
		`SELECT id, class_id, user_id, agent_id, title, created_at, updated_at
		   FROM conversations
		  WHERE user_id = ? AND class_id = ?
		  ORDER BY updated_at DESC, id DESC`, userID, classID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Conversation
	for rows.Next() {
		var c Conversation
		var created, updated time.Time
		if err := rows.Scan(&c.ID, &c.ClassID, &c.UserID, &c.AgentID, &c.Title, &created, &updated); err != nil {
			return nil, err
		}
		c.CreatedAt = created.Format("2006-01-02 15:04:05")
		c.UpdatedAt = updated.Format("2006-01-02 15:04:05")
		out = append(out, c)
	}
	if out == nil {
		out = []Conversation{}
	}
	return out, rows.Err()
}

// Get 取会话；不属于该用户或该班时返回 ErrNotFound。
func (r *Repo) Get(userID, classID, id int64) (Conversation, error) {
	var c Conversation
	var created, updated time.Time
	err := r.DB.QueryRow(
		`SELECT id, class_id, user_id, agent_id, title, created_at, updated_at
		   FROM conversations
		  WHERE id = ? AND user_id = ? AND class_id = ?`, id, userID, classID,
	).Scan(&c.ID, &c.ClassID, &c.UserID, &c.AgentID, &c.Title, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, ErrNotFound
	}
	if err != nil {
		return Conversation{}, err
	}
	c.CreatedAt = created.Format("2006-01-02 15:04:05")
	c.UpdatedAt = updated.Format("2006-01-02 15:04:05")
	return c, nil
}

// Create 新建会话，标题为占位串。
func (r *Repo) Create(userID, classID, agentID int64) (Conversation, error) {
	res, err := r.DB.Exec(
		`INSERT INTO conversations (class_id, user_id, agent_id, title) VALUES (?, ?, ?, ?)`,
		classID, userID, agentID, PlaceholderTitle)
	if err != nil {
		return Conversation{}, fmt.Errorf("写入会话失败: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Conversation{}, err
	}
	return r.Get(userID, classID, id)
}

// Messages 取会话的消息。limit > 0 时取末尾 limit 条，返回时仍按 id 升序。
func (r *Repo) Messages(convID int64, limit int) ([]Message, error) {
	q := `SELECT id, role, content, citations_json, COALESCE(skill_name,''), created_at
	        FROM messages WHERE conversation_id = ? ORDER BY id`
	args := []any{convID}
	if limit > 0 {
		q = `SELECT * FROM (
		         SELECT id, role, content, citations_json, COALESCE(skill_name,'') AS skill_name, created_at
		           FROM messages WHERE conversation_id = ? ORDER BY id DESC LIMIT ?
		      ) t ORDER BY t.id`
		args = append(args, limit)
	}

	rows, err := r.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Message
	for rows.Next() {
		var m Message
		var citations sql.NullString
		var created time.Time
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &citations, &m.SkillName, &created); err != nil {
			return nil, err
		}
		m.CreatedAt = created.Format("2006-01-02 15:04:05")
		if citations.Valid && citations.String != "" {
			_ = json.Unmarshal([]byte(citations.String), &m.Citations)
		}
		out = append(out, m)
	}
	if out == nil {
		out = []Message{}
	}
	return out, rows.Err()
}

// AppendUser 写入一条用户消息；若会话标题仍是占位串，同时改写为前 24 字。
// 返回消息编号。
func (r *Repo) AppendUser(convID int64, content string) (int64, error) {
	tx, err := r.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(
		`INSERT INTO messages (conversation_id, role, content) VALUES (?, 'user', ?)`,
		convID, content)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	// 首轮提问改写标题；此后只刷新 updated_at。
	if _, err := tx.Exec(
		`UPDATE conversations
		    SET title = CASE WHEN title = ? THEN ? ELSE title END,
		        updated_at = CURRENT_TIMESTAMP
		  WHERE id = ?`,
		PlaceholderTitle, truncateRunes(content, TitleRunes), convID); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// AppendAssistant 写入助手消息（含引用与命中技能名），并刷新会话更新时间。
func (r *Repo) AppendAssistant(convID int64, content string, citations []retrieval.Hit, skillName string) (int64, error) {
	citationsJSON := "[]"
	if len(citations) > 0 {
		if raw, err := json.Marshal(citations); err == nil {
			citationsJSON = string(raw)
		}
	}

	tx, err := r.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(
		`INSERT INTO messages (conversation_id, role, content, citations_json, skill_name)
		 VALUES (?, 'assistant', ?, ?, ?)`,
		convID, content, citationsJSON, nullIfEmpty(skillName))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE conversations SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, convID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
