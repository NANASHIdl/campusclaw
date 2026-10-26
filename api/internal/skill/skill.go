// Package skill 负责技能文件的解析与存取。
//
// 技能是单一文本文件：空行以上是元数据（中文键行），空行以下是引导规程。
// 解析失败一律返回面向教师可读的错误——教师是唯一输入方，含糊的错误等于无法修正。
package skill

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// 长度上限（按字符计）。
const (
	MaxFileRunes        = 8000
	MaxNameRunes        = 32
	MaxTriggerRunes     = 120
	MaxSubjectRunes     = 32
	MaxDescriptionRunes = 2000

	// ToolKnowledgeSearch 是本课唯一允许的工具名。
	ToolKnowledgeSearch = "knowledge_search"
)

// 解析错误。
var (
	ErrEmptyFile    = errors.New("技能文件是空的")
	ErrNoBody       = errors.New("技能文件需要一个空行，空行下面写技能说明")
	ErrTooLongFile  = fmt.Errorf("技能文件不能超过 %d 字", MaxFileRunes)
	ErrUnknownField = errors.New("技能文件里有未定义的字段")
	ErrDuplicate    = errors.New("技能文件里同一字段出现了两次")
	ErrBadTool      = fmt.Errorf("工具只能是 %s", ToolKnowledgeSearch)
	ErrBadEnabled   = errors.New("启用只能写「是」或「否」")
	ErrNotFound     = errors.New("技能不存在")
	ErrDuplicateName = errors.New("本班已有同名技能")
)

// 元数据键名。
const (
	keyName    = "名称"
	keyTrigger = "适用条件"
	keySubject = "资料"
	keyTools   = "工具"
	keyEnabled = "启用"
)

var knownKeys = map[string]bool{
	keyName: true, keyTrigger: true, keySubject: true, keyTools: true, keyEnabled: true,
}

// Parsed 是解析后的技能。
type Parsed struct {
	Name        string
	Trigger     string
	Subject     string
	Description string
	Tools       []string
	Enabled     bool
	Source      string
}

// Parse 把技能文件原文解析为结构化字段。
func Parse(source string) (Parsed, error) {
	if strings.TrimSpace(source) == "" {
		return Parsed{}, ErrEmptyFile
	}
	if utf8.RuneCountInString(source) > MaxFileRunes {
		return Parsed{}, ErrTooLongFile
	}

	lines := strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n")

	// 元数据与正文以第一个空行分隔。
	sep := -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			sep = i
			break
		}
	}
	if sep < 0 {
		return Parsed{}, ErrNoBody
	}
	body := strings.TrimSpace(strings.Join(lines[sep+1:], "\n"))
	if body == "" {
		return Parsed{}, ErrNoBody
	}

	fields := map[string]string{}
	for _, raw := range lines[:sep] {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := splitKeyValue(line)
		if !ok {
			return Parsed{}, fmt.Errorf("这一行看不懂，应写成「字段：值」——%s", line)
		}
		if !knownKeys[key] {
			return Parsed{}, fmt.Errorf("%w：%s", ErrUnknownField, key)
		}
		if _, dup := fields[key]; dup {
			return Parsed{}, fmt.Errorf("%w：%s", ErrDuplicate, key)
		}
		fields[key] = value
	}

	for _, required := range []string{keyName, keyTrigger, keyEnabled} {
		if strings.TrimSpace(fields[required]) == "" {
			return Parsed{}, fmt.Errorf("缺少必填字段：%s", required)
		}
	}

	name := strings.TrimSpace(fields[keyName])
	if utf8.RuneCountInString(name) > MaxNameRunes {
		return Parsed{}, fmt.Errorf("名称不能超过 %d 字", MaxNameRunes)
	}
	trigger := strings.TrimSpace(fields[keyTrigger])
	if utf8.RuneCountInString(trigger) > MaxTriggerRunes {
		return Parsed{}, fmt.Errorf("适用条件不能超过 %d 字", MaxTriggerRunes)
	}
	subject := strings.TrimSpace(fields[keySubject])
	if utf8.RuneCountInString(subject) > MaxSubjectRunes {
		return Parsed{}, fmt.Errorf("资料不能超过 %d 字", MaxSubjectRunes)
	}
	if utf8.RuneCountInString(body) > MaxDescriptionRunes {
		return Parsed{}, fmt.Errorf("说明不能超过 %d 字", MaxDescriptionRunes)
	}

	tools, err := parseTools(fields[keyTools])
	if err != nil {
		return Parsed{}, err
	}

	enabled, err := parseEnabled(fields[keyEnabled])
	if err != nil {
		return Parsed{}, err
	}

	return Parsed{
		Name: name, Trigger: trigger, Subject: subject,
		Description: body, Tools: tools, Enabled: enabled, Source: source,
	}, nil
}

// splitKeyValue 支持中英文冒号。
func splitKeyValue(line string) (string, string, bool) {
	for _, sep := range []string{"：", ":"} {
		if k, v, ok := strings.Cut(line, sep); ok {
			return strings.TrimSpace(k), strings.TrimSpace(v), true
		}
	}
	return "", "", false
}

func parseTools(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}, nil
	}
	parts := splitList(raw)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != ToolKnowledgeSearch {
			return nil, fmt.Errorf("%w（收到 %q）", ErrBadTool, p)
		}
		out = append(out, p)
	}
	return out, nil
}

func parseEnabled(raw string) (bool, error) {
	switch strings.TrimSpace(raw) {
	case "是", "true", "1", "yes":
		return true, nil
	case "否", "false", "0", "no":
		return false, nil
	}
	return false, ErrBadEnabled
}

// SplitTriggers 按中英文逗号、顿号、分号与竖线切分适用条件。
func SplitTriggers(s string) []string {
	return splitList(s)
}

func splitList(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case ',', '，', '、', ';', '；', '|':
			return true
		}
		return false
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if v := strings.TrimSpace(f); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// --- 存取 -------------------------------------------------------------------

// Skill 是 skills 表的一行。
type Skill struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Trigger   string   `json:"trigger"`
	Subject   string   `json:"subject"`
	Desc      string   `json:"description"`
	Tools     []string `json:"tools"`
	Enabled   bool     `json:"enabled"`
	Source    string   `json:"source"`
	SeedKey   string   `json:"-"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

// Repo 负责 skills 表。
type Repo struct{ DB *sql.DB }

// List 列出某班技能（按 id 升序）。
func (r *Repo) List(classID int64) ([]Skill, error) {
	rows, err := r.DB.Query(
		`SELECT id, name, trigger_text, COALESCE(subject_text,''), description, tools_json,
		        source_text, enabled, COALESCE(seed_key,''), created_at, updated_at
		   FROM skills WHERE class_id = ? ORDER BY id`, classID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSkills(rows)
}

// Get 取某班的一条技能；不存在返回 ErrNotFound。
func (r *Repo) Get(classID, id int64) (Skill, error) {
	rows, err := r.DB.Query(
		`SELECT id, name, trigger_text, COALESCE(subject_text,''), description, tools_json,
		        source_text, enabled, COALESCE(seed_key,''), created_at, updated_at
		   FROM skills WHERE class_id = ? AND id = ?`, classID, id)
	if err != nil {
		return Skill{}, err
	}
	defer rows.Close()
	list, err := scanSkills(rows)
	if err != nil {
		return Skill{}, err
	}
	if len(list) == 0 {
		return Skill{}, ErrNotFound
	}
	return list[0], nil
}

// Create 新建技能。同名返回 ErrDuplicateName。
func (r *Repo) Create(classID int64, p Parsed) (int64, error) {
	toolsJSON, _ := json.Marshal(p.Tools)
	res, err := r.DB.Exec(
		`INSERT INTO skills (class_id, name, trigger_text, subject_text, description,
		                     tools_json, source_text, enabled)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		classID, p.Name, p.Trigger, nullIfEmpty(p.Subject), p.Description,
		string(toolsJSON), p.Source, boolToInt(p.Enabled))
	if err != nil {
		if isDuplicate(err) {
			return 0, ErrDuplicateName
		}
		return 0, err
	}
	return res.LastInsertId()
}

// Update 改写技能。跨班或不存在返回 ErrNotFound，同名返回 ErrDuplicateName。
func (r *Repo) Update(classID, id int64, p Parsed) error {
	toolsJSON, _ := json.Marshal(p.Tools)
	res, err := r.DB.Exec(
		`UPDATE skills
		    SET name = ?, trigger_text = ?, subject_text = ?, description = ?,
		        tools_json = ?, source_text = ?, enabled = ?
		  WHERE class_id = ? AND id = ?`,
		p.Name, p.Trigger, nullIfEmpty(p.Subject), p.Description,
		string(toolsJSON), p.Source, boolToInt(p.Enabled), classID, id)
	if err != nil {
		if isDuplicate(err) {
			return ErrDuplicateName
		}
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// 可能内容与原来完全一致；用一次存在性查询区分。
		if _, err := r.Get(classID, id); err != nil {
			return ErrNotFound
		}
	}
	return nil
}

// Delete 删除技能；不存在返回 ErrNotFound。绑定关系由外键级联清除。
func (r *Repo) Delete(classID, id int64) error {
	res, err := r.DB.Exec(`DELETE FROM skills WHERE class_id = ? AND id = ?`, classID, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListEnabledBound 返回某智能体绑定且已启用的技能，按 id 升序（路由候选集）。
func (r *Repo) ListEnabledBound(agentID int64) ([]Skill, error) {
	rows, err := r.DB.Query(
		`SELECT s.id, s.name, s.trigger_text, COALESCE(s.subject_text,''), s.description,
		        s.tools_json, s.source_text, s.enabled, COALESCE(s.seed_key,''),
		        s.created_at, s.updated_at
		   FROM skills s
		   JOIN tutor_agent_skills tas ON tas.skill_id = s.id
		  WHERE tas.agent_id = ? AND s.enabled = 1
		  ORDER BY s.id`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSkills(rows)
}

func scanSkills(rows *sql.Rows) ([]Skill, error) {
	var out []Skill
	for rows.Next() {
		var s Skill
		var toolsJSON string
		var enabled int
		if err := rows.Scan(&s.ID, &s.Name, &s.Trigger, &s.Subject, &s.Desc, &toolsJSON,
			&s.Source, &enabled, &s.SeedKey, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.Enabled = enabled == 1
		if err := json.Unmarshal([]byte(toolsJSON), &s.Tools); err != nil || s.Tools == nil {
			s.Tools = []string{}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func isDuplicate(err error) bool {
	return strings.Contains(err.Error(), "1062") || strings.Contains(err.Error(), "Duplicate entry")
}
