.PHONY: all clean run

all: test_lbr

test_lbr: test_lbr.c
	gcc -O2 -g -o test_lbr test_lbr.c

clean:
	rm -f test_lbr

run: test_lbr
	./test_lbr
