package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// ---- Anthropic SSE 流式事件：把 Ollama 的 NDJSON 流翻译成 Anthropic SSE 序列 ----

// streamMessageStart 对应 message_start 事件。
type streamMessageStart struct {
	Type    string             `json:"type"`
	Message anthropicStreamMsg `json:"message"`
}

type anthropicStreamMsg struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Model        string         `json:"model"`
	Content      []ContentBlock `json:"content"`
	StopReason   *string        `json:"stop_reason"`   // 流开始时为 null（Claude Code 严格解析要求该字段存在）
	StopSequence any            `json:"stop_sequence"` // 流开始时为 null
	Usage        AnthropicUsage `json:"usage"`
}

// streamContentBlockStart 对应 content_block_start 事件。
// ContentBlock 用 any：text 块要显式带 "text":""（规范要求），tool_use 块用 ContentBlock。
type streamContentBlockStart struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock any    `json:"content_block"`
}

// streamTextBlock 是 content_block_start 里 text 块的结构：text 字段必在（可为空串）。
type streamTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// streamToolUseBlock 是 content_block_start 里 tool_use 块的结构：input 字段必在（可为空对象）。
// 不能用 ContentBlock（其 input 带 omitempty，空 map 会被吞掉，Claude Code 解析失败）。
type streamToolUseBlock struct {
	Type  string         `json:"type"`
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// streamContentBlockDelta 对应 content_block_delta 事件。
type streamContentBlockDelta struct {
	Type  string         `json:"type"`
	Index int            `json:"index"`
	Delta map[string]any `json:"delta"`
}

// streamContentBlockStop 对应 content_block_stop 事件。
type streamContentBlockStop struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

// streamMessageDelta 对应 message_delta 事件。
type streamMessageDelta struct {
	Type  string         `json:"type"`
	Delta map[string]any `json:"delta"`
	Usage streamUsage    `json:"usage"`
}

// streamUsage 是 message_delta 的 usage（只带 output_tokens；input 在 message_start 里）。
type streamUsage struct {
	OutputTokens int `json:"output_tokens"`
}

// streamMessageStop 对应 message_stop 事件。
type streamMessageStop struct {
	Type string `json:"type"`
}

// Flusher 抽象 http.Flusher，便于测试时传 nil。
type Flusher interface {
	Flush()
}

// StreamOllamaToAnthropic 把 Ollama 的流式 NDJSON 响应翻译成 Anthropic SSE 事件流。
//
// r 是 Ollama 响应体（每行一个 JSON 块），w 是 Anthropic 响应体（已设 text/event-stream）。
// 覆盖文本增量 + 流式 tool_use（input_json_delta）。
//
// Ollama 流式 tool_call 实测（gemma4）：一个 chunk 里原子出现完整 tool_calls，
// arguments 是完整 JSON 对象（非分片字符串），所以映射成单个 input_json_delta 即可。
// Gemma4 的 reasoning 走 message.thinking（content 为空），这里直接忽略。
func StreamOllamaToAnthropic(r io.Reader, w io.Writer, requestID, model string, flusher Flusher) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	started := false
	blockIdx := 0 // 下一个 content block 的 index
	textOpen := false
	textIdx := 0
	sawToolCall := false

	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var chunk OllamaChatResponse
		if err := json.Unmarshal(line, &chunk); err != nil {
			// 流中间某行解析失败不中断（Ollama 可能混入 keepalive），跳过继续。
			continue
		}

		if !started {
			ev := streamMessageStart{
				Type: "message_start",
				Message: anthropicStreamMsg{
					ID:      requestID,
					Type:    "message",
					Role:    "assistant",
					Model:   model,
					Content: []ContentBlock{},
					Usage:   AnthropicUsage{InputTokens: chunk.PromptEvalCount},
				},
			}
			if err := writeSSE(w, "message_start", ev); err != nil {
				return err
			}
			started = true
		}

		if chunk.Message.Content != "" {
			if !textOpen {
				textIdx = blockIdx
				blockIdx++
				ev := streamContentBlockStart{
					Type:         "content_block_start",
					Index:        textIdx,
					ContentBlock: streamTextBlock{Type: "text", Text: ""},
				}
				if err := writeSSE(w, "content_block_start", ev); err != nil {
					return err
				}
				textOpen = true
			}
			ev := streamContentBlockDelta{
				Type:  "content_block_delta",
				Index: textIdx,
				Delta: map[string]any{"type": "text_delta", "text": chunk.Message.Content},
			}
			if err := writeSSE(w, "content_block_delta", ev); err != nil {
				return err
			}
		}

		for _, tc := range chunk.Message.ToolCalls {
			sawToolCall = true
			// 文本块先关闭（text + tool 混出时）
			if textOpen {
				if err := writeSSE(w, "content_block_stop", streamContentBlockStop{Type: "content_block_stop", Index: textIdx}); err != nil {
					return err
				}
				textOpen = false
			}
			idx := blockIdx
			blockIdx++
			// content_block_start（tool_use）
			cb := streamToolUseBlock{Type: "tool_use", ID: tc.Function.Name, Name: tc.Function.Name, Input: map[string]any{}}
			if err := writeSSE(w, "content_block_start", streamContentBlockStart{Type: "content_block_start", Index: idx, ContentBlock: cb}); err != nil {
				return err
			}
			// input_json_delta（Ollama 一次性给全 arguments，作单个 partial_json）
			argsJSON, _ := json.Marshal(tc.Function.Arguments)
			ev := streamContentBlockDelta{
				Type:  "content_block_delta",
				Index: idx,
				Delta: map[string]any{"type": "input_json_delta", "partial_json": string(argsJSON)},
			}
			if err := writeSSE(w, "content_block_delta", ev); err != nil {
				return err
			}
			if err := writeSSE(w, "content_block_stop", streamContentBlockStop{Type: "content_block_stop", Index: idx}); err != nil {
				return err
			}
		}

		if chunk.Done {
			if textOpen {
				if err := writeSSE(w, "content_block_stop", streamContentBlockStop{Type: "content_block_stop", Index: textIdx}); err != nil {
					return err
				}
				textOpen = false
			}
			stopReason := mapStopReason(chunk.DoneReason)
			if sawToolCall {
				stopReason = "tool_use"
			}
			if err := writeSSE(w, "message_delta", streamMessageDelta{
				Type:  "message_delta",
				Delta: map[string]any{"stop_reason": stopReason, "stop_sequence": nil},
				Usage: streamUsage{OutputTokens: chunk.EvalCount},
			}); err != nil {
				return err
			}
			if err := writeSSE(w, "message_stop", streamMessageStop{Type: "message_stop"}); err != nil {
				return err
			}
			break
		}

		if flusher != nil {
			flusher.Flush()
		}
	}
	return sc.Err()
}

// writeSSE 写一个 SSE 事件：event: <name>\ndata: <json>\n\n。
func writeSSE(w io.Writer, event string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	return err
}
