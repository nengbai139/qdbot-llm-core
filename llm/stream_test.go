package llm

import (
	"bytes"
	"strings"
	"testing"
)

func TestStreamOllamaToAnthropic(t *testing.T) {
	ndjson := `{"model":"gemma4","message":{"role":"assistant","content":"你好"},"done":false}
{"model":"gemma4","message":{"role":"assistant","content":"，世界"},"done":false}
{"model":"gemma4","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":8}
`
	var buf bytes.Buffer
	if err := StreamOllamaToAnthropic(strings.NewReader(ndjson), &buf, "msg_1", "gemma4", nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, want := range []string{
		"event: message_start",
		"event: content_block_start",
		"event: content_block_delta",
		"event: content_block_stop",
		"event: message_delta",
		"event: message_stop",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}

	// 文本增量按顺序出现
	if !strings.Contains(out, "你好") || !strings.Contains(out, "，世界") {
		t.Fatalf("missing text deltas in:\n%s", out)
	}
	// 终止原因是 end_turn（done_reason=stop 的映射）
	if !strings.Contains(out, `"stop_reason":"end_turn"`) {
		t.Fatalf("missing end_turn stop_reason in:\n%s", out)
	}
	// 输出 token 取自最终块的 eval_count
	if !strings.Contains(out, `"output_tokens":8`) {
		t.Fatalf("missing output_tokens:8 in:\n%s", out)
	}
}

func TestStreamOllamaToolCall(t *testing.T) {
	// 实测 gemma4 流式：先 thinking（content 空），再原子给 tool_calls，最后 done
	ndjson := `{"model":"gemma4","message":{"role":"assistant","content":"","thinking":"..."},"done":false}
{"model":"gemma4","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","function":{"index":0,"name":"get_weather","arguments":{"city":"SF"}}}]},"done":false}
{"model":"gemma4","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","eval_count":10}
`
	var buf bytes.Buffer
	if err := StreamOllamaToAnthropic(strings.NewReader(ndjson), &buf, "msg_1", "gemma4", nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, want := range []string{
		"content_block_start",
		`"type":"tool_use"`,
		`"name":"get_weather"`,
		`"input_json_delta"`,
		`"partial_json":"{\"city\":\"SF\"}"`,
		"content_block_stop",
		`"stop_reason":"tool_use"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestStreamOllamaToAnthropicIgnoresGarbage(t *testing.T) {
	// 流里混入空行与解析不出来的 keepalive，不应中断
	ndjson := "\nnot-json\n\n" +
		"{\"model\":\"gemma4\",\"message\":{\"role\":\"assistant\",\"content\":\"ok\"},\"done\":false}\n" +
		"{\"model\":\"gemma4\",\"message\":{\"role\":\"assistant\",\"content\":\"\"},\"done\":true,\"done_reason\":\"stop\"}\n"
	var buf bytes.Buffer
	if err := StreamOllamaToAnthropic(strings.NewReader(ndjson), &buf, "msg_2", "gemma4", nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "event: message_stop") {
		t.Fatalf("missing message_stop in:\n%s", out)
	}
	if !strings.Contains(out, "ok") {
		t.Fatalf("missing content in:\n%s", out)
	}
}
