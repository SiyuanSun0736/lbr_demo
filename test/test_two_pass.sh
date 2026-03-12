#!/bin/bash

# two-pass 模式验证脚本
# 流程：
#   1. 编译测试程序（带 SFrame）
#   2. 编译 lbr-demo（如需）
#   3. 启动测试程序（后台循环运行）
#   4. 以 two-pass 模式后台启动 lbr-demo（Phase 1 采集热点地址）
#      脚本等待 PHASE1_DURATION 秒后向 lbr-demo 发送 SIGINT 触发切换到 Phase 2，
#      Phase 2 自动挂载 uprobe、捕获寄存器快照并做栈展开，完成后自动退出
#   5. 验证输出文件（phase1_addr_stats_*.csv 和 phase2_unwind_*.csv）是否正常生成

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
LBR_DEMO="$REPO_DIR/lbr-demo"
LOG_DIR="$REPO_DIR/log"
TEST_BIN="$SCRIPT_DIR/test_stack_unwinding1"

# Phase 1 持续时间（秒），可通过环境变量覆盖
PHASE1_DURATION="${PHASE1_DURATION:-10}"
TOP_N="${TOP_N:-3}"

# ── 权限检查 ──────────────────────────────────────────────────────────────────
if [ "$EUID" -ne 0 ]; then
    echo -e "${RED}错误: 请使用 sudo 运行此脚本${NC}"
    echo "用法: sudo ./test_two_pass.sh"
    exit 1
fi

echo -e "${BLUE}=== two-pass 模式验证脚本 ===${NC}"
echo -e "Phase 1 持续时间: ${PHASE1_DURATION}s  |  Top-N 热点地址: ${TOP_N}\n"

# ── 步骤 1: 编译测试程序 ──────────────────────────────────────────────────────
echo -e "${YELLOW}[1/4] 编译测试程序...${NC}"
cd "$SCRIPT_DIR"
make test_stack_unwinding1
if [ ! -f "$TEST_BIN" ]; then
    echo -e "${RED}✗ 测试程序编译失败${NC}"
    exit 1
fi
echo -e "${GREEN}✓ 测试程序已就绪: $TEST_BIN${NC}\n"

# ── 步骤 2: 编译 lbr-demo ─────────────────────────────────────────────────────
echo -e "${YELLOW}[2/4] 检查 lbr-demo...${NC}"
cd "$REPO_DIR"
echo "重新编译 lbr-demo..."
 make
echo -e "${GREEN}✓ lbr-demo 已就绪${NC}\n"

# ── 步骤 3: 启动测试程序（后台持续运行）────────────────────────────────────────
echo -e "${YELLOW}[3/4] 启动测试程序（后台）...${NC}"
"$TEST_BIN" &
TEST_PID=$!
echo -e "${GREEN}✓ 测试程序已启动，PID=$TEST_PID${NC}"

# 等待测试程序初始化
sleep 2
if ! kill -0 "$TEST_PID" 2>/dev/null; then
    echo -e "${RED}✗ 测试程序启动后立即退出，请检查${NC}"
    exit 1
fi

# ── 步骤 4: 以 two-pass 模式运行 lbr-demo ─────────────────────────────────────
echo -e "\n${YELLOW}[4/4] 启动 lbr-demo（two-pass 模式）...${NC}"
echo -e "${BLUE}  Phase 1: 采集 ${PHASE1_DURATION}s 热点地址（脚本发送 SIGINT 触发切换）${NC}"
echo -e "${BLUE}  Phase 2: uprobe 捕获寄存器 + SFrame 栈展开，完成后自动退出${NC}\n"

mkdir -p "$LOG_DIR"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
MONITOR_LOG="$LOG_DIR/two_pass_test_${TIMESTAMP}.log"

# 后台启动 lbr-demo，日志写入文件并同步输出到终端
set +e
"$LBR_DEMO" \
    -pid "$TEST_PID" \
    -two-pass \
    -top-n "$TOP_N" \
    -sframe=true \
    -logdir "$LOG_DIR" \
    -debug=true \
    > "$MONITOR_LOG" 2>&1 &
LBR_PID=$!
echo -e "${GREEN}✓ lbr-demo 已启动，PID=$LBR_PID${NC}"

# 实时跟踪日志输出
tail -f "$MONITOR_LOG" &
TAIL_PID=$!

# 后台诊断收集器：当日志出现尝试挂载并包含 pid=N 时，自动采集该 PID 的诊断信息
diag_collector() {
    local log_file="$1"
    # 使用 tail -F 持续监听，新出现的 pid 行触发采集
    tail -n0 -F "$log_file" | while read -r line; do
        if [[ $line =~ pid=([0-9]+) ]]; then
            pid=${BASH_REMATCH[1]}
            ts=$(date +%Y%m%d_%H%M%S)
            out="$LOG_DIR/diag_${pid}_${ts}.txt"
            echo "==== diag for PID=$pid at $ts ====" > "$out"
            echo "LOG_LINE: $line" >> "$out"
            echo >> "$out"
            echo "ps -p $pid -o pid,comm,etime,cmd:" >> "$out"
            ps -p "$pid" -o pid,comm,etime,cmd >> "$out" 2>&1 || true
            echo >> "$out"
            echo "head /proc/$pid/maps (first 200 lines):" >> "$out"
            head -n 200 "/proc/$pid/maps" >> "$out" 2>&1 || echo "(failed to read /proc/$pid/maps)" >> "$out"
            echo >> "$out"
            echo "ls -l /proc/$pid/ns:" >> "$out"
            ls -l "/proc/$pid/ns" >> "$out" 2>&1 || true
            echo >> "$out"
            echo "cat /proc/$pid/status:" >> "$out"
            cat "/proc/$pid/status" >> "$out" 2>&1 || true
            echo "==== end ====" >> "$out"
        fi
    done
}

diag_collector "$MONITOR_LOG" &
DIAG_PID=$!

# 清理函数：终止后台进程并保底收尾
cleanup() {
    rv=$?
    # 只尝试杀存在的 PID
    if [ -n "${TAIL_PID:-}" ]; then
        kill "$TAIL_PID" 2>/dev/null || true
    fi
    if [ -n "${DIAG_PID:-}" ]; then
        kill "$DIAG_PID" 2>/dev/null || true
    fi
    if [ -n "${LBR_PID:-}" ]; then
        kill "$LBR_PID" 2>/dev/null || true
    fi
    if [ -n "${TEST_PID:-}" ]; then
        kill "$TEST_PID" 2>/dev/null || true
    fi
    wait 2>/dev/null || true
    exit $rv
}

# 在退出或被中断时执行清理
trap cleanup EXIT INT TERM

# Phase 1：等待指定时间后发送 SIGINT，触发切换到 Phase 2
echo -e "${BLUE}  Phase 1 采集中，${PHASE1_DURATION}s 后自动发送 SIGINT...${NC}"
sleep "$PHASE1_DURATION"

if ! kill -0 "$LBR_PID" 2>/dev/null; then
    echo -e "\n${RED}✗ lbr-demo 在 Phase 1 期间意外退出${NC}"
    kill "$TAIL_PID" 2>/dev/null || true
    kill "$TEST_PID" 2>/dev/null || true
    exit 1
fi

echo -e "\n${YELLOW}→ 发送 SIGINT 给 lbr-demo (PID=$LBR_PID)，触发 Phase 2...${NC}"
kill -INT "$LBR_PID"
# 查看pid是否收到信号后仍在运行
ps -p "$LBR_PID" -o pid,comm,etime,cmd
ps -p "$TEST_PID" -o pid,comm,etime,cmd     
# 等待 Phase 2 完成（最多 60 秒）
PHASE2_TIMEOUT=60
echo -e "${BLUE}  等待 Phase 2 完成（最多 ${PHASE2_TIMEOUT}s）...${NC}"
ELAPSED=0
while kill -0 "$LBR_PID" 2>/dev/null; do
    sleep 1
    ELAPSED=$((ELAPSED + 1))
    if [ "$ELAPSED" -ge "$PHASE2_TIMEOUT" ]; then
        echo -e "\n${RED}✗ Phase 2 在 ${PHASE2_TIMEOUT}s 内未完成（超时），强制终止${NC}"
        kill "$LBR_PID" 2>/dev/null || true
        kill "$TAIL_PID" 2>/dev/null || true
        kill "$TEST_PID" 2>/dev/null || true
        exit 1
    fi
done
wait "$LBR_PID"
LBR_EXIT=$?
set -e

kill "$TAIL_PID" 2>/dev/null || true
echo -e "\n${GREEN}✓ lbr-demo 已退出（退出码: $LBR_EXIT）${NC}"

# 停止测试程序
kill "$TEST_PID" 2>/dev/null || true
wait "$TEST_PID" 2>/dev/null || true

# ── 步骤 5: 验证输出文件 ───────────────────────────────────────────────────────
echo -e "\n${YELLOW}[验证] 检查输出文件...${NC}"
PASS=0
FAIL=0

check_csv() {
    local pattern="$1"
    local desc="$2"
    local min_rows="${3:-1}"

    local file
    file=$(ls -t "$LOG_DIR"/${pattern} 2>/dev/null | head -1)
    if [ -z "$file" ]; then
        echo -e "  ${RED}✗ 未找到 ${desc} 文件（pattern: ${pattern}）${NC}"
        FAIL=$((FAIL+1))
        return
    fi

    local rows
    rows=$(tail -n +2 "$file" | grep -c '[^[:space:]]' || true)
    if [ "$rows" -ge "$min_rows" ]; then
        echo -e "  ${GREEN}✓ ${desc}: $file  ($rows 行数据)${NC}"
        PASS=$((PASS+1))
    else
        echo -e "  ${RED}✗ ${desc}: $file 存在但数据不足（${rows} 行，期望 >=${min_rows}）${NC}"
        FAIL=$((FAIL+1))
    fi
}

check_csv "phase1_addr_stats_*.csv" "Phase 1 地址统计" 1


# 检查日志中是否出现关键标志
for keyword in \
    "Phase 1 结束" \
    "Phase 2 开始" \
    "uprobe 已挂载" \
    "Phase 2 uprobe" \
    "栈展开结果已写入"
do
    if grep -q "$keyword" "$MONITOR_LOG" 2>/dev/null; then
        echo -e "  ${GREEN}✓ 日志包含关键词: \"${keyword}\"${NC}"
        PASS=$((PASS+1))
    else
        echo -e "  ${RED}✗ 日志缺少关键词: \"${keyword}\"${NC}"
        FAIL=$((FAIL+1))
    fi
done

# ── 汇总 ───────────────────────────────────────────────────────────────────────
echo ""
echo -e "========================================="
echo -e "验证结果: ${GREEN}通过 ${PASS}${NC} / ${RED}失败 ${FAIL}${NC}"
echo -e "监控日志: $MONITOR_LOG"
echo -e "========================================="

if [ "$FAIL" -eq 0 ]; then
    echo -e "${GREEN}所有验证项均通过，two-pass 模式工作正常！${NC}"
    exit 0
else
    echo -e "${RED}存在 ${FAIL} 项验证失败，请查看日志排查问题${NC}"
    exit 1
fi
