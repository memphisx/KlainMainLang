# KlainMainLang

A TypeScript-to-native compiler, written in Go, that emits LLVM IR and hands it to `clang`. You write `.ts`, it writes `.ll`, `clang` writes a real executable, your operating system is none the wiser.

Why does this exist? Not because TypeScript-to-native compilation needed solving. Microsoft's own research arm already tried this properly, with an actual team, an actual budget, and an actual production use case ([Static TypeScript](https://www.microsoft.com/en-us/research/publication/static-typescript/), compiling a restricted TypeScript subset to native code for microcontrollers), and this project has none of the above going for it. It exists because "how would I even build a compiler" is a much more fun rabbit hole than whatever I was supposed to be doing that day. The name is the actual mission statement. Κλάιν Μάιν (Klain Main) is Greek slang for "I don't care": build it anyway, for no better reason than "because I can." It has since grown a garbage collector's worth of features (including, as of `-mm=gc`, an actual garbage collector — opt-in, see below) and a small mountain of design-decision paperwork in `docs/adr/`.

> **⚠️ Personal / experimental project.** One person, building this for fun, learning how compilers actually work by making all the mistakes smarter people already made, and already fixed, in better languages, ages ago. Not audited, not hardened, no stability guarantees between commits, and never destined for a production pipeline near you. It leaks memory on purpose by default (see below) and is enthusiastically fine with that. Perfect for tinkering, small CLI toys, and impressing exactly one (1) person at a dinner party. Bring your own garbage collector, or just pass `-mm=gc` and let it bring one for you.

## What actually works right now

The honest, itemized answer lives in **[`docs/status/README.md`](docs/status/README.md)**: a feature-by-feature matrix, one page per feature area, with coverage percentages kept current as things ship — trust it over any claim here if the two ever disagree. It also has a "Fidelity Gaps" section for features marked done that still have real, non-cosmetic differences from actual JS/TS behavior worth knowing before you rely on them.

Broad strokes: core TypeScript language features (control flow, operators, closures, classes — including inheritance, `static`/`private`/`protected`/`abstract`/`implements`, getters/setters — generics, enums, interfaces) are mostly there; Node.js-style APIs (`fs`, `process`, `path`, `os`, `http.listen`, `EventEmitter`) are substantial; WHATWG Web Platform APIs (`fetch`, `URL`, `WebSocket`, `ArrayBuffer`/TypedArrays) are a mixed bag. The compiler also fuzzes itself (lexer, parser, and the full parse-through-binary pipeline) via `make fuzz`/`make fuzz-codegen`/`make fuzz-all`.

Every feature and bug fix comes with a matching entry in **[`docs/adr/`](docs/adr/README.md)**: a paper trail of what was tried, what broke, and why a given weird decision was made on purpose rather than by accident. Bigger features get scoped out in **[`docs/tdd/`](docs/tdd/README.md)** first: a design doc written before any code exists. Both indexes are cross-linked from `docs/status/` wherever relevant, rather than repeated here.

Want to see it in action instead of reading about it? Every language feature has a runnable example under **[`examples/`](examples/)**: no README code snippets to go stale, just `.ts` files that actually compile and run (verified by `make examples`, every time).

Releases follow [Semantic Versioning](https://semver.org/), applied automatically from Conventional Commit messages via GitHub Actions. See **[`VERSIONING.md`](VERSIONING.md)** for the exact scheme and what still has to be true before this hits `1.0.0`.

## Requirements

- Go 1.26+ (see `go.mod` for the exact pinned version)
- `clang` (LLVM 15+, needs opaque-pointer support)
- `libcurl`, needed if the compiled program calls `fetch` **or** `http.listen` — the HTTP server's event loop links libcurl unconditionally so it can merge `fetch`'s non-blocking transfers into the same `select()` loop, even in a server that never calls `fetch` itself. Every other program stays plain-libc, no extra install needed
- `bdw-gc`/`libgc` (the Boehm-Demers-Weiser garbage collector), needed only if compiling with `-mm=gc` — `brew install bdw-gc` on macOS, `apt-get install libgc-dev` on Debian/Ubuntu, `apk add gc-dev` on Alpine. The default `manual` mode needs nothing beyond plain libc
- `libpcre2-8`/`libpcre2-dev`, needed if the compiled program uses `RegExp` (either `new RegExp(...)` or a `/pattern/flags` literal) — `apt-get install libpcre2-dev` on Debian/Ubuntu, `brew install pcre2` on macOS, `apk add pcre2-dev` on Alpine. Same conditional-linking convention as `libcurl`: every other program stays plain-libc. See [docs/status/REGEXP.md](docs/status/REGEXP.md)

### Debugging tools (optional, for chasing memory-corruption bugs)

- **AddressSanitizer/UndefinedBehaviorSanitizer** — no separate install: bundled with `clang` itself, including Xcode's clang on macOS (confirmed directly on the Linux x86-64 box; not yet re-confirmed on Apple Silicon — see "Switching development machines" in the project's own instructions). `tests/compiler_test.go`'s `buildBinaryASan`/`buildBinaryGCASan` build a `-fsanitize=address -fsanitize=undefined` binary for a given source, for a specific investigation to call deliberately (not part of the regular `go test ./...` run — ASan roughly doubles memory/time cost). See `docs/adr/README.md` for what had to be fixed in `-mm=gc`'s allocator shim before this was actually usable.
- **Valgrind** — `apt-get install valgrind` on Debian/Ubuntu. **On Apple Silicon (arm64) macOS, Valgrind has historically had no official upstream support** — `brew install valgrind` may fail outright or install a build that doesn't actually work; confirm directly before relying on it there rather than assuming parity with the Linux box. ASan/UBSan (above) work identically on both platforms and should be the default tool; reach for Valgrind only for the specific class of bug (e.g. conservative-GC-adjacent memory questions) where its instruction-level, allocator-agnostic instrumentation is worth the extra setup friction — and expect some false-positive "uninitialized value" noise from Boehm GC's own conservative stack scanning under Memcheck, a known pattern for conservative collectors, generally addressed with suppressions rather than code changes.

## Quick start

```sh
# Build the compiler
make build          # produces ./klainmain

# Compile a TypeScript file to a native binary (does NOT run it)
./klainmain examples/basics/basics.ts
# → produces examples/basics/basics

# Run the binary yourself
./examples/basics/basics

# Specify a custom output name
./klainmain -o myapp examples/basics/basics.ts
./myapp

# Compile and run in one step
make run FILE=examples/basics/basics.ts

# Inspect the generated LLVM IR (in case you, too, enjoy pain)
make ir FILE=examples/basics/basics.ts
```

## Make targets

| Target | Description |
|---|---|
| `make build` | Compile the KlainMainLang compiler to `./klainmain` |
| `make install` | Install to `$GOPATH/bin` |
| `make test` | Run Go unit tests |
| `make examples` | Compile and run every example file (the closest thing this project has to a regression suite you can read) |
| `make compile FILE=f.ts` | Compile a `.ts` file to a native binary (does not run it) |
| `make compile-o FILE=f.ts OUT=name` | Compile to a named output binary |
| `make run FILE=f.ts` | Compile **and** run a single file |
| `make ir FILE=f.ts` | Emit LLVM IR only (no binary) |
| `make fmt` | Format all Go source |
| `make vet` | Run `go vet` |
| `make lint` | `fmt` + `vet` |
| `make fuzz [FUZZTIME=30s]` | Fuzz the lexer and parser |
| `make fuzz-codegen [FUZZTIME=30s]` | Fuzz the full parse→codegen→clang→run pipeline (slower per-iteration) |
| `make fuzz-all` | Run every fuzz target |
| `make conformance-fetch` | Clone/update the pinned Test262 corpus into `.test262/` (idempotent, gitignored — see [Test262 conformance](#test262-conformance)) |
| `make conformance` | Regenerate `docs/testing/CONFORMANCE-RESULTS.md` against the full Test262 corpus (fetches first if needed) |
| `make clean` | Remove compiler binary and compiled example artifacts |

## CLI flags

```sh
klainmain [flags] <file.ts>

  --emit-llvm   Emit LLVM IR to stdout and stop (do not compile)
  -o <name>     Output binary name (default: input path without .ts)
  --static      Statically link the output binary, for a scratch/distroless
                Docker image with nothing else in it. Linux only: run
                klainmain itself on Linux to use this. macOS's linker has
                no static-libc support at all (Apple ships no static
                libSystem/crt0.o, by design), so klainmain refuses --static
                immediately with an explanation rather than surfacing a
                confusing linker error.
  -mm <mode>    Memory management mode: manual (default, Memory.free(x)
                only — see docs/status/MEMORY-MANAGEMENT.md) or gc
                (Boehm GC: every allocation gets collected automatically,
                needs bdw-gc/libgc installed — see Requirements above).
                Works identically on Linux and macOS, no special linker
                flags needed either way.
```

Every other compiled binary here is dynamically linked (against libSystem on
macOS, glibc on Linux, plus `libcurl` if the program calls `fetch` or
`http.listen`, plus `libpcre2-8` if it uses `RegExp`), closer
to typical C/C++ toolchain output than a normal Go binary's usual
self-contained default. `--static` closes that gap on Linux, verified
end-to-end against real Docker builds: see `docker/Dockerfile` for a plain
example, `docker/Dockerfile.fetch-test` for one using `fetch`, and
`docker/Dockerfile.regexp-test` for one using `RegExp`.
A `fetch`-using program needs curl's *entire* static dependency chain listed
explicitly at link time (static archives don't auto-pull their own
dependencies the way shared libraries do), and (on Alpine/musl, at least)
a two-step `clang`-then-`gcc` link rather than a single `clang` invocation,
since some of Alpine's static archives are LTO-built in a format clang's
linker can't consume but gcc's can. See `docs/adr/README.md` for the full
recipe and investigation; this compiler doesn't attempt to automate it
itself, since the exact package list/workaround is specific to one distro's
build choices, not a portable fact this compiler could bake in safely.
`RegExp`'s `libpcre2-8` has none of that complexity — no TLS backend, no
transitive dependency chain — so `--static` just works for it with zero
extra flags, on both a bare Linux build and inside `Dockerfile.regexp-test`'s
`scratch` container.

## Test262 conformance

`docs/testing/CONFORMANCE-RESULTS.md` is a generated report, not hand-written — regenerate it, don't edit it. It's produced by running the *full, unfiltered* upstream [tc39/test262](https://github.com/tc39/test262) suite (53k+ files) through this compiler's own real pipeline (`parser.Parse` → `llvm.NewEmitter`/`EmitProgram` → `clang`, the same path `tests/compiler_test.go` uses), giving a real external conformance number rather than a hand-curated one. See [TDD-00008](docs/tdd/TDD-00008.md) (Design V2) and [ADR-00153](docs/adr/ADR-00153.md) for the full design/investigation.

```sh
make conformance-fetch   # clone the pinned test262 commit into .test262/ (idempotent, gitignored — ~263MB, not vendored)
make conformance         # regenerate docs/testing/CONFORMANCE-RESULTS.md (fetches first if needed; ~30s, all CPU cores)
```

Both targets are safe to re-run on a fresh machine (a new dev machine, after `git clone`, or after switching hosts per this project's own "Machine switch" practice) — `make conformance` alone is enough; it fetches on demand. `tools/conformance/fetch.sh` pins an exact commit SHA (test262 has no versioned release tags upstream) so re-running reproduces the identical corpus; `tools/conformance/main.go` walks it directly as a Go library (no dependency on the `klainmain` binary being built first) and needs only `clang` on `PATH`, same as everything else here. `tools/conformance/harness-shim/` holds this repo's own compiler-compatible reimplementation of test262's shared `sta.js`/`assert.js` harness files (the real upstream ones use prototype-based pseudo-classes this compiler's type system can't represent) — every actual test file stays 100% unmodified.

## The pipeline, in one breath

```
Lexer → Parser (recursive descent, Pratt precedence climbing) → Module resolver → LLVM IR emitter → clang -O2 → a binary that runs on your machine, unsupervised
```

`import`/`export` exist, but don't expect a real linker anywhere in there. The module resolver parses every file your entry file imports, merges them all into one AST, and hands *that* to the emitter. One `.ll`, one `clang` call, one generated `main()` either way. Imported files may only contain declarations (functions, types, that sort of thing): no top-level side effects yet. Only the file you actually pointed the compiler at gets to have opinions at runtime.

## Project layout

```
ast/                AST node definitions
codegen/
  llvm/             LLVM IR emitter — split into ~60 small domain files rather
                     than a handful of huge ones; the full file-by-file map
                     lives in the project's own instructions, condensed
                     here by domain:
    emitter.go, types.go   core Emitter struct/scope stack/EmitProgram; the
                     IR type system (Type, ArrayOf, ObjectOf, StructIR)
    emit_stmts.go     statements: for/while/do-while/if/switch/try/labeled break…
    emit_exprs*.go    expression dispatch, operators, assignment (incl.
                     &&=/||=/??=), member/index access, static type
                     inference, scalar coercion, var declarations
    emit_strings.go   string operations (concat, methods, template literals)
    emit_arrays_*.go  array mutation/HOF/sort/search/transform/iteration
                     (push/pop/map/filter/reduce/sort/slice/Array.from/…)
    emit_objects.go   objects, Object.keys/values/entries/groupBy, spread
    emit_func.go      functions, closures, callbacks
    emit_call*.go     call dispatch router + console/JSON/Math/Number/
                     encoding-crypto statics
    emit_classes.go   class fields/constructors/methods/this/new/instanceof,
                     inheritance (extends/super, static+vtable dispatch),
                     static members, private/protected, abstract, implements,
                     class-based for...of
    emit_collections.go  Map<K,V> and Set<T>
    emit_exceptions.go   try/catch/throw (setjmp/longjmp)
    emit_process.go   process.argv/env/exit/readLineSync/execFileSync/cwd/chdir/pid/platform/kill/on(SIGINT/SIGTERM)
    emit_date.go      Date: construction, getters/setters, parse, arithmetic, formatting
    emit_dynamic.go   any/unknown as a runtime-tagged {tag, payload} value
    emit_async.go, emit_promise.go   async/await, Promise<T> (real non-blocking
                     await on fetch()'s Promise<Response>; every other
                     Promise<T> is a resolved-slot read), Promise.all/race/allSettled
    emit_fetch.go     fetch(url[, init]) and Response, backed by libcurl's
                     multi-interface — non-blocking, driven by the same
                     select()-based event loop as http.listen
    emit_fs.go        fs.readFileSync/writeFileSync/appendFileSync/existsSync/unlinkSync/mkdirSync/rmdirSync/renameSync/copyFileSync/readdirSync
    emit_path.go      path.join/resolve/dirname/basename/extname/isAbsolute/parse/format
    emit_url.go       URL/URLSearchParams (backed by libcurl's URL API)
    emit_arraybuffer.go  ArrayBuffer + TypedArrays (Int8Array…Float64Array)
    emit_http.go      http.listen(port, handler): request dispatch, Request/Response wiring on top of the event loop/fiber scheduler
    emit_timers.go    setTimeout/clearTimeout/setInterval/clearInterval
    emit_memory.go    Memory.free(x): manual heap release (Stage 1 of the memory-management plan)
    runtime_*.go      ensure* C-runtime declarations (malloc, printf, sscanf, …) and the hand-written select()-based event loop + ucontext.h fiber scheduler backing http.listen and non-blocking fetch, split by domain to pair with the emit_*.go file that uses them
    gcshim.go         //go:embed of gcsrc/gcshim.c, the -mm=gc allocator shim's source
    gclocate.go       LocateGC(): portable pkg-config-based Boehm GC discovery for -mm=gc builds
    gcsrc/gcshim.c    -mm=gc's C shim: malloc/calloc/realloc/free forwarding to GC_malloc/GC_realloc/GC_free (its own subdirectory since a .c file directly in a Go package dir makes `go build` demand cgo)
docs/
  adr/              Architecture Decision Records: one per feature/bugfix, numbered, never renumbered
  tdd/              Technical Design Documents: scoping/design work for big features, referenced from docs/status/
  status/           Implementation status: docs/status/README.md is a scannable index (coverage % + caveats per area), one page per feature area for the full detail
  testing/          Test262 conformance results — generated, not hand-written; see "Test262 conformance" above
docker/             Dockerfiles verifying --static (+ fetch, + RegExp) actually runs in a scratch image, and -mm=gc actually runs on Linux/musl
.github/
  workflows/        GitHub Actions: test + automated SemVer releases (see VERSIONING.md)
examples/           Sample .ts files: each compiles to a native binary, all wired into `make examples`
jsdoc/              JSDoc comment parser (@type annotations for the cases TS types can't express)
lexer/              Tokeniser
parser/             Recursive-descent parser with Pratt precedence climbing
resolver/           Module resolver: parses the entry file's transitive imports, merges into one AST
main.go             CLI entry point
tests/              End-to-end tests (parse → IR → clang → run → assert on stdout), split by feature area; shared harness in tests/compiler_test.go
tools/
  conformance/      Test262 runner — fetch.sh, main.go, harness-shim/; see "Test262 conformance" above
  httpbin-lite/     Local HTTP fixture server backing `make examples`'s fetch/http examples (ADR-00096)
VERSIONING.md       SemVer policy + the automated release mechanism
Makefile            Build, test, and example targets
```

## How it works

1. **Lex**: `lexer.Tokenize` produces a flat token slice.
2. **Parse**: `parser.Parse` builds an AST; expressions use Pratt-style precedence climbing.
3. **Emit**: `llvm.NewEmitter().EmitProgram` walks the AST and writes LLVM IR text. The load-bearing tricks:
   - Two-builder pattern: `allocas` (entry-block allocas) and `body` (everything else), merged at function end.
   - `freshReg()` / `freshLabel()` mint unique SSA names; nothing is ever hand-numbered.
   - A scope stack for symbol resolution, plus a two-pass setup so functions can forward-reference each other.
   - Arrays are `{ptr, i64}` aggregates; objects are heap-allocated structs reached via GEP; closures are heap-allocated `{funcPtr, envPtr}` pairs; exceptions are `setjmp`/`longjmp` with a 64-slot jump-buffer stack; `any`/`unknown` are a boxed `{tag, payload}` pair with runtime-dispatched `typeof`/print/equality.
   - `ensure*()` pattern: every C stdlib dependency (`malloc`, `sscanf`, `gmtime`, you name it) gets declared exactly once, the first time it's actually needed.
   - Concurrency, such as it is: a `select()`-based event loop merges the listening socket, every open `http.listen` connection, libcurl's own fds (for `fetch`), and the timer queue into one wait. Actual suspension is `ucontext.h` fibers, not LLVM's coroutine intrinsics — a direct prototype proved coroutines segfault the moment a `try`/`catch` spans a suspend point, since this compiler's `setjmp`/`longjmp` exceptions assume a C stack frame that a coroutine's suspend unwinds out from under them. Fibers keep their own OS stack, so they don't have that problem. No thread pool, no preemption: exactly one fiber runs at a time, cooperatively, same as JS's own single-threaded model.
4. **Compile**: the emitter writes a `.ll` file next to the source, then shells out to `clang -O2` for the actual native codegen. KlainMainLang does the fun 90% and quietly lets a real compiler backend handle the part that would otherwise take a PhD.

## Things this compiler will cheerfully never do

- Collect garbage, automatically — *by default*. `manual` mode (the default `-mm` value) never frees anything on its own (the one automatic exception: a `Promise`'s slot gets freed the moment `await` reads it), and `Memory.free(x)` (see [`docs/status/MEMORY-MANAGEMENT.md`](docs/status/MEMORY-MANAGEMENT.md)) is there if you want to free something by hand, C-style footguns and all. Left in `manual` mode, your program's memory footprint is a monotonically increasing function of its runtime: a *feature* for short-lived CLI tools and a *life choice* for anything long-running. If you actually want automatic collection, `-mm=gc` opts into a real one (Boehm) — see the CLI flags section above.
- Let an imported file run side-effecting top-level code. `import`/`export` exist (including true per-file scoping and aliasing), but an imported file may only contain declarations; everything still boils down to one merged AST and one `main()` behind the scenes.
- Judge you for using `var`. (It'll just quietly treat it like `let`. We've all been there.)

If any of that sounds like a dealbreaker, this was never going to be your compiler anyway, and that's fine. For everything it *does* do, [`docs/status/`](docs/status/README.md) has the receipts.
