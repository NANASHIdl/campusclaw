// Package config 从环境变量读取配置。
//
// 必需变量缺失时 Load 返回错误，服务启动失败——不使用不安全的默认值。
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr string

	DBUser     string
	DBPassword string
	DBHost     string
	DBPort     string
	DBName     string

	// SessionSecret 用作会话标识摘要的 HMAC 密钥（pepper）。
	SessionSecret string
	SessionTTL    time.Duration

	UploadDir      string
	MaxUploadBytes int64

	// 种子口令明文，仅启动时用于计算哈希。
	SeedTeacherPassword string
	SeedStudentPassword string

	LoginRateLimit  int
	LoginRateWindow time.Duration
}

func Load() (*Config, error) {
	var missing []string
	required := func(key string) string {
		v := strings.TrimSpace(os.Getenv(key))
		if v == "" {
			missing = append(missing, key)
		}
		return v
	}

	cfg := &Config{
		Addr:           getenv("API_ADDR", ":8080"),
		DBUser:         required("DB_USER"),
		DBPassword:     required("DB_PASSWORD"),
		DBHost:         getenv("DB_HOST", "db"),
		DBPort:         getenv("DB_PORT", "3306"),
		DBName:         required("DB_NAME"),
		SessionSecret:  required("SESSION_SECRET"),
		UploadDir:      getenv("UPLOAD_DIR", "/app/uploads"),
		MaxUploadBytes: getenvInt64("MAX_UPLOAD_BYTES", 5<<20),
		LoginRateLimit: int(getenvInt64("LOGIN_RATE_LIMIT", 10)),
	}
	cfg.SessionTTL = time.Duration(getenvInt64("SESSION_TTL_MINUTES", 720)) * time.Minute
	cfg.LoginRateWindow = time.Duration(getenvInt64("LOGIN_RATE_WINDOW_SECONDS", 60)) * time.Second

	// 种子口令允许缺省（例如库已初始化时），但一旦提供就用于种子写入。
	cfg.SeedTeacherPassword = getenv("SEED_TEACHER_PASSWORD", "")
	cfg.SeedStudentPassword = getenv("SEED_STUDENT_PASSWORD", "")

	if len(missing) > 0 {
		return nil, fmt.Errorf("缺少必需环境变量: %s（参见 .env.example）", strings.Join(missing, ", "))
	}
	if cfg.SessionSecret == "" {
		return nil, fmt.Errorf("SESSION_SECRET 不能为空")
	}
	return cfg, nil
}

// DSN 返回 go-sql-driver/mysql 的连接串。
func (c *Config) DSN() string {
	return fmt.Sprintf(
		"%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4&collation=utf8mb4_unicode_ci&loc=Local&timeout=5s",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName,
	)
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func getenvInt64(key string, def int64) int64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}
