package main

import (
	"KlainMainLang/codegen/llvm"
	"KlainMainLang/resolver"
	"flag"
	"fmt"
	"os"
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
	webviewBackend := flag.String("webview", "system", "klain:webview engine `backend`, selected only when a program opens a webview window: system (default — the platform engine: WebKitGTK/WKWebView/WebView2) or cef / qt / sailfish (opt-in Chromium/Gecko backends, not yet built). The bind/eval/serve contract is identical across backends")
	var pkg packageFlag
	flag.Var(&pkg, "package", "after compiling, also build a package around the binary. Bare -package builds a double-clickable host bundle (a .app on macOS, a .desktop launcher on Linux, a GUI-subsystem <name>\\<name>.exe on Windows) — for webview GUI programs. -package=rpm builds an RPM (for Sailfish OS / RPM Linux), -package=rpm:harbour applies Sailfish Harbour naming (harbour-<name>); the .spec + build tree are always written and rpmbuild is run when present, so an RPM cross-package can be produced from a --target build. The standalone binary is still produced too")
	appName := flag.String("app-name", "", "display `name` for -package (default: the output binary's name). Sets the .app folder name and the app's shown name")
	appID := flag.String("app-id", "", "bundle `identifier` for -package, e.g. com.example.myapp (default: com.klain.<name>). Becomes the macOS Info.plist CFBundleIdentifier")
	appVersion := flag.String("app-version", "1.0.0", "app `version` string for -package (macOS CFBundleShortVersionString/CFBundleVersion)")
	appIcon := flag.String("app-icon", "", "`path` to an app icon for -package: a .icns (used as-is) or .png (converted to .icns on macOS) on macOS; a .png or .svg on Linux; an .ico, or a .png of at most 256x256 (wrapped into an .ico), on Windows; a .png for -package=rpm:harbour. If omitted, the platform's generic app icon is used")
	appLicense := flag.String("app-license", "Proprietary", "license `identifier` for -package=rpm (the RPM License: tag), e.g. GPLv3+, MIT, BSD. Defaults to Proprietary")
	emitWindowDTS := flag.Bool("emit-window-dts", false, "for a klain:webview program, also write a <output>.window.d.ts declaring the window.* functions its typed bindings expose, so the page-side code gets autocomplete/typechecking on them")
	emitDecoratorMetadata := flag.Bool("emit-decorator-metadata", false, "with experimental decorators, emit design:type/design:paramtypes/design:returntype reflection metadata for decorated members (readable via Reflect.getMetadata) — mirrors TypeScript's emitDecoratorMetadata")
	decorators := flag.String("decorators", "experimental", "decorator dialect: experimental (legacy (target, key, descriptor), the default) or standard (TC39 (value, context))")
	optimizeMemory := flag.Bool("optimize-memory", false, "allocation optimizations with no semantic change: stack-allocate object literals the escape analysis proves never outlive their block, instead of heap-allocating them (less allocator pressure, better cache locality). Off by default while the analysis matures")
	finalizers := flag.String("finalizers", "off", "FinalizationRegistry exit `diagnostics`: off (default) or report — under -mm=manual, print one line per registration still live at exit (its target was never freed — a labeled leak) before running its cleanup callback")
	target := flag.String("target", "", "cross-compile for another platform instead of the host. Either a full clang `triple` (e.g. aarch64-linux-gnu) or a preset name: sfos-aarch64 (Sailfish OS on 64-bit ARM → aarch64-linux-gnu). Requires --sysroot pointing at the target's root filesystem. The emitted IR and the whole embedded C runtime are built for the target; a genuine cross-arch link needs lld on PATH (used automatically when present). Default (empty): build for the host")
	sysroot := flag.String("sysroot", "", "for --target: `path` to the target platform's root filesystem (its /usr/include headers and /usr/lib libraries), passed to clang as --sysroot. Required whenever --target is set")
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
	showVersion := flag.Bool("version", false, "print the compiler's version (stamped from the release tag by the release pipeline; a git describe for a `make build`; 0.0.0-dev for a plain `go build`) and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("klainmain %s %s/%s\n", llvm.KlainVersion, runtime.GOOS, runtime.GOARCH)
		return
	}

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	if *static && runtime.GOOS == "darwin" {
		fatal("--static is not supported on macOS: static linking needs a static libc to link against, and macOS's linker ships none at all — Apple deliberately never provides a static libSystem/crt0.o, with no workaround. On Linux, --static produces a fully static binary (scratch/distroless-ready). On Windows, --static statically links the non-system libraries (libstdc++, winpthread, pcre2, the curl chain) so the .exe is self-contained — the UCRT and core Win32 DLLs stay dynamic, as they ship with Windows 10+. To produce a static Linux binary from here, use a Linux build stage in a multi-stage Dockerfile.")
	}
	llvm.SetStaticLink(*static)

	// Cross-compilation target (TDD-00146 Stage 1). A preset name resolves to a
	// real clang triple; a bare triple is passed through. --sysroot is always
	// required and validated, the deliberately-explicit posture (no magic SDK
	// probing) so a missing rootfs fails here with a clear message rather than
	// deep inside clang.
	var crossTriple string // resolved --target triple, "" for a host build; read again at packaging time
	if *target != "" {
		triple := *target
		if resolved, ok := crossPresets[*target]; ok {
			triple = resolved
		}
		crossTriple = triple
		if *sysroot == "" {
			fatal("--target=%s requires --sysroot=<path>: point it at the target platform's root filesystem (the tree holding its /usr/include and /usr/lib). Extract one from the target's SDK or a matching container image", *target)
		}
		if st, err := os.Stat(*sysroot); err != nil || !st.IsDir() {
			fatal("--sysroot %q is not a directory: it must point at the target platform's root filesystem (holding /usr/include and /usr/lib)", *sysroot)
		}
		if *static {
			fatal("--target cannot yet be combined with --static: cross static linking is not wired up. Build a dynamic cross binary (the target's shared libs live in --sysroot), or run a static build natively on the target platform")
		}
		// Cross-*OS* codegen: the compiler now threads the *target* OS/arch
		// through the runtime-emission sites (ucontext layout, process.execPath,
		// the os module, process.platform/arch, math, dgram, FFI, fs stat layouts,
		// clocks) via llvm.targetGOOS()/targetGOARCH() (TDD-00146 Stage 1
		// completion), so a program's C-level behavior follows the target. The
		// only enabled cross-OS pair is macOS→Linux, which is fully
		// execution-verifiable (link with lld against the target sysroot, run the
		// ELF under an arm64 Linux container). The other directions stay rejected:
		// a Windows target needs the mingw link toolchain (not wired for cross),
		// and Linux→macOS cannot be execution-verified here so it is not claimed.
		// Cross-*arch* within one OS is unaffected (the guard only concerns OS).
		if tgtOS := targetOSFromTriple(triple); tgtOS != "" && tgtOS != runtime.GOOS {
			if !(runtime.GOOS == "darwin" && tgtOS == "linux") {
				fatal("cross-compiling from %s to a %s target is not supported yet: only macOS→Linux cross-OS builds are enabled (the fully execution-verifiable direction). A Windows target needs the mingw link toolchain; a macOS target can't be produced from a non-macOS host. Cross-compiling to a different CPU architecture on the same OS always works — so build a %s target by running klainmain on a %s host", runtime.GOOS, tgtOS, tgtOS, tgtOS)
			}
		}
		llvm.SetCrossTarget(triple, *sysroot)
	} else if *sysroot != "" {
		fatal("--sysroot has no effect without --target: set --target=<triple|preset> to cross-compile")
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

	switch *webviewBackend {
	case "system":
		// ok — the per-platform system engine (TDD-00142).
	case "cef", "qt", "sailfish":
		// Recognized opt-in backends whose shims are not yet built (TDD-00144
		// Stage 1). The flag validates so scripts/presets can pin one now; the
		// clean "not yet implemented" rejection fires at link time, and only
		// for a program that actually opens a webview (see below).
	default:
		fatal("unrecognized -webview value %q — must be one of: system (default), cef, qt, sailfish", *webviewBackend)
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
	em.SetWebviewBackend(*webviewBackend)
	em.SetCompatMode(*compat)
	em.SetEmitDecoratorMetadata(*emitDecoratorMetadata)
	em.SetDecoratorDialect(*decorators)
	em.SetFinalizersMode(*finalizers)
	em.SetOptimizeMemory(*optimizeMemory)
	ir, err := em.EmitProgram(prog)
	if err != nil {
		fatal("codegen error: %v", err)
	}

	// Cross-*OS* builds thread the target through the runtime-emission sites, but
	// the C++ GUI/TUI subsystems (klain:webview, klain:tui/Yoga) still choose
	// their compile/link recipe — libc++ vs libstdc++, .dylib vs .so, macOS
	// frameworks — from the build host, so a macOS→Linux build of one would
	// mis-link. Reject it cleanly rather than emit a broken link. (Plain CLI +
	// the whole Node runtime surface cross-compiles fine.)
	if llvm.CrossTargetGOOS() != "" && llvm.CrossTargetGOOS() != runtime.GOOS {
		if em.UsesWebview() {
			fatal("cross-OS build (%s→%s) of a klain:webview program is not supported: the webview backend is still selected and linked for the build host. Build webview apps natively on the target OS", runtime.GOOS, llvm.CrossTargetGOOS())
		}
		if em.UsesTui() {
			fatal("cross-OS build (%s→%s) of a klain:tui program is not supported: the Yoga C++ layout engine is still compiled/linked for the build host. Build TUI apps natively on the target OS", runtime.GOOS, llvm.CrossTargetGOOS())
		}
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
		outBin += llvm.HostExeSuffix()
	}

	clangArgs := []string{"-O2", llFile}
	if em.UsesWorkers() {
		// Worker threads (worker_threads): the first and only pthread
		// dependency; -pthread covers both compile and link phases.
		clangArgs = append(clangArgs, llvm.WorkerPthreadLinkFlags()...)
	}
	if *mm == "gc" {
		gcShimPath := strings.TrimSuffix(inFile, filepath.Ext(inFile)) + ".gcshim.c"
		if err := os.WriteFile(gcShimPath, []byte(llvm.GCShimSource), 0644); err != nil {
			fatal("cannot write GC shim: %v", err)
		}
		clangArgs = append(clangArgs, gcShimPath)
	}
	clangArgs = append(clangArgs, "-o", outBin)
	if *static && runtime.GOOS == "linux" {
		// Fully static (incl. glibc) — scratch/distroless-ready. On Windows,
		// --static instead statically links only the non-system libraries (via the
		// llvm package's staticLinkMode gating); clang -static there would try to
		// static the UCRT too and break. macOS --static is rejected above.
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
		// A Qt source (the Sailfish webview shim) needs moc run over it first,
		// producing the webview_sailfish.moc it #includes (TDD-00146 Stage 3).
		if cs.NeedsMoc() {
			moc := llvm.SailfishMocPath(llvm.CrossTargetSysroot())
			if err := llvm.RunSailfishMoc(moc, cPath, llvm.CrossTargetSysroot()); err != nil {
				fatal("%v", err)
			}
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
	// node:ffi's dlopen(null) needs the executable's own symbols exported and the
	// common FFI target libs (libm) force-loaded on Linux — see FFILinkFlags.
	if em.UsesFFIDl() {
		clangArgs = append(clangArgs, llvm.FFILinkFlags()...)
	}
	// -l flags go LAST, after every object and sidecar .c: a library listed
	// before an object that needs it is never searched again by GNU ld, and
	// on Windows the .dll.a import libraries are archives, so the sidecar's
	// pcre2/curl references went unresolved (ADR-00738). Shared libraries on
	// Linux/macOS forgave the old order; archives don't.
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, llvm.LinkLibFlags(lib)...)
	}
	// Harbour RPM (TDD-00146 Stage 2): non-allowlisted libraries (pcre2) are
	// shipped app-privately under /usr/share/harbour-<name>/lib, so the binary
	// must carry an rpath to that dir. It has to be set at link time, hence here
	// rather than in the packaging block below. The name mirrors
	// resolvePackageOptsRPM's default (given -app-name, else the binary name).
	if pkg.value == "rpm:harbour" && len(harbourLibsToBundle(em.LinkLibs(), *mm == "gc")) > 0 {
		nm := *appName
		if nm == "" {
			nm = filepath.Base(outBin)
		}
		clangArgs = append(clangArgs, "-Wl,-rpath,"+harbourRpathDir("harbour-"+appSlug(nm)))
	}
	cmd := llvm.ClangCommand(clangArgs...)
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
		if runtime.GOOS == "windows" {
			soExt = ".dll"
		}
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
			iem.SetWebviewBackend(*webviewBackend)
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
				iArgs = append(iArgs, llvm.WorkerPthreadLinkFlags()...)
			}
			for _, lib := range iem.LinkLibs() {
				iArgs = append(iArgs, llvm.LinkLibFlags(lib)...)
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
				if cs.NeedsMoc() {
					moc := llvm.SailfishMocPath(llvm.CrossTargetSysroot())
					if err := llvm.RunSailfishMoc(moc, cPath, llvm.CrossTargetSysroot()); err != nil {
						fatal("island %s: %v", root, err)
					}
				}
				iArgs = append(iArgs, cPath)
				iArgs = append(iArgs, cs.CFlags...)
				iArgs = append(iArgs, cs.Libs...)
			}
			icmd := llvm.ClangCommand(iArgs...)
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
	if pkg.set {
		// RPM shapes (TDD-00146 Stage 2) are *target* packages, not host
		// bundles: no GOOS gate (they ride a --target cross build), no
		// webview requirement (CLI tools are the primary RPM case).
		if pkg.value == "rpm" || pkg.value == "rpm:harbour" {
			opts, oerr := resolvePackageOptsRPM(outBin, *appName, *appVersion, *appIcon)
			if oerr != nil {
				fatal("%v", oerr)
			}
			bundleLibs := harbourLibsToBundle(em.LinkLibs(), *mm == "gc")
			artifact, perr := writeRPM(outBin, opts, pkg.value == "rpm:harbour", *appLicense, crossTriple, *sysroot, bundleLibs)
			if perr != nil {
				fatal("package: %v", perr)
			}
			fmt.Fprintf(os.Stderr, "packaged: %s\n", artifact)
			return
		}
		if runtime.GOOS != "darwin" && runtime.GOOS != "linux" && runtime.GOOS != "windows" {
			fatal("-package is only supported on macOS, Linux and Windows (this run is on %s)", runtime.GOOS)
		}
		if !em.UsesWebview() {
			fmt.Fprintf(os.Stderr, "klainmain: warning: -package on a program that doesn't open a webview window; bundling anyway\n")
		}
		opts, oerr := resolvePackageOpts(outBin, *appName, *appID, *appVersion, *appIcon)
		if oerr != nil {
			fatal("%v", oerr)
		}
		// Windows re-links the program as a GUI-subsystem executable with its
		// resources (ADR-00726): the same argv as the link above, a different
		// output, plus the packager's extra arguments.
		relink := func(extra []string, out string) error {
			args := append([]string{}, clangArgs...)
			for i := 0; i+1 < len(args); i++ {
				if args[i] == "-o" {
					args[i+1] = out
				}
			}
			args = append(args, extra...)
			cmd := llvm.ClangCommand(args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			return cmd.Run()
		}
		artifact, perr := packageApp(outBin, opts, relink)
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

// packageFlag backs -package. It is bool-like (IsBoolFlag) so bare `-package`
// still means "host-native bundle" (the original behavior) while `-package=rpm`
// / `-package=rpm:harbour` select the RPM shapes (TDD-00146 Stage 2). Validated
// at parse time.
type packageFlag struct {
	set   bool
	value string // "auto" (host bundle), "rpm", or "rpm:harbour"
}

func (f *packageFlag) String() string { return f.value }

func (f *packageFlag) Set(s string) error {
	if s == "true" || s == "" {
		s = "auto"
	}
	switch s {
	case "auto", "rpm", "rpm:harbour":
		f.set = true
		f.value = s
		return nil
	default:
		return fmt.Errorf("unrecognized -package value %q — bare -package (host bundle: .app/.desktop/.exe), or -package=rpm / -package=rpm:harbour", s)
	}
}

// IsBoolFlag lets bare `-package` set the flag without a value. Note: with this,
// the format form must use `=` (`-package=rpm`), not a space.
func (f *packageFlag) IsBoolFlag() bool { return true }

// crossPresets maps a friendly --target preset name to the clang triple it
// stands for (TDD-00146 Stage 1). A bare triple bypasses this map. Sailfish OS
// is glibc/aarch64 Linux; its *native* toolchain triple is
// aarch64-meego-linux-gnu (the Mer heritage), and using it verbatim is what
// makes clang's own GCC-installation detector find the target's crt objects,
// libgcc, and RPM-style /usr/lib64 layout inside a Sailfish --sysroot — the
// generic aarch64-linux-gnu triple does not match that gcc install and fails to
// link. The "meego" vendor field does not change the aarch64 glibc ELF ABI, so
// the binary still loads on-device against /lib/ld-linux-aarch64.so.1. Verified
// end-to-end: a pcre2-linked binary built this way runs on a real Xperia 10 II
// (Sailfish 5.1.0.11), resolving libpcre2-8.so.0 from /usr/lib64.
var crossPresets = map[string]string{
	"sfos-aarch64": "aarch64-meego-linux-gnu",
}

// targetOSFromTriple extracts the operating system a clang triple names,
// normalized to the Go GOOS spelling ("linux"/"darwin"/"windows"), or "" if the
// triple names an OS this compiler doesn't recognize. Triples are
// arch[-vendor]-os[-abi]; the OS token is what matters here, so a substring
// match is enough and tolerates the vendor field being present or absent.
func targetOSFromTriple(triple string) string {
	t := strings.ToLower(triple)
	switch {
	case strings.Contains(t, "linux"):
		return "linux"
	case strings.Contains(t, "darwin"), strings.Contains(t, "macos"), strings.Contains(t, "apple"):
		return "darwin"
	case strings.Contains(t, "windows"), strings.Contains(t, "mingw"), strings.Contains(t, "w64"):
		return "windows"
	default:
		return ""
	}
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
