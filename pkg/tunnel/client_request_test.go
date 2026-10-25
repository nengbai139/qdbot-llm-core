package tunnel

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nengbai139/qdbot-llm-core/pkg/protocol"
)

// TestClientServesForwardedRequest 服务端把 HTTP 请求转发过来，客户端必须代理到本地服务并回响应。
// 这是隧道的主干路径：公网请求 → 服务端 → 客户端 → 本地服务 → 原样回来。
func TestClientServesForwardedRequest(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("local:" + r.URL.Path))
	}))
	defer local.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	respCh := make(chan []byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		readHandshakeLine(br)

		// 组一个真实的 HTTP 请求转发给客户端
		req, _ := http.NewRequest(http.MethodGet, "http://tunnel.example/probe", nil)
		var raw bytes.Buffer
		_ = req.Write(&raw)
		if _, err := conn.Write(protocol.Encode(protocol.TypeRequest, 7, raw.Bytes())); err != nil {
			return
		}

		typ, id, payload, err := protocol.ReadFrame(br)
		if err != nil {
			return
		}
		if typ != protocol.TypeResponse || id != 7 {
			t.Errorf("回帧类型/序号不对: type=%d id=%d", typ, id)
		}
		respCh <- payload
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = RunWithOptions(ctx, ln.Addr().String(),
			[]string{strings.TrimPrefix(local.URL, "http://")}, "reqclient", "", nil,
			Options{Heartbeat: 50 * time.Millisecond, ReadTimeout: 5 * time.Second})
	}()

	select {
	case payload := <-respCh:
		if !strings.Contains(string(payload), "local:/probe") {
			t.Fatalf("响应内容不对: %q", payload)
		}
		t.Log("✅ 转发请求已代理到本地服务并原样返回")
	case <-time.After(10 * time.Second):
		t.Fatal("客户端没有回响应")
	}
}
