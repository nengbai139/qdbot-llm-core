package subscription

import (
	"testing"
	"time"
)

func TestTokenRoundTrip(t *testing.T) {
	secret := "test-secret-123"
	uid := "device-abc"
	exp := time.Now().Add(24 * time.Hour)
	tok := SignToken(uid, exp, secret)

	if got := VerifyToken(tok, secret, time.Now()); got != uid {
		t.Fatalf("正常验证失败: %q", got)
	}
	if got := VerifyToken(tok, "wrong-secret", time.Now()); got != "" {
		t.Fatalf("错误密钥未拦截: %q", got)
	}
	expired := SignToken(uid, time.Now().Add(-time.Hour), secret)
	if got := VerifyToken(expired, secret, time.Now()); got != "" {
		t.Fatalf("过期未拦截: %q", got)
	}
}

func TestActivateRenewStacks(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 首次订阅：约 1 年
	sub1, err := s.Activate("u1", "pro_yearly", "")
	if err != nil {
		t.Fatal(err)
	}
	// 续费：未过期 → 叠加，约 2 年
	sub2, err := s.Activate("u1", "pro_yearly", "")
	if err != nil {
		t.Fatal(err)
	}
	if sub2.ExpiresAt.Sub(sub1.ExpiresAt) < 360*24*time.Hour {
		t.Fatalf("续费未叠加: %v -> %v", sub1.ExpiresAt, sub2.ExpiresAt)
	}
}

func TestTrialOnce(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	if _, err := s.StartTrial("u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartTrial("u1"); err == nil {
		t.Fatal("重复开通试用未拦截")
	}
}
