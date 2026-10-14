package materials

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"campusclaw/api/internal/auth"
	"campusclaw/api/internal/chunk"
	"campusclaw/api/internal/httpx"
)

// chunkOptionsFromForm 从 multipart 表单读取切分参数。
//
// 未提供时按 auto 处理；auto 策略下另行填写的长度与预处理参数不生效
// （由 chunk.Normalize 保证）。
func chunkOptionsFromForm(r *http.Request) (chunk.Options, error) {
	opt := chunk.Options{
		Strategy:           strings.TrimSpace(r.FormValue("chunk_strategy")),
		RemoveURLs:         truthy(r.FormValue("remove_urls")),
		CollapseWhitespace: truthy(r.FormValue("collapse_whitespace")),
	}
	if v := strings.TrimSpace(r.FormValue("max_chars")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return opt, err
		}
		opt.MaxChars = n
	}
	if v := strings.TrimSpace(r.FormValue("overlap_percent")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return opt, err
		}
		opt.OverlapPercent = n
	}
	if v := strings.TrimSpace(r.FormValue("separator")); v != "" {
		opt.Separators = []string{v}
	}
	return chunk.Normalize(opt)
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "on", "yes":
		return true
	}
	return false
}

// Vectors 处理 GET /api/materials/{id}/vectors。
//
// 返回 MySQL 侧切片与向量库侧的对应关系：向量主键 = knowledge_chunks.id。
func (h *Handler) Vectors(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}
	id, ok := pathID(r)
	if !ok {
		httpx.NotFound(w)
		return
	}

	row, found, err := h.fetchMaterial(id)
	if err != nil {
		log.Printf("fetch material %d: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if !found || row.ClassID != user.ClassID {
		reason := "not_found"
		if found {
			reason = "cross_class"
		}
		log.Printf("material vectors: reason=%s material_id=%d user=%s", reason, id, user.Username)
		httpx.NotFound(w)
		return
	}

	entryID := h.entryIDFor(id)
	collection, distance, dim := "", "", 0
	var views any = []any{}
	if h.Indexer != nil {
		collection, distance, dim = h.Indexer.CollectionMeta()
		chunks, err := h.Indexer.Vectors(r.Context(), id)
		if err != nil {
			log.Printf("load vectors material_id=%d: %v", id, err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error")
			return
		}
		views = chunks
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"collection":         collection,
		"distance":           distance,
		"dim":                dim,
		"material_id":        id,
		"knowledge_entry_id": entryID,
		"class_id":           user.ClassID,
		"chunks":             views,
	})
}

type reindexRequest struct {
	Strategy           string `json:"strategy"`
	MaxChars           int    `json:"max_chars"`
	OverlapPercent     int    `json:"overlap_percent"`
	RemoveURLs         bool   `json:"remove_urls"`
	CollapseWhitespace bool   `json:"collapse_whitespace"`
	Separator          string `json:"separator"`
}

// Reindex 处理 POST /api/materials/{id}/reindex：按本次请求的策略重建切片与向量。
//
// 仅教师可用；班级仍取自会话。
func (h *Handler) Reindex(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}
	if !user.IsTeacher() {
		log.Printf("reindex rejected: reason=role_forbidden user=%s role=%s", user.Username, user.Role)
		httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden)
		return
	}
	id, ok := pathID(r)
	if !ok {
		httpx.NotFound(w)
		return
	}

	row, found, err := h.fetchMaterial(id)
	if err != nil {
		log.Printf("fetch material %d: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if !found || row.ClassID != user.ClassID {
		reason := "not_found"
		if found {
			reason = "cross_class"
		}
		log.Printf("material reindex: reason=%s material_id=%d user=%s", reason, id, user.Username)
		httpx.NotFound(w)
		return
	}

	var req reindexRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	opt := chunk.Options{
		Strategy:           req.Strategy,
		MaxChars:           req.MaxChars,
		OverlapPercent:     req.OverlapPercent,
		RemoveURLs:         req.RemoveURLs,
		CollapseWhitespace: req.CollapseWhitespace,
	}
	if req.Separator != "" {
		opt.Separators = []string{req.Separator}
	}
	normalized, err := chunk.Normalize(opt)
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	entryID, body, found, err := h.entryFor(id)
	if err != nil {
		log.Printf("load entry for material %d: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if !found {
		httpx.NotFound(w)
		return
	}
	if h.Indexer == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "indexer_unavailable")
		return
	}

	count, err := h.Indexer.IndexMaterial(r.Context(), id, row.ClassID, entryID, body, normalized)
	if err != nil {
		log.Printf("reindex failed material_id=%d: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}

	log.Printf("reindex ok: material_id=%d strategy=%s chunks=%d user=%s", id, normalized.Strategy, count, user.Username)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"material_id":    id,
		"chunk_strategy": normalized.Strategy,
		"chunk_count":    count,
	})
}

func (h *Handler) entryFor(materialID int64) (int64, string, bool, error) {
	var id int64
	var body string
	err := h.DB.QueryRow(
		`SELECT id, body_text FROM knowledge_entries WHERE material_id = ? ORDER BY id LIMIT 1`, materialID,
	).Scan(&id, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	return id, body, true, nil
}

func (h *Handler) entryIDFor(materialID int64) int64 {
	id, _, found, err := h.entryFor(materialID)
	if err != nil || !found {
		return 0
	}
	return id
}
