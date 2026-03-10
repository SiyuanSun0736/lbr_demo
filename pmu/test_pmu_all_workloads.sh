#!/bin/bash
# test_pmu_all_workloads.sh — PMU 全指标自动批量测试
#
# 依次将 WORKLOAD_BIN 设置为各专项测试程序，自动调用
# test_pmu_timeseries.sh 完成采集与验证，最终输出汇总报告。
#
# 测试覆盖的指标：
#   dTLB     — dTLB-loads/misses, dTLB-stores/misses
#   iTLB     — iTLB-loads/misses
#   L1D      — L1-dcache-loads/misses/stores
#   L1I      — L1-icache-load-misses
#   L1Dpend  — l1d.replacement, l1d_pend_miss.fb_full/pending
#   LLC      — LLC-loads/misses/stores, cache-references/misses
#   MemInst  — mem_inst_retired.all_loads/stores/any
#   General  — 综合压力（所有计数器）
#
# 用法：
#   sudo bash test_pmu_all_workloads.sh [-i <interval_ms>] [-d <duration_s>]
#
# 环境变量（均可在命令行前置覆盖）：
#   INTERVAL_MS    采样间隔，默认 500 ms
#   TEST_DURATION  每个工作负载的测试时长，默认 15 s
#   SKIP_BUILD     若已编译，设为 1 跳过编译步骤

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TIMESERIES_TEST="$SCRIPT_DIR/test_pmu_timeseries.sh"

# ── 可覆盖的全局配置 ─────────────────────────────────────────────────────────
: "${INTERVAL_MS:=500}"
: "${TEST_DURATION:=15}"
: "${SKIP_BUILD:=0}"

# 解析命令行
while [[ $# -gt 0 ]]; do
    case "$1" in
        -i) INTERVAL_MS="$2";  shift 2 ;;
        -d) TEST_DURATION="$2"; shift 2 ;;
        *)  echo "Usage: $0 [-i <interval_ms>] [-d <duration_s>]"; exit 1 ;;
    esac
done

# WORKLOAD_DURATION 必须大于 TEST_DURATION，多留 30 s 余量
WORKLOAD_DURATION=$(( TEST_DURATION + 30 ))
export WORKLOAD_DURATION

# ── 颜色 ─────────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'

info()  { printf "${CYAN}[INFO]${NC}  %s\n" "$*"; }
pass()  { printf "${GREEN}[PASS]${NC}  %s\n" "$*"; }
fail()  { printf "${RED}[FAIL]${NC}  %s\n" "$*"; }
warn()  { printf "${YELLOW}[WARN]${NC}  %s\n" "$*"; }
title() { printf "\n${BOLD}%s${NC}\n" "$*"; }

# ── 工作负载定义（顺序：综合 → 各专项） ─────────────────────────────────────
WORKLOAD_BINS=(
    "./test/test_pmu_workload"   # 综合压力——所有计数器均可观测
    "./test/test_dtlb"           # dTLB-loads/misses, dTLB-stores/misses
    "./test/test_itlb"           # iTLB-loads/misses
    "./test/test_l1dcache"       # L1-dcache-loads/misses/stores
    "./test/test_l1icache"       # L1-icache-load-misses
    "./test/test_l1d_pend_miss"  # l1d.replacement, l1d_pend_miss.*
    "./test/test_llc"            # LLC-loads/misses/stores, cache-references/misses
    "./test/test_mem_inst"       # mem_inst_retired.*
    "./test/test_baseline_sleep"  # 空载基准对比
    "./test/test_baseline_busyloop"  # 空载基准对比（仅 busyloop）
    "./test/test_workload2"      # 额外压力测试（覆盖更多计数器）
    "./test/test_workload2_light"  # 额外压力测试（较轻负载，验证敏感性）
)

WORKLOAD_LABELS=(
    "综合压力测试"
    "dTLB 压力测试"
    "iTLB 压力测试"
    "L1D 缓存测试"
    "L1I 缓存测试"
    "L1D 悬挂缺失测试"
    "LLC 缓存测试"
    "内存指令吞吐测试"
    "空载基准对比"
    "空载基准对比（忙循环）"
    "额外压力测试"
    "额外压力测试（较轻负载）"
)

WORKLOAD_FOCUS=(
    "dTLB/iTLB/L1D/L1I/LLC/MemInst"
    "dTLB-loads/misses, dTLB-stores/misses"
    "iTLB-loads/misses"
    "L1-dcache-loads/misses/stores"
    "L1-icache-load-misses"
    "l1d.replacement, l1d_pend_miss.fb_full/pending"
    "LLC-loads/misses, cache-references/misses"
    "mem_inst_retired.all_loads/stores/any"
    "空载基准（所有计数器接近零）"
    "空载基准（忙循环）"
    "额外压力（覆盖更多计数器）"
    "额外压力（较轻负载，验证敏感性）"
)

N="${#WORKLOAD_BINS[@]}"
EST_TOTAL=$(( N * (TEST_DURATION + 7) ))  # +7 秒：启动 + 等待 + 停止

# ── 横幅 ─────────────────────────────────────────────────────────────────────
echo "======================================================"
echo "     PMU 全指标自动批量测试  (pmu_timeseries)"
echo "======================================================"
printf "  工作负载数  : %d\n"  "$N"
printf "  采样间隔    : %d ms\n" "$INTERVAL_MS"
printf "  每轮时长    : %d s\n"  "$TEST_DURATION"
printf "  预计总时长  : ~%d s (~%d 分钟)\n" "$EST_TOTAL" $(( EST_TOTAL / 60 + 1 ))
echo

cd "$SCRIPT_DIR"

# ── 步骤 1：编译 ──────────────────────────────────────────────────────────────
if [[ "$SKIP_BUILD" != "1" ]]; then
    title "步骤 1/2：编译所有二进制"

    if [[ ! -x "./pmu_timeseries" ]]; then
        info "编译 pmu_timeseries..."
        make pmu_timeseries
    else
        info "pmu_timeseries 已存在，跳过编译"
    fi

    need_build=0
    for bin in "${WORKLOAD_BINS[@]}"; do
        [[ ! -x "$bin" ]] && { need_build=1; break; }
    done

    if [[ "$need_build" -eq 1 ]]; then
        info "编译 test/ 目录下所有工作负载..."
        make -C test
    else
        info "所有工作负载二进制已存在，跳过编译"
    fi

    # 验证二进制
    for bin in "${WORKLOAD_BINS[@]}"; do
        if [[ ! -x "$bin" ]]; then
            warn "未找到可执行文件：$bin（对应测试将被跳过）"
        fi
    done
fi

# ── 步骤 2：逐一运行测试 ──────────────────────────────────────────────────────
title "步骤 2/2：逐一运行测试"

TOTAL=0
PASSED=0
FAILED=0
SKIPPED=0
FAILED_LABELS=()
RESULT_LOG="$SCRIPT_DIR/log/test_pmu_all_workloads_$(date +%Y%m%d_%H%M%S).txt"
mkdir -p "$SCRIPT_DIR/log"

# 表头写入结果日志
{
    echo "PMU 全量测试报告 — $(date)"
    echo "采样间隔 ${INTERVAL_MS} ms，每轮 ${TEST_DURATION} s"
    echo "------------------------------------------------------"
} >> "$RESULT_LOG"

for i in "${!WORKLOAD_BINS[@]}"; do
    bin="${WORKLOAD_BINS[$i]}"
    label="${WORKLOAD_LABELS[$i]}"
    focus="${WORKLOAD_FOCUS[$i]}"

    echo ""
    echo "======================================================"
    printf "${BOLD}[%d/%d] %s${NC}\n" "$((i+1))" "$N" "$label"
    printf "   关注计数器: %s\n" "$focus"
    printf "   工作负载  : %s\n" "$bin"
    echo "======================================================"

    ((TOTAL++)) || true

    if [[ ! -x "$bin" ]]; then
        warn "跳过：二进制不可执行或不存在"
        ((SKIPPED++)) || true
        echo "SKIP  $label" >> "$RESULT_LOG"
        continue
    fi

    # 通过环境变量把参数传给 test_pmu_timeseries.sh
    exit_code=0
    WORKLOAD_BIN="$bin" \
    INTERVAL_MS="$INTERVAL_MS" \
    TEST_DURATION="$TEST_DURATION" \
    WORKLOAD_DURATION="$WORKLOAD_DURATION" \
        bash "$TIMESERIES_TEST" || exit_code=$?

    if [[ "$exit_code" -eq 0 ]]; then
        ((PASSED++)) || true
        pass "$label 测试通过"
        echo "PASS  $label" >> "$RESULT_LOG"
    else
        ((FAILED++)) || true
        FAILED_LABELS+=("$label")
        fail "$label 测试失败（exit=$exit_code）"
        echo "FAIL  $label  (exit=$exit_code)" >> "$RESULT_LOG"
    fi
done

# ── 汇总报告 ──────────────────────────────────────────────────────────────────
echo ""
echo "======================================================"
printf "${BOLD}全量测试汇总${NC}\n"
echo "------------------------------------------------------"
printf "  总计    : %d\n"  "$TOTAL"
printf "  ${GREEN}通过${NC}    : %d\n"  "$PASSED"
printf "  ${RED}失败${NC}    : %d\n"  "$FAILED"
if [[ "$SKIPPED" -gt 0 ]]; then
    printf "  ${YELLOW}跳过${NC}    : %d\n" "$SKIPPED"
fi

if [[ "${#FAILED_LABELS[@]}" -gt 0 ]]; then
    printf "\n  ${RED}失败工作负载：${NC}\n"
    for lbl in "${FAILED_LABELS[@]}"; do
        printf "    × %s\n" "$lbl"
    done
fi

printf "\n  结果日志: %s\n" "$RESULT_LOG"
echo "======================================================"

{
    echo "------------------------------------------------------"
    printf "通过 %d / 失败 %d / 跳过 %d\n" "$PASSED" "$FAILED" "$SKIPPED"
} >> "$RESULT_LOG"

if [[ "$FAILED" -eq 0 ]]; then
    printf "${GREEN}所有测试通过！${NC}\n"
    exit 0
else
    printf "${RED}存在失败测试，请检查上方输出。${NC}\n"
    exit 1
fi
