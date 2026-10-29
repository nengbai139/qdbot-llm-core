package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

// OpenAI Chat Completions 和 Responses 都先译成 Anthropic，再走原来的 Ollama 翻译。
// ponytail: 图片内容没做。升级路径是按 OpenAI 文档补 content part。

func handleChat(client *http.Client, ollamaURL string) http.HandlerFunc {
	return handleTranslated(client, ollamaURL, chatToAnthropic, writeChatBody)
}

func handleResponses(client *http.Client, ollamaURL string) http.HandlerFunc {
	return handleTranslated(client, ollamaURL, responsesToAnthropic, writeResponsesBody)
}

func handleModels(client *http.Client, ollamaURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeOAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "仅支持 GET")
			return
		}
		up, err := client.Get(ollamaURL + "/api/tags")
		if err != nil {
			writeOAIError(w, http.StatusBadGateway, "api_error", fmt.Sprintf("调用上游失败: %v", err))
			return
		}
		defer up.Body.Close()
		var tags struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := json.NewDecoder(up.Body).Decode(&tags); err != nil {
			writeOAIError(w, http.StatusBadGateway, "api_error", fmt.Sprintf("解析上游响应失败: %v", err))
			return
		}
		data := make([]map[string]string, 0, len(tags.Models))
		for _, m := range tags.Models {
			if m.Name == "" {
				continue
			}
			data = append(data, map[string]string{"id": m.Name, "object": "model", "owned_by": "ollama"})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	}
}

func handleTranslated(client *http.Client, ollamaURL string, decode func([]byte) (AnthropicMessageRequest, error), write func(http.ResponseWriter, io.Reader, string, bool) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeOAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "仅支持 POST")
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			writeOAIError(w, http.StatusBadRequest, "invalid_request_error", "读取请求失败")
			return
		}
		req, err := decode(raw)
		if err != nil {
			writeOAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		public := req.Model
		up, err := translateAndPost(client, ollamaURL, req)
		if err != nil {
			code, typ := http.StatusBadGateway, "api_error"
			if isBadRequest(err) {
				code, typ = http.StatusBadRequest, "invalid_request_error"
			}
			writeOAIError(w, code, typ, err.Error())
			return
		}
		defer up.Body.Close()
		if err := write(w, up.Body, public, req.Stream); err != nil {
			log.Printf("回写失败: %v", err)
		}
	}
}

func writeOAIError(w http.ResponseWriter, code int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": msg, "type": typ}})
}

func chatToAnthropic(raw []byte) (AnthropicMessageRequest, error) {
	var in struct {
		Model               string      `json:"model"`
		Messages            []oaMessage `json:"messages"`
		Stream              bool        `json:"stream"`
		MaxTokens           int         `json:"max_tokens"`
		MaxCompletionTokens int         `json:"max_completion_tokens"`
		Tools               []oaTool    `json:"tools"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return AnthropicMessageRequest{}, fmt.Errorf("解析请求失败: %v", err)
	}
	if in.Model == "" {
		return AnthropicMessageRequest{}, fmt.Errorf("缺少 model")
	}
	maxTok := in.MaxTokens
	if in.MaxCompletionTokens > 0 {
		maxTok = in.MaxCompletionTokens
	}
	return oaToAnthropic(in.Model, maxTok, in.Stream, "", in.Messages, in.Tools)
}

func responsesToAnthropic(raw []byte) (AnthropicMessageRequest, error) {
	var in struct {
		Model           string          `json:"model"`
		Input           json.RawMessage `json:"input"`
		Instructions    string          `json:"instructions"`
		Stream          bool            `json:"stream"`
		MaxOutputTokens int             `json:"max_output_tokens"`
		Tools           []oaTool        `json:"tools"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return AnthropicMessageRequest{}, fmt.Errorf("解析请求失败: %v", err)
	}
	if in.Model == "" {
		return AnthropicMessageRequest{}, fmt.Errorf("缺少 model")
	}
	msgs, err := responsesInput(in.Input)
	if err != nil {
		return AnthropicMessageRequest{}, err
	}
	return oaToAnthropic(in.Model, in.MaxOutputTokens, in.Stream, in.Instructions, msgs, in.Tools)
}

func oaToAnthropic(model string, maxTok int, stream bool, instructions string, msgs []oaMessage, tools []oaTool) (AnthropicMessageRequest, error) {
	if maxTok <= 0 {
		maxTok = 4096
	}
	var sys strings.Builder
	if instructions != "" {
		sys.WriteString(instructions)
	}
	var out []AnthropicMessage
	for _, m := range msgs {
		switch m.Role {
		case "system", "developer":
			if sys.Len() > 0 {
				sys.WriteByte('\n')
			}
			sys.WriteString(m.text())
		case "tool":
			out = append(out, AnthropicMessage{
				Role: "user",
				Content: AnthropicContent{{
					Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.text(),
				}},
			})
		default:
			role := "user"
			if m.Role == "assistant" {
				role = "assistant"
			}
			blocks := []ContentBlock{}
			if t := m.text(); t != "" {
				blocks = append(blocks, ContentBlock{Type: "text", Text: t})
			}
			for _, tc := range m.ToolCalls {
				var input map[string]any
				if json.Unmarshal([]byte(tc.Function.Arguments), &input) != nil {
					input = map[string]any{"raw": tc.Function.Arguments}
				}
				blocks = append(blocks, ContentBlock{
					Type: "tool_use", ID: tc.ID, Name: tc.Function.Name, Input: input,
				})
			}
			if len(blocks) == 0 {
				blocks = []ContentBlock{{Type: "text"}}
			}
			out = append(out, AnthropicMessage{Role: role, Content: blocks})
		}
	}
	var aTools []AnthropicTool
	for _, t := range tools {
		name, desc, schema := t.normalized()
		if name == "" {
			continue
		}
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		aTools = append(aTools, AnthropicTool{Name: name, Description: desc, InputSchema: schema})
	}
	req := AnthropicMessageRequest{Model: model, MaxTokens: maxTok, Messages: out, Stream: stream, Tools: aTools}
	if sys.Len() > 0 {
		req.System = AnthropicContent{{Type: "text", Text: sys.String()}}
	}
	return req, nil
}

type oaMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id"`
	ToolCalls  []struct {
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

func (m oaMessage) text() string {
	if len(m.Content) == 0 || string(m.Content) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(m.Content, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "" || p.Type == "text" || p.Type == "input_text" {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return ""
}

type oaTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Function    *struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

func (t oaTool) normalized() (string, string, map[string]any) {
	if t.Function != nil && t.Function.Name != "" {
		return t.Function.Name, t.Function.Description, t.Function.Parameters
	}
	return t.Name, t.Description, t.Parameters
}

func responsesInput(raw json.RawMessage) ([]oaMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("缺少 input")
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		b, _ := json.Marshal(s)
		return []oaMessage{{Role: "user", Content: b}}, nil
	}
	var msgs []oaMessage
	if err := json.Unmarshal(raw, &msgs); err != nil {
		return nil, fmt.Errorf("解析 input 失败: %v", err)
	}
	return msgs, nil
}

func writeChatBody(w http.ResponseWriter, body io.Reader, model string, stream bool) error {
	if stream {
		return writeChatStream(w, body, model)
	}
	var ollamaResp OllamaChatResponse
	if err := json.NewDecoder(body).Decode(&ollamaResp); err != nil {
		writeOAIError(w, http.StatusBadGateway, "api_error", fmt.Sprintf("解析上游响应失败: %v", err))
		return err
	}
	resp := FromOllama(ollamaResp, newRequestID())
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(chatFromAnthropic(resp, model))
}

func writeResponsesBody(w http.ResponseWriter, body io.Reader, model string, stream bool) error {
	if stream {
		return writeResponsesStream(w, body, model)
	}
	var ollamaResp OllamaChatResponse
	if err := json.NewDecoder(body).Decode(&ollamaResp); err != nil {
		writeOAIError(w, http.StatusBadGateway, "api_error", fmt.Sprintf("解析上游响应失败: %v", err))
		return err
	}
	resp := FromOllama(ollamaResp, newRequestID())
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(responsesFromAnthropic(resp, model))
}

func chatFromAnthropic(resp AnthropicMessageResponse, model string) map[string]any {
	text, calls := splitOAIBlocks(resp.Content)
	msg := map[string]any{"role": "assistant", "content": text}
	reason := "stop"
	if len(calls) > 0 {
		msg["tool_calls"] = calls
		reason = "tool_calls"
	} else if resp.StopReason == "max_tokens" {
		reason = "length"
	}
	return map[string]any{
		"id": resp.ID, "object": "chat.completion", "model": model,
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": reason}},
		"usage": map[string]int{
			"prompt_tokens": resp.Usage.InputTokens, "completion_tokens": resp.Usage.OutputTokens,
			"total_tokens": resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
	}
}

func responsesFromAnthropic(resp AnthropicMessageResponse, model string) map[string]any {
	text, calls := splitOAIBlocks(resp.Content)
	output := []any{}
	if text != "" {
		output = append(output, map[string]any{
			"type": "message", "role": "assistant",
			"content": []any{map[string]string{"type": "output_text", "text": text}},
		})
	}
	for _, c := range calls {
		fn := c["function"].(map[string]string)
		output = append(output, map[string]any{
			"type": "function_call", "call_id": c["id"], "name": fn["name"], "arguments": fn["arguments"],
		})
	}
	return map[string]any{
		"id": resp.ID, "object": "response", "status": "completed", "model": model, "output": output,
		"usage": map[string]int{
			"input_tokens": resp.Usage.InputTokens, "output_tokens": resp.Usage.OutputTokens,
			"total_tokens": resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
	}
}

func splitOAIBlocks(blocks []ContentBlock) (string, []map[string]any) {
	var text strings.Builder
	var calls []map[string]any
	for _, b := range blocks {
		switch b.Type {
		case "text", "":
			text.WriteString(b.Text)
		case "tool_use":
			args, _ := json.Marshal(b.Input)
			calls = append(calls, map[string]any{
				"id": b.ID, "type": "function",
				"function": map[string]string{"name": b.Name, "arguments": string(args)},
			})
		}
	}
	return text.String(), calls
}

func writeChatStream(w http.ResponseWriter, body io.Reader, model string) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	return StreamOllamaToOpenAI(body, w, model, flusher)
}

func writeResponsesStream(w http.ResponseWriter, body io.Reader, model string) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	return StreamOllamaToResponses(body, w, model, flusher)
}

// StreamOllamaToOpenAI 把 Ollama NDJSON 写成 OpenAI chat.completion.chunk。
func StreamOllamaToOpenAI(r io.Reader, w io.Writer, model string, flusher Flusher) error {
	id := newRequestID()
	roleSent := false
	sawTool := false
	err := eachOllama(r, func(chunk OllamaChatResponse) error {
		if chunk.Message.Content != "" {
			delta := map[string]any{"content": chunk.Message.Content}
			if !roleSent {
				delta["role"] = "assistant"
				roleSent = true
			}
			if err := writeData(w, chatChunk(id, model, delta, nil)); err != nil {
				return err
			}
		}
		for i, tc := range chunk.Message.ToolCalls {
			sawTool = true
			args, _ := json.Marshal(tc.Function.Arguments)
			delta := map[string]any{"tool_calls": []any{map[string]any{
				"index": i, "id": tc.Function.Name, "type": "function",
				"function": map[string]string{"name": tc.Function.Name, "arguments": string(args)},
			}}}
			if err := writeData(w, chatChunk(id, model, delta, nil)); err != nil {
				return err
			}
		}
		if chunk.Done {
			reason := "stop"
			if sawTool {
				reason = "tool_calls"
			} else if chunk.DoneReason == "length" {
				reason = "length"
			}
			if err := writeData(w, chatChunk(id, model, map[string]any{}, reason)); err != nil {
				return err
			}
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	})
	_, werr := io.WriteString(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
	if err != nil {
		return err
	}
	return werr
}

func chatChunk(id, model string, delta map[string]any, reason any) map[string]any {
	return map[string]any{
		"id": id, "object": "chat.completion.chunk", "model": model,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": reason}},
	}
}

// StreamOllamaToResponses 把 Ollama NDJSON 写成 Responses 的文本增量，结束时给 completed。
func StreamOllamaToResponses(r io.Reader, w io.Writer, model string, flusher Flusher) error {
	var text strings.Builder
	in, out := 0, 0
	err := eachOllama(r, func(chunk OllamaChatResponse) error {
		if chunk.PromptEvalCount > 0 {
			in = chunk.PromptEvalCount
		}
		if chunk.EvalCount > 0 {
			out = chunk.EvalCount
		}
		if chunk.Message.Content == "" {
			return nil
		}
		text.WriteString(chunk.Message.Content)
		return writeEvent(w, flusher, "response.output_text.delta", map[string]any{
			"type": "response.output_text.delta", "delta": chunk.Message.Content,
		})
	})
	cerr := writeEvent(w, flusher, "response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": newRequestID(), "object": "response", "status": "completed", "model": model,
			"output": []any{map[string]any{
				"type": "message", "role": "assistant",
				"content": []any{map[string]string{"type": "output_text", "text": text.String()}},
			}},
			"usage": map[string]int{"input_tokens": in, "output_tokens": out, "total_tokens": in + out},
		},
	})
	if err != nil {
		return err
	}
	return cerr
}

func eachOllama(r io.Reader, fn func(OllamaChatResponse) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var chunk OllamaChatResponse
		if json.Unmarshal(line, &chunk) != nil {
			continue
		}
		if err := fn(chunk); err != nil {
			return err
		}
	}
	return sc.Err()
}

func writeData(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", b)
	return err
}

func writeEvent(w io.Writer, flusher Flusher, event string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	if flusher != nil {
		flusher.Flush()
	}
	return err
}
