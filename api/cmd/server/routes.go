package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"campusclaw/api/internal/agent"
	"campusclaw/api/internal/auth"
	"campusclaw/api/internal/chat"
	"campusclaw/api/internal/conversation"
	"campusclaw/api/internal/materials"
	"campusclaw/api/internal/mcp"
	"campusclaw/api/internal/qa"
	"campusclaw/api/internal/retrieval"
	"campusclaw/api/internal/skill"
)

// authLevel 是一条路由的鉴权级别。
type authLevel int

const (
	authPublic  authLevel = iota // 白名单：不需要令牌
	authUser                     // 登录即可
	authTeacher                  // 仅教师
)

// route 是一条实际注册的路由。
//
// pattern 用于 mux 注册；docPath 是它在 OpenAPI 描述里的路径（为空时与 pattern 相同）。
// 两者分开是因为 `/swagger/` 必须以目录形式注册才能取到静态资源，
// 而文档描述的是具体的 `/swagger/index.html`。
type route struct {
	method  string
	pattern string
	docPath string
	auth    authLevel
	handler http.HandlerFunc
}

func (r route) path() string {
	if r.docPath != "" {
		return r.docPath
	}
	return r.pattern
}

// endpoints 汇总各业务包的处理函数，供路由表引用。
type endpoints struct {
	auth      *auth.Handler
	materials *materials.Handler
	retrieval *retrieval.Handler
	qa        *qa.Handler
	agents    *agent.Handler
	skills    *skill.Handler
	convs     *conversation.Handler
	chat      *chat.Handler
	mcp       *mcp.Handler
}

// buildRoutes 返回本项目全部路由。
//
// 注册与「注解一致性自检」都读这一份，避免两处各写一遍而漂移。
func buildRoutes(e endpoints) []route {
	return []route{
		// 白名单
		{"GET", "/health", "", authPublic, health},
		{"POST", "/api/login", "", authPublic, e.auth.Login},
		{"GET", "/openapi.json", "", authPublic, serveOpenAPI},
		{"GET", "/swagger/", "/swagger/index.html", authPublic, swaggerUI},

		// 登录即可
		{"POST", "/api/logout", "", authUser, e.auth.Logout},
		{"GET", "/api/me", "", authUser, e.auth.Me},

		{"GET", "/api/materials", "", authUser, e.materials.List},
		{"GET", "/api/materials/{id}", "", authUser, e.materials.Detail},
		{"GET", "/api/materials/{id}/file", "", authUser, e.materials.Download},
		{"POST", "/api/materials/upload", "", authUser, e.materials.Upload},
		{"GET", "/api/materials/{id}/vectors", "", authUser, e.materials.Vectors},
		{"POST", "/api/materials/{id}/reindex", "", authUser, e.materials.Reindex},

		{"POST", "/api/search", "", authUser, e.retrieval.Search},
		{"POST", "/api/ask", "", authUser, e.qa.Ask},

		{"GET", "/api/agents", "", authUser, e.agents.List},
		{"GET", "/api/agents/{id}", "", authUser, e.agents.Get},
		{"GET", "/api/skills", "", authUser, e.skills.List},
		{"GET", "/api/skills/{id}", "", authUser, e.skills.Get},

		{"GET", "/api/conversations", "", authUser, e.convs.List},
		{"POST", "/api/conversations", "", authUser, e.convs.Create},
		{"GET", "/api/conversations/{id}", "", authUser, e.convs.Get},
		{"POST", "/api/conversations/{id}/messages", "", authUser, e.chat.SendMessage},

		// 教师专属
		{"POST", "/api/agents", "", authTeacher, e.agents.Create},
		{"PUT", "/api/agents/{id}", "", authTeacher, e.agents.Update},
		{"POST", "/api/skills", "", authTeacher, e.skills.Create},
		{"PUT", "/api/skills/{id}", "", authTeacher, e.skills.Update},
		{"DELETE", "/api/skills/{id}", "", authTeacher, e.skills.Delete},

		// 第 6 课：MCP 工具接入（全部仅教师）
		{"GET", "/api/mcp/servers", "", authTeacher, e.mcp.List},
		{"POST", "/api/mcp/servers", "", authTeacher, e.mcp.Create},
		{"PUT", "/api/mcp/servers/{id}", "", authTeacher, e.mcp.Update},
		{"DELETE", "/api/mcp/servers/{id}", "", authTeacher, e.mcp.Delete},
		{"POST", "/api/mcp/servers/{id}/connect", "", authTeacher, e.mcp.Connect},
		{"POST", "/api/mcp/servers/{id}/disconnect", "", authTeacher, e.mcp.Disconnect},
		{"GET", "/api/mcp/servers/{id}/tools", "", authTeacher, e.mcp.Tools},
		{"GET", "/api/mcp/tool-calls", "", authTeacher, e.mcp.Calls},
	}
}

// register 按路由表注册到 mux，并按鉴权级别套上中间件。
func register(mux *http.ServeMux, routes []route, authHandler *auth.Handler) {
	for _, rt := range routes {
		var h http.Handler = rt.handler
		switch rt.auth {
		case authUser:
			h = authHandler.RequireAuth(rt.handler)
		case authTeacher:
			h = authHandler.RequireTeacher(rt.handler)
		}
		mux.Handle(rt.method+" "+rt.pattern, h)
	}
}

// checkRoutes 比对「注解声明的操作集合」与「实际注册的路由集合」。
//
// 存在只在代码里、或只在注解里的路由时启动必须失败：
// 宁可起不来，也不要对外提供一份缺条目的文档。
func checkRoutes(doc []byte, routes []route) error {
	var parsed struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		return fmt.Errorf("解析 OpenAPI 描述失败: %w", err)
	}

	annotated := make(map[string]bool, len(parsed.Paths))
	for path, ops := range parsed.Paths {
		for method := range ops {
			annotated[strings.ToLower(method)+" "+path] = true
		}
	}
	registered := make(map[string]bool, len(routes))
	for _, rt := range routes {
		registered[strings.ToLower(rt.method)+" "+rt.path()] = true
	}

	var missing, extra []string
	for k := range registered {
		if !annotated[k] {
			missing = append(missing, displayRoute(k))
		}
	}
	for k := range annotated {
		if !registered[k] {
			extra = append(extra, displayRoute(k))
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		return fmt.Errorf("注解与注册路由不一致：代码有但注解缺 %v；注解有但代码无 %v", missing, extra)
	}
	return nil
}

// displayRoute 把内部比对用的「小写方法 + 路径」还原成便于阅读的形式。
func displayRoute(key string) string {
	parts := strings.SplitN(key, " ", 2)
	if len(parts) != 2 {
		return key
	}
	return strings.ToUpper(parts[0]) + " " + parts[1]
}
