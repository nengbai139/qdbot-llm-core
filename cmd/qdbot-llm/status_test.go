package main

import (
	"strings"
	"testing"
)

func TestNextStepWhenOllamaAlreadyHasModels(t *testing.T) {
	s := setup{
		ollamaBin:       "/usr/local/bin/ollama",
		ollamaURL:       "http://127.0.0.1:11434",
		ollamaReachable: true,
		models:          []string{"gemma4:12b-mlx"},
		vmlxSupported:   true,
	}
	got := s.next()
	if !strings.Contains(got, "qdbot-llm expose --model gemma4:12b-mlx") {
		t.Fatalf("next: %s", got)
	}
	rep := s.report()
	if !strings.Contains(rep, "不必再装") {
		t.Fatalf("已有模型时不应劝装 vMLX:\n%s", rep)
	}
	if msg := s.ollamaProblem(); msg != "" {
		t.Fatalf("能用时不应提示: %s", msg)
	}
}

func TestNextStepWhenNothingInstalled(t *testing.T) {
	s := setup{ollamaURL: "http://127.0.0.1:11434", vmlxSupported: true}
	if !strings.Contains(s.next(), "先安装 Ollama") {
		t.Fatalf("next: %s", s.next())
	}
	if !strings.Contains(s.report(), "uv tool install vmlx") {
		t.Fatal(s.report())
	}
	if s.ollamaProblem() == "" {
		t.Fatal("没装 Ollama 时应提示")
	}
}

func TestVMLXHiddenOffMac(t *testing.T) {
	s := setup{ollamaBin: "/usr/bin/ollama", ollamaReachable: true}
	rep := s.report()
	if strings.Contains(rep, "uv tool install") {
		t.Fatalf("非 mac 不应推荐 vMLX:\n%s", rep)
	}
}
