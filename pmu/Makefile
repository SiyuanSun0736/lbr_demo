CC := gcc
CFLAGS := -O2 -Wall -Wextra
LDFLAGS := -lm

TARGETS := l1_dcache l1d_pend_miss l1_icache llc_cache dtlb itlb mem_inst pmu_monitor_all
SOURCES := l1_dcache.c l1d_pend_miss.c l1_icache.c llc_cache.c dtlb.c itlb.c mem_inst.c pmu_monitor_all.c

.PHONY: all clean help

all: $(TARGETS)

l1_dcache: l1_dcache.o
	$(CC) $(CFLAGS) -o $@ $^ $(LDFLAGS)

l1d_pend_miss: l1d_pend_miss.o
	$(CC) $(CFLAGS) -o $@ $^ $(LDFLAGS)

l1_icache: l1_icache.o
	$(CC) $(CFLAGS) -o $@ $^ $(LDFLAGS)

llc_cache: llc_cache.o
	$(CC) $(CFLAGS) -o $@ $^ $(LDFLAGS)

dtlb: dtlb.o
	$(CC) $(CFLAGS) -o $@ $^ $(LDFLAGS)

itlb: itlb.o
	$(CC) $(CFLAGS) -o $@ $^ $(LDFLAGS)

mem_inst: mem_inst.o
	$(CC) $(CFLAGS) -o $@ $^ $(LDFLAGS)

pmu_monitor_all: pmu_monitor_all.o
	$(CC) $(CFLAGS) -o $@ $^ $(LDFLAGS)

%.o: %.c
	$(CC) $(CFLAGS) -c -o $@ $<

help:
	@echo "Cache Performance Monitor - Makefile"
	@echo ""
	@echo "Targets:"
	@echo "  all             - Build all programs"
	@echo "  l1_dcache       - Build the l1_dcache program"
	@echo "  l1d_pend_miss   - Build the l1d_pend_miss program"
	@echo "  l1_icache       - Build the l1_icache program"
	@echo "  llc_cache       - Build the llc_cache program"
	@echo "  dtlb            - Build the dtlb program"
	@echo "  itlb            - Build the itlb program"
	@echo "  mem_inst        - Build the mem_inst program"
	@echo "  pmu_monitor_all - Build the comprehensive monitoring program"
	@echo "  clean           - Remove compiled files"
	@echo "  help            - Show this help message"
	@echo ""
	@echo "Usage:"
	@echo "  make                   # Build all programs"
	@echo "  make pmu_monitor_all   # Build comprehensive monitor"
	@echo "  make l1_dcache         # Build only l1_dcache"
	@echo "  make l1d_pend_miss     # Build only l1d_pend_miss"
	@echo "  make l1_icache         # Build only l1_icache"
	@echo "  make llc_cache         # Build only llc_cache"
	@echo "  make dtlb              # Build only dtlb"
	@echo "  make itlb              # Build only itlb"
	@echo "  make mem_inst          # Build only mem_inst"
	@echo "  make clean             # Remove compiled files"
	@echo "  ./pmu_monitor_all      # Run comprehensive monitor"
	@echo "  ./pmu_monitor_all <PID># Monitor specific process by PID"
	@echo "  ./l1_dcache            # Run l1_dcache with current process"
	@echo "  ./l1_dcache <PID>      # Monitor specific process by PID"
	@echo "  ./l1d_pend_miss        # Run l1d_pend_miss with current process"
	@echo "  ./l1d_pend_miss <PID>  # Monitor specific process by PID"
	@echo "  ./l1_icache            # Run l1_icache with current process"
	@echo "  ./l1_icache <PID>      # Monitor specific process by PID"
	@echo "  ./llc_cache            # Run llc_cache with current process"
	@echo "  ./llc_cache <PID>      # Monitor specific process by PID"
	@echo "  ./dtlb                 # Run dtlb with current process"
	@echo "  ./dtlb <PID>           # Monitor specific process by PID"
	@echo "  ./itlb                 # Run itlb with current process"
	@echo "  ./itlb <PID>           # Monitor specific process by PID"
	@echo "  ./mem_inst             # Run mem_inst with current process"
	@echo "  ./mem_inst <PID>       # Monitor specific process by PID"
	@echo ""
	@echo "Note: May require elevated privileges (sudo) to run"

clean:
	rm -f $(TARGETS) *.o
