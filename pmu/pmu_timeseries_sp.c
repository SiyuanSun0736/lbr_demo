/*
 * pmu_timeseries_sp.c — PMU time-series sampler (sample-period triggered)
 *
 * Identical in output format to pmu_timeseries.c, but the sampling cadence
 * is driven by hardware counter overflow rather than a fixed timerfd interval.
 * A dedicated CPU-cycles counter fires after every <sample_period> cycles; on
 * each overflow all data counters are read, reset, and appended to the CSV.
 *
 * Because the trigger is event-driven the effective sampling rate adapts to
 * the workload naturally: a heavily loaded CPU fires more frequently than an
 * idle one for the same sample_period value.
 *
 * An mmap ring-buffer (1 metadata page + 1 data page) is attached to the
 * trigger fd so that overflow notifications are delivered via poll(2) POLLIN.
 * Sample records written to the ring-buffer are discarded immediately (we
 * only need the notification, not the individual sample payloads).
 *
 * Usage:
 *   sudo ./pmu_timeseries_sp [PID] [-p <sample_period>]
 *
 *   PID              – target process (-1 = system-wide, default)
 *   -p <period>      – CPU-cycles between trigger overflows
 *                      (default: 1 000 000 000 ≈ 0.3 – 1 s depending on freq)
 *
 * Examples:
 *   sudo ./pmu_timeseries_sp                        # system-wide
 *   sudo ./pmu_timeseries_sp 12345                  # pid 12345
 *   sudo ./pmu_timeseries_sp 12345 -p 500000000     # 500 M cycles per sample
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
#include <sys/mman.h>
#include <poll.h>

/* ── counter descriptors ─────────────────────────────────────────────────── */

typedef struct {
    int         fd;
    const char *name;
    uint64_t    count;      /* value accumulated over the most recent interval */
    int         enabled;    /* 1 if the fd was opened successfully              */
} perf_counter_t;

static perf_counter_t counters[] = {
    /* dTLB */
    {-1, "dTLB-loads",               0, 0},
    {-1, "dTLB-load-misses",         0, 0},
    {-1, "dTLB-stores",              0, 0},
    {-1, "dTLB-store-misses",        0, 0},
    /* iTLB */
    {-1, "iTLB-loads",               0, 0},
    {-1, "iTLB-load-misses",         0, 0},
    /* L1-D */
    {-1, "L1-dcache-loads",          0, 0},
    {-1, "L1-dcache-load-misses",    0, 0},
    {-1, "L1-dcache-stores",         0, 0},
    /* L1-I */
    {-1, "L1-icache-load-misses",    0, 0},
    /* L1D pending miss (raw) */
    {-1, "l1d.replacement",          0, 0},
    {-1, "l1d_pend_miss.fb_full",    0, 0},
    {-1, "l1d_pend_miss.pending",    0, 0},
    /* LLC */
    {-1, "LLC-loads",                0, 0},
    {-1, "LLC-load-misses",          0, 0},
    {-1, "LLC-stores",               0, 0},
    {-1, "LLC-store-misses",         0, 0},
    {-1, "cache-references",         0, 0},
    {-1, "cache-misses",             0, 0},
    /* Memory instructions */
    {-1, "mem-loads",                0, 0},
    {-1, "mem-stores",               0, 0},
    {-1, "mem_inst_retired.all_loads",  0, 0},
    {-1, "mem_inst_retired.all_stores", 0, 0},
    {-1, "mem_inst_retired.any",        0, 0},
};
#define NUM_COUNTERS (sizeof(counters) / sizeof(counters[0]))

/* ── globals ─────────────────────────────────────────────────────────────── */

static FILE  *log_file   = NULL;
static int    trigger_fd = -1;
static void  *mmap_buf   = NULL;
static size_t mmap_sz    = 0;

/* ── helpers ─────────────────────────────────────────────────────────────── */

static int perf_event_open(struct perf_event_attr *hw, pid_t pid,
                           int cpu, int group_fd, unsigned long flags)
{
    return (int)syscall(__NR_perf_event_open, hw, pid, cpu, group_fd, flags);
}

/* Try system-wide (cpu=-1) first, fall back to cpu=0. */
static int try_open(struct perf_event_attr *pe, pid_t pid)
{
    int fd = perf_event_open(pe, pid, -1, -1, 0);
    if (fd < 0)
        fd = perf_event_open(pe, pid, 0, -1, 0);
    return fd;
}

static int init_counter(int idx, struct perf_event_attr *pe, pid_t pid)
{
    counters[idx].fd = try_open(pe, pid);
    if (counters[idx].fd >= 0) {
        counters[idx].enabled = 1;
        ioctl(counters[idx].fd, PERF_EVENT_IOC_ENABLE, 0);
        return 1;
    }
    return 0;
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
    if (trigger_fd >= 0) {
        if (mmap_buf && mmap_buf != MAP_FAILED)
            munmap(mmap_buf, mmap_sz);
        close(trigger_fd);
    }
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
    pid_t    target_pid    = -1;
    uint64_t sample_period = 1000000000ULL;  /* 1e9 CPU cycles ≈ 0.3–1 s */
    int      active_counters = 0;
    int      idx             = 0;

    /* ---- parse arguments ---- */
    for (int i = 1; i < argc; i++) {
        if (strcmp(argv[i], "-p") == 0 && i + 1 < argc) {
            char *end;
            long long v = strtoll(argv[++i], &end, 10);
            if (*end != '\0' || v <= 0) {
                fprintf(stderr, "Invalid sample_period: %s\n", argv[i]);
                return 1;
            }
            sample_period = (uint64_t)v;
        } else {
            char *end;
            long v = strtol(argv[i], &end, 10);
            if (*end == '\0')
                target_pid = (pid_t)v;
            else {
                fprintf(stderr, "Usage: %s [PID] [-p <sample_period>]\n", argv[0]);
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
    fprintf(log_file, "elapsed_ms,timestamp");
    for (size_t i = 0; i < NUM_COUNTERS; i++)
        fprintf(log_file, ",%s", counters[i].name);
    fprintf(log_file, "\n");
    fflush(log_file);

    /* ---- initialise data counters (identical to pmu_timeseries.c) ---- */
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

    /* ---- set up trigger counter (CPU cycles, sample_period overflow) ---- */
    /*
     * sample_period: overflow after every N CPU cycles → kernel writes a
     *   PERF_RECORD_SAMPLE to the ring-buffer and raises POLLIN on the fd.
     * wakeup_events = 1: wake up (deliver POLLIN) after every 1 record.
     * An mmap of (1 meta page + 1 data page) is required; without a ring-
     * buffer attached, perf_poll() never raises POLLIN for overflow events.
     */
    struct perf_event_attr tpe;
    memset(&tpe, 0, sizeof(tpe));
    tpe.size          = sizeof(tpe);
    tpe.type          = PERF_TYPE_HARDWARE;
    tpe.config        = PERF_COUNT_HW_CPU_CYCLES;
    tpe.sample_period = sample_period;
    tpe.wakeup_events = 1;

    trigger_fd = try_open(&tpe, target_pid);
    if (trigger_fd < 0) {
        fprintf(stderr,
                "Error: could not open trigger counter (CPU cycles): %s\n",
                strerror(errno));
        return 1;
    }

    /* mmap: metadata page (index 0) + 1 data page for ring-buffer */
    long   page_sz = sysconf(_SC_PAGESIZE);
    mmap_sz = (size_t)(2 * page_sz);
    mmap_buf = mmap(NULL, mmap_sz, PROT_READ | PROT_WRITE,
                    MAP_SHARED, trigger_fd, 0);
    if (mmap_buf == MAP_FAILED) {
        fprintf(stderr, "Error: mmap failed: %s\n", strerror(errno));
        close(trigger_fd);
        trigger_fd = -1;
        return 1;
    }

    ioctl(trigger_fd, PERF_EVENT_IOC_ENABLE, 0);

    printf("=== PMU Time-Series Sampler (sample-period triggered) ===\n");
    printf("Target PID    : %d%s\n", target_pid,
           target_pid == -1 ? " (system-wide)" : "");
    printf("Sample period : %llu CPU cycles\n",
           (unsigned long long)sample_period);
    printf("Log file      : %s\n", ts_filename);
    printf("Symlink       : %s\n\n", link);
    printf("Opened %d/%zu data counters:\n", active_counters, NUM_COUNTERS);
    for (size_t i = 0; i < NUM_COUNTERS; i++)
        if (counters[i].enabled)
            printf("  + %s\n", counters[i].name);
        else
            printf("    (unavailable) %s\n", counters[i].name);
    printf("\nMonitoring… (Ctrl+C to stop)\n\n");

    /* record start time for elapsed_ms */
    uint64_t start_ms = now_ms();

    /* ── sampling loop ─────────────────────────────────────────────────── */
    while (1) {
        struct pollfd pfd = { .fd = trigger_fd, .events = POLLIN };
        if (poll(&pfd, 1, -1) < 0) {
            if (errno == EINTR) continue;
            perror("poll"); break;
        }

        /*
         * Consume all pending sample records by advancing the ring-buffer
         * tail to match the head.  We discard the record payloads; we only
         * needed the overflow notification.  The ACQUIRE/RELEASE ordering
         * matches the requirement in tools/include/perf/mmap.h.
         */
        volatile struct perf_event_mmap_page *meta =
            (volatile struct perf_event_mmap_page *)mmap_buf;
        uint64_t head = __atomic_load_n(&meta->data_head, __ATOMIC_ACQUIRE);
        __atomic_store_n(&meta->data_tail, head, __ATOMIC_RELEASE);

        /* reset trigger counter so it fires again after sample_period cycles */
        ioctl(trigger_fd, PERF_EVENT_IOC_RESET, 0);

        /* time-axis: elapsed milliseconds since start */
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

        /* read and immediately reset each data counter */
        for (size_t i = 0; i < NUM_COUNTERS; i++) {
            if (counters[i].enabled) {
                read(counters[i].fd, &counters[i].count, sizeof(uint64_t));
                ioctl(counters[i].fd, PERF_EVENT_IOC_RESET, 0);
            }
        }

        /* ---- write CSV row ---- */
        fprintf(log_file, "%llu,%s",
                (unsigned long long)elapsed, wall_str);
        for (size_t i = 0; i < NUM_COUNTERS; i++) {
            if (counters[i].enabled)
                fprintf(log_file, ",%llu",
                        (unsigned long long)counters[i].count);
            else
                fprintf(log_file, ",");
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
        printf("  Cache:\n");
        for (size_t i = 6; i < 19; i++)
            if (counters[i].enabled)
                printf("    %-35s %12llu\n", counters[i].name,
                       (unsigned long long)counters[i].count);
        printf("  Memory instructions:\n");
        for (size_t i = 19; i < NUM_COUNTERS; i++)
            if (counters[i].enabled)
                printf("    %-35s %12llu\n", counters[i].name,
                       (unsigned long long)counters[i].count);
        fflush(stdout);
    }

    cleanup(0);
    return 0;
}
