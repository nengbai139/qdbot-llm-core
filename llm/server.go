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

// Serve 启动本地网关，阻塞直到进程退出。
// Anthropic /v1/messages、OpenAI /v1/chat/completions、Responses /v1/responses 都译成 Ollama /api/chat。
func Serve(addr, ollamaURL string) {
	log.Printf("qdbot-llm 网关监听 %s，上游 Ollama %s", addr, ollamaURL)
	if err := http.ListenAndServe(addr, Handler(ollamaURL)); err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}

// Handler 是本地网关的路由。测试和嵌入用它，避免占住 DefaultServeMux。
func Handler(ollamaURL string) http.Handler {
	// 本地大模型推理慢，给足超时（CPU 9B 模型单次可达数分钟）。
	client := &http.Client{Timeout: 15 * time.Minute}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", handleMessages(client, ollamaURL))
	mux.HandleFunc("/v1/chat/completions", handleChat(client, ollamaURL))
	mux.HandleFunc("/v1/responses", handleResponses(client, ollamaURL))
	mux.HandleFunc("/v1/models", handleModels(client, ollamaURL))
	return mux
}

// handleMessages 处理 Anthropic POST /v1/messages：翻译 → 转发 Ollama → 反向翻译。
func handleMessages(client *http.Client, ollamaURL string) http.HandlerFunc {
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

		upstream, err := translateAndPost(client, ollamaURL, req)
		if err != nil {
			code := http.StatusBadGateway
			if isBadRequest(err) {
				code = http.StatusBadRequest
			}
			http.Error(w, err.Error(), code)
			return
		}
		defer upstream.Body.Close()

		if req.Stream {
			streamAnthropic(w, upstream.Body, OllamaModelFor(req.Model))
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

// translateAndPost 把 Anthropic 形态的请求译成 Ollama /api/chat 并拿回响应。
// 调用方负责关闭成功时的 Body。请求本身不合法时返回 badRequestError。
func translateAndPost(client *http.Client, ollamaURL string, req AnthropicMessageRequest) (*http.Response, error) {
	oreq, err := ToOllama(req)
	if err != nil {
		return nil, badRequestError{err}
	}
	body, err := json.Marshal(oreq)
	if err != nil {
		return nil, err
	}
	up, err := client.Post(ollamaURL+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("调用上游失败: %v", err)
	}
	if up.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(up.Body)
		up.Body.Close()
		return nil, fmt.Errorf("上游返回 %d: %s", up.StatusCode, b)
	}
	return up, nil
}

type badRequestError struct{ error }

func isBadRequest(err error) bool {
	_, ok := err.(badRequestError)
	return ok
}
