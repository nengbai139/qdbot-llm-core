package main

import (
	"fmt"
	"os"
	"os/exec"
)

// ollamaBin 返回 ollama 可执行文件路径；未安装返回空串。
func ollamaBin() string {
	p, err := exec.LookPath("ollama")
	if err != nil {
		return ""
	}
	return p
}

// cmdInstall 查看 Ollama 和 vMLX 是否就绪，并给出下一步。
// 刻意不自动安装，系统级改动交给用户自己决定。
func cmdInstall() {
	fmt.Print(inspect(ollamaEnv(), vmlxEnv(), true, vmlxOnThisOS()).report())
}

// cmdPull 拉取模型（透传给 ollama pull）。
func cmdPull(args []string) {
	if len(args) == 0 {
		fmt.Println("用法: qdbot-llm pull <model>")
		return
	}
	if !requireOllama() {
		return
	}
	runOllama("pull", args[0])
}

// cmdList 列出本地已拉取的模型（透传给 ollama list）。
func cmdList() {
	if !requireOllama() {
		return
	}
	runOllama("list", "")
}

// requireOllama 检查 ollama 是否安装，未装则提示并返回 false。
func requireOllama() bool {
	if ollamaBin() != "" {
		return true
	}
	fmt.Println("未安装 ollama，先运行 qdbot-llm install 看安装提示")
	return false
}

// runOllama 前台执行 ollama 子命令，标准输出/错误透传给用户。
func runOllama(sub, model string) {
	args := []string{sub}
	if model != "" {
		args = append(args, model)
	}
	cmd := exec.Command("ollama", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "ollama %s 失败: %v\n", sub, err)
		os.Exit(1)
	}
}
