// Package proxy 正向代理（relay 模式）：本地 HTTP+SOCKS5 混合代理，每个连接经海外中继出网。
// 客户端不持 SSH 私钥——私钥留在海外节点的 tunnel-relay 服务端，客户端只发订阅令牌，
// 所以本包零密钥、可开源。
package proxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nengbai139/qdbot-llm-core/pkg/auth"
	"github.com/nengbai139/qdbot-llm-core/pkg/config"
	"github.com/nengbai139/qdbot-llm-core/pkg/subscription"

	"github.com/nengbai139/qdbot-llm-core/pkg/sysproxy"
)

// relayAddrs 决定本次用哪些中继：只认平台下发的 proxy.relays。
//
// 节点地址是运营信息，不进开源仓库——由平台下发给客户端（config.toml 的
// proxy.relays），换节点/加节点不用发客户端新版本。列表为空说明还没拉到
// 平台配置，直接报错，不静默降级到写死的地址。
func relayAddrs(cfg config.Config) []string {
	var out []string
	for _, a := range cfg.Proxy.Relays {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// relayPool 中继节点池：轮询起点 + 故障节点冷却。
//
// 轮询：三台节点地位平等，每个新连接换一个起点，负载均摊。
// 冷却：某台连接失败后拉黑一段时间（避免每条连接都先在死节点上等一次超时），
// 冷却期满自动恢复参与轮询。
type relayPool struct {
	mu      sync.Mutex
	counter uint64
	cooling map[string]time.Time // addr -> 冷却截止时间
	failed  map[string]int       // addr -> 连续失败次数（决定冷却时长）
}

var pool = &relayPool{cooling: map[string]time.Time{}, failed: map[string]int{}}

// cooldownDuration 首次失败的冷却时长。
const cooldownDuration = 60 * time.Second

// cooldownMax 冷却时长上限。
const cooldownMax = 10 * time.Minute

// order 返回本次尝试中继的顺序：从轮询起点开始。
//
// 冷却中的节点直接跳过（不是排到最后）：排到最后意味着每条连接都要先在死节点上
// 白等一次 10 秒连接超时，延迟会叠加到所有请求上。冷却期满后自动重新参与轮询。
//
// 兜底：若所有节点都在冷却（刚集体失败），退化为完整列表——宁可慢，也不能没有出口。
func (p *relayPool) order(addrs []string) []string {
	if len(addrs) == 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counter++
	start := int(p.counter-1) % len(addrs)
	now := time.Now()
	out := make([]string, 0, len(addrs))
	for i := 0; i < len(addrs); i++ {
		a := addrs[(start+i)%len(addrs)]
		if until, ok := p.cooling[a]; ok && now.Before(until) {
			continue
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		return append([]string(nil), addrs...)
	}
	return out
}

// markFailed 标记节点故障，进入冷却。
//
// 冷却时长随连续失败次数指数增长（60s → 120s → … 上限 10 分钟）：
// 节点彻底宕机时不该每分钟都去戳它一次——每次探测都要赔上一个连接超时，
// 等于把延迟周期性甩给用户。恢复后 markHealthy 会把计数清零。
func (p *relayPool) markFailed(addr string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failed[addr]++
	d := cooldownDuration << uint(p.failed[addr]-1)
	if d > cooldownMax || d <= 0 {
		d = cooldownMax
	}
	p.cooling[addr] = time.Now().Add(d)
}

// markHealthy 清掉节点的冷却状态（恢复了就立刻重新参与轮询）。
func (p *relayPool) markHealthy(addr string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.cooling, addr)
	delete(p.failed, addr)
}

// Start 启动本地 HTTP+SOCKS5 混合代理并阻塞；ctx 取消时关闭监听退出（由调用方负责停）。
func Start(ctx context.Context, cfg config.Config) error {
	src := newTokenSource()
	if src.get() == "" {
		return errors.New("拉订阅令牌失败：未登录或订阅无效（先 qtunnel login/verify 并确认订阅）")
	}
	relays := relayAddrs(cfg)
	if len(relays) == 0 {
		return errors.New("未配置中继节点：先从服务端拉一次配置（proxy.relays 由服务端下发）")
	}

	bind := strings.TrimSpace(cfg.Proxy.Bind)
	if bind == "" {
		bind = "127.0.0.1"
	}
	if err := checkBindLocal(bind); err != nil {
		return err
	}
	addr := net.JoinHostPort(bind, strconv.Itoa(cfg.Proxy.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", addr, err)
	}
	defer ln.Close()

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	if err := sysproxy.Install(cfg.Proxy.Port, cfg.Proxy.Rules); err != nil {
		log.Printf("系统分流未装上: %v", err)
	}
	defer sysproxy.Clear()

	log.Printf("代理已启动 %s -> 海外中继 %s（HTTP+SOCKS5 同端口，%d 节点轮询）", addr, strings.Join(relays, ","), len(relays))

	// 后台探测节点健康度：把「发现故障」提前到用户请求之前。
	// 首轮探测最多等 8 秒，让用户的第一条连接不必自己踩到故障节点。
	probed := probeRelays(ctx, src, relays)
	select {
	case <-probed:
	case <-time.After(8 * time.Second):
		log.Printf("中继首轮探测未完成，先用现有节点服务")
	case <-ctx.Done():
		return nil
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			return err // ctx 取消后 ln.Close() 会让 Accept 报错，正好退出
		}
		go serveConn(conn, src, relays)
	}
}

// 中继健康探测的参数。
const (
	// relayProbeInterval 探测周期。
	relayProbeInterval = 60 * time.Second
	// relayProbeTarget 探测用的目标地址：只要求能建立 TCP 连接，不发任何数据。
	// 选一个海外节点能稳定连上的目标即可。
	relayProbeTarget = "www.google.com:443"
)

// probeWithToken 并发探测所有中继节点，把结果写进节点池。
func probeWithToken(token string, relays []string) {
	var wg sync.WaitGroup
	for _, addr := range relays {
		wg.Add(1)
		go func(a string) {
			defer wg.Done()
			conn, _, err := dialRelayWithTimeout(a, token, relayProbeTarget, relayProbeTimeout)
			if err != nil {
				pool.markFailed(a)
				log.Printf("中继 %s 探测失败: %v", a, err)
				return
			}
			_ = conn.Close()
			pool.markHealthy(a)
		}(addr)
	}
	wg.Wait()
}

// probeRelays 后台并发探测所有中继节点，把结果写进节点池；返回的 channel 在首轮
// 探测结束后关闭（调用方可用它等待首轮结果）。
//
// 为什么要有这个：节点宕机时，用户连接要等一个 10 秒超时才发现不对——
// 而且轮询起点恰好落在故障节点上时，每个冷却窗口都会付一次这个延迟。
// 后台探测把故障发现提前，故障节点直接被冷却跳过，用户侧无感。
func probeRelays(ctx context.Context, src *tokenSource, relays []string) <-chan struct{} {
	done := make(chan struct{})
	first := make(chan struct{})
	var once sync.Once

	probeOnce := func() {
		token := src.get()
		if token == "" {
			return
		}
		probeWithToken(token, relays)
	}

	go func() {
		probeOnce()
		once.Do(func() { close(first) })
		ticker := time.NewTicker(relayProbeInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				close(done)
				return
			case <-ticker.C:
				probeOnce()
			}
		}
	}()
	return first
}

// serveConn 单端口混合代理：按首个字节区分协议再分发。
// SOCKS5 首字节是 0x05，HTTP 首字节是方法名首字母（C/G/P…），互不冲突。
func serveConn(conn net.Conn, src *tokenSource, relays []string) {
	br := bufio.NewReader(conn)
	first, err := br.Peek(1)
	if err != nil {
		_ = conn.Close()
		return
	}
	if first[0] == 0x05 {
		handleSocks5(conn, br, src, relays)
		return
	}
	handleHTTP(conn, br, src, relays)
}

// handleSocks5 处理单个 SOCKS5 连接：无认证握手 + CONNECT + 经中继转发。
func handleSocks5(c net.Conn, br *bufio.Reader, src *tokenSource, relays []string) {
	defer c.Close()
	buf := make([]byte, 258)
	if _, err := io.ReadFull(br, buf[:2]); err != nil {
		return
	}
	if buf[0] != 0x05 {
		return
	}
	nmethods := int(buf[1])
	if _, err := io.ReadFull(br, buf[:nmethods]); err != nil {
		return
	}
	if _, err := c.Write([]byte{0x05, 0x00}); err != nil { // 选择无认证
		return
	}

	if _, err := io.ReadFull(br, buf[:4]); err != nil {
		return
	}
	if buf[1] != 0x01 { // 只支持 CONNECT
		return
	}
	var host string
	switch buf[3] {
	case 0x01: // IPv4
		if _, err := io.ReadFull(br, buf[:4]); err != nil {
			return
		}
		host = net.IP(buf[:4]).String()
	case 0x03: // 域名
		if _, err := io.ReadFull(br, buf[:1]); err != nil {
			return
		}
		l := int(buf[0])
		if _, err := io.ReadFull(br, buf[:l]); err != nil {
			return
		}
		host = string(buf[:l])
	case 0x04: // IPv6
		if _, err := io.ReadFull(br, buf[:16]); err != nil {
			return
		}
		host = net.IP(buf[:16]).String()
	default:
		return
	}
	if _, err := io.ReadFull(br, buf[:2]); err != nil {
		return
	}
	port := int(binary.BigEndian.Uint16(buf[:2]))
	target := net.JoinHostPort(host, strconv.Itoa(port))

	relayConn, relayBr, err := relayDial(src.get(), target, relays)
	if err != nil {
		_, _ = c.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) // 连接失败
		return
	}
	defer relayConn.Close()

	if _, err := c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	relayTunnel(c, br, relayConn, relayBr)
}

// handleHTTP 处理单个 HTTP 代理连接：CONNECT 隧道（HTTPS）+ 绝对 URL 转发（纯 HTTP）。
func handleHTTP(c net.Conn, br *bufio.Reader, src *tokenSource, relays []string) {
	defer c.Close()
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}

	if req.Method == http.MethodConnect {
		target := req.Host
		if target == "" {
			target = req.URL.Host
		}
		if _, _, err := net.SplitHostPort(target); err != nil {
			target = net.JoinHostPort(target, "443")
		}
		relayConn, relayBr, err := relayDial(src.get(), target, relays)
		if err != nil {
			_, _ = c.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
			return
		}
		defer relayConn.Close()
		if _, err := c.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
			return
		}
		relayTunnel(c, br, relayConn, relayBr)
		return
	}

	if req.URL == nil || !req.URL.IsAbs() {
		return
	}
	target := req.URL.Host
	if !strings.Contains(target, ":") {
		target = net.JoinHostPort(target, "80")
	}
	relayConn, relayBr, err := relayDial(src.get(), target, relays)
	if err != nil {
		_, _ = c.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer relayConn.Close()
	req.RequestURI = ""
	req.URL.Scheme = ""
	req.URL.Host = ""
	if err := req.Write(relayConn); err != nil {
		return
	}
	relayTunnel(c, br, relayConn, relayBr)
}

// relayDial 连海外中继，发订阅令牌 + CONNECT 目标，返回 (conn, br)。
// br 可能已缓冲目标返回的首段数据（握手后紧跟），转发时必须用它读。
//
// 多节点：按轮询顺序逐个尝试，某台失败就换下一台（失败节点进入冷却，
// 免得每条连接都在死节点上白等一次超时）。
func relayDial(token, target string, addrs []string) (net.Conn, *bufio.Reader, error) {
	if len(addrs) == 0 {
		return nil, nil, errors.New("没有可用的中继节点")
	}
	var lastErr error
	for _, addr := range pool.order(addrs) {
		conn, br, err := dialRelay(addr, token, target)
		if err == nil {
			pool.markHealthy(addr)
			return conn, br, nil
		}
		lastErr = err
		pool.markFailed(addr)
		log.Printf("中继 %s 不可用: %v", addr, err)
	}
	return nil, nil, fmt.Errorf("所有中继节点均不可用: %w", lastErr)
}

// relayHandshakeTimeout 中继握手（发出 SUBSCRIBE + CONNECT，等回 OK）的超时。
//
// 为什么必须有：节点可能处于「TCP 能连上但不回话」的半死状态（内核假死、
// 进程卡住）。没有读超时的话 ReadString 会一直挂着，客户端这条连接永久卡死。
const relayHandshakeTimeout = 10 * time.Second

// relayHandshakeTimeoutSec 便于测试把握手超时调短（生产值仍是 10 秒）。
var relayHandshakeTimeoutSec = relayHandshakeTimeout

// relayDialTimeout 建立 TCP 连接的超时。
//
// 海外节点的正常握手延迟在几百毫秒量级，5 秒足够；再长只是让故障节点
// 拖慢每条连接（后台探测会独立感知节点状态，用户流量不该为此付钱）。
const relayDialTimeout = 5 * time.Second

// relayProbeTimeout 探测专用的握手超时，比用户流量更短。
//
// 健康节点几百毫秒就应答（令牌校验结果在中继侧缓存 300 秒），3 秒足够；
// 死节点早判定早拉黑，代理启动后的第一批用户请求就不会踩到它。
const relayProbeTimeout = 3 * time.Second

// dialRelay 连单个中继节点并完成握手（用户流量用默认超时）。
func dialRelay(addr, token, target string) (net.Conn, *bufio.Reader, error) {
	return dialRelayWithTimeout(addr, token, target, relayHandshakeTimeoutSec)
}

// dialRelayWithTimeout 连单个中继节点并完成握手，握手超时可指定。
func dialRelayWithTimeout(addr, token, target string, timeout time.Duration) (net.Conn, *bufio.Reader, error) {
	conn, err := net.DialTimeout("tcp", addr, relayDialTimeout)
	if err != nil {
		return nil, nil, err
	}
	// 握手阶段整段限时：连上但不应答的节点必须在超时内放弃，否则连接永久卡住。
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := fmt.Fprintf(conn, "SUBSCRIBE %s\nCONNECT %s\n", token, target); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "OK" {
		_ = conn.Close()
		if err != nil && isTimeout(err) {
			return nil, nil, fmt.Errorf("中继握手超时（%s 无响应）: %w", addr, err)
		}
		return nil, nil, errors.New("中继握手失败")
	}
	// 握手完成后交给隧道使用，不应再有总时限。
	_ = conn.SetDeadline(time.Time{})
	return conn, br, nil
}

// isTimeout 判断错误是否超时（含被包装过的情况）。
func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// relayTunnel 双向转发：clientBr→relayConn，relayBr→clientConn。
func relayTunnel(clientConn net.Conn, clientBr *bufio.Reader, relayConn net.Conn, relayBr *bufio.Reader) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(relayConn, clientBr)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(clientConn, relayBr)
		done <- struct{}{}
	}()
	<-done
}

// tokenSource 缓存订阅令牌，过期（5 分钟）自动重拉。
type tokenSource struct {
	mu      sync.Mutex
	token   string
	fetched time.Time
}

func newTokenSource() *tokenSource { return &tokenSource{} }

func (s *tokenSource) get() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token == "" || time.Since(s.fetched) > 5*time.Minute {
		s.token = fetchToken()
		s.fetched = time.Now()
	}
	return s.token
}

// fetchToken 拉订阅令牌（服务端签名）。客户端不持 auth_secret。
func fetchToken() string {
	st, err := auth.Open("")
	if err != nil {
		return ""
	}
	a, err := st.Load()
	if err != nil || a == nil || a.Token == "" || a.ServerURL == "" {
		return ""
	}
	token, _, err := subscription.FetchToken(a.ServerURL, a.Token)
	if err != nil {
		return ""
	}
	return token
}

// checkBindLocal 在监听前确认 bind 地址确实存在于本机。
//
// 为什么需要：proxy.bind 是**每设备**配置（服务端「默认配置」下发到所有设备，
// 「设备专属配置」只下发到一台）。最典型的翻车是管理员把服务器用的 docker 网关
// 地址（如 172.19.0.1）填进了**默认配置**，于是每台设备都去监听那个地址——
// 而它在笔记本上根本不存在，net.Listen 只会报一个含糊的 "cannot assign requested
// address"，用户完全看不出是「配置填错地方了」。
//
// 这里提前解析一次并给出可执行的提示，把排查成本从「看日志猜」降到「读一句话」。
// 0.0.0.0 / :: 跳过检查（它们永远可绑，语义是「所有网卡」）。
func checkBindLocal(bind string) error {
	if bind == "0.0.0.0" || bind == "::" || bind == "[::]" {
		return nil
	}
	ip := net.ParseIP(bind)
	if ip == nil {
		// 不是 IP（主机名），交给 net.Listen 去解析；这里不拦。
		return nil
	}
	if ip.IsLoopback() || ip.IsUnspecified() {
		return nil
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil // 拿不到网卡信息就不拦，让 Listen 自己报错
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.Equal(ip) {
			return nil
		}
	}
	return fmt.Errorf("proxy.bind %q 在本机不存在：请确认这是本机的地址（如 docker 网关 172.x.0.1），"+
		"且只配在「设备专属配置」里——填到「默认配置」会下发到所有设备，导致其它设备代理起不来", bind)
}
