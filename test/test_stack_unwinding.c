#include <stdio.h>
#include <unistd.h>
#include <signal.h>

// 深层嵌套的函数调用，用于测试栈回溯

void function_level_5() {
    printf("Level 5: 睡眠中... (PID: %d)\n", getpid());
    printf("在另一个终端运行: ./stack_unwinding_demo %d\n", getpid());
    
    // 无限循环以保持进程运行
    while(1) {
        sleep(1);
    }
}

void function_level_4() {
    printf("Level 4: 调用 Level 5\n");
    function_level_5();
}

void function_level_3() {
    printf("Level 3: 调用 Level 4\n");
    function_level_4();
}

void function_level_2() {
    printf("Level 2: 调用 Level 3\n");
    function_level_3();
}

void function_level_1() {
    printf("Level 1: 调用 Level 2\n");
    function_level_2();
}

int main() {
    printf("栈回溯测试程序\n");
    printf("================\n");
    printf("进程 PID: %d\n\n", getpid());
    
    function_level_1();
    
    return 0;
}
