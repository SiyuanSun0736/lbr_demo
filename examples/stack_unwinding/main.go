package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	lbr "github.com/SiyuanSun0736/lbr_demo/internal"
)

var (
	debugMode    = flag.Bool("debug", false, "启用调试日志")
	unwindMethod = flag.String("method", "auto", "栈展开方法: auto(自动,优先SFrame), sframe(仅SFrame), fp(仅帧指针)")
)

func main() {
	flag.Parse()

	// 设置调试模式
	lbr.SetDebugMode(*debugMode)

	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "用法: %s [-debug] [-method auto|sframe|fp] <PID>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  -debug         启用调试日志\n")
		fmt.Fprintf(os.Stderr, "  -method        栈展开方法:\n")
		fmt.Fprintf(os.Stderr, "                 auto   - 自动模式(优先SFrame,失败时回退到FP) [默认]\n")
		fmt.Fprintf(os.Stderr, "                 sframe - 仅使用SFrame展开\n")
		fmt.Fprintf(os.Stderr, "                 fp     - 仅使用帧指针展开\n")
		fmt.Fprintf(os.Stderr, "  对指定进程进行栈回溯\n")
		os.Exit(1)
	}

	pid, err := strconv.Atoi(args[0])
	if err != nil {
		log.Fatalf("无效的PID: %v", err)
	}

	// 创建日志目录
	logDir := "../log"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Fatalf("创建日志目录失败: %v", err)
	}

	// 创建日志文件
	timestamp := time.Now().Format("20060102_150405")
	logFile := filepath.Join(logDir, fmt.Sprintf("stack_unwinding_%d_%s.log", pid, timestamp))
	f, err := os.Create(logFile)
	if err != nil {
		log.Fatalf("创建日志文件失败: %v", err)
	}
	defer f.Close()

	// 同时输出到控制台和日志文件
	multiWriter := io.MultiWriter(os.Stdout, f)
	log.SetOutput(multiWriter)

	fmt.Fprintf(multiWriter, "正在对进程 %d 进行栈回溯...\n", pid)
	fmt.Fprintf(multiWriter, "日志文件: %s\n\n", logFile)

	// 创建 SFrame 解析器
	resolver, err := lbr.NewSFrameResolver(pid)
	if err != nil {
		log.Fatalf("创建解析器失败: %v", err)
	}
	defer resolver.Close()

	// 执行栈回溯
	maxFrames := 32
	fmt.Fprintf(f, "最大栈帧数: %d\n", maxFrames)
	fmt.Fprintf(f, "栈展开方法: %s\n\n", *unwindMethod)

	var frames []lbr.StackFrame
	switch *unwindMethod {
	case "sframe":
		fmt.Fprintf(multiWriter, "使用 SFrame 栈展开...\n")
		frames, err = resolver.UnwindStackWithSFrame(maxFrames)
	case "fp":
		fmt.Fprintf(multiWriter, "使用帧指针 (FP) 栈展开...\n")
		frames, err = resolver.UnwindStackWithFP(maxFrames)
	case "auto":
		fmt.Fprintf(multiWriter, "使用自动模式栈展开 (优先SFrame)...\n")
		frames, err = resolver.UnwindStack(maxFrames)
	default:
		log.Fatalf("无效的栈展开方法: %s (应为 auto, sframe 或 fp)", *unwindMethod)
	}

	if err != nil {
		log.Printf("警告: 栈回溯可能不完整: %v\n", err)
	}

	if len(frames) == 0 {
		log.Fatal("未能获取任何栈帧")
	}

	// 打印栈跟踪
	resolver.PrintStackTrace(frames)

	// 详细信息
	fmt.Fprintf(f, "\n=== 详细栈帧信息 ===\n")
	for i, frame := range frames {
		fmt.Fprintf(f, "\n帧 #%d:\n", i)
		fmt.Fprintf(f, "  程序计数器 (PC): 0x%016x\n", frame.PC)
		fmt.Fprintf(f, "  栈指针     (SP): 0x%016x\n", frame.SP)
		fmt.Fprintf(f, "  基址指针   (BP): 0x%016x\n", frame.BP)

		if frame.Info != nil {
			if frame.Info.Function != "" {
				fmt.Fprintf(f, "  函数: %s\n", frame.Info.Function)
			}
			if frame.Info.File != "" && frame.Info.Line > 0 {
				fmt.Fprintf(f, "  位置: %s:%d\n", frame.Info.File, frame.Info.Line)
			}
			if frame.Info.Library != "" {
				fmt.Fprintf(f, "  库: %s\n", frame.Info.Library)
			}
		} else {
			fmt.Fprintf(f, "  (未找到符号信息)\n")
		}
	}

	// 统计信息
	fmt.Fprintf(f, "\n=== 统计信息 ===\n")
	fmt.Fprintf(f, "总栈帧数: %d\n", len(frames))

	// 按库分类
	libCount := make(map[string]int)
	for _, frame := range frames {
		if frame.Info != nil {
			lib := frame.Info.Library
			if lib == "" {
				lib = "<main>"
			}
			libCount[lib]++
		}
	}

	fmt.Fprintf(f, "\n各库栈帧数:\n")
	for lib, count := range libCount {
		fmt.Fprintf(f, "  %-30s: %d\n", lib, count)
	}

	fmt.Printf("\n栈回溯完成，结果已保存到: %s\n", logFile)
}
