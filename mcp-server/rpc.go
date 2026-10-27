package main

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
)

// JSON-RPC 2.0 标准错误码。
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
	// codeUnauthorized 是本服务自定义的鉴权失败码，与 HTTP 401 一起返回。
	codeUnauthorized = -32001
)

// 协议版本：握手时回给客户端，与实现无关地固定一处。
const protocolVersion = "2024-11-05"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func writeError(w http.ResponseWriter, status int, id json.RawMessage, code int, message string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	writeJSON(w, status, rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}})
}

// bearerToken 取 Authorization: Bearer <token>，方案不符或含空格即视为无效。
func bearerToken(r *http.Request) string {
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if raw == "" {
		return ""
	}
	parts := strings.SplitN(raw, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	token := parts[1]
	if token == "" || strings.ContainsAny(token, " \t") {
		return ""
	}
	return token
}

// rpcHandler 是唯一的 RPC 入口：先鉴权，再解析，再分发。
func rpcHandler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := bearerToken(r)
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			// 真实原因只写日志，对外统一「未授权」。
			log.Printf("拒绝请求：令牌缺失或不匹配（来源 %s）", r.RemoteAddr)
			writeError(w, http.StatusUnauthorized, nil, codeUnauthorized, "未授权：缺少令牌或令牌不正确")
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeError(w, http.StatusOK, nil, codeParseError, "读取请求体失败")
			return
		}

		var req rpcRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusOK, nil, codeParseError, "报文不是合法的 JSON")
			return
		}
		if req.JSONRPC != "2.0" || strings.TrimSpace(req.Method) == "" {
			writeError(w, http.StatusOK, req.ID, codeInvalidRequest, "请求缺少 jsonrpc=2.0 或 method")
			return
		}

		switch req.Method {
		case "initialize":
			writeResult(w, req.ID, map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]string{"name": "campusclaw-mcp-demo", "version": "1.0"},
			})
		case "tools/list":
			writeResult(w, req.ID, map[string]any{"tools": toolDeclarations()})
		case "tools/call":
			callTool(w, req)
		case "notifications/initialized", "ping":
			// 客户端发来的通知不需要结果；按 MCP 约定忽略。
			writeResult(w, req.ID, map[string]any{})
		default:
			writeError(w, http.StatusOK, req.ID, codeMethodNotFound, "方法不存在："+req.Method)
		}
	})
}

// callTool 执行一个工具：先校验参数，再执行，最后统一包成 content 数组。
func callTool(w http.ResponseWriter, req rpcRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || strings.TrimSpace(params.Name) == "" {
		writeError(w, http.StatusOK, req.ID, codeInvalidParams, "tools/call 需要 name 与 arguments")
		return
	}

	tool, ok := tools[params.Name]
	if !ok {
		writeError(w, http.StatusOK, req.ID, codeInvalidParams, "未知工具："+params.Name)
		return
	}

	args := map[string]any{}
	if len(params.Arguments) > 0 && string(params.Arguments) != "null" {
		if err := json.Unmarshal(params.Arguments, &args); err != nil {
			writeError(w, http.StatusOK, req.ID, codeInvalidParams, "arguments 必须是 JSON 对象")
			return
		}
	}

	if msg := tool.validate(args); msg != "" {
		// 参数不合法：不执行，返回可读原因。
		writeResult(w, req.ID, map[string]any{
			"content": []map[string]string{{"type": "text", "text": "参数不合法：" + msg}},
			"isError": true,
		})
		return
	}

	text, err := tool.run(args)
	if err != nil {
		writeResult(w, req.ID, map[string]any{
			"content": []map[string]string{{"type": "text", "text": err.Error()}},
			"isError": true,
		})
		return
	}
	writeResult(w, req.ID, map[string]any{
		"content": []map[string]string{{"type": "text", "text": text}},
		"isError": false,
	})
}
