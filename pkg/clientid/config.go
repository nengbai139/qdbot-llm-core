package clientid

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// qdbotConfigSlice 只解析 qdbot.yaml 中的 qdbotSystem.clientId。
type qdbotConfigSlice struct {
	QDBotSystem struct {
		ClientID string `yaml:"clientId"`
		URL      string `yaml:"url"`
	} `yaml:"qdbotSystem"`
}

// ResolveConfigPath 与 qdbot 相同的默认路径顺序。
func ResolveConfigPath(path string) string {
	if path != "" {
		return path
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		"./qdbot.yaml",
		"./qdbot.yml",
		"./config/qdbot.yaml",
		"./config/qdbot.yml",
		filepath.Join(home, ".qdbot", "qdbot.yaml"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(home, ".qdbot", "qdbot.yaml")
}

// LoadClientIDFromQDBotConfig 从 qdbot 配置文件中读取 qdbotSystem.clientId；支持环境变量 QDBOT_SYSTEM_CLIENT_ID；若 URL 已配置且 clientId 为空则用系统设备 ID。
func LoadClientIDFromQDBotConfig(configPath string) string {
	resolved := ResolveConfigPath(configPath)
	data, err := os.ReadFile(resolved)
	if err != nil {
		return ""
	}
	var c qdbotConfigSlice
	if err := yaml.Unmarshal(data, &c); err != nil {
		return ""
	}
	id := strings.TrimSpace(c.QDBotSystem.ClientID)
	if id == "" && os.Getenv("QDBOT_SYSTEM_CLIENT_ID") != "" {
		id = strings.TrimSpace(os.Getenv("QDBOT_SYSTEM_CLIENT_ID"))
	}
	if id == "" && strings.TrimSpace(c.QDBotSystem.URL) != "" {
		id = GetSystemDeviceID()
	}
	return id
}

// EffectiveClientID 返回用于隧道子域名的 Client ID：显式 clientID > qdbot 配置 > 系统设备 ID。
func EffectiveClientID(explicitClientID, configPath string) string {
	if s := strings.TrimSpace(explicitClientID); s != "" {
		return s
	}
	if s := LoadClientIDFromQDBotConfig(configPath); s != "" {
		return s
	}
	return GetSystemDeviceID()
}
