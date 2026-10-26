package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func fakeRoutes() []route {
	return []route{
		{"GET", "/health", "", authPublic, nil},
		{"POST", "/api/login", "", authPublic, nil},
		{"GET", "/swagger/", "/swagger/index.html", authPublic, nil},
		{"GET", "/api/agents/{id}", "", authUser, nil},
		{"DELETE", "/api/skills/{id}", "", authTeacher, nil},
	}
}

func docFor(t *testing.T, ops ...string) []byte {
	t.Helper()
	paths := map[string]map[string]any{}
	for _, op := range ops {
		parts := strings.SplitN(op, " ", 2)
		method, path := strings.ToLower(parts[0]), parts[1]
		if paths[path] == nil {
			paths[path] = map[string]any{}
		}
		paths[path][method] = map[string]any{"operationId": "X"}
	}
	raw := `{"paths":` + mustJSON(t, paths) + `}`
	return []byte(raw)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	return string(b)
}

func TestCheckRoutesPasses(t *testing.T) {
	doc := docFor(t,
		"GET /health", "POST /api/login", "GET /swagger/index.html",
		"GET /api/agents/{id}", "DELETE /api/skills/{id}")
	if err := checkRoutes(doc, fakeRoutes()); err != nil {
		t.Fatalf("完全一致时不应报错: %v", err)
	}
}

func TestCheckRoutesDetectsMissingAnnotation(t *testing.T) {
	// 少了 DELETE /api/skills/{id} 的注解：代码里有、注解里没有。
	doc := docFor(t,
		"GET /health", "POST /api/login", "GET /swagger/index.html",
		"GET /api/agents/{id}")
	err := checkRoutes(doc, fakeRoutes())
	if err == nil {
		t.Fatal("缺少注解时必须失败")
	}
	if !strings.Contains(err.Error(), "DELETE /api/skills/{id}") {
		t.Errorf("错误信息应指出缺失的路由，实际 %v", err)
	}
}

func TestCheckRoutesDetectsExtraAnnotation(t *testing.T) {
	// 注解里多了一条代码中不存在的路由：文档漂移。
	doc := docFor(t,
		"GET /health", "POST /api/login", "GET /swagger/index.html",
		"GET /api/agents/{id}", "DELETE /api/skills/{id}", "POST /api/ghost")
	err := checkRoutes(doc, fakeRoutes())
	if err == nil {
		t.Fatal("注解多余时必须失败")
	}
	if !strings.Contains(err.Error(), "POST /api/ghost") {
		t.Errorf("错误信息应指出多余的路由，实际 %v", err)
	}
}

func TestCheckRoutesUsesDocPath(t *testing.T) {
	// /swagger/ 注册为目录，文档写的是 /swagger/index.html，自检必须按文档路径比对。
	doc := docFor(t,
		"GET /health", "POST /api/login", "GET /swagger/index.html",
		"GET /api/agents/{id}", "DELETE /api/skills/{id}")
	if err := checkRoutes(doc, fakeRoutes()); err != nil {
		t.Fatalf("docPath 未被采用: %v", err)
	}
}

func TestCheckRoutesRejectsBrokenDoc(t *testing.T) {
	if err := checkRoutes([]byte("not json"), fakeRoutes()); err == nil {
		t.Error("无法解析的文档应报错")
	}
}
