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
	"flag"
	"fmt"
	"log"
	"os"
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

  install             检测 ollama，未装则打印安装提示（不自动装）
  pull <model>        拉取模型（透传 ollama pull）
  list                列出本地模型（透传 ollama list）
  serve               仅本地网关（127.0.0.1:4000，P0）
  expose              网关 + 反向隧道（复用 qdbot_tunnel 隧道，P3）
  daemon              服务常驻 + 开机自启（复用 qdbot_tunnel 三平台 daemon）

环境变量:
  OLLAMA_URL  Ollama 地址，默认 http://127.0.0.1:11434`, version)
}

// serve 只起本地网关，不做公网暴露。
func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:4000", "网关监听地址")
	ollama := fs.String("ollama", ollamaEnv(), "Ollama 地址")
	_ = fs.Parse(args)

	log.Printf("本地网关监听 %s，上游 Ollama %s", *addr, *ollama)
	llm.Serve(*addr, *ollama)
}

// expose 起网关 + 复用 qdbot_tunnel 的 client 端反代隧道，把网关暴露到公网。
// 可选 --model --price-in/--price-out 把模型注册进市场目录。
func cmdExpose(args []string) {
	fs := flag.NewFlagSet("expose", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:4000", "网关监听地址")
	ollama := fs.String("ollama", ollamaEnv(), "Ollama 地址")
	model := fs.String("model", "", "注册到市场的模型名（如 gemma4），空 = 不注册")
	priceIn := fs.Float64("price-in", 0, "¥/百万 input token")
	priceOut := fs.Float64("price-out", 0, "¥/百万 output token")
	payoutUser := fs.String("payout-user", "", "提供方 qdbot_account user_id（分成收款账号，user_...）")
	_ = fs.Parse(args)

	// 1. 后台起本地网关（隧道转发的目标）
	go llm.Serve(*addr, *ollama)

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
			log.Printf("公网 Anthropic 端点: %s/v1/messages", u)
		}
	}
}

// ollamaEnv 返回 OLLAMA_URL，未设置时返回默认值。
func ollamaEnv() string {
	if v := os.Getenv("OLLAMA_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:11434"
}
