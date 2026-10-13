package auth

import (
	"strings"
	"testing"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("teach123")
	if err != nil {
		t.Fatalf("HashPassword 返回错误: %v", err)
	}
	if !strings.HasPrefix(hash, "pbkdf2_sha256$") {
		t.Fatalf("哈希格式不符合预期: %q", hash)
	}
	if strings.Contains(hash, "teach123") {
		t.Fatalf("哈希中不应出现口令明文: %q", hash)
	}
	if !VerifyPassword(hash, "teach123") {
		t.Fatal("正确口令校验应当通过")
	}
	if VerifyPassword(hash, "teach124") {
		t.Fatal("错误口令校验不应通过")
	}
}

func TestHashPasswordUsesRandomSalt(t *testing.T) {
	a, err := HashPassword("same-password")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("same-password")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("相同口令两次哈希结果不应相同（盐必须随机）")
	}
}

func TestVerifyPasswordRejectsMalformed(t *testing.T) {
	for _, bad := range []string{"", "plain", "pbkdf2_sha256$abc$x$y", "md5$1$2$3"} {
		if VerifyPassword(bad, "whatever") {
			t.Fatalf("畸形哈希不应校验通过: %q", bad)
		}
	}
}

func TestVerifyDummyDoesNotPanic(t *testing.T) {
	VerifyDummy("anything")
}
