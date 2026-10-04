BINARY       := klainmain
GO           := go
CLANG        := clang
# The tests/ E2E suite (each case spawns clang + a compiled binary) runs well
# past Go's default 600s `go test` timeout on a slower box — override it here so
# a full run doesn't die mid-suite with a timeout panic. test-par uses it as
# each shard's timeout too.
TEST_TIMEOUT ?= 45m
# *_worker.ts files are worker modules loaded via new Worker(...) — they are
# compiled into their spawning example's binary, not standalone entries.
# Showcase apps — real applications (the landing-page gallery), not console.log
# fixtures. Built as distributable single binaries via `make apps` (`--static`),
# with a self-containment assertion on Windows. Listed by entry source; loadtest
# lives under apps/ (multi-module), the rest still under examples/ pending the
# apps/ relocation.
APPS         := apps/klaintop/main.ts apps/files/main.ts apps/todo/main.ts apps/menu/main.ts apps/explorer/main.ts apps/loadtest/main.ts apps/klainconf/main.ts
HTTPBIN_LITE := .httpbin-lite
HTTPBIN_LITE_PORT := 8765
# TDD-00174 Stage A mode knobs: `make examples MM=auto OPTMEM=1` compiles the
# example corpus in that memory mode / with -optimize-memory, turning the
# whole corpus into a mode-differential lane (outputs must be identical to a
# default-mode run). MM supports manual/auto (gc needs per-machine libgc and
# its own suite). memory_free.ts pins manual: Memory.free is a compile error
# under -mm=auto by design.
MM ?=
OPTMEM ?=
MODEFLAGS := $(if $(MM),-mm=$(MM)) $(if $(OPTMEM),-optimize-memory)
MODEFLAGS_NOMM := $(if $(OPTMEM),-optimize-memory)

.PHONY: all build dist install test test-unit test-par test-shard examples drift apps compile compile-o run ir clean fmt vet lint fuzz fuzz-codegen fuzz-all conformance-fetch conformance conformance-node conformance-ts conformance-wpt shadow-report mode-lanes status status-check status-roundtrip reference-check reference-sync conformance-check conformance-sync coverage-check coverage-sync help

## all: build the compiler
all: build

## build: compile KlainMainLang to ./klainmain
# VERSION: what `klainmain --version` and process.versions.klain report. The
# release workflow stamps the release tag; a local build stamps `git describe`
# (e.g. 0.63.0-3-gabc1234-dirty) so no checkout ships as "0.0.0-dev" by accident.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')
LDFLAGS_VERSION := -X KlainMainLang/codegen/llvm.KlainVersion=$(VERSION)

build:
	$(GO) build -ldflags "$(LDFLAGS_VERSION)" -o $(BINARY) .

## dist: cross-compile the release binaries for every supported platform into dist/ (the same command the release workflow runs, minus the per-platform test gate)
dist:
	@mkdir -p dist
	@for t in linux/amd64/linux-x64 linux/arm64/linux-arm64 darwin/amd64/macos-x64 darwin/arm64/macos-arm64 windows/amd64/windows-x64.exe; do \
	  goos=$${t%%/*}; rest=$${t#*/}; goarch=$${rest%%/*}; name=$${rest#*/}; \
	  echo "  $$goos/$$goarch -> dist/$(BINARY)-v$(VERSION)-$$name"; \
	  GOOS=$$goos GOARCH=$$goarch CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w $(LDFLAGS_VERSION)" -o dist/$(BINARY)-v$(VERSION)-$$name . || exit 1; \
	done
	@cd dist && (sha256sum $(BINARY)-* 2>/dev/null || shasum -a 256 $(BINARY)-*) | sed 's/ \*/  /' > checksums.txt && cat checksums.txt

## install: install KlainMainLang to GOPATH/bin
install:
	$(GO) install .

## test: run every package's tests, tests/ included — the command CI runs
test:
	$(GO) test -timeout $(TEST_TIMEOUT) ./...

## test-unit: every package's tests except the tests/ suite
test-unit:
	$(GO) test -timeout $(TEST_TIMEOUT) $$($(GO) list ./... | grep -v '/tests$$')

## test-par: run the tests/ suite sharded across SHARDS parallel processes.
## tools/testshard compiles the test binary once, deals the tests round-robin
## into SHARDS shards and runs them concurrently; any failure is re-run
## *serially*, so a parallel-unsafe test (signal-disposition timing, a
## fixed-port server) that only flakes under concurrency doesn't fail the run —
## only a test that also fails alone does. The unit packages run first
## (test-unit), so test-par covers everything `make test` does.
SHARDS ?= 4
test-par: build test-unit
	$(GO) run ./tools/testshard -shards $(SHARDS) -timeout $(TEST_TIMEOUT)

## test-shard: run one shard (SHARD, 0-based) of the tests/ suite dealt into
## SHARDS — one CI matrix job; the same split test-par runs all of at once
test-shard: build
	$(GO) run ./tools/testshard -shard $(SHARD) -shards $(SHARDS) -timeout $(TEST_TIMEOUT)

## examples: compile every example .ts file and run it
## examples/fetch/*.ts and examples/async/promise_all.ts talk to a local
## httpbin-lite fixture server (tools/httpbin-lite/, ADR-00096) instead of a
## real external host, so the suite stays deterministic and offline-capable
## instead of depending on some third-party website's uptime.
# Each example runs under a time limit, so one that hangs fails instead of
# stalling the whole run (macOS has no `timeout`; SIGALRM ends the program).
EXAMPLE_TIMEOUT := 300
# Concurrent example compiles (tools/runexamples); the runs stay serial.
EXAMPLES_JOBS ?= $(shell getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)

examples: build
	@./$(BINARY) -o $(HTTPBIN_LITE) tools/httpbin-lite/httpbin.ts >/dev/null 2>&1
	@HTTPBIN_LITE_PORT=$(HTTPBIN_LITE_PORT) ./$(HTTPBIN_LITE) & \
	fixture_pid=$$!; \
	trap "kill $$fixture_pid 2>/dev/null" EXIT INT TERM; \
	for i in $$(seq 1 50); do \
		curl -s -o /dev/null http://127.0.0.1:$(HTTPBIN_LITE_PORT)/get && break; \
		sleep 0.1; \
	done; \
	$(GO) run ./tools/runexamples -bin ./$(BINARY) -j $(EXAMPLES_JOBS) -timeout $(EXAMPLE_TIMEOUT)s -modeflags "$(MODEFLAGS)" -modeflags-nomm "$(MODEFLAGS_NOMM)"

## drift: the builtin library compiles the same in every example and every
## compile is deterministic (TDD-00238 Stage 4; tools/libdrift)
drift: build
	@ex=$$($(GO) run ./tools/runexamples -list); \
	$(GO) run ./tools/libdrift -bin ./$(BINARY) -fail -top 20 $$ex && \
	$(GO) run ./tools/libdrift -bin ./$(BINARY) -repeat 2 $$ex

## apps: build every showcase app (APPS) as a distributable single binary
## (--static) and, on Windows, assert it imports no non-system DLL. These are
## real applications, not console.log fixtures, so they get their own guard:
## `make examples` only checks exit-0 on a non-tty and can't see a missing DLL.
## macOS builds without --static (no static libSystem) — compile-check only there.
apps: build
	@uname_s=$$(uname -s 2>/dev/null); \
	win=0; static=--static; \
	case "$$uname_s" in MINGW*|MSYS*|CYGWIN*) win=1;; Darwin) static=;; esac; \
	ok=0; fail=0; \
	for src in $(APPS); do \
		[ -e "$$src" ] || { printf '%-40s SKIP (missing)\n' "  $$src"; continue; }; \
		base=$$(dirname $$src)/$$(basename $$src .ts); out=$$base; \
		[ "$$win" = "1" ] && out=$$base.exe; \
		printf '%-40s' "  $$src"; \
		if ./$(BINARY) $$static -o $$out $$src >/tmp/kml-apps.log 2>&1; then \
			if [ "$$win" = "1" ]; then \
				bad=$$(objdump -p "$$out" 2>/dev/null | grep -i 'DLL Name' | grep -icE 'stdc|gcc_s|winpthread|pcre|curl|ssl|crypto|nghttp|brotli|zstd|ssh|idn|psl|unistring|iconv'); \
				if [ "$$bad" = "0" ]; then echo "OK (self-contained)"; ok=$$((ok+1)); \
				else echo "FAIL ($$bad non-system DLL import(s))"; fail=$$((fail+1)); fi; \
			else echo "OK (built)"; ok=$$((ok+1)); fi; \
		else echo "FAIL (build)"; tail -3 /tmp/kml-apps.log; fail=$$((fail+1)); fi; \
	done; \
	echo ""; \
	echo "apps: $$ok ok, $$fail failed"; \
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
	$(GO) test ./lexer/ -run=^$$ -fuzz=FuzzScan -fuzztime=$(FUZZTIME)
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

## conformance: regenerate the Test262 reports (both compat lanes → docs/testing/<platform>/{strict,js}/) by running the full corpus through this compiler's own pipeline (fetches first if needed; self-contained — go run, not the klainmain binary)
conformance: conformance-fetch
	$(GO) run ./tools/conformance -compat=both

## conformance-node: regenerate the Node-core reports (both compat lanes → docs/testing/<platform>/{strict|js}/CONFORMANCE-RESULTS-NODE.md) — Node pure-module behavioral tests (TDD-00121 Track B, TDD-00022)
conformance-node: conformance-fetch
	$(GO) run ./tools/conformance -suite=node -compat=both

## conformance-ts: regenerate the TypeScript-oracle reports (both compat lanes → docs/testing/<platform>/{strict|js}/CONFORMANCE-RESULTS-TS.md) — accept/reject oracle (TDD-00121 Track C)
conformance-ts: conformance-fetch
	$(GO) run ./tools/conformance -suite=ts -compat=both

## conformance-wpt: regenerate the Web Platform Tests reports (both compat lanes → docs/testing/<platform>/{strict|js}/CONFORMANCE-RESULTS-WPT.md) — the FULL headless multi-global corpus (every .any.js/.window.js/.worker.js repo-wide, no allowlist) through a testharness.js shim (TDD-00082 Track 2, TDD-00204)
conformance-wpt: conformance-fetch
	$(GO) run ./tools/conformance -suite=wpt -compat=both

## mode-lanes: build and run every example in the default memory mode and under -mm=auto, -mm=auto -optimize-memory and -mm=gc, and fail when an output differs (.modelanes-out/MODE-LANES.md; TDD-00230 P0.3)
mode-lanes: build
	@./$(BINARY) -o $(HTTPBIN_LITE) tools/httpbin-lite/httpbin.ts >/dev/null 2>&1
	@HTTPBIN_LITE_PORT=$(HTTPBIN_LITE_PORT) ./$(HTTPBIN_LITE) & \
	fixture_pid=$$!; \
	trap "kill $$fixture_pid 2>/dev/null" EXIT INT TERM; \
	for i in $$(seq 1 50); do \
		curl -s -o /dev/null http://127.0.0.1:$(HTTPBIN_LITE_PORT)/get && break; \
		sleep 0.1; \
	done; \
	$(GO) run ./tools/modelanes -bin ./$(BINARY)

## shadow-report: compile the corpus comparing every old codegen decider with the new front end, and rank the disagreements into .shadow-out/SHADOW-REPORT.md (TDD-00230 P0.2)
shadow-report: build
	$(GO) run ./tools/shadow -bin ./$(BINARY)

## status: regenerate every docs/status page (README included) from the docs/status/data/*.json source of truth — edit the JSON, never the pages; all coverage numbers derive from the row tables. Also regenerates the docs/adr/ and docs/tdd/ index README tables (and the status TDD backlog) from the ADR/TDD record files — edit those files, never the index tables.
status:
	$(GO) run ./cmd/statusgen generate

## status-check: fail if any docs/status page is out of sync with its data/ source (CI forward guard)
status-check:
	$(GO) run ./cmd/statusgen check

## status-roundtrip: re-derivation tool — verify every docs/status page survives the Markdown→JSON→Markdown round-trip byte-identically (useful after a deliberate hand-edit, before re-exporting)
status-roundtrip:
	$(GO) run ./cmd/statusgen roundtrip $(wildcard docs/status/*.md)

## reference-check: fail if the website API reference (website/src/data/reference/*.json) has drifted from the status data — every ✅ status row needs a reference entry, badges must agree. The website is a SECOND projection of docs/status/data; run this same-commit as any change that flips/adds a ✅ status row. Node built-ins only, no npm install.
reference-check:
	node website/scripts/check-reference.mjs

## reference-sync: rewrite each website reference surface's coverage counts/percentages from its status page (fixes stale numbers instead of erroring). Run after a change flips ✅ rows, then commit the updated reference JSON.
reference-sync:
	node website/scripts/check-reference.mjs --sync

## conformance-check: fail if the website conformance data (website/src/data/conformance-platforms.json) has drifted from the canonical per-platform summaries under docs/testing/. Run same-commit as any conformance re-run so the site never ships stale numbers. Node built-ins only, no npm install.
conformance-check:
	node website/scripts/check-conformance.mjs

## conformance-sync: regenerate the website conformance data from docs/testing/<platform>/conformance-summary.json (fixes drift instead of erroring). Run after a conformance re-run, then commit the updated JSON.
conformance-sync:
	node website/scripts/gen-conformance.mjs

## coverage-check: fail if the website feature-area coverage figures (website/src/data/coverage.json) have drifted from the canonical status rollup (docs/status/coverage-rollup.json, emitted by `make status`). Run same-commit as any status-data change. Node built-ins only, no npm install.
coverage-check:
	node website/scripts/check-coverage.mjs

## coverage-sync: regenerate the website coverage figures from docs/status/coverage-rollup.json (fixes drift instead of erroring). Run after a status-data change, then commit the updated JSON.
coverage-sync:
	node website/scripts/gen-coverage.mjs

## clean: remove the compiler binary and all compiled example artifacts
clean:
	rm -f $(BINARY) $(HTTPBIN_LITE)
	find examples -type f ! -name '*.*' -delete
	find examples -name '*.ll' -delete

## help: list available targets
help:
	@awk '/^## [A-Za-z0-9_-]+: / { t = $$2; sub(/:$$/, "", t); d = substr($$0, index($$0, ": ") + 2); \
		u = ""; if (match(d, /\(usage: [^)]*\)/)) u = "  " substr(d, RSTART + 8, RLENGTH - 9); \
		sub(/ *\(usage:.*/, "", d); sub(/ *\(.*$$/, "", d); sub(/ — .*$$/, "", d); sub(/[.;] .*$$/, "", d); sub(/\.$$/, "", d); \
		if (length(d) > 72) d = substr(d, 1, 69) "..."; printf "  %-20s %s%s\n", t, d, u }' Makefile
