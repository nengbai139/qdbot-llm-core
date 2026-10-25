//go:build darwin

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type darwinDaemon struct{ name string }

func newPlatformDaemon(name string) Daemon { return &darwinDaemon{name: name} }

func (d *darwinDaemon) label() string { return "com.qdbot." + d.name }

func (d *darwinDaemon) plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", d.label()+".plist"), nil
}

// Install 写 LaunchAgent plist，KeepAlive=true 实现断线自动重连，RunAtLoad=true 实现开机自启。
func (d *darwinDaemon) Install(cmd string, args []string) error {
	path, err := d.plistPath()
	if err != nil {
		return err
	}
	var argXML strings.Builder
	for _, a := range args {
		argXML.WriteString("\n        <string>" + a + "</string>")
	}
	logOut := "/tmp/qdbot-" + d.name + ".log"
	logErr := "/tmp/qdbot-" + d.name + ".err"
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key><string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>%s
    </array>
    <key>KeepAlive</key><true/>
    <key>RunAtLoad</key><true/>
    <!-- ThrottleInterval=60: SSH 长连接挂了重连时，避免 launchd 15 秒疯重启；
         内层 manager.StartProxy 已经有指数退避循环，60s 是兜底 -->
    <key>ThrottleInterval</key><integer>60</integer>
    <key>StandardOutPath</key><string>%s</string>
    <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>`, d.label(), cmd, argXML.String(), logOut, logErr)
	return os.WriteFile(path, []byte(plist), 0o644)
}

func (d *darwinDaemon) Uninstall() error {
	_ = d.Stop()
	path, err := d.plistPath()
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func (d *darwinDaemon) Start() error {
	return exec.Command("launchctl", "load", "-w", d.plistPathOrDie()).Run()
}

func (d *darwinDaemon) Stop() error {
	return exec.Command("launchctl", "unload", d.plistPathOrDie()).Run()
}

func (d *darwinDaemon) Status() (string, error) {
	out, err := exec.Command("launchctl", "list", d.label()).Output()
	if err != nil {
		return "not-installed", nil
	}
	if strings.Contains(string(out), "PID") {
		return "running", nil
	}
	return "stopped", nil
}

func (d *darwinDaemon) plistPathOrDie() string {
	p, _ := d.plistPath()
	return p
}
