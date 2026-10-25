package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// Serve 启动本地网关：POST /v1/messages（Anthropic）→ Ollama /api/chat。
// 阻塞直到进程退出。支持非流式与 SSE 流式（按请求的 stream 字段分流）。
func Serve(addr, ollamaURL string) {
	http.HandleFunc("/v1/messages", handleMessages(ollamaURL))
	log.Printf("qdbot-llm 网关监听 %s，上游 Ollama %s", addr, ollamaURL)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}

// handleMessages 处理 Anthropic POST /v1/messages：翻译 → 转发 Ollama → 反向翻译。
func handleMessages(ollamaURL string) http.HandlerFunc {
	// 本地大模型推理慢，给足超时（CPU 9B 模型单次可达数分钟）。
	client := &http.Client{Timeout: 15 * time.Minute}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "仅支持 POST", http.StatusMethodNotAllowed)
			return
		}

		var req AnthropicMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("解析请求失败: %v", err), http.StatusBadRequest)
			return
		}

		ollamaReq, err := ToOllama(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body, err := json.Marshal(ollamaReq)
		if err != nil {
			http.Error(w, fmt.Sprintf("编码上游请求失败: %v", err), http.StatusInternalServerError)
			return
		}

		upstream, err := client.Post(ollamaURL+"/api/chat", "application/json", bytes.NewReader(body))
		if err != nil {
			http.Error(w, fmt.Sprintf("调用上游失败: %v", err), http.StatusBadGateway)
			return
		}
		defer upstream.Body.Close()

		if upstream.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(upstream.Body)
			http.Error(w, fmt.Sprintf("上游返回 %d: %s", upstream.StatusCode, b), http.StatusBadGateway)
			return
		}

		if req.Stream {
			streamAnthropic(w, upstream.Body, ollamaReq.Model)
			return
		}

		var ollamaResp OllamaChatResponse
		if err := json.NewDecoder(upstream.Body).Decode(&ollamaResp); err != nil {
			http.Error(w, fmt.Sprintf("解析上游响应失败: %v", err), http.StatusBadGateway)
			return
		}
		resp := FromOllama(ollamaResp, newRequestID())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// streamAnthropic 把 Ollama 流式响应翻译成 Anthropic SSE 流写回。
func streamAnthropic(w http.ResponseWriter, body io.Reader, model string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, _ := w.(http.Flusher)
	if err := StreamOllamaToAnthropic(body, w, newRequestID(), model, flusher); err != nil {
		log.Printf("流式翻译失败: %v", err)
	}
}

func newRequestID() string {
	return fmt.Sprintf("msg_%d", time.Now().UnixNano())
}
