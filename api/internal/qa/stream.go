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

// Prompt 是一次流式调用的完整输入。
//
// 系统消息由调用方（chat 包）按五段顺序组装好——角色、技能、有无资料、
// 不得暴露模型、边界句；这里只负责把它连同历史与用户消息发出去。
type Prompt struct {
	System        string
	UserText      string
	History       []Message
	WithCitations bool
}

// StreamingProvider 在 Provider 之上增加逐段输出：每产生一段就回调 onDelta，
// 返回值仍是完整回答（用于落库）。
type StreamingProvider interface {
	Provider
	AnswerPrompt(ctx context.Context, p Prompt, onDelta func(string) error) (string, error)
}

var (
	_ StreamingProvider = (*gatewayProvider)(nil)
	_ StreamingProvider = (*localProvider)(nil)
)

// AnswerPrompt 以 stream=true 调用对话网关，逐段转发增量文本。
func (g *gatewayProvider) AnswerPrompt(ctx context.Context, p Prompt, onDelta func(string) error) (string, error) {
	messages := make([]chatMessage, 0, len(p.History)+2)
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

	body, err := json.Marshal(chatRequest{Model: g.model, Messages: messages, Stream: true})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if g.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+g.apiKey)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("调用对话网关失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("对话网关返回 %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	var full strings.Builder
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
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			// 单帧解析失败不中断整条流。
			continue
		}
		if chunk.Error != nil {
			return full.String(), fmt.Errorf("对话网关错误: %s", chunk.Error.Message)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		text := chunk.Choices[0].Delta.Content
		if text == "" {
			continue
		}
		full.WriteString(text)
		if err := onDelta(text); err != nil {
			return full.String(), err
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return full.String(), fmt.Errorf("读取对话流失败: %w", err)
	}
	return full.String(), nil
}

// AnswerPrompt 是抽取式回退的流式版本：从用户消息里的资料摘一句，再按片段回调。
//
// 没有资料时不加编号——与系统消息里的约束保持一致。
func (l *localProvider) AnswerPrompt(ctx context.Context, p Prompt, onDelta func(string) error) (string, error) {
	answer := extractFrom(p.UserText, p.WithCitations)
	const chunkRunes = 12
	runes := []rune(answer)
	for i := 0; i < len(runes); i += chunkRunes {
		end := i + chunkRunes
		if end > len(runes) {
			end = len(runes)
		}
		if err := onDelta(string(runes[i:end])); err != nil {
			return answer, err
		}
	}
	return answer, nil
}

// extractFrom 在给定文本里挑与最后一行（提问）最相关的一句。
func extractFrom(text string, withCitation bool) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	question := ""
	if len(lines) > 0 {
		question = lines[len(lines)-1]
	}
	best, bestScore := "", -1.0
	for _, s := range sentences(text) {
		if score := overlap(question, s); score > bestScore {
			best, bestScore = s, score
		}
	}
	if strings.TrimSpace(best) == "" {
		best = "这个问题我暂时没有找到可以直接引用的材料内容。"
	}
	if withCitation {
		return best + " [1]"
	}
	return best
}
