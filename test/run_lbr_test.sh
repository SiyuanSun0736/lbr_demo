#!/bin/bash

# LBR 测试自动化脚本
# 此脚本会启动LBR监控程序并运行测试代码

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}=== LBR 测试自动化脚本 ===${NC}\n"

# 检查是否有root权限
if [ "$EUID" -ne 0 ]; then 
    echo -e "${RED}错误: 请使用 sudo 运行此脚本${NC}"
    echo "用法: sudo ./run_lbr_test.sh"
    exit 1
fi

# 检查LBR监控程序是否存在
LBR_DEMO="../lbr-demo"
if [ ! -f "$LBR_DEMO" ]; then
    echo -e "${YELLOW}LBR监控程序不存在，正在编译...${NC}"
    cd ..
    make build
    cd test
    echo -e "${GREEN}编译完成${NC}\n"
fi

# 检查测试程序是否存在
if [ ! -f "./test_lbr" ]; then
    echo -e "${YELLOW}测试程序不存在，正在编译...${NC}"
    make
    echo -e "${GREEN}编译完成${NC}\n"
fi

# 清理旧的日志
LOG_DIR="../log"
mkdir -p "$LOG_DIR"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
LBR_LOG="$LOG_DIR/lbr_output_$TIMESTAMP.log"

echo -e "${GREEN}步骤1: 运行测试程序（后台）${NC}"
./test_lbr &
TEST_PID=$!
echo -e "测试程序已启动 (PID: $TEST_PID)\n"

# 等待测试程序输出PID
echo -e "${YELLOW}等待测试程序初始化 (2秒)...${NC}"
sleep 2

echo -e "${GREEN}步骤2: 启动 LBR 监控程序（监控 PID: $TEST_PID）${NC}"
$LBR_DEMO -pid $TEST_PID > "$LBR_LOG" 2>&1 &
LBR_PID=$!
echo -e "LBR 监控程序已启动 (PID: $LBR_PID)"
echo -e "日志文件: $LBR_LOG\n"

# 等待测试完成（测试程序会等待5秒让eBPF attach，然后执行测试并再等待5秒）
echo -e "${YELLOW}等待测试完成 (约12秒)...${NC}"
sleep 12

# 停止监控程序
echo -e "${GREEN}步骤3: 停止 LBR 监控程序${NC}"
kill -SIGINT $LBR_PID 2>/dev/null || true
wait $LBR_PID 2>/dev/null || true
echo -e "LBR 监控程序已停止\n"

# 确保测试程序已结束
if kill -0 $TEST_PID 2>/dev/null; then
    echo -e "${YELLOW}等待测试程序结束...${NC}"
    wait $TEST_PID 2>/dev/null || true
fi

TEST_EXIT_CODE=$?
if [ $TEST_EXIT_CODE -ne 0 ] && [ $TEST_EXIT_CODE -ne 143 ]; then
    echo -e "${RED}测试程序执行失败 (退出码: $TEST_EXIT_CODE)${NC}"
else
    echo -e "${GREEN}测试程序执行成功${NC}\n"
fi

# 显示结果
echo -e "${GREEN}=== 测试完成 ===${NC}"
echo -e "\n${YELLOW}LBR 监控日志:${NC}"
echo "----------------------------------------"
if [ -f "$LBR_LOG" ]; then
    tail -n 50 "$LBR_LOG"
    echo "----------------------------------------"
    echo -e "\n完整日志保存在: ${GREEN}$LBR_LOG${NC}"
else
    echo -e "${RED}警告: 日志文件不存在${NC}"
fi

echo -e "\n${GREEN}提示:${NC}"
echo "- 查看完整日志: cat $LBR_LOG"
echo "- 搜索LBR数据: grep -A 20 'Captured LBR' $LBR_LOG"
echo "- 分析分支模式: grep 'FROM\\|TO' $LBR_LOG"

exit 0
