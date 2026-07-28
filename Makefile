BINARY   := klainmain
GO       := go
CLANG    := clang
EXAMPLES := $(shell find examples -name '*.ts' | sort)

.PHONY: all build install test examples compile compile-o run ir clean fmt vet lint fuzz fuzz-codegen fuzz-all help

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

## examples: compile every example .ts file and run it
examples: build
	@ok=0; fail=0; \
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

## fuzz-all: run every fuzz target (lexer, parser, and the codegen pipeline)
fuzz-all: fuzz fuzz-codegen

## clean: remove the compiler binary and all compiled example artifacts
clean:
	rm -f $(BINARY)
	find examples -type f ! -name '*.ts' -delete
	find examples -name '*.ll' -delete

## help: list available targets
help:
	@grep -E '^## ' Makefile | sed 's/^## /  /'
