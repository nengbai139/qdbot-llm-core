package proxy

import (
	"net"
	"testing"
	"time"
)

// TestDialRelayHandshakeTimeout 锁定「半死中继不会把客户端永久挂住」。
//
// 场景：节点 TCP 能连上（内核还活着）但进程不应答——机器假死时的真实表现。
// 没有握手读超时的话 ReadString 会一直阻塞，客户端这条连接就废了。
func TestDialRelayHandshakeTimeout(t *testing.T) {
	// 假中继：accept 之后什么都不回。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// 故意不回应，也不关闭连接。
			_ = c
		}
	}()

	old := relayHandshakeTimeoutSec
	relayHandshakeTimeoutSec = 800 * time.Millisecond
	defer func() { relayHandshakeTimeoutSec = old }()

	start := time.Now()
	_, _, err = dialRelay(ln.Addr().String(), "token", "example.com:443")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("半死中继应握手失败")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("握手应在超时内放弃，实际耗时 %v", elapsed)
	}
}

// TestDialRelayReject 令牌无效时中继会拒绝（不回 OK），客户端应报错而不是干等。
func TestDialRelayReject(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 128)
		_, _ = c.Read(buf)
		_, _ = c.Write([]byte("DENIED\n"))
		_ = c.Close()
	}()

	old := relayHandshakeTimeoutSec
	relayHandshakeTimeoutSec = 3 * time.Second
	defer func() { relayHandshakeTimeoutSec = old }()

	if _, _, err := dialRelay(ln.Addr().String(), "bad-token", "example.com:443"); err == nil {
		t.Fatal("被拒的中继应返回错误")
	}
}
