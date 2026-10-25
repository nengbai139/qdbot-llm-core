//go:build windows

package daemon

import (
	"fmt"
	"os/exec"
	"strings"
)

type windowsDaemon struct{ name string }

func newPlatformDaemon(name string) Daemon { return &windowsDaemon{name: name} }

func (d *windowsDaemon) serviceName() string { return "qdbot-" + d.name }

// Install 用 sc.exe 注册 Windows 服务（binPath 需带绝对路径 + 参数）。
//
// 失败策略：sc failure actions=restart/5s/10s/30s reset=300
//   - 第 1 次崩溃：5s 后重启
//   - 第 2 次崩溃（未过 reset）：10s 后重启
//   - 第 3 次崩溃（未过 reset）：30s 后重启
//   - reset=300s: 5 分钟内不重置失败计数，避免 SSH 服务端长时间宕机时瘋重启
//   - 内层 manager.StartProxy 已有指数退避，所以 Windows 服务自身重启是兜底
func (d *windowsDaemon) Install(cmd string, args []string) error {
	binPath := fmt.Sprintf("%s %s", cmd, strings.Join(args, " "))
	if err := exec.Command("sc", "create", d.serviceName(), "binPath=", `"`+binPath+`"`, "start=", "auto").Run(); err != nil {
		return fmt.Errorf("sc create 失败: %w", err)
	}
	// 用空格分隔而非 &，避免 Run 把参数分割成多个命令
	return exec.Command("sc", "failure", d.serviceName(), "actions=", "restart/5000/restart/10000/restart/30000", "reset=", "300").Run()
}

func (d *windowsDaemon) Uninstall() error {
	_ = d.Stop()
	return exec.Command("sc", "delete", d.serviceName()).Run()
}

func (d *windowsDaemon) Start() error {
	return exec.Command("sc", "start", d.serviceName()).Run()
}

func (d *windowsDaemon) Stop() error {
	return exec.Command("sc", "stop", d.serviceName()).Run()
}

func (d *windowsDaemon) Status() (string, error) {
	out, err := exec.Command("sc", "query", d.serviceName()).Output()
	if err != nil {
		return "not-installed", nil
	}
	if strings.Contains(string(out), "RUNNING") {
		return "running", nil
	}
	return "stopped", nil
}
