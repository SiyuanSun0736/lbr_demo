#!/bin/bash

# 测试用户态符号解析的脚本

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

echo -e "${GREEN}=== LBR 用户态符号解析测试 ===${NC}\n"

# 检查权限
if [ "$EUID" -ne 0 ]; then 
    echo -e "${RED}错误: 请使用 sudo 运行此脚本${NC}"
    exit 1
fi

# 进入test目录
cd "$(dirname "$0")"

# 编译测试程序（带调试符号）
echo -e "${BLUE}步骤1: 编译测试程序（带调试符号）${NC}"
make clean
make
echo -e "${GREEN}✓ 编译完成${NC}\n"

# 验证调试符号
echo -e "${BLUE}步骤2: 验证调试符号${NC}"
if file test_lbr | grep -q "not stripped"; then
    echo -e "${GREEN}✓ 测试程序包含调试符号${NC}"
else
    echo -e "${YELLOW}⚠ 测试程序没有调试符号，将只能使用符号表${NC}"
fi

# 检查是否有addr2line
if command -v addr2line &> /dev/null; then
    echo -e "${GREEN}✓ addr2line 工具可用${NC}"
else
    echo -e "${RED}✗ addr2line 工具不可用${NC}"
fi
echo ""

# 编译LBR监控程序
echo -e "${BLUE}步骤3: 编译 LBR 监控程序${NC}"
cd ..
if [ ! -f "lbr-demo" ]; then
    make build
    echo -e "${GREEN}✓ LBR监控程序编译完成${NC}\n"
else
    # 检查是否需要重新编译
    if [ internal/usersym.go -nt lbr-demo ] || [ internal/dwarf_resolver.go -nt lbr-demo ] || [ internal/stack.go -nt lbr-demo ] || [ cmd/main.go -nt lbr-demo ]; then
        echo -e "${YELLOW}检测到源代码更新，重新编译...${NC}"
        make build
        echo -e "${GREEN}✓ LBR监控程序重新编译完成${NC}\n"
    else
        echo -e "${GREEN}✓ LBR监控程序已存在${NC}\n"
    fi
fi
cd test

# 准备日志目录
LOG_DIR="../log"
mkdir -p "$LOG_DIR"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)

echo -e "${GREEN}=== 测试场景 1: 使用 addr2line 解析（默认） ===${NC}"
LBR_LOG_1="$LOG_DIR/lbr_addr2line_$TIMESTAMP.log"

echo -e "${YELLOW}启动测试程序...${NC}"
./test_lbr &
TEST_PID=$!
echo -e "测试程序 PID: $TEST_PID"

sleep 2

echo -e "${YELLOW}启动 LBR 监控（使用 addr2line）...${NC}"
../lbr-demo -pid $TEST_PID -addr2line=true -resolve=true > "$LBR_LOG_1" 2>&1 &
LBR_PID=$!

echo -e "等待测试完成 (12秒)..."
sleep 12

kill -SIGINT $LBR_PID 2>/dev/null || true
wait $LBR_PID 2>/dev/null || true
wait $TEST_PID 2>/dev/null || true

echo -e "${GREEN}✓ 测试完成${NC}"
echo -e "日志文件: ${BLUE}$LBR_LOG_1${NC}\n"

echo -e "${YELLOW}--- addr2line 解析结果示例 ---${NC}"
grep -A 10 "LBR Stack:" "$LBR_LOG_1" | head -15
echo ""

echo -e "${GREEN}=== 测试场景 2: 使用 DWARF 解析 ===${NC}"
LBR_LOG_2="$LOG_DIR/lbr_dwarf_$TIMESTAMP.log"

echo -e "${YELLOW}启动测试程序...${NC}"
./test_lbr &
TEST_PID=$!
echo -e "测试程序 PID: $TEST_PID"

sleep 2

echo -e "${YELLOW}启动 LBR 监控（使用 DWARF）...${NC}"
../lbr-demo -pid $TEST_PID -dwarf=true -resolve=true > "$LBR_LOG_2" 2>&1 &
LBR_PID=$!

echo -e "等待测试完成 (12秒)..."
sleep 12

kill -SIGINT $LBR_PID 2>/dev/null || true
wait $LBR_PID 2>/dev/null || true
wait $TEST_PID 2>/dev/null || true

echo -e "${GREEN}✓ 测试完成${NC}"
echo -e "日志文件: ${BLUE}$LBR_LOG_2${NC}\n"

echo -e "${YELLOW}--- DWARF 解析结果示例 ---${NC}"
grep -A 10 "LBR Stack:" "$LBR_LOG_2" | head -15
echo ""

echo -e "${GREEN}=== 测试场景 3: 不解析符号（仅地址） ===${NC}"
LBR_LOG_3="$LOG_DIR/lbr_noResolve_$TIMESTAMP.log"

echo -e "${YELLOW}启动测试程序...${NC}"
./test_lbr &
TEST_PID=$!
echo -e "测试程序 PID: $TEST_PID"

sleep 2

echo -e "${YELLOW}启动 LBR 监控（不解析符号）...${NC}"
../lbr-demo -pid $TEST_PID -resolve=false > "$LBR_LOG_3" 2>&1 &
LBR_PID=$!

echo -e "等待测试完成 (12秒)..."
sleep 12

kill -SIGINT $LBR_PID 2>/dev/null || true
wait $LBR_PID 2>/dev/null || true
wait $TEST_PID 2>/dev/null || true

echo -e "${GREEN}✓ 测试完成${NC}"
echo -e "日志文件: ${BLUE}$LBR_LOG_3${NC}\n"

echo -e "${YELLOW}--- 未解析结果示例（原始地址） ---${NC}"
grep -A 10 "LBR Stack:" "$LBR_LOG_3" | head -15
echo ""

echo -e "${GREEN}=== 所有测试完成 ===${NC}\n"
echo -e "${BLUE}比较三种方式的结果：${NC}"
echo -e "1. addr2line: $LBR_LOG_1"
echo -e "2. DWARF:     $LBR_LOG_2"
echo -e "3. 仅地址:     $LBR_LOG_3"
echo ""

echo -e "${YELLOW}分析建议：${NC}"
echo -e "• addr2line 方式：简单易用，依赖系统工具"
echo -e "• DWARF 方式：  纯 Go 实现，更快但需要调试符号"
echo -e "• 仅地址方式：  原始数据，适合后处理"
echo ""

echo -e "${GREEN}提示：${NC}"
echo -e "• 查看详细日志: cat <日志文件>"
echo -e "• 统计函数调用: grep -oP '\\w+(?= \\()' <日志文件> | sort | uniq -c | sort -rn"
echo -e "• 查找特定函数: grep 'bubble_sort\\|fibonacci\\|classify_number' <日志文件>"
