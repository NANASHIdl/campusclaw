package skill

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"campusclaw/api/internal/auth"
	"campusclaw/api/internal/httpx"
)

// Handler 暴露技能接口。写操作的路由已由 RequireTeacher 保护。
type Handler struct{ Repo *Repo }

func NewHandler(r *Repo) *Handler { return &Handler{Repo: r} }

// SourceRequest 是用技能文件新建/改写技能的请求体，供 OpenAPI 文档引用。
type SourceRequest struct {
	Source string `json:"source"`
}

// List 处理 GET /api/skills。
//
// @Summary      列出本班技能
// @Description  教师与学生都可读。没有技能时 skills 为空数组。
// @Tags         skills
// @Produce      json
// @Security     bearerAuth
// @Success      200 {object} map[string]interface{}
// @Failure      401 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/skills [get]
// @ID           SKL-001
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}
	list, err := h.Repo.List(user.ClassID)
	if err != nil {
		log.Printf("skills list: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"skills": list})
}

// Get 处理 GET /api/skills/{id}。
//
// @Summary      读取一条技能
// @Description  返回结构化字段与文件原文，便于取回再编辑。
// @Tags         skills
// @Produce      json
// @Security     bearerAuth
// @Param        id path int true "技能编号"
// @Success      200 {object} skill.Skill
// @Failure      401 {object} httpx.ErrorBody
// @Failure      404 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/skills/{id} [get]
// @ID           SKL-002
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
	s, err := h.Repo.Get(user.ClassID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.NotFound(w)
			return
		}
		log.Printf("skills get: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	httpx.JSON(w, http.StatusOK, s)
}

// Create 处理 POST /api/skills：用技能文件新建。
//
// @Summary      用技能文件新建技能
// @Description  仅教师。source 为技能文件原文：空行以上写名称、适用条件、资料、工具、启用，空行以下写说明。名称 ≤32、适用条件 ≤120、说明 ≤2000、整份文件 ≤8000；工具只能是 knowledge_search。同班同名返回 400。
// @Tags         skills
// @Accept       json
// @Produce      json
// @Security     bearerAuth
// @Param        body body skill.SourceRequest true "技能文件原文"
// @Success      201 {object} skill.Skill
// @Failure      400 {object} httpx.ErrorBody
// @Failure      401 {object} httpx.ErrorBody
// @Failure      403 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/skills [post]
// @ID           SKL-003
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}
	var req SourceRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	p, err := Parse(req.Source)
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	id, err := h.Repo.Create(user.ClassID, p)
	if err != nil {
		if errors.Is(err, ErrDuplicateName) {
			httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		log.Printf("skills create: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	created, err := h.Repo.Get(user.ClassID, id)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	log.Printf("skills create ok: id=%d name=%q user=%s", id, p.Name, user.Username)
	httpx.JSON(w, http.StatusCreated, created)
}

// Update 处理 PUT /api/skills/{id}：用技能文件改写。
//
// @Summary      用技能文件改写技能
// @Description  仅教师。文件格式与新建相同。跨班或不存在返回 404；与另一条技能同名返回 400。
// @Tags         skills
// @Accept       json
// @Produce      json
// @Security     bearerAuth
// @Param        id   path int                true "技能编号"
// @Param        body body skill.SourceRequest true "技能文件原文"
// @Success      200 {object} skill.Skill
// @Failure      400 {object} httpx.ErrorBody
// @Failure      401 {object} httpx.ErrorBody
// @Failure      403 {object} httpx.ErrorBody
// @Failure      404 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/skills/{id} [put]
// @ID           SKL-004
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
	var req SourceRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	p, err := Parse(req.Source)
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := h.Repo.Update(user.ClassID, id, p); err != nil {
		switch {
		case errors.Is(err, ErrDuplicateName):
			httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, ErrNotFound):
			httpx.NotFound(w)
		default:
			log.Printf("skills update: %v", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error")
		}
		return
	}
	updated, err := h.Repo.Get(user.ClassID, id)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	log.Printf("skills update ok: id=%d user=%s", id, user.Username)
	httpx.JSON(w, http.StatusOK, updated)
}

// Delete 处理 DELETE /api/skills/{id}：成功返回 204 且无响应体。
//
// @Summary      删除技能
// @Description  仅教师。成功返回 204 且无响应体；对同一编号再次删除返回 404。删除会同时清除智能体上的绑定。
// @Tags         skills
// @Security     bearerAuth
// @Param        id path int true "技能编号"
// @Success      204 "无响应体"
// @Failure      401 {object} httpx.ErrorBody
// @Failure      403 {object} httpx.ErrorBody
// @Failure      404 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/skills/{id} [delete]
// @ID           SKL-005
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
	if err := h.Repo.Delete(user.ClassID, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.NotFound(w)
			return
		}
		log.Printf("skills delete: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	log.Printf("skills delete ok: id=%d user=%s", id, user.Username)
	w.WriteHeader(http.StatusNoContent)
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
