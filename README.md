# qdbot-llm

把本地 [Ollama](https://ollama.com) 模型变成一个 **Anthropic 兼容** 的 API 服务，让 Claude Code、Anthropic SDK 等工具直接接入本地模型。

- **零依赖**：纯 Go 标准库实现，无第三方依赖
- **本地运行**：模型与数据不出你的机器，完全离线可用
- **流式输出**：支持 SSE 流式、tool_use、thinking

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
