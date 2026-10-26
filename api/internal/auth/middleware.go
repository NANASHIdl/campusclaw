package auth

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"campusclaw/api/internal/httpx"
	"campusclaw/api/internal/jwtauth"
)

// userCtxKey 与 claimsCtxKey 必须是不同类型：若共用一个空结构体类型做 key，
// 两个零值实例作为 map key 相等，第二次 WithValue 会覆盖第一次的值
// （曾导致 ctx.Value(userKey) 拿到 Claims，类型断言失败后所有接口都 401）。
type userCtxKey struct{}
type claimsCtxKey struct{}

var (
	userKey   userCtxKey
	claimsKey claimsCtxKey
)

// UserFromContext 取出认证中间件注入的用户对象。
func UserFromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userKey).(User)
	return u, ok
}

// ClaimsFromContext 取出本次请求的令牌声明（登出需要 jti 与 exp）。
func ClaimsFromContext(ctx context.Context) (jwtauth.Claims, bool) {
	c, ok := ctx.Value(claimsKey).(jwtauth.Claims)
	return c, ok
}

// Handler 组装认证相关的 HTTP 处理器。
type Handler struct {
	Users       *UserRepo
	Tokens      *jwtauth.Manager
	Revocations *jwtauth.Revocations
	Limiter     *Limiter
}

func NewHandler(users *UserRepo, tokens *jwtauth.Manager, revocations *jwtauth.Revocations, limiter *Limiter) *Handler {
	return &Handler{Users: users, Tokens: tokens, Revocations: revocations, Limiter: limiter}
}

// bearerToken 从 Authorization 头取令牌。
//
// 只接受「Bearer <token>」这一种方案；Cookie 不再作为登录凭据。
func bearerToken(r *http.Request) string {
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if raw == "" {
		return ""
	}
	const prefix = "bearer "
	if len(raw) <= len(prefix) || !strings.EqualFold(raw[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(raw[len(prefix):])
}

// RequireAuth 是认证中间件：无有效令牌即 401，通过后注入用户与令牌声明。
//
// 校验顺序：签名与算法 → 过期 → 是否已吊销 → 用户是否仍存在。
// 任一失败都返回同一个 401，不区分原因。
func (h *Handler) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
			return
		}

		claims, err := h.Tokens.Verify(token)
		if err != nil {
			if errors.Is(err, jwtauth.ErrExpired) {
				log.Printf("auth: reason=expired")
			} else {
				log.Printf("auth: reason=bad_token err=%v", err)
			}
			httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
			return
		}

		revoked, err := h.Revocations.IsRevoked(claims.Jti)
		if err != nil {
			log.Printf("auth: 查询吊销失败: %v", err)
			httpx.Error(w, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		if revoked {
			log.Printf("auth: reason=revoked jti=%s", claims.Jti)
			httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
			return
		}

		userID, err := strconv.ParseInt(claims.Sub, 10, 64)
		if err != nil {
			httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
			return
		}
		user, err := h.Users.FindByID(userID)
		if err != nil {
			log.Printf("auth: reason=user_gone sub=%s err=%v", claims.Sub, err)
			httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), userKey, user)
		ctx = context.WithValue(ctx, claimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireTeacher 在认证之后再做角色校验；学生调用返回 403。
//
// 角色来自数据库中的用户行，不采信令牌或请求头里声明的角色。
func (h *Handler) RequireTeacher(next http.Handler) http.Handler {
	return h.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFromContext(r.Context())
		if !ok {
			httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
			return
		}
		if !user.IsTeacher() {
			log.Printf("auth: reason=role_forbidden user=%s role=%s path=%s", user.Username, user.Role, r.URL.Path)
			httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
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
