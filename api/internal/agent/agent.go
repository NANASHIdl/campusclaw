// Package agent 负责智能体的存取。智能体是班级级配置单位：
// 只保存角色提示词与两份绑定（技能、材料），不复制它们的正文。
package agent

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// 字段上限。
const (
	MaxNameRunes   = 64
	MaxPromptRunes = 2000
)

// 对外错误。
var (
	ErrNotFound      = errors.New("智能体不存在")
	ErrDuplicateName = errors.New("本班已有同名智能体")
	ErrBadName       = fmt.Errorf("名称需为 1 至 %d 个字符", MaxNameRunes)
	ErrPromptTooLong = fmt.Errorf("提示词不能超过 %d 个字符", MaxPromptRunes)
	// ErrBindingNotFound 表示绑定的技能或材料不属于本班。
	ErrBindingNotFound = errors.New("绑定的技能或材料不存在")
)

// Agent 是 tutor_agents 表的一行，附带两份绑定。
type Agent struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Prompt      string  `json:"prompt"`
	Enabled     bool    `json:"enabled"`
	SkillIDs    []int64 `json:"skill_ids"`
	MaterialIDs []int64 `json:"material_ids"`
}

// Input 是创建/修改的入参。
type Input struct {
	Name        string
	Prompt      string
	Enabled     bool
	SkillIDs    []int64
	MaterialIDs []int64
}

// Normalize 校验并规范化入参。
//
// 提示词允许为空（课件：为空时回退到默认角色设定，不因此拒绝请求）。
func (in *Input) Normalize() error {
	in.Name = strings.TrimSpace(in.Name)
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > MaxNameRunes {
		return ErrBadName
	}
	if utf8.RuneCountInString(in.Prompt) > MaxPromptRunes {
		return ErrPromptTooLong
	}
	in.SkillIDs = dedupe(in.SkillIDs)
	in.MaterialIDs = dedupe(in.MaterialIDs)
	return nil
}

func dedupe(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// Repo 负责 tutor_agents 与两张绑定表。
type Repo struct{ DB *sql.DB }

// List 列出某班智能体。onlyEnabled 为真时只返回已启用的（学生视角）。
func (r *Repo) List(classID int64, onlyEnabled bool) ([]Agent, error) {
	q := `SELECT id, name, prompt, enabled FROM tutor_agents WHERE class_id = ?`
	if onlyEnabled {
		q += ` AND enabled = 1`
	}
	q += ` ORDER BY id`

	rows, err := r.DB.Query(q, classID)
	if err != nil {
		return nil, err
	}
	var list []Agent
	for rows.Next() {
		var a Agent
		var enabled int
		if err := rows.Scan(&a.ID, &a.Name, &a.Prompt, &enabled); err != nil {
			rows.Close()
			return nil, err
		}
		a.Enabled = enabled == 1
		list = append(list, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range list {
		if err := r.loadBindings(&list[i]); err != nil {
			return nil, err
		}
	}
	if list == nil {
		list = []Agent{}
	}
	return list, nil
}

// Get 取某班的一个智能体；跨班或不存在返回 ErrNotFound。
// 学生读取未启用的智能体由调用方按 404 处理（这里不区分）。
func (r *Repo) Get(classID, id int64) (Agent, error) {
	var a Agent
	var enabled int
	err := r.DB.QueryRow(
		`SELECT id, name, prompt, enabled FROM tutor_agents WHERE class_id = ? AND id = ?`,
		classID, id,
	).Scan(&a.ID, &a.Name, &a.Prompt, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, err
	}
	a.Enabled = enabled == 1
	if err := r.loadBindings(&a); err != nil {
		return Agent{}, err
	}
	return a, nil
}

func (r *Repo) loadBindings(a *Agent) error {
	a.SkillIDs = []int64{}
	a.MaterialIDs = []int64{}

	rows, err := r.DB.Query(`SELECT skill_id FROM tutor_agent_skills WHERE agent_id = ? ORDER BY skill_id`, a.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		a.SkillIDs = append(a.SkillIDs, id)
	}
	rows.Close()

	rows, err = r.DB.Query(`SELECT material_id FROM tutor_agent_materials WHERE agent_id = ? ORDER BY material_id`, a.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		a.MaterialIDs = append(a.MaterialIDs, id)
	}
	rows.Close()
	return rows.Err()
}

// Create 新建智能体，并在同一事务内写入两份绑定。
func (r *Repo) Create(classID int64, in Input) (int64, error) {
	if err := r.verifyBindings(classID, in); err != nil {
		return 0, err
	}

	tx, err := r.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(
		`INSERT INTO tutor_agents (class_id, name, prompt, enabled) VALUES (?, ?, ?, ?)`,
		classID, in.Name, in.Prompt, boolToInt(in.Enabled))
	if err != nil {
		if isDuplicate(err) {
			return 0, ErrDuplicateName
		}
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := writeBindings(tx, id, in); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// Update 修改智能体：整份覆盖字段与绑定。跨班/不存在返回 ErrNotFound。
func (r *Repo) Update(classID, id int64, in Input) error {
	if err := r.verifyBindings(classID, in); err != nil {
		return err
	}

	tx, err := r.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(
		`UPDATE tutor_agents SET name = ?, prompt = ?, enabled = ?
		  WHERE class_id = ? AND id = ?`,
		in.Name, in.Prompt, boolToInt(in.Enabled), classID, id)
	if err != nil {
		if isDuplicate(err) {
			return ErrDuplicateName
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// 内容未变时 RowsAffected 为 0，需要再确认存在性。
		var one int
		if err := tx.QueryRow(`SELECT 1 FROM tutor_agents WHERE class_id = ? AND id = ?`, classID, id).Scan(&one); err != nil {
			return ErrNotFound
		}
	}

	if _, err := tx.Exec(`DELETE FROM tutor_agent_skills WHERE agent_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tutor_agent_materials WHERE agent_id = ?`, id); err != nil {
		return err
	}
	if err := writeBindings(tx, id, in); err != nil {
		return err
	}
	return tx.Commit()
}

// verifyBindings 校验绑定的技能与材料都属于本班。
func (r *Repo) verifyBindings(classID int64, in Input) error {
	for _, sid := range in.SkillIDs {
		var one int
		err := r.DB.QueryRow(`SELECT 1 FROM skills WHERE class_id = ? AND id = ?`, classID, sid).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBindingNotFound
		}
		if err != nil {
			return err
		}
	}
	for _, mid := range in.MaterialIDs {
		var one int
		err := r.DB.QueryRow(`SELECT 1 FROM materials WHERE class_id = ? AND id = ?`, classID, mid).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBindingNotFound
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func writeBindings(tx *sql.Tx, agentID int64, in Input) error {
	for _, sid := range in.SkillIDs {
		if _, err := tx.Exec(`INSERT INTO tutor_agent_skills (agent_id, skill_id) VALUES (?, ?)`, agentID, sid); err != nil {
			return err
		}
	}
	for _, mid := range in.MaterialIDs {
		if _, err := tx.Exec(`INSERT INTO tutor_agent_materials (agent_id, material_id) VALUES (?, ?)`, agentID, mid); err != nil {
			return err
		}
	}
	return nil
}

// BoundMaterialIDs 返回该智能体当前绑定的材料编号（每轮提问重新读取）。
func (r *Repo) BoundMaterialIDs(agentID int64) ([]int64, error) {
	rows, err := r.DB.Query(`SELECT material_id FROM tutor_agent_materials WHERE agent_id = ? ORDER BY material_id`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
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
