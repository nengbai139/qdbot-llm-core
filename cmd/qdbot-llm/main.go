// qdbot-llm：本地 LLM 网关。
//
// 把本地 Ollama 模型翻译成 Anthropic Messages 协议，在 127.0.0.1:4000 起本地 HTTP 服务，
// 让 Claude Code、Anthropic SDK 等工具直接接入本地模型。模型与数据不出本机。
//
// 子命令：
//
//	qdbot-llm serve             启动本地网关
//	qdbot-llm install           检测 ollama，未装则打印安装提示（不自动装）
//	qdbot-llm pull <model>      拉取模型（透传 ollama pull）
//	qdbot-llm list              列出本地模型（透传 ollama list）
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

	"github.com/nengbai139/qdbot-llm-core/llm"
)

// version 由构建时用 -ldflags "-X main.version=..." 注入（本地开发时为 dev）。
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	switch os.Args[1] {
	case "serve":
		cmdServe(os.Args[2:])
	case "install":
		cmdInstall()
	case "pull":
		cmdPull(os.Args[2:])
	case "list":
		cmdList()
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
  serve               启动本地网关（127.0.0.1:4000）

环境变量:
  OLLAMA_URL  Ollama 地址，默认 http://127.0.0.1:11434`, version)
}

// cmdServe 启动本地网关（不做任何公网暴露）。
func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:4000", "网关监听地址")
	ollama := fs.String("ollama", ollamaEnv(), "Ollama 地址")
	_ = fs.Parse(args)

	log.Printf("本地网关监听 %s，上游 Ollama %s", *addr, *ollama)
	llm.Serve(*addr, *ollama)
}

// ollamaEnv 返回 OLLAMA_URL，未设置时返回默认值。
func ollamaEnv() string {
	if v := os.Getenv("OLLAMA_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:11434"
}
