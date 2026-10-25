// Package authz 权益校验：决定这台机器此刻能不能用出海代理 / 反向隧道。
//
// 为什么必须有这道闸门：SSH 私钥是 go:embed 进客户端二进制的，任何人下载 zip
// 就能拿到这台海外机器的 SSH 访问权。不做校验的话，产品等于免费送出海节点 ——
// 谁下载谁白嫖，服务端根本不知道谁在用。
//
// 校验方式复用「订阅令牌」接口：/api/subscribe-token 由服务端签名，只有登录态有效
// 且订阅处于 active/grace 时才签发。因此「能拿到非空令牌」就等价于「服务端认可
// 该账号此刻有权益」，不需要客户端自己判断到期时间（那会被人改本地时钟绕过）。
//
// 客户端不缓存判定结果，每次校验都打服务端：付费产品的权益判定必须以服务端为准。
package authz

import (
	"errors"
	"time"

	"github.com/nengbai139/qdbot-llm-core/internal/auth"
	"github.com/nengbai139/qdbot-llm-core/internal/subscription"
)

var (
	// ErrNotLoggedIn 未登录。
	ErrNotLoggedIn = errors.New("未登录：请先运行 qtunnel login --email <你的邮箱>")
	// ErrNoSubscription 已登录但订阅无效（未开通 / 已过期）。
	ErrNoSubscription = errors.New("订阅无效或已过期：请运行 qtunnel subscribe 续费")
)

// Decision 一次校验的结果。
type Decision struct {
	Allowed   bool
	UserID    string
	Email     string
	ExpiresAt time.Time
	Reason    string // 不允许时的原因（面向用户的中文提示）
}

// Check 校验当前登录账号的权益。
//
// 链路：本地 auth.json 取会话令牌 → 打服务端 /api/subscribe-token →
// 服务端校验 JWT + 订阅状态，签发带到期时间的订阅令牌。
//
// 拿不到订阅令牌分两种：没登录（ErrNotLoggedIn）、订阅无效（ErrNoSubscription）——
// 服务端对这两种情况返回的 HTTP 状态码不同，用它区分，才能给出准确的引导提示。
func Check() Decision {
	s, err := auth.Open("")
	if err != nil {
		return Decision{Reason: "读取本地登录信息失败: " + err.Error()}
	}
	a, err := s.Load()
	if err != nil || a == nil || a.Token == "" || a.ServerURL == "" {
		return Decision{Reason: ErrNotLoggedIn.Error()}
	}
	token, expires, err := subscription.FetchToken(a.ServerURL, a.Token)
	if err != nil || token == "" {
		return Decision{
			UserID: a.UserID,
			Email:  a.Email,
			Reason: ErrNoSubscription.Error() + "（服务端返回: " + errMsg(err) + "）",
		}
	}
	return Decision{
		Allowed:   true,
		UserID:    a.UserID,
		Email:     a.Email,
		ExpiresAt: expires,
	}
}

func errMsg(err error) string {
	if err == nil {
		return "无令牌"
	}
	return err.Error()
}