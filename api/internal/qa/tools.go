package qa

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ToolProvider 在 StreamingProvider 之上支持工具声明与调用结果回传。
//
// 只有真正支持函数调用的实现才具备这个能力；本地抽取式回退不实现它，
// 于是「没有配置对话网关」时整条工具链路自动关闭。
type ToolProvider interface {
	AnswerWithTools(ctx context.Context, p Prompt, tools []ToolSpec,
		exchanges []ToolExchange, onDelta func(string) error) (StreamResult, error)
}

// ToolSpec 是发给模型的一条工具声明。三项都来自发现结果，不手写。
type ToolSpec struct {
	Name        string
	Description string
	// Schema 是该工具的 inputSchema 原文（JSON Schema）。
	Schema json.RawMessage
}

// ToolCall 是模型提出的一次调用。
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // JSON 原文，由宿主按同一份 schema 校验
}

// ToolResult 是宿主执行后回传给模型的一条工具结果。
type ToolResult struct {
	CallID  string
	Name    string
	Content string
}

// ToolExchange 是「一轮调用 + 一轮结果」，按顺序拼进消息序列。
type ToolExchange struct {
	Calls   []ToolCall
	Results []ToolResult
}

// StreamResult 是一轮流式生成的结果：正文、模型提出的调用与结束原因。
type StreamResult struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
}

// AnswerWithTools 带工具声明发起一轮流式生成。
//
// 与 AnswerPrompt 的区别：
//   - 请求体里带 tools；
//   - 会累积流式返回的 tool_calls 分片（按 index 拼接 id/name/arguments）；
//   - 忽略 reasoning_content，只把 content 当作正文。
func (g *gatewayProvider) AnswerWithTools(ctx context.Context, p Prompt,
	toolSpecs []ToolSpec, exchanges []ToolExchange,
	onDelta func(string) error) (StreamResult, error) {

	messages := make([]chatMessage, 0, len(p.History)+2+2*len(exchanges))
	if strings.TrimSpace(p.System) != "" {
		messages = append(messages, chatMessage{Role: "system", Content: p.System})
	}
	for _, m := range p.History {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		messages = append(messages, chatMessage{Role: m.Role, Content: m.Content})
	}
	messages = append(messages, chatMessage{Role: "user", Content: p.UserText})

	// 前几轮：助手提出调用 → 工具结果。工具消息落在数据位，与检索资料同一通道。
	for _, ex := range exchanges {
		calls := make([]wireToolCall, 0, len(ex.Calls))
		for _, c := range ex.Calls {
			calls = append(calls, wireToolCall{
				ID:       c.ID,
				Type:     "function",
				Function: wireFunction{Name: c.Name, Arguments: c.Arguments},
			})
		}
		messages = append(messages, chatMessage{Role: "assistant", ToolCalls: calls})
		for _, r := range ex.Results {
			messages = append(messages, chatMessage{
				Role: "tool", Content: r.Content, ToolCallID: r.CallID,
			})
		}
	}

	tools := make([]wireTool, 0, len(toolSpecs))
	for _, t := range toolSpecs {
		schema := t.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		tools = append(tools, wireTool{
			Type: "function",
			Function: wireFunctionSpec{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  schema,
			},
		})
	}

	body, err := json.Marshal(chatRequest{
		Model: g.model, Messages: messages, Stream: true, Tools: tools,
	})
	if err != nil {
		return StreamResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/chat/completions",
		bytes.NewReader(body))
	if err != nil {
		return StreamResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if g.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+g.apiKey)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return StreamResult{}, fmt.Errorf("调用对话网关失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return StreamResult{}, fmt.Errorf("对话网关返回 %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	var (
		full   strings.Builder
		acc    []ToolCall
		finish string
	)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue // 单帧解析失败不中断整条流
		}
		if chunk.Error != nil {
			return StreamResult{Content: full.String(), ToolCalls: acc},
				fmt.Errorf("对话网关错误: %s", chunk.Error.Message)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != "" {
			finish = choice.FinishReason
		}

		// tool_calls 以分片到达：按 index 累积，id/name 可能只在首帧出现。
		for _, tc := range choice.Delta.ToolCalls {
			for len(acc) <= tc.Index {
				acc = append(acc, ToolCall{})
			}
			if tc.ID != "" {
				acc[tc.Index].ID = tc.ID
			}
			acc[tc.Index].Name += tc.Function.Name
			acc[tc.Index].Arguments += tc.Function.Arguments
		}

		text := choice.Delta.Content
		if text == "" {
			continue
		}
		full.WriteString(text)
		if err := onDelta(text); err != nil {
			return StreamResult{Content: full.String(), ToolCalls: acc, FinishReason: finish}, err
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return StreamResult{Content: full.String(), ToolCalls: acc, FinishReason: finish},
			fmt.Errorf("读取对话流失败: %w", err)
	}

	// 去掉空调用（有些网关会补一个空分片）。
	cleaned := make([]ToolCall, 0, len(acc))
	for _, c := range acc {
		if c.Name != "" {
			cleaned = append(cleaned, c)
		}
	}
	return StreamResult{Content: full.String(), ToolCalls: cleaned, FinishReason: finish}, nil
}

// --- 线上结构 ---------------------------------------------------------------

type wireToolCall struct {
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

type wireFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireTool struct {
	Type     string           `json:"type"`
	Function wireFunctionSpec `json:"function"`
}

type wireFunctionSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}
