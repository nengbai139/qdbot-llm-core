package authz

import (
	"strings"
	"testing"
)

// TestCheck_NotLoggedIn 锁定未登录时 Check 返回不允许 + 可执行的引导提示。
//
// 这是权益闸门最关键的一条：SSH 私钥嵌在二进制里，若未登录也能放行代理，
// 等于任何拿到 zip 的人都能白嫖海外节点。
func TestCheck_NotLoggedIn(t *testing.T) {
	// auth.Open("") 读 ~/.qdbot-tunnel/auth.json。测试环境指向一个空目录，
	// 模拟"从未登录过"的新用户。
	t.Setenv("HOME", t.TempDir())

	d := Check()
	if d.Allowed {
		t.Fatal("未登录必须拒绝（否则等于免费送海外节点）")
	}
	if !strings.Contains(d.Reason, "登录") {
		t.Fatalf("提示应引导用户登录, got %q", d.Reason)
	}
}

// TestDecision_ReasonNonEmpty 锁定不允许时必须给出非空原因（否则用户只看到"启动失败"）。
func TestDecision_ReasonNonEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if d := Check(); d.Reason == "" {
		t.Fatal("拒绝时 Reason 不能为空")
	}
}
