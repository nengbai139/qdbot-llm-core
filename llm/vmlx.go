package llm

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// HandlerVMLX 把 /v1/messages、/v1/chat/completions、/v1/responses、/v1/models
// 原样转到本地 vMLX。vMLX 自己说这四种协议，不再译成 Ollama。
func HandlerVMLX(base string) http.Handler {
	base = strings.TrimRight(base, "/")
	client := proxyClient()
	mux := http.NewServeMux()
	proxy := func(w http.ResponseWriter, r *http.Request) {
		proxyTo(client, base, w, r)
	}
	mux.HandleFunc("/v1/messages", proxy)
	mux.HandleFunc("/v1/chat/completions", proxy)
	mux.HandleFunc("/v1/responses", proxy)
	mux.HandleFunc("/v1/models", proxy)
	return mux
}

// ServeVMLX 启动面向 vMLX 的本地网关，阻塞直到进程退出。
func ServeVMLX(addr, base string) {
	log.Printf("qdbot-llm 网关监听 %s，上游 vMLX %s", addr, base)
	if err := http.ListenAndServe(addr, HandlerVMLX(base)); err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}

func proxyClient() *http.Client {
	tr, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Client{}
	}
	cloned := tr.Clone()
	cloned.ResponseHeaderTimeout = 10 * time.Minute
	return &http.Client{Transport: cloned}
}

func proxyTo(client *http.Client, base string, w http.ResponseWriter, r *http.Request) {
	target := base + r.URL.Path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	up, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("构造上游请求失败: %v", err), http.StatusBadGateway)
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		up.Header.Set("Content-Type", ct)
	}
	if accept := r.Header.Get("Accept"); accept != "" {
		up.Header.Set("Accept", accept)
	}
	resp, err := client.Do(up)
	if err != nil {
		http.Error(w, fmt.Sprintf("调用 vMLX 失败: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	copyHeader(w.Header(), resp.Header, "Content-Type", "Cache-Control")
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			return
		}
	}
}

func copyHeader(dst, src http.Header, keys ...string) {
	for _, k := range keys {
		if v := src.Get(k); v != "" {
			dst.Set(k, v)
		}
	}
}
