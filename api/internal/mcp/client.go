package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client 是 JSON-RPC 2.0 的最小客户端，只实现本课需要的三个方法。
type Client struct {
	http    *http.Client
	timeout time.Duration
}

func NewClient(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &Client{
		http:    &http.Client{Timeout: timeout},
		timeout: timeout,
	}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// roundTrip 发一次 JSON-RPC 请求并返回 result。
func (c *Client) roundTrip(ctx context.Context, baseURL, token, method string, params any) (json.RawMessage, error) {
	endpoint := strings.TrimRight(baseURL, "/") + "/mcp"
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("拼装请求失败：%w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法连接工具服务（地址或网络不通）：%w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errors.New("令牌不正确（工具服务返回 401）")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("工具服务返回 HTTP %d：%s", resp.StatusCode, truncate(string(raw), 120))
	}

	var env rpcEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("工具服务返回的不是 JSON-RPC 报文：%s", truncate(string(raw), 120))
	}
	if env.Error != nil {
		return nil, fmt.Errorf("工具服务报错（%d）：%s", env.Error.Code, env.Error.Message)
	}
	return env.Result, nil
}

// Initialize 完成握手：确认双方能用同一套协议，并声明 tools 能力。
func (c *Client) Initialize(ctx context.Context, baseURL, token string) error {
	result, err := c.roundTrip(ctx, baseURL, token, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "campusclaw", "version": "1.0"},
	})
	if err != nil {
		return err
	}
	var parsed struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		return fmt.Errorf("握手响应无法解析：%w", err)
	}
	if parsed.ProtocolVersion == "" {
		return errors.New("握手响应缺少 protocolVersion")
	}
	return nil
}

// ListTools 取回工具清单；工具名不合规的条目直接丢弃，不让它进入候选集。
func (c *Client) ListTools(ctx context.Context, baseURL, token string) ([]Tool, error) {
	result, err := c.roundTrip(ctx, baseURL, token, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	// 线上字段是 MCP 规范的 inputSchema（驼峰），与库里的 input_schema 不是一回事。
	var parsed struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		return nil, fmt.Errorf("工具清单无法解析：%w", err)
	}

	out := make([]Tool, 0, len(parsed.Tools))
	for _, t := range parsed.Tools {
		if !ValidToolName(t.Name) {
			continue
		}
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, Tool{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	return out, nil
}

// CallTool 执行工具，返回文本与「工具是否自报错误」。
func (c *Client) CallTool(ctx context.Context, baseURL, token, name string, args json.RawMessage) (string, bool, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	result, err := c.roundTrip(ctx, baseURL, token, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return "", true, err
	}

	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		return "", true, fmt.Errorf("工具返回无法解析：%w", err)
	}

	var sb strings.Builder
	for _, item := range parsed.Content {
		if item.Type == "text" && item.Text != "" {
			sb.WriteString(item.Text)
			sb.WriteString("\n")
		}
	}
	text := strings.TrimSpace(sb.String())
	if text == "" {
		return "（工具没有返回文本内容）", parsed.IsError, nil
	}
	return text, parsed.IsError, nil
}

func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
