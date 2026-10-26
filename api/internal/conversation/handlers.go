package conversation

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"campusclaw/api/internal/agent"
	"campusclaw/api/internal/auth"
	"campusclaw/api/internal/httpx"
)

// Handler 暴露会话接口。
type Handler struct {
	Repo   *Repo
	Agents *agent.Repo
}

func NewHandler(r *Repo, agents *agent.Repo) *Handler {
	return &Handler{Repo: r, Agents: agents}
}

// CreateRequest 是新建会话的请求体，供 OpenAPI 文档引用。
type CreateRequest struct {
	AgentID int64 `json:"agent_id"`
}

// List 处理 GET /api/conversations。
//
// @Summary      列出我的会话
// @Description  只返回当前用户在本班的会话，按 updated_at 倒序。
// @Tags         conversations
// @Produce      json
// @Security     bearerAuth
// @Success      200 {object} map[string]interface{}
// @Failure      401 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/conversations [get]
// @ID           CNV-001
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}
	list, err := h.Repo.List(user.ID, user.ClassID)
	if err != nil {
		log.Printf("conversations list: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"conversations": list})
}

// Create 处理 POST /api/conversations：必须选定本班一个已启用的智能体。
//
// @Summary      按智能体新建会话
// @Description  必须指定本班一个已启用的智能体，否则 404；请求体为空或 agent_id 小于 1 返回 400；本班没有已启用的智能体也返回 400。会话创建后 agent_id 不再变更。
// @Tags         conversations
// @Accept       json
// @Produce      json
// @Security     bearerAuth
// @Param        body body conversation.CreateRequest true "选定的智能体"
// @Success      201 {object} conversation.Conversation
// @Failure      400 {object} httpx.ErrorBody
// @Failure      401 {object} httpx.ErrorBody
// @Failure      404 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/conversations [post]
// @ID           CNV-002
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}
	var req CreateRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}

	if req.AgentID < 1 {
		// 区分「没选」与「本班根本没有可用的」。
		enabled, err := h.Agents.List(user.ClassID, true)
		if err != nil {
			log.Printf("conversations create: %v", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error")
			return
		}
		if len(enabled) == 0 {
			httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": "本班没有已启用的智能体"})
			return
		}
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": "请选择智能体"})
		return
	}

	ag, err := h.Agents.Get(user.ClassID, req.AgentID)
	if err != nil || !ag.Enabled {
		// 他班、已停用、不存在对外完全一致。
		log.Printf("conversations create: reason=agent_unavailable agent=%d user=%s", req.AgentID, user.Username)
		httpx.NotFound(w)
		return
	}

	conv, err := h.Repo.Create(user.ID, user.ClassID, ag.ID)
	if err != nil {
		log.Printf("conversations create: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	log.Printf("conversations create ok: id=%d agent=%d user=%s", conv.ID, ag.ID, user.Username)
	httpx.JSON(w, http.StatusCreated, conv)
}

// Get 处理 GET /api/conversations/{id}：返回会话与全部消息。
//
// @Summary      读取会话和消息
// @Description  返回会话与其全部消息。他人会话、他班会话与不存在的会话返回同一个 404。
// @Tags         conversations
// @Produce      json
// @Security     bearerAuth
// @Param        id path int true "会话编号"
// @Success      200 {object} map[string]interface{}
// @Failure      401 {object} httpx.ErrorBody
// @Failure      404 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/conversations/{id} [get]
// @ID           CNV-003
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
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

	conv, err := h.Repo.Get(user.ID, user.ClassID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			log.Printf("conversations get: reason=not_found id=%d user=%s", id, user.Username)
			httpx.NotFound(w)
			return
		}
		log.Printf("conversations get: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}

	msgs, err := h.Repo.Messages(conv.ID, 0)
	if err != nil {
		log.Printf("conversations messages: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"conversation": conv, "messages": msgs})
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
