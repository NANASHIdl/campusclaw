package qa

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// sseServer 起一个假的对话网关，按给定帧回放。
func sseServer(t *testing.T, frames []string, onRequest func(map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("请求体不是 JSON: %v", err)
		}
		if onRequest != nil {
			onRequest(body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func toolProvider(t *testing.T, url string) ToolProvider {
	t.Helper()
	p := New(Config{BaseURL: url, Model: "m", Timeout: 5 * time.Second})
	tp, ok := p.(ToolProvider)
	if !ok {
		t.Fatal("网关实现应支持工具调用")
	}
	return tp
}

func TestAnswerWithToolsAccumulatesFragments(t *testing.T) {
	frames := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","function":{"name":"formula_hint","arguments":"{\"top"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ic\":\"求根公式\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	var gotTools any
	srv := sseServer(t, frames, func(body map[string]any) {
		gotTools = body["tools"]
	})
	defer srv.Close()

	res, err := toolProvider(t, srv.URL).AnswerWithTools(context.Background(),
		Prompt{UserText: "求根公式是什么"},
		[]ToolSpec{{Name: "formula_hint", Description: "提示", Schema: json.RawMessage(`{"type":"object"}`)}},
		nil, func(string) error { return nil })
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}

	if gotTools == nil {
		t.Error("请求体应带 tools 声明")
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("应累积出 1 个调用，实际 %d", len(res.ToolCalls))
	}
	call := res.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "formula_hint" {
		t.Errorf("id/name 累积不对：%+v", call)
	}
	if call.Arguments != `{"topic":"求根公式"}` {
		t.Errorf("arguments 分片拼接不对：%q", call.Arguments)
	}
	if res.FinishReason != "tool_calls" {
		t.Errorf("finish_reason 应为 tool_calls，实际 %q", res.FinishReason)
	}
}

func TestAnswerWithToolsStreamsTextAndIgnoresReasoning(t *testing.T) {
	frames := []string{
		`{"choices":[{"delta":{"reasoning_content":"想一想"}}]}`,
		`{"choices":[{"delta":{"content":"先看"}}]}`,
		`{"choices":[{"delta":{"content":"括号"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	}
	srv := sseServer(t, frames, nil)
	defer srv.Close()

	var deltas []string
	res, err := toolProvider(t, srv.URL).AnswerWithTools(context.Background(),
		Prompt{UserText: "去括号"}, nil, nil, func(s string) error {
			deltas = append(deltas, s)
			return nil
		})
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if res.Content != "先看括号" {
		t.Errorf("正文应只含 content，实际 %q", res.Content)
	}
	if len(deltas) != 2 {
		t.Errorf("应有 2 段正文回调，实际 %d", len(deltas))
	}
	if len(res.ToolCalls) != 0 {
		t.Errorf("没有调用时应为空，实际 %+v", res.ToolCalls)
	}
}

func TestAnswerWithToolsSendsExchanges(t *testing.T) {
	frames := []string{`{"choices":[{"delta":{"content":"好"},"finish_reason":"stop"}]}`}
	var messages []any
	srv := sseServer(t, frames, func(body map[string]any) {
		messages, _ = body["messages"].([]any)
	})
	defer srv.Close()

	_, err := toolProvider(t, srv.URL).AnswerWithTools(context.Background(),
		Prompt{UserText: "继续"},
		[]ToolSpec{{Name: "formula_hint"}},
		[]ToolExchange{{
			Calls:   []ToolCall{{ID: "call_1", Name: "formula_hint", Arguments: `{"topic":"勾股"}`}},
			Results: []ToolResult{{CallID: "call_1", Name: "formula_hint", Content: "公式：a²+b²=c²"}},
		}}, func(string) error { return nil })
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}

	// user(本轮) + assistant(tool_calls) + tool —— 没有 system 与历史时共 3 条
	if len(messages) < 3 {
		t.Fatalf("消息序列应含工具消息，实际 %d 条", len(messages))
	}
	var hasTool, hasAssistantCall bool
	for _, m := range messages {
		msg, _ := m.(map[string]any)
		if msg["role"] == "tool" && msg["tool_call_id"] == "call_1" {
			hasTool = true
		}
		if msg["role"] == "assistant" && msg["tool_calls"] != nil {
			hasAssistantCall = true
		}
	}
	if !hasTool {
		t.Error("应有一条 tool 消息回传结果")
	}
	if !hasAssistantCall {
		t.Error("应有一条带 tool_calls 的 assistant 消息")
	}
}
