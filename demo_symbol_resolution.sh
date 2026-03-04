#!/bin/bash

# 简单演示符号解析功能

set -e

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

if [ "$EUID" -ne 0 ]; then 
    echo "请使用 sudo 运行"
    exit 1
fi

cd "$(dirname "$0")"

echo -e "${GREEN}=== LBR 符号解析演示 ===${NC}\n"

# 编译
echo -e "${BLUE}编译测试程序...${NC}"
cd test
make clean && make && make test_lbr_sframe
cd ..

# 编译监控程序
if [ ! -f "lbr-demo" ] || [ "cmd/main.go" -nt "lbr-demo" ]; then
    echo -e "${BLUE}编译监控程序...${NC}"
    make build
fi

echo -e "\n${GREEN}启动测试程序（后台）...${NC}"
./test/test_lbr_sframe &
TEST_PID=$!
echo "PID: $TEST_PID"

sleep 2

echo -e "\n${YELLOW}开始监控并解析符号（10秒）...${NC}"
echo -e "${BLUE}输出将显示函数名和源代码位置${NC}\n"

# timeout 10 ./lbr-demo -pid $TEST_PID -sframe=true -debug=true|| true
timeout 10 ./lbr-demo -pid $TEST_PID || true
echo -e "\n${GREEN}演示完成！${NC}"
echo -e "\n${YELLOW}提示：${NC}"
echo "• 使用 -dwarf=true 切换到 DWARF 解析"
echo "• 使用 -resolve=false 查看原始地址"
echo "• 运行 test/test_symbol_resolution.sh 进行完整测试"

wait $TEST_PID 2>/dev/null || true
