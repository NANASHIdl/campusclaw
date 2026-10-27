package mcp

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"campusclaw/api/internal/auth"
	"campusclaw/api/internal/httpx"
)

// Handler 暴露 MCP 服务器的管理接口。全部路由已由 RequireTeacher 保护。
type Handler struct {
	Repo   *Repo
	Client *Client
}

func NewHandler(repo *Repo, client *Client) *Handler {
	return &Handler{Repo: repo, Client: client}
}

// WriteRequest 是登记/修改服务器的请求体，供 OpenAPI 文档引用。
type WriteRequest struct {
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	AuthToken string `json:"auth_token"`
}

// List 处理 GET /api/mcp/servers。
//
// @Summary      列出本班 MCP 服务器
// @Description  仅教师。响应不含令牌原文，只返回 has_auth 布尔值与工具数。
// @Tags         mcp
// @Produce      json
// @Security     bearerAuth
// @Success      200 {object} map[string]interface{}
// @Failure      401 {object} httpx.ErrorBody
// @Failure      403 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/mcp/servers [get]
// @ID           MCP-001
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}
	list, err := h.Repo.ListServers(user.ClassID)
	if err != nil {
		log.Printf("mcp servers list: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"servers": list})
}

// Create 处理 POST /api/mcp/servers：登记一台服务器，初始为未连接。
//
// @Summary      登记 MCP 服务器
// @Description  仅教师。令牌写入服务端，响应不返回令牌。同班同名返回 400；登记后状态为未连接。
// @Tags         mcp
// @Accept       json
// @Produce      json
// @Security     bearerAuth
// @Param        body body mcp.WriteRequest true "服务器名称、地址与令牌"
// @Success      201 {object} mcp.Server
// @Failure      400 {object} httpx.ErrorBody
// @Failure      401 {object} httpx.ErrorBody
// @Failure      403 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/mcp/servers [post]
// @ID           MCP-002
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}
	var req WriteRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	in := Input{Name: req.Name, BaseURL: req.BaseURL, AuthToken: req.AuthToken}
	if err := in.Normalize(); err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	id, err := h.Repo.CreateServer(user.ClassID, in)
	if err != nil {
		writeRepoError(w, "mcp servers create", err)
		return
	}
	created, err := h.Repo.GetServer(user.ClassID, id)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	log.Printf("mcp servers create ok: id=%d name=%q user=%s", id, in.Name, user.Username)
	httpx.JSON(w, http.StatusCreated, created)
}

// Update 处理 PUT /api/mcp/servers/{id}：令牌留空表示不修改。
//
// @Summary      修改 MCP 服务器
// @Description  仅教师。令牌留空表示不修改；修改地址或令牌后清空连接状态与已发现工具。跨班或不存在返回 404。
// @Tags         mcp
// @Accept       json
// @Produce      json
// @Security     bearerAuth
// @Param        id   path int               true "服务器编号"
// @Param        body body mcp.WriteRequest  true "名称、地址与令牌"
// @Success      200 {object} mcp.Server
// @Failure      400 {object} httpx.ErrorBody
// @Failure      401 {object} httpx.ErrorBody
// @Failure      403 {object} httpx.ErrorBody
// @Failure      404 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/mcp/servers/{id} [put]
// @ID           MCP-003
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
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
	var req WriteRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	in := Input{Name: req.Name, BaseURL: req.BaseURL, AuthToken: req.AuthToken,
		KeepToken: req.AuthToken == ""}
	if err := in.Normalize(); err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := h.Repo.UpdateServer(user.ClassID, id, in); err != nil {
		writeRepoError(w, "mcp servers update", err)
		return
	}
	updated, err := h.Repo.GetServer(user.ClassID, id)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	log.Printf("mcp servers update ok: id=%d user=%s", id, user.Username)
	httpx.JSON(w, http.StatusOK, updated)
}

// Delete 处理 DELETE /api/mcp/servers/{id}：成功 204，重复删除 404。
//
// @Summary      删除 MCP 服务器
// @Description  仅教师。删除服务器及其工具清单；历史调用记录保留。重复删除返回 404。
// @Tags         mcp
// @Security     bearerAuth
// @Param        id path int true "服务器编号"
// @Success      204 "无响应体"
// @Failure      401 {object} httpx.ErrorBody
// @Failure      403 {object} httpx.ErrorBody
// @Failure      404 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/mcp/servers/{id} [delete]
// @ID           MCP-004
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
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
	if err := h.Repo.DeleteServer(user.ClassID, id); err != nil {
		writeRepoError(w, "mcp servers delete", err)
		return
	}
	log.Printf("mcp servers delete ok: id=%d user=%s", id, user.Username)
	w.WriteHeader(http.StatusNoContent)
}

// Connect 处理 POST /api/mcp/servers/{id}/connect：握手 + 发现，成功才替换清单。
//
// @Summary      连接并发现工具
// @Description  仅教师。先 initialize 握手再 tools/list 发现；两步都成功才整体替换工具清单并标记已连接，任一步失败停在未连接并给出可读原因。
// @Tags         mcp
// @Produce      json
// @Security     bearerAuth
// @Param        id path int true "服务器编号"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} httpx.ErrorBody
// @Failure      401 {object} httpx.ErrorBody
// @Failure      403 {object} httpx.ErrorBody
// @Failure      404 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/mcp/servers/{id}/connect [post]
// @ID           MCP-005
func (h *Handler) Connect(w http.ResponseWriter, r *http.Request) {
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
	server, err := h.Repo.GetServer(user.ClassID, id)
	if err != nil {
		writeRepoError(w, "mcp servers connect", err)
		return
	}
	token, err := h.Repo.ServerToken(user.ClassID, id)
	if err != nil {
		writeRepoError(w, "mcp servers connect", err)
		return
	}

	if err := h.Client.Initialize(r.Context(), server.BaseURL, token); err != nil {
		reason := "握手失败：" + err.Error()
		_ = h.Repo.MarkConnectFailed(server.ID, reason)
		log.Printf("mcp connect failed: id=%d step=initialize err=%v", id, err)
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": reason})
		return
	}
	tools, err := h.Client.ListTools(r.Context(), server.BaseURL, token)
	if err != nil {
		reason := "发现工具失败：" + err.Error()
		_ = h.Repo.MarkConnectFailed(server.ID, reason)
		log.Printf("mcp connect failed: id=%d step=tools/list err=%v", id, err)
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": reason})
		return
	}
	if err := h.Repo.ReplaceTools(server.ID, tools); err != nil {
		reason := "写入工具清单失败：" + err.Error()
		_ = h.Repo.MarkConnectFailed(server.ID, reason)
		log.Printf("mcp connect failed: id=%d step=store err=%v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	log.Printf("mcp connect ok: id=%d name=%s tools=%d user=%s", id, server.Name, len(tools), user.Username)
	httpx.JSON(w, http.StatusOK, map[string]any{"connected": true, "tools": tools})
}

// Disconnect 处理 POST /api/mcp/servers/{id}/disconnect：清空清单并置为未连接。
//
// @Summary      断开 MCP 服务器
// @Description  仅教师。清空该服务器的工具清单并置为未连接；重复断开幂等成功。断开后引用其工具的调用会被记为被拒。
// @Tags         mcp
// @Produce      json
// @Security     bearerAuth
// @Param        id path int true "服务器编号"
// @Success      200 {object} map[string]interface{}
// @Failure      401 {object} httpx.ErrorBody
// @Failure      403 {object} httpx.ErrorBody
// @Failure      404 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/mcp/servers/{id}/disconnect [post]
// @ID           MCP-006
func (h *Handler) Disconnect(w http.ResponseWriter, r *http.Request) {
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
	server, err := h.Repo.GetServer(user.ClassID, id)
	if err != nil {
		writeRepoError(w, "mcp servers disconnect", err)
		return
	}
	if err := h.Repo.MarkDisconnected(server.ID); err != nil {
		log.Printf("mcp disconnect: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	log.Printf("mcp disconnect ok: id=%d user=%s", id, user.Username)
	httpx.JSON(w, http.StatusOK, map[string]any{"connected": false})
}

// Tools 处理 GET /api/mcp/servers/{id}/tools：返回最近一次发现清单。
//
// @Summary      查看已发现的工具
// @Description  仅教师。返回最近一次连接写入的工具清单（含名称、说明与输入结构）；未连接时可能为空。
// @Tags         mcp
// @Produce      json
// @Security     bearerAuth
// @Param        id path int true "服务器编号"
// @Success      200 {object} map[string]interface{}
// @Failure      401 {object} httpx.ErrorBody
// @Failure      403 {object} httpx.ErrorBody
// @Failure      404 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/mcp/servers/{id}/tools [get]
// @ID           MCP-007
func (h *Handler) Tools(w http.ResponseWriter, r *http.Request) {
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
	server, err := h.Repo.GetServer(user.ClassID, id)
	if err != nil {
		writeRepoError(w, "mcp servers tools", err)
		return
	}
	tools, err := h.Repo.ListTools(server.ID)
	if err != nil {
		log.Printf("mcp tools list: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"tools": tools})
}

// Calls 处理 GET /api/mcp/tool-calls：本班最近的调用记录。
//
// @Summary      查看工具调用记录
// @Description  仅教师。返回本班最近的工具调用记录，按时间倒序，默认最多 50 条；含工具引用、服务器名、状态、用户、会话与轮次。
// @Tags         mcp
// @Produce      json
// @Security     bearerAuth
// @Success      200 {object} map[string]interface{}
// @Failure      401 {object} httpx.ErrorBody
// @Failure      403 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/mcp/tool-calls [get]
// @ID           MCP-008
func (h *Handler) Calls(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	calls, err := h.Repo.ListCalls(user.ClassID, limit)
	if err != nil {
		log.Printf("mcp calls list: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"calls": calls})
}

func writeRepoError(w http.ResponseWriter, scope string, err error) {
	switch {
	case errors.Is(err, ErrDuplicateName), errors.Is(err, ErrBadName), errors.Is(err, ErrBadBaseURL):
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrNotFound):
		httpx.NotFound(w)
	default:
		log.Printf("%s: %v", scope, err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
	}
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
