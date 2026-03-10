#!/bin/bash
# plot_pmu_all_workloads.sh — PMU 全指标自动批量采集 + 可视化脚本
#
# 依次将 WORKLOAD_BIN 设置为各专项测试程序，自动调用
# test_pmu_timeseries.sh 完成数据采集，再调用
# shell/plot_pmu_timeseries.py 完成可视化，最终输出汇总报告。
#
# CSV 命名约定（由 test_pmu_timeseries.sh 写入）：
#   log/pmu_timeseries_{WORKLOAD_NAME}.csv
#   其中 WORKLOAD_NAME = basename(WORKLOAD_BIN)，例如 test_dtlb
#   可视化脚本读取 log/pmu_timeseries_test_{project}.csv
#   project = WORKLOAD_NAME 去除 "test_" 前缀，例如 dtlb
#
# 用法：
#   sudo bash plot_pmu_all_workloads.sh [-i <interval_ms>] [-d <duration_s>]
#
# 环境变量（均可在命令行前置覆盖）：
#   INTERVAL_MS      采样间隔，默认 500 ms
#   TEST_DURATION    每个工作负载的测试时长，默认 15 s
#   SKIP_BUILD       若已编译，设为 1 跳过编译步骤
#   SKIP_COLLECTION  若已有 CSV 数据，设为 1 仅执行绘图步骤

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TIMESERIES_TEST="$SCRIPT_DIR/test_pmu_timeseries.sh"
PLOT_SCRIPT="$SCRIPT_DIR/shell/plot_pmu_timeseries.py"

# ── 可覆盖的全局配置 ─────────────────────────────────────────────────────────
: "${INTERVAL_MS:=500}"
: "${TEST_DURATION:=15}"
: "${SKIP_BUILD:=1}"
: "${SKIP_COLLECTION:=1}"

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
    "./test/test_mem_inst"           # mem_inst_retired.*
    "./test/test_baseline_sleep"     # 空载基准对比（仅 sleep）
    "./test/test_baseline_busyloop"  # 空载基准对比（仅 busyloop）
    "./test/test_workload2"          # 额外压力测试（覆盖更多计数器）
    "./test/test_workload2_light"    # 额外压力测试（较轻负载，验证敏感性）
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
EST_TOTAL=$(( N * (TEST_DURATION + 10) ))

# ── 横幅 ─────────────────────────────────────────────────────────────────────
echo "======================================================"
echo "     PMU 全指标批量采集 + 可视化  (pmu_timeseries)"
echo "======================================================"
printf "  工作负载数    : %d\n"  "$N"
printf "  采样间隔      : %d ms\n" "$INTERVAL_MS"
printf "  每轮时长      : %d s\n"  "$TEST_DURATION"
printf "  跳过采集      : %s\n"    "$SKIP_COLLECTION"
printf "  预计总时长    : ~%d s (~%d 分钟)\n" "$EST_TOTAL" $(( EST_TOTAL / 60 + 1 ))
echo

cd "$SCRIPT_DIR"

# ── 步骤 1：检查 Python 环境 ──────────────────────────────────────────────────
title "步骤 1/3：检查 Python 环境"

VENV_ACTIVATE="$SCRIPT_DIR/shell/.venv/bin/activate"
if [[ -f "$VENV_ACTIVATE" ]]; then
    # shellcheck source=/dev/null
    source "$VENV_ACTIVATE"
    info "已激活虚拟环境: $VENV_ACTIVATE"
fi

PYTHON_BIN=""
for py in "$SCRIPT_DIR/shell/.venv/bin/python3" python3 python; do
    if command -v "$py" &>/dev/null || [[ -x "$py" ]]; then
        PYTHON_BIN="$py"
        break
    fi
done

if [[ -z "$PYTHON_BIN" ]]; then
    fail "未找到 Python 可执行文件，请安装 Python 3 或激活虚拟环境"
    exit 1
fi
info "使用 Python: $PYTHON_BIN ($($PYTHON_BIN --version 2>&1))"

if [[ ! -f "$PLOT_SCRIPT" ]]; then
    fail "未找到绘图脚本: $PLOT_SCRIPT"
    exit 1
fi
info "绘图脚本: $PLOT_SCRIPT"

# ── 步骤 2：编译 ──────────────────────────────────────────────────────────────
if [[ "$SKIP_BUILD" != "1" && "$SKIP_COLLECTION" != "1" ]]; then
    title "步骤 2/3：编译所有二进制"

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

    for bin in "${WORKLOAD_BINS[@]}"; do
        if [[ ! -x "$bin" ]]; then
            warn "未找到可执行文件：$bin（对应测试将被跳过）"
        fi
    done
else
    title "步骤 2/3：跳过编译（SKIP_BUILD=$SKIP_BUILD / SKIP_COLLECTION=$SKIP_COLLECTION）"
fi

# ── 步骤 3：逐一采集 + 绘图 ──────────────────────────────────────────────────
title "步骤 3/3：逐一采集 + 绘图"

TOTAL=0
COLLECT_PASSED=0
COLLECT_FAILED=0
PLOT_PASSED=0
PLOT_FAILED=0
SKIPPED=0
FAILED_LABELS=()
RESULT_LOG="$SCRIPT_DIR/log/plot_pmu_all_workloads_$(date +%Y%m%d_%H%M%S).txt"
mkdir -p "$SCRIPT_DIR/log"

{
    echo "PMU 全量采集+可视化报告 — $(date)"
    echo "采样间隔 ${INTERVAL_MS} ms，每轮 ${TEST_DURATION} s"
    echo "------------------------------------------------------"
} >> "$RESULT_LOG"

for i in "${!WORKLOAD_BINS[@]}"; do
    bin="${WORKLOAD_BINS[$i]}"
    label="${WORKLOAD_LABELS[$i]}"
    focus="${WORKLOAD_FOCUS[$i]}"

    # 从 binary basename 中提取项目名：去除 "test_" 前缀
    workload_name="$(basename "$bin")"
    project="${workload_name#test_}"

    echo ""
    echo "======================================================"
    printf "${BOLD}[%d/%d] %s${NC}\n" "$((i+1))" "$N" "$label"
    printf "   关注计数器: %s\n" "$focus"
    printf "   工作负载  : %s\n" "$bin"
    printf "   项目名    : %s\n" "$project"
    echo "======================================================"

    ((TOTAL++)) || true

    # ── 3a：数据采集 ────────────────────────────────────────────────────
    if [[ "$SKIP_COLLECTION" != "1" ]]; then
        if [[ ! -x "$bin" ]]; then
            warn "跳过：二进制不可执行或不存在"
            ((SKIPPED++)) || true
            echo "SKIP  $label" >> "$RESULT_LOG"
            continue
        fi

        collect_exit=0
        WORKLOAD_BIN="$bin" \
        INTERVAL_MS="$INTERVAL_MS" \
        TEST_DURATION="$TEST_DURATION" \
        WORKLOAD_DURATION="$WORKLOAD_DURATION" \
            bash "$TIMESERIES_TEST" || collect_exit=$?

        if [[ "$collect_exit" -eq 0 ]]; then
            ((COLLECT_PASSED++)) || true
            pass "[$label] 数据采集通过"
            echo "COLLECT_PASS  $label" >> "$RESULT_LOG"
        else
            ((COLLECT_FAILED++)) || true
            fail "[$label] 数据采集失败（exit=$collect_exit）"
            echo "COLLECT_FAIL  $label  (exit=$collect_exit)" >> "$RESULT_LOG"
            FAILED_LABELS+=("$label (采集失败)")
            # 采集失败时仍尝试绘图（可能存在旧数据）
        fi
    else
        info "跳过数据采集（SKIP_COLLECTION=1）"
    fi

    # ── 3b：可视化 ──────────────────────────────────────────────────────
    csv_file="$SCRIPT_DIR/log/pmu_timeseries_test_${project}.csv"
    if [[ ! -f "$csv_file" ]]; then
        warn "CSV 文件不存在，跳过绘图：$csv_file"
        echo "PLOT_SKIP  $label  (no csv)" >> "$RESULT_LOG"
        continue
    fi

    info "开始绘图：project=$project"
    plot_exit=0
    if [[ -n "${SUDO_USER:-}" ]]; then
        sudo -u "$SUDO_USER" "$PYTHON_BIN" "$PLOT_SCRIPT" -p "$project" || plot_exit=$?
    else
        "$PYTHON_BIN" "$PLOT_SCRIPT" -p "$project" || plot_exit=$?
    fi

    if [[ "$plot_exit" -eq 0 ]]; then
        ((PLOT_PASSED++)) || true
        pass "[$label] 绘图完成"
        echo "PLOT_PASS  $label" >> "$RESULT_LOG"
    else
        ((PLOT_FAILED++)) || true
        fail "[$label] 绘图失败（exit=$plot_exit）"
        echo "PLOT_FAIL  $label  (exit=$plot_exit)" >> "$RESULT_LOG"
        FAILED_LABELS+=("$label (绘图失败)")
    fi
done

# ── 汇总报告 ──────────────────────────────────────────────────────────────────
echo ""
echo "======================================================"
printf "${BOLD}全量测试汇总${NC}\n"
echo "------------------------------------------------------"
printf "  总计        : %d\n" "$TOTAL"
if [[ "$SKIP_COLLECTION" != "1" ]]; then
    printf "  ${GREEN}采集通过${NC}    : %d\n"  "$COLLECT_PASSED"
    printf "  ${RED}采集失败${NC}    : %d\n"  "$COLLECT_FAILED"
fi
printf "  ${GREEN}绘图通过${NC}    : %d\n"  "$PLOT_PASSED"
printf "  ${RED}绘图失败${NC}    : %d\n"  "$PLOT_FAILED"
if [[ "$SKIPPED" -gt 0 ]]; then
    printf "  ${YELLOW}跳过${NC}        : %d\n" "$SKIPPED"
fi

if [[ "${#FAILED_LABELS[@]}" -gt 0 ]]; then
    printf "\n  ${RED}失败项目：${NC}\n"
    for lbl in "${FAILED_LABELS[@]}"; do
        printf "    × %s\n" "$lbl"
    done
fi

printf "\n  结果日志: %s\n" "$RESULT_LOG"
echo "======================================================"

{
    echo "------------------------------------------------------"
    printf "采集 通过 %d / 失败 %d  |  绘图 通过 %d / 失败 %d  |  跳过 %d\n" \
        "$COLLECT_PASSED" "$COLLECT_FAILED" "$PLOT_PASSED" "$PLOT_FAILED" "$SKIPPED"
} >> "$RESULT_LOG"

TOTAL_FAILED=$(( COLLECT_FAILED + PLOT_FAILED ))
if [[ "$TOTAL_FAILED" -eq 0 ]]; then
    printf "${GREEN}所有采集与绘图任务完成！${NC}\n"
    exit 0
else
    printf "${RED}存在失败项，请检查上方输出。${NC}\n"
    exit 1
fi
