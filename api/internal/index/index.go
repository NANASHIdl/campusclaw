// Package index 负责把知识库正文变成可检索的切片与向量。
//
// 入库四步：保存原文（第 3 课事务已提交）→ 切分 → 嵌入 → 写入向量库。
// 失败隔离：嵌入或写入失败时材料记录保留，对应切片标记 failed，
// 不写入不完整的向量数据。
package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"

	"campusclaw/api/internal/chunk"
	"campusclaw/api/internal/embed"
	"campusclaw/api/internal/vector"
)

// embedBatch 是单次调用嵌入网关的文本条数。
const embedBatch = 32

// Service 是索引流水线。
type Service struct {
	DB     *sql.DB
	Embed  embed.Provider
	Vector *vector.Client
}

// New 构造索引服务。
func New(db *sql.DB, emb embed.Provider, vec *vector.Client) *Service {
	return &Service{DB: db, Embed: emb, Vector: vec}
}

// IndexMaterial 重建某份材料的切片与向量：先删除旧切片与旧向量，再写入新的。
// 返回切片数量。opt 为本次请求指定的策略。
func (s *Service) IndexMaterial(ctx context.Context, materialID, classID, entryID int64, body string, opt chunk.Options) (int, error) {
	normalized, err := chunk.Normalize(opt)
	if err != nil {
		return 0, err
	}
	chunks, err := chunk.Split(body, normalized)
	if err != nil {
		return 0, err
	}

	// 先删除旧切片与旧向量记录（重建索引语义）。
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM knowledge_chunks WHERE material_id = ?`, materialID); err != nil {
		return 0, fmt.Errorf("删除旧切片失败: %w", err)
	}
	if s.Vector != nil {
		if err := s.Vector.DeleteByFilter(ctx, "material_id", materialID); err != nil && !errors.Is(err, vector.ErrUnavailable) {
			log.Printf("清理旧向量失败 material_id=%d: %v", materialID, err)
		}
	}

	if len(chunks) == 0 {
		return 0, nil
	}

	ids := make([]int64, 0, len(chunks))
	for _, c := range chunks {
		res, err := s.DB.ExecContext(ctx,
			`INSERT INTO knowledge_chunks
			   (knowledge_entry_id, material_id, class_id, chunk_index, chunk_text,
			    start_offset, end_offset, index_status, chunk_strategy)
			 VALUES (?, ?, ?, ?, ?, ?, ?, 'ready', ?)`,
			entryID, materialID, classID, c.Index, c.Text, c.Start, c.End, normalized.Strategy,
		)
		if err != nil {
			return 0, fmt.Errorf("写入切片失败: %w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}

	// 逐批嵌入。
	vecs := make([][]float32, 0, len(chunks))
	for start := 0; start < len(chunks); start += embedBatch {
		end := start + embedBatch
		if end > len(chunks) {
			end = len(chunks)
		}
		texts := make([]string, 0, end-start)
		for _, c := range chunks[start:end] {
			texts = append(texts, c.Text)
		}
		out, err := s.Embed.Embed(ctx, texts)
		if err != nil {
			s.markFailed(ctx, materialID)
			return 0, fmt.Errorf("嵌入失败: %w", err)
		}
		vecs = append(vecs, out...)
	}

	// 写入向量库：向量主键 = 切片主键，payload 仅含编号。
	points := make([]vector.Point, 0, len(chunks))
	for i, c := range chunks {
		points = append(points, vector.Point{
			ID:     uint64(ids[i]),
			Vector: vecs[i],
			Payload: map[string]any{
				"class_id":           classID,
				"material_id":        materialID,
				"knowledge_entry_id": entryID,
				"chunk_id":           ids[i],
				"chunk_index":        c.Index,
			},
		})
	}
	if s.Vector != nil {
		if err := s.Vector.Upsert(ctx, points); err != nil {
			s.markFailed(ctx, materialID)
			return 0, fmt.Errorf("写入向量库失败: %w", err)
		}
	}

	log.Printf("索引完成 material_id=%d class_id=%d 策略=%s 切片=%d 嵌入=%s",
		materialID, classID, normalized.Strategy, len(chunks), s.Embed.Kind())
	return len(chunks), nil
}

// markFailed 把该材料的切片标记为 failed——不写入不完整的向量数据。
func (s *Service) markFailed(ctx context.Context, materialID int64) {
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE knowledge_chunks SET index_status = 'failed' WHERE material_id = ?`, materialID); err != nil {
		log.Printf("标记切片失败状态出错 material_id=%d: %v", materialID, err)
	}
}

// Backfill 为「有 knowledge_entries 但无 knowledge_chunks」的材料补齐索引。
//
// 采用 auto 策略（课件：种子材料启动时补齐索引同样采用自动切分）。
// 向量库不可用时整体跳过，避免把可用的切片误标为 failed。
func (s *Service) Backfill(ctx context.Context) error {
	if s.Vector != nil {
		if err := s.Vector.Ping(ctx); err != nil {
			log.Printf("向量库暂不可用，跳过补齐索引（关键字检索仍可用）: %v", err)
			return nil
		}
	}

	rows, err := s.DB.QueryContext(ctx,
		`SELECT m.id, m.class_id, k.id, k.body_text
		   FROM materials m
		   JOIN knowledge_entries k ON k.material_id = m.id
		  WHERE NOT EXISTS (SELECT 1 FROM knowledge_chunks c WHERE c.material_id = m.id)`)
	if err != nil {
		return fmt.Errorf("查询待补齐材料失败: %w", err)
	}
	defer rows.Close()

	type pending struct {
		materialID, classID, entryID int64
		body                         string
	}
	var list []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.materialID, &p.classID, &p.entryID, &p.body); err != nil {
			return err
		}
		list = append(list, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, p := range list {
		n, err := s.IndexMaterial(ctx, p.materialID, p.classID, p.entryID, p.body, chunk.Options{Strategy: chunk.StrategyAuto})
		if err != nil {
			log.Printf("补齐索引失败 material_id=%d: %v", p.materialID, err)
			continue
		}
		log.Printf("补齐索引 material_id=%d 切片=%d", p.materialID, n)
	}
	return nil
}

// CollectionMeta 返回向量集合名、距离度量与维度，用于页面展示。
func (s *Service) CollectionMeta() (string, string, int) {
	if s.Vector == nil {
		return "", "", s.Embed.Dim()
	}
	return s.Vector.Collection, "Cosine", s.Vector.Dim
}

// ChunkRow 是 knowledge_chunks 的一行。
type ChunkRow struct {
	ID          int64
	EntryID     int64
	MaterialID  int64
	ClassID     int64
	ChunkIndex  int
	Text        string
	StartOffset int
	EndOffset   int
	IndexStatus string
}

// VectorView 是「材料详情 · 向量」页面需要的一行：MySQL 侧与向量库侧的对应关系。
type VectorView struct {
	PointID           uint64    `json:"point_id"`
	ChunkIndex        int       `json:"chunk_index"`
	StartOffset       int       `json:"start_offset"`
	EndOffset         int       `json:"end_offset"`
	IndexStatus       string    `json:"index_status"`
	Excerpt           string    `json:"excerpt"`
	Dim               int       `json:"dim"`
	Norm              float64   `json:"norm"`
	Preview           []float32 `json:"preview"`
	Vector            []float32 `json:"vector"`
	MySQLChunkID      int64     `json:"mysql_chunk_id"`
	MySQLEntryID      int64     `json:"mysql_entry_id"`
	MySQLMaterialID   int64     `json:"mysql_material_id"`
	MySQLClassID      int64     `json:"mysql_class_id"`
	PayloadClassID    any       `json:"payload_class_id"`
	PayloadMaterialID any       `json:"payload_material_id"`
	PayloadEntryID    any       `json:"payload_entry_id"`
	PayloadChunkID    any       `json:"payload_chunk_id"`
	PayloadChunkIdx   any       `json:"payload_chunk_index"`
	Linked            bool      `json:"linked"`
}

// LoadChunks 读取某份材料的切片（按序号）。
func (s *Service) LoadChunks(ctx context.Context, materialID int64) ([]ChunkRow, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, knowledge_entry_id, material_id, class_id, chunk_index, chunk_text,
		        start_offset, end_offset, index_status
		   FROM knowledge_chunks
		  WHERE material_id = ?
		  ORDER BY chunk_index`,
		materialID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ChunkRow
	for rows.Next() {
		var c ChunkRow
		if err := rows.Scan(&c.ID, &c.EntryID, &c.MaterialID, &c.ClassID, &c.ChunkIndex,
			&c.Text, &c.StartOffset, &c.EndOffset, &c.IndexStatus); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Vectors 组装某份材料的向量视图：MySQL 提供切片与偏移，向量库提供向量本身。
func (s *Service) Vectors(ctx context.Context, materialID int64) ([]VectorView, error) {
	chunks, err := s.LoadChunks(ctx, materialID)
	if err != nil {
		return nil, err
	}

	vectors := map[uint64]vector.Hit{}
	if s.Vector != nil && len(chunks) > 0 {
		ids := make([]uint64, 0, len(chunks))
		for _, c := range chunks {
			ids = append(ids, uint64(c.ID))
		}
		hits, err := s.Vector.Retrieve(ctx, ids)
		if err != nil {
			if errors.Is(err, vector.ErrUnavailable) {
				log.Printf("向量库不可用，仅返回 MySQL 侧切片 material_id=%d: %v", materialID, err)
			} else {
				return nil, err
			}
		}
		for _, h := range hits {
			vectors[h.ID] = h
		}
	}

	out := make([]VectorView, 0, len(chunks))
	for _, c := range chunks {
		v := VectorView{
			PointID:         uint64(c.ID),
			ChunkIndex:      c.ChunkIndex,
			StartOffset:     c.StartOffset,
			EndOffset:       c.EndOffset,
			IndexStatus:     c.IndexStatus,
			Excerpt:         excerpt(c.Text, 160),
			MySQLChunkID:    c.ID,
			MySQLEntryID:    c.EntryID,
			MySQLMaterialID: c.MaterialID,
			MySQLClassID:    c.ClassID,
			Dim:             s.Embed.Dim(),
		}
		if h, ok := vectors[uint64(c.ID)]; ok {
			v.Linked = true
			v.Vector = h.Vector
			v.Dim = len(h.Vector)
			v.Norm = embed.Norm(h.Vector)
			v.Preview = preview(h.Vector, 8)
			v.PayloadClassID = h.Payload["class_id"]
			v.PayloadMaterialID = h.Payload["material_id"]
			v.PayloadEntryID = h.Payload["knowledge_entry_id"]
			v.PayloadChunkID = h.Payload["chunk_id"]
			v.PayloadChunkIdx = h.Payload["chunk_index"]
		}
		out = append(out, v)
	}
	return out, nil
}

func excerpt(text string, n int) string {
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	return string(runes[:n]) + "…"
}

func preview(vec []float32, n int) []float32 {
	if len(vec) <= n {
		return vec
	}
	return vec[:n]
}
