// Package llm 提供 Anthropic Messages API 与 Ollama /api/chat 之间的协议翻译。
//
// 覆盖：非流式 + SSE 流式（P2 起）、tools（P2 起）。
package llm

import (
	"bytes"
	"encoding/json"
	"strings"
)

// ---- Anthropic Messages API（使用方入口，Claude Code 等）----

// AnthropicMessageRequest 是 POST /v1/messages 的请求体。
//
// System 用 AnthropicContent 而非 string：Claude Code 实测发送的是 block 数组
// （[{"type":"text","text":"…","cache_control":{…}}]），纯字符串写法只兼容裸 curl。
type AnthropicMessageRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    AnthropicContent   `json:"system,omitempty"`
	Messages  []AnthropicMessage `json:"messages"`
	Stream    bool               `json:"stream,omitempty"`
	Tools     []AnthropicTool    `json:"tools,omitempty"`
	Thinking  *AnthropicThinking `json:"thinking,omitempty"`
}

// AnthropicThinking 是 extended thinking 参数（本代理不透传给 Ollama，
// 但必须能解码，否则整个请求 400）。
type AnthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

// forwardRequest 是「市场网关 → 提供方」之间的转发体。
//
// 与请求体的唯一差别：system 压平成字符串。提供方可能是尚未升级的旧版本
// （只认 string system），压平后两边都兼容，不必同时升级。
type forwardRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []AnthropicMessage `json:"messages"`
	Stream    bool               `json:"stream,omitempty"`
	Tools     []AnthropicTool    `json:"tools,omitempty"`
}

// ForwardJSON 编码转发给提供方的请求体（system 已是字符串形态）。
func ForwardJSON(req AnthropicMessageRequest) ([]byte, error) {
	return json.Marshal(forwardRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		System:    req.System.Text(),
		Messages:  req.Messages,
		Stream:    req.Stream,
		Tools:     req.Tools,
	})
}

// AnthropicMessage 是一条对话消息。
type AnthropicMessage struct {
	Role    string           `json:"role"` // user / assistant
	Content AnthropicContent `json:"content"`
}

// AnthropicTool 是 Anthropic 的工具定义（请求侧）。
type AnthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

// AnthropicContent 兼容 Anthropic 的两种 content 写法：
//   - 纯字符串："你好"
//   - content block 数组：[{"type":"text","text":"你好"}]
type AnthropicContent []ContentBlock

// ContentBlock 是 Anthropic content 里的一个块：text / tool_use / tool_result / thinking。
type ContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`        // text 块
	Thinking  string         `json:"thinking,omitempty"`    // thinking 块（extended thinking 回传）
	ID        string         `json:"id,omitempty"`          // tool_use 块
	Name      string         `json:"name,omitempty"`        // tool_use 块
	Input     map[string]any `json:"input,omitempty"`       // tool_use 块
	ToolUseID string         `json:"tool_use_id,omitempty"` // tool_result 块
	Content   any            `json:"content,omitempty"`     // tool_result 块（字符串或数组）
}

// UnmarshalJSON 让 content 字段同时接受字符串和数组两种形态。
func (c *AnthropicContent) UnmarshalJSON(b []byte) error {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		*c = AnthropicContent{{Type: "text", Text: s}}
		return nil
	}
	var blocks []ContentBlock
	if err := json.Unmarshal(trimmed, &blocks); err != nil {
		return err
	}
	*c = blocks
	return nil
}

// Text 把 content blocks 压平成纯文本（只取 text 块，忽略 tool_use/tool_result）。
func (c AnthropicContent) Text() string {
	var b strings.Builder
	for _, blk := range c {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

// ToolResults 返回 content 里的 tool_result 块（无则空）。
func (c AnthropicContent) ToolResults() []ContentBlock {
	var out []ContentBlock
	for _, blk := range c {
		if blk.Type == "tool_result" {
			out = append(out, blk)
		}
	}
	return out
}

// ToolUses 返回 content 里的 tool_use 块（无则空）。
func (c AnthropicContent) ToolUses() []ContentBlock {
	var out []ContentBlock
	for _, blk := range c {
		if blk.Type == "tool_use" {
			out = append(out, blk)
		}
	}
	return out
}

// AnthropicMessageResponse 是 /v1/messages 的响应体（非流式）。
type AnthropicMessageResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Model        string         `json:"model"`
	Content      []ContentBlock `json:"content"`
	StopReason   string         `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        AnthropicUsage `json:"usage"`
}

// AnthropicUsage 是 Anthropic 的 token 用量。
type AnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ---- Ollama 原生 API（提供方本地，POST /api/chat）----

// OllamaChatRequest 是 Ollama /api/chat 的请求体。
type OllamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []OllamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  OllamaOptions   `json:"options,omitempty"`
	Tools    []OllamaTool    `json:"tools,omitempty"`
	// Think 对应 Ollama 的思考开关：false=关闭，"low"/"medium"/"high"=开启并限档。
	// Anthropic 侧由 thinking 参数映射过来；用 any 承载是为了同时表达布尔与档位，
	// omitempty 只在 nil 时省略，所以「明确关闭」和「未设置」能区分开。
	Think any `json:"think,omitempty"`
	// KeepAlive 模型驻留时长（"30m"/"-1"=常驻等）。默认 -1 常驻，避免每次请求冷加载模型
	// （qwen3:8b 冷加载 ~8.4s）。可用 OLLAMA_KEEP_ALIVE 覆盖。
	KeepAlive string `json:"keep_alive,omitempty"`
}

// OllamaMessage 是 Ollama 的一条消息。
type OllamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []OllamaToolCall `json:"tool_calls,omitempty"`
	ToolName  string           `json:"tool_name,omitempty"` // role=tool 时回填，关联回上一条 tool_call
}

// OllamaOptions 是 Ollama 的生成参数。
type OllamaOptions struct {
	NumPredict int `json:"num_predict,omitempty"`
	// NumCtx 是上下文窗口长度（token 数）。不传时 Ollama 落到默认的 4096，
	// 而 Claude Code 一次请求就带 system prompt + 工具定义，很容易超限被截断。
	NumCtx int `json:"num_ctx,omitempty"`
}

// OllamaTool 是 Ollama 的工具定义（OpenAI function 风格）。
type OllamaTool struct {
	Type     string       `json:"type"` // "function"
	Function OllamaToolFn `json:"function"`
}

// OllamaToolFn 是 Ollama 工具定义里的 function 字段。
type OllamaToolFn struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// OllamaToolCall 是 Ollama 返回的工具调用。
// 实测（gemma4 流式）：tool_calls 在一个 chunk 里原子出现，arguments 是完整 JSON 对象
// （非分片累积的字符串）。
type OllamaToolCall struct {
	ID       string           `json:"id"`
	Function OllamaToolCallFn `json:"function"`
}

// OllamaToolCallFn 是 Ollama 工具调用里的 function 字段。
type OllamaToolCallFn struct {
	Index     int            `json:"index"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// OllamaChatResponse 是 Ollama /api/chat 的响应体（非流式 done=true；流式时逐块）。
type OllamaChatResponse struct {
	Model           string        `json:"model"`
	Message         OllamaMessage `json:"message"`
	Done            bool          `json:"done"`
	DoneReason      string        `json:"done_reason"`
	PromptEvalCount int           `json:"prompt_eval_count"`
	EvalCount       int           `json:"eval_count"`
}
