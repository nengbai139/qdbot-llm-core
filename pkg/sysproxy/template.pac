// qdbot 分流规则：命中 PROXY_DOMAINS 的走本机 HTTP 代理（CONNECT），其余一律直连。
// 由代理启动时自动装上，停止时卸下。
//
// 规则来源：config.toml 的 proxy.rules，可在 WebUI(8900) 或 config.toml 里改，
// 不在本文件里写死任何域名——分流是产品配置，不是代码常量。
//
// 为什么用 PROXY（HTTP 代理）而不是 SOCKS5：
//   - HTTP CONNECT 在协议层就要求浏览器把「域名」发给代理，代理在海外节点解析 DNS，
//     天然绕开 GFW 本地 DNS 污染。
//   - SOCKS5 在 Chrome 系浏览器的 PAC 里是「本机解析 DNS」：先在本机把 google.com 解析成
//     污染后的假 IP，再拿假 IP 交给代理 → 假 IP 黑洞超时，google/tiktok 打不开。
//   - SOCKS5h 又是 curl/libcurl 的私有约定，浏览器 PAC 解析器不认，会退回直连。
//     所以浏览器这条链路只能用 PROXY。
//   - 命令行（curl/git/LLM）仍走 socks5h://127.0.0.1:<port>（代理端解析 DNS），
//     与本 PAC 无关、不受影响。
var PROXY_DOMAINS = __EXTRA_RULES__;

function FindProxyForURL(url, host) {
    host = (host || "").toLowerCase();
    if (!host || host === "localhost" || host === "127.0.0.1" || /^\d+\.\d+\.\d+\.\d+$/.test(host)) {
        return "DIRECT";
    }
    if (matchRules(host)) {
        return "PROXY 127.0.0.1:1080";
    }
    return "DIRECT";
}

function matchRules(host) {
    for (var i = 0; i < PROXY_DOMAINS.length; i++) {
        var d = PROXY_DOMAINS[i];
        // 配 "google.com" 同时覆盖 www.google.com / accounts.google.com 等子域
        if (host === d || host.slice(-(d.length + 1)) === "." + d) return true;
    }
    return false;
}
