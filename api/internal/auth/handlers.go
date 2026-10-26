package auth

import (
	"log"
	"net/http"
	"strings"
	"time"

	"campusclaw/api/internal/httpx"
)

// LoginRequest 是登录请求体，供 OpenAPI 文档引用。
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse 是登录成功的响应体，供 OpenAPI 文档引用。
type LoginResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

// Login 处理 POST /api/login：校验口令并签发 JWT 访问令牌。
//
// 失败路径（账号不存在 / 口令错误 / 触发限流）对外一律返回同一状态码与同一响应体，
// 且都消耗一次等价的口令比对，避免以响应内容或耗时区分账号是否存在。
//
// @Summary      登录并签发访问令牌
// @Description  校验用户名与口令。账号不存在、口令错误与触发限流返回完全相同的状态码与响应体。成功后返回 HS256 JWT，其余接口以 `Authorization: Bearer <token>` 访问。
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body body auth.LoginRequest true "用户名与口令"
// @Success      200 {object} auth.LoginResponse
// @Failure      400 {object} httpx.ErrorBody
// @Failure      401 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/login [post]
// @ID           AUTH-001
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	username := strings.TrimSpace(req.Username)
	ip := clientIP(r)

	if !h.Limiter.Allow(loginKey(username, ip)) {
		log.Printf("login rejected: reason=rate_limited username=%q ip=%s", username, ip)
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeInvalidCredentials)
		return
	}

	cred, err := h.Users.FindByUsername(username)
	if err != nil {
		// 账号不存在时仍执行一次等价比对，使失败耗时与「口令错误」接近。
		VerifyDummy(req.Password)
		log.Printf("login rejected: reason=user_not_found username=%q ip=%s", username, ip)
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeInvalidCredentials)
		return
	}

	if !VerifyPassword(cred.PasswordHash, req.Password) {
		log.Printf("login rejected: reason=bad_password username=%q ip=%s", username, ip)
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeInvalidCredentials)
		return
	}

	// 携带未过期的旧令牌再次登录：先吊销旧 jti，再签发新令牌。
	if old := bearerToken(r); old != "" {
		if oldClaims, err := h.Tokens.Verify(old); err == nil {
			if err := h.Revocations.Revoke(oldClaims.Jti, cred.ID, time.Unix(oldClaims.Exp, 0)); err != nil {
				log.Printf("revoke old token: %v", err)
			}
		}
	}

	token, _, err := h.Tokens.Issue(cred.ID)
	if err != nil {
		log.Printf("issue token: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	log.Printf("login ok: username=%q role=%s class_id=%d", cred.Username, cred.Role, cred.ClassID)

	// 响应体只含令牌与身份信息，绝不含口令或哈希。
	httpx.JSON(w, http.StatusOK, LoginResponse{Token: token, User: cred.User})
}

// Logout 处理 POST /api/logout：把当前令牌的 jti 写入吊销表。
//
// @Summary      登出并吊销当前令牌
// @Description  把当前令牌的 jti 写入吊销表。同一令牌再次调用会在鉴权阶段得到 401。
// @Tags         auth
// @Produce      json
// @Security     bearerAuth
// @Success      200 {object} map[string]interface{}
// @Failure      401 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/logout [post]
// @ID           AUTH-002
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	claims, ok := ClaimsFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}
	user, _ := UserFromContext(r.Context())
	if err := h.Revocations.Revoke(claims.Jti, user.ID, time.Unix(claims.Exp, 0)); err != nil {
		log.Printf("revoke on logout: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Me 处理 GET /api/me。
//
// 该接口只用于前端决定是否渲染写入口（可发现性），不构成授权；
// 授权在服务端角色校验中完成。
//
// @Summary      读取当前登录用户
// @Description  返回令牌 sub 对应的角色与班级，供界面决定是否显示教师入口。授权仍以服务端角色校验为准。
// @Tags         auth
// @Produce      json
// @Security     bearerAuth
// @Success      200 {object} auth.User
// @Failure      401 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/me [get]
// @ID           AUTH-003
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}
	httpx.JSON(w, http.StatusOK, user)
}
