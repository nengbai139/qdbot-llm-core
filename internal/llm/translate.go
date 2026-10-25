package llm

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ToOllama 把 Anthropic 请求翻译成 Ollama /api/chat 请求。
//
// 映射关系：
//   - Anthropic system 字符串 → 前置一条 role=system 的 Ollama 消息
//   - Anthropic max_tokens       → Ollama options.num_predict
//   - Anthropic tools            → Ollama function 风格 tools
//   - Anthropic tool_result 块   → Ollama role=tool 消息
//   - Anthropic stream           → 原样透传（P2 起支持流式）
func ToOllama(req AnthropicMessageRequest) (OllamaChatRequest, error) {
	if len(req.Messages) == 0 {
		return OllamaChatRequest{}, errors.New("messages 不能为空")
	}

	msgs := make([]OllamaMessage, 0, len(req.Messages)+1)
	if sys := req.System.Text(); sys != "" {
		msgs = append(msgs, OllamaMessage{Role: "system", Content: sys})
	}
	// 先扫一遍助手消息，建立 tool_use id → 工具名 的映射：
	// tool_result 只带 tool_use_id，Ollama 的 tool 消息却要按工具名/顺序回填。
	toolNames := collectToolNames(req.Messages)
	for _, m := range req.Messages {
		msgs = append(msgs, toOllamaMessage(m, toolNames)...)
	}

	tools := toOllamaTools(req.Tools)
	// 小模型（如 qwen3:8b）收到 Claude Code 的 21 个工具会乱调（把「自我介绍」当成要跑
	// Workflow），导致消费端执行工具卡死。提供方可用 OLLAMA_STRIP_TOOLS=1 剥掉工具，
	// 让模型只回答文字。
	if strings.TrimSpace(os.Getenv("OLLAMA_STRIP_TOOLS")) != "" {
		tools = nil
	}

	return OllamaChatRequest{
		Model:     OllamaModelFor(req.Model),
		Messages:  msgs,
		Stream:    req.Stream,
		Options:   OllamaOptions{NumPredict: req.MaxTokens, NumCtx: ollamaNumCtx()},
		Tools:     tools,
		Think:     thinkMode(req.Thinking),
		KeepAlive: ollamaKeepAlive(),
	}, nil
}

// ollamaNumCtx 返回转发给 Ollama 的上下文窗口长度（token）。
//
// 默认 16384：Claude Code 真实的一次请求（system prompt + 21 个工具定义）实测
// 约 13.4k token，8192 装不下。而 Ollama 遇到超限不报错、直接静默挂住，
// 表现为「模型一直不出字」，极难排查——所以这里默认给足余量。
// 可用 OLLAMA_NUM_CTX 覆盖。
func ollamaNumCtx() int {
	if v := strings.TrimSpace(os.Getenv("OLLAMA_NUM_CTX")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 16384
}

// ollamaKeepAlive 返回转发给 Ollama 的 keep_alive 时长。默认 24h（一天，对常驻 provider
// 基本等于「不卸载」），避免 Ollama 默认 5 分钟闲置卸载后、下次请求又冷加载模型
// （qwen3:8b 冷加载约 8.4s）。可用 OLLAMA_KEEP_ALIVE 环境变量覆盖。
// 注意：Ollama 只接受带单位的时长字符串（如 "30m"/"24h"），"-1" 会报
// time: missing unit in duration（实测这台 Ollama 不支持 -1 特殊值）。
func ollamaKeepAlive() string {
	if v := strings.TrimSpace(os.Getenv("OLLAMA_KEEP_ALIVE")); v != "" {
		return v
	}
	return "24h"
}

// thinkMode 把 Anthropic 的 thinking 参数映射成 Ollama 的 think 开关。
//
// 未开启思考时显式下发 false：qwen3 的思考模板会把 </think> 尾巴漏进工具参数
// （实测命令变成 "ls /think"），关掉思考能避免这种脏参数，也让本地推理更快。
func thinkMode(t *AnthropicThinking) any {
	if t == nil || t.Type != "enabled" {
		return false
	}
	switch {
	case t.BudgetTokens >= 8192:
		return "high"
	case t.BudgetTokens >= 2048:
		return "medium"
	default:
		return "low"
	}
}

// OllamaModelName 从市场 model id 里取出底层模型名。
//
// 市场的 model id 是 "<模型>@<别名>"（别名用于路由与计价），但 Ollama 只认 "@" 前面那段。
// 网关会重写 model，直连别名子域名时不会 —— 这里统一剥掉，两条路径行为一致。
//
// 只剥最后一个 "@" 且要求其后不含 ":"：别名是子域名形态（llm-c1e7a99a-...），
// 而 Ollama 按 digest 拉取的模型名本身带 "@sha256:xxx"，那种不能剥。
func OllamaModelName(model string) string {
	i := strings.LastIndex(model, "@")
	if i <= 0 {
		return model
	}
	if strings.Contains(model[i+1:], ":") {
		return model // "@sha256:..." 是模型名的一部分，不是别名
	}
	return model[:i]
}

// OllamaModelFor 把市场 model id 映射成本机 Ollama 真正认的模型 tag。
//
// 市场里注册的模型名是产品名（如 "gemma4"），本机 Ollama 装的 tag 却是
// "gemma4:12b-mlx" —— 两者不一致时 Ollama 直接 404
// （model 'gemma4:latest' not found），网关表现为 502。
// 用 OLLAMA_MODEL_MAP 显式声明映射，格式 "市场名=本地tag,市场名2=本地tag2"；
// 未声明的按原样透传（本地 tag 与市场名一致的老配置不受影响）。
func OllamaModelFor(modelID string) string {
	name := OllamaModelName(modelID)
	for _, kv := range strings.Split(os.Getenv("OLLAMA_MODEL_MAP"), ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if !ok || k == "" || v == "" {
			continue
		}
		if k == name {
			return v
		}
	}
	return name
}

// collectToolNames 收集所有助手消息里 tool_use 块的 id → name 映射。
func collectToolNames(msgs []AnthropicMessage) map[string]string {
	out := map[string]string{}
	for _, m := range msgs {
		if m.Role != "assistant" {
			continue
		}
		for _, blk := range m.Content.ToolUses() {
			if blk.ID != "" && blk.Name != "" {
				out[blk.ID] = blk.Name
			}
		}
	}
	return out
}

// toOllamaMessage 把一条 Anthropic 消息翻译成 0..N 条 Ollama 消息。
//
// 关键差异：
//   - Anthropic 把工具结果放在 user 消息的 tool_result 块里；Ollama 要 role=tool 的独立消息。
//   - 助手轮的 tool_use 必须还原成 Ollama tool_calls，否则多轮工具调用时模型看不到
//     自己上一轮调过什么，只剩一堆孤立的 tool 结果，agentic 场景会跑飞。
func toOllamaMessage(m AnthropicMessage, toolNames map[string]string) []OllamaMessage {
	if results := m.Content.ToolResults(); len(results) > 0 {
		out := make([]OllamaMessage, 0, len(results))
		extra := m.Content.Text() // tool_result 之外还带的说明文字
		for i, blk := range results {
			msg := OllamaMessage{
				Role:     "tool",
				Content:  toolResultText(blk.Content),
				ToolName: toolNameOf(blk, toolNames),
			}
			if i == 0 && extra != "" {
				msg.Content = extra + "\n" + msg.Content
			}
			out = append(out, msg)
		}
		return out
	}

	msg := OllamaMessage{Role: m.Role, Content: m.Content.Text()}
	for _, blk := range m.Content.ToolUses() {
		name := blk.Name
		if name == "" {
			name = blk.ID
		}
		msg.ToolCalls = append(msg.ToolCalls, OllamaToolCall{
			ID:       blk.ID,
			Function: OllamaToolCallFn{Name: name, Arguments: blk.Input},
		})
	}
	return []OllamaMessage{msg}
}

// toolNameOf 解析 tool_result 块对应的工具名：优先用 id→name 映射，
// 映射缺失时退回 tool_use_id 本身（我们自造的 tool_use 用工具名当 id）。
func toolNameOf(blk ContentBlock, toolNames map[string]string) string {
	if name, ok := toolNames[blk.ToolUseID]; ok && name != "" {
		return name
	}
	return blk.ToolUseID
}

// toolResultText 把 tool_result 的 content（字符串或 [{"type":"text",...}]）压平成字符串。
func toolResultText(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for _, item := range c {
			if m, ok := item.(map[string]any); ok && m["type"] == "text" {
				if s, ok := m["text"].(string); ok {
					b.WriteString(s)
				}
			}
		}
		return b.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

// toOllamaTools 把 Anthropic 工具定义翻译成 Ollama function 风格。
// Anthropic 的 input_schema 对应 Ollama 的 function.parameters。
func toOllamaTools(tools []AnthropicTool) []OllamaTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]OllamaTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, OllamaTool{
			Type: "function",
			Function: OllamaToolFn{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}
	return out
}

// FromOllama 把 Ollama 响应翻译成 Anthropic Messages 响应（非流式）。
//
// 映射关系：
//   - Ollama message.content  → Anthropic content=[{type:text,text:...}]
//   - Ollama message.tool_calls → Anthropic content tool_use 块
//   - Ollama done_reason=stop → Anthropic stop_reason=end_turn（有 tool_call 则 tool_use）
//   - Ollama prompt_eval_count  → usage.input_tokens
//   - Ollama eval_count         → usage.output_tokens
func FromOllama(resp OllamaChatResponse, requestID string) AnthropicMessageResponse {
	stop := mapStopReason(resp.DoneReason)
	if len(resp.Message.ToolCalls) > 0 {
		stop = "tool_use"
	}
	return AnthropicMessageResponse{
		ID:         requestID,
		Type:       "message",
		Role:       "assistant",
		Model:      resp.Model,
		Content:    contentFromOllama(resp.Message),
		StopReason: stop,
		Usage: AnthropicUsage{
			InputTokens:  resp.PromptEvalCount,
			OutputTokens: resp.EvalCount,
		},
	}
}

// contentFromOllama 把 Ollama 消息转成 Anthropic content blocks（text + tool_use）。
func contentFromOllama(m OllamaMessage) []ContentBlock {
	blocks := make([]ContentBlock, 0, 1+len(m.ToolCalls))
	if m.Content != "" {
		blocks = append(blocks, ContentBlock{Type: "text", Text: m.Content})
	}
	for _, tc := range m.ToolCalls {
		// 用工具名当 tool_use 的 ID：这样 tool_result 回填的 tool_use_id 就是工具名，
		// 能直接映射回 Ollama 的 tool_name，省掉一张 id→name 的状态表。
		// 局限：同一轮同一工具被调用多次时 ID 会碰撞，属已知边界。
		blocks = append(blocks, ContentBlock{
			Type:  "tool_use",
			ID:    tc.Function.Name,
			Name:  tc.Function.Name,
			Input: tc.Function.Arguments,
		})
	}
	return blocks
}

// mapStopReason 把 Ollama done_reason 映射成 Anthropic stop_reason。
func mapStopReason(doneReason string) string {
	switch doneReason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	default:
		return "end_turn"
	}
}
