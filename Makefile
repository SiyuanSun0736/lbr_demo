.PHONY: all generate build build-examples clean run

all: generate build build-examples

generate:
	go generate ./...

build: generate
	go build -o lbr-demo ./cmd

build-examples: generate
	go build -o examples/stack_unwinding/stack_unwinding ./examples/stack_unwinding

clean:
	rm -f lbr-demo
	rm -f cmd/lbr_bpfeb.o cmd/lbr_bpfel.o
	rm -f cmd/lbr_bpfeb.go cmd/lbr_bpfel.go
	rm -f examples/stack_unwinding/stack_unwinding

run: build
	sudo ./lbr-demo