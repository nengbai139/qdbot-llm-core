package tunnel

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRunReturnsOnServerClose 老接口 Run 也要能在服务端断开时返回错误（交给上层重连）。
func TestRunReturnsOnServerClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		conn.Close() // 服务端立刻断开
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, ln.Addr().String(), []string{"127.0.0.1:1"}, "legacyclient", "", nil)
	}()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("服务端断开后 Run 应返回错误")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("服务端断开后 Run 一直不返回")
	}
}

// TestProxyToLocalErrorPaths 代理失败时必须回一个像样的 HTTP 错误响应，
// 不能把空响应丢给公网（那会变成 bad response from tunnel）。
func TestProxyToLocalErrorPaths(t *testing.T) {
	t.Run("请求本身是坏的", func(t *testing.T) {
		got := proxyToLocal([]string{"127.0.0.1:1"}, []byte("这不是 HTTP 请求\r\n\r\n"))
		resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(got)), nil)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 500 {
			t.Fatalf("状态码应为 500，实际 %d", resp.StatusCode)
		}
	})

	t.Run("没有可用地址", func(t *testing.T) {
		got := proxyToLocal(nil, []byte("GET / HTTP/1.1\r\n\r\n"))
		resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(got)), nil)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 502 {
			t.Fatalf("状态码应为 502，实际 %d", resp.StatusCode)
		}
		if !strings.Contains(resp.Status, "no local address") {
			t.Fatalf("状态描述应说明原因，实际 %q", resp.Status)
		}
	})

	t.Run("地址全部连不通", func(t *testing.T) {
		got := proxyToLocal([]string{"127.0.0.1:1"}, []byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"))
		resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(got)), nil)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 502 {
			t.Fatalf("状态码应为 502，实际 %d", resp.StatusCode)
		}
	})
}
