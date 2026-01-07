.PHONY: all generate build clean run

all: generate build

generate:
	go generate ./...

build: generate
	go build -o lbr-demo ./cmd

clean:
	rm -f lbr-demo
	rm -f cmd/lbr_bpfeb.o cmd/lbr_bpfel.o
	rm -f cmd/lbr_bpfeb.go cmd/lbr_bpfel.go

run: build
	sudo ./lbr-demo