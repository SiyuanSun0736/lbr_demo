#!/bin/bash
# compare_all_vs_baselines.sh — 将各专项测试与基准程序进行 PMU 对比可视化
#
# 对所有专项测试（dtlb / itlb / l1dcache / l1icache / l1d_pend_miss / llc / mem_inst）
# 分别与以下五个基准程序生成对比图：
#   - baseline_sleep    （纯 sleep 空载基准，各计数器接近零）
#   - baseline_busyloop （纯寄存器忙等，CPU 满转但零 cache/TLB miss）
#   - pmu_workload      （第一版综合压力，各计数器均有显著数值）
#   - workload2         （第二版 8 阶段综合压力，覆盖所有 PMU 计数器组）
#   - workload2_light   （低频率版，迭代量为 workload2 的 1/20 + usleep）
#
# 依赖：
#   shell/plot_pmu_compare.py  — 双项目对比绘图脚本
#   log/pmu_timeseries_test_{project}.csv  — 已采集的 CSV 数据
#
# 用法：
#   bash compare_all_vs_baselines.sh
#   sudo bash compare_all_vs_baselines.sh          # 与 sudo 结合时保持文件归属
#
# 环境变量（均默认为 0，设为 1 可跳过对应基准的所有对比）：
#   SKIP_BASELINE_SLEEP    跳过与 baseline_sleep 的对比
#   SKIP_BASELINE_BUSYLOOP 跳过与 baseline_busyloop 的对比
#   SKIP_PMU_WORKLOAD      跳过与 pmu_workload 的对比
#   SKIP_WORKLOAD2         跳过与 workload2 的对比
#   SKIP_WORKLOAD2_LIGHT   跳过与 workload2_light 的对比

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPARE_SCRIPT="$SCRIPT_DIR/shell/plot_pmu_compare.py"

: "${SKIP_BASELINE_SLEEP:=0}"
: "${SKIP_BASELINE_BUSYLOOP:=0}"
: "${SKIP_PMU_WORKLOAD:=0}"
: "${SKIP_WORKLOAD2:=0}"
: "${SKIP_WORKLOAD2_LIGHT:=0}"

# ── 颜色 ─────────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'

info()  { printf "${CYAN}[INFO]${NC}  %s\n" "$*"; }
pass()  { printf "${GREEN}[PASS]${NC}  %s\n" "$*"; }
fail()  { printf "${RED}[FAIL]${NC}  %s\n" "$*"; }
warn()  { printf "${YELLOW}[WARN]${NC}  %s\n" "$*"; }
title() { printf "\n${BOLD}%s${NC}\n" "$*"; }

# ── 专项测试列表（不含两个基准） ──────────────────────────────────────────────
# project = binary basename 去除 test_ 前缀
PROJECTS=(
    "dtlb"
    "itlb"
    "l1dcache"
    "l1icache"
    "l1d_pend_miss"
    "llc"
    "mem_inst"
)

PROJECT_LABELS=(
    "dTLB 压力测试"
    "iTLB 压力测试"
    "L1D 缓存测试"
    "L1I 缓存测试"
    "L1D 悬挂缺失测试"
    "LLC 缓存测试"
    "内存指令吞吐测试"
)

# 五个基准
N="${#PROJECTS[@]}"
BASELINES=()
[[ "$SKIP_BASELINE_SLEEP"    != "1" ]] && BASELINES+=("baseline_sleep")
[[ "$SKIP_BASELINE_BUSYLOOP" != "1" ]] && BASELINES+=("baseline_busyloop")
[[ "$SKIP_PMU_WORKLOAD"      != "1" ]] && BASELINES+=("pmu_workload")
[[ "$SKIP_WORKLOAD2"         != "1" ]] && BASELINES+=("workload2")
[[ "$SKIP_WORKLOAD2_LIGHT"   != "1" ]] && BASELINES+=("workload2_light")

echo "======================================================"
echo "   PMU 专项测试 vs 基准对比可视化"
echo "======================================================"
printf "  专项测试数    : %d\n" "$N"
printf "  基准程序      : %s\n" "${BASELINES[*]:-（全部跳过）}"
echo

# ── 检查绘图脚本 ──────────────────────────────────────────────────────────────
if [[ ! -f "$COMPARE_SCRIPT" ]]; then
    fail "未找到对比绘图脚本: $COMPARE_SCRIPT"
    exit 1
fi

# ── 激活虚拟环境 & 探测 Python ────────────────────────────────────────────────
title "步骤 1/2：检查 Python 环境"

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

# ── 执行对比绘图 ──────────────────────────────────────────────────────────────
title "步骤 2/2：执行对比绘图"

TOTAL=0
PASSED=0
FAILED=0
SKIPPED=0
FAILED_ITEMS=()
RESULT_LOG="$SCRIPT_DIR/log/compare_all_vs_baselines_$(date +%Y%m%d_%H%M%S).txt"
mkdir -p "$SCRIPT_DIR/log"

{
    echo "PMU 对比绘图报告 — $(date)"
    echo "专项测试数: $N  |  基准: ${BASELINES[*]:-无}"
    echo "------------------------------------------------------"
} >> "$RESULT_LOG"

run_python() {
    # 根据是否在 sudo 环境下决定调用方式，避免输出文件归属为 root
    if [[ -n "${SUDO_USER:-}" ]]; then
        sudo -u "$SUDO_USER" "$PYTHON_BIN" "$@"
    else
        "$PYTHON_BIN" "$@"
    fi
}

for baseline in "${BASELINES[@]}"; do
    echo ""
    echo "======================================================"
    printf "${BOLD}基准: %s${NC}\n" "$baseline"
    echo "======================================================"

    baseline_csv="$SCRIPT_DIR/log/pmu_timeseries_test_${baseline}.csv"
    if [[ ! -f "$baseline_csv" ]]; then
        warn "基准 CSV 不存在，跳过所有与 ${baseline} 的对比: $baseline_csv"
        echo "SKIP_BASELINE  $baseline  (no csv)" >> "$RESULT_LOG"
        SKIPPED=$(( SKIPPED + N ))
        continue
    fi

    for idx in "${!PROJECTS[@]}"; do
        project="${PROJECTS[$idx]}"
        label="${PROJECT_LABELS[$idx]}"
        ((TOTAL++)) || true

        printf "\n  [%d/%d] %s  vs  %s\n" "$((idx+1))" "$N" "$project" "$baseline"

        csv_file="$SCRIPT_DIR/log/pmu_timeseries_test_${project}.csv"
        if [[ ! -f "$csv_file" ]]; then
            warn "  CSV 不存在，跳过: $csv_file"
            ((SKIPPED++)) || true
            echo "SKIP  ${project}_vs_${baseline}  (no csv: $project)" >> "$RESULT_LOG"
            continue
        fi

        plot_exit=0
        run_python "$COMPARE_SCRIPT" -p1 "$project" -p2 "$baseline" || plot_exit=$?

        if [[ "$plot_exit" -eq 0 ]]; then
            ((PASSED++)) || true
            pass "  [${label}] vs [${baseline}] 对比图生成完成"
            echo "PASS  ${project}_vs_${baseline}" >> "$RESULT_LOG"
        else
            ((FAILED++)) || true
            fail "  [${label}] vs [${baseline}] 绘图失败（exit=$plot_exit）"
            echo "FAIL  ${project}_vs_${baseline}  (exit=$plot_exit)" >> "$RESULT_LOG"
            FAILED_ITEMS+=("${project} vs ${baseline}")
        fi
    done
done

# ── 汇总报告 ──────────────────────────────────────────────────────────────────
echo ""
echo "======================================================"
printf "${BOLD}对比绘图汇总${NC}\n"
echo "------------------------------------------------------"
printf "  总计          : %d\n" "$TOTAL"
printf "  ${GREEN}通过${NC}          : %d\n" "$PASSED"
printf "  ${RED}失败${NC}          : %d\n" "$FAILED"
if [[ "$SKIPPED" -gt 0 ]]; then
    printf "  ${YELLOW}跳过${NC}          : %d （CSV 文件不存在）\n" "$SKIPPED"
fi

if [[ "${#FAILED_ITEMS[@]}" -gt 0 ]]; then
    printf "\n  ${RED}失败项目：${NC}\n"
    for item in "${FAILED_ITEMS[@]}"; do
        printf "    × %s\n" "$item"
    done
fi

printf "\n  结果日志: %s\n" "$RESULT_LOG"
echo "======================================================"

{
    echo "------------------------------------------------------"
    printf "通过 %d / 失败 %d / 跳过 %d\n" "$PASSED" "$FAILED" "$SKIPPED"
} >> "$RESULT_LOG"

if [[ "$FAILED" -eq 0 ]]; then
    printf "${GREEN}全部对比图生成完成！${NC}\n"
    exit 0
else
    printf "${RED}存在失败项，请检查上方输出。${NC}\n"
    exit 1
fi
