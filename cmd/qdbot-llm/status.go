package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// setup 是用户眼前的两套运行时。只描述已经发生的事，不安装、不下载。
type setup struct {
	ollamaBin       string
	ollamaURL       string
	ollamaReachable bool
	models          []string
	vmlxBin         string
	vmlxURL         string
	vmlxUp          bool
	vmlxSupported   bool // 仅 macOS。vMLX 跑在 Apple 芯片上。
}

func inspect(ollamaURL, vmlxURL string, checkVMLX, vmlxSupported bool) setup {
	s := setup{
		ollamaBin:     ollamaBin(),
		ollamaURL:     ollamaURL,
		vmlxURL:       vmlxURL,
		vmlxSupported: vmlxSupported,
	}
	if names, ok := ollamaModels(ollamaURL); ok {
		s.ollamaReachable = true
		s.models = names
	}
	if checkVMLX {
		if p, err := exec.LookPath("vmlx"); err == nil {
			s.vmlxBin = p
		}
		s.vmlxUp = endpointUp(strings.TrimRight(vmlxURL, "/") + "/v1/models")
	}
	return s
}

func (s setup) report() string {
	var b strings.Builder
	b.WriteString("Ollama\n")
	switch {
	case s.ollamaBin == "":
		b.WriteString("  未安装。本地模型只留这一份：\n")
		b.WriteString("  macOS:   brew install ollama\n")
		b.WriteString("  Linux:   curl -fsSL https://ollama.com/install.sh | sh\n")
		b.WriteString("  Windows: https://ollama.com/download\n")
		b.WriteString("  装好后先 ollama serve，再 qdbot-llm pull <模型名>\n")
	case !s.ollamaReachable:
		fmt.Fprintf(&b, "  已安装: %s\n", s.ollamaBin)
		fmt.Fprintf(&b, "  服务没在听 %s\n", s.ollamaURL)
		b.WriteString("  先运行: ollama serve\n")
	case len(s.models) == 0:
		fmt.Fprintf(&b, "  已安装: %s\n", s.ollamaBin)
		b.WriteString("  服务在跑，还没有模型\n")
		b.WriteString("  拉取: qdbot-llm pull <模型名>\n")
	default:
		fmt.Fprintf(&b, "  已安装: %s\n", s.ollamaBin)
		fmt.Fprintf(&b, "  服务在跑，模型: %s\n", strings.Join(s.models, ", "))
	}

	b.WriteString("\nvMLX\n")
	switch {
	case !s.vmlxSupported:
		b.WriteString("  只支持 macOS（Apple 芯片）。这个系统用 Ollama。\n")
	case s.vmlxUp:
		fmt.Fprintf(&b, "  服务在跑 %s\n", s.vmlxURL)
		b.WriteString("  只会用这里已经加载的模型，不会去读 Ollama 的目录。\n")
	case s.vmlxBin != "":
		fmt.Fprintf(&b, "  已安装: %s\n", s.vmlxBin)
		fmt.Fprintf(&b, "  服务没在听 %s\n", s.vmlxURL)
		if len(s.models) > 0 {
			b.WriteString("  不必启动。Ollama 里已有模型，再开一份会多占磁盘。\n")
		}
	case len(s.models) > 0:
		b.WriteString("  未安装。模型已在 Ollama，不必再装。\n")
	default:
		b.WriteString("  未安装。Ollama 里没有需要的模型时再装: uv tool install vmlx\n")
		b.WriteString("  它不使用 ~/.ollama/models，会另外下载。\n")
	}

	fmt.Fprintf(&b, "\n下一步\n  %s\n", s.next())
	return b.String()
}

func (s setup) next() string {
	switch {
	case s.vmlxUp && len(s.models) == 0:
		return "qdbot-llm serve --backend vmlx"
	case len(s.models) == 1:
		return "本机用 qdbot-llm serve；挂公网先 qtunnel login，再 qdbot-llm expose --model " + s.models[0] + "。已经注册过就沿用原来的名字。"
	case len(s.models) > 1:
		return "本机用 qdbot-llm serve；挂公网先 qtunnel login，再 qdbot-llm expose --model <上面的某个模型名>。已经注册过就沿用原来的名字。"
	case s.ollamaBin != "" && !s.ollamaReachable:
		return "ollama serve"
	case s.ollamaBin != "":
		return "qdbot-llm pull <模型名>"
	default:
		return "先安装 Ollama，再运行 qdbot-llm install"
	}
}

// ollamaProblem 在准备用 Ollama、但还用不了时给出一句。能用则返回空串。
func (s setup) ollamaProblem() string {
	if s.ollamaReachable && len(s.models) > 0 {
		return ""
	}
	if s.ollamaBin == "" {
		return "未安装 Ollama。运行 qdbot-llm install 看安装步骤"
	}
	if !s.ollamaReachable {
		return "Ollama 没在听 " + s.ollamaURL + "。先运行 ollama serve"
	}
	return "Ollama 里还没有模型。运行 qdbot-llm pull <模型名>"
}
