// Package auth 客户端鉴权：保存登录后服务端返回的 JWT 到 ~/.qdbot-tunnel/auth.json。
// 用于调 tunnel-web 的 REST API（注册设备、拉配置、拉订阅令牌）。
package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Auth 登录态。
type Auth struct {
	Token     string    `json:"token"`      // Bearer JWT
	UserID    string    `json:"user_id"`    // 用户 ID（与 server User.UserID 一致）
	Email     string    `json:"email"`      // 邮箱
	ServerURL string    `json:"server_url"` // tunnel-web API 基础 URL，如 https://tunnel.aimatchem.com
	CreatedAt time.Time `json:"created_at"`
}

// Store auth.json 持久化。
type Store struct {
	mu  sync.Mutex
	dir string
}

// Open 默认目录（~/.qdbot-tunnel）。
func Open(dir string) (*Store, error) {
	if dir == "" {
		home, _ := os.UserHomeDir()
		if home == "" {
			return &Store{}, nil
		}
		dir = filepath.Join(home, ".qdbot-tunnel")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) path() string {
	if s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, "auth.json")
}

// Load 读取已保存的登录态（不存在返回空 Auth + nil）。
func (s *Store) Load() (*Auth, error) {
	p := s.path()
	if p == "" {
		return nil, os.ErrNotExist
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var a Auth
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// Save 写入登录态（覆盖）。
func (s *Store) Save(a *Auth) error {
	p := s.path()
	if p == "" {
		return os.ErrNotExist
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// Clear 清除登录态（退出登录）。
func (s *Store) Clear() error {
	p := s.path()
	if p == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// HasLogin 是否有有效登录态。
func (s *Store) HasLogin() bool {
	a, err := s.Load()
	return err == nil && a != nil && a.Token != ""
}