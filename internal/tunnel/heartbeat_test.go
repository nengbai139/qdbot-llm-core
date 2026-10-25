package tunnel

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"

	"github.com/nengbai139/qdbot-llm-core/internal/protocol"
)

// TestClientSendsHeartbeat 客户端必须定时发心跳。
//
// 现场故障：客户端隧道连接被网络黑洞掐断后，客户端整整 1 小时才发现
// （系统 TCP keepalive 的超时），期间公网全是 "tunnel timeout"。
func TestClientSendsHeartbeat(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	pings := make(chan struct{}, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		readHandshakeLine(br) // 真服务端也是先读握手行再读帧
		for {
			typ, _, _, err := protocol.ReadFrame(br)
			if err != nil {
				return
			}
			if typ == protocol.TypePing {
				select {
				case pings <- struct{}{}:
				default:
				}
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = RunWithOptions(ctx, ln.Addr().String(), []string{"127.0.0.1:1"}, "hbclient", "", nil,
			Options{Heartbeat: 200 * time.Millisecond, ReadTimeout: 5 * time.Second})
	}()

	select {
	case <-pings:
	case <-time.After(5 * time.Second):
		t.Fatal("5s 内没收到心跳帧")
	}
	t.Log("✅ 客户端按节奏发出了心跳")
}

// TestClientDetectsDeadTunnel 服务端不回心跳时，客户端必须自己判定链路已死并返回错误，
// 让上层重连——而不是把一条死连接挂着等系统 keepalive。
func TestClientDetectsDeadTunnel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	release := make(chan struct{})
	defer close(release)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		readHandshakeLine(br)
		// 只回一次心跳回应，之后连接保持但彻底沉默（模拟被中间设备掐断）
		for {
			typ, _, _, err := protocol.ReadFrame(br)
			if err != nil {
				return
			}
			if typ == protocol.TypePing {
				if _, err := conn.Write(protocol.Encode(protocol.TypePong, 0, nil)); err != nil {
					return
				}
				<-release
				return
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunWithOptions(ctx, ln.Addr().String(), []string{"127.0.0.1:1"}, "hbclient", "", nil,
			Options{Heartbeat: 100 * time.Millisecond, ReadTimeout: 800 * time.Millisecond})
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("链路已死，Run 应返回错误")
		}
		t.Logf("✅ 客户端及时发现链路已死：%v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("链路死了客户端却没察觉")
	}
}

// TestClientKeepsAliveWithPong 服务端持续回心跳时，客户端不能自己把连接判死。
func TestClientKeepsAliveWithPong(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	release := make(chan struct{})
	defer close(release)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		readHandshakeLine(br)
		for {
			typ, _, _, err := protocol.ReadFrame(br)
			if err != nil {
				return
			}
			if typ == protocol.TypePing {
				if _, err := conn.Write(protocol.Encode(protocol.TypePong, 0, nil)); err != nil {
					return
				}
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunWithOptions(ctx, ln.Addr().String(), []string{"127.0.0.1:1"}, "hbclient", "", nil,
			Options{Heartbeat: 100 * time.Millisecond, ReadTimeout: 400 * time.Millisecond})
	}()

	select {
	case err := <-errCh:
		t.Fatalf("服务端正常回心跳，客户端不该断开: %v", err)
	case <-time.After(2 * time.Second):
	}
	t.Log("✅ 心跳正常时连接保持")
}

// readHandshakeLine 读掉客户端先发来的握手行。真服务端先读握手再读帧，
// 假服务端也得照做，否则会把握手文本当成帧头。
func readHandshakeLine(br *bufio.Reader) {
	_, _ = br.ReadString('\n')
}
