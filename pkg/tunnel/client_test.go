package tunnel

import (
	"bufio"
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProxyTriesNextAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, "http://tunnel.example/", nil)
	var raw bytes.Buffer
	_ = req.Write(&raw)

	got := proxyToLocal([]string{"127.0.0.1:1", strings.TrimPrefix(srv.URL, "http://")}, raw.Bytes())
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(got)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d, want the second address", resp.StatusCode)
	}
}
