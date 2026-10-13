package auth

import (
	"context"
	"net/http"
	"strings"

	"campusclaw/api/internal/httpx"
)

// CookieName 是承载随机会话标识的 Cookie 名。
const CookieName = "cc_session"

type ctxKey struct{}

var userKey ctxKey

// UserFromContext 取出认证中间件注入的用户对象。
func UserFromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userKey).(User)
	return u, ok
}

// Handler 组装认证相关的 HTTP 处理器。
type Handler struct {
	Users        *UserRepo
	Sessions     *SessionRepo
	Limiter      *Limiter
	CookieSecure bool
}

func NewHandler(users *UserRepo, sessions *SessionRepo, limiter *Limiter) *Handler {
	return &Handler{Users: users, Sessions: sessions, Limiter: limiter}
}

// RequireAuth 是认证中间件：无有效会话即 401，认证通过才把 User 注入上下文。
//
// 所有受保护接口都必须先经过它；后续模块只从上下文取用户，不再解析 Cookie，
// 也不接受请求中自行声明的角色与班级。
func (h *Handler) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err != nil || strings.TrimSpace(c.Value) == "" {
			httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
			return
		}
		user, err := h.Sessions.Lookup(c.Value)
		if err != nil {
			httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, user)))
	})
}

func (h *Handler) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.CookieSecure,
		MaxAge:   int(h.Sessions.TTL.Seconds()),
	})
}

func (h *Handler) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.CookieSecure,
		MaxAge:   -1,
	})
}

// clientIP 取客户端地址（优先反向代理写入的 X-Forwarded-For 首段）。
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first, _, ok := strings.Cut(xff, ","); ok {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := splitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func splitHostPort(addr string) (string, string, error) {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[:i], addr[i+1:], nil
	}
	return addr, "", nil
}

// loginKey 构造限流键：用户名 + 来源 IP。
func loginKey(username, ip string) string {
	return strings.ToLower(username) + "|" + ip
}
