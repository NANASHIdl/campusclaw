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

// AskRequest 是问答请求体，供 OpenAPI 文档引用。
type AskRequest struct {
	Q       string    `json:"q"`
	History []Message `json:"history"`
}

// Ask 处理 POST /api/ask。
//
// @Summary      按本班资料作简短回答
// @Description  先以混合模式取本班前 4 条切片；有切片才调用对话网关，回答中以 [1]、[2] 标注出处。无命中时返回 status=no_evidence、固定文案与空 citations，且不调用模型。
// @Tags         search
// @Accept       json
// @Produce      json
// @Security     bearerAuth
// @Param        body body qa.AskRequest true "问句"
// @Success      200 {object} qa.Answer
// @Failure      400 {object} httpx.ErrorBody
// @Failure      401 {object} httpx.ErrorBody
// @Failure      503 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/ask [post]
// @ID           SRC-002
func (h *Handler) Ask(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}

	var req AskRequest
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
