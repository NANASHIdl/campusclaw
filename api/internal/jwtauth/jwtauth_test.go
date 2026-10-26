package jwtauth

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestIssueAndVerify(t *testing.T) {
	m := New("test-secret", time.Hour)

	token, claims, err := m.Issue(7)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if claims.Sub != "7" {
		t.Errorf("sub 应为 7，实际 %q", claims.Sub)
	}
	if len(claims.Jti) != 32 {
		t.Errorf("jti 应为 32 位十六进制，实际 %q", claims.Jti)
	}

	got, err := m.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Sub != claims.Sub || got.Jti != claims.Jti || got.Exp != claims.Exp {
		t.Errorf("声明不一致: %+v vs %+v", got, claims)
	}
}

func TestRejectTamperedSignature(t *testing.T) {
	m := New("test-secret", time.Hour)
	token, _, _ := m.Issue(1)

	// 改掉签名最后一个字符。
	tampered := token[:len(token)-1] + "A"
	if _, err := m.Verify(tampered); err == nil {
		t.Error("签名被篡改应校验失败")
	}
}

func TestRejectAlgNone(t *testing.T) {
	m := New("test-secret", time.Hour)

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload, _ := json.Marshal(Claims{Sub: "1", Jti: "abc", Iat: time.Now().Unix(), Exp: time.Now().Add(time.Hour).Unix()})
	body := base64.RawURLEncoding.EncodeToString(payload)

	if _, err := m.Verify(header + "." + body + "."); err == nil {
		t.Error("alg=none 必须被拒绝")
	}
}

func TestRejectExpired(t *testing.T) {
	m := New("test-secret", time.Second)
	past := time.Now().Add(-2 * time.Second)
	m.now = func() time.Time { return past }

	token, _, err := m.Issue(1)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	m.now = time.Now
	if _, err := m.Verify(token); err == nil {
		t.Error("过期令牌必须被拒绝")
	}
}

func TestRejectMalformed(t *testing.T) {
	m := New("test-secret", time.Hour)
	for _, tok := range []string{"", "abc", "a.b", "a.b.c", "a b.c.d"} {
		if _, err := m.Verify(tok); err == nil {
			t.Errorf("畸形令牌 %q 必须被拒绝", tok)
		}
	}
}
