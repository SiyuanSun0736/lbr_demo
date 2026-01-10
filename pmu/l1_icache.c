#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <fcntl.h>
#include <sys/ioctl.h>
#include <sys/mman.h>
#include <linux/perf_event.h>
#include <asm/unistd.h>
#include <signal.h>
#include <stdint.h>
#include <errno.h>

static int perf_event_open(struct perf_event_attr *hw_event, pid_t pid,
                           int cpu, int group_fd, unsigned long flags) {
    int ret = syscall(__NR_perf_event_open, hw_event, pid, cpu, group_fd, flags);
    return ret;
}

static int try_open_perf_event(struct perf_event_attr *pe, pid_t pid, const char *name) {
    int fd = perf_event_open(pe, pid, -1, -1, 0);
    if (fd == -1) {
        fprintf(stderr, "Failed to open %s (pid=%d, cpu=-1): %s (errno=%d)\n", 
                name, pid, strerror(errno), errno);
        
        // Try with CPU 0 instead
        fd = perf_event_open(pe, pid, 0, -1, 0);
        if (fd == -1) {
            fprintf(stderr, "Failed to open %s (pid=%d, cpu=0): %s (errno=%d)\n", 
                    name, pid, strerror(errno), errno);
            return -1;
        }
        printf("Successfully opened %s on CPU 0 (fd=%d)\n", name, fd);
        return fd;
    }
    printf("Successfully opened %s on all CPUs (fd=%d)\n", name, fd);
    return fd;
}

static int fd_loads = -1;
static int fd_load_misses = -1;

void cleanup(int sig __attribute__((unused))) {
    if (fd_loads >= 0) close(fd_loads);
    if (fd_load_misses >= 0) close(fd_load_misses);
    printf("\nCleaning up...\n");
    exit(0);
}

int main(int argc, char **argv) {
    struct perf_event_attr pe_loads, pe_load_misses;
    uint64_t count_loads = 0, count_load_misses = 0;
    pid_t target_pid;
    int active_counters = 0;
    
    if (argc > 1) {
        target_pid = atoi(argv[1]);
    } else {
        target_pid = -1;
    }
    
    signal(SIGINT, cleanup);
    signal(SIGTERM, cleanup);
    
    printf("L1 Instruction Cache Monitoring\n");
    printf("Target PID: %d\n", target_pid == -1 ? getpid() : target_pid);
    printf("Opening performance counters...\n\n");
    
    // Try L1-icache-loads
    memset(&pe_loads, 0, sizeof(struct perf_event_attr));
    pe_loads.type = PERF_TYPE_HW_CACHE;
    pe_loads.config = (PERF_COUNT_HW_CACHE_L1I) | 
                      (PERF_COUNT_HW_CACHE_OP_READ << 8) |
                      (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    pe_loads.size = sizeof(struct perf_event_attr);
    pe_loads.disabled = 1;
    
    fd_loads = try_open_perf_event(&pe_loads, target_pid, "L1-icache-loads");
    if (fd_loads >= 0) active_counters++;
    
    // Try L1-icache-load-misses
    memset(&pe_load_misses, 0, sizeof(struct perf_event_attr));
    pe_load_misses.type = PERF_TYPE_HW_CACHE;
    pe_load_misses.config = (PERF_COUNT_HW_CACHE_L1I) | 
                            (PERF_COUNT_HW_CACHE_OP_READ << 8) |
                            (PERF_COUNT_HW_CACHE_RESULT_MISS << 16);
    pe_load_misses.size = sizeof(struct perf_event_attr);
    pe_load_misses.disabled = 1;
    
    fd_load_misses = try_open_perf_event(&pe_load_misses, target_pid, "L1-icache-load-misses");
    if (fd_load_misses >= 0) active_counters++;
    
    if (active_counters == 0) {
        fprintf(stderr, "\nError: Could not open any performance counters.\n");
        fprintf(stderr, "Your hardware may not support L1 instruction cache events.\n");
        fprintf(stderr, "Try running 'perf list cache' to see available cache events.\n");
        return 1;
    }
    
    printf("\nSuccessfully opened %d/%d counters\n", active_counters, 2);
    if (fd_loads < 0) {
        printf("Note: L1-icache-loads not supported on this hardware, showing misses only\n");
    }
    
    // Enable all opened counters
    if (fd_loads >= 0) ioctl(fd_loads, PERF_EVENT_IOC_ENABLE, 0);
    if (fd_load_misses >= 0) ioctl(fd_load_misses, PERF_EVENT_IOC_ENABLE, 0);
    // Read and display counters periodically
    uint64_t prev_load_misses = 0;
    while (1) {
        sleep(1);
        
        if (fd_loads >= 0) {
            read(fd_loads, &count_loads, sizeof(uint64_t));
        }
        if (fd_load_misses >= 0) {
            read(fd_load_misses, &count_load_misses, sizeof(uint64_t));
        }
        
        uint64_t delta_misses = count_load_misses - prev_load_misses;
        prev_load_misses = count_load_misses;
        
        printf("\r");
        if (fd_loads >= 0) {
            printf("L1-icache-loads: %-12lu | ", count_loads);
        }
        
        if (fd_load_misses >= 0) {
            printf("L1-icache-load-misses: %-12lu (Δ: %-10lu/s)", 
                   count_load_misses, delta_misses);
            if (fd_loads >= 0 && count_loads > 0) {
                double miss_rate = (double)count_load_misses / count_loads * 100.0;
                printf(" | Miss rate: %.2f%%", miss_rate);
            }
        } else {
            printf("L1-icache-load-misses: N/A");
        }
        
        fflush(stdout);
    }
    
    return 0;
}
