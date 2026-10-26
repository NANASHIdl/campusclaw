// Package jwtauth 提供 HS256 JWT 的签发、校验与吊销。
//
// 设计要点：
//   - 算法固定 HS256，校验时拒绝其他 alg（含 none）——JWT 最经典的漏洞入口；
//   - 载荷只放注册声明，角色与班级一律按 sub 回查数据库；
//   - 吊销按 jti 记录，登出与再次登录都按它作废旧令牌。
package jwtauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 对外错误。
var (
	ErrInvalidToken = errors.New("invalid token")
	ErrExpired      = errors.New("token expired")
)

const (
	algName   = "HS256"
	jtiBytes  = 16
	clockSkew = 0
)

var b64 = base64.RawURLEncoding

// Claims 是令牌载荷：只有注册声明。
type Claims struct {
	Sub string `json:"sub"` // 用户 ID 的十进制字符串
	Jti string `json:"jti"` // 令牌标识，吊销按它记录
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

// Manager 负责签发与校验。
type Manager struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

// New 构造 Manager。secret 必须非空（由调用方从环境变量注入）。
func New(secret string, ttl time.Duration) *Manager {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &Manager{secret: []byte(secret), ttl: ttl, now: time.Now}
}

// TTL 返回令牌有效期。
func (m *Manager) TTL() time.Duration { return m.ttl }

type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// Issue 为指定用户签发令牌，同时返回解析后的声明（调用方需要 jti 与 exp）。
func (m *Manager) Issue(userID int64) (string, Claims, error) {
	jtiRaw := make([]byte, jtiBytes)
	if _, err := rand.Read(jtiRaw); err != nil {
		return "", Claims{}, fmt.Errorf("生成令牌标识失败: %w", err)
	}

	now := m.now()
	claims := Claims{
		Sub: fmt.Sprintf("%d", userID),
		Jti: hex.EncodeToString(jtiRaw),
		Iat: now.Unix(),
		Exp: now.Add(m.ttl).Unix(),
	}

	headerJSON, err := json.Marshal(header{Alg: algName, Typ: "JWT"})
	if err != nil {
		return "", Claims{}, err
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", Claims{}, err
	}

	signingInput := b64.EncodeToString(headerJSON) + "." + b64.EncodeToString(payloadJSON)
	return signingInput + "." + b64.EncodeToString(m.sign(signingInput)), claims, nil
}

func (m *Manager) sign(input string) []byte {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(input))
	return mac.Sum(nil)
}

// Verify 校验令牌并返回声明。任何异常都折叠成 ErrInvalidToken / ErrExpired。
func (m *Manager) Verify(token string) (Claims, error) {
	token = strings.TrimSpace(token)
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return Claims{}, ErrInvalidToken
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrInvalidToken
	}

	headerJSON, err := b64.DecodeString(parts[0])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	var h header
	if err := json.Unmarshal(headerJSON, &h); err != nil {
		return Claims{}, ErrInvalidToken
	}
	// 只接受 HS256；显式拒绝 none 与 RS256 等。
	if h.Alg != algName {
		return Claims{}, ErrInvalidToken
	}

	sig, err := b64.DecodeString(parts[2])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	if !hmac.Equal(sig, m.sign(parts[0]+"."+parts[1])) {
		return Claims{}, ErrInvalidToken
	}

	payloadJSON, err := b64.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	var claims Claims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return Claims{}, ErrInvalidToken
	}
	if claims.Sub == "" || claims.Jti == "" || claims.Exp == 0 {
		return Claims{}, ErrInvalidToken
	}
	if m.now().Add(clockSkew).Unix() >= claims.Exp {
		return Claims{}, ErrExpired
	}
	return claims, nil
}

// --- 吊销表 -----------------------------------------------------------------

// Revocations 负责 revoked_tokens 表。
type Revocations struct{ DB *sql.DB }

// Revoke 记录一个作废的 jti。幂等：重复写入不报错。
func (r *Revocations) Revoke(jti string, userID int64, exp time.Time) error {
	if jti == "" {
		return nil
	}
	_, err := r.DB.Exec(
		`INSERT INTO revoked_tokens (jti, user_id, expires_at) VALUES (?, ?, ?)
		 ON DUPLICATE KEY UPDATE revoked_at = CURRENT_TIMESTAMP`,
		jti, userID, exp,
	)
	if err != nil {
		return fmt.Errorf("写入吊销记录失败: %w", err)
	}
	return nil
}

// IsRevoked 判断 jti 是否已被吊销。
func (r *Revocations) IsRevoked(jti string) (bool, error) {
	var one int
	err := r.DB.QueryRow(`SELECT 1 FROM revoked_tokens WHERE jti = ?`, jti).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("查询吊销记录失败: %w", err)
	}
	return true, nil
}

// Purge 清理已过期令牌的吊销记录（令牌本身过期后，记录没有意义）。
func (r *Revocations) Purge() error {
	_, err := r.DB.Exec(`DELETE FROM revoked_tokens WHERE expires_at <= NOW()`)
	return err
}
