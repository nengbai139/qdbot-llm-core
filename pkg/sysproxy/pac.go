package sysproxy

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed template.pac
var template string

// RenderPAC 生成分流脚本：命中 extraRules 的域名走本机 HTTP 代理（CONNECT，代理端解析 DNS），
// 其余直连。port 为本机混合代理监听端口（HTTP+SOCKS5 同端口）；extraRules 来自
// config.toml 的 proxy.rules（可在 WebUI 或配置文件里改），不在代码里写死域名。
//
// 两处替换都必须做：
//   - __EXTRA_RULES__ 替换成 JS 数组字面量，否则 PAC 直接语法错误 → 浏览器当没有代理用。
//     注意必须做 JS 转义，域名含引号或反斜杠会把 PAC 打断（配置来自用户输入）。
//   - 127.0.0.1:1080 替换成实际端口。
func RenderPAC(port int, extraRules []string) string {
	if port <= 0 {
		port = 1080
	}
	body := strings.ReplaceAll(template, "__EXTRA_RULES__", renderRules(extraRules))
	return strings.ReplaceAll(body, "127.0.0.1:1080", fmt.Sprintf("127.0.0.1:%d", port))
}

// renderRules 把域名列表渲染成 JS 数组字面量。空列表给 []，非法字符直接剔除。
func renderRules(rules []string) string {
	seen := make(map[string]bool, len(rules))
	clean := make([]string, 0, len(rules))
	for _, r := range rules {
		r = strings.ToLower(strings.TrimSpace(r))
		if r == "" || seen[r] {
			continue
		}
		// 只放行形如 host / *.host 的安全字符，杜绝引号、反斜杠、分号等破坏 PAC 语法
		if strings.ContainsAny(r, "\"'`\\;{}\n\r") {
			continue
		}
		seen[r] = true
		clean = append(clean, r)
	}
	sort.Strings(clean)
	if len(clean) == 0 {
		return "[]"
	}
	quoted := make([]string, len(clean))
	for i, r := range clean {
		quoted[i] = fmt.Sprintf("%q", r)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
