package proxy

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// TestRelayE2E 真机联调：客户端 → 海外中继 → 目标站点。
//
// 默认跳过（需要真实订阅令牌 + 公网中继），显式打开：
//
//	RELAY_E2E=1 RELAY_E2E_ADDRS="host:22,host:22" go test ./pkg/proxy -run TestRelayE2E -v
//
// 节点地址必须显式给（RELAY_E2E_ADDRS）：中继地址是运营信息，不在代码里写死。
func TestRelayE2E(t *testing.T) {
	if os.Getenv("RELAY_E2E") == "" {
		t.Skip("需要真实中继环境，设置 RELAY_E2E=1 后运行")
	}

	var addrs []string
	for _, a := range strings.Split(os.Getenv("RELAY_E2E_ADDRS"), ",") {
		if a = strings.TrimSpace(a); a != "" {
			addrs = append(addrs, a)
		}
	}
	if len(addrs) == 0 {
		t.Skip("需要用 RELAY_E2E_ADDRS 指定中继节点")
	}

	token := fetchToken()
	if token == "" {
		t.Fatal("拉不到订阅令牌：先 qtunnel login/verify 并确认订阅有效")
	}

	const target = "www.google.com:443"
	for _, addr := range addrs {
		t.Run(addr, func(t *testing.T) {
			conn, br, err := dialRelay(addr, token, target)
			if err != nil {
				t.Fatalf("中继握手失败: %v", err)
			}
			defer conn.Close()

			tlsConn := tls.Client(&readerConn{Conn: conn, r: br}, &tls.Config{ServerName: "www.google.com"})
			_ = tlsConn.SetDeadline(time.Now().Add(20 * time.Second))
			if err := tlsConn.Handshake(); err != nil {
				t.Fatalf("TLS 握手失败（说明流量没真到目标）: %v", err)
			}
			fmt.Fprintf(tlsConn, "GET / HTTP/1.1\r\nHost: www.google.com\r\nConnection: close\r\nUser-Agent: qdbot-relay-e2e\r\n\r\n")
			resp, err := io.ReadAll(io.LimitReader(tlsConn, 4096))
			if err != nil {
				t.Fatalf("读响应失败: %v", err)
			}
			if !strings.HasPrefix(string(resp), "HTTP/1.1 200") {
				t.Fatalf("响应异常: %q", string(resp[:min(len(resp), 80)]))
			}
			t.Logf("中继 %s 出网成功：%s", addr, strings.SplitN(string(resp), "\r\n", 2)[0])
		})
	}
}

// readerConn 把 bufio.Reader 里已缓冲的数据接在 Conn 前面。
// 中继握手后目标的首段数据可能已在 bufio 里，直接读 conn 会丢掉它。
type readerConn struct {
	net.Conn
	r interface {
		Read([]byte) (int, error)
	}
}

func (c *readerConn) Read(p []byte) (int, error) { return c.r.Read(p) }
