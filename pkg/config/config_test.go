package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteClientConfigDropsAuthSecret 锁定「客户端不再持有 auth_secret」。
//
// 订阅令牌已改为服务端签发（客户端拿 auth.json 里的令牌去平台换），
// 客户端没有任何代码需要这把密钥——落盘只会多留一份副本。
func TestWriteClientConfigDropsAuthSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := Default()
	cfg.Server.Address = "tunnel.example.com:9444"
	cfg.Server.AuthSecret = "super-secret-value"
	cfg.Proxy.Relays = []string{"1.2.3.4:22", "5.6.7.8:22"}

	if err := WriteClientConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if strings.Contains(body, "super-secret-value") || strings.Contains(body, "auth_secret") {
		t.Fatalf("落盘的配置不应包含 auth_secret:\n%s", body)
	}
	if !strings.Contains(body, `address = "tunnel.example.com:9444"`) {
		t.Fatalf("应保留公开的 server.address:\n%s", body)
	}
	if !strings.Contains(body, `"1.2.3.4:22"`) {
		t.Fatalf("应写入 relays:\n%s", body)
	}
}

// TestLoadDropsAuthSecret 旧配置文件里的 auth_secret 读到即丢弃。
func TestLoadDropsAuthSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	old := "# 旧版配置\n[server]\naddress = \"tunnel.example.com:9444\"\nauth_secret = \"legacy-secret\"\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.AuthSecret != "" {
		t.Fatalf("加载后不应保留 auth_secret，得到 %q", cfg.Server.AuthSecret)
	}
	if cfg.Server.Address != "tunnel.example.com:9444" {
		t.Fatalf("应保留 address，得到 %q", cfg.Server.Address)
	}
}
