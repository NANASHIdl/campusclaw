package auth

import (
	"strings"
	"testing"
	"time"
)

func TestNewSessionTokenIsRandomAndOpaque(t *testing.T) {
	a, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("两次生成的会话标识不应相同")
	}
	if len(a) < 32 {
		t.Fatalf("会话标识过短: %d", len(a))
	}
	// 标识本身不得携带角色或班级等可读信息
	for _, leak := range []string{"teacher", "student", "class"} {
		if strings.Contains(strings.ToLower(a), leak) {
			t.Fatalf("会话标识不应包含 %q", leak)
		}
	}
}

func TestSessionDigestDependsOnSecret(t *testing.T) {
	token := "fixed-token-for-test"
	d1 := SessionDigest(token, "secret-one")
	d2 := SessionDigest(token, "secret-two")

	if d1 == token {
		t.Fatal("入库摘要不应等于标识原文")
	}
	if len(d1) != 64 {
		t.Fatalf("SHA-256 摘要应为 64 位十六进制，实际 %d", len(d1))
	}
	if d1 == d2 {
		t.Fatal("不同密钥应得到不同摘要")
	}
	if d1 != SessionDigest(token, "secret-one") {
		t.Fatal("相同输入与密钥的摘要应稳定")
	}
}

func TestLimiterBlocksAfterThreshold(t *testing.T) {
	l := NewLimiter(3, time.Minute)
	key := "teacher_a|127.0.0.1"

	for i := 1; i <= 3; i++ {
		if !l.Allow(key) {
			t.Fatalf("第 %d 次尝试不应被限流", i)
		}
	}
	if l.Allow(key) {
		t.Fatal("超过阈值后应被限流")
	}
	// 不同键互不影响
	if !l.Allow("student_a1|127.0.0.1") {
		t.Fatal("不同账号不应被同一计数影响")
	}
}

func TestLimiterDisabledWhenMaxIsZero(t *testing.T) {
	l := NewLimiter(0, time.Minute)
	for i := 0; i < 100; i++ {
		if !l.Allow("k") {
			t.Fatal("max<=0 时不应限流")
		}
	}
}
