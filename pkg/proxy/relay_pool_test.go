package proxy

import (
	"testing"
	"time"

	"github.com/nengbai139/qdbot-llm-core/pkg/config"
)

// configForTest 造一份只带 relays 的配置。
func configForTest(relays []string) config.Config {
	return config.Config{Proxy: config.ProxyConfig{Relays: relays}}
}

// TestRelayPoolOrder 锁定中继池的轮询与冷却行为：
//
//  1. 三台节点轮询起点依次前移（负载均摊）
//  2. 故障节点进入冷却后**直接跳过**，不再排在最后拖慢每条连接
//  3. 冷却期满后自动恢复参与轮询
//  4. 全部冷却时退化为完整列表（宁可慢也不能没出口）
func TestRelayPoolOrder(t *testing.T) {
	p := &relayPool{cooling: map[string]time.Time{}, failed: map[string]int{}}
	addrs := []string{"a:22", "b:22", "c:22"}

	// 1. 轮询起点依次前移
	first := p.order(addrs)
	if len(first) != 3 {
		t.Fatalf("应有 3 个节点，得到 %v", first)
	}
	if first[0] != "a:22" {
		t.Fatalf("第一次应从 a 开始，得到 %v", first)
	}
	second := p.order(addrs)
	if second[0] != "b:22" {
		t.Fatalf("第二次应从 b 开始，得到 %v", second)
	}
	third := p.order(addrs)
	if third[0] != "c:22" {
		t.Fatalf("第三次应从 c 开始，得到 %v", third)
	}

	// 2. 故障节点进入冷却后被跳过（不是排到最后）
	p.markFailed("b:22")
	got := p.order(addrs)
	for _, a := range got {
		if a == "b:22" {
			t.Fatalf("冷却中的 b 不该出现: %v", got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("应有 2 个可用节点，得到 %v", got)
	}

	// 3. 冷却期满后恢复
	p.mu.Lock()
	p.cooling["b:22"] = time.Now().Add(-time.Second)
	p.mu.Unlock()
	got = p.order(addrs)
	found := false
	for _, a := range got {
		if a == "b:22" {
			found = true
		}
	}
	if !found {
		t.Fatalf("冷却期满后 b 应恢复: %v", got)
	}

	// 4. 全部冷却 → 退化为完整列表
	p.markFailed("a:22")
	p.markFailed("b:22")
	p.markFailed("c:22")
	got = p.order(addrs)
	if len(got) != 3 {
		t.Fatalf("全部冷却时应返回完整列表，得到 %v", got)
	}
}

// TestRelayPoolCooldownEscalates 连续失败的节点冷却时长指数增长。
//
// 为什么要涨：彻底宕机的节点不该每分钟被戳一次——每次探测赔一个连接超时，
// 等于把延迟周期性甩给用户。恢复后计数清零，重新从 60 秒开始。
func TestRelayPoolCooldownEscalates(t *testing.T) {
	p := &relayPool{cooling: map[string]time.Time{}, failed: map[string]int{}}
	addr := "dead:22"

	cooldownOf := func() time.Duration {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.cooling[addr].Sub(time.Now())
	}

	p.markFailed(addr)
	first := cooldownOf()
	if first < 50*time.Second || first > 70*time.Second {
		t.Fatalf("首次失败冷却应约 60s，实际 %v", first)
	}

	p.markFailed(addr)
	second := cooldownOf()
	if second < 110*time.Second || second > 130*time.Second {
		t.Fatalf("二次失败冷却应约 120s，实际 %v", second)
	}

	// 连败多次后封顶 10 分钟
	for i := 0; i < 10; i++ {
		p.markFailed(addr)
	}
	if c := cooldownOf(); c > cooldownMax {
		t.Fatalf("冷却应封顶 %v，实际 %v", cooldownMax, c)
	}

	// 恢复后计数清零，冷却重新从 60 秒开始
	p.markHealthy(addr)
	p.markFailed(addr)
	if c := cooldownOf(); c > 70*time.Second {
		t.Fatalf("恢复后冷却应回到约 60s，实际 %v", c)
	}
}

// TestRelayAddrs 平台下发的节点列表会被过滤空串；为空时不再回落内置地址。
func TestRelayAddrs(t *testing.T) {
	cfg := configForTest([]string{" x:22 ", "", "y:22"})
	got := relayAddrs(cfg)
	if len(got) != 2 || got[0] != "x:22" || got[1] != "y:22" {
		t.Fatalf("平台列表应过滤空串，得到 %v", got)
	}

	// 没有平台列表就没有节点：节点地址只由服务端下发，不在代码里写死。
	if got = relayAddrs(configForTest(nil)); len(got) != 0 {
		t.Fatalf("无平台列表时应为空，得到 %v", got)
	}
}
