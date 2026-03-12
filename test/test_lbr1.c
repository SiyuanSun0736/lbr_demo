#define _POSIX_C_SOURCE 199309L
#include <stdio.h>
#include <stdlib.h>
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

// 深度调用链（递归），用于产生更深的调用栈，避免使用 rand
void deep_call(int level) {
    volatile int marker = level; /* 防止被编译器优化掉 */
    if (level > 1) {
        deep_call(level - 1);
    }
    /* 在返回后做少量工作，确保不是尾递归 */
    marker += level;
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


    /* 允许通过 Ctrl-C 或 SIGTERM 停止程序并优雅退出 */
    signal(SIGINT, handle_signal);
    signal(SIGTERM, handle_signal);

    /* 循环执行多轮测试，增加负载（无限循环，直到收到停止信号） */
    int round = 0;
    while (!stop_flag) {

        // /* 测试3: 递归调用 - 增加计算次数 */
        // int fib_sum = 0;
        // for (int i = 0; i < 100; i++) {
        //     fib_sum += fibonacci(15);
        // }


        // /* 测试5: 循环和条件 - 更大范围 */
        // int sum_total = 0;
        // for (int j = 0; j < 100; j++) {
        //     sum_total += sum_even_numbers(1000);
        // }


        // /* 测试7: 嵌套循环和条件 */
        // int nested_count = 0;
        // for (int i = 0; i < 1000; i++) {
        //     for (int j = 0; j < 100; j++) {
        //         if ((i + j) % 2 == 0) {
        //             if ((i * j) % 3 == 0) {
        //                 nested_count++;
        //             } else {
        //                 nested_count--;
        //             }
        //         } else {
        //             if ((i - j) % 5 == 0) {
        //                 nested_count += 2;
        //             }
        //         }
        //     }
        // }

        /* 新增：每轮调用更深的调用栈（不使用 rand），随着 round 轻微变化以覆盖不同深度 */
        {
            int base_depth = 60; /* 基础深度，注意不要太大以免栈溢出 */
            int variability = (round % 20); /* 不使用随机数，简单变化 */
            int depth = base_depth + variability; /* 最终深度在 60..79 */
            deep_call(depth);
        }

        round++;
    }
    return 0;
}
