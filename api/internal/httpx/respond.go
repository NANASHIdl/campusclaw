// Package httpx 提供统一的 JSON 响应与错误文案。
//
// 安全约定：错误响应体只含稳定的机器可读 code，不含任何业务数据，
// 也不因内部真实原因而不同——同构响应依赖这里的固定文案。
package httpx

import (
	"encoding/json"
	"log"
	"net/http"
)

// 对外统一的错误码。客户端无法据此区分内部真实原因。
const (
	CodeInvalidCredentials = "invalid_credentials" // 登录失败（账号不存在 / 口令错误 / 限流）
	CodeUnauthorized       = "unauthorized"        // 未认证
	CodeForbidden          = "forbidden"           // 角色不足
	CodeNotFound           = "not_found"           // 记录不存在或跨班级（同构）
	CodeBadRequest         = "bad_request"         // 请求不合法
)

type errorBody struct {
	Error string `json:"error"`
}

// JSON 写出 JSON 响应。
func JSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("write json response: %v", err)
	}
}

// Error 写出统一结构的错误响应。
func Error(w http.ResponseWriter, status int, code string) {
	JSON(w, status, errorBody{Error: code})
}

// NotFound 返回同构 404：跨班访问与记录不存在使用完全相同的响应。
func NotFound(w http.ResponseWriter) {
	Error(w, http.StatusNotFound, CodeNotFound)
}

// DecodeJSON 解析请求体 JSON；失败返回 false 并已写出响应。
//
// 刻意不启用 DisallowUnknownFields：客户端在正文中自行加入的 role / class_id
// 等字段应当被忽略，而不是让请求失败——身份一律以服务端会话为准。
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		Error(w, http.StatusBadRequest, CodeBadRequest)
		return false
	}
	return true
}
