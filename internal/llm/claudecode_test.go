package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// claudeCodeRequest 是 Claude Code 实际发出的请求形态（截取自真实抓包）：
//   - system 是 block 数组（带 cache_control），不是字符串
//   - 带 thinking 参数与 metadata
//   - 工具定义带 cache_control
//   - 助手轮含 tool_use，user 轮含 tool_result
const claudeCodeRequest = `{
  "model": "gemma4@gemma4",
  "max_tokens": 32000,
  "system": [
    {"type": "text", "text": "You are Claude Code.", "cache_control": {"type": "ephemeral"}},
    {"type": "text", "text": "Be concise."}
  ],
  "messages": [
    {"role": "user", "content": [{"type": "text", "text": "列出目录"}]},
    {"role": "assistant", "content": [
      {"type": "thinking", "thinking": "…", "signature": "abc"},
      {"type": "text", "text": "我先看看"},
      {"type": "tool_use", "id": "toolu_01", "name": "Bash", "input": {"command": "ls"}}
    ]},
    {"role": "user", "content": [
      {"type": "tool_result", "tool_use_id": "toolu_01", "content": "a\nb"}
    ]}
  ],
  "tools": [
    {"name": "Bash", "description": "run", "input_schema": {"type": "object"}, "cache_control": {"type": "ephemeral"}}
  ],
  "thinking": {"type": "enabled", "budget_tokens": 1024},
  "metadata": {"user_id": "u-1"},
  "stream": true
}`

// TestDecodeClaudeCodeRequest 保证 Claude Code 的请求体能被解码——
// 之前 system 只接受 string，直接 400：cannot unmarshal array into ... system of type string。
func TestDecodeClaudeCodeRequest(t *testing.T) {
	var req AnthropicMessageRequest
	if err := json.Unmarshal([]byte(claudeCodeRequest), &req); err != nil {
		t.Fatalf("解码 Claude Code 请求失败: %v", err)
	}
	if got := req.System.Text(); got != "You are Claude Code.Be concise." {
		t.Errorf("system 文本 = %q，期望两段拼接", got)
	}
	if req.Thinking == nil || req.Thinking.BudgetTokens != 1024 {
		t.Errorf("thinking 参数未解析: %+v", req.Thinking)
	}
	if !req.Stream {
		t.Error("stream 应为 true")
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "Bash" {
		t.Errorf("tools 解析错误: %+v", req.Tools)
	}
}

// TestOllamaModelName 校验市场 model id（模型@别名）能剥出底层 Ollama 模型名。
// 直连别名子域名时请求里带的是 "@别名"，Ollama 不认，必须剥掉。
func TestOllamaModelName(t *testing.T) {
	cases := map[string]string{
		"qwen3:8b@qwen3": "qwen3:8b",
		"gemma4@gemma4":  "gemma4",
		"qwen3:8b":       "qwen3:8b",
		"":               "",
		// digest 拉取的模型名带 "@sha256:xxx"，那是模型名的一部分，不能剥。
		"gemma4@sha256:abc123": "gemma4@sha256:abc123",
	}
	for in, want := range cases {
		if got := OllamaModelName(in); got != want {
			t.Errorf("OllamaModelName(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestOllamaModelFor 校验市场名 → 本地 Ollama tag 的映射。
// 市场注册的是产品名（gemma4），本机装的是 gemma4:12b-mlx，对不上就是 404。
func TestOllamaModelFor(t *testing.T) {
	cases := []struct {
		env  string
		in   string
		want string
	}{
		{env: "", in: "gemma4:12b-mlx", want: "gemma4:12b-mlx"},
		{env: "", in: "gemma4:12b-mlx@llm-c1e7", want: "gemma4:12b-mlx"},
		{env: "gemma4=gemma4:12b-mlx", in: "gemma4@llm-c1e7", want: "gemma4:12b-mlx"},
		{env: "gemma4=gemma4:12b-mlx", in: "qwen3:8b@llm-c1e7", want: "qwen3:8b"},
		{env: "gemma4=gemma4:12b-mlx,qwen3=qwen3:8b", in: "qwen3@llm-c1e7", want: "qwen3:8b"},
		// 映射项写坏了不能把请求打坏，退回原名。
		{env: "gemma4", in: "gemma4", want: "gemma4"},
	}
	for _, c := range cases {
		t.Setenv("OLLAMA_MODEL_MAP", c.env)
		if got := OllamaModelFor(c.in); got != c.want {
			t.Errorf("OllamaModelFor(%q) map=%q = %q，期望 %q", c.in, c.env, got, c.want)
		}
	}
}

// TestForwardJSONSystemFlattened 校验转发给提供方的请求体里 system 是字符串：
// 提供方可能是只认 string 的旧版本，压平后两边都兼容。
func TestForwardJSONSystemFlattened(t *testing.T) {
	var req AnthropicMessageRequest
	if err := json.Unmarshal([]byte(claudeCodeRequest), &req); err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	raw, err := ForwardJSON(req)
	if err != nil {
		t.Fatalf("编码失败: %v", err)
	}
	var probe struct {
		System json.RawMessage `json:"system"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("解析转发体失败: %v", err)
	}
	if len(probe.System) == 0 || probe.System[0] != '"' {
		t.Fatalf("转发体的 system 应为字符串，实际 %s", probe.System)
	}
	if !strings.Contains(string(probe.System), "You are Claude Code.") {
		t.Errorf("转发体 system 内容缺失: %s", probe.System)
	}
}

// TestToOllamaClaudeCodeShape 校验翻译结果：system 前置、助手 tool_use 还原成
// Ollama tool_calls、tool_result 回到 role=tool 且带上正确的工具名。
func TestToOllamaClaudeCodeShape(t *testing.T) {
	var req AnthropicMessageRequest
	if err := json.Unmarshal([]byte(claudeCodeRequest), &req); err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	out, err := ToOllama(req)
	if err != nil {
		t.Fatalf("翻译失败: %v", err)
	}

	if len(out.Messages) == 0 || out.Messages[0].Role != "system" {
		t.Fatalf("首条应是 system 消息，实际 %+v", out.Messages)
	}
	if !strings.Contains(out.Messages[0].Content, "You are Claude Code.") {
		t.Errorf("system 内容缺失: %q", out.Messages[0].Content)
	}

	// 助手轮的 tool_use 必须还原，否则 agentic 多轮丢失工具调用上下文。
	var assistant *OllamaMessage
	for i := range out.Messages {
		if out.Messages[i].Role == "assistant" {
			assistant = &out.Messages[i]
			break
		}
	}
	if assistant == nil {
		t.Fatal("缺少 assistant 消息")
	}
	if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].Function.Name != "Bash" {
		t.Fatalf("助手 tool_calls 未还原: %+v", assistant.ToolCalls)
	}
	if assistant.ToolCalls[0].Function.Arguments["command"] != "ls" {
		t.Errorf("工具参数丢失: %+v", assistant.ToolCalls[0].Function.Arguments)
	}

	// tool_result 必须回到 role=tool，并带上工具名（id → name 映射）。
	var tool *OllamaMessage
	for i := range out.Messages {
		if out.Messages[i].Role == "tool" {
			tool = &out.Messages[i]
			break
		}
	}
	if tool == nil {
		t.Fatal("缺少 role=tool 消息")
	}
	if tool.ToolName != "Bash" {
		t.Errorf("tool 消息工具名 = %q，期望 Bash", tool.ToolName)
	}
	if tool.Content != "a\nb" {
		t.Errorf("tool 消息内容 = %q", tool.Content)
	}
}
