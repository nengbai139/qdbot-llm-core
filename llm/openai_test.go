package llm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIRoutes(t *testing.T) {
	var seenModel string
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]string{{"name": "gemma4:latest"}},
			})
		case "/api/chat":
			var req OllamaChatRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode upstream: %v", err)
			}
			seenModel = req.Model
			if req.Stream {
				w.Header().Set("Content-Type", "application/x-ndjson")
				_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":"hi"},"done":false}`+"\n")
				_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":""},"done":true,"done_reason":"stop"}`+"\n")
				return
			}
			_ = json.NewEncoder(w).Encode(OllamaChatResponse{
				Model: req.Model, Done: true, DoneReason: "stop",
				Message:         OllamaMessage{Role: "assistant", Content: "pong"},
				PromptEvalCount: 3, EvalCount: 1,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollama.Close()
	h := Handler(ollama.URL)

	chat := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"gemma4@qwen3","messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, chat)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"content":"pong"`) || seenModel != "gemma4" {
		t.Fatalf("chat %d model %q body %s", rec.Code, seenModel, rec.Body.String())
	}

	stream := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"gemma4","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, stream)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"content":"hi"`) || !strings.Contains(rec.Body.String(), "data: [DONE]") {
		t.Fatalf("stream %d %s", rec.Code, rec.Body.String())
	}

	resp := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(
		`{"model":"gemma4","input":"hi"}`))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, resp)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "output_text") || !strings.Contains(rec.Body.String(), "pong") {
		t.Fatalf("responses %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "gemma4:latest") || !strings.Contains(rec.Body.String(), `"owned_by":"ollama"`) {
		t.Fatalf("models %d %s", rec.Code, rec.Body.String())
	}
}
