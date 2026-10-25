package llm

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestToOllama(t *testing.T) {
	tests := []struct {
		name    string
		in      AnthropicMessageRequest
		want    OllamaChatRequest
		wantErr bool
	}{
		{
			name: "system + max_tokens + 纯文本 content",
			in: AnthropicMessageRequest{
				Model:     "gemma4",
				MaxTokens: 512,
				System:    AnthropicContent{{Type: "text", Text: "你是助手"}},
				Messages: []AnthropicMessage{
					{Role: "user", Content: AnthropicContent{{Type: "text", Text: "你好"}}},
				},
			},
			want: OllamaChatRequest{
				Model: "gemma4",
				Messages: []OllamaMessage{
					{Role: "system", Content: "你是助手"},
					{Role: "user", Content: "你好"},
				},
				Stream:  false,
				Options: OllamaOptions{NumPredict: 512},
			},
		},
		{
			name: "stream 透传",
			in: AnthropicMessageRequest{
				Model:     "gemma4",
				MaxTokens: 100,
				Stream:    true,
				Messages:  []AnthropicMessage{{Role: "user", Content: AnthropicContent{{Type: "text", Text: "hi"}}}},
			},
			want: OllamaChatRequest{
				Model:    "gemma4",
				Messages: []OllamaMessage{{Role: "user", Content: "hi"}},
				Stream:   true,
				Options:  OllamaOptions{NumPredict: 100},
			},
		},
		{
			name: "tools 翻译成 function 风格",
			in: AnthropicMessageRequest{
				Model:    "gemma4",
				Messages: []AnthropicMessage{{Role: "user", Content: AnthropicContent{{Type: "text", Text: "查天气"}}}},
				Tools: []AnthropicTool{{
					Name:        "get_weather",
					Description: "查天气",
					InputSchema: map[string]any{"type": "object"},
				}},
			},
			want: OllamaChatRequest{
				Model:    "gemma4",
				Messages: []OllamaMessage{{Role: "user", Content: "查天气"}},
				Stream:   false,
				Tools: []OllamaTool{{
					Type: "function",
					Function: OllamaToolFn{
						Name:        "get_weather",
						Description: "查天气",
						Parameters:  map[string]any{"type": "object"},
					},
				}},
			},
		},
		{
			name: "tool_result 转 role=tool",
			in: AnthropicMessageRequest{
				Model: "gemma4",
				Messages: []AnthropicMessage{
					{Role: "user", Content: AnthropicContent{{Type: "tool_result", ToolUseID: "get_weather", Content: "晴"}}},
				},
			},
			want: OllamaChatRequest{
				Model:    "gemma4",
				Messages: []OllamaMessage{{Role: "tool", Content: "晴", ToolName: "get_weather"}},
				Stream:   false,
			},
		},
		{
			name:    "空 messages 拒绝",
			in:      AnthropicMessageRequest{Model: "gemma4"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToOllama(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Model != tt.want.Model || got.Stream != tt.want.Stream {
				t.Fatalf("got %+v want %+v", got, tt.want)
			}
			if got.Options.NumPredict != tt.want.Options.NumPredict {
				t.Fatalf("num_predict got %d want %d", got.Options.NumPredict, tt.want.Options.NumPredict)
			}
			if len(got.Messages) != len(tt.want.Messages) {
				t.Fatalf("messages len got %d want %d", len(got.Messages), len(tt.want.Messages))
			}
			for i := range tt.want.Messages {
				if !reflect.DeepEqual(got.Messages[i], tt.want.Messages[i]) {
					t.Fatalf("msg[%d] got %+v want %+v", i, got.Messages[i], tt.want.Messages[i])
				}
			}
			if len(got.Tools) != len(tt.want.Tools) {
				t.Fatalf("tools len got %d want %d", len(got.Tools), len(tt.want.Tools))
			}
			for i := range tt.want.Tools {
				if !reflect.DeepEqual(got.Tools[i], tt.want.Tools[i]) {
					t.Fatalf("tool[%d] got %+v want %+v", i, got.Tools[i], tt.want.Tools[i])
				}
			}
		})
	}
}

func TestFromOllama(t *testing.T) {
	resp := FromOllama(OllamaChatResponse{
		Model:           "gemma4",
		Message:         OllamaMessage{Role: "assistant", Content: "你好"},
		Done:            true,
		DoneReason:      "stop",
		PromptEvalCount: 10,
		EvalCount:       20,
	}, "msg_test")

	if resp.ID != "msg_test" || resp.Type != "message" || resp.Role != "assistant" {
		t.Fatalf("bad envelope: %+v", resp)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "text" || resp.Content[0].Text != "你好" {
		t.Fatalf("bad content: %+v", resp.Content)
	}
	if resp.StopReason != "end_turn" {
		t.Fatalf("stop_reason got %q want end_turn", resp.StopReason)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 20 {
		t.Fatalf("usage got %+v", resp.Usage)
	}
}

func TestFromOllamaToolUse(t *testing.T) {
	resp := FromOllama(OllamaChatResponse{
		Model: "gemma4",
		Message: OllamaMessage{
			Role: "assistant",
			ToolCalls: []OllamaToolCall{{
				Function: OllamaToolCallFn{Name: "get_weather", Arguments: map[string]any{"city": "beijing"}},
			}},
		},
		DoneReason: "stop",
	}, "msg_test")

	if resp.StopReason != "tool_use" {
		t.Fatalf("stop_reason got %q want tool_use", resp.StopReason)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("content len got %d want 1", len(resp.Content))
	}
	blk := resp.Content[0]
	if blk.Type != "tool_use" || blk.Name != "get_weather" || blk.ID != "get_weather" {
		t.Fatalf("bad tool_use block: %+v", blk)
	}
	if blk.Input["city"] != "beijing" {
		t.Fatalf("bad tool_use input: %+v", blk.Input)
	}
}

func TestAnthropicContentUnmarshal(t *testing.T) {
	// 纯字符串形态
	var c1 AnthropicContent
	if err := json.Unmarshal([]byte(`"你好"`), &c1); err != nil {
		t.Fatal(err)
	}
	if c1.Text() != "你好" {
		t.Fatalf("got %q want 你好", c1.Text())
	}

	// block 数组形态
	var c2 AnthropicContent
	if err := json.Unmarshal([]byte(`[{"type":"text","text":"世界"}]`), &c2); err != nil {
		t.Fatal(err)
	}
	if c2.Text() != "世界" {
		t.Fatalf("got %q want 世界", c2.Text())
	}
}

func TestToolResultText(t *testing.T) {
	if got := toolResultText("晴"); got != "晴" {
		t.Fatalf("string got %q", got)
	}
	if got := toolResultText([]any{map[string]any{"type": "text", "text": "多云"}}); got != "多云" {
		t.Fatalf("array got %q", got)
	}
}

func TestOllamaNumCtx(t *testing.T) {
	// 默认 16384：Claude Code 真实请求约 13.4k token，8192 装不下，
	// 而 Ollama 超限不报错、直接静默挂住。
	if got := ollamaNumCtx(); got != 16384 {
		t.Fatalf("default got %d want 16384", got)
	}
	t.Setenv("OLLAMA_NUM_CTX", "32768")
	if got := ollamaNumCtx(); got != 32768 {
		t.Fatalf("env override got %d want 32768", got)
	}
	// 非法值回落到默认，不把脏配置透传给 Ollama。
	t.Setenv("OLLAMA_NUM_CTX", "abc")
	if got := ollamaNumCtx(); got != 16384 {
		t.Fatalf("invalid got %d want 16384", got)
	}
	t.Setenv("OLLAMA_NUM_CTX", "0")
	if got := ollamaNumCtx(); got != 16384 {
		t.Fatalf("zero got %d want 16384", got)
	}
}

func TestToOllamaPassesNumCtx(t *testing.T) {
	req := AnthropicMessageRequest{
		Model:     "gemma4:12b-mlx",
		MaxTokens: 256,
		Messages:  []AnthropicMessage{{Role: "user", Content: AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	got, err := ToOllama(req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Options.NumCtx != 16384 {
		t.Fatalf("num_ctx got %d want 16384", got.Options.NumCtx)
	}
	// 市场 model id 带 @提供方 后缀时，转给 Ollama 的是干净模型名。
	if got.Model != "gemma4:12b-mlx" {
		t.Fatalf("model got %q", got.Model)
	}
}
