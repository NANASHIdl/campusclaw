package retrieval

import (
	"errors"
	"log"
	"net/http"

	"campusclaw/api/internal/auth"
	"campusclaw/api/internal/httpx"
)

// Handler 暴露检索接口。
type Handler struct {
	Service *Service
}

func NewHandler(s *Service) *Handler { return &Handler{Service: s} }

type searchRequest struct {
	Q    string `json:"q"`
	Mode string `json:"mode"`
}

// Search 处理 POST /api/search。
//
// 请求体中的 class_id 等字段即使存在也被忽略：班级只取自会话。
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}

	var req searchRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}

	res, err := h.Service.Search(r.Context(), user.ClassID, req.Q, req.Mode)
	if err != nil {
		switch {
		case errors.Is(err, ErrQueryTooShort):
			httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": ErrQueryTooShort.Error()})
		case errors.Is(err, ErrBadMode):
			httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": ErrBadMode.Error()})
		case errors.Is(err, ErrVectorUnavailable):
			log.Printf("search: reason=vector_unavailable user=%s class=%d", user.Username, user.ClassID)
			httpx.JSON(w, http.StatusServiceUnavailable, map[string]string{"error": ErrVectorUnavailable.Error()})
		default:
			log.Printf("search: user=%s mode=%s err=%v", user.Username, req.Mode, err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error")
		}
		return
	}

	log.Printf("search ok: user=%s class=%d mode=%s hits=%d", user.Username, user.ClassID, res.Mode, len(res.Hits))
	httpx.JSON(w, http.StatusOK, res)
}
