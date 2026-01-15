#!/bin/bash

# 栈回溯功能测试脚本

set -e

echo "======================================"
echo "栈回溯功能测试"
echo "======================================"
echo

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 检查是否已编译测试程序
if [ ! -f test_stack_unwinding ]; then
    echo -e "${YELLOW}测试程序未编译，正在编译...${NC}"
    make test_stack_unwinding
fi

# 检查是否已编译栈回溯工具
if [ ! -f ../lbr-demo ]; then
    echo -e "${YELLOW}lbr-demo 未编译，需要先编译主程序${NC}"
    echo "请运行: cd .. && make"
    exit 1
fi

echo -e "${GREEN}步骤 1: 启动测试程序${NC}"
./test_stack_unwinding &
TEST_PID=$!
echo "测试进程 PID: $TEST_PID"

# 等待程序启动
sleep 2

# 检查进程是否还在运行
if ! kill -0 $TEST_PID 2>/dev/null; then
    echo -e "${RED}错误: 测试进程已退出${NC}"
    exit 1
fi

echo
echo -e "${GREEN}步骤 2: 查看进程信息${NC}"
echo "进程状态:"
ps -p $TEST_PID -o pid,comm,state,cmd

echo
echo -e "${GREEN}步骤 3: 查看内存映射${NC}"
echo "可执行段:"
grep -E "test_stack_unwinding" /proc/$TEST_PID/maps | head -n 3

echo
echo -e "${GREEN}步骤 4: 尝试读取寄存器信息${NC}"
if [ -f /proc/$TEST_PID/syscall ]; then
    echo "Syscall 信息:"
    cat /proc/$TEST_PID/syscall
else
    echo -e "${YELLOW}警告: /proc/$TEST_PID/syscall 不可用${NC}"
fi

echo
echo -e "${GREEN}步骤 5: 使用 GDB 获取栈跟踪（参考）${NC}"
if command -v gdb &> /dev/null; then
    echo "GDB 栈跟踪:"
    gdb -batch -ex "attach $TEST_PID" -ex "bt" -ex "detach" -ex "quit" 2>/dev/null | grep -A 20 "^#" || echo "GDB 栈跟踪获取失败"
else
    echo -e "${YELLOW}GDB 未安装，跳过此步骤${NC}"
fi

echo
echo -e "${GREEN}步骤 6: 构建并运行栈回溯工具${NC}"

# 编译栈回溯演示程序
if [ ! -f ../examples/stack_unwinding/stack_unwinding ]; then
    echo "正在编译栈回溯演示程序..."
    cd ../examples/stack_unwinding
    go build -o stack_unwinding main.go
    cd ../../test
else
    echo "栈回溯演示程序已存在"
fi

echo
echo "运行栈回溯（调试模式）..."
sudo ../examples/stack_unwinding/stack_unwinding -debug -method sframe $TEST_PID

# 清理
echo
echo -e "${GREEN}步骤 7: 清理${NC}"
kill $TEST_PID 2>/dev/null || true
wait $TEST_PID 2>/dev/null || true

echo
echo -e "${GREEN}测试完成！${NC}"
echo
echo "提示："
echo "  - 如果栈回溯失败，请确保程序使用 -fno-omit-frame-pointer 编译"
echo "  - 需要 root 权限才能读取 /proc/PID/mem"
echo "  - 可以手动运行: sudo ../cmd/stack_unwinding_demo <PID>"
