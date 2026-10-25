// Package daemon 对外暴露 qdbot_tunnel 的跨平台服务管理（launchd/systemd/Windows 服务），
// 供 qdbot-llm-market 复用「进程常驻 + 开机自启」，服务名用产品段区分（llm vs tunnel 不撞名）。
package daemon

import internaldaemon "github.com/nengbai139/qdbot-llm-core/internal/daemon"

// Daemon 服务管理接口（别名，等于 internal/daemon.Daemon）。
type Daemon = internaldaemon.Daemon

// New 返回当前平台的实现。name 是服务标识的产品段：
// darwin 用 "com.qdbot.<name>"，linux/windows 用 "qdbot-<name>"。
func New(name string) Daemon {
	return internaldaemon.New(name)
}
