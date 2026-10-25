// Package tunnel 反向隧道客户端：连接公网 tunnel-server，把服务器转发的 HTTP 请求代理到本地服务。
// 从 cmd/tunnel-client 抽取出来，供 qtunnel 与独立 tunnel-client 复用。
package tunnel

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nengbai139/qdbot-llm-core/pkg/protocol"
)

// 缺省心跳与读超时。读超时要比心跳长得多：偶尔丢一个心跳不该判死。
const (
	defaultHeartbeat   = 20 * time.Second
	defaultReadTimeout = 90 * time.Second
)

// Options 隧道保活参数（零值取缺省值）。
type Options struct {
	// Heartbeat 心跳发送间隔。
	Heartbeat time.Duration
	// ReadTimeout 确认对端支持心跳后，多久收不到任何数据就判定链路已死。
	// 未确认对端支持心跳前不启用，避免把老服务端误判成死连接。
	ReadTimeout time.Duration
}

func (o Options) normalized() Options {
	if o.Heartbeat <= 0 {
		o.Heartbeat = defaultHeartbeat
	}
	if o.ReadTimeout <= 0 {
		o.ReadTimeout = defaultReadTimeout
	}
	return o
}

// Run 连接 tunnel-server 并把请求转发到 locals，直到出错或 ctx 取消返回（由调用方负责重连）。
// locals 按顺序尝试，前一个拨号失败才用下一个。clientID 非空时作为隧道子域名；subscribeToken 非空时作为订阅令牌随握手发送。
func Run(ctx context.Context, serverAddr string, locals []string, clientID, subscribeToken string, onAssigned func(string)) error {
	return RunWithOptions(ctx, serverAddr, locals, clientID, subscribeToken, onAssigned, Options{})
}

// RunWithOptions 带保活参数的隧道连接。
//
// 心跳是必须的：中间设备会静默掐断长连接（现场表现为每约一小时断一次），
// 没有心跳就只能等系统 TCP keepalive 才发现，那次整整等了一个小时。
func RunWithOptions(ctx context.Context, serverAddr string, locals []string, clientID, subscribeToken string, onAssigned func(string), opts Options) error {
	opts = opts.normalized()
	conn, err := net.DialTimeout("tcp", serverAddr, 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(30 * time.Second)
	}
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
	log.Printf("connected to %s", serverAddr)
	if clientID != "" {
		if _, err := conn.Write([]byte("CLIENT_ID " + clientID + "\n")); err != nil {
			return err
		}
	}
	if subscribeToken != "" {
		if _, err := conn.Write([]byte("SUBSCRIBE " + subscribeToken + "\n")); err != nil {
			return err
		}
	}

	var mu sync.Mutex
	// 心跳：定时发 PING，服务端回 PONG。写失败说明链路已断，
	// 直接关连接，让下面的读循环立刻退出并触发重连。
	beatDone := make(chan struct{})
	defer close(beatDone)
	go func() {
		ticker := time.NewTicker(opts.Heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-beatDone:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := writeFrame(conn, &mu, protocol.TypePing, 0, nil); err != nil {
					_ = conn.Close()
					return
				}
			}
		}
	}()

	br := bufio.NewReader(conn)
	var assignedReported bool
	sawPong := false
	for {
		if sawPong {
			// 确认对端支持心跳后才启用读超时：老服务端不会回 PONG，
			// 无条件启用会把好端端误判成死连接。
			_ = conn.SetReadDeadline(time.Now().Add(opts.ReadTimeout))
		}
		typ, id, payload, err := protocol.ReadFrame(br)
		if err != nil {
			return err
		}
		switch typ {
		case protocol.TypeAssigned:
			url := string(payload)
			log.Printf("Your URL: %s", url)
			if !assignedReported && onAssigned != nil {
				assignedReported = true
				onAssigned(url)
			}
		case protocol.TypePong:
			sawPong = true
		case protocol.TypeRequest:
			go func(id uint64, reqBytes []byte) {
				resp := proxyToLocal(locals, reqBytes)
				sendResponse(conn, &mu, id, resp)
			}(id, payload)
		}
	}
}

func proxyToLocal(locals []string, reqBytes []byte) []byte {
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(reqBytes)))
	if err != nil {
		return errorResponse(500, "bad request")
	}
	defer req.Body.Close()
	body, _ := io.ReadAll(req.Body)
	client := &http.Client{Timeout: 60 * time.Second}
	var last error
	tried := 0
	for _, addr := range locals {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		tried++
		out := req.Clone(context.Background())
		out.URL.Scheme = "http"
		out.URL.Host = addr
		out.RequestURI = ""
		out.Body = io.NopCloser(bytes.NewReader(body))
		out.ContentLength = int64(len(body))
		resp, err := client.Do(out)
		if err != nil {
			last = err
			continue
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		var buf bytes.Buffer
		_ = resp.Write(&buf)
		return buf.Bytes()
	}
	if tried == 0 {
		return errorResponse(502, "no local address")
	}
	return errorResponse(502, last.Error())
}

func errorResponse(code int, msg string) []byte {
	resp := &http.Response{
		StatusCode: code,
		Status:     http.StatusText(code) + ": " + msg,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewBufferString(msg)),
	}
	var buf bytes.Buffer
	_ = resp.Write(&buf)
	return buf.Bytes()
}

func sendResponse(conn net.Conn, mu *sync.Mutex, id uint64, respBytes []byte) {
	_ = writeFrame(conn, mu, protocol.TypeResponse, id, respBytes)
}

// writeFrame 写一帧。写操作必须串行：心跳和响应共用一条连接，
// 交错写会把两帧拼成一团乱码。
func writeFrame(conn net.Conn, mu *sync.Mutex, typ byte, id uint64, payload []byte) error {
	frame := protocol.Encode(typ, id, payload)
	mu.Lock()
	defer mu.Unlock()
	_, err := conn.Write(frame)
	return err
}
