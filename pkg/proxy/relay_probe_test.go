package proxy

import (
	"net"
	"testing"
	"time"
)

// TestProbeWithToken 锁定后台探测的判定：通的节点标记为健康，不通的标记为故障。
//
// 这是「故障发现提前到用户请求之前」的关键——用户流量不该自己踩坑才知道节点挂了。
func TestProbeWithToken(t *testing.T) {
	old := relayHandshakeTimeoutSec
	relayHandshakeTimeoutSec = time.Second
	defer func() { relayHandshakeTimeoutSec = old }()

	// 健康节点：回 OK
	good := newFakeRelay(t, func(c net.Conn) { _, _ = c.Write([]byte("OK\n")) })
	// 故障节点：连上但不回话（半死节点的真实表现）
	dead := newFakeRelay(t, func(c net.Conn) { select {} })

	// 复位全局节点池，避免受其他测试影响
	pool.mu.Lock()
	pool.cooling = map[string]time.Time{}
	pool.failed = map[string]int{}
	pool.mu.Unlock()

	probeWithToken("test-token", []string{good, dead})

	pool.mu.Lock()
	_, goodCooling := pool.cooling[good]
	_, deadCooling := pool.cooling[dead]
	pool.mu.Unlock()

	if goodCooling {
		t.Fatalf("健康节点 %s 不该被冷却", good)
	}
	if !deadCooling {
		t.Fatalf("故障节点 %s 应被冷却", dead)
	}
}

// newFakeRelay 起一个假中继，返回它的地址；handler 决定它怎么回应。
func newFakeRelay(t *testing.T, handler func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handler(c)
		}
	}()
	return ln.Addr().String()
}
