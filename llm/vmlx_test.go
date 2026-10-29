package llm

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVMLXProxiesNativePath(t *testing.T) {
	var gotPath, gotBody string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer up.Close()

	h := HandlerVMLX(up.URL)
	body := `{"model":"mlx-community/Qwen3-8B-4bit","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || gotPath != "/v1/messages" || !strings.Contains(gotBody, "Qwen3-8B-4bit") {
		t.Fatalf("code %d path %q body %q resp %s", rec.Code, gotPath, gotBody, rec.Body.String())
	}
	if strings.Contains(gotPath, "/api/chat") {
		t.Fatal("vMLX 请求不应改写成 Ollama")
	}
}
