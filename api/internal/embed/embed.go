// Package embed 把文本转换为固定维度的向量。
//
// 两类实现：
//   - gateway：调用服务端配置的嵌入网关（兼容 OpenAI /embeddings 接口）。
//   - local：未配置网关时使用的确定性回退——字符 n-gram 特征哈希。
//
// 本地回退是「词面近似」，不具备语义泛化能力；它的存在意义是让整条链路
// 在无外部凭据时仍可离线运行与验收。生产环境应配置嵌入网关。
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// Dim 是课件规定的向量维度，与 Qdrant 集合保持一致。
const Dim = 2048

// Config 是嵌入模块的配置。
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	Dim     int
	Timeout time.Duration
	// LocalBias 是本地回退的「背景分量」权重 α（默认 0.67）。
	// 真实嵌入模型对任意两段文本都有一定的背景相似度；纯词面的稀疏向量没有，
	// 于是长文本与短问句的余弦会低到 0.05 量级，无法与「不相关」区分。
	// 加入固定的正交背景分量后：cos = (α² + ρ) / (α² + 1)，
	// 其中 ρ 为词面重叠。这样「相关」与「不相关」才落在阈值两侧。
	LocalBias float64
}

// DefaultLocalBias 是本地回退的默认背景分量权重（经标定：相关问句余弦约 0.45、
// 不相关问句约 0.28，能干净地落在默认阈值 0.35 两侧）。
const DefaultLocalBias = 0.60

// unigramWeight 降低单字权重：中文单字噪声大，双字更能代表实际匹配。
const unigramWeight = 0.5

// Provider 是嵌入提供方。
type Provider interface {
	// Dim 返回向量维度。
	Dim() int
	// Kind 返回实现种类：gateway 或 local。
	Kind() string
	// Embed 把若干文本各转换为一条向量。
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// New 按配置选择实现：配置了 BaseURL 走网关，否则使用本地确定性回退。
func New(cfg Config) Provider {
	if cfg.Dim <= 0 {
		cfg.Dim = Dim
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 20 * time.Second
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		bias := cfg.LocalBias
		if bias <= 0 {
			bias = DefaultLocalBias
		}
		return &localProvider{dim: cfg.Dim, bias: bias}
	}
	return &gatewayProvider{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		dim:     cfg.Dim,
		client:  &http.Client{Timeout: cfg.Timeout},
	}
}

// --- 网关实现 ---------------------------------------------------------------

type gatewayProvider struct {
	baseURL string
	apiKey  string
	model   string
	dim     int
	client  *http.Client
}

func (g *gatewayProvider) Dim() int     { return g.dim }
func (g *gatewayProvider) Kind() string { return "gateway" }

type embeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (g *gatewayProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(embeddingRequest{Model: g.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("序列化嵌入请求失败: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if g.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+g.apiKey)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("调用嵌入网关失败: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("读取嵌入网关响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("嵌入网关返回 %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	var parsed embeddingResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("解析嵌入网关响应失败: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("嵌入网关错误: %s", parsed.Error.Message)
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("嵌入网关返回 %d 条向量，期望 %d 条", len(parsed.Data), len(texts))
	}

	out := make([][]float32, len(texts))
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("嵌入网关返回越界下标 %d", d.Index)
		}
		if g.dim > 0 && len(d.Embedding) != g.dim {
			return nil, fmt.Errorf("嵌入维度 %d 与集合维度 %d 不一致", len(d.Embedding), g.dim)
		}
		out[d.Index] = normalize(d.Embedding)
	}
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("嵌入网关未返回第 %d 条向量", i)
		}
	}
	return out, nil
}

// --- 本地确定性回退 ---------------------------------------------------------

type localProvider struct {
	dim  int
	bias float64
}

func (l *localProvider) Dim() int     { return l.dim }
func (l *localProvider) Kind() string { return "local" }

func (l *localProvider) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = l.embedOne(t)
	}
	return out, nil
}

// embedOne 用特征哈希把文本映射为定长向量：
// 每个 token 经 FNV-1a 哈希落到某一维，符号由哈希高位决定，最后做 L2 归一化。
// 同一文本两次调用结果逐位相同——这正是「确定性」的含义。
//
// 维度 0 保留给背景分量 α，token 落在 1..dim-1：
//
//	v = [α, ŝ] / √(α²+1)，其中 ŝ 是 token 部分的单位向量
//	于是 cos(v_q, v_c) = (α² + ρ) / (α² + 1)，ρ 为词面重叠
func (l *localProvider) embedOne(text string) []float32 {
	vec := make([]float32, l.dim)
	span := l.dim - 1
	if span <= 0 {
		span = l.dim
	}

	for _, tok := range tokenize(text) {
		h := fnv.New64a()
		_, _ = h.Write([]byte(tok))
		sum := h.Sum64()
		idx := int(sum % uint64(span))
		if l.dim > 1 {
			idx++
		}
		w := float32(1)
		if len([]rune(tok)) == 1 {
			w = unigramWeight
		}
		if sum&(1<<63) != 0 {
			vec[idx] -= w
		} else {
			vec[idx] += w
		}
	}

	// 先把 token 部分归一化，再叠加背景分量。
	normalizeInPlace(vec[1:])
	vec[0] = float32(l.bias)
	return normalize(vec)
}

// tokenize 生成检索用 token：
//   - 中日韩统一表意文字：单字 + 相邻双字（bigram）
//   - 拉丁字母与数字：按连续串成词
func tokenize(s string) []string {
	s = strings.ToLower(s)
	runes := []rune(s)
	toks := make([]string, 0, len(runes))

	var cjk []rune
	var word []rune

	flushWord := func() {
		if len(word) > 0 {
			toks = append(toks, string(word))
			word = word[:0]
		}
	}
	flushCJK := func() {
		for i := range cjk {
			toks = append(toks, string(cjk[i]))
		}
		for i := 0; i+1 < len(cjk); i++ {
			toks = append(toks, string(cjk[i:i+2]))
		}
		cjk = cjk[:0]
	}

	for _, r := range runes {
		switch {
		case isCJK(r):
			flushWord()
			cjk = append(cjk, r)
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			flushCJK()
			word = append(word, r)
		default:
			flushWord()
			flushCJK()
		}
	}
	flushWord()
	flushCJK()
	return toks
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF)
}

// normalize 做 L2 归一化，使余弦相似度可直接由点积得到。
func normalize(vec []float32) []float32 {
	normalizeInPlace(vec)
	return vec
}

// normalizeInPlace 就地做 L2 归一化；零向量保持不变。
func normalizeInPlace(vec []float32) {
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	if sum == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range vec {
		vec[i] *= inv
	}
}

// Norm 返回向量的 L2 范数，用于页面展示。
func Norm(vec []float32) float64 {
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	return math.Sqrt(sum)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
