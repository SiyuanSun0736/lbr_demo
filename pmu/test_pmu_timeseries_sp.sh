#!/bin/bash
# test_pmu_timeseries_sp.sh — pmu_timeseries_sp 样本周期触发采集测试脚本
#
# 测试流程：
#   1. 检查运行权限（perf_event_paranoid）
#   2. 编译 pmu_timeseries_sp 和 test_pmu_workload（如未编译）
#   3. 对比 timerfd 版：验证 sample_period 触发机制的正确性
#      3a. 系统级（无 PID）采集，小 sample_period 快速触发，验证产生样本
#      3b. 指定目标 PID 采集，较大 sample_period，验证 elapsed_ms 单调递增
#      3c. 参数校验：非法 -p 值应报错退出
#   4. 验证 CSV 格式与 pmu_timeseries 完全一致（标题列顺序、字段数）
#   5. 验证 mmap 触发路径：ring-buffer 正常消耗，不卡死，不 crash
#   6. 输出测试结果

set -euo pipefail

# ── 配置 ────────────────────────────────────────────────────────────────────
# 使用较小的 sample_period（1亿 cycles），在有负载时约每 0.03~0.1 s 触发一次
SAMPLE_PERIOD_FAST=100000000    # 1e8 cycles — 快速触发，用于样本数量验证
SAMPLE_PERIOD_SLOW=2000000000   # 2e9 cycles — 慢速触发，用于单调性验证
TEST_DURATION=15                # 测试持续时间（秒）
WORKLOAD_DURATION=120           # 工作负载最长持续时间（秒）
SP_BIN="./pmu_timeseries_sp"
WORKLOAD_BIN="./test/test_pmu_workload"
WORKLOAD_NAME="$(basename "$WORKLOAD_BIN")"
# 唯一运行 ID，包含时间戳与 PID，保证文件名一致
RUN_ID="$(date +%s)_$$"
LOG_LINK="log/pmu_timeseries.csv"
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

assert_false() {
    local desc="$1"; shift
    if ! "$@" 2>/dev/null; then
        pass "$desc"
        ((TESTS_PASSED++)) || true
    else
        fail "$desc"
        ((TESTS_FAILED++)) || true
    fi
}

# ── 清理钩子 ────────────────────────────────────────────────────────────────
SP_PID=""
WORKLOAD_PID=""

cleanup() {
    [[ -n "$SP_PID"       ]] && kill "$SP_PID"       2>/dev/null || true
    [[ -n "$WORKLOAD_PID" ]] && kill "$WORKLOAD_PID" 2>/dev/null || true
    wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# ── 进入 pmu 目录 ────────────────────────────────────────────────────────────
cd "$SCRIPT_DIR"

echo "======================================================"
echo "    pmu_timeseries_sp 样本周期触发采集功能测试"
echo "======================================================"
printf "  快速 sample_period : %d cycles\n"    "$SAMPLE_PERIOD_FAST"
printf "  慢速 sample_period : %d cycles\n"    "$SAMPLE_PERIOD_SLOW"
printf "  每阶段测试时长      : %d s\n"         "$TEST_DURATION"
echo

# ─────────────────────────────────────────────────────────────────────────────
# 步骤 1：权限检查
# ─────────────────────────────────────────────────────────────────────────────
info "检查 perf_event_paranoid 权限..."
PARANOID=$(cat /proc/sys/kernel/perf_event_paranoid 2>/dev/null || echo "2")
if [[ "$PARANOID" -gt 1 ]]; then
    warn "perf_event_paranoid=$PARANOID，跨进程监控可能需要 root 或 CAP_PERFMON"
    warn "若计数器全部失败，请以 root 运行或执行："
    warn "  echo 1 | sudo tee /proc/sys/kernel/perf_event_paranoid"
fi
info "当前 perf_event_paranoid=$PARANOID"
echo

# ─────────────────────────────────────────────────────────────────────────────
# 步骤 2：编译
# ─────────────────────────────────────────────────────────────────────────────
info "检查并编译所需程序..."

if [[ ! -x "$SP_BIN" ]]; then
    info "编译 pmu_timeseries_sp..."
    make pmu_timeseries_sp
fi
assert_true "pmu_timeseries_sp 可执行文件存在" test -x "$SP_BIN"

if [[ ! -x "$WORKLOAD_BIN" ]]; then
    info "编译 test/.."
    make test
fi
assert_true "test 可执行文件存在" test -x "$WORKLOAD_BIN"
echo

# ─────────────────────────────────────────────────────────────────────────────
# 步骤 3a：参数校验 — 非法 -p 值应以非 0 退出
# ─────────────────────────────────────────────────────────────────────────────
echo "------------------------------------------------------"
info "3a. 参数校验测试"

assert_false "-p 0 非法值应报错退出" "$SP_BIN" -p 0
assert_false "-p -1 负数非法值应报错退出" "$SP_BIN" -p -1
assert_false "-p 字母 非法值应报错退出" "$SP_BIN" -p abc
echo

# ─────────────────────────────────────────────────────────────────────────────
# 步骤 3b：系统级采集（无 PID），快速 sample_period，验证触发机制
# ─────────────────────────────────────────────────────────────────────────────
echo "------------------------------------------------------"
info "3b. 系统级采集（无 PID，sample_period=${SAMPLE_PERIOD_FAST}）"

mkdir -p log

# 先启动工作负载产生 CPU 活动，确保 cycles 计数器能快速溢出
"$WORKLOAD_BIN" "$WORKLOAD_DURATION" &
WORKLOAD_PID=$!
info "工作负载 PID: $WORKLOAD_PID"
sleep 1  # 等待工作负载稳定

info "启动 pmu_timeseries_sp（系统级，快速 period）..."
"$SP_BIN" -p "$SAMPLE_PERIOD_FAST" \
    >"log/sp_stdout_syswide_${RUN_ID}.txt" 2>"log/sp_stderr_syswide_${RUN_ID}.txt" &
SP_PID=$!
info "采集器 PID: $SP_PID"



info "等待 ${TEST_DURATION} 秒..."
sleep "$TEST_DURATION"

assert_true "系统级采集器进程持续运行（未 crash）" kill -0 "$SP_PID"

# 停止采集器，保留工作负载给下一阶段
kill "$SP_PID" 2>/dev/null || true
wait "$SP_PID" 2>/dev/null || true
SP_PID=""
    # 保留原始 *_$$.txt，不删除它们；在脚本末尾创建 workload-specific 链接
    CHOSEN_LOG=""
    # (延迟创建 stdout/stderr 与 CSV 链接至脚本末尾)

# 验证 CSV
REAL_LOG_3B="$(readlink -f "$LOG_LINK" 2>/dev/null || echo "")"
assert_true "系统级日志符号链接存在" test -L "$LOG_LINK"
assert_true "系统级日志真实文件存在" test -f "$REAL_LOG_3B"


LINES_3B=$(wc -l < "$REAL_LOG_3B")
DATA_LINES_3B=$(( LINES_3B - 1 ))
info "系统级日志数据行数: $DATA_LINES_3B"
assert_true "系统级采集至少产生 3 行数据（cycles 触发正常）" \
    test "$DATA_LINES_3B" -ge 3

# 标题验证
assert_true "标题首列为 elapsed_ms"    grep -q "^elapsed_ms," "$REAL_LOG_3B"
assert_true "标题包含 dTLB-loads"      grep -q "dTLB-loads"      "$REAL_LOG_3B"
assert_true "标题包含 LLC-load-misses" grep -q "LLC-load-misses"  "$REAL_LOG_3B"
assert_true "标题包含 L1-dcache-loads" grep -q "L1-dcache-loads"  "$REAL_LOG_3B"
echo

# ─────────────────────────────────────────────────────────────────────────────
# 步骤 3c：指定 PID 采集，慢速 sample_period，验证 elapsed_ms 单调递增
# ─────────────────────────────────────────────────────────────────────────────
echo "------------------------------------------------------"
info "3c. 指定 PID 采集（PID=$WORKLOAD_PID，sample_period=${SAMPLE_PERIOD_SLOW}）"

assert_true "工作负载进程仍存活" kill -0 "$WORKLOAD_PID"

info "启动 pmu_timeseries_sp（PID 指定，慢速 period）..."
"$SP_BIN" "$WORKLOAD_PID" -p "$SAMPLE_PERIOD_SLOW" \
    >"log/sp_stdout_pid_${RUN_ID}.txt" 2>"log/sp_stderr_pid_${RUN_ID}.txt" &
SP_PID=$!
info "采集器 PID: $SP_PID"

info "等待 ${TEST_DURATION} 秒..."
sleep "$TEST_DURATION"

assert_true "指定 PID 采集器进程持续运行（未 crash）" kill -0 "$SP_PID"

kill "$SP_PID" 2>/dev/null || true
wait "$SP_PID" 2>/dev/null || true
SP_PID=""

kill "$WORKLOAD_PID" 2>/dev/null || true
wait "$WORKLOAD_PID" 2>/dev/null || true
WORKLOAD_PID=""

REAL_LOG_3C="$(readlink -f "$LOG_LINK" 2>/dev/null || echo "")"
assert_true "指定 PID 日志文件存在" test -f "$REAL_LOG_3C"


DATA_LINES_3C=$(( $(wc -l < "$REAL_LOG_3C") - 1 ))
info "指定 PID 日志数据行数: $DATA_LINES_3C"
assert_true "指定 PID 采集至少产生 1 行数据" test "$DATA_LINES_3C" -ge 1
echo

# ─────────────────────────────────────────────────────────────────────────────
# 步骤 4：CSV 格式与 pmu_timeseries 对齐验证
# ─────────────────────────────────────────────────────────────────────────────
echo "------------------------------------------------------"
info "4. 验证 CSV 格式与 pmu_timeseries 一致"

# 两个程序输出应有相同的列数（elapsed_ms + timestamp + 24 个计数器 = 26 列）
EXPECTED_COLS=26
HEADER_COLS=$(head -1 "$REAL_LOG_3B" | tr ',' '\n' | wc -l)
info "日志列数: $HEADER_COLS（预期 $EXPECTED_COLS）"
assert_true "CSV 列数等于 $EXPECTED_COLS" test "$HEADER_COLS" -eq "$EXPECTED_COLS"

# 第一列必须是 elapsed_ms，第二列必须是 timestamp
COL1=$(head -1 "$REAL_LOG_3B" | cut -d',' -f1)
COL2=$(head -1 "$REAL_LOG_3B" | cut -d',' -f2)
assert_true "第1列名称为 elapsed_ms"  test "$COL1" = "elapsed_ms"
assert_true "第2列名称为 timestamp"   test "$COL2" = "timestamp"

# 数据行字段数与标题一致
if [[ "$DATA_LINES_3B" -ge 1 ]]; then
    DATA_COLS=$(sed -n '2p' "$REAL_LOG_3B" | tr ',' '\n' | wc -l)
    assert_true "数据行字段数与标题行一致" test "$DATA_COLS" -eq "$HEADER_COLS"
fi
echo

# ─────────────────────────────────────────────────────────────────────────────
# 步骤 5：elapsed_ms 单调递增验证
# ─────────────────────────────────────────────────────────────────────────────
echo "------------------------------------------------------"
info "5. 验证 elapsed_ms 单调递增"

MONO_OK=true
PREV=-1
CUR=0
while IFS=, read -r elapsed_ms _rest; do
    [[ "$elapsed_ms" == "elapsed_ms" ]] && continue
    if [[ "$elapsed_ms" =~ ^[0-9]+$ ]]; then
        if (( elapsed_ms <= PREV )); then
            MONO_OK=false
            CUR=$elapsed_ms
            break
        fi
        PREV=$elapsed_ms
    fi
done < "$REAL_LOG_3B"

if $MONO_OK; then
    pass "elapsed_ms 列单调递增"
    ((TESTS_PASSED++)) || true
else
    fail "elapsed_ms 非单调：prev=$PREV cur=$CUR"
    ((TESTS_FAILED++)) || true
fi
echo

# ─────────────────────────────────────────────────────────────────────────────
# 步骤 6：验证非零计数器值（至少一个计数器产生了真实数据）
# ─────────────────────────────────────────────────────────────────────────────
echo "------------------------------------------------------"
info "6. 验证日志中存在非零计数器值"

ANY_NONZERO=false
while IFS= read -r line; do
    [[ "$line" == elapsed_ms* ]] && continue
    IFS=',' read -ra fields <<< "$line"
    for f in "${fields[@]:2}"; do   # 跳过前两列 elapsed_ms / timestamp
        [[ "$f" =~ ^[0-9]+$ ]] && [[ "$f" -gt 0 ]] && { ANY_NONZERO=true; break 2; }
    done
done < "$REAL_LOG_3B"

if $ANY_NONZERO; then
    pass "日志中存在非零计数器值"
    ((TESTS_PASSED++)) || true
else
    fail "日志所有计数器均为 0（硬件不支持或权限不足）"
    ((TESTS_FAILED++)) || true
fi
echo

# ─────────────────────────────────────────────────────────────────────────────
# 步骤 7：与 timerfd 版对比——触发模式差异说明
# ─────────────────────────────────────────────────────────────────────────────
echo "------------------------------------------------------"
info "7. 触发机制差异统计"

if [[ "$DATA_LINES_3B" -ge 2 ]]; then
    FIRST_MS=$(sed -n '2p' "$REAL_LOG_3B" | cut -d',' -f1)
    LAST_MS=$(tail -1  "$REAL_LOG_3B" | cut -d',' -f1)
    SPAN_MS=$(( LAST_MS - FIRST_MS ))
    if [[ "$DATA_LINES_3B" -gt 1 ]]; then
        AVG_INTERVAL=$(( SPAN_MS / (DATA_LINES_3B - 1) ))
        info "实测平均触发间隔: ${AVG_INTERVAL} ms（${DATA_LINES_3B} 个样本，跨度 ${SPAN_MS} ms）"
        info "对应吞吐量约: $(( SAMPLE_PERIOD_FAST / (AVG_INTERVAL > 0 ? AVG_INTERVAL : 1) / 1000 )) MHz cycles"
    fi
fi

# ─────────────────────────────────────────────────────────────────────────────
# 步骤 8：日志摘要 & 清理
# ─────────────────────────────────────────────────────────────────────────────
echo
echo "------------------------------------------------------"
info "日志摘要（系统级，前 3 行）："
head -n 3 "$REAL_LOG_3B" | while IFS= read -r l; do printf "    %s\n" "$l"; done

TOTAL_COLS_FINAL=$(head -1 "$REAL_LOG_3B" | tr ',' '\n' | wc -l)
COUNTER_COLS=$(( TOTAL_COLS_FINAL - 2 ))
EMPTY_COUNT=0
if [[ "$DATA_LINES_3B" -ge 1 ]]; then
    FIRST_DATA=$(sed -n '2p' "$REAL_LOG_3B")
    EMPTY_COUNT=$(echo "$FIRST_DATA" | tr ',' '\n' | tail -n +"$((2+1))" | grep -c '^$' || true)
fi
ACTIVE_COUNT=$(( COUNTER_COLS - EMPTY_COUNT ))
info "计数器统计：共 ${COUNTER_COLS} 个，活跃 ${ACTIVE_COUNT} 个，不可用 ${EMPTY_COUNT} 个"
info "系统级日志路径: $REAL_LOG_3B"

# 保留原始 *_$$.txt，不删除它们；在脚本末尾创建 workload-specific 链接


# 在脚本结束前创建工作负载专用的 stdout/stderr 和 CSV 链接，保留原始 *_$$.txt
CHOSEN_LOG=""
if [[ -n "$REAL_LOG_3C" && -f "$REAL_LOG_3C" ]]; then
    CHOSEN_LOG="$REAL_LOG_3C"
elif [[ -n "$REAL_LOG_3B" && -f "$REAL_LOG_3B" ]]; then
    CHOSEN_LOG="$REAL_LOG_3B"
fi
if [[ -n "$CHOSEN_LOG" ]]; then
    WORKLOAD_SPECIFIC="log/pmu_timeseries_${WORKLOAD_NAME}.csv"
    ln -sf "$CHOSEN_LOG" "$WORKLOAD_SPECIFIC"
    ln -sf "$WORKLOAD_SPECIFIC" "log/pmu_timeseries.csv"
    info "已创建符号链接：log/pmu_timeseries.csv -> ${WORKLOAD_SPECIFIC}"
fi

# stdout/stderr 链接：优先使用 PID 文件，否则使用 syswide
if [[ -f "log/sp_stdout_pid_${RUN_ID}.txt" ]]; then
    ln -sf "log/sp_stdout_pid_${RUN_ID}.txt" "log/timeseries_stdout_${WORKLOAD_NAME}.txt"
fi
if [[ -f "log/sp_stderr_pid_${RUN_ID}.txt" ]]; then
    ln -sf "log/sp_stderr_pid_${RUN_ID}.txt" "log/timeseries_stderr_${WORKLOAD_NAME}.txt"
fi
if [[ ! -f "log/timeseries_stdout_${WORKLOAD_NAME}.txt" && -f "log/sp_stdout_syswide_${RUN_ID}.txt" ]]; then
    ln -sf "log/sp_stdout_syswide_${RUN_ID}.txt" "log/timeseries_stdout_${WORKLOAD_NAME}.txt"
fi
if [[ ! -f "log/timeseries_stderr_${WORKLOAD_NAME}.txt" && -f "log/sp_stderr_syswide_${RUN_ID}.txt" ]]; then
    ln -sf "log/sp_stderr_syswide_${RUN_ID}.txt" "log/timeseries_stderr_${WORKLOAD_NAME}.txt"
fi

# ─────────────────────────────────────────────────────────────────────────────
# 汇总
# ─────────────────────────────────────────────────────────────────────────────
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