package qa

import (
	"errors"
	"log"
	"net/http"

	"campusclaw/api/internal/auth"
	"campusclaw/api/internal/httpx"
	"campusclaw/api/internal/retrieval"
)

// Handler 暴露问答接口。
type Handler struct {
	Service *Service
}

func NewHandler(s *Service) *Handler { return &Handler{Service: s} }

type askRequest struct {
	Q       string    `json:"q"`
	History []Message `json:"history"`
	// 客户端若注入 system 消息，会在对话模块处被丢弃。
}

// Ask 处理 POST /api/ask。
func (h *Handler) Ask(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}

	var req askRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}

	ans, err := h.Service.Ask(r.Context(), user.ClassID, req.Q, req.History)
	if err != nil {
		switch {
		case errors.Is(err, retrieval.ErrQueryTooShort):
			httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": retrieval.ErrQueryTooShort.Error()})
		case errors.Is(err, retrieval.ErrVectorUnavailable):
			log.Printf("ask: reason=vector_unavailable user=%s class=%d", user.Username, user.ClassID)
			httpx.JSON(w, http.StatusServiceUnavailable, map[string]string{"error": retrieval.ErrVectorUnavailable.Error()})
		default:
			log.Printf("ask: user=%s err=%v", user.Username, err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error")
		}
		return
	}

	log.Printf("ask ok: user=%s class=%d status=%s citations=%d", user.Username, user.ClassID, ans.Status, len(ans.Citations))
	httpx.JSON(w, http.StatusOK, ans)
}
