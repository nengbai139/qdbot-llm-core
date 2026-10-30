package main

import "testing"

func TestPickBackendUsesExistingOllamaWeights(t *testing.T) {
	use, err := pickBackend("vmlx", false, true)
	if err != nil || use != "ollama" {
		t.Fatalf("got %q %v", use, err)
	}
	use, err = pickBackend("vmlx", true, true)
	if err != nil || use != "vmlx" {
		t.Fatalf("vMLX 已在听仍应走 vmlx, got %q %v", use, err)
	}
	if _, err = pickBackend("vmlx", false, false); err == nil {
		t.Fatal("两边都没有模型时应失败")
	}
	use, err = pickBackend("ollama", false, false)
	if err != nil || use != "ollama" {
		t.Fatalf("默认应走 ollama, got %q %v", use, err)
	}
}
