// Package main 是 CampusClaw 的 MCP 工具服务。
//
// 职责边界：只暴露工具，不做业务判断。
//   - 传输：HTTP，单入口 POST /mcp；另有免令牌的 GET /health。
//   - 报文：JSON-RPC 2.0，实现 initialize / tools/list / tools/call 三个方法。
//   - 认证：令牌只从环境变量 MCP_AUTH_TOKEN 读取，无令牌或令牌不符一律 401。
//
// 工具只给提示或换算结果，不给最终答案；返回文本里不夹带指令式语句。
package main

import (
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	log.SetFlags(log.LstdFlags)

	token := os.Getenv("MCP_AUTH_TOKEN")
	if token == "" {
		log.Fatal("缺少环境变量 MCP_AUTH_TOKEN：工具服务拒绝在无令牌的情况下启动")
	}
	addr := ":8090"
	if p := os.Getenv("MCP_PORT"); p != "" {
		addr = ":" + p
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	mux.Handle("POST /mcp", rpcHandler(token))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("MCP 工具服务启动于 %s（工具 %d 个）", addr, len(tools))
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("服务异常退出: %v", err)
	}
}

// health 是存活探测，不需要令牌，也不返回任何令牌信息。
func health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"service": "campusclaw-mcp-demo",
		"tools":   len(tools),
	})
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
		log.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, rec.status,
			time.Since(start).Round(time.Millisecond))
	})
}
