package agent

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"campusclaw/api/internal/auth"
	"campusclaw/api/internal/httpx"
)

// Handler 暴露智能体接口。
type Handler struct{ Repo *Repo }

func NewHandler(r *Repo) *Handler { return &Handler{Repo: r} }

// WriteRequest 是新建/修改智能体的请求体，供 OpenAPI 文档引用。
type WriteRequest struct {
	Name        string  `json:"name"`
	Prompt      string  `json:"prompt"`
	Enabled     bool    `json:"enabled"`
	SkillIDs    []int64 `json:"skill_ids"`
	MaterialIDs []int64 `json:"material_ids"`
}

// List 处理 GET /api/agents：教师见全部，学生只见已启用。
//
// @Summary      列出本班智能体
// @Description  教师返回本班全部智能体；学生只返回已启用的。响应不含 class_id。
// @Tags         agents
// @Produce      json
// @Security     bearerAuth
// @Success      200 {object} map[string]interface{}
// @Failure      401 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/agents [get]
// @ID           AGT-001
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}
	list, err := h.Repo.List(user.ClassID, !user.IsTeacher())
	if err != nil {
		log.Printf("agents list: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"agents": list})
}

// Create 处理 POST /api/agents（仅教师）。
//
// @Summary      新建智能体
// @Description  仅教师。名称去空白后 1–64 字符且班内唯一；提示词不超过 2000 字符且允许为空（为空时回退默认角色）。skill_ids 与 material_ids 必须属于本班，否则 404。同班同名返回 400。
// @Tags         agents
// @Accept       json
// @Produce      json
// @Security     bearerAuth
// @Param        body body agent.WriteRequest true "智能体字段与绑定"
// @Success      201 {object} agent.Agent
// @Failure      400 {object} httpx.ErrorBody
// @Failure      401 {object} httpx.ErrorBody
// @Failure      403 {object} httpx.ErrorBody
// @Failure      404 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/agents [post]
// @ID           AGT-002
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
	in := Input{Name: req.Name, Prompt: req.Prompt, Enabled: req.Enabled,
		SkillIDs: req.SkillIDs, MaterialIDs: req.MaterialIDs}
	if err := in.Normalize(); err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	id, err := h.Repo.Create(user.ClassID, in)
	if err != nil {
		writeRepoError(w, "agents create", err)
		return
	}
	created, err := h.Repo.Get(user.ClassID, id)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	log.Printf("agents create ok: id=%d name=%q user=%s", id, in.Name, user.Username)
	httpx.JSON(w, http.StatusCreated, created)
}

// Get 处理 GET /api/agents/{id}。学生读未启用/他班/不存在一律 404。
//
// @Summary      读取一个智能体
// @Description  教师可读本班未启用的智能体；学生读到未启用、他班或不存在时都是同一个 404。
// @Tags         agents
// @Produce      json
// @Security     bearerAuth
// @Param        id path int true "智能体编号"
// @Success      200 {object} agent.Agent
// @Failure      401 {object} httpx.ErrorBody
// @Failure      404 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/agents/{id} [get]
// @ID           AGT-003
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
	a, err := h.Repo.Get(user.ClassID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.NotFound(w)
			return
		}
		log.Printf("agents get: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if !user.IsTeacher() && !a.Enabled {
		// 学生对未启用智能体视同不存在。
		httpx.NotFound(w)
		return
	}
	httpx.JSON(w, http.StatusOK, a)
}

// Update 处理 PUT /api/agents/{id}（仅教师）。
//
// @Summary      修改智能体
// @Description  仅教师。整份覆盖字段与绑定；提示词允许为空。跨班或不存在返回 404，与他人同名返回 400。
// @Tags         agents
// @Accept       json
// @Produce      json
// @Security     bearerAuth
// @Param        id   path int               true "智能体编号"
// @Param        body body agent.WriteRequest true "智能体字段与绑定"
// @Success      200 {object} agent.Agent
// @Failure      400 {object} httpx.ErrorBody
// @Failure      401 {object} httpx.ErrorBody
// @Failure      403 {object} httpx.ErrorBody
// @Failure      404 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/agents/{id} [put]
// @ID           AGT-004
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
	in := Input{Name: req.Name, Prompt: req.Prompt, Enabled: req.Enabled,
		SkillIDs: req.SkillIDs, MaterialIDs: req.MaterialIDs}
	if err := in.Normalize(); err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if err := h.Repo.Update(user.ClassID, id, in); err != nil {
		writeRepoError(w, "agents update", err)
		return
	}
	updated, err := h.Repo.Get(user.ClassID, id)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	log.Printf("agents update ok: id=%d user=%s", id, user.Username)
	httpx.JSON(w, http.StatusOK, updated)
}

func writeRepoError(w http.ResponseWriter, scope string, err error) {
	switch {
	case errors.Is(err, ErrDuplicateName), errors.Is(err, ErrBadName), errors.Is(err, ErrPromptTooLong):
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrBindingNotFound), errors.Is(err, ErrNotFound):
		// 绑定他班资源与资源不存在对外一致。
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
