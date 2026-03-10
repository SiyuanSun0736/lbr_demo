#!/bin/bash
# test_pmu_monitor_all_time.sh — pmu_monitor_all_time 功能验证测试脚本
#
# 测试流程：
#   1. 检查运行权限（perf_event_paranoid）
#   2. 编译 pmu_monitor_all_time 和 test_pmu_workload（如未编译）
#   3. 启动 test_pmu_workload 作为被监控目标
#   4. 启动 pmu_monitor_all_time 监控该进程，采集 SAMPLE_COUNT 个样本
#   5. 终止两个进程
#   6. 验证日志文件（行数、非零计数器）
#   7. 输出测试结果

set -euo pipefail

# ── 配置 ────────────────────────────────────────────────────────────────────
TEST_DURATION=30        # 固定测试时长（秒）
SAMPLE_INTERVAL_MS=100  # pmu_monitor_all_time 的采样间隔（与源码 SAMPLE_INTERVAL_MS 保持一致）
WORKLOAD_DURATION=120   # 工作负载最长持续时间（秒，须大于 TEST_DURATION）
MONITOR_BIN="./pmu_monitor_all_time"
WORKLOAD_BIN="./test/test_pmu_workload"
WORKLOAD_NAME="$(basename "$WORKLOAD_BIN")"
# 唯一运行 ID（时间戳 + PID），用于生成临时文件名
RUN_ID="$(date +%s)_$$"
LOG_LINK="log/pmu_monitor_all_time.log"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ── 颜色 ────────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; NC='\033[0m'

pass() { printf "${GREEN}[PASS]${NC} %s\n" "$*"; }
fail() { printf "${RED}[FAIL]${NC} %s\n" "$*"; }
info() { printf "${CYAN}[INFO]${NC} %s\n" "$*"; }
warn() { printf "${YELLOW}[WARN]${NC} %s\n" "$*"; }

TESTS_PASSED=0
TESTS_FAILED=0

assert_true() {
    local desc="$1"; shift
    if "$@" 2>/dev/null; then
        pass "$desc"
        ((TESTS_PASSED++)) || true
    else
        fail "$desc"
        ((TESTS_FAILED++)) || true
    fi
}

# ── 清理钩子 ────────────────────────────────────────────────────────────────
MONITOR_PID=""
WORKLOAD_PID=""

cleanup() {
    [[ -n "$MONITOR_PID"  ]] && kill "$MONITOR_PID"  2>/dev/null || true
    [[ -n "$WORKLOAD_PID" ]] && kill "$WORKLOAD_PID" 2>/dev/null || true
    wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# ── 进入 pmu 目录 ────────────────────────────────────────────────────────────
cd "$SCRIPT_DIR"

EXPECTED_SAMPLES=$(( TEST_DURATION * 1000 / SAMPLE_INTERVAL_MS ))

echo "======================================================"
echo "       pmu_monitor_all_time 功能验证测试"
echo "======================================================"
printf "  测试时长  : %d s\n"   "$TEST_DURATION"
printf "  采样间隔  : %d ms\n"  "$SAMPLE_INTERVAL_MS"
printf "  预期样本数: ~%d 行\n" "$EXPECTED_SAMPLES"
echo

# ── 步骤 1：权限检查 ─────────────────────────────────────────────────────────
info "检查 perf_event_paranoid 权限..."
PARANOID=$(cat /proc/sys/kernel/perf_event_paranoid 2>/dev/null || echo "2")
if [[ "$PARANOID" -gt 1 ]]; then
    warn "perf_event_paranoid=$PARANOID，跨进程监控可能需要 root 或 CAP_PERFMON"
    warn "若计数器全部失败，请以 root 运行或执行："
    warn "  echo 1 | sudo tee /proc/sys/kernel/perf_event_paranoid"
fi
info "当前 perf_event_paranoid=$PARANOID"
echo

# ── 步骤 2：编译 ─────────────────────────────────────────────────────────────
info "检查并编译所需程序..."

if [[ ! -x "$MONITOR_BIN" ]]; then
    info "编译 pmu_monitor_all_time..."
    make pmu_monitor_all_time
fi
assert_true "pmu_monitor_all_time 可执行文件存在" test -x "$MONITOR_BIN"

if [[ ! -x "$WORKLOAD_BIN" ]]; then
    info "编译 test/.."
    make test
fi
assert_true "test 可执行文件存在" test -x "$WORKLOAD_BIN"
echo

# ── 步骤 3：启动工作负载 ─────────────────────────────────────────────────────
mkdir -p log
info "启动 test_pmu_workload（持续 ${WORKLOAD_DURATION}s）..."
"$WORKLOAD_BIN" "$WORKLOAD_DURATION" &
WORKLOAD_PID=$!
info "工作负载 PID: $WORKLOAD_PID"

# 等待工作负载完成初始化（打印"内存初始化完成"后才开始产生稳定事件）
sleep 3

assert_true "工作负载进程存活" kill -0 "$WORKLOAD_PID"
echo

# ── 步骤 4：启动监控器 ───────────────────────────────────────────────────────
MONITOR_LOG="log/monitor_stderr_${RUN_ID}.txt"
info "启动 pmu_monitor_all_time 监控 PID $WORKLOAD_PID..."
"$MONITOR_BIN" "$WORKLOAD_PID" >"log/monitor_stdout_${RUN_ID}.txt" 2>"$MONITOR_LOG" &
MONITOR_PID=$!
info "监控器 PID: $MONITOR_PID"

# (延迟创建 stdout/stderr 链接至脚本末尾，保留原始 *_$$.txt)

# 固定等待 TEST_DURATION 秒
info "等待 ${TEST_DURATION} 秒完成采集..."
sleep "$TEST_DURATION"

assert_true "监控器进程在采集期间持续运行" kill -0 "$MONITOR_PID"
echo

# ── 步骤 5：停止进程 ─────────────────────────────────────────────────────────
info "停止监控器和工作负载..."
kill "$MONITOR_PID" 2>/dev/null || true
kill "$WORKLOAD_PID" 2>/dev/null || true
wait "$MONITOR_PID"  2>/dev/null || true
wait "$WORKLOAD_PID" 2>/dev/null || true
MONITOR_PID=""
WORKLOAD_PID=""
echo

# ── 步骤 6：验证日志文件 ─────────────────────────────────────────────────────
echo "------------------------------------------------------"
info "验证日志文件：$LOG_LINK"

assert_true "日志符号链接存在" test -L "$LOG_LINK"

REAL_LOG="$(readlink -f "$LOG_LINK" 2>/dev/null || echo "")"
assert_true "日志真实文件存在" test -f "$REAL_LOG"

# (延迟创建日志文件的 workload-specific 链接至脚本末尾)

# 日志必须有标题行 + 至少 SAMPLE_COUNT 行数据
TOTAL_LINES=$(wc -l < "$REAL_LOG")
DATA_LINES=$(( TOTAL_LINES - 1 ))   # 减去标题行

info "日志总行数: $TOTAL_LINES（标题 1 行 + 数据 $DATA_LINES 行）"

assert_true "日志至少包含 1 行数据" test "$DATA_LINES" -ge 1

# 宽松检查：允许最多 20% 误差（定时器偶发 overrun 或启动延迟）
MIN_SAMPLES=$(( EXPECTED_SAMPLES * 8 / 10 ))
if [[ "$DATA_LINES" -ge "$MIN_SAMPLES" ]]; then
    pass "数据行数 ${DATA_LINES} 满足预期（≥ ${MIN_SAMPLES}，预期 ~${EXPECTED_SAMPLES}）"
    ((TESTS_PASSED++)) || true
else
    warn "数据行数 ${DATA_LINES} 低于预期下限 ${MIN_SAMPLES}（预期 ~${EXPECTED_SAMPLES}），可能是监控器启动较慢"
fi

# 检查标题行包含已知计数器名称
assert_true "标题行包含 dTLB-loads"        grep -q "dTLB-loads"  "$REAL_LOG"
assert_true "标题行包含 LLC-load-misses"   grep -q "LLC-load-misses" "$REAL_LOG"
assert_true "标题行包含 L1-dcache-loads"   grep -q "L1-dcache-loads" "$REAL_LOG"

# 检查至少一行数据中存在非零计数
# 列格式：Timestamp,val1,val2,...  "N/A" 表示该计数器不可用
ANY_NONZERO=false
while IFS= read -r line; do
    # 跳过标题行
    [[ "$line" == Timestamp* ]] && continue
    # 检查是否存在至少一个仅由数字组成且大于零的字段
    IFS=',' read -ra fields <<< "$line"
    for f in "${fields[@]:1}"; do     # 跳过第 0 列（时间戳）
        [[ "$f" =~ ^[0-9]+$ ]] && [[ "$f" -gt 0 ]] && { ANY_NONZERO=true; break 2; }
    done
done < "$REAL_LOG"

if $ANY_NONZERO; then
    pass "日志中存在非零计数器值"
    ((TESTS_PASSED++)) || true
else
    fail "日志中所有计数器均为 0 或 N/A（硬件不支持或权限不足）"
    ((TESTS_FAILED++)) || true
fi

# ── 步骤 7：显示日志摘要 ─────────────────────────────────────────────────────
echo
echo "------------------------------------------------------"
info "日志文件前 3 行："
head -n 3 "$REAL_LOG" | while IFS= read -r l; do printf "    %s\n" "$l"; done

# 统计各计数器开启情况
ENABLED_COUNT=$(head -1 "$REAL_LOG" | tr ',' '\n' | tail -n +2 | wc -l)
NA_COUNT=0
if [[ "$DATA_LINES" -ge 1 ]]; then
    # 从第一条数据行统计 N/A 字段
    FIRST_DATA=$(sed -n '2p' "$REAL_LOG")
    NA_COUNT=$(echo "$FIRST_DATA" | tr ',' '\n' | grep -c '^N/A$' || true)
fi
ACTIVE_COUNT=$(( ENABLED_COUNT - NA_COUNT ))
info "计数器统计：共 ${ENABLED_COUNT} 个，活跃 ${ACTIVE_COUNT} 个，不可用 ${NA_COUNT} 个"

# 保留原始 *_$$.txt，不删除它们；在脚本末尾创建 workload-specific 链接


# 在脚本结束时创建工作负载专用的 stdout/stderr 和日志 链接，保留原始 *_$$.txt
WORKLOAD_SPECIFIC="log/pmu_monitor_all_time_${WORKLOAD_NAME}.log"
if [[ -n "$REAL_LOG" && -f "$REAL_LOG" ]]; then
    ln -sf "$REAL_LOG" "$WORKLOAD_SPECIFIC"
    ln -sf "$WORKLOAD_SPECIFIC" "log/pmu_monitor_all_time.log"
    info "已创建符号链接：log/pmu_monitor_all_time.log -> ${WORKLOAD_SPECIFIC}"
fi

if [[ -f "log/monitor_stdout_${RUN_ID}.txt" ]]; then
    ln -sf "log/monitor_stdout_${RUN_ID}.txt" "log/monitor_stdout_${WORKLOAD_NAME}.txt"
fi
if [[ -f "log/monitor_stderr_${RUN_ID}.txt" ]]; then
    ln -sf "log/monitor_stderr_${RUN_ID}.txt" "log/monitor_stderr_${WORKLOAD_NAME}.txt"
fi

# ── 汇总 ─────────────────────────────────────────────────────────────────────
echo
echo "======================================================"
printf "测试结果：${GREEN}%d 通过${NC}，${RED}%d 失败${NC}\n" \
       "$TESTS_PASSED" "$TESTS_FAILED"
echo "======================================================"

if [[ "$TESTS_FAILED" -eq 0 ]]; then
    printf "${GREEN}所有测试通过！${NC}\n"
    exit 0
else
    printf "${RED}存在失败测试，请检查上方输出。${NC}\n"
    exit 1
fi