// Package db 负责 MySQL 连接、建表与种子数据。
package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"campusclaw/api/internal/auth"
	"campusclaw/api/internal/config"
)

// Connect 连接 MySQL 并等待其就绪（容器首启时 db 可能尚未可用）。
func Connect(cfg *config.Config) (*sql.DB, error) {
	database, err := sql.Open("mysql", cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	database.SetMaxOpenConns(16)
	database.SetMaxIdleConns(8)
	database.SetConnMaxLifetime(30 * time.Minute)

	deadline := time.Now().Add(60 * time.Second)
	for attempt := 1; ; attempt++ {
		pingErr := database.Ping()
		if pingErr == nil {
			log.Printf("已连接数据库 %s:%s/%s", cfg.DBHost, cfg.DBPort, cfg.DBName)
			return database, nil
		}
		if time.Now().After(deadline) {
			database.Close()
			return nil, fmt.Errorf("等待数据库就绪超时: %w", pingErr)
		}
		log.Printf("等待数据库就绪（第 %d 次）: %v", attempt, pingErr)
		time.Sleep(2 * time.Second)
	}
}

// schema 是全部业务表：classes、users、revoked_tokens、materials、knowledge_entries、
// knowledge_chunks，以及第 5 课的 tutor_agents、skills、tutor_agent_skills、
// tutor_agent_materials、conversations、messages。
// 带租户列的表其 class_id 均非空且带索引。
var schema = []string{
	`CREATE TABLE IF NOT EXISTS classes (
		id   INT PRIMARY KEY,
		name VARCHAR(64) NOT NULL UNIQUE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

	`CREATE TABLE IF NOT EXISTS users (
		id            BIGINT AUTO_INCREMENT PRIMARY KEY,
		username      VARCHAR(64) NOT NULL UNIQUE,
		password_hash VARCHAR(255) NOT NULL,
		role          ENUM('teacher','student') NOT NULL,
		class_id      INT NOT NULL,
		CONSTRAINT fk_users_class FOREIGN KEY (class_id) REFERENCES classes(id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

	// 第 5 课：认证改为 JWT，sessions 表退役（迁移时 DROP），改用吊销表。
	`CREATE TABLE IF NOT EXISTS revoked_tokens (
		jti        CHAR(32) PRIMARY KEY,
		user_id    BIGINT NOT NULL,
		expires_at DATETIME NOT NULL,
		revoked_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		INDEX idx_revoked_expires (expires_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

	`CREATE TABLE IF NOT EXISTS materials (
		id          BIGINT AUTO_INCREMENT PRIMARY KEY,
		title       VARCHAR(255) NOT NULL,
		class_id    INT NOT NULL,
		file_path   VARCHAR(512) NOT NULL,
		uploader_id BIGINT NULL,
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		CONSTRAINT fk_materials_class FOREIGN KEY (class_id) REFERENCES classes(id),
		CONSTRAINT fk_materials_uploader FOREIGN KEY (uploader_id) REFERENCES users(id),
		INDEX idx_materials_class (class_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

	`CREATE TABLE IF NOT EXISTS knowledge_entries (
		id          BIGINT AUTO_INCREMENT PRIMARY KEY,
		material_id BIGINT NOT NULL,
		class_id    INT NOT NULL,
		body_text   MEDIUMTEXT NOT NULL,
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		CONSTRAINT fk_ke_material FOREIGN KEY (material_id) REFERENCES materials(id) ON DELETE CASCADE,
		CONSTRAINT fk_ke_class FOREIGN KEY (class_id) REFERENCES classes(id),
		INDEX idx_ke_class (class_id),
		INDEX idx_ke_material (material_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

	// 第 4 课：切片是检索的最小单位。正文存 MySQL，向量存 Qdrant；
	// 向量主键 = 本表主键。chunk_text 建 ngram 全文索引供关键字检索。
	`CREATE TABLE IF NOT EXISTS knowledge_chunks (
		id                 BIGINT AUTO_INCREMENT PRIMARY KEY,
		knowledge_entry_id BIGINT NOT NULL,
		material_id        BIGINT NOT NULL,
		class_id           INT NOT NULL,
		chunk_index        INT NOT NULL,
		chunk_text         MEDIUMTEXT NOT NULL,
		start_offset       INT NOT NULL,
		end_offset         INT NOT NULL,
		index_status       ENUM('ready','failed') NOT NULL DEFAULT 'ready',
		chunk_strategy     VARCHAR(16) NOT NULL,
		created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		CONSTRAINT fk_chunks_entry FOREIGN KEY (knowledge_entry_id) REFERENCES knowledge_entries(id) ON DELETE CASCADE,
		CONSTRAINT fk_chunks_material FOREIGN KEY (material_id) REFERENCES materials(id) ON DELETE CASCADE,
		CONSTRAINT fk_chunks_class FOREIGN KEY (class_id) REFERENCES classes(id),
		INDEX idx_chunks_class (class_id),
		INDEX idx_chunks_material (material_id),
		FULLTEXT KEY ft_chunks_text (chunk_text) WITH PARSER ngram
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

	// 第 5 课：智能体是班级级配置单位，只保存绑定关系，不复制技能与材料正文。
	`CREATE TABLE IF NOT EXISTS tutor_agents (
		id         BIGINT AUTO_INCREMENT PRIMARY KEY,
		class_id   INT NOT NULL,
		name       VARCHAR(64) NOT NULL,
		prompt     TEXT NOT NULL,
		enabled    TINYINT(1) NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		CONSTRAINT fk_agents_class FOREIGN KEY (class_id) REFERENCES classes(id),
		UNIQUE KEY uk_agents_class_name (class_id, name),
		INDEX idx_agents_class (class_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

	`CREATE TABLE IF NOT EXISTS skills (
		id           BIGINT AUTO_INCREMENT PRIMARY KEY,
		class_id     INT NOT NULL,
		name         VARCHAR(32) NOT NULL,
		trigger_text VARCHAR(120) NOT NULL,
		subject_text VARCHAR(32) NULL,
		description  TEXT NOT NULL,
		tools_json   JSON NOT NULL,
		source_text  MEDIUMTEXT NOT NULL,
		enabled      TINYINT(1) NOT NULL DEFAULT 1,
		seed_key     VARCHAR(64) NULL,
		created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		CONSTRAINT fk_skills_class FOREIGN KEY (class_id) REFERENCES classes(id),
		UNIQUE KEY uk_skills_class_name (class_id, name),
		UNIQUE KEY uk_skills_seed (class_id, seed_key)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

	`CREATE TABLE IF NOT EXISTS tutor_agent_skills (
		agent_id BIGINT NOT NULL,
		skill_id BIGINT NOT NULL,
		PRIMARY KEY (agent_id, skill_id),
		CONSTRAINT fk_tas_agent FOREIGN KEY (agent_id) REFERENCES tutor_agents(id) ON DELETE CASCADE,
		CONSTRAINT fk_tas_skill FOREIGN KEY (skill_id) REFERENCES skills(id) ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

	`CREATE TABLE IF NOT EXISTS tutor_agent_materials (
		agent_id    BIGINT NOT NULL,
		material_id BIGINT NOT NULL,
		PRIMARY KEY (agent_id, material_id),
		CONSTRAINT fk_tam_agent FOREIGN KEY (agent_id) REFERENCES tutor_agents(id) ON DELETE CASCADE,
		CONSTRAINT fk_tam_material FOREIGN KEY (material_id) REFERENCES materials(id) ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

	// 会话只记归属、选定的智能体与标题；正文与出处落在 messages。
	`CREATE TABLE IF NOT EXISTS conversations (
		id         BIGINT AUTO_INCREMENT PRIMARY KEY,
		class_id   INT NOT NULL,
		user_id    BIGINT NOT NULL,
		agent_id   BIGINT NOT NULL,
		title      VARCHAR(64) NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		CONSTRAINT fk_conv_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
		CONSTRAINT fk_conv_agent FOREIGN KEY (agent_id) REFERENCES tutor_agents(id),
		INDEX idx_conv_user (user_id, updated_at),
		INDEX idx_conv_class (class_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

	`CREATE TABLE IF NOT EXISTS messages (
		id              BIGINT AUTO_INCREMENT PRIMARY KEY,
		conversation_id BIGINT NOT NULL,
		role            ENUM('user','assistant') NOT NULL,
		content         MEDIUMTEXT NOT NULL,
		citations_json  JSON NULL,
		skill_name      VARCHAR(32) NULL,
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		CONSTRAINT fk_msg_conv FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE,
		INDEX idx_msg_conv (conversation_id, id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

	// --- 第 6 课：MCP 工具接入 -------------------------------------------------

	// auth_token 只落库，不出现在任何响应里；响应只给 has_auth 布尔值。
	`CREATE TABLE IF NOT EXISTS mcp_servers (
		id           BIGINT AUTO_INCREMENT PRIMARY KEY,
		class_id     INT NOT NULL,
		name         VARCHAR(32) NOT NULL,
		base_url     VARCHAR(255) NOT NULL,
		auth_token   VARCHAR(255) NOT NULL,
		connected    TINYINT(1) NOT NULL DEFAULT 0,
		connected_at DATETIME NULL,
		last_error   VARCHAR(255) NULL,
		created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		UNIQUE KEY uk_mcp_server_name (class_id, name),
		INDEX idx_mcp_server_class (class_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

	// 工具清单恒等于「最近一次成功的发现结果」：每次连接整体替换。
	`CREATE TABLE IF NOT EXISTS mcp_tools (
		id            BIGINT AUTO_INCREMENT PRIMARY KEY,
		server_id     BIGINT NOT NULL,
		name          VARCHAR(64) NOT NULL,
		description   TEXT NOT NULL,
		input_schema  JSON NOT NULL,
		discovered_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE KEY uk_mcp_tool (server_id, name),
		CONSTRAINT fk_mcp_tool_server FOREIGN KEY (server_id) REFERENCES mcp_servers(id) ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

	// 调用记录只增不改。server_id 不建外键：服务器删除后历史记录仍要保留。
	`CREATE TABLE IF NOT EXISTS mcp_tool_calls (
		id              BIGINT AUTO_INCREMENT PRIMARY KEY,
		class_id        INT NOT NULL,
		server_id       BIGINT NULL,
		server_name     VARCHAR(32) NOT NULL,
		tool_name       VARCHAR(64) NOT NULL,
		tool_ref        VARCHAR(100) NOT NULL,
		status          ENUM('ok','denied','error') NOT NULL,
		user_id         BIGINT NOT NULL,
		conversation_id BIGINT NULL,
		round_no        INT NOT NULL DEFAULT 1,
		detail          TEXT NULL,
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		INDEX idx_mcp_call_class (class_id, created_at),
		INDEX idx_mcp_call_conv (conversation_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
}

// Migrate 建表（幂等）。
func Migrate(database *sql.DB) error {
	// 第 5 课：认证改为 JWT，服务端会话表退役。
	if _, err := database.Exec(`DROP TABLE IF EXISTS sessions`); err != nil {
		return fmt.Errorf("删除 sessions 表失败: %w", err)
	}
	for i, stmt := range schema {
		if _, err := database.Exec(stmt); err != nil {
			return fmt.Errorf("建表失败（第 %d 条）: %w", i+1, err)
		}
	}
	log.Printf("数据表已就绪（%d 张）", len(schema))
	return nil
}

type seedMaterial struct {
	classID int
	title   string
	body    string
	relPath string
}

const seedQuadratic = `# 一元二次方程讲义

## 定义

只含一个未知数，并且未知数的最高次数是 2 的整式方程，叫做一元二次方程。
一般形式写成 ax² + bx + c = 0，其中二次项系数 a 不能等于 0。

## 求根公式

当判别式 Δ = b² − 4ac 大于或等于 0 时，方程的根可以直接写出来：
x = (−b ± √Δ) / (2a)。代入时先算判别式，再算分子，最后除以 2a。

## 判别式的作用

Δ 大于 0 时，方程有两个不相等的实数根。
Δ 等于 0 时，方程有两个相等的实数根，也叫做重根。
Δ 小于 0 时，方程没有实数根，此时求根公式中的根号内为负数。

## 常见错误

第一，忘记讨论 a = 0 的情形；当 a = 0 时方程退化为一次方程，不再是二次方程。
第二，把判别式的正负与根的正负混为一谈：判别式判断的是根的个数，不是根的大小。
第三，代入求根公式时漏写分母 2a，或把 −b 的负号丢掉。

## 练习

解方程 x² − 5x + 6 = 0。这里 a = 1，b = −5，c = 6，判别式为 25 − 24 = 1，两根为 2 与 3。
解方程 x² + 2x + 5 = 0。判别式为 4 − 20 = −16 小于 0，因此没有实数根。
`

const seedPhotosynthesis = `# 光合作用讲义

## 光反应阶段

光反应在叶绿体的类囊体薄膜上进行。水在这里被分解，产生氧气并释放出电子。
光能被色素吸收后转化为活跃的化学能，储存在 ATP 与 NADPH 中。

## 暗反应阶段

暗反应在叶绿体基质中进行，不需要光直接参与，但需要光反应提供的 ATP 与 NADPH。
二氧化碳在这一阶段被固定，经过还原最终形成有机物。这一过程也叫做卡尔文循环。

## 影响因素

光照强度、二氧化碳浓度与温度都会影响光合作用的速率。
光照过强时气孔关闭，二氧化碳供应不足，速率反而下降。
温度主要通过影响酶的活性起作用，温度过高会使酶变性失活。

## 与呼吸作用的关系

光合作用合成有机物并储存能量，呼吸作用分解有机物并释放能量。
白天两者同时进行，夜间只有呼吸作用进行。
`

// Seed 在库为空时写入种子数据：两个可区分班级、各自教师与学生、两条可区分标题的材料。
// 幂等：users 非空即跳过，重复启动不会重复写入。
func Seed(database *sql.DB, cfg *config.Config) error {
	var userCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil {
		return fmt.Errorf("统计用户失败: %w", err)
	}
	if userCount > 0 {
		log.Printf("库中已有 %d 个用户，跳过种子写入", userCount)
		return nil
	}
	if cfg.SeedTeacherPassword == "" || cfg.SeedStudentPassword == "" {
		return fmt.Errorf("首次启动需要 SEED_TEACHER_PASSWORD 与 SEED_STUDENT_PASSWORD（参见 .env.example）")
	}

	if _, err := database.Exec(
		`INSERT IGNORE INTO classes (id, name) VALUES (1, '班级A'), (2, '班级B')`,
	); err != nil {
		return fmt.Errorf("写入班级失败: %w", err)
	}

	teacherHash, err := auth.HashPassword(cfg.SeedTeacherPassword)
	if err != nil {
		return err
	}
	studentHash, err := auth.HashPassword(cfg.SeedStudentPassword)
	if err != nil {
		return err
	}

	users := []struct {
		username, hash, role string
		classID              int
	}{
		{"teacher_a", teacherHash, "teacher", 1},
		{"student_a1", studentHash, "student", 1},
		{"student_b1", studentHash, "student", 2},
	}
	for _, u := range users {
		if _, err := database.Exec(
			`INSERT INTO users (username, password_hash, role, class_id) VALUES (?, ?, ?, ?)`,
			u.username, u.hash, u.role, u.classID,
		); err != nil {
			return fmt.Errorf("写入用户 %s 失败: %w", u.username, err)
		}
	}

	// 种子材料同时落盘，保证下载接口对预置数据也可验收。
	// 标题带学科前缀：第 5 课的技能按「资料」字段过滤材料标题
	//     （如数学解题引导的「资料：数学」只保留标题含「数学」的材料）。
	materials := []seedMaterial{
		{1, "A 班 · 数学一元二次方程讲义", seedQuadratic, filepath.Join("1", "seed_a_quadratic.md")},
		{2, "B 班 · 生物光合作用讲义", seedPhotosynthesis, filepath.Join("2", "seed_b_photosynthesis.md")},
	}
	for _, m := range materials {
		abs := filepath.Join(cfg.UploadDir, m.relPath)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return fmt.Errorf("创建上传子目录失败: %w", err)
		}
		if err := os.WriteFile(abs, []byte(m.body), 0o644); err != nil {
			return fmt.Errorf("写入种子材料文件失败: %w", err)
		}
		res, err := database.Exec(
			`INSERT INTO materials (title, class_id, file_path, uploader_id) VALUES (?, ?, ?, NULL)`,
			m.title, m.classID, abs,
		)
		if err != nil {
			return fmt.Errorf("写入材料失败: %w", err)
		}
		materialID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := database.Exec(
			`INSERT INTO knowledge_entries (material_id, class_id, body_text) VALUES (?, ?, ?)`,
			materialID, m.classID, m.body,
		); err != nil {
			return fmt.Errorf("写入知识库正文失败: %w", err)
		}
	}

	log.Printf("种子数据写入完成：2 个班级、3 个账号、%d 条材料", len(materials))
	return nil
}
