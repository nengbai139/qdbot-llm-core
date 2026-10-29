# qdbot-llm-core

把本地 [Ollama](https://ollama.com) 模型变成一个 **Anthropic 兼容** 的 API 服务，让 Claude Code、Anthropic SDK 等工具直接接入本地模型。

- **本地运行**：模型与数据不出你的机器，完全离线可用
- **流式输出**：支持 SSE 流式、tool_use、thinking
- **核心零依赖**：协议翻译核心纯 Go 标准库；隧道客户端仅依赖 toml / yaml 两个开源库

## 功能一览

客户端的全部能力都在本仓库：

| 能力 | 命令 / 包 | 说明 |
|------|-----------|------|
| 本地模型网关 | `serve` | 把本地 Ollama 翻译成 Anthropic Messages、OpenAI Chat Completions 和 Responses |
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

本地网关把下面这些协议都翻译成 Ollama `POST /api/chat`：

| 协议 | 端点 | 说明 |
|---|---|---|
| Anthropic Messages | `POST /v1/messages` | Claude Code、OpenCode 的 Anthropic 包 |
| OpenAI Chat Completions | `POST /v1/chat/completions` | OpenAI SDK、OpenCode 的 OpenAI 包 |
| OpenAI Models | `GET /v1/models` | 列出本机 Ollama 模型（`/api/tags`） |
| OpenAI Responses | `POST /v1/responses` | Codex（`wire_api = "responses"`） |

按请求的 `stream` 字段分流：非流式返回 JSON，流式返回 SSE。市场形态的模型 id（`gemma4@别名`）会剥掉 `@` 后面再交给 Ollama。

## 接入

模型名分两种：连本地 `serve` 时用 Ollama 里的名字（如 `gemma4`）；连公网时用 `GET https://llm.tunnel.aimatchem.com/v1/models` 返回的 `id`（如 `gemma4@qwen3`）。公网 key 是 dashboard 签发的 `sk-` key。本地 `serve` 不校验 key，但 Claude Code 要求这个环境变量非空，填任意值即可。

### Claude Code

本地：

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:4000
export ANTHROPIC_API_KEY=local
export ANTHROPIC_MODEL=gemma4
export ANTHROPIC_DEFAULT_OPUS_MODEL=gemma4
export ANTHROPIC_DEFAULT_SONNET_MODEL=gemma4
export ANTHROPIC_DEFAULT_HAIKU_MODEL=gemma4
```

公网把地址和模型换成市场入口，key 用真的 `sk-`：

```bash
export ANTHROPIC_BASE_URL=https://llm.tunnel.aimatchem.com
export ANTHROPIC_API_KEY=sk-...
export ANTHROPIC_MODEL=gemma4@qwen3
export ANTHROPIC_DEFAULT_OPUS_MODEL=gemma4@qwen3
export ANTHROPIC_DEFAULT_SONNET_MODEL=gemma4@qwen3
export ANTHROPIC_DEFAULT_HAIKU_MODEL=gemma4@qwen3
```

`ANTHROPIC_BASE_URL` 不要带 `/v1`，Claude Code 自己会请求 `/v1/messages`。`ANTHROPIC_AUTH_TOKEN` 同样可用，网关认 Bearer 和 `x-api-key`。三条 `ANTHROPIC_DEFAULT_*` 都要设成同一个模型，否则 Claude Code 仍会去要名为 `claude-…` 的模型。

### OpenCode

用 Anthropic 包，`baseURL` 要带 `/v1`（客户端会再拼 `/messages`）。写在项目或 `~/.config/opencode/opencode.json`：

```json
{
  "$schema": "https://opencode.ai/config.json",
  "model": "qdbot/gemma4",
  "providers": {
    "qdbot": {
      "env": ["ANTHROPIC_API_KEY"],
      "package": "@opencode/ai/providers/anthropic",
      "settings": { "baseURL": "http://127.0.0.1:4000/v1" },
      "models": { "gemma4": { "modelID": "gemma4" } }
    }
  }
}
```

公网把 `baseURL` 换成 `https://llm.tunnel.aimatchem.com/v1`，`modelID` 换成目录里的 id（如 `gemma4@qwen3`），`ANTHROPIC_API_KEY` 换成 `sk-` key。

也可以改用 OpenAI 兼容包 `@opencode/ai/providers/openai-compatible`。本地 `baseURL` 是 `http://127.0.0.1:4000/v1`，公网是 `https://llm.tunnel.aimatchem.com/v1`（请求 `/v1/chat/completions`）。

### Codex

Codex 只走 Responses API（`wire_api = "responses"`），请求 `POST /v1/responses`。本地和公网都认。`~/.codex/config.toml` 连本地：

```toml
model_provider = "qdbot"
model = "gemma4"

[model_providers.qdbot]
name = "qdbot"
base_url = "http://127.0.0.1:4000/v1"
env_key = "QDBOT_API_KEY"
wire_api = "responses"
```

公网把 `base_url` 换成 `https://llm.tunnel.aimatchem.com/v1`，`model` 换成 `GET /v1/models` 里的 id（如 `gemma4@qwen3`）。`base_url` 以 `/v1` 结尾，Codex 会自己拼 `/responses`。`wire_api` 只能写 `responses`。本地 `serve` 不校验 key，但 Codex 会读这个环境变量，填任意非空值即可：

```bash
export QDBOT_API_KEY=local
```

## 构建

```bash
go build ./cmd/qdbot-llm
```

## License

[MIT](LICENSE)
