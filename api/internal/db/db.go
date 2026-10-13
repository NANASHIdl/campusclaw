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

// schema 是五张表：classes、users、sessions、materials、knowledge_entries。
// materials 与 knowledge_entries 携带非空且带索引的租户列 class_id。
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

	`CREATE TABLE IF NOT EXISTS sessions (
		id         CHAR(64) PRIMARY KEY,
		user_id    BIGINT NOT NULL,
		expires_at DATETIME NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		CONSTRAINT fk_sessions_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
		INDEX idx_sessions_user (user_id),
		INDEX idx_sessions_expires (expires_at)
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
}

// Migrate 建表（幂等）。
func Migrate(database *sql.DB) error {
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
	materials := []seedMaterial{
		{1, "A 班 · 一元二次方程讲义", "本讲义覆盖一元二次方程的定义、求根公式与判别式。", filepath.Join("1", "seed_a_quadratic.md")},
		{2, "B 班 · 光合作用讲义", "本讲义覆盖光合作用的光反应与暗反应阶段。", filepath.Join("2", "seed_b_photosynthesis.md")},
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
