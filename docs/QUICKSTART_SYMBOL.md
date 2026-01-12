# 快速开始：LBR 用户态符号解析

## 一分钟快速测试

```bash
# 1. 编译测试程序（带调试符号）
cd test
make

# 2. 启动测试程序
./test_lbr &
TEST_PID=$!

# 3. 运行 LBR 监控（自动解析符号）
cd ..
sudo ./lbr-demo -pid $TEST_PID

# 4. 查看输出（会显示函数名和源代码行号）
```

## 三种解析模式对比

### 模式 1: addr2line（默认，推荐）
```bash
sudo ./lbr-demo -pid <PID> -addr2line=true
```
**输出示例:**
```
[#00] bubble_sort (test_lbr.c:9) -> bubble_sort (test_lbr.c:11)
[#01] main (test_lbr.c:85) -> bubble_sort (test_lbr.c:7)
```

### 模式 2: DWARF
```bash
sudo ./lbr-demo -pid <PID> -dwarf=true
```
**输出示例:**
```
[#00] bubble_sort (test_lbr.c:9) -> bubble_sort (test_lbr.c:11)
[#01] main (test_lbr.c:85) -> bubble_sort (test_lbr.c:7)
```

### 模式 3: 仅地址
```bash
sudo ./lbr-demo -pid <PID> -resolve=false
```
**输出示例:**
```
[#00] [user]+0x5647a1234567 -> [user]+0x5647a1234590
[#01] [user]+0x5647a1234598 -> [user]+0x5647a12343cc
```

## 完整自动化测试

运行所有三种模式的对比测试：

```bash
cd test
sudo ./test_symbol_resolution.sh
```

该脚本会：
- ✅ 自动编译程序
- ✅ 分别测试三种解析模式
- ✅ 生成对比日志
- ✅ 显示结果摘要

## 编译要求

### 确保程序包含调试符号
```bash
# 正确：包含 -g 标志
gcc -O2 -g -o program source.c

# 验证
file program
# 应输出: ELF 64-bit ... not stripped
```

### 如果看不到符号信息
```bash
# 检查是否被 strip
file test_lbr

# 重新编译
make clean && make
```

## 常用命令

### 实时监控并解析
```bash
sudo ./lbr-demo -pid <PID> | tee output.log
```

### 分析特定函数
```bash
grep "fibonacci\|bubble_sort" output.log
```

### 统计函数调用次数
```bash
grep -oP '\w+(?= \()' output.log | sort | uniq -c | sort -rn
```

## 更多信息

详细文档: [docs/SYMBOL_RESOLUTION.md](docs/SYMBOL_RESOLUTION.md)
