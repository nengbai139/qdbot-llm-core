# qdbot-llm-core

把本地 [Ollama](https://ollama.com) 模型变成一个 **Anthropic 兼容** 的 API 服务，让 Claude Code、Anthropic SDK 等工具直接接入本地模型。

- **零依赖**：纯 Go 标准库实现，无第三方依赖
- **本地运行**：模型与数据不出你的机器，完全离线可用
- **流式输出**：支持 SSE 流式、tool_use、thinking

## 免费开源 · 平台会员

**本仓库是免费开源的本地网关**：`serve` / `pull` / `list` 全免费，本地跑模型、接 Claude Code 不花一分钱，模型与数据不出本机。

想**一键接入平台**（把本地模型反向代理到公网，供他人调用或按量售卖），需注册平台会员（年费）。平台能力（`expose` 反向隧道、`daemon` 开机常驻、市场鉴权 / 路由 / 计量 / 结算）在闭源仓库，**不包含在本仓库**。

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
