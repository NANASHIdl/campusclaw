// Command server 是 CampusClaw 的 api 服务（Go）。
//
// 职责边界：认证、授权、班级隔离、上传校验、检索与流式问答全部在这里完成。
// 上游 Nginx 只做静态资源与 /api、/health、/swagger 的反向代理，不参与鉴权。
//
// @title           CampusClaw API
// @version         1.0
// @description     按班级隔离的知识库与解题助手。认证方式为 JWT（HS256），请求头形如 `Authorization: Bearer <token>`；角色与班级一律由令牌 sub 回查用户表得出。
// @BasePath        /
// @schemes         http
// @securityDefinitions.apikey bearerAuth
// @in              header
// @name            Authorization
// @description     登录后取得的访问令牌，填写为 `Bearer <token>`。
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	httpSwagger "github.com/swaggo/http-swagger"
	"github.com/swaggo/swag"

	_ "campusclaw/api/docs"
	"campusclaw/api/internal/agent"
	"campusclaw/api/internal/auth"
	"campusclaw/api/internal/chat"
	"campusclaw/api/internal/config"
	"campusclaw/api/internal/conversation"
	"campusclaw/api/internal/db"
	"campusclaw/api/internal/embed"
	"campusclaw/api/internal/httpx"
	"campusclaw/api/internal/index"
	"campusclaw/api/internal/jwtauth"
	"campusclaw/api/internal/materials"
	"campusclaw/api/internal/openapi"
	"campusclaw/api/internal/qa"
	"campusclaw/api/internal/retrieval"
	"campusclaw/api/internal/skill"
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

	// --- 认证：JWT 访问令牌 + 吊销表 ---
	tokens := jwtauth.New(cfg.SessionSecret, cfg.SessionTTL)
	revocations := &jwtauth.Revocations{DB: database}
	if err := revocations.Purge(); err != nil {
		log.Printf("清理过期吊销记录失败: %v", err)
	}
	authHandler := auth.NewHandler(
		&auth.UserRepo{DB: database},
		tokens,
		revocations,
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
	chatProvider := qa.New(qa.Config{
		BaseURL: cfg.ChatBaseURL,
		APIKey:  cfg.ChatAPIKey,
		Model:   cfg.ChatModel,
		Timeout: cfg.ChatTimeout,
	})
	streamer, ok := chatProvider.(qa.StreamingProvider)
	if !ok {
		log.Fatalf("对话实现不支持流式输出")
	}
	log.Printf("对话实现=%s", chatProvider.Kind())

	materialHandler := materials.NewHandler(database, cfg.UploadDir, cfg.MaxUploadBytes, indexer)
	retrievalHandler := retrieval.NewHandler(retriever)
	qaHandler := qa.NewHandler(qa.NewService(retriever, chatProvider, cfg.AskTopN))

	// --- 第 5 课：智能体、技能、会话与流式回答 ---
	agentRepo := &agent.Repo{DB: database}
	skillRepo := &skill.Repo{DB: database}
	convRepo := &conversation.Repo{DB: database}

	agentHandler := agent.NewHandler(agentRepo)
	skillHandler := skill.NewHandler(skillRepo)
	convHandler := conversation.NewHandler(convRepo, agentRepo)
	chatHandler := chat.NewHandler(&chat.Service{
		DB:        database,
		Agents:    agentRepo,
		Skills:    skillRepo,
		Convs:     convRepo,
		Retrieval: retriever,
		Chat:      streamer,
		TopN:      cfg.AskTopN,
	})

	// 预置技能与示例智能体（幂等，与用户种子解耦）。
	if err := agent.SeedFixtures(database, 1); err != nil {
		log.Printf("写入预置配置失败: %v", err)
	}

	// 为第 3 课已上传、尚无切片的材料补齐索引（采用 auto 策略）。
	if err := indexer.Backfill(context.Background()); err != nil {
		log.Printf("补齐索引失败: %v", err)
	}

	mux := http.NewServeMux()

	routes := buildRoutes(endpoints{
		auth: authHandler, materials: materialHandler, retrieval: retrievalHandler,
		qa: qaHandler, agents: agentHandler, skills: skillHandler,
		convs: convHandler, chat: chatHandler,
	})
	register(mux, routes, authHandler)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound)
	})

	// 启动自检：注解声明的操作必须与注册的路由完全一致，否则直接失败。
	doc, err := openapiDocument()
	if err != nil {
		log.Fatalf("接口文档自检失败: %v", err)
	}
	if err := checkRoutes(doc, routes); err != nil {
		log.Fatalf("接口文档自检失败: %v", err)
	}

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

// health 是存活探测，不需要认证，也不返回任何业务数据。
//
// @Summary      存活检查
// @Description  不需要认证，仅返回 ok，不包含任何材料或用户数据。
// @Tags         auth
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Router       /health [get]
// @ID           SYS-001
func health(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// serveOpenAPI 输出由注解生成的 OpenAPI 3.0 描述。
//
// @Summary      接口描述（OpenAPI 3.0）
// @Description  由处理函数上方的注解生成，经格式投影为 OpenAPI 3.0。不需要认证。
// @Tags         docs
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Failure      500 {object} httpx.ErrorBody
// @Router       /openapi.json [get]
// @ID           DOC-001
func serveOpenAPI(w http.ResponseWriter, r *http.Request) {
	doc, err := openapiDocument()
	if err != nil {
		log.Printf("读取 OpenAPI 文档失败: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(doc)
}

// openapiDocument 读取 swag 生成的描述，并投影为 OpenAPI 3.0。
func openapiDocument() ([]byte, error) {
	raw, err := swag.ReadDoc()
	if err != nil {
		return nil, fmt.Errorf("读取 swag 文档失败: %w", err)
	}
	if len(raw) < 200 {
		return nil, errors.New("OpenAPI 文档为空，请先执行 swag init")
	}
	return openapi.Convert([]byte(raw))
}

// swaggerUI 提供官方 Swagger UI。静态资源随依赖内嵌，运行期不访问外网。
//
// @Summary      接口调试页面（Swagger UI）
// @Description  官方 Swagger UI。不需要认证；页面内填入 Bearer 令牌即可试调需登录的接口。静态资源随镜像分发，运行期不依赖 CDN。
// @Tags         docs
// @Produce      html
// @Success      200 {string} string "Swagger UI 页面"
// @Failure      500 {object} httpx.ErrorBody
// @Router       /swagger/index.html [get]
// @ID           DOC-002
func swaggerUI(w http.ResponseWriter, r *http.Request) {
	httpSwagger.WrapHandler(w, r)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush 透传，保证 SSE 在包了日志中间件之后仍能逐帧刷出。
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
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
