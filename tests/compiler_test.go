package tests

import (
	"KlainMainLang/options"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"

	"KlainMainLang/ast"
	"KlainMainLang/codegen/llvm"
	"KlainMainLang/lib"
	"KlainMainLang/parser"
	"KlainMainLang/resolver"
)

// parseAndCompile runs parsing and codegen only (no clang), returning the
// generated IR and any error — used by negative tests asserting a clean
// compile-time rejection rather than a successful run.
func parseAndCompile(src string) (string, error) {
	prog, err := parseStrict(src)
	if err != nil {
		return "", err
	}
	em := llvm.NewEmitter()
	return em.EmitProgram(prog)
}

// parseStrict parses src and type-checks it as the strict lane does: a
// program TypeScript rejects is a compile error here too (TDD-00230 P2.7).
var declaresTopLevelMain = regexp.MustCompile(`(?m)^(?:export\s+)?(?:async\s+)?(?:function\*?|const|let|var|class)\s+main\b`)

func parseStrict(src string) (*ast.Program, error) {
	// A program naming a global a global module implements (TDD-00232) is
	// resolved, as the CLI resolves every program: the module joins it.
	// So is one declaring a top-level `main`: the resolver renames the
	// program's names, as the CLI's does, so it never meets the C entry point.
	if namesGlobalModule(src) || declaresTopLevelMain.MatchString(src) {
		d, err := os.MkdirTemp("", "kmlglobal")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(d)
		f := filepath.Join(d, "main.ts")
		if err := os.WriteFile(f, []byte(src), 0644); err != nil {
			return nil, err
		}
		return resolver.ResolveProgram(f)
	}
	prog, err := parser.Parse(src)
	if err != nil {
		return nil, err
	}
	if err := resolver.TypeCheck(prog, nil, options.Options{}, !resolver.IsModule(prog)); err != nil {
		return nil, err
	}
	return prog, nil
}

// parseCompatJS parses src for the -compat=js lane, resolving it (as the
// CLI does) when it names a global module.
func parseCompatJS(src string) (*ast.Program, error) {
	if !namesGlobalModule(src) {
		return parser.Parse(src)
	}
	d, err := os.MkdirTemp("", "kmlglobal")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(d)
	f := filepath.Join(d, "main.ts")
	if err := os.WriteFile(f, []byte(src), 0644); err != nil {
		return nil, err
	}
	return resolver.ResolveProgramWithOptions(f, options.Options{Compat: "js"})
}

// namesGlobalModule reports whether src mentions a global a global module
// implements, or `process`, parts of which are modules the resolver brings
// in the same way (its stdio streams, its emitter, emitWarning).
func namesGlobalModule(src string) bool {
	if processRE.MatchString(src) {
		return true
	}
	for name := range lib.NativeGlobalUses {
		if regexp.MustCompile(`\b` + name + `\b`).MatchString(src) {
			return true
		}
	}
	for name := range lib.GlobalModuleNames() {
		if regexp.MustCompile(`\b` + name + `\b`).MatchString(src) {
			return true
		}
	}
	for _, uses := range lib.LoweredFuncUses() {
		all := true
		for _, u := range uses {
			all = all && regexp.MustCompile(`\b`+u+`\b`).MatchString(src)
		}
		if all {
			return true
		}
	}
	return false
}

// resolveAndCompile is parseAndCompile for sources that use imports: it writes
// src to a real temp file and goes through resolver.ResolveProgram (which strips
// ImportDeclaration nodes) before codegen, so a rejection test can assert on the
// codegen error of an import-bearing program (e.g. `import http from 'http'`)
// without building a binary.
func resolveAndCompile(t *testing.T, src string) (string, error) {
	t.Helper()
	d := tempDir(t)
	srcFile := filepath.Join(d, "main.ts")
	if err := os.WriteFile(srcFile, []byte(src), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	prog, err := resolver.ResolveProgram(srcFile)
	if err != nil {
		return "", err
	}
	return llvm.NewEmitter().EmitProgram(prog)
}

// applyTestModeEnv applies the TDD-00174 Stage-A mode knobs to a generic
// buildBinary emitter: KLAIN_TEST_MM=manual|auto and KLAIN_TEST_OPTMEM=1 run
// the whole E2E suite through that memory mode / -optimize-memory, turning it
// into a mode-differential lane (assertions are unchanged — a mode-sensitive
// failure IS the finding). gc is deliberately not env-selectable here: it
// needs extra link flags and per-machine libgc, and has its own suite.
// Mode-specific helpers (buildBinaryGC, buildBinaryOptimizeMemory, …) keep
// their explicit pins and never read these.
func applyTestModeEnv(em *llvm.Emitter) {
	switch mm := os.Getenv("KLAIN_TEST_MM"); mm {
	case "", "gc":
	default:
		em.SetMemMode(mm)
	}
	if os.Getenv("KLAIN_TEST_OPTMEM") == "1" {
		em.SetOptimizeMemory(true)
	}
}

// sidecarObj returns what to hand clang for one embedded C runtime file: the
// path of an object compiled once and shared by every test that needs it
// (llvm.CSource.CachedObject, keyed by source + flags, under the temp root so
// it follows KML_SCRATCH), instead of a fresh copy of the source recompiled for
// each of the few thousand programs this suite builds. The code-generation
// flags already on the caller's clang line (-O level, -fsanitize, -D, …) are
// carried into that compile, so an ASan build gets an instrumented sidecar of
// its own. The caller still appends cs's own flags to the link line. If the
// object cannot be built, the source is written into dir and clang reports
// the error through the normal path.
func sidecarObj(t *testing.T, dir string, clangArgs []string, cs llvm.CSource) string {
	t.Helper()
	var compileFlags []string
	for _, a := range clangArgs {
		for _, p := range []string{"-O", "-f", "-g", "-D", "-m", "-std=", "-I", "-isystem"} {
			if strings.HasPrefix(a, p) {
				compileFlags = append(compileFlags, a)
				break
			}
		}
	}
	if obj, err := cs.CachedObject(filepath.Join(os.TempDir(), "klainmain-cobj"), compileFlags); err == nil {
		return obj
	}
	p := filepath.Join(dir, cs.Name+"."+cs.SrcExt())
	if err := os.WriteFile(p, []byte(cs.Content), 0644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// buildBinary compiles the given TypeScript source to a native binary and
// returns its path. The test is skipped if clang is not available.
func buildBinary(t *testing.T, src string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}

	prog, err := parseStrict(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	em := llvm.NewEmitter()
	applyTestModeEnv(em)
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}

	dir := tempDir(t)
	llFile := filepath.Join(dir, "prog.ll")
	binFile := filepath.Join(dir, "prog"+llvm.HostExeSuffix())

	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}

	clangArgs := []string{"-O2", llFile, "-o", binFile}
	if em.UsesWorkers() {
		clangArgs = append(clangArgs, llvm.WorkerPthreadLinkFlags()...)
	}
	clangArgs = appendRuntime(t, em, dir, clangArgs)
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, llvm.LinkLibFlags(lib)...)
	}
	out, err := llvm.ClangCommand(clangArgs...).CombinedOutput()
	if err != nil {
		skipIfBackendMissing(t, em, err, out)
		t.Fatalf("clang: %v\n%s", err, llvm.AnnotateClangOutput(out))
	}
	return binFile
}

// buildBinaryGC is buildBinary's gc-mode counterpart: sets the emitter's
// memory mode to "gc", writes the GC shim alongside the generated IR, and
// links via llvm.LocateGC() — the same discovery logic main.go uses, so the
// two clang invocations can't silently drift apart (see ADR-00020's
// original writeup of exactly that risk for buildBinary/buildBinaryMultiFile
// vs. main.go). Skips (doesn't fail) if clang or the Boehm GC dev package
// aren't available, so `go test ./...` stays green on a machine that hasn't
// installed bdw-gc/libgc-dev.
// missingDependencyRE matches the toolchain's own report that a library or a
// header could not be resolved: GNU ld / lld / Apple ld for `-lNAME`, clang for
// an `#include`.
var missingDependencyRE = regexp.MustCompile(
	`cannot find -l|library not found for -l|unable to find library -l|library '[^']+' not found|` +
		`fatal error: '[^']+' file not found|fatal error: [^:\n]+: No such file or directory`)

// dependencyMissing reports whether a failed clang run failed because a
// library or header is not installed — the one failure an optional backend's
// test may skip on. Anything else (invalid IR, an undefined symbol, a compile
// error in a sidecar) is a bug in what is being tested and has to fail: a
// blanket "the backend may not be installed" skip once hid eleven tests that
// had never linked.
func dependencyMissing(out []byte) bool {
	return missingDependencyRE.Match(out)
}

// skipIfBackendMissing skips when an optional bigint/crypto backend is in use
// and the link failed on a missing dependency; it returns otherwise, and the
// caller fails the test.
func skipIfBackendMissing(t *testing.T, em *llvm.Emitter, err error, out []byte) {
	t.Helper()
	if !dependencyMissing(out) {
		return
	}
	if em.UsesBigInt() {
		t.Skipf("bigint backend %q is not installed: clang: %v\n%s", em.BigIntBackend(), err, out)
	}
	if em.UsesCrypto() {
		t.Skipf("crypto backend %q is not installed: clang: %v\n%s", em.CryptoBackend(), err, out)
	}
}

// appendRuntime links every runtime C file the program uses: the list the
// CLI links (EmbeddedCSources), so the test build cannot drift from it. The
// webview binding is left to appendWebview, which skips when it is absent.
func appendRuntime(t *testing.T, em *llvm.Emitter, dir string, clangArgs []string) []string {
	t.Helper()
	srcs, err := em.EmbeddedCSources()
	if err != nil {
		t.Fatalf("runtime sources: %v", err)
	}
	if em.UsesFFIDl() {
		clangArgs = append(clangArgs, llvm.FFILinkFlags()...)
	}
	for _, cs := range srcs {
		if strings.HasPrefix(cs.Name, "webview") {
			continue
		}
		clangArgs = append(clangArgs, sidecarObj(t, dir, clangArgs, cs))
		clangArgs = append(clangArgs, cs.CFlags...)
		clangArgs = append(clangArgs, cs.Libs...)
	}
	return clangArgs
}

// appendWebview compiles+links the vendored C++ webview binding (TDD-00142)
// into the clang invocation when the program constructed a Webview, mirroring
// EmbeddedCSources so the test build path can't drift from the CLI's. The
// source is written with its real .cc extension so clang's driver compiles it
// as C++; the framework/pkg-config flags come from LocateWebview. Returns
// whether webview was used and any LocateWebview error, so a caller on a
// machine without WebKitGTK (headless Linux CI) can Skip rather than Fail.
func appendWebview(t *testing.T, em *llvm.Emitter, dir string, clangArgs []string) ([]string, bool, error) {
	t.Helper()
	if !em.UsesWebview() {
		return clangArgs, false, nil
	}
	cflags, libs, err := em.LocateWebview(em.WebviewBackend())
	if err != nil {
		return clangArgs, true, err
	}
	wvFile := filepath.Join(dir, "webview.cc")
	if err := os.WriteFile(wvFile, []byte(llvm.WebviewSource(em.WebviewBackend())), 0644); err != nil {
		t.Fatalf("write webview source: %v", err)
	}
	if em.WebviewBackend() == "sailfish" {
		moc := llvm.SailfishMocPath(em.Options().Target.Sysroot)
		if err := llvm.RunSailfishMoc(moc, wvFile, em.Options().Target.Sysroot); err != nil {
			return clangArgs, true, err
		}
	}
	clangArgs = append(clangArgs, wvFile)
	clangArgs = append(clangArgs, cflags...)
	clangArgs = append(clangArgs, libs...)
	return clangArgs, true, nil
}

func buildBinaryGC(t *testing.T, src string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}

	prog, err := parseStrict(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	em := llvm.NewEmitter()
	em.SetMemMode("gc")
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}

	dir := tempDir(t)
	llFile := filepath.Join(dir, "prog.ll")
	shimFile := filepath.Join(dir, "gcshim.c")
	binFile := filepath.Join(dir, "prog"+llvm.HostExeSuffix())

	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	if err := os.WriteFile(shimFile, []byte(llvm.GCShimSource), 0644); err != nil {
		t.Fatalf("write GC shim: %v", err)
	}

	cflags, libs, err := llvm.LocateGC()
	if err != nil {
		t.Skipf("gc mode: %v", err)
	}
	clangArgs := []string{"-O2", llFile, shimFile, "-o", binFile}
	clangArgs = append(clangArgs, cflags...)
	clangArgs = append(clangArgs, libs...)
	clangArgs = appendRuntime(t, em, dir, clangArgs)
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, llvm.LinkLibFlags(lib)...)
	}
	out, err := llvm.ClangCommand(clangArgs...).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "library not found for -lgc") || strings.Contains(string(out), "cannot find -lgc") {
			t.Skip("libgc/bdw-gc not installed")
		}
		t.Fatalf("clang: %v\n%s", err, llvm.AnnotateClangOutput(out))
	}
	return binFile
}

// buildBinaryImports is buildBinary's counterpart for source that uses a
// real `import` statement (TDD-00049's import-gated built-ins among them):
// resolver.ResolveProgram only ever reads from disk, and only it — never
// plain parser.Parse, which buildBinary uses — actually consumes/strips
// ImportDeclaration nodes before codegen runs (see resolver's own package
// doc). Writing src to a real temp file and resolving it from there is the
// same thing main.go itself does, just skipped by buildBinary's
// string-in-memory shortcut for the (much more common) import-free case.
func buildBinaryImports(t *testing.T, src string) string {
	t.Helper()
	d := tempDir(t)
	srcFile := filepath.Join(d, "main.ts")
	if err := os.WriteFile(srcFile, []byte(src), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return buildBinaryFromFile(t, srcFile)
}

// buildBinaryFromFile resolves and compiles a program from a real entry file,
// following its relative imports from that file's own directory — so a
// multi-module app (e.g. apps/klaintop/main.ts importing ./data, ./view) builds
// correctly, unlike buildBinaryImports which writes one inline source to a temp
// file. Build artifacts go to a fresh temp dir, never next to the source.
func buildBinaryFromFile(t *testing.T, srcFile string) string {
	t.Helper()
	return buildBinaryFromFileCrypto(t, srcFile, "")
}

// buildBinaryFromFileCrypto is buildBinaryFromFile with a `-crypto` backend
// ("" for the default).
func buildBinaryFromFileCrypto(t *testing.T, srcFile, cryptoBackend string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	if cryptoBackend == "commoncrypto" && runtime.GOOS != "darwin" {
		t.Skip("-crypto=commoncrypto is macOS-only")
	}

	prog, err := resolver.ResolveProgram(srcFile)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	dir := tempDir(t)
	em := llvm.NewEmitter()
	if cryptoBackend != "" {
		em.SetCryptoBackend(cryptoBackend)
	}
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}

	llFile := filepath.Join(dir, "prog.ll")
	binFile := filepath.Join(dir, "prog"+llvm.HostExeSuffix())

	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}

	clangArgs := []string{"-O2", llFile, "-o", binFile}
	if em.UsesWorkers() {
		clangArgs = append(clangArgs, llvm.WorkerPthreadLinkFlags()...)
	}
	clangArgs = appendRuntime(t, em, dir, clangArgs)
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, llvm.LinkLibFlags(lib)...)
	}
	clangArgs, webviewUsed, wverr := appendWebview(t, em, dir, clangArgs)
	if wverr != nil {
		t.Skipf("webview: %v", wverr)
	}
	clangArgs = appendEmbedBlobs(t, em, dir, clangArgs)
	out, err := llvm.ClangCommand(clangArgs...).CombinedOutput()
	if err != nil {
		skipIfBackendMissing(t, em, err, out)
		if webviewUsed && dependencyMissing(out) {
			t.Skipf("webview dev packages are not installed: clang: %v\n%s", err, out)
		}
		t.Fatalf("clang: %v\n%s", err, llvm.AnnotateClangOutput(out))
	}
	return binFile
}

// appendEmbedBlobs writes each embedded-asset blob (.bin) + its .incbin .s stub
// and links them, mirroring main.go so the test build path can't drift
// (TDD-00142 Stage 7).
func appendEmbedBlobs(t *testing.T, em *llvm.Emitter, dir string, clangArgs []string) []string {
	t.Helper()
	if !em.UsesEmbeddedAssets() {
		return clangArgs
	}
	blobs, err := em.EmbeddedBlobs()
	if err != nil {
		t.Fatalf("embedded blobs: %v", err)
	}
	for _, b := range blobs {
		binPath := filepath.Join(dir, b.Symbol+".bin")
		asmPath := filepath.Join(dir, b.Symbol+".s")
		if err := os.WriteFile(binPath, b.Blob, 0644); err != nil {
			t.Fatalf("write embed blob: %v", err)
		}
		if err := os.WriteFile(asmPath, []byte(llvm.EmbedBlobAsm(b.Symbol, binPath, runtime.GOOS)), 0644); err != nil {
			t.Fatalf("write embed asm: %v", err)
		}
		clangArgs = append(clangArgs, asmPath)
	}
	return clangArgs
}

// buildBinaryGCImports is buildBinaryGC's counterpart for source using a
// real `import` statement — see buildBinaryImports's doc comment for why
// this needs to go through resolver.ResolveProgram (a real file on disk)
// rather than buildBinaryGC's plain parser.Parse(src).
func buildBinaryGCImports(t *testing.T, src string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}

	dir := tempDir(t)
	srcFile := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(srcFile, []byte(src), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return buildGCProgram(t, dir, srcFile)
}

// buildGCProgram compiles the program at entry under -mm=gc into dir: the
// gcshim, Boehm, and every embedded runtime unit the program uses. Skips
// (not fails) when bdw-gc isn't installed.
func buildGCProgram(t *testing.T, dir, entry string) string {
	t.Helper()
	prog, err := resolver.ResolveProgram(entry)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	em := llvm.NewEmitter()
	em.SetMemMode("gc")
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}

	llFile := filepath.Join(dir, "prog.ll")
	shimFile := filepath.Join(dir, "gcshim.c")
	binFile := filepath.Join(dir, "prog"+llvm.HostExeSuffix())

	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	if err := os.WriteFile(shimFile, []byte(llvm.GCShimSource), 0644); err != nil {
		t.Fatalf("write GC shim: %v", err)
	}

	cflags, libs, err := llvm.LocateGC()
	if err != nil {
		t.Skipf("gc mode: %v", err)
	}
	clangArgs := []string{"-O2", llFile, shimFile, "-o", binFile}
	if em.UsesWorkers() {
		clangArgs = append(clangArgs, llvm.WorkerPthreadLinkFlags()...)
	}
	clangArgs = append(clangArgs, cflags...)
	clangArgs = append(clangArgs, libs...)
	clangArgs = appendRuntime(t, em, dir, clangArgs)
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, llvm.LinkLibFlags(lib)...)
	}
	out, err := llvm.ClangCommand(clangArgs...).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "library not found for -lgc") || strings.Contains(string(out), "cannot find -lgc") {
			t.Skip("libgc/bdw-gc not installed")
		}
		t.Fatalf("clang: %v\n%s", err, llvm.AnnotateClangOutput(out))
	}
	return binFile
}

// compileAndRunImports is compileAndRun's counterpart for source using a
// real `import` statement — see buildBinaryImports.
func compileAndRunImports(t *testing.T, src string) string {
	t.Helper()
	binFile := buildBinaryImports(t, src)
	result, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return strings.TrimRight(string(result), "\n")
}

// assertOutputImports is assertOutput's counterpart for source using a real
// `import` statement — see buildBinaryImports.
func assertOutputImports(t *testing.T, src, want string) {
	t.Helper()
	compareLines(t, compileAndRunImports(t, src), want)
}

// assertOutputImportsEnv compiles import-using source, then runs the binary
// with extra environment variables overriding the inherited ones (each entry
// "KEY=VALUE"). The env override applies only to the child run, not to the Go
// test process — so a test can set e.g. TMPDIR for the program under test
// without disturbing t.TempDir()/the build, which read TMPDIR themselves. An
// override with an existing key replaces the inherited value rather than
// appending a duplicate (whose resolution getenv leaves platform-dependent).
func assertOutputImportsEnv(t *testing.T, src, want string, overrides ...string) {
	t.Helper()
	binFile := buildBinaryImports(t, src)
	env := os.Environ()
	for _, o := range overrides {
		key := o[:strings.IndexByte(o, '=')+1]
		kept := env[:0]
		for _, e := range env {
			if !strings.HasPrefix(e, key) {
				kept = append(kept, e)
			}
		}
		env = append(kept, o)
	}
	cmd := exec.Command(binFile)
	cmd.Env = env
	result, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	compareLines(t, strings.TrimRight(string(result), "\n"), want)
}

// compileAndRunExpectExitImports is compileAndRunExpectExit's counterpart
// for source using a real `import` statement — see buildBinaryImports.
func compileAndRunExpectExitImports(t *testing.T, src string) (string, int) {
	t.Helper()
	binFile := buildBinaryImports(t, src)
	cmd := exec.Command(binFile)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	err := cmd.Run()
	exitCode := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run: %v", err)
	}
	return strings.TrimRight(stdout.String(), "\n"), exitCode
}

// parseAndCompileImports is parseAndCompile's counterpart for source using
// a real `import` statement — see buildBinaryImports for why this needs a
// real file on disk and resolver.ResolveProgram rather than a bare
// parser.Parse(src) call. Used by negative tests asserting a clean
// compile-time rejection (codegen or resolution) rather than a successful
// run.
func parseAndCompileImports(t *testing.T, src string) (string, error) {
	t.Helper()
	dir := tempDir(t)
	srcFile := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(srcFile, []byte(src), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	prog, err := resolver.ResolveProgram(srcFile)
	if err != nil {
		return "", err
	}
	em := llvm.NewEmitter()
	return em.EmitProgram(prog)
}

// asanOptionsSource overrides ASan's default leak detection off, baked
// into the binary itself (via ASan's own __asan_default_options() hook, a
// weak C symbol ASan looks for at startup) rather than left as an
// ASAN_OPTIONS env var callers have to remember to set. This project's
// `manual` memory mode never frees by design (the project's own instructions: "every heap
// allocation is malloc'd and (almost) never freed") — LeakSanitizer (part
// of ASan by default on Linux) would otherwise flag that expected,
// documented behavior as a bug on every single manual-mode ASan run,
// confirmed directly: a trivial "let arr = [1,2,3]; console.log(...)"
// program reports two direct leaks under plain -fsanitize=address. Actual
// corruption bugs (heap-buffer-overflow, use-after-free, UBSan's checks)
// are unaffected by this — only the separate "still-reachable-at-exit"
// leak check is disabled.
const asanOptionsSource = `const char *__asan_default_options(void) { return "detect_leaks=0"; }`

// buildBinaryASan is buildBinary's AddressSanitizer/UndefinedBehaviorSanitizer
// counterpart, for chasing memory-corruption bugs that don't reproduce
// under a plain build (e.g. the residual -mm=gc clustering hang
// investigated in ADR-00099). Not part of the regular `go test ./...` run
// — ASan roughly doubles memory/time cost, and this is an opt-in debugging
// tool a specific investigation calls deliberately, not a default check.
// `-O1` (not `-O2`) and `-fno-omit-frame-pointer` are ASan's own documented
// recommendation for accurate stack traces; `-g` adds line numbers to
// those traces. `ASAN_OPTIONS`/`UBSAN_OPTIONS` (e.g. `abort_on_error=1` for
// a core dump, `halt_on_error=0` to keep going and log every violation
// instead of stopping at the first) can still be set by the caller in the
// environment when running the returned binary for anything beyond the
// leak-detection default this helper already bakes in.
func buildBinaryASan(t *testing.T, src string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	skipSanitizersOnWindows(t)

	prog, err := parseStrict(src)
	if strings.Contains(src, "import ") {
		// A program importing a module is resolved from a file, as the CLI
		// resolves it.
		srcFile := filepath.Join(tempDir(t), "main.ts")
		if err := os.WriteFile(srcFile, []byte(src), 0644); err != nil {
			t.Fatalf("write source: %v", err)
		}
		prog, err = resolver.ResolveProgram(srcFile)
	}
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	em := llvm.NewEmitter()
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}

	dir := tempDir(t)
	llFile := filepath.Join(dir, "prog.ll")
	asanOptFile := filepath.Join(dir, "asan_options.c")
	binFile := filepath.Join(dir, "prog"+llvm.HostExeSuffix())

	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	if err := os.WriteFile(asanOptFile, []byte(asanOptionsSource), 0644); err != nil {
		t.Fatalf("write asan_options.c: %v", err)
	}

	clangArgs := []string{
		"-O1", "-g", "-fno-omit-frame-pointer",
		"-fsanitize=address", "-fsanitize=undefined",
		llFile, asanOptFile, "-o", binFile,
	}
	clangArgs = appendRuntime(t, em, dir, clangArgs)
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, llvm.LinkLibFlags(lib)...)
	}
	out, err := llvm.ClangCommand(clangArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("clang: %v\n%s", err, llvm.AnnotateClangOutput(out))
	}
	return binFile
}

// writeMultiFile writes each file in files (keyed by relative path, e.g.
// "math.ts") into a fresh temp directory and returns the directory.
func writeMultiFile(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := tempDir(t)
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// resolveMultiFile writes files to a temp dir and runs the module resolver
// on entryName, returning the merged program (or a resolution error) — used
// by negative tests asserting a clean multi-file compile-time rejection.
func resolveMultiFile(t *testing.T, files map[string]string, entryName string) (*ast.Program, error) {
	t.Helper()
	dir := writeMultiFile(t, files)
	return resolver.ResolveProgram(filepath.Join(dir, entryName))
}

// resolveAndEmitMultiFile runs resolution and codegen (no clang) and
// returns the first error from either stage — used by negative multi-file
// tests asserting a clean rejection that only surfaces during codegen
// (e.g. an unresolved identifier), not during resolution itself.
func resolveAndEmitMultiFile(t *testing.T, files map[string]string, entryName string) error {
	t.Helper()
	dir := writeMultiFile(t, files)
	prog, err := resolver.ResolveProgram(filepath.Join(dir, entryName))
	if err != nil {
		return err
	}
	em := llvm.NewEmitter()
	_, err = em.EmitProgram(prog)
	return err
}

// buildBinaryMultiFile writes files to a temp dir, resolves imports
// starting from entryName, and compiles the merged program to a native
// binary. The test is skipped if clang is not available.
func buildBinaryMultiFile(t *testing.T, files map[string]string, entryName string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	dir := writeMultiFile(t, files)

	prog, err := resolver.ResolveProgram(filepath.Join(dir, entryName))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	em := llvm.NewEmitter()
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}

	llFile := filepath.Join(dir, "prog.ll")
	binFile := filepath.Join(dir, "prog"+llvm.HostExeSuffix())
	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	clangArgs := []string{"-O2", llFile, "-o", binFile}
	if em.UsesWorkers() {
		clangArgs = append(clangArgs, llvm.WorkerPthreadLinkFlags()...)
	}
	clangArgs = appendRuntime(t, em, dir, clangArgs)
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, llvm.LinkLibFlags(lib)...)
	}
	out, err := llvm.ClangCommand(clangArgs...).CombinedOutput()
	if err != nil {
		skipIfBackendMissing(t, em, err, out)
		t.Fatalf("clang: %v\n%s", err, llvm.AnnotateClangOutput(out))
	}
	return binFile
}

// resolveMultiFilePermissive is resolveMultiFile's `-globals=permissive`
// (TDD-00050) counterpart.
func resolveMultiFilePermissive(t *testing.T, files map[string]string, entryName string) (*ast.Program, error) {
	t.Helper()
	dir := writeMultiFile(t, files)
	return resolver.ResolveProgramWithOptions(filepath.Join(dir, entryName), options.Options{Compat: "js"})
}

// buildBinaryMultiFilePermissive is buildBinaryMultiFile's
// `-globals=permissive` (TDD-00050) counterpart.
func buildBinaryMultiFilePermissive(t *testing.T, files map[string]string, entryName string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	dir := writeMultiFile(t, files)

	opts := options.Options{Compat: "js"}
	prog, err := resolver.ResolveProgramWithOptions(filepath.Join(dir, entryName), opts)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	em := llvm.NewEmitter()
	em.SetOptions(opts)
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}

	llFile := filepath.Join(dir, "prog.ll")
	binFile := filepath.Join(dir, "prog"+llvm.HostExeSuffix())
	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	clangArgs := []string{"-O2", llFile, "-o", binFile}
	if em.UsesWorkers() {
		clangArgs = append(clangArgs, llvm.WorkerPthreadLinkFlags()...)
	}
	clangArgs = appendRuntime(t, em, dir, clangArgs)
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, llvm.LinkLibFlags(lib)...)
	}
	out, err := llvm.ClangCommand(clangArgs...).CombinedOutput()
	if err != nil {
		skipIfBackendMissing(t, em, err, out)
		t.Fatalf("clang: %v\n%s", err, llvm.AnnotateClangOutput(out))
	}
	return binFile
}

// assertMultiFileOutputPermissive is assertMultiFileOutput's
// `-globals=permissive` (TDD-00050) counterpart.
func assertMultiFileOutputPermissive(t *testing.T, files map[string]string, entryName, want string) {
	t.Helper()
	binFile := buildBinaryMultiFilePermissive(t, files, entryName)
	result, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	compareLines(t, strings.TrimRight(string(result), "\n"), want)
}

// assertMultiFileOutput builds and runs a multi-file program and compares
// its stdout against want, line by line.
func assertMultiFileOutput(t *testing.T, files map[string]string, entryName, want string) {
	t.Helper()
	binFile := buildBinaryMultiFile(t, files, entryName)
	result, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	compareLines(t, strings.TrimRight(string(result), "\n"), want)
}

// compileAndRun compiles the given TypeScript source to a native binary and
// returns its stdout. The test is skipped if clang is not available.
func compileAndRun(t *testing.T, src string) string {
	t.Helper()
	binFile := buildBinary(t, src)
	result, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return strings.TrimRight(string(result), "\n")
}

// buildBinaryRegexMode is buildBinary with an explicit `-regex` dialect mode
// (TDD-00067: "", "pcre", "es-ascii", or "es-unicode"). Mirrors main.go's
// em.SetRegexMode() so the mode-matrix RegExp tests exercise each dialect
// without threading a mode through every other call site (which keeps the
// default, empty == es-unicode).
func buildBinaryRegexMode(t *testing.T, src, mode string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	prog, err := parseStrict(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	em := llvm.NewEmitter()
	em.SetRegexMode(mode)
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}
	dir := tempDir(t)
	llFile := filepath.Join(dir, "prog.ll")
	binFile := filepath.Join(dir, "prog"+llvm.HostExeSuffix())
	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	clangArgs := []string{"-O2", llFile, "-o", binFile}
	if em.UsesWorkers() {
		clangArgs = append(clangArgs, llvm.WorkerPthreadLinkFlags()...)
	}
	clangArgs = appendRuntime(t, em, dir, clangArgs)
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, llvm.LinkLibFlags(lib)...)
	}
	out, err := llvm.ClangCommand(clangArgs...).CombinedOutput()
	if err != nil {
		skipIfBackendMissing(t, em, err, out)
		t.Fatalf("clang: %v\n%s", err, llvm.AnnotateClangOutput(out))
	}
	return binFile
}

// buildBinaryCompatJS is buildBinary with -compat=js (TDD-00075), mirroring
// main.go's em.SetCompatMode("js") — for the emitter-side compat inhabitants
// such as bigint↔float comparison. (Global shadowing is resolver-side, so this
// helper does not opt into permissive shadowing.)
func buildBinaryCompatJS(t *testing.T, src string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	prog, err := parseCompatJS(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	em := llvm.NewEmitter()
	em.SetCompatMode("js")
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}
	dir := tempDir(t)
	llFile := filepath.Join(dir, "prog.ll")
	binFile := filepath.Join(dir, "prog"+llvm.HostExeSuffix())
	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	clangArgs := []string{"-O2", llFile, "-o", binFile}
	if em.UsesWorkers() {
		clangArgs = append(clangArgs, llvm.WorkerPthreadLinkFlags()...)
	}
	clangArgs = appendRuntime(t, em, dir, clangArgs)
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, llvm.LinkLibFlags(lib)...)
	}
	out, err := llvm.ClangCommand(clangArgs...).CombinedOutput()
	if err != nil {
		skipIfBackendMissing(t, em, err, out)
		t.Fatalf("clang: %v\n%s", err, llvm.AnnotateClangOutput(out))
	}
	return binFile
}

// assertOutputWithDecoratorMetadata compiles src with
// -emit-decorator-metadata, runs it, and compares stdout line-by-line
// (TDD-00161 Stage 3).
func assertOutputWithDecoratorMetadata(t *testing.T, src, want string) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	prog, err := parseStrict(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	em := llvm.NewEmitter()
	em.SetEmitDecoratorMetadata(true)
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}
	dir := tempDir(t)
	llFile := filepath.Join(dir, "prog.ll")
	binFile := filepath.Join(dir, "prog"+llvm.HostExeSuffix())
	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	clangArgs := []string{"-O2", llFile, "-o", binFile}
	clangArgs = appendRuntime(t, em, dir, clangArgs)
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, llvm.LinkLibFlags(lib)...)
	}
	if out, err := llvm.ClangCommand(clangArgs...).CombinedOutput(); err != nil {
		t.Fatalf("clang: %v\n%s", err, llvm.AnnotateClangOutput(out))
	}
	result, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	compareLines(t, strings.TrimRight(string(result), "\n"), want)
}

// assertOutputStandardDecorators compiles src under -decorators=standard, runs
// it, and compares stdout line-by-line (TDD-00161 Stage 5).
func assertOutputStandardDecorators(t *testing.T, src, want string) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	prog, err := parseStrict(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	em := llvm.NewEmitter()
	em.SetDecoratorDialect("standard")
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}
	dir := tempDir(t)
	llFile := filepath.Join(dir, "prog.ll")
	binFile := filepath.Join(dir, "prog"+llvm.HostExeSuffix())
	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	clangArgs := []string{"-O2", llFile, "-o", binFile}
	clangArgs = appendRuntime(t, em, dir, clangArgs)
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, llvm.LinkLibFlags(lib)...)
	}
	if out, err := llvm.ClangCommand(clangArgs...).CombinedOutput(); err != nil {
		t.Fatalf("clang: %v\n%s", err, llvm.AnnotateClangOutput(out))
	}
	result, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	compareLines(t, strings.TrimRight(string(result), "\n"), want)
}

// compileStandardDecorators compiles src under -decorators=standard and returns
// any codegen error (for rejection tests).
func compileStandardDecorators(src string) error {
	prog, err := parseStrict(src)
	if err != nil {
		return err
	}
	em := llvm.NewEmitter()
	em.SetDecoratorDialect("standard")
	_, err = em.EmitProgram(prog)
	return err
}

// assertOutputCompatJS compiles src under -compat=js, runs it, and compares
// stdout line-by-line against want.
func assertOutputCompatJS(t *testing.T, src, want string) {
	t.Helper()
	binFile := buildBinaryCompatJS(t, src)
	result, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	compareLines(t, strings.TrimRight(string(result), "\n"), want)
}

// buildBinaryCryptoMode is buildBinary with an explicit `-crypto` backend
// (TDD-00104: "openssl" or "commoncrypto"). Mirrors main.go's
// em.SetCryptoBackend() so the backend-matrix Web Crypto tests exercise each
// library; skips (not fails) when the backend isn't installed/applicable.
func buildBinaryCryptoMode(t *testing.T, src, backend string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	if backend == "commoncrypto" && runtime.GOOS != "darwin" {
		t.Skip("-crypto=commoncrypto is macOS-only")
	}
	prog, err := parseStrict(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	em := llvm.NewEmitter()
	em.SetCryptoBackend(backend)
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}
	dir := tempDir(t)
	llFile := filepath.Join(dir, "prog.ll")
	binFile := filepath.Join(dir, "prog"+llvm.HostExeSuffix())
	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	clangArgs := []string{"-O2", llFile, "-o", binFile}
	if em.UsesWorkers() {
		clangArgs = append(clangArgs, llvm.WorkerPthreadLinkFlags()...)
	}
	clangArgs = appendRuntime(t, em, dir, clangArgs)
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, llvm.LinkLibFlags(lib)...)
	}
	out, err := llvm.ClangCommand(clangArgs...).CombinedOutput()
	if err != nil {
		skipIfBackendMissing(t, em, err, out)
		t.Fatalf("clang: %v\n%s", err, llvm.AnnotateClangOutput(out))
	}
	return binFile
}

// compileAndRunCryptoMode compiles src under a given `-crypto` backend, runs
// it, and returns trimmed stdout.
func compileAndRunCryptoMode(t *testing.T, src, backend string) string {
	t.Helper()
	binFile := buildBinaryCryptoMode(t, src, backend)
	result, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return strings.TrimRight(string(result), "\n")
}

// compileAndRunRegexMode compiles src under a given `-regex` mode, runs it,
// and returns trimmed stdout.
func compileAndRunRegexMode(t *testing.T, src, mode string) string {
	t.Helper()
	binFile := buildBinaryRegexMode(t, src, mode)
	result, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return strings.TrimRight(string(result), "\n")
}

// compileAndRunWithStdinImports is compileAndRunWithStdin for a program with
// imports (through the resolver).
func compileAndRunWithStdinImports(t *testing.T, src, stdin string) string {
	t.Helper()
	binFile := buildBinaryImports(t, src)
	cmd := exec.Command(binFile)
	cmd.Stdin = strings.NewReader(stdin)
	result, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return strings.TrimRight(string(result), "\n")
}

// compileAndRunWithArgs is like compileAndRun but passes extra CLI args to the binary.
func compileAndRunWithArgs(t *testing.T, src string, args ...string) string {
	t.Helper()
	binFile := buildBinary(t, src)
	result, err := exec.Command(binFile, args...).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return strings.TrimRight(string(result), "\n")
}

// compileAndRunExpectExit compiles and runs the given source, returning stdout
// and the process exit code (instead of failing the test on a non-zero exit).
func compileAndRunExpectExit(t *testing.T, src string) (string, int) {
	t.Helper()
	binFile := buildBinary(t, src)
	cmd := exec.Command(binFile)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	err := cmd.Run()
	exitCode := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run: %v", err)
	}
	return strings.TrimRight(stdout.String(), "\n"), exitCode
}

// compileAndRunCaptureStderr is like compileAndRun but returns stdout and
// stderr separately (untrimmed) rather than merging or discarding either —
// needed for asserting on raw, no-auto-newline output from
// process.stdout.write/process.stderr.write, where trailing-newline
// trimming would hide the exact bug this feature exists to avoid.
func compileAndRunCaptureStderr(t *testing.T, src string) (stdout, stderr string) {
	t.Helper()
	binFile := buildBinary(t, src)
	cmd := exec.Command(binFile)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	return outBuf.String(), errBuf.String()
}

func assertOutput(t *testing.T, src, want string) {
	t.Helper()
	compareLines(t, compileAndRun(t, src), want)
}

// nodeStripTypesBin returns a node binary that can run TypeScript source
// directly (`--experimental-strip-types`, Node ≥ 22.6), or skips the test.
// Memoized: one probe per test binary.
var nodeStripTypesOnce sync.Once
var nodeStripTypesPath string

func nodeStripTypesBin(t *testing.T) string {
	t.Helper()
	nodeStripTypesOnce.Do(func() {
		bin, err := exec.LookPath("node")
		if err != nil {
			return
		}
		dir, err := os.MkdirTemp("", "kml-node-probe")
		if err != nil {
			return
		}
		defer os.RemoveAll(dir)
		probe := filepath.Join(dir, "probe.ts")
		if err := os.WriteFile(probe, []byte("const n: number = 1; console.log(n);\n"), 0644); err != nil {
			return
		}
		out, err := exec.Command(bin, "--experimental-strip-types", "--no-warnings", probe).Output()
		if err == nil && strings.TrimSpace(string(out)) == "1" {
			nodeStripTypesPath = bin
		}
	})
	if nodeStripTypesPath == "" {
		t.Skip("node with --experimental-strip-types not on PATH — skipping Node-identical check")
	}
	return nodeStripTypesPath
}

// runNodeTS runs src (TypeScript, types stripped) under Node and returns its
// stdout; a non-zero exit is a test failure that shows Node's stderr, since
// the program is meant to be a valid, terminating Node program.
func runNodeTS(t *testing.T, src string) string {
	t.Helper()
	bin := nodeStripTypesBin(t)
	dir := tempDir(t)
	file := filepath.Join(dir, "prog.ts")
	if err := os.WriteFile(file, []byte(src), 0644); err != nil {
		t.Fatalf("write prog.ts: %v", err)
	}
	cmd := exec.Command(bin, "--experimental-strip-types", "--no-warnings", file)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("node run failed: %v\n%s", err, stderr.String())
	}
	return strings.TrimRight(string(raw), "\n")
}

// assertSameAsNode is the faithfulness check in one line: the SAME TypeScript
// program is compiled and run here and run under Node (types stripped), and
// their stdout must be identical. Node is the oracle, so the expected output
// is never typed by hand (ADR-01060). Skips when Node isn't available; use
// assertOutput next to it for the CI lanes without Node when the expectation
// must also be pinned in the source.
func assertSameAsNode(t *testing.T, src string) {
	t.Helper()
	theirs := runNodeTS(t, src)
	ours := compileAndRun(t, src)
	if ours != theirs {
		t.Fatalf("output differs from node:\n--- ours ---\n%s\n--- node ---\n%s", ours, theirs)
	}
	compareLines(t, ours, theirs)
}

// assertSameAsNodeImports is assertSameAsNode for source using a real `import`
// statement (the resolver path, see buildBinaryImports).
func assertSameAsNodeImports(t *testing.T, src string) {
	t.Helper()
	theirs := runNodeTS(t, src)
	ours := compileAndRunImports(t, src)
	if ours != theirs {
		t.Fatalf("output differs from node:\n--- ours ---\n%s\n--- node ---\n%s", ours, theirs)
	}
	compareLines(t, ours, theirs)
}

// assertSameAsNodeCompatJS is assertSameAsNode under -compat=js.
func assertSameAsNodeCompatJS(t *testing.T, src string) {
	t.Helper()
	theirs := runNodeTS(t, src)
	binFile := buildBinaryCompatJS(t, src)
	raw, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	ours := strings.TrimRight(string(raw), "\n")
	if ours != theirs {
		t.Fatalf("output differs from node:\n--- ours ---\n%s\n--- node ---\n%s", ours, theirs)
	}
}

// compareLines compares got against want line by line so individual
// mismatches are clear, rather than one big diff on the whole string.
func compareLines(t *testing.T, got, want string) {
	t.Helper()
	gotLines := strings.Split(got, "\n")
	wantLines := strings.Split(want, "\n")
	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		var g, w string
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			t.Errorf("line %d: got %q, want %q", i+1, g, w)
		}
	}
}

// tempDir is tempDir(t) with forward slashes. Tests splice the path into
// TypeScript string literals ("%s"), where a Windows backslash would start an
// escape sequence; forward slashes are accepted by every fs API on Windows
// (as they are by Node), and on Linux/macOS this is the identity.
func tempDir(t *testing.T) string {
	t.Helper()
	return filepath.ToSlash(t.TempDir())
}

// nodeJoin is Node's path.join of plain segments on this host: `\` on
// Windows, `/` elsewhere.
func nodeJoin(parts ...string) string {
	if runtime.GOOS == "windows" {
		return strings.Join(parts, "\\")
	}
	return strings.Join(parts, "/")
}

// nodeEOLJSON is JSON.stringify(os.EOL) on this host.
func nodeEOLJSON() string {
	if runtime.GOOS == "windows" {
		return `"\r\n"`
	}
	return `"\n"`
}

// skipPOSIXToolsOnWindows marks a test whose *expectations* are POSIX-shaped
// — absolute /bin paths, `cwd: "/"` or "/tmp", sh arithmetic, death by
// SIGKILL — rather than a compiler feature. Node on Windows fails the same
// programs the same way, so they are skipped there with the reason rather
// than rewritten (TDD-00177, per-test Windows expectations vs skips).
func skipPOSIXToolsOnWindows(t *testing.T, why string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-specific expectation, not portable to Windows: " + why)
	}
}

// skipSanitizersOnWindows: LLVM's Windows release ships the ASan/UBSan
// runtime only for the MSVC target; the mingw-w64 target this port builds
// for has none, so -fsanitize builds cannot link there (TDD-00177).
func skipSanitizersOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("no ASan/UBSan runtime for the mingw-w64 target in LLVM's Windows build")
	}
}

// skipForkOnlyOnWindows: Windows has no fork(), so a { workers: N } cluster's
// workers are re-spawned processes that run the program from the top (as every
// Node cluster worker does) instead of continuing from the fork point. A test
// of a fork-only property — code before http.listen running once — cannot hold
// there. Distribution itself is tested on every host.
func skipForkOnlyOnWindows(t *testing.T, why string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fork()-only property (Windows cluster workers are re-spawned, not forked): " + why)
	}
}

var processRE = regexp.MustCompile(`\bprocess\b`)
