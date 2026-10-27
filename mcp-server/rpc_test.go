package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "test-token-123"

func doRPC(t *testing.T, token string, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	rpcHandler(testToken).ServeHTTP(rec, req)

	var parsed map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	return rec, parsed
}

func errorCode(payload map[string]any) float64 {
	errObj, _ := payload["error"].(map[string]any)
	if errObj == nil {
		return 0
	}
	code, _ := errObj["code"].(float64)
	return code
}

func TestAuthRequired(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

	rec, payload := doRPC(t, "", body)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("无令牌应 401，实际 %d", rec.Code)
	}
	if errorCode(payload) != codeUnauthorized {
		t.Errorf("无令牌应返回 -32001，实际 %v", payload["error"])
	}

	rec, _ = doRPC(t, "wrong-token", body)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("错令牌应 401，实际 %d", rec.Code)
	}

	rec, payload = doRPC(t, testToken, body)
	if rec.Code != http.StatusOK {
		t.Errorf("正确令牌应 200，实际 %d", rec.Code)
	}
	if _, ok := payload["result"]; !ok {
		t.Errorf("正确令牌应有 result，实际 %v", payload)
	}
}

func TestHealthNeedsNoToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	health(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("health 应 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Errorf("health 正文异常：%s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), testToken) {
		t.Error("health 不应泄露令牌")
	}
}

func TestInitialize(t *testing.T) {
	_, payload := doRPC(t, testToken,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"campusclaw","version":"1.0"}}}`)

	result, ok := payload["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize 应有 result，实际 %v", payload)
	}
	if result["protocolVersion"] == "" {
		t.Error("initialize 应返回 protocolVersion")
	}
	caps, _ := result["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Error("initialize 应声明 capabilities.tools")
	}
	info, _ := result["serverInfo"].(map[string]any)
	if info["name"] == "" {
		t.Error("initialize 应返回 serverInfo.name")
	}
}

func TestToolsList(t *testing.T) {
	_, payload := doRPC(t, testToken, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	result, _ := payload["result"].(map[string]any)
	list, _ := result["tools"].([]any)
	if len(list) < 2 {
		t.Fatalf("至少两个工具，实际 %d", len(list))
	}
	first, _ := list[0].(map[string]any)
	for _, key := range []string{"name", "description", "inputSchema"} {
		if first[key] == nil {
			t.Errorf("工具声明缺 %s", key)
		}
	}
}

func TestToolsCallSuccess(t *testing.T) {
	_, payload := doRPC(t, testToken,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"unit_convert","arguments":{"value":3,"from":"千米","to":"米"}}}`)
	result, _ := payload["result"].(map[string]any)
	if result["isError"] != false {
		t.Errorf("正常调用 isError 应为 false，实际 %v", result["isError"])
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("content 不应为空")
	}
	item, _ := content[0].(map[string]any)
	text, _ := item["text"].(string)
	if !strings.Contains(text, "3000") {
		t.Errorf("返回文本应含 3000，实际 %q", text)
	}
}

func TestToolsCallToolError(t *testing.T) {
	_, payload := doRPC(t, testToken,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"unit_convert","arguments":{"value":1,"from":"米","to":"千克"}}}`)
	result, _ := payload["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("不同物理量应 isError=true，实际 %v", result["isError"])
	}
}

func TestToolsCallBadArgs(t *testing.T) {
	_, payload := doRPC(t, testToken,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"formula_hint","arguments":{}}}`)
	result, _ := payload["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("缺必填参数应 isError=true，实际 %v", result["isError"])
	}
}

func TestUnknownMethodAndBadPayload(t *testing.T) {
	_, payload := doRPC(t, testToken, `{"jsonrpc":"2.0","id":6,"method":"tools/delete"}`)
	if errorCode(payload) != codeMethodNotFound {
		t.Errorf("未知方法应 -32601，实际 %v", payload["error"])
	}

	_, payload = doRPC(t, testToken, `{不是 JSON`)
	if errorCode(payload) != codeParseError {
		t.Errorf("非法 JSON 应 -32700，实际 %v", payload["error"])
	}

	_, payload = doRPC(t, testToken, `{"jsonrpc":"2.0","id":7}`)
	if errorCode(payload) != codeInvalidRequest {
		t.Errorf("缺 method 应 -32600，实际 %v", payload["error"])
	}
}
