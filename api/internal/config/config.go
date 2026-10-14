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

	// --- 第 4 课：向量库与模型网关 ---
	//
	// QdrantURL 为容器网络内的向量库地址；集合名与维度需与嵌入网关输出一致。
	QdrantURL        string
	QdrantCollection string
	VectorDim        int

	// 嵌入网关与对话网关均可选：未配置 BaseURL 时使用本地确定性回退，
	// 使整条检索链路在无外部凭据时仍可离线运行与验收。
	EmbeddingBaseURL string
	EmbeddingAPIKey  string
	EmbeddingModel   string
	// LocalEmbedBias 是本地回退嵌入的背景分量权重；仅在不配置网关时生效。
	LocalEmbedBias float64
	ChatBaseURL    string
	ChatAPIKey     string
	ChatModel      string
	// ChatTimeout 是单次对话请求的超时；推理型模型耗时较长，默认 60s。
	ChatTimeout time.Duration

	// 检索参数：余弦阈值、RRF 常数、返回条数、问答取前几条切片。
	VectorThreshold float64
	RRFK            int
	SearchLimit     int
	AskTopN         int
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

	cfg.QdrantURL = getenv("QDRANT_URL", "http://qdrant:6333")
	cfg.QdrantCollection = getenv("QDRANT_COLLECTION", "campusclaw_chunks")
	// 向量维度：VECTOR_DIM 优先，其次兼容嵌入网关惯用的 EMBEDDING_DIM。
	cfg.VectorDim = int(getenvInt64("VECTOR_DIM", getenvInt64("EMBEDDING_DIM", 2048)))

	cfg.EmbeddingBaseURL = getenv("EMBEDDING_BASE_URL", "")
	cfg.EmbeddingAPIKey = getenv("EMBEDDING_API_KEY", "")
	cfg.EmbeddingModel = getenv("EMBEDDING_MODEL", "text-embedding-3-small")
	cfg.LocalEmbedBias = getenvFloat("LOCAL_EMBED_BIAS", 0.60)
	cfg.ChatBaseURL = getenv("CHAT_BASE_URL", "")
	cfg.ChatAPIKey = getenv("CHAT_API_KEY", "")
	cfg.ChatModel = getenv("CHAT_MODEL", "gpt-4o-mini")
	cfg.ChatTimeout = time.Duration(getenvInt64("CHAT_TIMEOUT_SECONDS", 60)) * time.Second

	cfg.VectorThreshold = getenvFloat("VECTOR_THRESHOLD", 0.35)
	cfg.RRFK = int(getenvInt64("RRF_K", 60))
	cfg.SearchLimit = int(getenvInt64("SEARCH_LIMIT", 10))
	cfg.AskTopN = int(getenvInt64("ASK_TOP_N", 4))

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

func getenvFloat(key string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}
