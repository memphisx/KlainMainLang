BINARY       := klainmain
GO           := go
CLANG        := clang
EXAMPLES     := $(shell find examples -name '*.ts' | sort)
HTTPBIN_LITE := .httpbin-lite
HTTPBIN_LITE_PORT := 8765

.PHONY: all build install test test-par examples compile compile-o run ir clean fmt vet lint fuzz fuzz-codegen fuzz-all conformance-fetch conformance help

## all: build the compiler
all: build

## build: compile KlainMainLang to ./klainmain
build:
	$(GO) build -o $(BINARY) .

## install: install KlainMainLang to GOPATH/bin
install:
	$(GO) install .

## test: run Go unit tests
test:
	$(GO) test ./...

## test-par: run the tests/ suite sharded across SHARDS parallel processes
## (~1.5-2x faster than serial — the suite is subprocess/IO-bound, not CPU-bound,
## so 4 shards is the sweet spot; more just thrashes memory/IO). Compiles the test
## binary once, then runs disjoint name-shards of it concurrently. Any failure is
## re-run *serially*, so a parallel-unsafe test (signal-disposition timing, a
## fixed-port server) that only flakes under concurrency doesn't fail the run —
## only a test that also fails alone does. Serial `make test` stays the source of
## truth; this is a fast local pre-check.
SHARDS ?= 4
test-par: build
	@tb=$$(mktemp -d)/kml_tests.test; sd=$$(mktemp -d); \
	$(GO) test ./tests/ -c -o "$$tb"; \
	$(GO) test ./tests/ -list '.*' 2>/dev/null | grep '^Test' | \
	  awk -v d="$$sd" -v n=$(SHARDS) '{print > (d "/s_" (NR % n) ".txt")}'; \
	for i in $$(seq 0 $$(($(SHARDS)-1))); do \
	  rx=$$(paste -sd'|' "$$sd/s_$$i.txt"); \
	  ( "$$tb" -test.run "^($$rx)$$" -test.timeout 20m >"$$sd/o_$$i.txt" 2>&1; echo $$? >"$$sd/c_$$i.txt" ) & \
	done; wait; \
	anyfail=0; fails=""; \
	for i in $$(seq 0 $$(($(SHARDS)-1))); do \
	  if [ "$$(cat $$sd/c_$$i.txt)" != "0" ]; then \
	    anyfail=1; \
	    n=$$(grep '^--- FAIL' "$$sd/o_$$i.txt" | sed 's/^--- FAIL: //; s/ (.*$$//'); \
	    if [ -z "$$n" ]; then echo "shard $$i failed with no test-level FAIL (panic/timeout) — full output:"; cat "$$sd/o_$$i.txt"; exit 1; fi; \
	    fails="$$fails $$n"; \
	  fi; \
	done; \
	if [ "$$anyfail" = "0" ]; then echo "ok  tests (parallel, $(SHARDS) shards)"; \
	else \
	  echo "parallel run had failures; re-running serially to rule out concurrency flakes:$$fails"; \
	  rx=$$(echo $$fails | tr ' ' '|'); \
	  "$$tb" -test.run "^($$rx)$$" -test.v; \
	fi

## examples: compile every example .ts file and run it
## examples/fetch/*.ts and examples/async/promise_all.ts talk to a local
## httpbin-lite fixture server (tools/httpbin-lite/, ADR-00096) instead of a
## real external host, so the suite stays deterministic and offline-capable
## instead of depending on some third-party website's uptime.
examples: build
	@$(GO) build -o $(HTTPBIN_LITE) ./tools/httpbin-lite
	@HTTPBIN_LITE_PORT=$(HTTPBIN_LITE_PORT) ./$(HTTPBIN_LITE) & \
	fixture_pid=$$!; \
	trap "kill $$fixture_pid 2>/dev/null" EXIT INT TERM; \
	for i in $$(seq 1 50); do \
		curl -s -o /dev/null http://127.0.0.1:$(HTTPBIN_LITE_PORT)/get && break; \
		sleep 0.1; \
	done; \
	ok=0; fail=0; \
	for f in $(EXAMPLES); do \
		out=$$(dirname $$f)/$$(basename $$f .ts); \
		printf '%-50s' "  $$f"; \
		if ./$(BINARY) $$f 2>/dev/null && $$out </dev/null 2>/dev/null >/dev/null; then \
			echo "OK"; ok=$$((ok+1)); \
		else \
			echo "FAIL"; fail=$$((fail+1)); \
		fi; \
	done; \
	echo ""; \
	echo "Results: $$ok passed, $$fail failed"; \
	test $$fail -eq 0

## compile: compile a .ts file to a native binary  (usage: make compile FILE=path/to/file.ts)
compile: build
ifndef FILE
	$(error FILE is not set. Usage: make compile FILE=path/to/file.ts)
endif
	./$(BINARY) $(FILE)

## compile-o: compile a .ts file to a named binary  (usage: make compile-o FILE=f.ts OUT=mybinary)
compile-o: build
ifndef FILE
	$(error FILE is not set. Usage: make compile-o FILE=path/to/file.ts OUT=mybinary)
endif
ifndef OUT
	$(error OUT is not set. Usage: make compile-o FILE=path/to/file.ts OUT=mybinary)
endif
	./$(BINARY) -o $(OUT) $(FILE)

## run: compile and run a single .ts file  (usage: make run FILE=path/to/file.ts)
run: build
ifndef FILE
	$(error FILE is not set. Usage: make run FILE=path/to/file.ts)
endif
	./$(BINARY) $(FILE)
	@bin=$$(echo $(FILE) | sed 's/\.ts$$//'); ./$$bin

## ir: emit LLVM IR for a single file without compiling  (usage: make ir FILE=...)
ir: build
ifndef FILE
	$(error FILE is not set. Usage: make ir FILE=path/to/file.ts)
endif
	./$(BINARY) --emit-llvm $(FILE)

## fmt: format all Go source files
fmt:
	$(GO) fmt ./...

## vet: run go vet
vet:
	$(GO) vet ./...

## lint: fmt + vet
lint: fmt vet

## fuzz: fuzz the lexer and parser for 30s each  (usage: make fuzz [FUZZTIME=30s])
FUZZTIME := 30s
fuzz:
	$(GO) test ./lexer/ -run=^$$ -fuzz=FuzzTokenize -fuzztime=$(FUZZTIME)
	$(GO) test ./parser/ -run=^$$ -fuzz=FuzzParse -fuzztime=$(FUZZTIME)

## fuzz-codegen: fuzz the full parse->codegen->clang->run pipeline for 30s each (usage: make fuzz-codegen [FUZZTIME=30s])
## Much slower per-iteration than 'fuzz' (each execution shells out to clang) — see TDD-00014.
fuzz-codegen:
	$(GO) test ./tests/ -run=^$$ -fuzz=FuzzArithmeticCorrectness -fuzztime=$(FUZZTIME)
	$(GO) test ./tests/ -run=^$$ -fuzz=FuzzProgramWellFormed -fuzztime=$(FUZZTIME)
	$(GO) test ./tests/ -run=^$$ -fuzz=FuzzPromiseCombinatorsOrdinary -fuzztime=$(FUZZTIME)
	$(GO) test ./tests/ -run=^$$ -fuzz=FuzzFetchInitOracle -fuzztime=$(FUZZTIME)

## fuzz-all: run every fuzz target (lexer, parser, and the codegen pipeline)
fuzz-all: fuzz fuzz-codegen

## conformance-fetch: clone/update the pinned Test262 corpus into .test262/ (idempotent — no-op if already at the pinned commit; TDD-00008 Design V2)
conformance-fetch:
	./tools/conformance/fetch.sh

## conformance: regenerate docs/testing/CONFORMANCE-RESULTS.md by running the full Test262 corpus through this compiler's own pipeline (fetches the corpus first if needed; self-contained — go run, not the klainmain binary)
conformance: conformance-fetch
	$(GO) run ./tools/conformance

## clean: remove the compiler binary and all compiled example artifacts
clean:
	rm -f $(BINARY) $(HTTPBIN_LITE)
	find examples -type f ! -name '*.*' -delete
	find examples -name '*.ll' -delete

## help: list available targets
help:
	@grep -E '^## ' Makefile | sed 's/^## /  /'
