package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrNoSession 表示 Cookie 缺失或会话不存在/已过期。
var ErrNoSession = errors.New("no valid session")

const sessionTokenBytes = 32

// NewSessionToken 生成随机会话标识（下发给浏览器，仅此一处出现明文）。
func NewSessionToken() (string, error) {
	buf := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成会话标识失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// SessionDigest 计算入库的摘要值：HMAC-SHA256(token, secret)。
// 数据库泄露也无法直接得到可用标识；secret 作为 pepper 只存在于环境变量。
func SessionDigest(token, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

// SessionRepo 负责 sessions 表的读写。
type SessionRepo struct {
	DB     *sql.DB
	Secret string
	TTL    time.Duration
}

// Create 写入一条新会话记录，返回下发给浏览器的标识明文。
func (r *SessionRepo) Create(userID int64) (string, error) {
	token, err := NewSessionToken()
	if err != nil {
		return "", err
	}
	_, err = r.DB.Exec(
		`INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)`,
		SessionDigest(token, r.Secret), userID, time.Now().Add(r.TTL),
	)
	if err != nil {
		return "", fmt.Errorf("写入会话失败: %w", err)
	}
	return token, nil
}

// Lookup 由 Cookie 标识解析出用户；会话不存在或过期返回 ErrNoSession。
// 角色与班级每次都从 users 表读取，不缓存在 Cookie 中。
func (r *SessionRepo) Lookup(token string) (User, error) {
	var u User
	err := r.DB.QueryRow(
		`SELECT u.id, u.username, u.role, u.class_id
		   FROM sessions s
		   JOIN users u ON u.id = s.user_id
		  WHERE s.id = ? AND s.expires_at > NOW()`,
		SessionDigest(token, r.Secret),
	).Scan(&u.ID, &u.Username, &u.Role, &u.ClassID)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNoSession
	}
	if err != nil {
		return User{}, fmt.Errorf("查询会话失败: %w", err)
	}
	return u, nil
}

// Delete 删除指定会话（登出）。幂等：记录不存在也算成功。
func (r *SessionRepo) Delete(token string) error {
	_, err := r.DB.Exec(`DELETE FROM sessions WHERE id = ?`, SessionDigest(token, r.Secret))
	return err
}

// PurgeExpired 清理过期会话（启动时调用一次）。
func (r *SessionRepo) PurgeExpired() error {
	_, err := r.DB.Exec(`DELETE FROM sessions WHERE expires_at <= NOW()`)
	return err
}
