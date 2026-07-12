# KlainMainLang

A TypeScript-to-native compiler, written in Go, that emits LLVM IR and hands it to `clang`. You write `.ts`, it writes `.ll`, `clang` writes a real executable, your operating system is none the wiser.

Why does this exist? Because "how would I even build a compiler" is a much more fun rabbit hole than whatever I was supposed to be doing that day. It has since grown a garbage collector's worth of features (well — minus the garbage collector; see below) and a small mountain of design-decision paperwork in `docs/adr/`.

> **⚠️ Personal / experimental project.** One person, building this for fun, learning how compilers actually work by making all the mistakes personally. Not audited, not hardened, no stability guarantees between commits, and never destined for a production pipeline near you. It leaks memory on purpose (see below) and is enthusiastically fine with that. Perfect for tinkering, small CLI toys, and impressing exactly one (1) person at a dinner party. Bring your own garbage collector.

## What actually works right now

The honest, itemized answer lives in **[`STATUS.md`](STATUS.md)** — a feature-by-feature matrix with coverage percentages, because vague marketing copy is worse than a spreadsheet. Current scorecard: roughly **74% of core TypeScript language features**, **~88% of Node.js-style APIs** (`fs`, `process`, and friends), and a much scrappier **~21% of genuine browser/WHATWG-style Web Platform APIs** (`fetch` exists; `setTimeout` doesn't — priorities are a journey, not a destination).

Every feature and bug fix in this repo comes with a matching entry in **[`docs/adr/`](docs/adr/README.md)** — a paper trail of what was tried, what broke, and why a given weird decision was made on purpose rather than by accident. If you ever wonder "wait, why does `Date.parse` return `-1` instead of `NaN`?", the answer is in there, in more detail than is strictly healthy.

Want to see it in action instead of reading about it? Every language feature has a runnable example under **[`examples/`](examples/)** — no README code snippets to go stale, just `.ts` files that actually compile and run (verified by `make examples`, every time).

Releases follow [Semantic Versioning](https://semver.org/), applied automatically from Conventional Commit messages via GitHub Actions — see **[`VERSIONING.md`](VERSIONING.md)** for the exact scheme and what still has to be true before this hits `1.0.0`.

## Requirements

- Go 1.21+
- `clang` (LLVM 15+ — needs opaque-pointer support)
- `libcurl` — only if the program you're compiling actually calls `fetch`; every other program stays plain-libc, no extra install needed

## Quick start

```sh
# Build the compiler
make build          # produces ./KlainMainLang

# Compile a TypeScript file to a native binary (does NOT run it)
./KlainMainLang examples/basics/basics.ts
# → produces examples/basics/basics

# Run the binary yourself
./examples/basics/basics

# Specify a custom output name
./KlainMainLang -o myapp examples/basics/basics.ts
./myapp

# Compile and run in one step
make run FILE=examples/basics/basics.ts

# Inspect the generated LLVM IR (in case you, too, enjoy pain)
make ir FILE=examples/basics/basics.ts
```

## Make targets

| Target | Description |
|---|---|
| `make build` | Compile the KlainMainLang compiler to `./KlainMainLang` |
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
| `make clean` | Remove compiler binary and compiled example artifacts |

## CLI flags

```sh
KlainMainLang [flags] <file.ts>

  --emit-llvm   Emit LLVM IR to stdout and stop (do not compile)
  -o <name>     Output binary name (default: input path without .ts)
  --static      Statically link the output binary — for a scratch/distroless
                Docker image with nothing else in it. Linux only: run
                KlainMainLang itself on Linux to use this. macOS's linker has
                no static-libc support at all (Apple ships no static
                libSystem/crt0.o, by design) — KlainMainLang refuses --static
                immediately with an explanation rather than surfacing a
                confusing linker error.
```

Every other compiled binary here is dynamically linked (against libSystem on
macOS, glibc on Linux, plus `libcurl` if the program calls `fetch`) — closer
to typical C/C++ toolchain output than a normal Go binary's usual
self-contained default. `--static` closes that gap on Linux, verified
end-to-end against real Docker builds — see `docker/Dockerfile` for a plain
example and `docker/Dockerfile.fetch-test` for one using `fetch` too.
A `fetch`-using program needs curl's *entire* static dependency chain listed
explicitly at link time (static archives don't auto-pull their own
dependencies the way shared libraries do), and — on Alpine/musl, at least —
a two-step `clang`-then-`gcc` link rather than a single `clang` invocation,
since some of Alpine's static archives are LTO-built in a format clang's
linker can't consume but gcc's can. See `docs/adr/ADR-00033.md` for the full
recipe and investigation; this compiler doesn't attempt to automate it
itself, since the exact package list/workaround is specific to one distro's
build choices, not a portable fact this compiler could bake in safely.

## The pipeline, in one breath

```
Lexer → Parser (recursive descent, Pratt precedence climbing) → Module resolver → LLVM IR emitter → clang -O2 → a binary that runs on your machine, unsupervised
```

`import`/`export` exist, but don't expect a real linker anywhere in there — the module resolver parses every file your entry file imports, merges them all into one AST, and hands *that* to the emitter. One `.ll`, one `clang` call, one generated `main()` either way. Imported files may only contain declarations (functions, types, that sort of thing) — no top-level side effects yet, only the file you actually pointed the compiler at gets to have opinions at runtime.

## Project layout

```
ast/                AST node definitions
codegen/
  llvm/             LLVM IR emitter, split by concern:
    emitter.go        core struct, scope stack, EmitProgram, pre-passes
    types.go          type system (IR types, FuncSig, StructIR)
    runtime.go        ensure* C-runtime declarations (malloc, printf, sscanf, …)
    emit_stmts.go     statements: for/while/do-while/if/switch/try/labeled break…
    emit_exprs.go     expressions, type inference, var declarations
    emit_strings.go   string operations (concat, methods, template literals)
    emit_arrays.go    array mutations, HOF (map/filter/reduce/sort/…)
    emit_objects.go   objects, Object.keys/values/entries/groupBy, spread
    emit_func.go      functions, closures, callbacks
    emit_call.go      call dispatch: console, JSON, Math, Number, Date statics
    emit_collections.go  Map<K,V> and Set<T>
    emit_exceptions.go   try/catch/throw (setjmp/longjmp)
    emit_process.go   process.argv/env/exit/readLineSync/execFileSync/cwd/chdir/pid/platform/kill
    emit_date.go      Date: construction, getters/setters, parse, arithmetic, formatting
    emit_dynamic.go   any/unknown as a runtime-tagged {tag, payload} value
    emit_async.go     async/await, Promise<T> (synchronous V1 — no event loop yet)
    emit_fetch.go     fetch(url) and Response, backed by libcurl (GET only)
    emit_fs.go        fs.readFileSync/writeFileSync/appendFileSync/existsSync/unlinkSync/mkdirSync/rmdirSync/renameSync/copyFileSync/readdirSync
docs/
  adr/              Architecture Decision Records — one per feature/bugfix, numbered, never renumbered
examples/           Sample .ts files — each compiles to a native binary, all wired into `make examples`
jsdoc/              JSDoc comment parser (@type annotations for the cases TS types can't express)
lexer/              Tokeniser
parser/             Recursive-descent parser with Pratt precedence climbing
resolver/           Module resolver — parses the entry file's transitive imports, merges into one AST
main.go             CLI entry point
compiler_test.go    End-to-end tests (parse → IR → clang → run → assert on stdout)
STATUS.md           The actual, current, itemized feature matrix — trust this over any prose
Makefile            Build, test, and example targets
```

## How it works

1. **Lex** — `lexer.Tokenize` produces a flat token slice.
2. **Parse** — `parser.Parse` builds an AST; expressions use Pratt-style precedence climbing.
3. **Emit** — `llvm.NewEmitter().EmitProgram` walks the AST and writes LLVM IR text. The load-bearing tricks:
   - Two-builder pattern: `allocas` (entry-block allocas) and `body` (everything else), merged at function end.
   - `freshReg()` / `freshLabel()` mint unique SSA names — nothing is ever hand-numbered.
   - A scope stack for symbol resolution, plus a two-pass setup so functions can forward-reference each other.
   - Arrays are `{ptr, i64}` aggregates; objects are heap-allocated structs reached via GEP; closures are heap-allocated `{funcPtr, envPtr}` pairs; exceptions are `setjmp`/`longjmp` with a 64-slot jump-buffer stack; `any`/`unknown` are a boxed `{tag, payload}` pair with runtime-dispatched `typeof`/print/equality.
   - `ensure*()` pattern: every C stdlib dependency (`malloc`, `sscanf`, `gmtime`, you name it) gets declared exactly once, the first time it's actually needed.
4. **Compile** — the emitter writes a `.ll` file next to the source, then shells out to `clang -O2` for the actual native codegen. KlainMainLang does the fun 90% and quietly lets a real compiler backend handle the part that would otherwise take a PhD.

## Things this compiler will cheerfully never do

- Collect garbage. Almost every heap allocation is `malloc`'d and never `free`d (the one principled exception: a `Promise`'s slot gets freed the moment `await` reads it — everything else just... accumulates). Your program's memory footprint is a monotonically increasing function of its runtime — this is a *feature* for short-lived CLI tools and a *life choice* for anything long-running.
- Let an imported file run side-effecting top-level code, or give two unrelated files their own private scope. `import`/`export` exist, but only for sharing declarations — everything still boils down to one merged AST and one `main()` behind the scenes.
- Judge you for using `var`. (It'll just quietly treat it like `let`. We've all been there.)

If any of that sounds like a dealbreaker, this was never going to be your compiler anyway — and that's fine. For everything it *does* do, `STATUS.md` has the receipts.
