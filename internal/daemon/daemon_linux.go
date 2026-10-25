//go:build linux

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type linuxDaemon struct{ name string }

func newPlatformDaemon(name string) Daemon { return &linuxDaemon{name: name} }

func (d *linuxDaemon) serviceName() string { return "qdbot-" + d.name }

// unitPath 优先 systemd user 服务（无需 root）。
func (d *linuxDaemon) unitPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "systemd", "user", d.serviceName()+".service"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user", d.serviceName()+".service"), nil
}

// Install 写 systemd user unit，Restart=always 实现断线重连，WantedBy=default.target 实现开机自启。
func (d *linuxDaemon) Install(cmd string, args []string) error {
	path, err := d.unitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	unit := fmt.Sprintf(`[Unit]
Description=qdbot %s daemon
After=network-online.target

[Service]
ExecStart=%s %s
Restart=always
RestartSec=10
# 5 分钟内最多重启 10 次，超过限制后不再自动重启（需要 daemon start 手动拉起）
# 避免 SSH 服务端长时间宕机时 systemd 疯重启；manager 内层已有指数退避
StartLimitIntervalSec=300
StartLimitBurst=10

[Install]
WantedBy=default.target
`, d.name, cmd, strings.Join(args, " "))
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return err
	}
	// 使能（创建 WantedBy 符号链接）
	return exec.Command("systemctl", "--user", "enable", d.serviceName()).Run()
}

func (d *linuxDaemon) Uninstall() error {
	_ = exec.Command("systemctl", "--user", "disable", d.serviceName()).Run()
	path, _ := d.unitPath()
	return os.Remove(path)
}

func (d *linuxDaemon) Start() error {
	return exec.Command("systemctl", "--user", "start", d.serviceName()).Run()
}

func (d *linuxDaemon) Stop() error {
	return exec.Command("systemctl", "--user", "stop", d.serviceName()).Run()
}

func (d *linuxDaemon) Status() (string, error) {
	out, err := exec.Command("systemctl", "--user", "is-active", d.serviceName()).Output()
	if err != nil {
		return "not-installed", nil
	}
	switch strings.TrimSpace(string(out)) {
	case "active":
		return "running", nil
	default:
		return "stopped", nil
	}
}
