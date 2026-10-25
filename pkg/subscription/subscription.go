// Package subscription 订阅与续费：年订阅套餐，账号绑定（user_id），叠加续费，惰性到期判定。
// 独立于 qdbot 账务，专为 qdbot_tunnel 单产品设计。
package subscription

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Plan 订阅套餐。
type Plan struct {
	PlanID string  `json:"plan_id"`
	Name   string  `json:"name"`
	Price  float64 `json:"price"`
	Years  int     `json:"years"` // 年订阅 = 1
}

// DefaultPlans 默认套餐：年订阅。
var DefaultPlans = []Plan{
	{PlanID: "pro_yearly", Name: "Pro 年订阅", Price: 199, Years: 1},
}

// GraceDays 到期后的宽限天数（宽限期内仍可用，但提示续费）。
const GraceDays = 7

// TrialDays 免费试用期（天）：新用户首次使用自动给一个月。
const TrialDays = 30

// Subscription 用户订阅。
type Subscription struct {
	UserID    string    `json:"user_id"`
	PlanID    string    `json:"plan_id"`
	PlanName  string    `json:"plan_name"`
	Status    string    `json:"status"` // active / grace / expired
	OrderID   string    `json:"order_id,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store 持久化订阅（单 JSON 文件 + 进程内锁）。
type Store struct {
	mu   sync.Mutex
	path string
	subs map[string]Subscription
}

// Open 打开（或初始化）订阅存储，目录默认为 ~/.qdbot-tunnel。
func Open(dir string) (*Store, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, ".qdbot-tunnel")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "subscriptions.json"), subs: map[string]Subscription{}}
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &s.subs)
	}
	return s, nil
}

// StartTrial 给新用户开启 30 天免费试用（仅一次：已有订阅/试用记录则报错）。
func (s *Store) StartTrial(userID string) (Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.subs[userID]; ok {
		return s.subs[userID], fmt.Errorf("该用户已有订阅/试用记录，不能重复开通试用")
	}
	now := time.Now()
	sub := Subscription{
		UserID:    userID,
		PlanID:    "trial",
		PlanName:  "免费试用（30 天）",
		ExpiresAt: now.AddDate(0, 0, TrialDays),
		CreatedAt: now,
		UpdatedAt: now,
	}
	sub.Status = statusOf(sub.ExpiresAt, now)
	s.subs[userID] = sub
	return sub, s.saveLocked()
}

// Activate 激活或续费：未过期则从原到期日叠加（续费），已过期则从现在起算。
// years 从套餐取；orderID 为空表示内部激活。
func (s *Store) Activate(userID, planID, orderID string) (Subscription, error) {
	plan := findPlan(planID)
	if plan == nil {
		return Subscription{}, fmt.Errorf("未知套餐 %q", planID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	expires := now.AddDate(plan.Years, 0, 0)
	if sub, ok := s.subs[userID]; ok && sub.ExpiresAt.After(now) {
		// 续费：未过期 → 在原到期日上叠加年限
		expires = sub.ExpiresAt.AddDate(plan.Years, 0, 0)
	}
	sub := Subscription{
		UserID:    userID,
		PlanID:    plan.PlanID,
		PlanName:  plan.Name,
		OrderID:   orderID,
		ExpiresAt: expires,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if old, ok := s.subs[userID]; ok {
		sub.CreatedAt = old.CreatedAt
	}
	sub.Status = statusOf(sub.ExpiresAt, now)
	s.subs[userID] = sub
	return sub, s.saveLocked()
}

// Get 返回订阅（未订阅返回零值 + false）；状态为惰性判定。
func (s *Store) Get(userID string) (Subscription, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.subs[userID]
	if !ok {
		return Subscription{}, false
	}
	sub.Status = statusOf(sub.ExpiresAt, time.Now())
	return sub, true
}

// IsUsable 是否可用（active 或 grace 都算可用）。
func (s *Store) IsUsable(userID string) bool {
	sub, ok := s.Get(userID)
	if !ok {
		return false
	}
	return sub.Status == "active" || sub.Status == "grace"
}

// DaysLeft 剩余天数（负数表示已过期）。
func (s *Store) DaysLeft(userID string) int {
	sub, ok := s.Get(userID)
	if !ok {
		return 0
	}
	return int(time.Until(sub.ExpiresAt).Hours() / 24)
}

func statusOf(expires, now time.Time) string {
	switch {
	case now.Before(expires):
		return "active"
	case now.Before(expires.AddDate(0, 0, GraceDays)):
		return "grace"
	default:
		return "expired"
	}
}

func findPlan(planID string) *Plan {
	for i := range DefaultPlans {
		if DefaultPlans[i].PlanID == planID {
			return &DefaultPlans[i]
		}
	}
	return nil
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.subs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}

// TokenForUser 计算用户的订阅令牌；secret 为空、未订阅或已过期返回空串。
// 签名到宽限期结束（与 IsUsable 的语义一致）。
func (s *Store) TokenForUser(userID, secret string) string {
	if secret == "" {
		return ""
	}
	sub, ok := s.Get(userID)
	if !ok || !s.IsUsable(userID) {
		return ""
	}
	return SignToken(userID, sub.ExpiresAt.AddDate(0, 0, GraceDays), secret)
}

// SignToken 生成订阅令牌：payload = user_id.expires_unix，sig = hex(HMAC-SHA256(secret, payload))。
// 客户端用，把订阅状态带给服务端做权益校验。
func SignToken(userID string, expires time.Time, secret string) string {
	payload := fmt.Sprintf("%s.%d", userID, expires.Unix())
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil))
}

// VerifyToken 校验令牌：签名比对（常数时间）+ 过期检查。成功返回 user_id，失败返回空串。
func VerifyToken(token, secret string, now time.Time) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[2])) {
		return ""
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || now.Unix() > exp {
		return ""
	}
	return parts[0]
}

// FetchToken 从服务端拉订阅令牌（服务端签名，客户端不自签）。
// serverURL 是 tunnel-web API 基础 URL（如 https://tunnel.aimatchem.com），jwt 为会话令牌。
// 返回服务端签发的订阅令牌 + 到期时间。
func FetchToken(serverURL, jwt string) (string, time.Time, error) {
	if serverURL == "" || jwt == "" {
		return "", time.Time{}, fmt.Errorf("serverURL 或 jwt 为空")
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(serverURL, "/")+"/api/subscribe-token", nil)
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("订阅令牌请求失败(%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", time.Time{}, err
	}
	if out.Token == "" {
		return "", time.Time{}, fmt.Errorf("服务端未返回订阅令牌")
	}
	return out.Token, out.ExpiresAt, nil
}
