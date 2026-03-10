.PHONY: all clean run test_lbr_sframe test_lbr_dwarf test_stack_unwinding

all: test_lbr test_stack_unwinding

# 默认编译（带调试符号）
test_lbr: test_lbr.c
	gcc -O2 -g -o test_lbr test_lbr.c

# 编译带 SFrame 信息的版本（需要 Binutils 2.41+）
# 使用 -Wa,--gsframe 将选项传递给汇编器生成 SFrame V2 格式
test_lbr_sframe: test_lbr.c
	@echo "使用 -Wa,--gsframe 编译以生成 SFrame section..."
	@gcc -O0 -g  -fno-omit-frame-pointer -Wa,--gsframe -o test_lbr_sframe test_lbr.c 2>/dev/null || \
		(echo "警告: --gsframe 不支持（需要 Binutils 2.41+），使用 -g 编译..." && \
		 gcc -O0 -g -fno-omit-frame-pointer -o test_lbr_sframe test_lbr.c)
	@echo "编译完成，检查 SFrame section 和 segment："
	@readelf -S test_lbr_sframe | grep "\.sframe" && echo "✓ 找到 .sframe section" || echo "✗ 未找到 .sframe section"
	@readelf -l test_lbr_sframe | grep "GNU_SFRAME" && echo "✓ 找到 GNU_SFRAME segment" || echo "✗ 未找到 GNU_SFRAME segment"
	@readelf -S test_lbr_sframe | grep -E "\.debug" > /dev/null && echo "✓ DWARF 调试信息存在" || echo "✗ 无 DWARF 调试信息"

# 编译仅带 DWARF 信息的版本
test_lbr_dwarf: test_lbr.c
	gcc -O2 -g -o test_lbr_dwarf test_lbr.c

# 编译栈回溯测试程序（必须保留帧指针）
test_stack_unwinding: test_stack_unwinding.c
	@echo "编译栈回溯测试程序（保留帧指针和SFrame）..."
	@gcc -O0 -g -fno-omit-frame-pointer -Wa,--gsframe -o test_stack_unwinding test_stack_unwinding.c 2>/dev/null || \
		(echo "警告: --gsframe 不支持（需要 Binutils 2.41+），使用 -g 编译..." && \
		 gcc -O0 -g -fno-omit-frame-pointer -o test_stack_unwinding test_stack_unwinding.c)
	@echo "编译完成，检查 SFrame section："
	@readelf -S test_stack_unwinding | grep "\.sframe" > /dev/null && echo "✓ 找到 .sframe section" || echo "✗ 未找到 .sframe section"
	@echo "✓ 栈回溯测试程序编译完成: test_stack_unwinding"

clean:
	rm -f test_lbr test_lbr_sframe test_lbr_dwarf test_stack_unwinding

run: test_lbr
	./test_lbr
