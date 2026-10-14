package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

const (
	hashScheme      = "pbkdf2_sha256"
	pbkdf2Iters     = 120_000
	pbkdf2KeyLen    = 32
	pbkdf2SaltLen   = 16
	encodedSegments = 4
)

// HashPassword 生成 pbkdf2_sha256$<iter>$<salt_b64>$<hash_b64> 形式的哈希。
func HashPassword(password string) (string, error) {
	salt := make([]byte, pbkdf2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("生成盐失败: %w", err)
	}
	dk, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iters, pbkdf2KeyLen)
	if err != nil {
		return "", fmt.Errorf("派生密钥失败: %w", err)
	}
	return fmt.Sprintf("%s$%d$%s$%s",
		hashScheme,
		pbkdf2Iters,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(dk),
	), nil
}

// VerifyPassword 以常量时间比较校验口令。
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != encodedSegments || parts[0] != hashScheme {
		return false
	}
	iters, err := strconv.Atoi(parts[1])
	if err != nil || iters <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iters, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash 用于「账号不存在」时仍执行一次等价的口令比对，
// 使失败响应耗时与「口令错误」接近，避免以耗时区分账号是否存在。
var dummyHash = mustHash("campusclaw-dummy-password-for-constant-time")

// VerifyDummy 在校验目标不存在时调用，消耗与真实校验相当的时间。
func VerifyDummy(password string) {
	_ = VerifyPassword(dummyHash, password)
}

func mustHash(pw string) string {
	h, err := HashPassword(pw)
	if err != nil {
		panic(err)
	}
	return h
}
