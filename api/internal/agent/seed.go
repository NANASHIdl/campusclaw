package agent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"campusclaw/api/internal/skill"
)

// 两份预置技能取自课件样板，逐字保留。
const seedMathSkill = `名称：数学解题引导
适用条件：方程,去括号,分数,通分,面积,应用题,未知数,行程
资料：数学
工具：knowledge_search
启用：是

学生正在自己做数学作业，往往已经写了一步。先看懂他卡在哪一步，只指出下一步该检查什么，例如去括号有没有乘到每一项、分母不同要不要先通分、该设哪个未知数。不要算出最终得数，不要把整道题做完。依据只用数学自学笔记，并标出材料标题和位置。`

const seedChineseSkill = `名称：语文解题引导
适用条件：春晓,默写,阅读,中心,作文,开头,古诗,词语
资料：语文
工具：knowledge_search
启用：是

学生正在自己背诵、做阅读或改作文，卡在某一句。先接住他已经写出的那一句，告诉他往前看哪一句、找哪类句子。不要把诗的下一句、中心句或改好的段落直接写出来。依据只用语文自学笔记，并标出材料标题和位置。`

// seedAgentName 是预置示例智能体的名称（班内唯一）。
const seedAgentName = "解题引导助手"

// SeedFixtures 为指定班级补齐预置技能与示例智能体。
//
// 幂等：技能靠 (class_id, seed_key) 判重，智能体靠 (class_id, name) 判重。
// 与用户种子解耦，因此已有数据的库升级后同样能补上。
func SeedFixtures(db *sql.DB, classID int64) error {
	skillIDs := make([]int64, 0, 2)
	for _, s := range []struct{ key, source string }{
		{"math-tutor", seedMathSkill},
		{"chinese-tutor", seedChineseSkill},
	} {
		id, err := ensureSkill(db, classID, s.key, s.source)
		if err != nil {
			return err
		}
		skillIDs = append(skillIDs, id)
	}

	agentID, err := ensureAgent(db, classID, seedAgentName)
	if err != nil {
		return err
	}

	materialIDs, err := classMaterialIDs(db, classID)
	if err != nil {
		return err
	}

	// 绑定同样幂等：INSERT IGNORE。
	for _, sid := range skillIDs {
		if _, err := db.Exec(`INSERT IGNORE INTO tutor_agent_skills (agent_id, skill_id) VALUES (?, ?)`, agentID, sid); err != nil {
			return fmt.Errorf("绑定预置技能失败: %w", err)
		}
	}
	for _, mid := range materialIDs {
		if _, err := db.Exec(`INSERT IGNORE INTO tutor_agent_materials (agent_id, material_id) VALUES (?, ?)`, agentID, mid); err != nil {
			return fmt.Errorf("绑定班级材料失败: %w", err)
		}
	}

	log.Printf("预置配置就绪：class_id=%d 技能=%d 智能体=%d 绑定材料=%d",
		classID, len(skillIDs), agentID, len(materialIDs))
	return nil
}

func ensureSkill(db *sql.DB, classID int64, seedKey, source string) (int64, error) {
	var id int64
	err := db.QueryRow(`SELECT id FROM skills WHERE class_id = ? AND seed_key = ?`, classID, seedKey).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}

	p, perr := skill.Parse(source)
	if perr != nil {
		return 0, fmt.Errorf("预置技能 %s 解析失败: %w", seedKey, perr)
	}
	toolsJSON, _ := json.Marshal(p.Tools)
	res, err := db.Exec(
		`INSERT INTO skills (class_id, name, trigger_text, subject_text, description,
		                     tools_json, source_text, enabled, seed_key)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		classID, p.Name, p.Trigger, nullIfEmpty(p.Subject), p.Description,
		string(toolsJSON), p.Source, boolToInt(p.Enabled), seedKey)
	if err != nil {
		if isDuplicate(err) {
			// 教师已自建同名技能：不覆盖，直接取它。
			if qerr := db.QueryRow(`SELECT id FROM skills WHERE class_id = ? AND name = ?`, classID, p.Name).Scan(&id); qerr == nil {
				return id, nil
			}
		}
		return 0, fmt.Errorf("写入预置技能失败: %w", err)
	}
	return res.LastInsertId()
}

func ensureAgent(db *sql.DB, classID int64, name string) (int64, error) {
	var id int64
	err := db.QueryRow(`SELECT id FROM tutor_agents WHERE class_id = ? AND name = ?`, classID, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	// 提示词留空：作答时回退到内置默认角色设定。
	res, err := db.Exec(
		`INSERT INTO tutor_agents (class_id, name, prompt, enabled) VALUES (?, ?, '', 1)`,
		classID, name)
	if err != nil {
		return 0, fmt.Errorf("写入预置智能体失败: %w", err)
	}
	return res.LastInsertId()
}

func classMaterialIDs(db *sql.DB, classID int64) ([]int64, error) {
	rows, err := db.Query(`SELECT id FROM materials WHERE class_id = ? ORDER BY id`, classID)
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

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
