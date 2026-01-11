#include <stdio.h>
#include <stdlib.h>
#include <time.h>
#include <unistd.h>

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
    switch(cmd) {
        case 'a':
            printf("Action: Add\n");
            break;
        case 'd':
            printf("Action: Delete\n");
            break;
        case 'u':
            printf("Action: Update\n");
            break;
        case 'q':
            printf("Action: Query\n");
            break;
        default:
            printf("Action: Unknown\n");
            break;
    }
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
    printf("=== LBR 测试程序开始 ===\n");
    printf("PID: %d\n", getpid());
    printf("等待 eBPF 监控程序 attach (5秒)...\n");
    fflush(stdout);
    sleep(5);
    printf("\n开始执行测试...\n\n");
    
    srand(time(NULL));
    
    // 循环执行多轮测试，增加负载
    for (int round = 0; round < 10; round++) {
        printf("=== 第 %d 轮测试 ===\n", round + 1);
        
        // 测试1: 排序算法 - 增加数组大小和执行次数
        printf("测试1: 冒泡排序 (大规模)\n");
        for (int iter = 0; iter < 50; iter++) {
            int arr[100];
            for (int i = 0; i < 100; i++) {
                arr[i] = rand() % 1000;
            }
            bubble_sort(arr, 100);
        }
        printf("完成 50 次 100 元素排序\n\n");
        
        // 测试2: 条件分支 - 大量执行
        printf("测试2: 数字分类 (密集)\n");
        int classify_count = 0;
        for (int i = 0; i < 10000; i++) {
            int random_num = (rand() % 10000) - 5000;
            classify_count += classify_number(random_num);
        }
        printf("完成 10000 次分类，结果: %d\n\n", classify_count);
        
        // 测试3: 递归调用 - 增加计算次数
        printf("测试3: 斐波那契数列 (密集递归)\n");
        int fib_sum = 0;
        for (int i = 0; i < 100; i++) {
            fib_sum += fibonacci(15);  // 更大的递归深度
        }
        printf("完成 100 次 fibonacci(15)，总和: %d\n\n", fib_sum);
        
        // 测试4: Switch分支 - 大量执行
        printf("测试4: 命令处理 (密集)\n");
        char all_cmds[] = {'a', 'd', 'u', 'q', 'x', 'y', 'z'};
        for (int i = 0; i < 5000; i++) {
            char cmd = all_cmds[rand() % 7];
            if (i < 5) {  // 只打印前几个
                printf("命令 '%c': ", cmd);
            }
            process_command(cmd);
        }
        printf("完成 5000 次命令处理\n\n");
        
        // 测试5: 循环和条件 - 更大范围
        printf("测试5: 偶数求和 (大范围)\n");
        int sum_total = 0;
        for (int j = 0; j < 100; j++) {
            sum_total += sum_even_numbers(1000);
        }
        printf("完成 100 次大范围求和，总和: %d\n\n", sum_total);
        
        // 测试6: 密集的分支操作 - 大幅增加
        printf("测试6: 超密集分支操作\n");
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
        printf("分支计数结果: %d\n\n", branch_count);
        
        // 测试7: 嵌套循环和条件
        printf("测试7: 嵌套分支\n");
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
        printf("嵌套分支结果: %d\n\n", nested_count);
        
        printf("第 %d 轮完成\n", round + 1);
        printf("----------------------------------------\n\n");
    }
    
    printf("=== LBR 测试程序结束 ===\n");
    printf("程序将等待5秒以便LBR捕获...\n");
    sleep(5);
    
    return 0;
}
