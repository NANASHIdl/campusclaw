// Command server 是 CampusClaw 的 api 服务（Go）。
//
// 职责边界：认证、授权、班级隔离与上传校验全部在这里完成。
// 上游 Nginx 只做静态资源与 /api、/health 的反向代理，不参与鉴权。
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"campusclaw/api/internal/auth"
	"campusclaw/api/internal/config"
	"campusclaw/api/internal/db"
	"campusclaw/api/internal/embed"
	"campusclaw/api/internal/httpx"
	"campusclaw/api/internal/index"
	"campusclaw/api/internal/materials"
	"campusclaw/api/internal/qa"
	"campusclaw/api/internal/retrieval"
	"campusclaw/api/internal/vector"
)

func main() {
	log.SetFlags(log.LstdFlags)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("配置错误: %v", err)
	}
	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil {
		log.Fatalf("创建上传目录失败: %v", err)
	}

	database, err := db.Connect(cfg)
	if err != nil {
		log.Fatalf("数据库连接失败: %v", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		log.Fatalf("建表失败: %v", err)
	}
	if err := db.Seed(database, cfg); err != nil {
		log.Fatalf("写入种子数据失败: %v", err)
	}

	sessions := &auth.SessionRepo{DB: database, Secret: cfg.SessionSecret, TTL: cfg.SessionTTL}
	if err := sessions.PurgeExpired(); err != nil {
		log.Printf("清理过期会话失败: %v", err)
	}
	authHandler := auth.NewHandler(
		&auth.UserRepo{DB: database},
		sessions,
		auth.NewLimiter(cfg.LoginRateLimit, cfg.LoginRateWindow),
	)

	// --- 第 4 课：嵌入、向量库、检索与问答 ---
	embedder := embed.New(embed.Config{
		BaseURL:   cfg.EmbeddingBaseURL,
		APIKey:    cfg.EmbeddingAPIKey,
		Model:     cfg.EmbeddingModel,
		Dim:       cfg.VectorDim,
		LocalBias: cfg.LocalEmbedBias,
	})
	qdrant := vector.New(cfg.QdrantURL, cfg.QdrantCollection, cfg.VectorDim, 15*time.Second)
	log.Printf("嵌入实现=%s（%s） 向量库=%s 集合=%s",
		embedder.Kind(), embedSource(cfg), cfg.QdrantURL, cfg.QdrantCollection)

	if err := qdrant.EnsureCollection(context.Background()); err != nil {
		log.Printf("向量库暂不可用，将在启动后重试: %v", err)
	}

	indexer := index.New(database, embedder, qdrant)
	retriever := retrieval.New(database, embedder, qdrant, cfg.VectorThreshold, cfg.RRFK, cfg.SearchLimit)
	chat := qa.New(qa.Config{
		BaseURL: cfg.ChatBaseURL,
		APIKey:  cfg.ChatAPIKey,
		Model:   cfg.ChatModel,
	})
	log.Printf("对话实现=%s", chat.Kind())

	materialHandler := materials.NewHandler(database, cfg.UploadDir, cfg.MaxUploadBytes, indexer)
	retrievalHandler := retrieval.NewHandler(retriever)
	qaHandler := qa.NewHandler(qa.NewService(retriever, chat, cfg.AskTopN))

	// 为第 3 课已上传、尚无切片的材料补齐索引（采用 auto 策略）。
	if err := indexer.Backfill(context.Background()); err != nil {
		log.Printf("补齐索引失败: %v", err)
	}

	mux := http.NewServeMux()
	// /health 不要求认证，且不返回任何材料内容。
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/login", authHandler.Login)
	mux.HandleFunc("POST /api/logout", authHandler.Logout)

	// 受保护接口一律先经过认证中间件。
	mux.Handle("GET /api/me", authHandler.RequireAuth(http.HandlerFunc(authHandler.Me)))
	mux.Handle("GET /api/materials", authHandler.RequireAuth(http.HandlerFunc(materialHandler.List)))
	mux.Handle("GET /api/materials/{id}", authHandler.RequireAuth(http.HandlerFunc(materialHandler.Detail)))
	mux.Handle("GET /api/materials/{id}/file", authHandler.RequireAuth(http.HandlerFunc(materialHandler.Download)))
	mux.Handle("POST /api/materials/upload", authHandler.RequireAuth(http.HandlerFunc(materialHandler.Upload)))
	mux.Handle("GET /api/materials/{id}/vectors", authHandler.RequireAuth(http.HandlerFunc(materialHandler.Vectors)))
	mux.Handle("POST /api/materials/{id}/reindex", authHandler.RequireAuth(http.HandlerFunc(materialHandler.Reindex)))
	mux.Handle("POST /api/search", authHandler.RequireAuth(http.HandlerFunc(retrievalHandler.Search)))
	mux.Handle("POST /api/ask", authHandler.RequireAuth(http.HandlerFunc(qaHandler.Ask)))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound)
	})

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("api 服务启动于 %s（上传目录 %s）", cfg.Addr, cfg.UploadDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP 服务异常退出: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("优雅关闭失败: %v", err)
	}
	log.Print("api 服务已停止")
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		log.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
	})
}

// embedSource 说明嵌入向量的来源，便于在日志中区分「课程网关」与「本地回退」。
func embedSource(cfg *config.Config) string {
	if cfg.EmbeddingBaseURL != "" {
		return "网关 " + cfg.EmbeddingBaseURL
	}
	return "本地确定性回退（未配置 EMBEDDING_BASE_URL）"
}
