// sframe_lbr_example 演示将 SFrame 符号解析与 LBR 分支地址结合使用。
//
// 用途：
//   - 接受一组十六进制地址（模拟 LBR 记录中的 From/To 字段）
//   - 使用 SFrame 解析器将地址还原为符号名称、文件位置和所属库
//   - 在 SFrame 不可用时自动回退到帧指针栈回溯，打印当前调用栈
//
// 用法：
//
//	sudo ./sframe_lbr <PID> [addr1 addr2 ...]
//
// 示例：
//
//	# 仅做栈回溯（不传地址）
//	sudo ./sframe_lbr 1234
//
//	# 解析指定地址（模拟来自 LBR 的 From/To 地址）
//	sudo ./sframe_lbr 1234 0x401234 0x401a80 0x7f3b4c00
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	lbr "github.com/SiyuanSun0736/lbr_demo/internal"
)

var (
	debugMode  = flag.Bool("debug", false, "启用调试日志")
	maxFrames  = flag.Int("frames", 32, "栈回溯最大帧数")
	noUnwind   = flag.Bool("no-unwind", false, "仅解析地址，不执行栈回溯")
	outputFile = flag.String("out", "", "额外输出到指定文件（默认仅控制台）")
)

func main() {
	flag.Parse()

	lbr.SetDebugMode(*debugMode)

	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "用法: %s [选项] <PID> [hex_addr ...]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "选项:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\n示例:\n")
		fmt.Fprintf(os.Stderr, "  # 对进程 1234 做栈回溯\n")
		fmt.Fprintf(os.Stderr, "  sudo %s 1234\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # 解析来自 LBR 的地址（模拟 LBR From/To 字段）\n")
		fmt.Fprintf(os.Stderr, "  sudo %s 1234 0x401234 0x401a80 0x7f3b4c001234\n", os.Args[0])
		os.Exit(1)
	}

	pid, err := strconv.Atoi(args[0])
	if err != nil {
		log.Fatalf("无效的 PID: %v", err)
	}

	// 设置输出：控制台 + 可选文件
	var out io.Writer = os.Stdout
	if *outputFile != "" {
		if err := os.MkdirAll(filepath.Dir(*outputFile), 0755); err != nil {
			log.Fatalf("创建目录失败: %v", err)
		}
		f, err := os.Create(*outputFile)
		if err != nil {
			log.Fatalf("创建输出文件失败: %v", err)
		}
		defer f.Close()
		out = io.MultiWriter(os.Stdout, f)
		log.SetOutput(out)
	}

	fmt.Fprintf(out, "============================================================\n")
	fmt.Fprintf(out, "  SFrame + LBR 地址解析示例\n")
	fmt.Fprintf(out, "  目标 PID : %d\n", pid)
	fmt.Fprintf(out, "  时间     : %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(out, "============================================================\n\n")

	// 创建 SFrame 解析器
	resolver, err := lbr.NewSFrameResolver(pid)
	if err != nil {
		log.Fatalf("创建 SFrameResolver 失败: %v\n提示: 确认目标程序以 -gsframe 编译，或 PID 存在", err)
	}
	defer resolver.Close()

	// ──────────────────────────────────────────────────────────
	// 第一部分：解析命令行传入的地址（模拟 LBR From/To 字段）
	// ──────────────────────────────────────────────────────────
	addrs := args[1:] // 剩余参数为十六进制地址
	if len(addrs) > 0 {
		fmt.Fprintf(out, "=== [1/2] LBR 地址解析 (共 %d 个地址) ===\n\n", len(addrs))
		fmt.Fprintf(out, "  %-20s  %-40s  %s\n", "地址", "函数名", "位置")
		fmt.Fprintf(out, "  %s\n", strings.Repeat("-", 90))

		for _, hexStr := range addrs {
			// 兼容 "0x" 前缀和纯十六进制
			hexStr = strings.TrimPrefix(hexStr, "0x")
			hexStr = strings.TrimPrefix(hexStr, "0X")
			addr, err := strconv.ParseUint(hexStr, 16, 64)
			if err != nil {
				fmt.Fprintf(out, "  %-20s  [解析地址失败: %v]\n", "0x"+hexStr, err)
				continue
			}

			info, err := resolver.ResolveAddress(addr)
			if err != nil || info == nil || info.Function == "" {
				// 未找到符号，打印原始地址
				fmt.Fprintf(out, "  0x%-18x  %-40s  %s\n",
					addr, "[unknown]", "")
				continue
			}

			loc := ""
			if info.File != "" && info.Line > 0 {
				loc = fmt.Sprintf("%s:%d", filepath.Base(info.File), info.Line)
			}
			funcField := info.Function
			if info.Library != "" {
				funcField = fmt.Sprintf("%s  (@%s)", info.Function, filepath.Base(info.Library))
			}

			fmt.Fprintf(out, "  0x%-18x  %-40s  %s\n", addr, funcField, loc)
		}
		fmt.Fprintln(out)
	}

	// ──────────────────────────────────────────────────────────
	// 第二部分：对目标进程执行实时栈回溯
	// 演示 SFrame 在 LBR 触发时的调用栈还原能力
	// ──────────────────────────────────────────────────────────
	if !*noUnwind {
		fmt.Fprintf(out, "=== [2/2] 实时栈回溯 (最大 %d 帧) ===\n\n", *maxFrames)

		frames, err := resolver.UnwindStack(*maxFrames)
		if err != nil {
			log.Printf("警告: 栈回溯不完整: %v", err)
		}

		if len(frames) == 0 {
			fmt.Fprintf(out, "  未能获取任何栈帧（进程可能已退出或无权限）\n\n")
		} else {
			// 简洁输出（类似 perf 的调用链格式）
			for i, frame := range frames {
				sym := "[unknown]"
				loc := ""
				libTag := ""

				if frame.Info != nil {
					if frame.Info.Function != "" {
						sym = frame.Info.Function
					}
					if frame.Info.File != "" && frame.Info.Line > 0 {
						loc = fmt.Sprintf(" (%s:%d)", filepath.Base(frame.Info.File), frame.Info.Line)
					}
					if frame.Info.Library != "" {
						libTag = fmt.Sprintf(" [%s]", filepath.Base(frame.Info.Library))
					}
				}

				fmt.Fprintf(out, "  #%-3d  0x%016x  %s%s%s\n",
					i, frame.PC, sym, loc, libTag)
			}

			fmt.Fprintln(out)

			// 按来源（库/主程序）汇总
			fmt.Fprintf(out, "--- 帧来源统计 ---\n")
			libCount := make(map[string]int)
			for _, frame := range frames {
				key := "<main>"
				if frame.Info != nil && frame.Info.Library != "" {
					key = filepath.Base(frame.Info.Library)
				}
				libCount[key]++
			}
			for lib, cnt := range libCount {
				fmt.Fprintf(out, "  %-35s  %d 帧\n", lib, cnt)
			}
			fmt.Fprintln(out)
		}
	}

	fmt.Fprintf(out, "============================================================\n")
	fmt.Fprintf(out, "  完成\n")
	fmt.Fprintf(out, "============================================================\n")
}
