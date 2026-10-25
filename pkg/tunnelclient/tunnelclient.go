// Package tunnelclient 反向隧道客户端管理：启动/停止/公网 URL。
// 从 qdbot_tunnel/internal/manager 拆出的隧道子集，不沾正向代理（proxy 及其密钥留在闭源）。
package tunnelclient

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/nengbai139/qdbot-llm-core/pkg/auth"
	"github.com/nengbai139/qdbot-llm-core/pkg/clientid"
	"github.com/nengbai139/qdbot-llm-core/pkg/config"
	"github.com/nengbai139/qdbot-llm-core/pkg/subscription"
	"github.com/nengbai139/qdbot-llm-core/pkg/tunnel"
)

// Client 管理反向隧道。cfg 必须是 *config.Config 指针。
type Client struct {
	cfg      *config.Config
	clientID string // 显式 client_id（空 = 用机器 device ID）

	mu     sync.Mutex
	cancel context.CancelFunc
	url    string // 服务端分配的公网 URL（首次握手后填入）
}

// New 创建隧道客户端。
func New(cfg *config.Config) *Client { return &Client{cfg: cfg} }

// SetClientID 显式指定隧道 client_id（须在 Start 之前调用）。
func (c *Client) SetClientID(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clientID = id
}

// Start 后台启动反向隧道（带重连循环，幂等）。失败由内部重连兜底，不返回错误。
func (c *Client) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	cid := c.clientID
	if cid == "" {
		cid = clientid.EffectiveClientID("", "")
	}
	go func() {
		backoff := 5 * time.Second
		// 退避上限 30 秒：有心跳后，链路被掐断最多 90 秒就能发现，再等 5 分钟纯属白等。
		const maxBackoff = 30 * time.Second
		for {
			if ctx.Err() != nil {
				return
			}
			// 每次重连都重算订阅令牌（续费后下次重连即生效）
			token := c.tokenForUser(cid)
			err := tunnel.Run(ctx, c.cfg.Server.Address, c.cfg.Tunnel.Addrs(), cid, token, func(url string) {
				c.setURL(url)
			})
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				log.Printf("tunnel 拨号 %s 失败: %v（%s 后重试）", c.cfg.Server.Address, err, backoff)
			} else {
				log.Printf("tunnel 已断开，%s 后重连", backoff)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff = 30 * time.Second
			} else if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		}
	}()
}

// tokenForUser 拉取订阅令牌（服务端签名）。客户端不再持有 auth_secret、无法自签。
func (c *Client) tokenForUser(userID string) string {
	s, err := auth.Open("")
	if err != nil {
		return ""
	}
	a, err := s.Load()
	if err != nil || a == nil || a.Token == "" || a.ServerURL == "" {
		return ""
	}
	token, _, err := subscription.FetchToken(a.ServerURL, a.Token)
	if err != nil {
		return ""
	}
	return token
}

// Stop 停止隧道。
func (c *Client) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
}

// URL 返回服务端分配的公网 URL（首次握手后非空）。
func (c *Client) URL() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.url
}

// Running 返回隧道是否在运行（cancel 非空视为已启动）。
func (c *Client) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cancel != nil
}

func (c *Client) setURL(url string) {
	c.mu.Lock()
	c.url = url
	c.mu.Unlock()
}
