#define _POSIX_C_SOURCE 199309L
#include <stdio.h>
#include <stdlib.h>
#include <time.h>
#include <unistd.h>
#include <signal.h>

volatile sig_atomic_t stop_flag = 0;

void handle_signal(int sig) {
    (void)sig;
    stop_flag = 1;
}

// 函数1: 包含条件分支的排序函数
void bubble_sort(int arr[], int n) {
    for (int i = 0; i < n - 1; i++) {
        for (int j = 0; j < n - i - 1; j++) {
            if (arr[j] > arr[j + 1]) {
                // 交换元素
                int temp = arr[j];
                arr[j] = arr[j + 1];
                arr[j + 1] = temp;
            }
        }
    }
}

// 函数2: 包含多个条件分支
int classify_number(int num) {
    if (num < 0) {
        return -1;  // 负数
    } else if (num == 0) {
        return 0;   // 零
    } else if (num < 100) {
        return 1;   // 小正数
    } else if (num < 1000) {
        return 2;   // 中等正数
    } else {
        return 3;   // 大正数
    }
}

// 函数3: 递归函数（会产生函数调用分支）
int fibonacci(int n) {
    if (n <= 1) {
        return n;
    }
    return fibonacci(n - 1) + fibonacci(n - 2);
}

// 函数4: Switch语句（多路分支）
void process_command(char cmd) {
    (void)cmd; /* 不打印任何信息，仅执行分支逻辑占用 CPU */
}

// 函数5: 循环和条件组合
int sum_even_numbers(int n) {
    int sum = 0;
    for (int i = 0; i <= n; i++) {
        if (i % 2 == 0) {
            sum += i;
        }
    }
    return sum;
}

int main() {
    struct timespec start_ts, end_ts;
    /* 记录开始时间（包含等待 attach 的时间） */
    clock_gettime(CLOCK_MONOTONIC, &start_ts);

    /* 等待 eBPF 监控程序 attach (保留等待，但不打印) */
    sleep(5);

    srand(time(NULL));

    /* 允许通过 Ctrl-C 或 SIGTERM 停止程序并优雅退出 */
    signal(SIGINT, handle_signal);
    signal(SIGTERM, handle_signal);

    /* 循环执行多轮测试，增加负载（无限循环，直到收到停止信号） */
    int round = 0;
    while (!stop_flag) {
        /* 测试1: 排序算法 - 增加数组大小和执行次数 */
        for (int iter = 0; iter < 50; iter++) {
            int arr[100];
            for (int i = 0; i < 100; i++) {
                arr[i] = rand() % 1000;
            }
            bubble_sort(arr, 100);
        }

        /* 测试2: 条件分支 - 大量执行 */
        int classify_count = 0;
        for (int i = 0; i < 10000; i++) {
            int random_num = (rand() % 10000) - 5000;
            classify_count += classify_number(random_num);
        }

        /* 测试3: 递归调用 - 增加计算次数 */
        int fib_sum = 0;
        for (int i = 0; i < 100; i++) {
            fib_sum += fibonacci(15);
        }

        /* 测试4: Switch分支 - 大量执行（不打印） */
        char all_cmds[] = {'a', 'd', 'u', 'q', 'x', 'y', 'z'};
        for (int i = 0; i < 5000; i++) {
            char cmd = all_cmds[rand() % 7];
            process_command(cmd);
        }

        /* 测试5: 循环和条件 - 更大范围 */
        int sum_total = 0;
        for (int j = 0; j < 100; j++) {
            sum_total += sum_even_numbers(1000);
        }

        /* 测试6: 密集的分支操作 - 大幅增加 */
        int branch_count = 0;
        for (int i = 0; i < 100000; i++) {
            int random_num = rand() % 100;
            if (random_num < 10) {
                branch_count += 1;
            } else if (random_num < 20) {
                branch_count += 2;
            } else if (random_num < 30) {
                branch_count += 3;
            } else if (random_num < 40) {
                branch_count += 4;
            } else if (random_num < 50) {
                branch_count += 5;
            } else if (random_num < 60) {
                branch_count += 6;
            } else if (random_num < 70) {
                branch_count += 7;
            } else if (random_num < 80) {
                branch_count += 8;
            } else if (random_num < 90) {
                branch_count += 9;
            } else {
                branch_count += 10;
            }
        }

        /* 测试7: 嵌套循环和条件 */
        int nested_count = 0;
        for (int i = 0; i < 1000; i++) {
            for (int j = 0; j < 100; j++) {
                if ((i + j) % 2 == 0) {
                    if ((i * j) % 3 == 0) {
                        nested_count++;
                    } else {
                        nested_count--;
                    }
                } else {
                    if ((i - j) % 5 == 0) {
                        nested_count += 2;
                    }
                }
            }
        }

        round++;
    }

    /* 记录结束时间并打印总耗时（仅此一次） */
    clock_gettime(CLOCK_MONOTONIC, &end_ts);
    double elapsed = (end_ts.tv_sec - start_ts.tv_sec) +
                     (end_ts.tv_nsec - start_ts.tv_nsec) / 1e9;
    printf("总执行时间: %.3f 秒\n", elapsed);
    fflush(stdout);
    return 0;
}
