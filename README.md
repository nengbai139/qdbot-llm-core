# qdbot-llm-core

把本地 [Ollama](https://ollama.com) 模型变成一个 **Anthropic 兼容** 的 API 服务，让 Claude Code、Anthropic SDK 等工具直接接入本地模型。

- **本地运行**：模型与数据不出你的机器，完全离线可用
- **流式输出**：支持 SSE 流式、tool_use、thinking
- **核心零依赖**：协议翻译核心纯 Go 标准库；隧道客户端仅依赖 toml / yaml 两个开源库

## 功能一览

客户端的全部能力都在本仓库：

| 能力 | 命令 / 包 | 说明 |
|------|-----------|------|
| 本地模型网关 | `serve` | 把本地 Ollama 翻译成 Anthropic Messages 协议，Claude Code / Anthropic SDK 直接接入 |
| 模型管理 | `install` / `pull` / `list` | 检测 Ollama、拉取模型、列出本地模型 |
| 公网暴露 | `expose` | 本地服务经反向隧道挂到公网域名，拿到一个可对外访问的地址 |
| 常驻自启 | `daemon` | macOS / Linux / Windows 三平台常驻，开机自启、断线自动重连 |

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
qdbot-llm expose [--model ...]   # 网关 + 反向隧道（把本地服务暴露到公网）
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
