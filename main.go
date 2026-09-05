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
	mm := flag.String("mm", "manual", "memory management `mode`: manual (default, Memory.free(x) only), gc (Boehm GC — allocations are collected automatically; needs bdw-gc/libgc installed), or auto (the compiler inserts free calls where it can prove them safe — /** @free */ and /** @owned */ annotations plus automatic freeing of provably-local values; Memory.free is a compile error)")
	dynImport := flag.String("dynamic-import", "eager", "dynamic `import()` `mode`: eager (default — a literal-specifier import resolved at compile time, target runs eagerly, wrapped in a resolved Promise) or lazy (each dynamic-import target compiled to a shared-library island loaded on first use — real laziness; incompatible with --static, produces multiple artifacts)")
	compat := flag.String("compat", "strict", "compatibility `mode`: strict (default — the compiler's opinionated, safer-than-JS semantics; e.g. a declaration colliding with an ambient built-in name like Math/fetch is a compile error) or js (best-effort JS-faithful — e.g. real-JS/browser global shadowing)")
	regex := flag.String("regex", "", "RegExp `dialect`: es-unicode (default — ECMAScript matching via PCRE2_UTF + NEWLINE_ANY), ecmascript (es-unicode plus a source-normalization pass — exact dot line-terminator semantics), es-utf16 (es-unicode plus true UTF-16 code-unit indices for .search/lastIndex/replace-callback offsets), es-ascii (cheaper ASCII-faithful option alignment only), or pcre (raw PCRE2, no ES wrapping)")
	bigint := flag.String("bigint", "libtommath", "bigint backend `library`, linked only when a program uses bigint: libtommath (default, public domain) or gmp (LGPL, faster). Both give identical arbitrary-precision semantics")
	cryptoBackend := flag.String("crypto", "openssl", "crypto.subtle backend `library`, compiled+linked only when a program uses crypto.subtle: openssl (default — libcrypto 3.x, all platforms) or commoncrypto (macOS only — Apple CommonCrypto plus Security.framework, no OpenSSL dependency). Both give identical Web Crypto semantics")
	pkg := flag.Bool("package", false, "after compiling, also build a double-clickable desktop app around the binary: a .app bundle on macOS, a .desktop launcher on Linux. Intended for webview GUI programs — the bundle is what gives a window proper foreground activation. The standalone binary is still produced too")
	appName := flag.String("app-name", "", "display `name` for -package (default: the output binary's name). Sets the .app folder name and the app's shown name")
	appID := flag.String("app-id", "", "bundle `identifier` for -package, e.g. com.example.myapp (default: com.klain.<name>). Becomes the macOS Info.plist CFBundleIdentifier")
	appVersion := flag.String("app-version", "1.0.0", "app `version` string for -package (macOS CFBundleShortVersionString/CFBundleVersion)")
	appIcon := flag.String("app-icon", "", "`path` to an app icon for -package: a .icns (used as-is) or .png (converted to .icns on macOS) on macOS; a .png or .svg on Linux. If omitted, the platform's generic app icon is used")
	emitWindowDTS := flag.Bool("emit-window-dts", false, "for a klain:webview program, also write a <output>.window.d.ts declaring the window.* functions its typed bindings expose, so the page-side code gets autocomplete/typechecking on them")
	emitDecoratorMetadata := flag.Bool("emit-decorator-metadata", false, "with experimental decorators, emit design:type/design:paramtypes/design:returntype reflection metadata for decorated members (readable via Reflect.getMetadata) — mirrors TypeScript's emitDecoratorMetadata")
	decorators := flag.String("decorators", "experimental", "decorator dialect: experimental (legacy (target, key, descriptor), the default) or standard (TC39 (value, context))")
	optimizeMemory := flag.Bool("optimize-memory", false, "allocation optimizations with no semantic change: stack-allocate object literals the escape analysis proves never outlive their block, instead of heap-allocating them (less allocator pressure, better cache locality). Off by default while the analysis matures")
	finalizers := flag.String("finalizers", "off", "FinalizationRegistry exit `diagnostics`: off (default) or report — under -mm=manual, print one line per registration still live at exit (its target was never freed — a labeled leak) before running its cleanup callback")
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
	case "manual", "gc", "auto":
		// ok
	default:
		fatal("unrecognized -mm value %q — must be one of: manual, gc, auto", *mm)
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

	switch *finalizers {
	case "off", "report":
		// ok
	default:
		fatal("unrecognized -finalizers value %q — must be one of: off (default), report", *finalizers)
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

	switch *dynImport {
	case "eager", "lazy":
		// ok
	default:
		fatal("unrecognized -dynamic-import value %q — must be one of: eager (default), lazy", *dynImport)
	}

	switch *decorators {
	case "experimental", "standard":
		// ok
	default:
		fatal("unrecognized -decorators value %q — must be one of: experimental (default), standard", *decorators)
	}

	inFile := flag.Arg(0)
	prog, err := resolver.ResolveProgramWithOptions(inFile, *compat == "js", *dynImport == "lazy")
	if err != nil {
		fatal("parse error: %v", err)
	}

	// TDD-00056: the lazy backend loads shared-library islands via dlopen at
	// runtime, which a statically-linked binary generally cannot do. Reject the
	// combination cleanly (only when dynamic import is actually used), the same
	// mutual-exclusion posture as -mm / -crypto above.
	if *dynImport == "lazy" && *static && prog.UsesDynamicImport {
		fatal("--static cannot be combined with -dynamic-import=lazy when the program uses dynamic import(): a statically-linked binary cannot dlopen() its shared-library islands at runtime. Use -dynamic-import=eager for a single self-contained --static binary, or drop --static to ship the islands alongside the executable")
	}

	em := llvm.NewEmitter()
	em.SetMemMode(*mm)
	em.SetDynamicImportMode(*dynImport)
	em.SetRegexMode(*regex)
	em.SetBigIntBackend(*bigint)
	em.SetCryptoBackend(*cryptoBackend)
	em.SetCompatMode(*compat)
	em.SetEmitDecoratorMetadata(*emitDecoratorMetadata)
	em.SetDecoratorDialect(*decorators)
	em.SetFinalizersMode(*finalizers)
	em.SetOptimizeMemory(*optimizeMemory)
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
	// Every embedded C runtime file this program's IR depends on (bigint / crypto
	// / tls / http2 / Buffer codecs / JSON parse-tree / URLPattern / dtoa float
	// formatter) — resolved from the one shared source of truth the conformance
	// runner uses too (EmbeddedCSources), so the CLI and the runner can never
	// drift on which .c files get linked.
	cSources, cerr := em.EmbeddedCSources()
	if cerr != nil {
		fatal("%v", cerr)
	}
	for _, cs := range cSources {
		cPath := strings.TrimSuffix(inFile, filepath.Ext(inFile)) + "." + cs.Name + "." + cs.SrcExt()
		if err := os.WriteFile(cPath, []byte(cs.Content), 0644); err != nil {
			fatal("cannot write %s source: %v", cs.Name, err)
		}
		clangArgs = append(clangArgs, cPath)
		clangArgs = append(clangArgs, cs.CFlags...)
		clangArgs = append(clangArgs, cs.Libs...)
	}
	// Embedded asset blobs (TDD-00142 Stage 7): each packed directory is written
	// as a sidecar `.bin` and linked via a generated `.s` that `.incbin`s it
	// under the per-GOOS symbol the IR references.
	blobs, berr := em.EmbeddedBlobs()
	if berr != nil {
		fatal("%v", berr)
	}
	for _, b := range blobs {
		base := strings.TrimSuffix(inFile, filepath.Ext(inFile))
		binPath := base + "." + b.Symbol + ".bin"
		asmPath := base + "." + b.Symbol + ".s"
		if err := os.WriteFile(binPath, b.Blob, 0644); err != nil {
			fatal("cannot write embed blob: %v", err)
		}
		absBin, _ := filepath.Abs(binPath)
		if err := os.WriteFile(asmPath, []byte(llvm.EmbedBlobAsm(b.Symbol, absBin, runtime.GOOS)), 0644); err != nil {
			fatal("cannot write embed asm: %v", err)
		}
		clangArgs = append(clangArgs, asmPath)
	}
	cmd := exec.Command("clang", clangArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fatal("clang: %v", err)
	}

	fmt.Fprintf(os.Stderr, "compiled: %s\n", outBin)

	// TDD-00056 lazy backend: compile each dynamic-import target into its own
	// shared-library island beside the binary, in a `<binary>.d/` directory, so
	// the executable dlopen()s it (self-locating) on first `import()`. Each
	// island is an independent whole-program compile rooted at its target
	// (nested dynamic imports inside an island stay eager in V1).
	if *dynImport == "lazy" && len(prog.IslandRoots) > 0 {
		islandDir := outBin + ".d"
		if err := os.MkdirAll(islandDir, 0755); err != nil {
			fatal("cannot create island directory %s: %v", islandDir, err)
		}
		soExt := ".so"
		if runtime.GOOS == "darwin" {
			soExt = ".dylib"
		}
		for _, root := range prog.IslandRoots {
			hash := llvm.IslandHash(root)
			iprog, err := resolver.ResolveProgramWithOptions(root, *compat == "js", false)
			if err != nil {
				fatal("island %s: parse error: %v", root, err)
			}
			iem := llvm.NewEmitter()
			iem.SetMemMode(*mm)
			iem.SetRegexMode(*regex)
			iem.SetBigIntBackend(*bigint)
			iem.SetCryptoBackend(*cryptoBackend)
			iem.SetCompatMode(*compat)
			iem.SetEmitDecoratorMetadata(*emitDecoratorMetadata)
			iem.SetDecoratorDialect(*decorators)
			iem.SetFinalizersMode(*finalizers)
			iem.SetOptimizeMemory(*optimizeMemory)
			iem.SetIslandHash(hash)
			iir, err := iem.EmitProgram(iprog)
			if err != nil {
				fatal("island %s: codegen error: %v", root, err)
			}
			illFile := filepath.Join(islandDir, hash+".ll")
			if err := os.WriteFile(illFile, []byte(iir), 0644); err != nil {
				fatal("cannot write island IR: %v", err)
			}
			soPath := filepath.Join(islandDir, hash+soExt)
			iArgs := []string{"-O2", "-shared", "-fPIC", illFile, "-o", soPath}
			if iem.UsesWorkers() {
				iArgs = append(iArgs, "-pthread")
			}
			for _, lib := range iem.LinkLibs() {
				iArgs = append(iArgs, "-l"+lib)
			}
			iCSources, cerr := iem.EmbeddedCSources()
			if cerr != nil {
				fatal("island %s: %v", root, cerr)
			}
			for _, cs := range iCSources {
				cPath := filepath.Join(islandDir, hash+"."+cs.Name+"."+cs.SrcExt())
				if err := os.WriteFile(cPath, []byte(cs.Content), 0644); err != nil {
					fatal("cannot write island %s source: %v", cs.Name, err)
				}
				iArgs = append(iArgs, cPath)
				iArgs = append(iArgs, cs.CFlags...)
				iArgs = append(iArgs, cs.Libs...)
			}
			icmd := exec.Command("clang", iArgs...)
			icmd.Stdout = os.Stdout
			icmd.Stderr = os.Stderr
			if err := icmd.Run(); err != nil {
				fatal("island %s: clang: %v", root, err)
			}
			fmt.Fprintf(os.Stderr, "  island: %s\n", soPath)
		}
	}

	// --emit-window-dts (TDD-00142 Stage 6): write the page-side Window typing
	// for this program's typed bindings next to the output.
	if *emitWindowDTS {
		if !em.HasWindowBindings() {
			fmt.Fprintf(os.Stderr, "klainmain: warning: --emit-window-dts: this program has no typed Webview bindings; nothing to declare\n")
		} else {
			dtsPath := outBin + ".window.d.ts"
			if err := os.WriteFile(dtsPath, []byte(em.WindowDTS()), 0644); err != nil {
				fatal("cannot write %s: %v", dtsPath, err)
			}
			fmt.Fprintf(os.Stderr, "window types: %s\n", dtsPath)
		}
	}

	// -package (TDD-00142 Stage 4): wrap the freshly-built binary into a
	// double-clickable desktop app for this host. Gated on GOOS the same way
	// -static is; a non-webview program is only a warning (a .app can wrap any
	// GUI binary).
	if *pkg {
		if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
			fatal("-package is only supported on macOS and Linux (this run is on %s)", runtime.GOOS)
		}
		if !em.UsesWebview() {
			fmt.Fprintf(os.Stderr, "klainmain: warning: -package on a program that doesn't open a webview window; bundling anyway\n")
		}
		opts, oerr := resolvePackageOpts(outBin, *appName, *appID, *appVersion, *appIcon)
		if oerr != nil {
			fatal("%v", oerr)
		}
		artifact, perr := packageApp(outBin, opts)
		if perr != nil {
			fatal("package: %v", perr)
		}
		fmt.Fprintf(os.Stderr, "packaged: %s\n", artifact)
	}
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
