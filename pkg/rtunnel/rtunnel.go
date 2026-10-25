// Package rtunnel 对外暴露 qdbot_tunnel 的 client 端反代隧道，
// 供其他项目（如 qdbot-llm-market）复用：把本地端口一键暴露到公网子域名，并把模型注册进市场。
//
// Go 的 internal 可见性要求消费方必须在 github.com/nengbai139/qdbot-llm-core 之下，跨项目无法直接
// import pkg/tunnelclient 或 internal/tunnel。本包是唯一对外公开的门面：
// 内部完整复用 pkg/tunnelclient 的自愈重连 + 订阅令牌刷新 + client-id 派生，
// 只暴露「暴露一个端口」「注册一个模型」两个语义。
package rtunnel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/nengbai139/qdbot-llm-core/internal/auth"
	"github.com/nengbai139/qdbot-llm-core/internal/clientid"
	"github.com/nengbai139/qdbot-llm-core/pkg/config"
	"github.com/nengbai139/qdbot-llm-core/pkg/tunnelclient"
)

// Exposed 一个已建立的反代隧道句柄，可查询服务端分配的公网 URL。
type Exposed struct {
	m *tunnelclient.Client
}

// Expose 把 local 端口（如 127.0.0.1:4000）通过 qdbot_tunnel 反向隧道暴露到公网。
//
// 用独立 client_id（"llm-"+deviceID）拿独立子域名：qdbot-llm 和 qtunnel 可以在同一台机器上
// 各暴露各的服务，互不抢子域名/端口（qtunnel 默认用 deviceID 转发它的 local，这里用 llm-* 区分）。
func Expose(local string) (*Exposed, error) {
	cfg, err := config.Load(config.Path())
	if err != nil {
		return nil, err
	}
	cfg.Tunnel.Local = local
	cfg.Tunnel.Local6 = "" // 只暴露本地网关这一个入口，不 fallback 到配置里残留的旧 Local6

	// 独立 client_id：服务端有所有权校验（CheckOwnership），先注册成设备再连。
	llmID := "llm-" + clientid.EffectiveClientID("", "")
	if err := registerClient(llmID); err != nil {
		return nil, fmt.Errorf("注册 LLM 设备失败: %w", err)
	}

	m := tunnelclient.New(cfg)
	m.SetClientID(llmID)
	m.Start()
	return &Exposed{m: m}, nil
}

// URL 返回服务端分配的公网 URL（首次握手后非空；尚未分配时返回空串）。
func (e *Exposed) URL() string {
	return e.m.URL()
}

// RegisterModel 把模型信息写进 tunnel-web 的设备 model 字段，
// 让 llm-gateway 的目录（rserver.ListProviders）能识别这个设备为模型提供方。
// payoutUserID 是提供方的 qdbot_account user_id（分成 credit 的收款账号）。
// 要求已登录（auth.json 有 JWT）。
func RegisterModel(model string, priceIn, priceOut float64, payoutUserID string) error {
	base, a, err := clientAuth()
	if err != nil {
		return err
	}
	cid := "llm-" + clientid.EffectiveClientID("", "")

	body, err := json.Marshal(map[string]any{
		"name": model, "price_in": priceIn, "price_out": priceOut, "payout_user_id": payoutUserID,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPut, base+"/api/clients/"+cid+"/model", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("注册模型失败: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("注册模型失败 %s: %s", resp.Status, rb)
	}
	return nil
}

// registerClient 把独立 client_id 注册成设备（POST /api/clients），拿到独立子域名。
func registerClient(clientID string) error {
	base, a, err := clientAuth()
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	body, _ := json.Marshal(map[string]string{"client_id": clientID, "name": host})
	req, err := http.NewRequest(http.MethodPost, base+"/api/clients", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("注册设备 %s: %s", resp.Status, rb)
	}
	return nil
}

// clientAuth 读登录态，返回 (server base URL, auth)。未登录返回错误。
func clientAuth() (string, *auth.Auth, error) {
	s, err := auth.Open("")
	if err != nil || s == nil {
		return "", nil, errors.New("无法打开鉴权存储")
	}
	a, err := s.Load()
	if err != nil || a == nil || a.Token == "" {
		return "", nil, errors.New("未登录：先跑 qtunnel login/verify")
	}
	base := a.ServerURL
	if base == "" {
		cfg, err := config.Load(config.Path())
		if err != nil {
			return "", nil, err
		}
		base = "https://" + cfg.Server.Domain
	}
	return base, a, nil
}
