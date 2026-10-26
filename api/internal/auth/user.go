package auth

import (
	"database/sql"
	"errors"
	"fmt"
)

// User 是认证模块解析出的身份对象；下游模块只使用它，不再解析 Cookie。
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`     // teacher | student
	ClassID  int64  `json:"class_id"` // 会话班级，租户列取值来源
}

// IsTeacher 供上传接口做角色校验。
func (u User) IsTeacher() bool { return u.Role == "teacher" }

// ErrUserNotFound 表示用户名不存在。
var ErrUserNotFound = errors.New("user not found")

// UserRepo 负责 users 表读取。
type UserRepo struct{ DB *sql.DB }

// Credentials 是登录时需要的用户行（含口令哈希，绝不外传）。
type Credentials struct {
	User
	PasswordHash string
}

// FindByID 按主键取用户；不存在返回 ErrUserNotFound。
// 令牌里只有 sub，角色与班级都由这一次查询给出。
func (r *UserRepo) FindByID(id int64) (User, error) {
	var u User
	err := r.DB.QueryRow(
		`SELECT id, username, role, class_id FROM users WHERE id = ?`,
		id,
	).Scan(&u.ID, &u.Username, &u.Role, &u.ClassID)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("查询用户失败: %w", err)
	}
	return u, nil
}

// FindByUsername 按用户名取用户；不存在返回 ErrUserNotFound。
func (r *UserRepo) FindByUsername(username string) (Credentials, error) {
	var c Credentials
	err := r.DB.QueryRow(
		`SELECT id, username, role, class_id, password_hash FROM users WHERE username = ?`,
		username,
	).Scan(&c.ID, &c.Username, &c.Role, &c.ClassID, &c.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return Credentials{}, ErrUserNotFound
	}
	if err != nil {
		return Credentials{}, fmt.Errorf("查询用户失败: %w", err)
	}
	return c, nil
}
