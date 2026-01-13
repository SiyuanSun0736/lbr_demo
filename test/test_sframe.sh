#!/bin/bash

# SFrame 符号解析测试脚本
# 测试 SFrame 格式的符号解析功能

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}=== SFrame 符号解析测试 ===${NC}\n"

# 1. 检查 GCC 版本
echo -e "${YELLOW}步骤 1: 检查 GCC 版本${NC}"
GCC_VERSION=$(gcc --version | head -n1 | grep -oP '\d+\.\d+' | head -1)
echo "GCC 版本: $GCC_VERSION"

GCC_MAJOR=$(echo $GCC_VERSION | cut -d. -f1)
if [ "$GCC_MAJOR" -lt 13 ]; then
    echo -e "${RED}警告: GCC 版本过低 (需要 13+)，SFrame 支持可能不可用${NC}"
    echo -e "${YELLOW}将尝试编译，但可能不会生成 .sframe 节${NC}\n"
else
    echo -e "${GREEN}✓ GCC 版本支持 SFrame${NC}\n"
fi

# 2. 编译测试程序
echo -e "${YELLOW}步骤 2: 编译测试程序${NC}"

# 清理旧文件
make clean 2>/dev/null || true

# 编译带 SFrame 的版本
echo "编译带 SFrame 信息的版本..."
if make test_lbr_sframe; then
    echo -e "${GREEN}✓ 编译成功${NC}\n"
else
    echo -e "${RED}✗ 编译失败${NC}\n"
    exit 1
fi

# 3. 验证 SFrame 节
echo -e "${YELLOW}步骤 3: 验证 .sframe 节${NC}"
if readelf -S test_lbr_sframe | grep -q '\.sframe'; then
    echo -e "${GREEN}✓ 发现 .sframe 节${NC}"
    readelf -S test_lbr_sframe | grep sframe
    echo ""
else
    echo -e "${YELLOW}⚠ 未发现 .sframe 节${NC}"
    echo -e "${YELLOW}编译器可能不支持 SFrame，将使用符号表作为后备方案${NC}\n"
fi

# 4. 检查符号表
echo -e "${YELLOW}步骤 4: 检查符号表${NC}"
SYMBOL_COUNT=$(nm test_lbr_sframe | wc -l)
echo "符号数量: $SYMBOL_COUNT"
echo "主要函数符号:"
nm test_lbr_sframe | grep -E 'bubble_sort|fibonacci|classify_number|main' | head -5
echo ""

# 5. 运行测试
echo -e "${YELLOW}步骤 5: 运行 LBR 测试（需要 root 权限）${NC}"

# 启动测试程序
echo "启动测试程序..."
./test_lbr_sframe &
TEST_PID=$!
echo "测试程序 PID: $TEST_PID"

# 等待程序启动
sleep 1

# 检查进程是否还在运行
if ! kill -0 $TEST_PID 2>/dev/null; then
    echo -e "${RED}✗ 测试程序已退出${NC}"
    exit 1
fi

echo -e "${GREEN}✓ 测试程序运行中${NC}\n"

# 构建 lbr-demo 程序
echo -e "${YELLOW}步骤 6: 编译 LBR 监控程序${NC}"
cd ..
if make build 2>/dev/null || go build -o lbr-demo ./cmd; then
    echo -e "${GREEN}✓ LBR 程序编译成功${NC}\n"
else
    echo -e "${RED}✗ LBR 程序编译失败${NC}\n"
    kill $TEST_PID 2>/dev/null || true
    exit 1
fi

# 使用 SFrame 解析器运行
echo -e "${YELLOW}步骤 7: 使用 SFrame 解析器监控${NC}"
echo "运行命令: sudo ./lbr-demo -pid $TEST_PID -sframe=true -resolve=true"
echo -e "${BLUE}监控 5 秒钟...${NC}\n"

timeout 5 sudo ./lbr-demo -pid $TEST_PID -sframe=true -resolve=true > test/sframe_output.log 2>&1 || true

# 停止测试程序
kill -SIGINT $TEST_PID 2>/dev/null || true
wait $TEST_PID 2>/dev/null || true

# 8. 检查结果
echo -e "\n${YELLOW}步骤 8: 检查解析结果${NC}"
cd test

if [ -f sframe_output.log ]; then
    echo -e "${GREEN}✓ 日志文件已生成${NC}"
    
    # 检查是否成功启用 SFrame
    if grep -q "已启用 SFrame 符号解析" sframe_output.log; then
        echo -e "${GREEN}✓ SFrame 解析器已启用${NC}"
    else
        echo -e "${YELLOW}⚠ SFrame 解析器未启用（可能回退到其他解析器）${NC}"
    fi
    
    # 检查是否有符号解析结果
    if grep -q "bubble_sort\|fibonacci\|classify_number" sframe_output.log; then
        echo -e "${GREEN}✓ 成功解析到函数符号${NC}\n"
        echo -e "${BLUE}--- 解析结果示例 ---${NC}"
        grep -A 10 "LBR Stack:" sframe_output.log | head -15
    else
        echo -e "${YELLOW}⚠ 未找到函数符号（可能数据采集不足）${NC}"
    fi
    
    echo -e "\n${BLUE}完整日志: test/sframe_output.log${NC}"
else
    echo -e "${RED}✗ 日志文件未生成${NC}"
fi

# 9. 总结
echo -e "\n${GREEN}=== 测试完成 ===${NC}"
echo -e "${BLUE}测试文件:${NC}"
echo "  - test_lbr_sframe: 带 SFrame 信息的测试程序"
echo "  - sframe_output.log: SFrame 解析结果日志"
echo ""
echo -e "${BLUE}使用方法:${NC}"
echo "  sudo ./lbr-demo -pid <PID> -sframe=true -resolve=true"
echo ""
