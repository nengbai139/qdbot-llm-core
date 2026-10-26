//go:build !darwin

package sysproxy

// Install 非 macOS 不改系统代理。
func Install(port int, extraRules []string) error { return nil }

// Clear 非 macOS 无系统代理可卸。
func Clear() {}
