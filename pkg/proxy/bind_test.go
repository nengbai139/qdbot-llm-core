package proxy

import (
	"net"
	"strings"
	"testing"
)

// TestCheckBindLocal_LoopbackAndAny 锁定回环/通配地址直接放行（永远可绑，不必探测）。
func TestCheckBindLocal_LoopbackAndAny(t *testing.T) {
	for _, bind := range []string{"127.0.0.1", "::1", "0.0.0.0", "::", "[::]"} {
		if err := checkBindLocal(bind); err != nil {
			t.Fatalf("%s 应直接放行, got %v", bind, err)
		}
	}
}

// TestCheckBindLocal_Hostname 锁定非 IP（主机名）不拦，交给 net.Listen 解析。
func TestCheckBindLocal_Hostname(t *testing.T) {
	if err := checkBindLocal("localhost"); err != nil {
		t.Fatalf("主机名不应被拦, got %v", err)
	}
}

// TestCheckBindLocal_本机存在的地址 锁定本机真实网卡地址能通过（docker 场景要靠它）。
func TestCheckBindLocal_本机存在的地址(t *testing.T) {
	addrs, err := net.InterfaceAddrs()
	if err != nil || len(addrs) == 0 {
		t.Skip("拿不到本机网卡地址")
	}
	var found string
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
			found = ipnet.IP.String()
			break
		}
	}
	if found == "" {
		t.Skip("本机无 IPv4 地址")
	}
	if err := checkBindLocal(found); err != nil {
		t.Fatalf("本机地址 %s 应放行, got %v", found, err)
	}
}

// TestCheckBindLocal_别机地址被拦并给出可执行提示
//
// 这是本文件的核心回归：proxy.bind 若被误填进「默认配置」，会下发到所有设备；
// 笔记本上没有服务器用的 docker 网关地址，若不拦，用户只看到一个含糊的
// "cannot assign requested address"，完全看不出是配置填错了地方。
func TestCheckBindLocal_别机地址被拦并给出可执行提示(t *testing.T) {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			// 用一个 RFC5737 文档保留段里、本机几乎不可能有的地址
			if ipnet.IP.String() == "203.0.113.7" {
				t.Skip("本机恰好有该地址，跳过")
			}
		}
	}
	const foreign = "203.0.113.7"
	err := checkBindLocal(foreign)
	if err == nil {
		t.Fatalf("本机不存在的地址 %s 应被拦截", foreign)
	}
	msg := err.Error()
	// 提示必须同时点明：地址不存在 + 只配在设备专属配置 + 默认配置会下发到所有设备
	for _, kw := range []string{foreign, "不存在", "设备专属配置", "默认配置"} {
		if !strings.Contains(msg, kw) {
			t.Fatalf("错误提示缺少关键词 %q, got %q", kw, msg)
		}
	}
}
