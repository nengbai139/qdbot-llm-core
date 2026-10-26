//go:build darwin

package sysproxy

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type savedService struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
}

type saved struct {
	Services []savedService `json:"services"`
}

// Install 把分流脚本设为各网卡的系统自动代理。已装过则只更新脚本。
// extraRules 来自 config.toml 的 proxy.rules，渲染进 PAC 后强制这些域名走代理。
func Install(port int, extraRules []string) error {
	path, err := pacPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(RenderPAC(port, extraRules)), 0o644); err != nil {
		return err
	}
	// PAC 用 HTTP 暴露（WebUI /proxy.pac，端口与 cmd/qtunnel 的 webAddr 一致）。
	// 不用 file://：Chrome/macOS 对 file:// PAC 加载不可靠，HTTP 还能被浏览器周期性刷新。
	pacURL := "http://127.0.0.1:8900/proxy.pac"
	services := enabledServices()
	if _, err := os.Stat(restorePath()); err != nil {
		var snap saved
		for _, name := range services {
			on, u := currentAuto(name)
			snap.Services = append(snap.Services, savedService{Name: name, Enabled: on, URL: u})
		}
		if b, err := json.Marshal(snap); err == nil {
			_ = os.WriteFile(restorePath(), b, 0o644)
		}
	}
	for _, name := range services {
		if err := exec.Command("networksetup", "-setautoproxyurl", name, pacURL).Run(); err != nil {
			log.Printf("系统分流未写到 %s: %v", name, err)
		}
	}
	log.Printf("系统分流已开启 %s", pacURL)
	return nil
}

// Clear 卸下本次装上的系统自动代理，并恢复原来的设置。
func Clear() {
	b, err := os.ReadFile(restorePath())
	if err != nil {
		return
	}
	var snap saved
	if json.Unmarshal(b, &snap) != nil {
		return
	}
	for _, s := range snap.Services {
		if s.Enabled && s.URL != "" && s.URL != "(null)" {
			_ = exec.Command("networksetup", "-setautoproxyurl", s.Name, s.URL).Run()
			continue
		}
		_ = exec.Command("networksetup", "-setautoproxystate", s.Name, "off").Run()
	}
	_ = os.Remove(restorePath())
	log.Printf("系统分流已关闭")
}

func pacPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".qdbot-tunnel", "proxy.pac"), nil
}

func restorePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".qdbot-tunnel", "sysproxy.json")
}

func enabledServices() []string {
	out, err := exec.Command("networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return nil
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "An asterisk") || strings.HasPrefix(line, "*") {
			continue
		}
		names = append(names, line)
	}
	return names
}

func currentAuto(service string) (bool, string) {
	out, err := exec.Command("networksetup", "-getautoproxyurl", service).Output()
	if err != nil {
		return false, ""
	}
	var on bool
	var u string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Enabled:") {
			on = strings.HasSuffix(line, "Yes")
		}
		if strings.HasPrefix(line, "URL:") {
			u = strings.TrimSpace(strings.TrimPrefix(line, "URL:"))
		}
	}
	return on, u
}
