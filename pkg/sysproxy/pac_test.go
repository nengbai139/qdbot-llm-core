package sysproxy

import (
	"strings"
	"testing"
)

// TestRenderPACUsesPort 端口必须替换进 PAC，且模板默认端口不能残留。
func TestRenderPACUsesPort(t *testing.T) {
	body := RenderPAC(1088, nil)
	if !strings.Contains(body, "PROXY 127.0.0.1:1088") {
		t.Fatalf("port not applied:\n%s", body)
	}
	if strings.Contains(body, "127.0.0.1:1080") {
		t.Fatal("template port left behind")
	}
}

// TestRenderPACUsesHTTPProxy 锁定回归：PAC 返回值必须用 PROXY（HTTP 代理），不能用 SOCKS5。
// SOCKS5 在 Chrome 系浏览器的 PAC 里是「本机解析 DNS」，撞上 GFW 污染后 google/tiktok
// 拿到假 IP 打不开；只有 HTTP CONNECT（PROXY）能强制代理端解析 DNS。
func TestRenderPACUsesHTTPProxy(t *testing.T) {
	body := RenderPAC(1080, nil)
	if !strings.Contains(body, `return "PROXY 127.0.0.1:1080"`) {
		t.Fatalf("PAC 未使用 PROXY（HTTP 代理）:\n%s", body)
	}
	if strings.Contains(body, `return "SOCKS5`) || strings.Contains(body, `return "SOCKS `) {
		t.Fatal("PAC 返回值用了 SOCKS/SOCKS5（Chrome 系会本机解析 DNS，撞污染）")
	}
}

// TestRenderPACInjectsRules config.toml 的 proxy.rules 必须真的进 PAC，
// 否则这个字段只是存下来不生效（历史 bug：rules 写了但路由完全不看它）。
func TestRenderPACInjectsRules(t *testing.T) {
	body := RenderPAC(1080, []string{"google.com", "gmail.com"})
	if !strings.Contains(body, `"google.com"`) || !strings.Contains(body, `"gmail.com"`) {
		t.Fatalf("rules 未注入 PAC:\n%s", body)
	}
	if strings.Contains(body, "__EXTRA_RULES__") {
		t.Fatal("占位符未替换，PAC 会语法错误")
	}
	if !strings.Contains(body, "function matchRules(host)") {
		t.Fatal("matchRules 未生成，rules 不会被匹配")
	}
}

// TestRenderPACNoHardcodedDomains 分流规则只能来自 config.toml/WebUI，
// 不能在 PAC 模板里写死域名表——那是代码常量，和产品配置冲突。
func TestRenderPACNoHardcodedDomains(t *testing.T) {
	body := RenderPAC(1080, []string{"google.com"})
	for _, hardcoded := range []string{"baidu.com", "taobao.com", "isChina", "qq.com", "jd.com"} {
		if strings.Contains(body, hardcoded) {
			t.Errorf("PAC 模板里残留硬编码域名 %q，应完全由 proxy.rules 驱动", hardcoded)
		}
	}
}

// TestRenderPACEmptyRules 空列表必须渲染成合法的空数组，而不是留占位符。
func TestRenderPACEmptyRules(t *testing.T) {
	body := RenderPAC(1080, nil)
	if !strings.Contains(body, "var PROXY_DOMAINS = [];") {
		t.Fatalf("空 rules 未渲染成 []:\n%s", firstLines(body, 3))
	}
}

// TestRenderRulesSanitizes 域名来自用户输入，含引号/分号会打断 PAC 语法，必须剔除。
func TestRenderRulesSanitizes(t *testing.T) {
	got := renderRules([]string{
		"google.com",
		"  Gmail.com  ",   // 应去空格 + 转小写
		"google.com",      // 应去重
		`bad";alert(1);"`, // 应被剔除
		"back\\slash",     // 应被剔除
	})
	want := `["gmail.com", "google.com"]`
	if got != want {
		t.Errorf("renderRules() = %s, want %s", got, want)
	}
}

// TestRenderRulesEmpty 空输入给 []，保证 PAC 永远语法合法。
func TestRenderRulesEmpty(t *testing.T) {
	if got := renderRules(nil); got != "[]" {
		t.Errorf("renderRules(nil) = %s, want []", got)
	}
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
