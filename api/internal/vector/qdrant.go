// Package vector 是 Qdrant 的极简 REST 客户端。
//
// 刻意不引入 qdrant-go：全部请求通过标准库 net/http 完成，vendor 保持最小。
// 向量库中只保存向量与若干编号，不保存切片正文。
package vector

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

// ErrUnavailable 表示向量库不可达。调用方据此降级（关键字检索仍可用）。
var ErrUnavailable = errors.New("向量库不可用")

// Point 是写入向量库的一条记录。ID 必须等于 knowledge_chunks.id。
type Point struct {
	ID      uint64         `json:"id"`
	Vector  []float32      `json:"vector"`
	Payload map[string]any `json:"payload"`
}

// Hit 是向量检索返回的一条候选。Retrieve 时 Vector 字段才会被填充。
type Hit struct {
	ID      uint64         `json:"id"`
	Score   float64        `json:"score"`
	Vector  []float32      `json:"vector,omitempty"`
	Payload map[string]any `json:"payload"`
}

// Client 是 Qdrant 客户端。
type Client struct {
	BaseURL    string
	Collection string
	Dim        int
	HTTP       *http.Client
}

// New 构造客户端。
func New(baseURL, collection string, dim int, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Collection: collection,
		Dim:        dim,
		HTTP:       &http.Client{Timeout: timeout},
	}
}

type apiResponse struct {
	Result json.RawMessage `json:"result"`
	Status any             `json:"status"`
}

func (c *Client) do(ctx context.Context, method, path string, payload any) (json.RawMessage, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("序列化请求失败: %w", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: 读取响应失败: %v", ErrUnavailable, err)
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%w: HTTP %d %s", ErrUnavailable, resp.StatusCode, truncate(string(raw), 200))
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("向量库返回 HTTP %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	var parsed apiResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("解析向量库响应失败: %w", err)
	}
	return parsed.Result, nil
}

// Ping 探测向量库是否可用。
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/collections", nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 500 {
		return fmt.Errorf("%w: HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	return nil
}

// EnsureCollection 确保集合存在（幂等）。集合已存在时不视为错误。
func (c *Client) EnsureCollection(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodGet, "/collections/"+c.Collection, nil)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrUnavailable) && !strings.Contains(err.Error(), "404") {
		// 其他错误（含 404）都尝试创建一次
		_ = err
	}

	payload := map[string]any{
		"vectors": map[string]any{"size": c.Dim, "distance": "Cosine"},
	}
	if _, err := c.do(ctx, http.MethodPut, "/collections/"+c.Collection, payload); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return nil
		}
		return err
	}
	return nil
}

// Upsert 写入或覆盖若干向量点。
func (c *Client) Upsert(ctx context.Context, points []Point) error {
	if len(points) == 0 {
		return nil
	}
	payload := map[string]any{"points": points}
	_, err := c.do(ctx, http.MethodPut, "/collections/"+c.Collection+"/points?wait=true", payload)
	return err
}

// DeleteByMaterial 删除某份材料的全部向量记录。
func (c *Client) DeleteByMaterial(ctx context.Context, materialID int64) error {
	payload := map[string]any{
		"filter": matchFilter("material_id", materialID),
	}
	_, err := c.do(ctx, http.MethodPost, "/collections/"+c.Collection+"/points/delete?wait=true", payload)
	return err
}

// Search 在指定班级内按余弦相似度检索。
//
// 过滤条件必须包含 class_id：这是检索侧班级隔离的向量一侧。
func (c *Client) Search(ctx context.Context, vec []float32, classID int64, limit int) ([]Hit, error) {
	return c.SearchScoped(ctx, vec, classID, nil, limit)
}

// SearchScoped 在班级之外再叠加材料范围过滤（智能体只在自己绑定的材料里检索）。
func (c *Client) SearchScoped(ctx context.Context, vec []float32, classID int64, materialIDs []int64, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 10
	}
	must := []map[string]any{
		{"key": "class_id", "match": map[string]any{"value": classID}},
	}
	if len(materialIDs) > 0 {
		vals := make([]any, 0, len(materialIDs))
		for _, id := range materialIDs {
			vals = append(vals, id)
		}
		must = append(must, map[string]any{
			"key":   "material_id",
			"match": map[string]any{"any": vals},
		})
	}

	payload := map[string]any{
		"vector":       vec,
		"limit":        limit,
		"with_payload": true,
		"filter":       map[string]any{"must": must},
	}
	raw, err := c.do(ctx, http.MethodPost, "/collections/"+c.Collection+"/points/search", payload)
	if err != nil {
		return nil, err
	}
	var hits []Hit
	if err := json.Unmarshal(raw, &hits); err != nil {
		return nil, fmt.Errorf("解析检索结果失败: %w", err)
	}
	return hits, nil
}

// Retrieve 按主键批量取回向量点（含向量与 payload）。
func (c *Client) Retrieve(ctx context.Context, ids []uint64) ([]Hit, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	payload := map[string]any{
		"ids":          ids,
		"with_payload": true,
		"with_vector":  true,
	}
	raw, err := c.do(ctx, http.MethodPost, "/collections/"+c.Collection+"/points", payload)
	if err != nil {
		return nil, err
	}
	var hits []Hit
	if err := json.Unmarshal(raw, &hits); err != nil {
		return nil, fmt.Errorf("解析向量记录失败: %w", err)
	}
	return hits, nil
}

// CollectionInfo 返回集合元信息，用于页面展示。
func (c *Client) CollectionInfo(ctx context.Context) (map[string]any, error) {
	raw, err := c.do(ctx, http.MethodGet, "/collections/"+c.Collection, nil)
	if err != nil {
		return nil, err
	}
	var info map[string]any
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, err
	}
	return info, nil
}

// DeleteByFilter 按 payload 字段删除记录（重建索引时清理旧向量）。
func (c *Client) DeleteByFilter(ctx context.Context, key string, value any) error {
	payload := map[string]any{"filter": matchFilter(key, value)}
	_, err := c.do(ctx, http.MethodPost, "/collections/"+c.Collection+"/points/delete?wait=true", payload)
	return err
}

func matchFilter(key string, value any) map[string]any {
	return map[string]any{
		"must": []map[string]any{
			{"key": key, "match": map[string]any{"value": value}},
		},
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
