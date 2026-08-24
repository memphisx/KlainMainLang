# KlainMainLang

A TypeScript-to-native compiler, written in Go, that emits LLVM IR and hands it to `clang`. You write `.ts`, it writes `.ll`, `clang` writes a real executable, your operating system is none the wiser.

Why does this exist? Not because TypeScript-to-native compilation needed solving. Microsoft's own research arm already tried this properly, with an actual team, an actual budget, and an actual production use case ([Static TypeScript](https://www.microsoft.com/en-us/research/publication/static-typescript/), compiling a restricted TypeScript subset to native code for microcontrollers), and this project has none of the above going for it. It exists because "how would I even build a compiler" is a much more fun rabbit hole than whatever I was supposed to be doing that day. The name is the actual mission statement. Κλάιν Μάιν (Klain Main) is Greek slang for "I don't care": build it anyway, for no better reason than "because I can." It has since grown a garbage collector's worth of features (including, as of `-mm=gc`, an actual garbage collector — opt-in, see below) and a small mountain of design-decision paperwork in `docs/adr/`.

> **⚠️ Personal / experimental project.** One person, building this for fun, learning how compilers actually work by making all the mistakes smarter people already made, and already fixed, in better languages, ages ago. Not audited, not hardened, no stability guarantees between commits, and never destined for a production pipeline near you. It leaks memory on purpose by default (see below) and is enthusiastically fine with that. Perfect for tinkering, small CLI toys, and impressing exactly one (1) person at a dinner party. Bring your own garbage collector, or just pass `-mm=gc` and let it bring one for you.

## What actually works right now

The honest, itemized answer lives in **[`docs/status/README.md`](docs/status/README.md)**: a feature-by-feature matrix, one page per feature area, with coverage percentages kept current as things ship — trust it over any claim here if the two ever disagree. It also has a "Fidelity Gaps" section for features marked done that still have real, non-cosmetic differences from actual JS/TS behavior worth knowing before you rely on them.

Broad strokes:

- **Core TypeScript — solid.** Control flow, operators, closures, full classes (inheritance, access modifiers, getters/setters), generics, enums, interfaces, destructuring, generators, tuples, unions, `bigint`, `RegExp`.
- **Node.js APIs — substantial.** `fs`, `process`, `path`, `os`, `http.listen`, `EventEmitter`, `worker_threads` (real OS threads).
- **Web Platform APIs — a mixed bag.** `fetch`, `URL`, `WebSocket`, `ArrayBuffer`/TypedArrays are in; plenty around them isn't.
- **Gaps you'll trip over first.** Async is mostly synchronous under the hood except `await fetch`; no `Proxy` or dynamic property bags, by design.

Oh, and it fuzzes itself — lexer, parser, and the whole parse-to-binary pipeline (`make fuzz` / `fuzz-codegen` / `fuzz-all`).

Every feature and bug fix comes with a matching entry in **[`docs/adr/`](docs/adr/README.md)**: a paper trail of what was tried, what broke, and why a given weird decision was made on purpose rather than by accident. Bigger features get scoped out in **[`docs/tdd/`](docs/tdd/README.md)** first: a design doc written before any code exists. Both indexes are cross-linked from `docs/status/` wherever relevant, rather than repeated here.

Want to see it in action instead of reading about it? Every language feature has a runnable example under **[`examples/`](examples/)**: no README code snippets to go stale, just `.ts` files that actually compile and run (verified by `make examples`, every time).

Releases follow [Semantic Versioning](https://semver.org/), applied automatically from Conventional Commit messages via GitHub Actions. See **[`VERSIONING.md`](VERSIONING.md)** for the exact scheme and what still has to be true before this hits `1.0.0`.

## Requirements

- Go 1.26+ (see `go.mod` for the exact pinned version)
- `clang` (LLVM 15+, needs opaque-pointer support)
- `libcurl`, needed if the compiled program calls `fetch` **or** `http.listen` — the HTTP server's event loop links libcurl unconditionally so it can merge `fetch`'s non-blocking transfers into the same `select()` loop, even in a server that never calls `fetch` itself. Every other program stays plain-libc, no extra install needed
- `bdw-gc`/`libgc` (the Boehm-Demers-Weiser garbage collector), needed only if compiling with `-mm=gc` — `brew install bdw-gc` on macOS, `apt-get install libgc-dev` on Debian/Ubuntu, `apk add gc-dev` on Alpine. The default `manual` mode needs nothing beyond plain libc
- `libpcre2-8`/`libpcre2-dev`, needed if the compiled program uses `RegExp` (either `new RegExp(...)` or a `/pattern/flags` literal) — `apt-get install libpcre2-dev` on Debian/Ubuntu, `brew install pcre2` on macOS, `apk add pcre2-dev` on Alpine. Same conditional-linking convention as `libcurl`: every other program stays plain-libc. See [docs/status/REGEXP.md](docs/status/REGEXP.md)
- a bigint backend library, needed only if the compiled program uses `bigint` — `libtommath` by default (`brew install libtommath` / `apt-get install libtommath-dev` / `apk add libtommath-dev`), or GMP with `-bigint=gmp` (`brew install gmp` / `apt-get install libgmp-dev`). Same conditional-linking convention: a program without bigint stays plain-libc. See [docs/tdd/TDD-00074.md](docs/tdd/TDD-00074.md)
- a crypto backend library, needed only if the compiled program uses `crypto.subtle` — OpenSSL 3's libcrypto by default (`brew install openssl@3` / `apt-get install libssl-dev` / `apk add openssl-dev`), or Apple CommonCrypto + Security.framework with `-crypto=commoncrypto` (macOS only, ships with the OS — no install at all). Same conditional-linking convention: a program without `crypto.subtle` stays plain-libc (`crypto.getRandomValues`/`randomUUID` use the OS CSPRNG directly, no library). See [docs/tdd/TDD-00104.md](docs/tdd/TDD-00104.md)

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
| `make test-par` | Same tests, sharded across `SHARDS` (default 4) parallel processes for a faster local pre-check — ~1.5–2× (the E2E suite is subprocess/IO-bound, so more shards just thrash); any failure is re-run serially so a parallel-unsafe test doesn't flake the run. `make test` stays the source of truth |
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

```text
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
  
  -bigint <lib> BigInt backend library, linked only when a program uses
                bigint: libtommath (default, public domain) or gmp (LGPL,
                faster). Both give identical arbitrary-precision semantics —
                the flag only trades license/speed. See docs/tdd/TDD-00074.md.
  
  -crypto <lib> crypto.subtle backend library, compiled+linked only when a
                program uses crypto.subtle: openssl (default — libcrypto
                3.x, all platforms) or commoncrypto (macOS only — Apple
                CommonCrypto plus Security.framework, no OpenSSL
                dependency). Both give identical Web Crypto semantics.
                See docs/tdd/TDD-00104.md.
  
  -compat <m>   Compatibility mode (see docs/tdd/TDD-00075.md): strict
                (default — the compiler's opinionated, safer-than-JS
                semantics; e.g. a declaration colliding with an ambient
                built-in like Math/fetch is a compile error) or js (best-
                effort JS-faithful — e.g. real-JS/browser global shadowing).
                Constructor-style Map/Date/RegExp stay reserved either way.
  
  -regex <m>    RegExp dialect: es-unicode (default) / ecmascript / es-utf16 /
                es-ascii / pcre. See docs/tdd/TDD-00067.md.
```

Run `klainmain` with no file (or `klainmain --help`) to print this list with
the full per-flag descriptions.

Every other compiled binary is dynamically linked (libSystem on macOS, glibc
on Linux, plus `libcurl` for `fetch`/`http.listen` and `libpcre2-8` for
`RegExp`) — closer to typical C/C++ toolchain output than a self-contained Go
binary. `--static` closes that gap on Linux, verified end-to-end in real
`scratch` Docker builds (`docker/Dockerfile*`). One caveat: a `fetch`-using
static binary needs curl's *entire* dependency chain spelled out at link time,
plus a `clang`-then-`gcc` two-step on Alpine/musl — too distro-specific to
safely automate, so the full recipe lives in `docs/adr/` rather than in the
compiler. `RegExp`'s pcre2 has none of that drama and just links statically.

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

`import`/`export` exist, but don't expect a real linker anywhere in there. The module resolver parses every file your entry file imports, merges them all into one AST, and hands *that* to the emitter — one `.ll`, one `clang` call, one generated `main()`. An imported file's top-level code now actually runs (once, in dependency order, before whatever imported it); only files tangled up in an import *cycle* are still held to declarations-only, on the theory that circular modules reading each other's half-built state is a horror best left to languages with therapists on staff.

## Project layout

```
ast/                AST node definitions
codegen/
  llvm/             LLVM IR emitter — ~60 small domain files, not a handful of
                     giant ones: emit_*.go per feature area, runtime_*.go for
                     the C-runtime decls + the select()/ucontext fiber engine,
                     gc* for the -mm=gc shim. Full file-by-file map lives in
                     docs/ARCHITECTURE.md (not re-typed here to go stale)
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
   - Concurrency, such as it is: a `select()`-based event loop merges the listening socket, every open `http.listen` connection, libcurl's own fds (for `fetch`), and the timer queue into one wait. Actual suspension is `ucontext.h` fibers, not LLVM's coroutine intrinsics — a direct prototype proved coroutines segfault the moment a `try`/`catch` spans a suspend point, since this compiler's `setjmp`/`longjmp` exceptions assume a C stack frame that a coroutine's suspend unwinds out from under them. Fibers keep their own OS stack, so they don't have that problem. Within one thread there is no preemption: exactly one fiber runs at a time, cooperatively, same as JS's own single-threaded model. Real parallelism exists one level up — `worker_threads`' `Worker` spawns actual pthreads, each running its own independent instance of this same loop (every runtime singleton is `thread_local`), talking to the parent over pipe-based message channels.
4. **Compile**: the emitter writes a `.ll` file next to the source, then shells out to `clang -O2` for the actual native codegen. KlainMainLang does the fun 90% and quietly lets a real compiler backend handle the part that would otherwise take a PhD.

## Things this compiler will cheerfully never do

- Collect garbage, automatically — *by default*. `manual` mode (the default `-mm` value) never frees anything on its own (the one automatic exception: a `Promise`'s slot gets freed the moment `await` reads it), and `Memory.free(x)` (see [`docs/status/MEMORY-MANAGEMENT.md`](docs/status/MEMORY-MANAGEMENT.md)) is there if you want to free something by hand, C-style footguns and all. Left in `manual` mode, your program's memory footprint is a monotonically increasing function of its runtime: a *feature* for short-lived CLI tools and a *life choice* for anything long-running. If you actually want automatic collection, `-mm=gc` opts into a real one (Boehm) — see the CLI flags section above.
- Grow a real linker. `import`/`export` exist (true per-file scoping, aliasing, and imported files now run their top-level code in dependency order), but there is no separate compilation and no link step: every module you touch gets flattened into one AST and one `main()` behind the scenes. Turtles all the way down, except it's one big turtle.
- Judge you for using `var`. It even does `var` *properly* now — function-scoped, hoisted, re-declarable — instead of the old cop-out of quietly pretending it was `let`. Personal growth.

If any of that sounds like a dealbreaker, this was never going to be your compiler anyway, and that's fine. For everything it *does* do, [`docs/status/`](docs/status/README.md) has the receipts.

## License

Copyright (C) 2026 Kyriakos Bompotis.

This program is free software: you can redistribute it and/or modify it under
the terms of the **GNU Affero General Public License** as published by the Free
Software Foundation, either version 3 of the License, or (at your option) any
later version (`AGPL-3.0-or-later`). See [`LICENSE`](LICENSE) for the full text.

It is distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY;
without even the implied warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR
PURPOSE. The AGPL's §13 additionally requires that anyone who runs a modified
version to interact with users over a network make the modified source
available to those users.

As the sole copyright holder, the author reserves the right to offer the
software under separate commercial terms as well.
