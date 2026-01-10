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

static int fd_mem_loads = -1;
static int fd_mem_stores = -1;
static int fd_all_loads = -1;
static int fd_all_stores = -1;
static int fd_any = -1;

void cleanup(int sig __attribute__((unused))) {
    if (fd_mem_loads >= 0) close(fd_mem_loads);
    if (fd_mem_stores >= 0) close(fd_mem_stores);
    if (fd_all_loads >= 0) close(fd_all_loads);
    if (fd_all_stores >= 0) close(fd_all_stores);
    if (fd_any >= 0) close(fd_any);
    printf("\nCleaning up...\n");
    exit(0);
}

int main(int argc, char **argv) {
    struct perf_event_attr pe_mem_loads, pe_mem_stores, pe_all_loads, pe_all_stores, pe_any;
    uint64_t count_mem_loads = 0, count_mem_stores = 0, count_all_loads = 0, count_all_stores = 0, count_any = 0;
    pid_t target_pid = -1;
    int active_counters = 0;
    
    if (argc > 1) {
        target_pid = atoi(argv[1]);
    }
    
    signal(SIGINT, cleanup);
    signal(SIGTERM, cleanup);
    
    // Ensure output is not buffered
    setbuf(stdout, NULL);
    setbuf(stderr, NULL);
    
    printf("Memory Instructions Retired Monitoring\n");
    printf("Target PID: %d\n", target_pid == -1 ? getpid() : target_pid);
    printf("Opening performance counters...\n\n");
    fflush(stdout);
    
    // Try mem-loads (hardware cache event)
    memset(&pe_mem_loads, 0, sizeof(struct perf_event_attr));
    pe_mem_loads.type = PERF_TYPE_HW_CACHE;
    pe_mem_loads.config = (PERF_COUNT_HW_CACHE_NODE) | 
                          (PERF_COUNT_HW_CACHE_OP_READ << 8) |
                          (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    pe_mem_loads.size = sizeof(struct perf_event_attr);
    pe_mem_loads.disabled = 1;
    pe_mem_loads.exclude_kernel = 0;
    pe_mem_loads.exclude_hv = 0;
    
    fd_mem_loads = try_open_perf_event(&pe_mem_loads, target_pid, "mem-loads");
    if (fd_mem_loads >= 0) active_counters++;
    
    // Try mem-stores (hardware cache event)
    memset(&pe_mem_stores, 0, sizeof(struct perf_event_attr));
    pe_mem_stores.type = PERF_TYPE_HW_CACHE;
    pe_mem_stores.config = (PERF_COUNT_HW_CACHE_NODE) | 
                           (PERF_COUNT_HW_CACHE_OP_WRITE << 8) |
                           (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    pe_mem_stores.size = sizeof(struct perf_event_attr);
    pe_mem_stores.disabled = 1;
    pe_mem_stores.exclude_kernel = 0;
    pe_mem_stores.exclude_hv = 0;
    
    fd_mem_stores = try_open_perf_event(&pe_mem_stores, target_pid, "mem-stores");
    if (fd_mem_stores >= 0) active_counters++;
    
    // Try mem_inst_retired.all_loads (raw PMU event)
    // Intel event code: 0xD0, umask: 0x81
    memset(&pe_all_loads, 0, sizeof(struct perf_event_attr));
    pe_all_loads.type = PERF_TYPE_RAW;
    pe_all_loads.config = 0x81D0;  // umask=0x81, event=0xD0
    pe_all_loads.size = sizeof(struct perf_event_attr);
    pe_all_loads.disabled = 1;
    pe_all_loads.exclude_kernel = 0;
    pe_all_loads.exclude_hv = 0;
    
    fd_all_loads = try_open_perf_event(&pe_all_loads, target_pid, "mem_inst_retired.all_loads");
    if (fd_all_loads >= 0) active_counters++;
    
    // Try mem_inst_retired.all_stores (raw PMU event)
    // Intel event code: 0xD0, umask: 0x82
    memset(&pe_all_stores, 0, sizeof(struct perf_event_attr));
    pe_all_stores.type = PERF_TYPE_RAW;
    pe_all_stores.config = 0x82D0;  // umask=0x82, event=0xD0
    pe_all_stores.size = sizeof(struct perf_event_attr);
    pe_all_stores.disabled = 1;
    pe_all_stores.exclude_kernel = 0;
    pe_all_stores.exclude_hv = 0;
    
    fd_all_stores = try_open_perf_event(&pe_all_stores, target_pid, "mem_inst_retired.all_stores");
    if (fd_all_stores >= 0) active_counters++;
    
    // Try mem_inst_retired.any (raw PMU event)
    // Intel event code: 0xD0, umask: 0x83
    memset(&pe_any, 0, sizeof(struct perf_event_attr));
    pe_any.type = PERF_TYPE_RAW;
    pe_any.config = 0x83D0;  // umask=0x83, event=0xD0
    pe_any.size = sizeof(struct perf_event_attr);
    pe_any.disabled = 1;
    pe_any.exclude_kernel = 0;
    pe_any.exclude_hv = 0;
    
    fd_any = try_open_perf_event(&pe_any, target_pid, "mem_inst_retired.any");
    if (fd_any >= 0) active_counters++;
    
    if (active_counters == 0) {
        fprintf(stderr, "\nError: Could not open any performance counters.\n");
        fprintf(stderr, "Your hardware may not support these memory events.\n");
        fprintf(stderr, "Try running 'perf list' to see available events.\n");
        fprintf(stderr, "Note: mem_inst_retired events require Intel CPUs with specific PMU support.\n");
        return 1;
    }
    
    printf("\nSuccessfully opened %d/%d counters\n", active_counters, 5);
    
    // Enable all opened counters
    if (fd_mem_loads >= 0) ioctl(fd_mem_loads, PERF_EVENT_IOC_ENABLE, 0);
    if (fd_mem_stores >= 0) ioctl(fd_mem_stores, PERF_EVENT_IOC_ENABLE, 0);
    if (fd_all_loads >= 0) ioctl(fd_all_loads, PERF_EVENT_IOC_ENABLE, 0);
    if (fd_all_stores >= 0) ioctl(fd_all_stores, PERF_EVENT_IOC_ENABLE, 0);
    if (fd_any >= 0) ioctl(fd_any, PERF_EVENT_IOC_ENABLE, 0);
    
    printf("Monitoring started. Press Ctrl+C to stop\n\n");
    
    // Read and display counters periodically
    while (1) {
        sleep(1);
        
        if (fd_mem_loads >= 0) {
            read(fd_mem_loads, &count_mem_loads, sizeof(uint64_t));
        }
        if (fd_mem_stores >= 0) {
            read(fd_mem_stores, &count_mem_stores, sizeof(uint64_t));
        }
        if (fd_all_loads >= 0) {
            read(fd_all_loads, &count_all_loads, sizeof(uint64_t));
        }
        if (fd_all_stores >= 0) {
            read(fd_all_stores, &count_all_stores, sizeof(uint64_t));
        }
        if (fd_any >= 0) {
            read(fd_any, &count_any, sizeof(uint64_t));
        }
        
        // Clear previous lines and move cursor up
        printf("\033[2K\r");  // Clear current line
        printf("\033[1A\033[2K");  // Move up and clear line
        printf("\033[1A\033[2K");  // Move up and clear line
        
        // Print first line
        if (fd_mem_loads >= 0) printf("mem-loads: %-15lu | ", count_mem_loads);
        else printf("mem-loads: N/A             | ");
        
        if (fd_mem_stores >= 0) printf("mem-stores: %-15lu\n", count_mem_stores);
        else printf("mem-stores: N/A             \n");
        
        // Print second line
        if (fd_all_loads >= 0) printf("all_loads: %-15lu | ", count_all_loads);
        else printf("all_loads: N/A             | ");
        
        if (fd_all_stores >= 0) printf("all_stores: %-15lu\n", count_all_stores);
        else printf("all_stores: N/A             \n");
        
        // Print third line
        if (fd_any >= 0) printf("any: %-15lu", count_any);
        else printf("any: N/A             ");
        
        fflush(stdout);
    }
    
    return 0;
}
