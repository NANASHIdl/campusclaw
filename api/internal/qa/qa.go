// Package qa 实现「先检索、再生成」的问答与引用标注。
//
// 铁律：只有在取得本班命中切片之后才调用对话网关；没有依据时不调用模型，
// 直接返回固定文案，避免模型凭参数记忆勉强作答。
package qa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"campusclaw/api/internal/retrieval"
)

// NoEvidenceAnswer 是无依据时的固定文案。
const NoEvidenceAnswer = "资料中未找到相关内容"

// Message 是一轮对话消息。role 为 system 的消息一律丢弃。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Context 是交给对话模块的一条依据。
type Context struct {
	Title string
	Index int
	Text  string
}

// Provider 是对话模块。
type Provider interface {
	Kind() string
	Answer(ctx context.Context, question string, contexts []Context, history []Message) (string, error)
}

// Config 是对话模块配置。
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

// New 按配置选择实现：配置了 BaseURL 走网关，否则使用抽取式回退。
func New(cfg Config) Provider {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return &localProvider{}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &gatewayProvider{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		client:  &http.Client{Timeout: cfg.Timeout},
	}
}

// --- 网关实现 ---------------------------------------------------------------

type gatewayProvider struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

func (g *gatewayProvider) Kind() string { return "gateway" }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (g *gatewayProvider) Answer(ctx context.Context, question string, contexts []Context, history []Message) (string, error) {
	messages := []chatMessage{{
		Role:    "system",
		Content: "你是教研助手。只能依据给出的材料片段作答，回答要简短，并在句末用 [1]、[2] 标注出处编号。材料中没有依据时回答「" + NoEvidenceAnswer + "」。",
	}}
	// 历史只保留 user / assistant，客户端注入的 system 消息被丢弃。
	for _, m := range history {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		messages = append(messages, chatMessage{Role: m.Role, Content: m.Content})
	}

	var b strings.Builder
	b.WriteString("材料片段：\n")
	for i, c := range contexts {
		fmt.Fprintf(&b, "[%d] 《%s》第 %d 段：%s\n", i+1, c.Title, c.Index+1, c.Text)
	}
	fmt.Fprintf(&b, "\n问题：%s", question)
	messages = append(messages, chatMessage{Role: "user", Content: b.String()})

	body, err := json.Marshal(chatRequest{Model: g.model, Messages: messages})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if g.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+g.apiKey)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("调用对话网关失败: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("对话网关返回 %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("解析对话网关响应失败: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("对话网关错误: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("对话网关未返回任何回答")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

// --- 本地抽取式回退 ---------------------------------------------------------

type localProvider struct{}

func (l *localProvider) Kind() string { return "local" }

// Answer 从命中切片中摘取与问句最相关的一句，并标注出处。
// 这是确定性实现：不生成新内容，只做抽取，符合「有依据才作答」的约束。
func (l *localProvider) Answer(_ context.Context, question string, contexts []Context, _ []Message) (string, error) {
	if len(contexts) == 0 {
		return NoEvidenceAnswer, nil
	}
	best := ""
	bestScore := -1.0
	bestIdx := 0
	for i, c := range contexts {
		for _, s := range sentences(c.Text) {
			score := overlap(question, s)
			if score > bestScore {
				bestScore = score
				best = s
				bestIdx = i
			}
		}
	}
	if best == "" {
		best = sentences(contexts[0].Text)[0]
		bestIdx = 0
	}
	return fmt.Sprintf("根据《%s》第 %d 段：%s [%d]",
		contexts[bestIdx].Title, contexts[bestIdx].Index+1, best, bestIdx+1), nil
}

// sentences 把切片按中文句末标点与换行切成句子。
func sentences(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case '。', '！', '？', '；', '\n', '\r':
			return true
		}
		return false
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if utf8.RuneCountInString(f) >= 4 {
			out = append(out, f+"。")
		}
	}
	if len(out) == 0 {
		return []string{strings.TrimSpace(text)}
	}
	return out
}

// overlap 用字符 bigram 的 Jaccard 近似衡量问句与句子的相关度。
func overlap(question, sentence string) float64 {
	q := bigrams(question)
	s := bigrams(sentence)
	if len(q) == 0 || len(s) == 0 {
		return 0
	}
	set := make(map[string]struct{}, len(s))
	for _, g := range s {
		set[g] = struct{}{}
	}
	hit := 0
	for _, g := range q {
		if _, ok := set[g]; ok {
			hit++
		}
	}
	return float64(hit) / float64(len(q))
}

func bigrams(s string) []string {
	runes := []rune(strings.ToLower(s))
	out := make([]string, 0, len(runes))
	for i := 0; i+1 < len(runes); i++ {
		out = append(out, string(runes[i:i+2]))
	}
	if len(out) == 0 && len(runes) == 1 {
		out = append(out, string(runes))
	}
	return out
}

// --- 服务 -------------------------------------------------------------------

// Service 组装问答流程。
type Service struct {
	Retrieval *retrieval.Service
	Chat      Provider
	TopN      int
}

// NewService 构造问答服务。
func NewService(r *retrieval.Service, chat Provider, topN int) *Service {
	if topN <= 0 {
		topN = 4
	}
	return &Service{Retrieval: r, Chat: chat, TopN: topN}
}

// Answer 是问答响应。
type Answer struct {
	Status    string          `json:"status"`
	Answer    string          `json:"answer"`
	Citations []retrieval.Hit `json:"citations"`
}

// Ask 先检索再生成：有切片才调用对话模块。
func (s *Service) Ask(ctx context.Context, classID int64, question string, history []Message) (Answer, error) {
	res, err := s.Retrieval.Search(ctx, classID, question, retrieval.ModeHybrid)
	if err != nil {
		return Answer{}, err
	}

	hits := retrieval.Top(res.Hits, s.TopN)
	if len(hits) == 0 {
		// 无依据：不调用对话网关。
		return Answer{Status: "no_evidence", Answer: NoEvidenceAnswer, Citations: []retrieval.Hit{}}, nil
	}

	contexts := make([]Context, 0, len(hits))
	for _, h := range hits {
		contexts = append(contexts, Context{Title: h.MaterialTitle, Index: h.ChunkIndex, Text: h.Snippet})
	}

	answer, err := s.Chat.Answer(ctx, question, contexts, history)
	if err != nil {
		return Answer{}, err
	}
	if strings.TrimSpace(answer) == "" {
		answer = NoEvidenceAnswer
	}
	return Answer{Status: "ok", Answer: answer, Citations: hits}, nil
}

// sortHitsByScore 仅用于测试与调试：按合成分降序。
func sortHitsByScore(hits []retrieval.Hit) {
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].ScoreFinal > hits[j].ScoreFinal })
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
