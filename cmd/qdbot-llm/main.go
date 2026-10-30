// qdbot-llm：本地 LLM 网关 + 反向隧道暴露。
//
// 复用 qdbot_tunnel 的 client 端反代隧道：经 github.com/nengbai139/qdbot-llm-core/pkg/rtunnel 门面，
// 把本地 Ollama 网关暴露成公网 Anthropic 兼容端点。
//
// 子命令：
//
//	qdbot-llm serve             仅本地网关（P0）
//	qdbot-llm expose            网关 + 反向隧道（P3，公网）
//
// 环境变量：
//
//	OLLAMA_URL  Ollama 地址，默认 http://127.0.0.1:11434
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/nengbai139/qdbot-llm-core/llm"
	"github.com/nengbai139/qdbot-llm-core/pkg/rtunnel"
)

// version 由 build.sh 用 -ldflags "-X main.version=..." 注入（本地开发时为 dev）。
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	switch os.Args[1] {
	case "serve":
		cmdServe(os.Args[2:])
	case "expose":
		cmdExpose(os.Args[2:])
	case "install":
		cmdInstall()
	case "pull":
		cmdPull(os.Args[2:])
	case "list":
		cmdList()
	case "daemon":
		cmdDaemon(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q\n\n", os.Args[1])
		usage()
	}
}

func usage() {
	fmt.Printf(`qdbot-llm %s

用法: qdbot-llm <子命令>

  install             查看 Ollama / vMLX 是否就绪，并给出下一步（不自动装）
  pull <model>        拉取模型（透传 ollama pull）
  list                列出本地模型（透传 ollama list）
  serve               仅本地网关（127.0.0.1:4000，P0）
  expose              网关 + 反向隧道（复用 qdbot_tunnel 隧道，P3）
  daemon              服务常驻 + 开机自启（复用 qdbot_tunnel 三平台 daemon）

环境变量:
  OLLAMA_URL  Ollama 地址，默认 http://127.0.0.1:11434
  VMLX_URL    vMLX 地址，默认 http://127.0.0.1:8000`, version)
}

// serve 只起本地网关，不做公网暴露。
func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:4000", "网关监听地址")
	backend, ollama, vmlx := upstreamFlags(fs)
	_ = fs.Parse(args)
	serveUpstream(*addr, *backend, *ollama, *vmlx)
}

// expose 起网关 + 复用 qdbot_tunnel 的 client 端反代隧道，把网关暴露到公网。
// 可选 --model --price-in/--price-out 把模型注册进市场目录。
func cmdExpose(args []string) {
	fs := flag.NewFlagSet("expose", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:4000", "网关监听地址")
	backend, ollama, vmlx := upstreamFlags(fs)
	model := fs.String("model", "", "注册到市场的模型名（如 gemma4），空 = 不注册")
	priceIn := fs.Float64("price-in", 0, "¥/百万 input token")
	priceOut := fs.Float64("price-out", 0, "¥/百万 output token")
	payoutUser := fs.String("payout-user", "", "提供方 qdbot_account user_id（分成收款账号，user_...）")
	_ = fs.Parse(args)

	// 1. 后台起本地网关（隧道转发的目标）
	go serveUpstream(*addr, *backend, *ollama, *vmlx)

	// 2. 复用 qdbot_tunnel 的反向隧道：把 local 指向本地网关。
	exposed, err := rtunnel.Expose(*addr)
	if err != nil {
		log.Fatalf("启动反向隧道失败: %v", err)
	}
	log.Printf("反向隧道已启动（复用 qdbot_tunnel），本地网关 %s → 公网", *addr)

	// 3. 可选：把模型注册进市场目录（写 tunnel-web 的设备 model 字段）
	if *model != "" {
		if err := rtunnel.RegisterModel(*model, *priceIn, *priceOut, *payoutUser); err != nil {
			log.Printf("注册模型失败（隧道仍可用）: %v", err)
		} else {
			log.Printf("模型已注册到市场: %s（¥%.2f / ¥%.2f 每百万 token）", *model, *priceIn, *priceOut)
		}
	}

	// 4. 轮询打印服务端分配的公网 URL（首次握手后填入）
	for {
		time.Sleep(3 * time.Second)
		if u := exposed.URL(); u != "" {
			log.Printf("公网端点: %s （/v1/messages、/v1/chat/completions、/v1/responses）", u)
		}
	}
}

func upstreamFlags(fs *flag.FlagSet) (backend, ollama, vmlx *string) {
	backend = fs.String("backend", "ollama", "上游：ollama 或 vmlx")
	ollama = fs.String("ollama", ollamaEnv(), "Ollama 地址")
	vmlx = fs.String("vmlx", vmlxEnv(), "vMLX 地址")
	return
}

// serveUpstream 按 backend 启动。vMLX 原样转发；Ollama 走协议翻译。
// 指定了 vmlx 但进程没在听、Ollama 里已有模型时，改走 Ollama，避免再下一份权重。
func serveUpstream(addr, backend, ollama, vmlx string) {
	s := inspect(ollama, vmlx, backend == "vmlx", vmlxOnThisOS())
	use, err := pickBackend(backend, s.vmlxUp, len(s.models) > 0)
	if err != nil {
		fmt.Fprint(os.Stderr, s.report())
		os.Exit(1)
	}
	if backend == "vmlx" && use == "ollama" {
		log.Printf("vMLX 未启动，改用 Ollama 已有模型（%s），不另下载一份", strings.Join(s.models, ", "))
	}
	if use == "ollama" {
		if msg := s.ollamaProblem(); msg != "" {
			log.Print(msg)
		}
	}
	switch use {
	case "vmlx":
		llm.ServeVMLX(addr, vmlx)
	case "ollama":
		llm.Serve(addr, ollama)
	default:
		log.Fatalf("未知 backend %q（用 ollama 或 vmlx）", backend)
	}
}

// pickBackend 决定实际上游。vMLX 只有已经在监听时才用；否则有 Ollama 模型就用那一份。
func pickBackend(requested string, vmlxUp, ollamaHasModels bool) (string, error) {
	switch requested {
	case "", "ollama":
		return "ollama", nil
	case "vmlx":
		if vmlxUp {
			return "vmlx", nil
		}
		if ollamaHasModels {
			return "ollama", nil
		}
		return "", fmt.Errorf("vMLX 未启动，Ollama 里也没有模型")
	default:
		return "", fmt.Errorf("未知 backend %q（用 ollama 或 vmlx）", requested)
	}
}

func endpointUp(url string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

func ollamaModels(base string) (names []string, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/api/tags", nil)
	if err != nil {
		return nil, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil {
		return nil, false
	}
	for _, m := range body.Models {
		if m.Name != "" {
			names = append(names, m.Name)
		}
	}
	return names, true
}

func vmlxOnThisOS() bool {
	return runtimeGOOS == "darwin"
}

// runtimeGOOS 便于测试替换。默认是本机系统。
var runtimeGOOS = goos()

// ollamaEnv 返回 OLLAMA_URL，未设置时返回默认值。
func ollamaEnv() string {
	if v := os.Getenv("OLLAMA_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:11434"
}

func vmlxEnv() string {
	if v := os.Getenv("VMLX_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:8000"
}

func goos() string { return runtime.GOOS }
