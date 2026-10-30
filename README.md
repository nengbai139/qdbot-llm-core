# qdbot-llm-core

当前版本 **v0.6.2**。

把本地 [Ollama](https://ollama.com) 或 [vMLX](https://vmlx.net/) 模型变成 **Anthropic、OpenAI、Responses** 兼容的 API 服务。Claude Code、Anthropic SDK、OpenAI SDK、Codex、OpenCode 可以直接接本地模型，也可以经反向隧道挂到公网。

- **本地运行**：模型与数据不出你的机器，完全离线可用
- **流式输出**：支持 SSE 流式、tool_use、thinking
- **核心零依赖**：协议翻译核心纯 Go 标准库；隧道客户端仅依赖 toml / yaml 两个开源库

## 功能一览

客户端的全部能力都在本仓库：

| 能力 | 命令 / 包 | 说明 |
|------|-----------|------|
| 本地模型网关 | `serve` | Ollama 译成 Anthropic / OpenAI / Responses；`--backend vmlx` 时原样转发到本地 vMLX |
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
qdbot-llm install                # 查看 Ollama / vMLX 是否就绪，并给出下一步（不自动装）
qdbot-llm pull <model>           # 拉取模型（透传 ollama pull）
qdbot-llm list                   # 列出本地模型（透传 ollama list）
qdbot-llm serve [--backend ollama|vmlx] [--ollama ...] [--vmlx ...]   # 启动本地网关
qdbot-llm expose [--backend vmlx] [--model ...]   # 网关 + 反向隧道（把本地服务暴露到公网）
qdbot-llm daemon install|start|stop|status|uninstall   # 开机常驻 + 断线重连
```

`serve` 默认监听 `127.0.0.1:4000`。默认上游是 Ollama（`--ollama` 或 `OLLAMA_URL`，默认 `http://127.0.0.1:11434`），请求会译成 Ollama `/api/chat`。

vMLX 自己说 Anthropic、OpenAI 和 Responses，所以 `--backend vmlx` 把这几条原样转到 vMLX，不再翻译。`vmlx serve` 默认 `http://127.0.0.1:8000`，桌面版 API Gateway 是 `http://127.0.0.1:8080`，用 `--vmlx` 或 `VMLX_URL` 指定。

```bash
qdbot-llm serve --backend vmlx
qdbot-llm serve --backend vmlx --vmlx http://127.0.0.1:8080
```

模型已经在 Ollama 里时，直接暴露这一份，不要再 `vmlx serve` 同一个模型（那会另外下载）：

```bash
qdbot-llm expose --model gemma4
```

`--backend vmlx` 只在 vMLX 已经监听时原样转发。vMLX 没起来、Ollama 里已有模型时，自动改走 Ollama。公网 id 仍是 `模型名@隧道别名`。挂到公网前先 `qtunnel login`。

使用方仍连 `https://llm.tunnel.aimatchem.com`，模型 id 看 `GET /v1/models`。

## 协议

默认上游是 Ollama，下面这些协议都译成 Ollama `POST /api/chat`。`--backend vmlx` 时不翻译，原样转到 vMLX 的同名路径。

| 协议 | 端点 | 说明 |
|---|---|---|
| Anthropic Messages | `POST /v1/messages` | Claude Code、OpenCode 的 Anthropic 包 |
| OpenAI Chat Completions | `POST /v1/chat/completions` | OpenAI SDK、OpenCode 的 OpenAI 包 |
| OpenAI Models | `GET /v1/models` | 列出本机 Ollama 模型（`/api/tags`） |
| OpenAI Responses | `POST /v1/responses` | Codex（`wire_api = "responses"`） |

按请求的 `stream` 字段分流：非流式返回 JSON，流式返回 SSE。市场形态的模型 id（`gemma4@别名`）会剥掉 `@` 后面再交给 Ollama。

## 接入

模型名分两种。连本地 `serve` 时用 Ollama 里的名字（如 `gemma4`）。连公网时用 `GET https://llm.tunnel.aimatchem.com/v1/models` 返回的 `id`。`@` 后面是隧道别名，不是另一个模型。别名不要用模型名。这台提供方的别名是 `studio`，所以 id 是 `gemma4@studio`。公网 key 是 dashboard 签发的 `sk-` key。本地 `serve` 不校验 key，但 Claude Code 要求这个环境变量非空，填任意值即可。

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
export ANTHROPIC_MODEL=gemma4@studio
export ANTHROPIC_DEFAULT_OPUS_MODEL=gemma4@studio
export ANTHROPIC_DEFAULT_SONNET_MODEL=gemma4@studio
export ANTHROPIC_DEFAULT_HAIKU_MODEL=gemma4@studio
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

公网把 `baseURL` 换成 `https://llm.tunnel.aimatchem.com/v1`，`modelID` 换成目录里的 id（如 `gemma4@studio`），`ANTHROPIC_API_KEY` 换成 `sk-` key。

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

公网把 `base_url` 换成 `https://llm.tunnel.aimatchem.com/v1`，`model` 换成 `GET /v1/models` 里的 id（如 `gemma4@studio`）。`base_url` 以 `/v1` 结尾，Codex 会自己拼 `/responses`。`wire_api` 只能写 `responses`。本地 `serve` 不校验 key，但 Codex 会读这个环境变量，填任意非空值即可：

```bash
export QDBOT_API_KEY=local
```

## 版本

### v0.6.2

`qdbot-llm install` 同时说明 Ollama 和 vMLX 是否就绪，并给出下一步。不自动安装。vMLX 没在听、Ollama 里已有模型时，走已有的那一份，不再下载第二份。

### v0.6.1

正向代理实现移出本仓库，留在闭源客户端。

### v0.6.0

增加本地 vMLX 上游。`qdbot-llm serve --backend vmlx` 和 `expose --backend vmlx` 把 Anthropic、OpenAI、Responses 原样转到 vMLX（默认 `http://127.0.0.1:8000`）。Ollama 仍是默认上游。

### v0.5.0

本地网关增加 OpenAI Chat Completions（`POST /v1/chat/completions`）、模型列表（`GET /v1/models`）和 Responses（`POST /v1/responses`）。三条都译成 Ollama `/api/chat`。Claude Code、OpenCode、Codex 可以接 `127.0.0.1:4000`。

### v0.4.0

客户端不再把 `auth_secret` 落盘。

### v0.3.0

隧道、协议、订阅相关代码提升为公开包 `pkg/`，供服务端复用。

### v0.2.0

完整客户端开源：`expose`、`daemon` 和隧道客户端。

### v0.1.0

首个开源版本：本地网关和 `qdbot-llm` 命令行。

## 构建

```bash
go build ./cmd/qdbot-llm
```

## License

[MIT](LICENSE)
