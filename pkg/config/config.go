// Package config 统一配置：把原来散落在 start_proxy.sh / start_tunnel.sh / 环境变量 / PAC / plist 的
// 配置收敛到一份 ~/.qdbot-tunnel/config.toml，三平台共用。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Config 顶层配置 —— 客户端可见的字段（用户可编辑）。服务端信息（地址、auth_secret 等）
// 不在此处，存到 ~/.qdbot-tunnel/auth.json (0600)，用户看不见也不需要看见。
type Config struct {
	Server ServerConfig `toml:"server"` // 反向隧道 server（仅作运行时合并用，不写文件）
	Tunnel TunnelConfig `toml:"tunnel"` // 反向隧道 client（用户可改 local）
	Proxy  ProxyConfig  `toml:"proxy"`  // 正向代理（用户可改 port + rules）
}

// ServerAddressOfficial 官方节点地址（编译时内置）。用户改不了、不需要改。
const ServerAddressOfficial = "tunnel.aimatchem.com:9444"
const ServerDomainOfficial = "tunnel.aimatchem.com"

// 正向代理的官方节点地址 / 主机密钥已移到 internal/proxy（机密，不出现在开源仓库）。

// SMTPConfig 邮件发送配置（登录验证码）。
type SMTPConfig struct {
	Host     string `toml:"host"`     // smtp.126.com
	Port     int    `toml:"port"`     // 465(TLS) / 587(STARTTLS)
	User     string `toml:"user"`     // 登录邮箱
	Password string `toml:"password"` // 授权码/密码
	From     string `toml:"from"`     // 发件人，默认同 User
}

// ServerConfig 反向隧道服务端（部署在公网 VPS）。
type ServerConfig struct {
	Address    string `toml:"address"`     // 如 tunnel.example.com:9444
	Domain     string `toml:"domain"`      // 通配符域名，如 tunnel.aimatchem.com
	DNS        string `toml:"dns"`         // aliyun / cloudflare / ""（不自动写 DNS）
	AuthSecret string `toml:"auth_secret"` // 已废弃：订阅令牌改由服务端签发，客户端不再持有/使用此密钥（保留字段兼容旧 config.toml）
}

// TunnelConfig 反向隧道客户端要暴露的本地服务。
// Local 是 IPv4，Local6 是 IPv6。两个都填时先连 Local，连不上再连 Local6。
type TunnelConfig struct {
	Local  string `toml:"local"`  // 如 127.0.0.1:8080
	Local6 string `toml:"local6"` // 如 [::1]:8080
}

// Addrs 返回要尝试的本地地址，IPv4 在前。
func (t TunnelConfig) Addrs() []string {
	var out []string
	if s := strings.TrimSpace(t.Local); s != "" {
		out = append(out, s)
	}
	if s := strings.TrimSpace(t.Local6); s != "" {
		out = append(out, s)
	}
	return out
}

// ProxyConfig 正向代理节点。
type ProxyConfig struct {
	Server  string   `toml:"server"`   // SSH 节点地址
	Port    int      `toml:"port"`     // 本地 SOCKS5 端口，默认 1080
	User    string   `toml:"user"`     // SSH 用户，默认 qdbot（受限账号，仅用于隧道转发）
	HostKey string   `toml:"host_key"` // 可选：固定服务端 host key（known_hosts 格式），留空则用 ~/.ssh/known_hosts
	Rules   []string `toml:"rules"`    // 分流白名单：命中走代理，默认直连
	// Bind SOCKS5 监听地址，默认 127.0.0.1（只有本机可用）。
	//
	// 何时该改：服务跑在 docker bridge 网络的容器里时，容器的 127.0.0.1 指向容器自己，
	// 连不上宿主机的 SOCKS5。此时设成 0.0.0.0 让容器能连——**但 SOCKS5 无鉴权，
	// 改 0.0.0.0 等于在所有网卡上开开放代理，必须用防火墙只放行可信网段**
	// （docker 网段 / 内网网段），绝不能让公网直接可达。
	Bind string `toml:"bind"`
	// Relays 正向代理中继节点列表（host:port），第一个为主、其余为备用。
	//
	// 走「海外节点中继」模式：客户端只发订阅令牌，中继验令牌后直接从海外出网，
	// 流量不经过平台服务器，客户端零密钥（无需内嵌私钥）。
	//
	// 由平台下发（proxy.relays），客户端不写死节点地址：节点增减/换 IP 不用发新版。
	// 留空则用客户端内置的官方列表兜底。
	Relays []string `toml:"relays"`
}

// Default 返回带默认值的配置（未配置时兜底）。默认指向官方节点 tunnel.aimatchem.com。
// 默认分流规则：常用大模型/代码托管域名（命中走代理）。
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Address: ServerAddressOfficial,
			Domain:  ServerDomainOfficial,
		},
		Tunnel: TunnelConfig{Local: "127.0.0.1:8080"},
		Proxy: ProxyConfig{
			Server: "", // 官方节点地址在 internal/proxy 里填（机密，不落在默认配置）
			Port:   1080,
			User:   "qdbot",
			Rules:  []string{"github.com", "openai.com", "api.anthropic.com"},
		},
	}
}

// Path 返回配置文件路径：$QDBOT_TUNNEL_CONFIG > ~/.qdbot-tunnel/config.toml。
func Path() string {
	if p := os.Getenv("QDBOT_TUNNEL_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".qdbot-tunnel", "config.toml")
}

// Load 读取配置；文件不存在时返回默认值 + 写入默认配置文件（首跑自动生成）。
func Load(path string) (*Config, error) {
	cfg := Default()
	if path == "" {
		path = Path()
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			_ = writeDefaultClientFile(path) // 首跑：写一份只含用户可见字段的默认配置
			return cfg, nil
		}
		return nil, err
	}
	if err := toml.Unmarshal(b, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// writeDefaultClientFile 首跑生成客户端配置：只暴露用户日常需要编辑的 3 个字段。
// 服务端信息（地址、auth_secret）不在文件里 —— 客户端无法感知、无法操作。
func writeDefaultClientFile(path string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	body := `# qdbot tunel 客户端配置
[tunnel]
local = "127.0.0.1:8080"

[proxy]
port = 1080
rules = ["github.com", "openai.com", "api.anthropic.com"]
`
	return os.WriteFile(path, []byte(body), 0o644)
}

// Save 把当前配置完整序列化（包含高级字段）。通常首跑自动生成用 writeDefaultClientFile。
func Save(path string, cfg *Config) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	b, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// writeClientConfig 把客户端可见字段写回 config.toml：
//   - 必显的 3 个用户字段：tunnel.local / proxy.port / proxy.rules
//   - [server] 段：address（公开）和 auth_secret（HMAC 密钥，与服务端协商）；
//     auth_secret 不应让用户手写，但 daemon 启动要读到它来签 SUBSCRIBE token，所以必须落盘
//   - 旧版（v0.3.x）不写 [server]，导致 cmdRun 中 cfg = mustLoad() 后 AuthSecret 变成空 —— tunnel 用空签名被 reject
func writeClientConfig(path string, cfg *Config) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	body := &strings.Builder{}
	body.WriteString("# qdbot tunel 客户端配置\n")
	if cfg.Server.Address != "" || cfg.Server.AuthSecret != "" {
		body.WriteString("[server]\n")
		if cfg.Server.Address != "" {
			fmt.Fprintf(body, "address = %q\n", cfg.Server.Address)
		}
		if cfg.Server.AuthSecret != "" {
			fmt.Fprintf(body, "auth_secret = %q\n", cfg.Server.AuthSecret)
		}
		body.WriteString("\n")
	}
	if cfg.Tunnel.Local != "" || cfg.Tunnel.Local6 != "" {
		body.WriteString("[tunnel]\n")
		if cfg.Tunnel.Local != "" {
			fmt.Fprintf(body, "local = %q\n", cfg.Tunnel.Local)
		}
		if cfg.Tunnel.Local6 != "" {
			fmt.Fprintf(body, "local6 = %q\n", cfg.Tunnel.Local6)
		}
		body.WriteString("\n")
	}
	body.WriteString("[proxy]\n")
	if cfg.Proxy.Bind != "" {
		fmt.Fprintf(body, "bind = %q\n", cfg.Proxy.Bind)
	}
	fmt.Fprintf(body, "port = %d\n", cfg.Proxy.Port)
	if len(cfg.Proxy.Rules) > 0 {
		body.WriteString("rules = [")
		for i, r := range cfg.Proxy.Rules {
			if i > 0 {
				body.WriteString(", ")
			}
			fmt.Fprintf(body, "%q", r)
		}
		body.WriteString("]\n")
	}
	// relays 海外中继节点列表（平台下发的中继地址）。留空则用客户端内置兜底列表。
	if len(cfg.Proxy.Relays) > 0 {
		body.WriteString("relays = [")
		for i, r := range cfg.Proxy.Relays {
			if i > 0 {
				body.WriteString(", ")
			}
			fmt.Fprintf(body, "%q", r)
		}
		body.WriteString("]\n")
	}
	return os.WriteFile(path, []byte(body.String()), 0o644)
}

// WriteClientConfig 公开版本，给 qtunnel 用。
func WriteClientConfig(path string, cfg *Config) error {
	return writeClientConfig(path, cfg)
}
