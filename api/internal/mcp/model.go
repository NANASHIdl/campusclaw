// Package mcp 负责宿主侧的 MCP 工具接入：登记服务器、连接与发现、引用校验、
// 执行工具调用，以及留下调用记录。
//
// 安全边界：
//   - 令牌只存服务端，Server 结构体刻意不含 auth_token 字段，序列化时无从泄露；
//   - 所有查询都带 class_id，跨班一律按「不存在」处理；
//   - 工具返回与工具说明都按不可信文本对待，清洗在 chat 包完成。
package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 对外错误。
var (
	ErrNotFound      = errors.New("MCP 服务器不存在")
	ErrDuplicateName = errors.New("本班已有同名 MCP 服务器")
	ErrBadName       = errors.New("名称需为 1 至 32 个字符，可含中文")
	ErrBadBaseURL    = errors.New("地址需以 http:// 或 https:// 开头")
	ErrNotConnected  = errors.New("该服务器当前未连接")
	ErrToolNotFound  = errors.New("该工具不在服务器的发现清单内")
)

// 调用状态：成功 / 被拒 / 出错。
const (
	StatusOK     = "ok"
	StatusDenied = "denied"
	StatusError  = "error"
)

// Server 是一台已登记的 MCP 服务器。
//
// 注意：这里**没有** AuthToken 字段——响应结构体不带令牌，就不存在手滑返回的可能。
type Server struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	Connected bool   `json:"connected"`
	HasAuth   bool   `json:"has_auth"`
	ToolCount int    `json:"tool_count"`
	LastError string `json:"last_error"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Tool 是发现结果里的一个工具。
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// CallRecord 是一次工具调用的记录，回答五个问题：
// 调了什么（ToolRef/ServerName/ToolName）、成了没有（Status/Detail）、
// 谁触发的（UserID）、在哪次对话的第几轮（ConversationID/RoundNo）、什么时候（CreatedAt）。
type CallRecord struct {
	// ClassID 与 ServerID 只用于写库，不出现在接口响应里。
	ClassID  int64 `json:"-"`
	ServerID int64 `json:"-"`

	ID             int64  `json:"id"`
	ToolRef        string `json:"tool_ref"`
	ServerName     string `json:"server_name"`
	ToolName       string `json:"tool_name"`
	Status         string `json:"status"`
	UserID         int64  `json:"user_id"`
	ConversationID int64  `json:"conversation_id"`
	RoundNo        int    `json:"round_no"`
	Detail         string `json:"detail"`
	CreatedAt      string `json:"created_at"`
}

// Input 是登记/修改服务器的入参。
type Input struct {
	Name      string
	BaseURL   string
	AuthToken string
	// KeepToken 为真时表示「令牌留空，不修改」。
	KeepToken bool
}

// Normalize 校验并规范化入参。
func (in *Input) Normalize() error {
	in.Name = strings.TrimSpace(in.Name)
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	in.AuthToken = strings.TrimSpace(in.AuthToken)

	if in.Name == "" || len([]rune(in.Name)) > 32 {
		return ErrBadName
	}
	if !strings.HasPrefix(in.BaseURL, "http://") && !strings.HasPrefix(in.BaseURL, "https://") {
		return ErrBadBaseURL
	}
	in.BaseURL = strings.TrimRight(in.BaseURL, "/")
	return nil
}

// CallSummary 截断工具结果，用于记录与 meta 里的摘要。
func CallSummary(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 {
		limit = 200
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

// FormatTime 统一时间格式，与既有接口保持一致。
func FormatTime(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}

// WrapConnectError 给连接失败的原因加上「地址还是令牌」的提示。
func WrapConnectError(baseURL string, err error) error {
	return fmt.Errorf("连接 %s 失败：%w", baseURL, err)
}
