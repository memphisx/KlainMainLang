package main

import (
	"KlainMainLang/codegen/llvm"
	"KlainMainLang/resolver"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	emitLLVM := flag.Bool("emit-llvm", false, "emit LLVM IR and stop")
	output := flag.String("o", "", "output binary name (default: input name without extension)")
	static := flag.Bool("static", false, "statically link the output binary — for minimal/scratch Docker images. Linux only: run klainmain itself on Linux to use this (macOS's linker has no static-libc support at all, by design)")
	mm := flag.String("mm", "manual", "memory management mode: manual (default, Memory.free(x) only) or gc (Boehm GC — see docs/tdd/TDD-00001.md)")
	compat := flag.String("compat", "strict", "compatibility mode (see docs/tdd/TDD-00075.md): strict (default — the compiler's opinionated, safer-than-JS semantics; e.g. a declaration colliding with an ambient built-in name like Math/fetch is a compile error) or js (best-effort JS-faithful — e.g. real-JS/browser global shadowing). Governs the strict-vs-JS behaviors that used to live behind -globals")
	regex := flag.String("regex", "", "RegExp `dialect` (see docs/tdd/TDD-00067.md): es-unicode (default — ECMAScript matching via PCRE2_UTF + NEWLINE_ANY), ecmascript (es-unicode plus the Option C source-normalization pass — exact dot line-terminator semantics), es-utf16 (es-unicode plus true UTF-16 code-unit indices for .search/lastIndex/replace-callback offsets), es-ascii (cheaper ASCII-faithful option alignment only), or pcre (raw PCRE2, no ES wrapping)")
	bigint := flag.String("bigint", "libtommath", "bigint backend library (see docs/tdd/TDD-00074.md), linked only when a program uses bigint: libtommath (default, public domain) or gmp (LGPL, faster). Both give identical arbitrary-precision semantics")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: klainmain [flags] <file.ts>")
		fmt.Fprintln(os.Stderr, "\nCompiles a TypeScript file to a native binary (TypeScript → LLVM IR → clang).")
		fmt.Fprintln(os.Stderr, "\nFlags:")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	if *static && runtime.GOOS != "linux" {
		fatal("--static is only supported when compiling on Linux (this run is on %s). This is not a missing-package issue: static linking needs a static libc to link against, and macOS's linker ships none at all — Apple deliberately never provides a static libSystem/crt0.o, with no workaround. To produce a statically-linked binary for a scratch/distroless Docker image, run klainmain itself on Linux — e.g. a Linux build stage in a multi-stage Dockerfile (build the compiler and your program there, then copy just the resulting static binary into a scratch final stage).", runtime.GOOS)
	}

	switch *mm {
	case "manual", "gc":
		// ok
	case "auto":
		fatal("-mm=auto is not implemented yet (see docs/tdd/TDD-00001.md) — use -mm=manual or -mm=gc")
	default:
		fatal("unrecognized -mm value %q — must be one of: manual, gc (auto is scoped but not implemented yet, see docs/tdd/TDD-00001.md)", *mm)
	}

	switch *compat {
	case "strict", "js":
		// ok
	default:
		fatal("unrecognized -compat value %q — must be one of: strict (default), js (see docs/tdd/TDD-00075.md)", *compat)
	}

	switch *regex {
	case "", "pcre", "es-ascii", "es-unicode", "es-utf16", "ecmascript":
		// ok ("" == default, resolves to es-unicode)
	default:
		fatal("unrecognized -regex value %q — must be one of: ecmascript, es-unicode (default), es-utf16, es-ascii, pcre (see docs/tdd/TDD-00067.md)", *regex)
	}

	switch *bigint {
	case "libtommath", "gmp":
		// ok
	default:
		fatal("unrecognized -bigint value %q — must be one of: libtommath (default), gmp (see docs/tdd/TDD-00074.md)", *bigint)
	}

	inFile := flag.Arg(0)
	prog, err := resolver.ResolveProgramWithOptions(inFile, *compat == "js")
	if err != nil {
		fatal("parse error: %v", err)
	}

	em := llvm.NewEmitter()
	em.SetMemMode(*mm)
	em.SetRegexMode(*regex)
	em.SetBigIntBackend(*bigint)
	em.SetCompatMode(*compat)
	ir, err := em.EmitProgram(prog)
	if err != nil {
		fatal("codegen error: %v", err)
	}

	if *emitLLVM {
		fmt.Print(ir)
		return
	}

	// Write IR to a temp file, then compile with clang.
	llFile := strings.TrimSuffix(inFile, filepath.Ext(inFile)) + ".ll"
	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		fatal("cannot write IR: %v", err)
	}

	outBin := *output
	if outBin == "" {
		outBin = strings.TrimSuffix(inFile, filepath.Ext(inFile))
	}

	clangArgs := []string{"-O2", llFile}
	if *mm == "gc" {
		gcShimPath := strings.TrimSuffix(inFile, filepath.Ext(inFile)) + ".gcshim.c"
		if err := os.WriteFile(gcShimPath, []byte(llvm.GCShimSource), 0644); err != nil {
			fatal("cannot write GC shim: %v", err)
		}
		clangArgs = append(clangArgs, gcShimPath)
	}
	clangArgs = append(clangArgs, "-o", outBin)
	if *static {
		clangArgs = append(clangArgs, "-static")
	}
	if *mm == "gc" {
		cflags, libs, err := llvm.LocateGC()
		if err != nil {
			fatal("gc mode: %v", err)
		}
		clangArgs = append(clangArgs, cflags...)
		clangArgs = append(clangArgs, libs...)
	}
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, "-l"+lib)
	}
	// bigint: compile the selected backend's C file (which implements the
	// __kml_bigint_* ABI) alongside the program and link its library — only when
	// the program actually used bigint. Same shape as the -mm=gc shim above.
	if em.UsesBigInt() {
		backend := em.BigIntBackend()
		src, ok := llvm.BigIntBackendSource(backend)
		if !ok {
			fatal("bigint: unknown backend %q", backend)
		}
		biPath := strings.TrimSuffix(inFile, filepath.Ext(inFile)) + ".bigint.c"
		if err := os.WriteFile(biPath, []byte(src), 0644); err != nil {
			fatal("cannot write bigint backend source: %v", err)
		}
		clangArgs = append(clangArgs, biPath)
		cflags, libs := llvm.LocateBigInt(backend)
		clangArgs = append(clangArgs, cflags...)
		clangArgs = append(clangArgs, libs...)
	}
	// JSON parse-tree (TDD-00077 Track P): compile the self-contained JSON
	// parser (implementing the __kml_json_* ABI, libc only) alongside the program
	// — only when it uses JSON.parse/Response.json(). Same shape as bigint above,
	// minus any external library to link.
	if em.UsesJSONParse() {
		jsonPath := strings.TrimSuffix(inFile, filepath.Ext(inFile)) + ".jsontree.c"
		if err := os.WriteFile(jsonPath, []byte(llvm.JSONParseTreeSource()), 0644); err != nil {
			fatal("cannot write JSON parse-tree source: %v", err)
		}
		clangArgs = append(clangArgs, jsonPath)
	}
	cmd := exec.Command("clang", clangArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fatal("clang: %v", err)
	}

	fmt.Fprintf(os.Stderr, "compiled: %s\n", outBin)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "klainmain: "+format+"\n", args...)
	os.Exit(1)
}
