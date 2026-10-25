// Package daemon 跨平台服务管理：把「进程常驻 + 开机自启」抽象成统一接口。
// 三个平台各自实现（launchd / systemd / Windows 服务），业务逻辑（隧道/代理）不感知平台差异。
package daemon

// Daemon 服务管理接口。
type Daemon interface {
	// Install 注册服务（写 plist / systemd unit / Windows 服务），cmd 为长驻进程二进制，args 为参数。
	Install(cmd string, args []string) error
	// Uninstall 注销服务。
	Uninstall() error
	// Start 启动服务。
	Start() error
	// Stop 停止服务。
	Stop() error
	// Status 返回 running / stopped / not-installed / unknown。
	Status() (string, error)
}

// New 返回当前平台的实现。
// name 是服务标识的产品段：darwin 用 "com.qdbot.<name>" 作 LaunchAgent label，
// linux/windows 用 "qdbot-<name>" 作 systemd unit / Windows 服务名。
// 例：qtunnel 传 "tunnel"，qdbot-llm 传 "llm"——两者不会撞名。
func New(name string) Daemon {
	return newPlatformDaemon(name)
}
