/*
 * pmu_timeseries.c — PMU time-series sampler
 *
 * Samples hardware performance counters at a fixed user-configurable interval
 * and writes results as CSV to a timestamped log file.  The first column of
 * every data row is the elapsed time in milliseconds since the program started,
 * making the output directly suitable for plotting / further analysis.
 *
 * Usage:
 *   sudo ./pmu_timeseries [PID] [-i <interval_ms>]
 *
 *   PID            – target process (-1 = system-wide, default)
 *   -i <ms>        – sampling interval in milliseconds (default: 1000)
 *
 * Examples:
 *   sudo ./pmu_timeseries                    # system-wide, 1 s interval
 *   sudo ./pmu_timeseries 12345              # pid 12345, 1 s interval
 *   sudo ./pmu_timeseries 12345 -i 100       # pid 12345, 100 ms interval
 */

#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/ioctl.h>
#include <linux/perf_event.h>
#include <asm/unistd.h>
#include <signal.h>
#include <stdint.h>
#include <errno.h>
#include <time.h>
#include <sys/stat.h>
#include <sys/timerfd.h>
#include <poll.h>

/* ── counter descriptors ─────────────────────────────────────────────────── */

typedef struct {
    int         fd;
    const char *name;
    uint64_t    count;      /* value of the most recent interval           */
    int         enabled;    /* 1 if the fd was opened successfully          */
    uint64_t    time_enabled; /* last read time_enabled */
    uint64_t    time_running; /* last read time_running */
} perf_counter_t;

static perf_counter_t counters[] = {
    /* dTLB */
    {-1, "dTLB-loads",               0, 0, 0, 0},
    {-1, "dTLB-load-misses",         0, 0, 0, 0},
    {-1, "dTLB-stores",              0, 0, 0, 0},
    {-1, "dTLB-store-misses",        0, 0, 0, 0},
    /* iTLB */
    {-1, "iTLB-loads",               0, 0, 0, 0},
    {-1, "iTLB-load-misses",         0, 0, 0, 0},
    /* L1-D */
    {-1, "L1-dcache-loads",          0, 0, 0, 0},
    {-1, "L1-dcache-load-misses",    0, 0, 0, 0},
    {-1, "L1-dcache-stores",         0, 0, 0, 0},
    /* L1-I */
    {-1, "L1-icache-load-misses",    0, 0, 0, 0},
    /* L1D pending miss (raw) */
    {-1, "l1d.replacement",          0, 0, 0, 0},
    {-1, "l1d_pend_miss.fb_full",    0, 0, 0, 0},
    {-1, "l1d_pend_miss.pending",    0, 0, 0, 0},
    /* LLC */
    {-1, "LLC-loads",                0, 0, 0, 0},
    {-1, "LLC-load-misses",          0, 0, 0, 0},
    {-1, "LLC-stores",               0, 0, 0, 0},
    {-1, "LLC-store-misses",         0, 0, 0, 0},
    {-1, "cache-references",         0, 0, 0, 0},
    {-1, "cache-misses",             0, 0, 0, 0},
    /* Memory instructions */
    {-1, "mem-loads",                0, 0, 0, 0},
    {-1, "mem-stores",               0, 0, 0, 0},
    {-1, "mem_inst_retired.all_loads",  0, 0, 0, 0},
    {-1, "mem_inst_retired.all_stores", 0, 0, 0, 0},
    {-1, "mem_inst_retired.any",        0, 0, 0, 0},
};
#define NUM_COUNTERS (sizeof(counters) / sizeof(counters[0]))

/* ── globals ─────────────────────────────────────────────────────────────── */

static FILE *log_file    = NULL;
static int   timer_fd    = -1;
static long  interval_ms = 1000;
static int   perf_cpu    = -1;  /* -1=follow thread, >=0=specific cpu       */

/* ── helpers ─────────────────────────────────────────────────────────────── */

static int perf_event_open(struct perf_event_attr *hw, pid_t pid,
                           int cpu, int group_fd, unsigned long flags)
{
    return (int)syscall(__NR_perf_event_open, hw, pid, cpu, group_fd, flags);
}

/* Open each event independently (no group) so the kernel can multiplex
 * all events via time-sharing.  Each fd carries its own
 * time_enabled / time_running scaling values. */
static int init_counter(int idx, struct perf_event_attr *pe, pid_t pid)
{
    pe->read_format = PERF_FORMAT_TOTAL_TIME_ENABLED |
                      PERF_FORMAT_TOTAL_TIME_RUNNING;
    int fd = perf_event_open(pe, pid, perf_cpu, -1, 0);
    if (fd < 0) return 0;
    counters[idx].fd      = fd;
    counters[idx].enabled = 1;
    ioctl(fd, PERF_EVENT_IOC_ENABLE, 0);
    return 1;
}

/* Return monotonic time in milliseconds. */
static uint64_t now_ms(void)
{
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (uint64_t)ts.tv_sec * 1000ULL + (uint64_t)(ts.tv_nsec / 1000000LL);
}

/* ── signal handler ──────────────────────────────────────────────────────── */

void cleanup(int sig __attribute__((unused)))
{
    for (size_t i = 0; i < NUM_COUNTERS; i++)
        if (counters[i].fd >= 0)
            close(counters[i].fd);
    if (timer_fd >= 0)
        close(timer_fd);
    if (log_file) {
        fclose(log_file);
        printf("\nLog file closed.\n");
    }
    printf("Exiting.\n");
    exit(0);
}

/* ── main ────────────────────────────────────────────────────────────────── */

int main(int argc, char **argv)
{
    pid_t    target_pid      = -1;
    int      active_counters = 0;
    int      idx             = 0;

    /* ---- parse arguments ---- */
    for (int i = 1; i < argc; i++) {
        if (strcmp(argv[i], "-i") == 0 && i + 1 < argc) {
            interval_ms = atol(argv[++i]);
            if (interval_ms <= 0) {
                fprintf(stderr, "Invalid interval: %ld\n", interval_ms);
                return 1;
            }
        } else {
            /* first non-flag argument is the PID */
            char *end;
            long v = strtol(argv[i], &end, 10);
            if (*end == '\0')
                target_pid = (pid_t)v;
            else {
                fprintf(stderr, "Usage: %s [PID] [-i <interval_ms>]\n", argv[0]);
                return 1;
            }
        }
    }

    signal(SIGINT,  cleanup);
    signal(SIGTERM, cleanup);

    /* ---- open log file ---- */
    mkdir("log", 0755);

    time_t     now = time(NULL);
    struct tm *tm  = localtime(&now);
    char ts_filename[256];
    snprintf(ts_filename, sizeof(ts_filename),
             "log/pmu_timeseries_%04d%02d%02d_%02d%02d%02d.csv",
             tm->tm_year + 1900, tm->tm_mon + 1, tm->tm_mday,
             tm->tm_hour, tm->tm_min, tm->tm_sec);

    log_file = fopen(ts_filename, "w");
    if (!log_file) {
        fprintf(stderr, "Failed to open log file: %s\n", strerror(errno));
        return 1;
    }

    /* symlink "log/pmu_timeseries.csv" → latest file */
    const char *link = "log/pmu_timeseries.csv";
    unlink(link);
    char rel_target[256];
    snprintf(rel_target, sizeof(rel_target),
             "pmu_timeseries_%04d%02d%02d_%02d%02d%02d.csv",
             tm->tm_year + 1900, tm->tm_mon + 1, tm->tm_mday,
             tm->tm_hour, tm->tm_min, tm->tm_sec);
    symlink(rel_target, link);

    setbuf(stdout, NULL);
    setbuf(stderr, NULL);

    /* ---- CSV header ---- */
    /* elapsed_ms: milliseconds elapsed since program start (time-axis column) */
    fprintf(log_file, "elapsed_ms,timestamp");
    for (size_t i = 0; i < NUM_COUNTERS; i++)
        /* 输出每个计数器的值以及其 time_enabled/time_running，便于排查 multiplex */
        fprintf(log_file, ",%s,%s_time_enabled,%s_time_running", counters[i].name, counters[i].name, counters[i].name);
    fprintf(log_file, "\n");
    fflush(log_file);

    /* For per-process monitoring cpu=-1 follows the thread on any core.
     * For system-wide (pid==-1) we must specify an actual CPU; use 0. */
    perf_cpu = (target_pid == -1) ? 0 : -1;

    /* ---- initialise perf counters ---- */
    struct perf_event_attr pe;

#define RESET_PE() do { memset(&pe, 0, sizeof(pe)); pe.size = sizeof(pe); pe.disabled = 1; } while (0)

    /* dTLB */
    RESET_PE(); pe.type = PERF_TYPE_HW_CACHE;
    pe.config = PERF_COUNT_HW_CACHE_DTLB | (PERF_COUNT_HW_CACHE_OP_READ  << 8) | (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);

    pe.config = PERF_COUNT_HW_CACHE_DTLB | (PERF_COUNT_HW_CACHE_OP_READ  << 8) | (PERF_COUNT_HW_CACHE_RESULT_MISS   << 16);
    active_counters += init_counter(idx++, &pe, target_pid);

    pe.config = PERF_COUNT_HW_CACHE_DTLB | (PERF_COUNT_HW_CACHE_OP_WRITE << 8) | (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);

    pe.config = PERF_COUNT_HW_CACHE_DTLB | (PERF_COUNT_HW_CACHE_OP_WRITE << 8) | (PERF_COUNT_HW_CACHE_RESULT_MISS   << 16);
    active_counters += init_counter(idx++, &pe, target_pid);

    /* iTLB */
    pe.config = PERF_COUNT_HW_CACHE_ITLB | (PERF_COUNT_HW_CACHE_OP_READ  << 8) | (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);

    pe.config = PERF_COUNT_HW_CACHE_ITLB | (PERF_COUNT_HW_CACHE_OP_READ  << 8) | (PERF_COUNT_HW_CACHE_RESULT_MISS   << 16);
    active_counters += init_counter(idx++, &pe, target_pid);

    /* L1-D */
    pe.config = PERF_COUNT_HW_CACHE_L1D  | (PERF_COUNT_HW_CACHE_OP_READ  << 8) | (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);

    pe.config = PERF_COUNT_HW_CACHE_L1D  | (PERF_COUNT_HW_CACHE_OP_READ  << 8) | (PERF_COUNT_HW_CACHE_RESULT_MISS   << 16);
    active_counters += init_counter(idx++, &pe, target_pid);

    pe.config = PERF_COUNT_HW_CACHE_L1D  | (PERF_COUNT_HW_CACHE_OP_WRITE << 8) | (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);

    /* L1-I */
    pe.config = PERF_COUNT_HW_CACHE_L1I  | (PERF_COUNT_HW_CACHE_OP_READ  << 8) | (PERF_COUNT_HW_CACHE_RESULT_MISS   << 16);
    active_counters += init_counter(idx++, &pe, target_pid);

    /* L1D pending miss (raw) */
    RESET_PE(); pe.type = PERF_TYPE_RAW;
    pe.config = 0x0151;  /* l1d.replacement       */
    active_counters += init_counter(idx++, &pe, target_pid);

    pe.config = 0x0248;  /* l1d_pend_miss.fb_full  */
    active_counters += init_counter(idx++, &pe, target_pid);

    pe.config = 0x0148;  /* l1d_pend_miss.pending  */
    active_counters += init_counter(idx++, &pe, target_pid);

    /* LLC */
    RESET_PE(); pe.type = PERF_TYPE_HW_CACHE;
    pe.config = PERF_COUNT_HW_CACHE_LL | (PERF_COUNT_HW_CACHE_OP_READ  << 8) | (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);

    pe.config = PERF_COUNT_HW_CACHE_LL | (PERF_COUNT_HW_CACHE_OP_READ  << 8) | (PERF_COUNT_HW_CACHE_RESULT_MISS   << 16);
    active_counters += init_counter(idx++, &pe, target_pid);

    pe.config = PERF_COUNT_HW_CACHE_LL | (PERF_COUNT_HW_CACHE_OP_WRITE << 8) | (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);

    pe.config = PERF_COUNT_HW_CACHE_LL | (PERF_COUNT_HW_CACHE_OP_WRITE << 8) | (PERF_COUNT_HW_CACHE_RESULT_MISS   << 16);
    active_counters += init_counter(idx++, &pe, target_pid);

    /* generic cache */
    RESET_PE(); pe.type = PERF_TYPE_HARDWARE;
    pe.config = PERF_COUNT_HW_CACHE_REFERENCES;
    active_counters += init_counter(idx++, &pe, target_pid);

    pe.config = PERF_COUNT_HW_CACHE_MISSES;
    active_counters += init_counter(idx++, &pe, target_pid);

    /* memory instructions via NODE cache */
    RESET_PE(); pe.type = PERF_TYPE_HW_CACHE;
    pe.config = PERF_COUNT_HW_CACHE_NODE | (PERF_COUNT_HW_CACHE_OP_READ  << 8) | (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);

    pe.config = PERF_COUNT_HW_CACHE_NODE | (PERF_COUNT_HW_CACHE_OP_WRITE << 8) | (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);

    /* raw memory instruction events */
    RESET_PE(); pe.type = PERF_TYPE_RAW;
    pe.config = 0x81D0;  /* mem_inst_retired.all_loads   */
    active_counters += init_counter(idx++, &pe, target_pid);

    pe.config = 0x82D0;  /* mem_inst_retired.all_stores  */
    active_counters += init_counter(idx++, &pe, target_pid);

    pe.config = 0x83D0;  /* mem_inst_retired.any         */
    active_counters += init_counter(idx++, &pe, target_pid);

    if (active_counters == 0) {
        fprintf(stderr, "Error: could not open any performance counters.\n");
        return 1;
    }

    printf("=== PMU Time-Series Sampler ===\n");
    printf("Target PID   : %d%s\n", target_pid,
           target_pid == -1 ? " (system-wide)" : "");
    printf("Interval     : %ld ms\n", interval_ms);
    printf("Log file     : %s\n", ts_filename);
    printf("Symlink      : %s\n\n", link);
    printf("Opened %d/%zu counters:\n", active_counters, NUM_COUNTERS);
    for (size_t i = 0; i < NUM_COUNTERS; i++)
        if (counters[i].enabled)
            printf("  + %s\n", counters[i].name);
        else
            printf("    (unavailable) %s\n", counters[i].name);
    printf("\nMonitoring… (Ctrl+C to stop)\n\n");

    /* ---- set up timerfd (CLOCK_MONOTONIC → no drift) ---- */
    timer_fd = timerfd_create(CLOCK_MONOTONIC, TFD_CLOEXEC);
    if (timer_fd < 0) { perror("timerfd_create"); return 1; }

    struct itimerspec its = {
        .it_interval = { .tv_sec  =  interval_ms / 1000,
                         .tv_nsec = (interval_ms % 1000) * 1000000L },
        .it_value    = { .tv_sec  =  interval_ms / 1000,
                         .tv_nsec = (interval_ms % 1000) * 1000000L },
    };
    if (timerfd_settime(timer_fd, 0, &its, NULL) < 0) {
        perror("timerfd_settime"); return 1;
    }

    /* record start time for elapsed_ms calculation */
    uint64_t start_ms = now_ms();

    /* ── sampling loop ─────────────────────────────────────────────────── */
    while (1) {
        struct pollfd pfd = { .fd = timer_fd, .events = POLLIN };
        if (poll(&pfd, 1, -1) < 0) {
            if (errno == EINTR) continue;
            perror("poll"); break;
        }

        uint64_t expirations = 0;
        read(timer_fd, &expirations, sizeof(expirations));
        if (expirations > 1)
            fprintf(stderr, "Warning: timer overrun — %llu missed tick(s)\n",
                    (unsigned long long)(expirations - 1));

        /* time-axis: elapsed milliseconds */
        uint64_t elapsed = now_ms() - start_ms;

        /* wall-clock timestamp for human readability */
        struct timespec wall;
        clock_gettime(CLOCK_REALTIME, &wall);
        char wall_str[32];
        strftime(wall_str, sizeof(wall_str), "%Y-%m-%d %H:%M:%S",
                 localtime(&wall.tv_sec));
        snprintf(wall_str + strlen(wall_str),
                 sizeof(wall_str) - strlen(wall_str),
                 ".%03ld", wall.tv_nsec / 1000000L);

        /* Read each counter independently.  Each fd has its own
         * time_enabled/time_running pair for accurate scaling when the
         * kernel multiplexes more events than the hardware can hold at once. */
        struct { uint64_t value; uint64_t time_enabled; uint64_t time_running; } cnt;
        for (size_t i = 0; i < NUM_COUNTERS; i++) {
            if (!counters[i].enabled) { counters[i].count = 0; continue; }
                if (read(counters[i].fd, &cnt, sizeof(cnt)) != sizeof(cnt)) {
                    counters[i].count = 0;
                    counters[i].time_enabled = 0;
                    counters[i].time_running = 0;
                    continue;
                }
                counters[i].time_enabled = cnt.time_enabled;
                counters[i].time_running = cnt.time_running;
                double scale = (cnt.time_running > 0 && cnt.time_running < cnt.time_enabled)
                               ? (double)cnt.time_enabled / cnt.time_running : 1.0;
                counters[i].count = (uint64_t)(cnt.value * scale);
                /* 当 time_running 非常小时记录警告，阈值以采样间隔的一部分为准 */
                uint64_t min_tr = (uint64_t)interval_ms * 1000000ULL / 10ULL; /* 10% 的采样周期 */
                if (cnt.time_running == 0 || cnt.time_running < min_tr) {
                    fprintf(stderr, "Warning: small time_running for %s: %llu (time_enabled=%llu)\n",
                            counters[i].name,
                            (unsigned long long)cnt.time_running,
                            (unsigned long long)cnt.time_enabled);
                }
        }

        /* Reset every counter individually */
        for (size_t i = 0; i < NUM_COUNTERS; i++)
            if (counters[i].enabled)
                ioctl(counters[i].fd, PERF_EVENT_IOC_RESET, 0);

        /* ---- write CSV row ---- */
        fprintf(log_file, "%llu,%s",
                (unsigned long long)elapsed, wall_str);
        for (size_t i = 0; i < NUM_COUNTERS; i++) {
            if (counters[i].enabled) {
                fprintf(log_file, ",%llu,%llu,%llu",
                        (unsigned long long)counters[i].count,
                        (unsigned long long)counters[i].time_enabled,
                        (unsigned long long)counters[i].time_running);
            } else {
                /* 三列占位：value,time_enabled,time_running */
                fprintf(log_file, ",,,");
            }
        }
        fprintf(log_file, "\n");
        fflush(log_file);

        /* ---- terminal summary ---- */
        printf("t=%llums  %s\n",
               (unsigned long long)elapsed, wall_str);
        printf("  TLB:\n");
        for (size_t i = 0; i < 6; i++)
            if (counters[i].enabled)
                printf("    %-35s %12llu\n", counters[i].name,
                       (unsigned long long)counters[i].count);
        fflush(stdout);
        printf("  Cache:\n");
        for (size_t i = 6; i < 19; i++)
            if (counters[i].enabled)
                printf("    %-35s %12llu\n", counters[i].name,
                       (unsigned long long)counters[i].count);
        fflush(stdout);
        printf("  Memory instructions:\n");
        for (size_t i = 19; i < NUM_COUNTERS; i++)
            if (counters[i].enabled)
                printf("    %-35s %12llu\n", counters[i].name,
                       (unsigned long long)counters[i].count);
        fflush(stdout);
    }

    return 0;
}
