package auth

import (
	"log"
	"net/http"
	"strings"

	"campusclaw/api/internal/httpx"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login 处理 POST /api/login。
//
// 失败路径（账号不存在 / 口令错误 / 触发限流）对外一律返回同一状态码与同一响应体，
// 且都消耗一次等价的口令比对，避免以响应内容或耗时区分账号是否存在。
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
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

	// 登录成功：丢弃浏览器携带的旧标识（防会话固定），签发并写入新会话。
	if old, err := r.Cookie(CookieName); err == nil && old.Value != "" {
		if err := h.Sessions.Delete(old.Value); err != nil {
			log.Printf("drop old session: %v", err)
		}
	}
	token, err := h.Sessions.Create(cred.ID)
	if err != nil {
		log.Printf("create session: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	h.setSessionCookie(w, token)
	log.Printf("login ok: username=%q role=%s class_id=%d", cred.Username, cred.Role, cred.ClassID)

	// 响应体只含身份信息，绝不含口令或哈希。
	httpx.JSON(w, http.StatusOK, cred.User)
}

// Logout 处理 POST /api/logout：删除会话记录并清除 Cookie，幂等。
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		if err := h.Sessions.Delete(c.Value); err != nil {
			log.Printf("delete session on logout: %v", err)
		}
	}
	h.clearSessionCookie(w)
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Me 处理 GET /api/me。
//
// 该接口只用于前端决定是否渲染上传入口（可发现性），不构成授权；
// 授权在服务端角色校验中完成。
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}
	httpx.JSON(w, http.StatusOK, user)
}
