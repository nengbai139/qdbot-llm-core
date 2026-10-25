package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/nengbai139/qdbot-llm-core/pkg/daemon"
)

// cmdDaemon 服务管理（复用 qdbot_tunnel 的三平台 daemon，服务名 com.qdbot.llm / qdbot-llm）。
//
//   - daemon install                        → 装 serve（仅本地网关常驻）
//   - daemon install --model <m> ...        → 装 expose（网关 + 隧道，公网常驻）
//
// expose 的参数烘焙进服务命令（plist/systemd/sc 的 argv），改模型要重装。
func cmdDaemon(args []string) {
	if len(args) == 0 {
		fmt.Println("用法: qdbot-llm daemon install [--model <m> --price-in <x> --price-out <y> --payout-user <u>] | start|stop|status|uninstall")
		return
	}
	d := daemon.New("llm")
	switch args[0] {
	case "install":
		exe, err := os.Executable()
		if err != nil {
			log.Fatal(err)
		}
		fs := flag.NewFlagSet("daemon install", flag.ExitOnError)
		model := fs.String("model", "", "模型名（带则装 expose，公网暴露）")
		priceIn := fs.Float64("price-in", 0, "¥/百万 input token")
		priceOut := fs.Float64("price-out", 0, "¥/百万 output token")
		payoutUser := fs.String("payout-user", "", "提供方 qdbot_account user_id（分成收款）")
		_ = fs.Parse(args[1:])

		runArgs := []string{"serve"}
		if *model != "" {
			runArgs = []string{"expose",
				"--model", *model,
				"--price-in", fmt.Sprintf("%g", *priceIn),
				"--price-out", fmt.Sprintf("%g", *priceOut),
				"--payout-user", *payoutUser}
		}
		if err := d.Install(exe, runArgs); err != nil {
			log.Fatal(err)
		}
		// install = 注册 + 启动（RunAtLoad 开机自启，KeepAlive 断线重连），一条命令搞定
		if err := d.Start(); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("已注册并启动服务（开机自启 + 断线重连），运行 qdbot-llm %s\n", runArgs[0])
	case "uninstall":
		if err := d.Uninstall(); err != nil {
			log.Fatal(err)
		}
		fmt.Println("已注销服务")
	case "start":
		if err := d.Start(); err != nil {
			log.Fatal(err)
		}
		fmt.Println("已启动")
	case "stop":
		if err := d.Stop(); err != nil {
			log.Fatal(err)
		}
		fmt.Println("已停止")
	case "status":
		s, err := d.Status()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("daemon:", s)
	default:
		fmt.Fprintf(os.Stderr, "未知 daemon 子命令 %q\n", args[0])
	}
}
