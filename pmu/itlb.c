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
#include <time.h>
#include <sys/stat.h>

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
static FILE *log_file = NULL;

void cleanup(int sig __attribute__((unused))) {
    if (fd_loads >= 0) close(fd_loads);
    if (fd_load_misses >= 0) close(fd_load_misses);
    if (log_file) {
        fclose(log_file);
        printf("\nLog file closed.\n");
    }
    printf("\nCleaning up...\n");
    exit(0);
}

int main(int argc, char **argv) {
    struct perf_event_attr pe_loads, pe_load_misses;
    uint64_t count_loads = 0, count_load_misses = 0;
    pid_t target_pid = -1;
    int active_counters = 0;
    
    if (argc > 1) {
        target_pid = atoi(argv[1]);
    }
    
    signal(SIGINT, cleanup);
    signal(SIGTERM, cleanup);
    
    // Create log directory if it doesn't exist
    mkdir("log", 0755);
    
    // Generate timestamped filename
    time_t now = time(NULL);
    struct tm *t = localtime(&now);
    char timestamped_filename[256];
    char link_name[] = "log/itlb_monitor.log";
    snprintf(timestamped_filename, sizeof(timestamped_filename), 
             "log/itlb_monitor_%04d%02d%02d_%02d%02d%02d.log",
             t->tm_year + 1900, t->tm_mon + 1, t->tm_mday,
             t->tm_hour, t->tm_min, t->tm_sec);
    
    // Open timestamped log file
    log_file = fopen(timestamped_filename, "w");
    if (!log_file) {
        fprintf(stderr, "Failed to open log file: %s\n", strerror(errno));
        return 1;
    }
    
    // Write header to log file
    fprintf(log_file, "Timestamp,iTLB-loads,iTLB-load-misses\n");
    fflush(log_file);
    
    // Remove old symlink and create new one
    unlink(link_name);
    char target[256];
    snprintf(target, sizeof(target), "itlb_monitor_%04d%02d%02d_%02d%02d%02d.log",
             t->tm_year + 1900, t->tm_mon + 1, t->tm_mday,
             t->tm_hour, t->tm_min, t->tm_sec);
    symlink(target, link_name);
    
    // Ensure output is not buffered
    setbuf(stdout, NULL);
    setbuf(stderr, NULL);
    
    printf("iTLB (Instruction Translation Lookaside Buffer) Monitoring\n");
    printf("Target PID: %d\n", target_pid == -1 ? getpid() : target_pid);
    printf("Log files:\n");
    printf("  - %s (timestamped)\n", timestamped_filename);
    printf("  - %s (symlink to latest)\n", link_name);
    printf("Opening performance counters...\n\n");
    fflush(stdout);
    
    // Try iTLB-loads
    memset(&pe_loads, 0, sizeof(struct perf_event_attr));
    pe_loads.type = PERF_TYPE_HW_CACHE;
    pe_loads.config = (PERF_COUNT_HW_CACHE_ITLB) | 
                      (PERF_COUNT_HW_CACHE_OP_READ << 8) |
                      (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    pe_loads.size = sizeof(struct perf_event_attr);
    pe_loads.disabled = 1;
    
    fd_loads = try_open_perf_event(&pe_loads, target_pid, "iTLB-loads");
    if (fd_loads >= 0) active_counters++;
    
    // Try iTLB-load-misses
    memset(&pe_load_misses, 0, sizeof(struct perf_event_attr));
    pe_load_misses.type = PERF_TYPE_HW_CACHE;
    pe_load_misses.config = (PERF_COUNT_HW_CACHE_ITLB) | 
                            (PERF_COUNT_HW_CACHE_OP_READ << 8) |
                            (PERF_COUNT_HW_CACHE_RESULT_MISS << 16);
    pe_load_misses.size = sizeof(struct perf_event_attr);
    pe_load_misses.disabled = 1;
    
    fd_load_misses = try_open_perf_event(&pe_load_misses, target_pid, "iTLB-load-misses");
    if (fd_load_misses >= 0) active_counters++;
    
    if (active_counters == 0) {
        fprintf(stderr, "\nError: Could not open any performance counters.\n");
        fprintf(stderr, "Your hardware may not support iTLB events.\n");
        fprintf(stderr, "Try running 'perf list cache' to see available cache events.\n");
        fclose(log_file);
        return 1;
    }
    
    printf("\nSuccessfully opened %d/%d counters\n", active_counters, 2);
    
    // Enable all opened counters
    if (fd_loads >= 0) ioctl(fd_loads, PERF_EVENT_IOC_ENABLE, 0);
    if (fd_load_misses >= 0) ioctl(fd_load_misses, PERF_EVENT_IOC_ENABLE, 0);
    
    printf("Monitoring started. Press Ctrl+C to stop\n\n");
    
    // Read and display counters periodically
    while (1) {
        sleep(1);
        
        if (fd_loads >= 0) {
            read(fd_loads, &count_loads, sizeof(uint64_t));
            ioctl(fd_loads, PERF_EVENT_IOC_RESET, 0);
        }
        if (fd_load_misses >= 0) {
            read(fd_load_misses, &count_load_misses, sizeof(uint64_t));
            ioctl(fd_load_misses, PERF_EVENT_IOC_RESET, 0);
        }
        
        // Get current timestamp
        time_t now = time(NULL);
        char timestamp[64];
        strftime(timestamp, sizeof(timestamp), "%Y-%m-%d %H:%M:%S", localtime(&now));
        
        // Write to log file
        fprintf(log_file, "%s,%lu,%lu\n", timestamp, count_loads, count_load_misses);
        fflush(log_file);
        
        // Display on screen
        printf("\r");
        if (fd_loads >= 0) printf("iTLB-loads: %-12lu | ", count_loads);
        else printf("iTLB-loads: N/A          | ");
        
        if (fd_load_misses >= 0) printf("iTLB-load-misses: %-12lu", count_load_misses);
        else printf("iTLB-load-misses: N/A         ");
        
        fflush(stdout);
    }
    
    return 0;
}
