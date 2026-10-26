# qdbot-llm-core

把本地 [Ollama](https://ollama.com) 模型变成一个 **Anthropic 兼容** 的 API 服务，让 Claude Code、Anthropic SDK 等工具直接接入本地模型。

- **本地运行**：模型与数据不出你的机器，完全离线可用
- **流式输出**：支持 SSE 流式、tool_use、thinking
- **核心零依赖**：协议翻译核心纯 Go 标准库；隧道客户端仅依赖 toml / yaml 两个开源库

## 免费开源 · 平台会员

**客户端（本仓库）完整开源、免费**：`serve` / `pull` / `list` / `install` / `expose` / `daemon` 全部子命令都在这里，本地跑模型、接 Claude Code 不花一分钱。

`expose`（一键接入平台：把本地模型反向代理到公网，供人调用或按量售卖）和 `daemon` 需要**注册平台会员（年费）**才能用——但这个收费由**平台服务端校验订阅**实现，客户端代码本身开源、零密钥。

**不在本仓库的闭源部分**：中继节点服务端（`tunnel-relay`）、市场网关（`llm-gateway` 鉴权 / 路由 / 计量 / 结算）。

**正向代理（`pkg/proxy`）也已开源**：本地 HTTP+SOCKS5 同端口代理，流量经海外中继出网。
客户端零密钥——不持任何 SSH 私钥，只发订阅令牌，中继回平台校验后才放行。
节点地址由平台下发（`config.toml` 的 `proxy.relays`），代码里不写死。
配套 `pkg/sysproxy`（系统代理 / PAC 分流）与 `pkg/authz`（订阅权益校验）也在这里。

## 快速开始

```bash
# 1. 安装并启动 Ollama，拉一个模型
ollama pull gemma4

# 2. 启动本地网关（默认监听 127.0.0.1:4000）
go run ./cmd/qdbot-llm serve

# 3. 让 Claude Code 接入本地模型
export ANTHROPIC_BASE_URL=http://127.0.0.1:4000
export ANTHROPIC_MODEL=gemma4
```

## 命令

```bash
qdbot-llm install                # 检测 ollama，未装则打印安装提示（不自动装）
qdbot-llm pull <model>           # 拉取模型（透传 ollama pull）
qdbot-llm list                   # 列出本地模型（透传 ollama list）
qdbot-llm serve [--addr ...] [--ollama ...]   # 启动本地网关
qdbot-llm expose [--model ...]   # 网关 + 反向隧道（一键接入平台，需会员）
qdbot-llm daemon install|start|stop|status|uninstall   # 开机常驻 + 断线重连
```

`serve` 默认监听 `127.0.0.1:4000`，上游 Ollama 地址可用 `--ollama` 或环境变量 `OLLAMA_URL` 指定（默认 `http://127.0.0.1:11434`）。

## 协议

本地网关把 Anthropic Messages 协议翻译成 Ollama 原生协议：

| 协议 | 端点 | 说明 |
|---|---|---|
| Anthropic Messages | `POST /v1/messages` | Claude Code / Anthropic SDK 走这里 |

按请求的 `stream` 字段自动分流：非流式返回 JSON，流式返回 SSE（`text/event-stream`）。

> 当前只实现了 Anthropic Messages 端点；OpenAI 兼容端点（`POST /v1/chat/completions`）尚未实现。

## 构建

```bash
go build ./cmd/qdbot-llm
```

## License

[MIT](LICENSE)
