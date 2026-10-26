// Package retrieval 实现关键字、向量与混合三种检索，并提供可追溯的命中结果。
//
// 关键约定：
//   - 班级只取自登录会话；请求中自带的 class_id 一律丢弃。
//   - 关键字与向量两条路径都携带班级条件，回表时再次核对。
//   - 融合的是「名次」而非分数：RRF，score = Σ 1/(k + rank)，k = 60。
//   - 摘录一律取自 MySQL 的 chunk_text，向量库不保存正文。
package retrieval

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"campusclaw/api/internal/embed"
	"campusclaw/api/internal/vector"
)

// 检索模式。
const (
	ModeKeyword = "keyword"
	ModeVector  = "vector"
	ModeHybrid  = "hybrid"
)

// 对外错误。
var (
	ErrQueryTooShort     = errors.New("查询至少需要 2 个字符")
	ErrBadMode           = errors.New("未知检索模式")
	ErrVectorUnavailable = errors.New("向量库不可用")
)

// Service 是检索服务。
type Service struct {
	DB        *sql.DB
	Embed     embed.Provider
	Vector    *vector.Client
	Threshold float64 // 余弦相似度下限，低于此值的候选被丢弃
	RRFK      int     // RRF 常数 k
	Limit     int     // 单次检索返回的最大条数
}

// New 构造检索服务。
func New(db *sql.DB, emb embed.Provider, vec *vector.Client, threshold float64, rrfK, limit int) *Service {
	if threshold <= 0 {
		threshold = 0.35
	}
	if rrfK <= 0 {
		rrfK = 60
	}
	if limit <= 0 {
		limit = 10
	}
	return &Service{DB: db, Embed: emb, Vector: vec, Threshold: threshold, RRFK: rrfK, Limit: limit}
}

// Hit 是一条可追溯的命中结果。
type Hit struct {
	ChunkID       int64    `json:"chunk_id"`
	MaterialID    int64    `json:"material_id"`
	MaterialTitle string   `json:"material_title"`
	ChunkIndex    int      `json:"chunk_index"`
	StartOffset   int      `json:"start_offset"`
	EndOffset     int      `json:"end_offset"`
	Snippet       string   `json:"snippet"`
	ScoreFinal    float64  `json:"score_final"`
	ScoreKeyword  *float64 `json:"score_keyword"`
	ScoreVector   *float64 `json:"score_vector"`
	RankKeyword   *int     `json:"rank_keyword"`
	RankVector    *int     `json:"rank_vector"`
}

// QueryVector 是问句的嵌入结果，供页面展示「问句先变成向量」这一步。
type QueryVector struct {
	Dim     int       `json:"dim"`
	Norm    float64   `json:"norm"`
	Preview []float32 `json:"preview"`
	Vector  []float32 `json:"vector"`
}

// Result 是检索响应。
type Result struct {
	Status      string       `json:"status"`
	Mode        string       `json:"mode"`
	Note        string       `json:"note,omitempty"`
	QueryVector *QueryVector `json:"query_vector,omitempty"`
	Hits        []Hit        `json:"hits"`
}

type rawHit struct {
	ChunkID       int64
	MaterialID    int64
	MaterialTitle string
	ChunkIndex    int
	StartOffset   int
	EndOffset     int
	Snippet       string
	ScoreKeyword  float64
	ScoreVector   float64
}

// Scope 限定检索范围。
//
// 该结构只由服务端内部构造（智能体绑定的材料），不从客户端读取。
//
// 两种「空」必须区分开：
//   - 零值 Scope{}：不做材料限定，检索全班（/api/search 的语义）；
//   - Scoped 为真且 MaterialIDs 为空：调用方确实限定了范围，但范围内一份材料也没有，
//     此时必须返回空结果。若混同于零值，空绑定的智能体会检索到全班材料，
//     越出「只在绑定材料内检索」的边界。
type Scope struct {
	MaterialIDs []int64
	Scoped      bool
}

// Empty 判断本次范围是否明确为空（限定范围内没有任何材料）。
func (s Scope) Empty() bool {
	return s.Scoped && len(s.MaterialIDs) == 0
}


// Search 执行检索。classID 必须来自会话。
func (s *Service) Search(ctx context.Context, classID int64, query, mode string) (Result, error) {
	return s.SearchScoped(ctx, classID, query, mode, Scope{}, s.Limit)
}

// SearchScoped 在指定范围内执行检索。classID 必须来自会话。
func (s *Service) SearchScoped(ctx context.Context, classID int64, query, mode string, scope Scope, limit int) (Result, error) {
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) < 2 {
		return Result{}, ErrQueryTooShort
	}
	switch mode {
	case "", ModeHybrid:
		mode = ModeHybrid
	case ModeKeyword, ModeVector:
	default:
		return Result{}, ErrBadMode
	}

	if limit <= 0 {
		limit = s.Limit
	}

	// 限定范围内没有任何材料：直接给空结果，不查库、不调嵌入。
	if scope.Empty() {
		return Result{Status: "ok", Mode: mode, Hits: []Hit{}}, nil
	}

	var (
		kwHits  []rawHit
		vecHits []rawHit
		qvec    []float32
	)

	// 向量路径先执行：不可用时立即返回 503，不编造相似度分数。
	if mode == ModeVector || mode == ModeHybrid {
		vecs, err := s.Embed.Embed(ctx, []string{query})
		if err != nil {
			return Result{}, fmt.Errorf("%w: 嵌入问句失败: %v", ErrVectorUnavailable, err)
		}
		if len(vecs) == 0 {
			return Result{}, fmt.Errorf("%w: 嵌入问句返回空结果", ErrVectorUnavailable)
		}
		qvec = vecs[0]

		vecHits, err = s.vectorSearch(ctx, classID, qvec, scope, limit)
		if err != nil {
			if errors.Is(err, vector.ErrUnavailable) {
				return Result{}, ErrVectorUnavailable
			}
			return Result{}, err
		}
	}

	if mode == ModeKeyword || mode == ModeHybrid {
		var err error
		kwHits, err = s.keywordSearch(ctx, classID, query, scope, limit)
		if err != nil {
			return Result{}, err
		}
	}

	res := Result{Status: "ok", Mode: mode}
	switch mode {
	case ModeKeyword:
		res.Hits = rankSingle(kwHits, "keyword", s.RRFK)
		res.Note = "本次只走关键字路径：MySQL 全文索引在本班切片里匹配原词，按全文分由高到低排名。"
	case ModeVector:
		res.Hits = rankSingle(vecHits, "vector", s.RRFK)
		res.QueryVector = newQueryVector(qvec)
		res.Note = fmt.Sprintf("本次只走向量路径：问句先变成向量，再与切片向量算余弦；低于 %.2f 的候选被丢弃。", s.Threshold)
	case ModeHybrid:
		res.Hits = fuse(kwHits, vecHits, s.RRFK)
		res.QueryVector = newQueryVector(qvec)
		res.Note = fmt.Sprintf("本次同时走两条路径：各路先按自己的分数排名，再用 RRF 合成名次分 1/(%d+名次)；两路都命中的切片排更前。", s.RRFK)
	}
	if res.Hits == nil {
		res.Hits = []Hit{}
	}
	return res, nil
}

// keywordSearch 仅查询 MySQL 全文索引；不调用嵌入，也不访问向量库。
func (s *Service) keywordSearch(ctx context.Context, classID int64, query string, scope Scope, limit int) ([]rawHit, error) {
	matClause, matArgs := materialClause(classID, scope)
	args := append([]any{query}, matArgs...)
	args = append(args, query, limit)

	rows, err := s.DB.QueryContext(ctx,
		`SELECT c.id, c.material_id, m.title, c.chunk_index, c.start_offset, c.end_offset, c.chunk_text,
		        MATCH(c.chunk_text) AGAINST (? IN NATURAL LANGUAGE MODE) AS score
		   FROM knowledge_chunks c
		   JOIN materials m ON m.id = c.material_id
		  WHERE c.class_id = ?`+matClause+`
		    AND c.index_status = 'ready'
		    AND MATCH(c.chunk_text) AGAINST (? IN NATURAL LANGUAGE MODE) > 0
		  ORDER BY score DESC, c.id ASC
		  LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("关键字检索失败: %w", err)
	}
	defer rows.Close()

	var out []rawHit
	for rows.Next() {
		var h rawHit
		if err := rows.Scan(&h.ChunkID, &h.MaterialID, &h.MaterialTitle, &h.ChunkIndex,
			&h.StartOffset, &h.EndOffset, &h.Snippet, &h.ScoreKeyword); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// vectorSearch 走向量库，阈值过滤后回 MySQL 取正文（再次带班级条件）。
func (s *Service) vectorSearch(ctx context.Context, classID int64, qvec []float32, scope Scope, limit int) ([]rawHit, error) {
	if s.Vector == nil {
		return nil, ErrVectorUnavailable
	}
	// 多取候选，阈值过滤后再截断。
	hits, err := s.Vector.SearchScoped(ctx, qvec, classID, scope.MaterialIDs, limit*4)
	if err != nil {
		return nil, err
	}

	type scored struct {
		id    uint64
		score float64
	}
	kept := make([]scored, 0, len(hits))
	for _, h := range hits {
		if h.Score < s.Threshold {
			continue
		}
		kept = append(kept, scored{id: h.ID, score: h.Score})
		if len(kept) >= limit {
			break
		}
	}
	if len(kept) == 0 {
		return nil, nil
	}

	ids := make([]any, 0, len(kept))
	for _, k := range kept {
		ids = append(ids, int64(k.id))
	}
	inClause := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")

	// 回表：班级条件与材料范围都必须再次出现，即便向量库侧漏加过滤也不会越界。
	matClause, matArgs := materialClause(classID, scope)
	args := append(matArgs, ids...)
	rows, err := s.DB.QueryContext(ctx,
		`SELECT c.id, c.material_id, m.title, c.chunk_index, c.start_offset, c.end_offset, c.chunk_text
		   FROM knowledge_chunks c
		   JOIN materials m ON m.id = c.material_id
		  WHERE c.class_id = ?`+matClause+`
		    AND c.index_status = 'ready'
		    AND c.id IN (`+inClause+`)`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("向量结果回表失败: %w", err)
	}
	defer rows.Close()

	byID := map[int64]rawHit{}
	for rows.Next() {
		var h rawHit
		if err := rows.Scan(&h.ChunkID, &h.MaterialID, &h.MaterialTitle, &h.ChunkIndex,
			&h.StartOffset, &h.EndOffset, &h.Snippet); err != nil {
			return nil, err
		}
		byID[h.ChunkID] = h
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]rawHit, 0, len(kept))
	for _, k := range kept {
		h, ok := byID[int64(k.id)]
		if !ok {
			// 归属其他班级或已删除：静默丢弃，真实原因仅写日志。
			continue
		}
		h.ScoreVector = k.score
		out = append(out, h)
	}
	return out, nil
}

// rankSingle 把单路结果转为 RRF 名次分。
func rankSingle(hits []rawHit, path string, k int) []Hit {
	out := make([]Hit, 0, len(hits))
	for i, h := range hits {
		rank := i + 1
		hit := toHit(h)
		hit.ScoreFinal = 1 / float64(k+rank)
		if path == ModeKeyword {
			hit.RankKeyword = &rank
			score := h.ScoreKeyword
			hit.ScoreKeyword = &score
		} else {
			hit.RankVector = &rank
			score := h.ScoreVector
			hit.ScoreVector = &score
		}
		out = append(out, hit)
	}
	return out
}

// fuse 用 RRF 融合两路名次；缺席的一路不贡献分数。
func fuse(kw, vec []rawHit, k int) []Hit {
	type entry struct {
		raw      rawHit
		rankKw   int
		rankVec  int
		scoreKw  float64
		scoreVec float64
	}
	byID := map[int64]*entry{}
	order := make([]int64, 0, len(kw)+len(vec))

	upsert := func(id int64, h rawHit) *entry {
		e, ok := byID[id]
		if !ok {
			e = &entry{raw: h}
			byID[id] = e
			order = append(order, id)
		}
		return e
	}

	for i, h := range kw {
		e := upsert(h.ChunkID, h)
		e.rankKw = i + 1
		e.scoreKw = h.ScoreKeyword
	}
	for i, h := range vec {
		e := upsert(h.ChunkID, h)
		e.rankVec = i + 1
		e.scoreVec = h.ScoreVector
	}

	out := make([]Hit, 0, len(order))
	for _, id := range order {
		e := byID[id]
		hit := toHit(e.raw)
		if e.rankKw > 0 {
			hit.ScoreFinal += 1 / float64(k+e.rankKw)
			rank := e.rankKw
			hit.RankKeyword = &rank
			score := e.scoreKw
			hit.ScoreKeyword = &score
		}
		if e.rankVec > 0 {
			hit.ScoreFinal += 1 / float64(k+e.rankVec)
			rank := e.rankVec
			hit.RankVector = &rank
			score := e.scoreVec
			hit.ScoreVector = &score
		}
		out = append(out, hit)
	}

	// 按合成分降序；同分时按切片主键升序，保证结果稳定。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			if out[j].ScoreFinal > out[j-1].ScoreFinal ||
				(out[j].ScoreFinal == out[j-1].ScoreFinal && out[j].ChunkID < out[j-1].ChunkID) {
				out[j], out[j-1] = out[j-1], out[j]
				continue
			}
			break
		}
	}
	return out
}

func toHit(h rawHit) Hit {
	return Hit{
		ChunkID:       h.ChunkID,
		MaterialID:    h.MaterialID,
		MaterialTitle: h.MaterialTitle,
		ChunkIndex:    h.ChunkIndex,
		StartOffset:   h.StartOffset,
		EndOffset:     h.EndOffset,
		Snippet:       h.Snippet,
	}
}

func newQueryVector(vec []float32) *QueryVector {
	if len(vec) == 0 {
		return nil
	}
	preview := vec
	if len(preview) > 8 {
		preview = preview[:8]
	}
	return &QueryVector{
		Dim:     len(vec),
		Norm:    embed.Norm(vec),
		Preview: preview,
		Vector:  vec,
	}
}

// Top 取前 n 条命中，供问答模块组装上下文。
func Top(hits []Hit, n int) []Hit {
	if n <= 0 || len(hits) <= n {
		return hits
	}
	return hits[:n]
}

// materialClause 生成材料范围条件，返回附加 SQL 片段与对应的参数
// （参数首位固定为 classID，调用方须按 SQL 中占位符顺序拼接）。
func materialClause(classID int64, scope Scope) (string, []any) {
	args := []any{classID}
	if len(scope.MaterialIDs) == 0 {
		return "", args
	}
	clause := " AND c.material_id IN (" + placeholdersOf(len(scope.MaterialIDs)) + ")"
	for _, id := range scope.MaterialIDs {
		args = append(args, id)
	}
	return clause, args
}

func placeholdersOf(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
