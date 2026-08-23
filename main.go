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
	output := flag.String("o", "", "output binary `name` (default: the input name without its extension)")
	static := flag.Bool("static", false, "statically link the output binary — for minimal/scratch Docker images. Linux only: run klainmain itself on Linux to use this (macOS's linker has no static-libc support at all, by design)")
	mm := flag.String("mm", "manual", "memory management `mode`: manual (default, Memory.free(x) only) or gc (Boehm GC — allocations are collected automatically; needs bdw-gc/libgc installed)")
	compat := flag.String("compat", "strict", "compatibility `mode`: strict (default — the compiler's opinionated, safer-than-JS semantics; e.g. a declaration colliding with an ambient built-in name like Math/fetch is a compile error) or js (best-effort JS-faithful — e.g. real-JS/browser global shadowing)")
	regex := flag.String("regex", "", "RegExp `dialect`: es-unicode (default — ECMAScript matching via PCRE2_UTF + NEWLINE_ANY), ecmascript (es-unicode plus a source-normalization pass — exact dot line-terminator semantics), es-utf16 (es-unicode plus true UTF-16 code-unit indices for .search/lastIndex/replace-callback offsets), es-ascii (cheaper ASCII-faithful option alignment only), or pcre (raw PCRE2, no ES wrapping)")
	bigint := flag.String("bigint", "libtommath", "bigint backend `library`, linked only when a program uses bigint: libtommath (default, public domain) or gmp (LGPL, faster). Both give identical arbitrary-precision semantics")
	cryptoBackend := flag.String("crypto", "openssl", "crypto.subtle backend `library`, compiled+linked only when a program uses crypto.subtle: openssl (default — libcrypto 3.x, all platforms) or commoncrypto (macOS only — Apple CommonCrypto plus Security.framework, no OpenSSL dependency). Both give identical Web Crypto semantics")
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintln(out, "usage: klainmain [flags] <file.ts>")
		fmt.Fprintln(out, "\nCompiles a TypeScript file to a native binary (TypeScript → LLVM IR → clang).")
		fmt.Fprintln(out, "\nFlags:")
		// Custom rendering instead of flag.PrintDefaults(): each description is
		// word-wrapped and indented under its flag name, so the long mode/dialect
		// explanations read as an aligned block rather than one runaway line.
		flag.VisitAll(func(f *flag.Flag) {
			placeholder, usage := flag.UnquoteUsage(f)
			head := "  -" + f.Name
			if placeholder != "" {
				head += " <" + placeholder + ">"
			}
			fmt.Fprintln(out, head)
			for _, line := range wrapText(usage, 74) {
				fmt.Fprintln(out, "      "+line)
			}
			fmt.Fprintln(out)
		})
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
		fatal("-mm=auto is not implemented yet — use -mm=manual or -mm=gc")
	default:
		fatal("unrecognized -mm value %q — must be one of: manual, gc (auto is not implemented yet)", *mm)
	}

	switch *compat {
	case "strict", "js":
		// ok
	default:
		fatal("unrecognized -compat value %q — must be one of: strict (default), js", *compat)
	}

	switch *regex {
	case "", "pcre", "es-ascii", "es-unicode", "es-utf16", "ecmascript":
		// ok ("" == default, resolves to es-unicode)
	default:
		fatal("unrecognized -regex value %q — must be one of: ecmascript, es-unicode (default), es-utf16, es-ascii, pcre", *regex)
	}

	switch *bigint {
	case "libtommath", "gmp":
		// ok
	default:
		fatal("unrecognized -bigint value %q — must be one of: libtommath (default), gmp", *bigint)
	}

	switch *cryptoBackend {
	case "openssl":
		// ok
	case "commoncrypto":
		if runtime.GOOS != "darwin" {
			fatal("-crypto=commoncrypto is only supported when compiling on macOS (this run is on %s) — CommonCrypto and Security.framework are Apple system libraries with no ports elsewhere. Use the default -crypto=openssl instead", runtime.GOOS)
		}
		if *static {
			fatal("-crypto=commoncrypto cannot be combined with --static: Apple frameworks are dynamic-only (and --static itself is Linux-only). Use -crypto=openssl for static builds")
		}
	default:
		fatal("unrecognized -crypto value %q — must be one of: openssl (default), commoncrypto (macOS only)", *cryptoBackend)
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
	em.SetCryptoBackend(*cryptoBackend)
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
	if em.UsesWorkers() {
		// Worker threads (worker_threads): the first and only pthread
		// dependency; -pthread covers both compile and link phases.
		clangArgs = append(clangArgs, "-pthread")
	}
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
	// crypto.subtle (TDD-00104): compile the selected crypto backend's C file
	// (which implements the __kml_crypto_* subtle ABI) alongside the program
	// and link its library/frameworks — only when the program actually used
	// crypto.subtle. Same shape as bigint above.
	if em.UsesCrypto() {
		backend := em.CryptoBackend()
		src, ok := llvm.CryptoBackendSource(backend)
		if !ok {
			fatal("crypto: unknown backend %q", backend)
		}
		crPath := strings.TrimSuffix(inFile, filepath.Ext(inFile)) + ".crypto.c"
		if err := os.WriteFile(crPath, []byte(src), 0644); err != nil {
			fatal("cannot write crypto backend source: %v", err)
		}
		clangArgs = append(clangArgs, crPath)
		cflags, libs := llvm.LocateCrypto(backend)
		clangArgs = append(clangArgs, cflags...)
		clangArgs = append(clangArgs, libs...)
	}
	// JSON parse-tree (TDD-00077 Track P): compile the self-contained JSON
	// parser (implementing the __kml_json_* ABI, libc only) alongside the program
	// — only when it uses JSON.parse/Response.json(). Same shape as bigint above,
	// minus any external library to link.
	// Buffer codecs (TDD-00103): hex/base64/latin1, libc only — compiled
	// alongside only when a program uses a Buffer string codec. Same shape
	// as the JSON parse-tree file below.
	if em.UsesBufferCodecs() {
		bcPath := strings.TrimSuffix(inFile, filepath.Ext(inFile)) + ".bufcodecs.c"
		if err := os.WriteFile(bcPath, []byte(llvm.BufferCodecsSource()), 0644); err != nil {
			fatal("cannot write Buffer codec source: %v", err)
		}
		clangArgs = append(clangArgs, bcPath)
	}
	if em.UsesJSONParse() {
		jsonPath := strings.TrimSuffix(inFile, filepath.Ext(inFile)) + ".jsontree.c"
		if err := os.WriteFile(jsonPath, []byte(llvm.JSONParseTreeSource()), 0644); err != nil {
			fatal("cannot write JSON parse-tree source: %v", err)
		}
		clangArgs = append(clangArgs, jsonPath)
	}
	// URLPattern (TDD-00100): the pattern-compile/match runtime (pcre2 + curl,
	// both already added via LinkLibs above when used) — only when the program
	// constructs a URLPattern. Same shape as the JSON parse-tree file.
	if em.UsesURLPattern() {
		upPath := strings.TrimSuffix(inFile, filepath.Ext(inFile)) + ".urlpattern.c"
		if err := os.WriteFile(upPath, []byte(llvm.URLPatternSource()), 0644); err != nil {
			fatal("cannot write URLPattern runtime source: %v", err)
		}
		clangArgs = append(clangArgs, upPath)
	}
	// Float formatter (TDD-00080): the JS-faithful shortest-round-trip dtoa,
	// compiled alongside the program only when it prints a float (libc only).
	if em.UsesFloatFmt() {
		dtoaPath := strings.TrimSuffix(inFile, filepath.Ext(inFile)) + ".dtoa.c"
		if err := os.WriteFile(dtoaPath, []byte(llvm.DtoaSource()), 0644); err != nil {
			fatal("cannot write dtoa source: %v", err)
		}
		clangArgs = append(clangArgs, dtoaPath)
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

// wrapText greedily word-wraps s into lines no wider than width columns, for the
// flag help. Width is measured in bytes, so a line with multi-byte runes (the
// — and → in some descriptions) wraps a touch early — harmless for help text.
func wrapText(s string, width int) []string {
	var lines []string
	var cur strings.Builder
	for _, w := range strings.Fields(s) {
		if cur.Len() > 0 && cur.Len()+1+len(w) > width {
			lines = append(lines, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte(' ')
		}
		cur.WriteString(w)
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return lines
}
