package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"KlainMainLang/ast"
	"KlainMainLang/codegen/llvm"
	"KlainMainLang/parser"
	"KlainMainLang/resolver"
)

// parseAndCompile runs parsing and codegen only (no clang), returning the
// generated IR and any error — used by negative tests asserting a clean
// compile-time rejection rather than a successful run.
func parseAndCompile(src string) (string, error) {
	prog, err := parser.Parse(src)
	if err != nil {
		return "", err
	}
	em := llvm.NewEmitter()
	return em.EmitProgram(prog)
}

// buildBinary compiles the given TypeScript source to a native binary and
// returns its path. The test is skipped if clang is not available.
func buildBinary(t *testing.T, src string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}

	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	em := llvm.NewEmitter()
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}

	dir := t.TempDir()
	llFile := filepath.Join(dir, "prog.ll")
	binFile := filepath.Join(dir, "prog")

	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}

	clangArgs := []string{"-O2", llFile, "-o", binFile}
	if em.UsesWorkers() {
		clangArgs = append(clangArgs, "-pthread")
	}
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, "-l"+lib)
	}
	clangArgs, bigintUsed := appendBigIntBackend(t, em, dir, clangArgs)
	clangArgs = appendJSONParseTree(t, em, dir, clangArgs)
	clangArgs = appendBufferCodecs(t, em, dir, clangArgs)
	clangArgs = appendDtoa(t, em, dir, clangArgs)
	clangArgs = appendURLPattern(t, em, dir, clangArgs)
	out, err := exec.Command("clang", clangArgs...).CombinedOutput()
	if err != nil {
		if bigintUsed {
			t.Skipf("bigint backend %q may not be installed: clang: %v\n%s", em.BigIntBackend(), err, out)
		}
		t.Fatalf("clang: %v\n%s", err, out)
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
// appendBigIntBackend compiles+links the selected bigint backend C file into the
// clang invocation when the program used bigint, mirroring main.go so the test
// build and the real build can't drift (the same rationale ADR-00020 applies to
// the gc path). Returns whether bigint was used, so a caller can Skip (not Fail)
// when the backend library isn't installed on the machine.
func appendBigIntBackend(t *testing.T, em *llvm.Emitter, dir string, clangArgs []string) ([]string, bool) {
	t.Helper()
	if !em.UsesBigInt() {
		return clangArgs, false
	}
	backend := em.BigIntBackend()
	src, _ := llvm.BigIntBackendSource(backend)
	biFile := filepath.Join(dir, "bigint.c")
	if err := os.WriteFile(biFile, []byte(src), 0644); err != nil {
		t.Fatalf("write bigint backend: %v", err)
	}
	clangArgs = append(clangArgs, biFile)
	cflags, libs := llvm.LocateBigInt(backend)
	clangArgs = append(clangArgs, cflags...)
	clangArgs = append(clangArgs, libs...)
	return clangArgs, true
}

// appendJSONParseTree compiles the JSON parse-tree C file (__kml_json_* ABI,
// libc only) into the clang invocation when the program used JSON.parse/
// Response.json(), mirroring main.go so the test build and the real build can't
// drift (TDD-00077 Track P; same rationale as appendBigIntBackend).
func appendJSONParseTree(t *testing.T, em *llvm.Emitter, dir string, clangArgs []string) []string {
	t.Helper()
	if !em.UsesJSONParse() {
		return clangArgs
	}
	jsonFile := filepath.Join(dir, "jsontree.c")
	if err := os.WriteFile(jsonFile, []byte(llvm.JSONParseTreeSource()), 0644); err != nil {
		t.Fatalf("write JSON parse-tree source: %v", err)
	}
	return append(clangArgs, jsonFile)
}

// appendBufferCodecs compiles the Buffer codec C file (__kml_buf_* ABI, libc
// only) into the clang invocation when the program used a Buffer string
// codec, mirroring main.go so the test build and the real build can't drift
// (TDD-00103; same rationale as appendJSONParseTree).
func appendBufferCodecs(t *testing.T, em *llvm.Emitter, dir string, clangArgs []string) []string {
	t.Helper()
	if !em.UsesBufferCodecs() {
		return clangArgs
	}
	bcFile := filepath.Join(dir, "bufcodecs.c")
	if err := os.WriteFile(bcFile, []byte(llvm.BufferCodecsSource()), 0644); err != nil {
		t.Fatalf("write Buffer codec source: %v", err)
	}
	return append(clangArgs, bcFile)
}

// appendURLPattern compiles the URLPattern runtime C file (__kml_urlpattern_*
// ABI; pcre2 + curl, both already added via LinkLibs when used) into the clang
// invocation when the program constructed a URLPattern, mirroring main.go so
// the test build and the real build can't drift (TDD-00100).
func appendURLPattern(t *testing.T, em *llvm.Emitter, dir string, clangArgs []string) []string {
	t.Helper()
	if !em.UsesURLPattern() {
		return clangArgs
	}
	upFile := filepath.Join(dir, "urlpattern.c")
	if err := os.WriteFile(upFile, []byte(llvm.URLPatternSource()), 0644); err != nil {
		t.Fatalf("write URLPattern runtime source: %v", err)
	}
	return append(clangArgs, upFile)
}

// appendDtoa compiles the JS-faithful float formatter C file (__kml_dtoa, libc
// only) into the clang invocation when the program printed a float, mirroring
// main.go so the test build and the real build can't drift (TDD-00080).
func appendDtoa(t *testing.T, em *llvm.Emitter, dir string, clangArgs []string) []string {
	t.Helper()
	if !em.UsesFloatFmt() {
		return clangArgs
	}
	dtoaFile := filepath.Join(dir, "dtoa.c")
	if err := os.WriteFile(dtoaFile, []byte(llvm.DtoaSource()), 0644); err != nil {
		t.Fatalf("write dtoa source: %v", err)
	}
	return append(clangArgs, dtoaFile)
}

func buildBinaryGC(t *testing.T, src string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}

	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	em := llvm.NewEmitter()
	em.SetMemMode("gc")
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}

	dir := t.TempDir()
	llFile := filepath.Join(dir, "prog.ll")
	shimFile := filepath.Join(dir, "gcshim.c")
	binFile := filepath.Join(dir, "prog")

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
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, "-l"+lib)
	}
	clangArgs = appendJSONParseTree(t, em, dir, clangArgs)
	clangArgs = appendBufferCodecs(t, em, dir, clangArgs)
	clangArgs = appendDtoa(t, em, dir, clangArgs)
	clangArgs = appendURLPattern(t, em, dir, clangArgs)
	out, err := exec.Command("clang", clangArgs...).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "library not found for -lgc") || strings.Contains(string(out), "cannot find -lgc") {
			t.Skip("libgc/bdw-gc not installed")
		}
		t.Fatalf("clang: %v\n%s", err, out)
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
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}

	dir := t.TempDir()
	srcFile := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(srcFile, []byte(src), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	prog, err := resolver.ResolveProgram(srcFile)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	em := llvm.NewEmitter()
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}

	llFile := filepath.Join(dir, "prog.ll")
	binFile := filepath.Join(dir, "prog")

	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}

	clangArgs := []string{"-O2", llFile, "-o", binFile}
	if em.UsesWorkers() {
		clangArgs = append(clangArgs, "-pthread")
	}
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, "-l"+lib)
	}
	clangArgs, bigintUsed := appendBigIntBackend(t, em, dir, clangArgs)
	clangArgs = appendJSONParseTree(t, em, dir, clangArgs)
	clangArgs = appendBufferCodecs(t, em, dir, clangArgs)
	clangArgs = appendDtoa(t, em, dir, clangArgs)
	clangArgs = appendURLPattern(t, em, dir, clangArgs)
	out, err := exec.Command("clang", clangArgs...).CombinedOutput()
	if err != nil {
		if bigintUsed {
			t.Skipf("bigint backend %q may not be installed: clang: %v\n%s", em.BigIntBackend(), err, out)
		}
		t.Fatalf("clang: %v\n%s", err, out)
	}
	return binFile
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

	dir := t.TempDir()
	srcFile := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(srcFile, []byte(src), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	prog, err := resolver.ResolveProgram(srcFile)
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
	binFile := filepath.Join(dir, "prog")

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
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, "-l"+lib)
	}
	clangArgs = appendJSONParseTree(t, em, dir, clangArgs)
	clangArgs = appendBufferCodecs(t, em, dir, clangArgs)
	clangArgs = appendDtoa(t, em, dir, clangArgs)
	clangArgs = appendURLPattern(t, em, dir, clangArgs)
	out, err := exec.Command("clang", clangArgs...).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "library not found for -lgc") || strings.Contains(string(out), "cannot find -lgc") {
			t.Skip("libgc/bdw-gc not installed")
		}
		t.Fatalf("clang: %v\n%s", err, out)
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
	dir := t.TempDir()
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

	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	em := llvm.NewEmitter()
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}

	dir := t.TempDir()
	llFile := filepath.Join(dir, "prog.ll")
	asanOptFile := filepath.Join(dir, "asan_options.c")
	binFile := filepath.Join(dir, "prog")

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
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, "-l"+lib)
	}
	clangArgs = appendJSONParseTree(t, em, dir, clangArgs)
	clangArgs = appendBufferCodecs(t, em, dir, clangArgs)
	clangArgs = appendDtoa(t, em, dir, clangArgs)
	clangArgs = appendURLPattern(t, em, dir, clangArgs)
	out, err := exec.Command("clang", clangArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("clang: %v\n%s", err, out)
	}
	return binFile
}

// buildBinaryGCASan is buildBinaryGC + buildBinaryASan combined, for
// chasing GC-mode-specific memory corruption directly. Important caveat,
// confirmed by reading gcshim.c: it #define-overrides malloc/calloc/
// realloc/free to call straight through to GC_malloc/GC_realloc/GC_free,
// bypassing whatever malloc clang's ASan runtime would otherwise intercept
// — so ASan's heap redzone instrumentation does NOT cover GC_malloc'd
// memory under this build (gcshim.c's malloc "wins" the symbol, same as it
// does against plain libc malloc in every other build mode). ASan here
// still catches stack- and global-variable overflows (compile-time
// instrumented, unaffected by which allocator is linked) and UBSan's
// checks (signed overflow, misaligned access, etc.) still apply
// everywhere. For heap corruption specifically inside GC-managed memory,
// Valgrind (Memcheck) is the better tool — it instruments at the
// binary/instruction level, independent of which allocator is in play —
// though expect Boehm's own conservative stack-scanning to trigger some
// false-positive "uninitialized value" noise under Memcheck, a known,
// generally-suppressible pattern for conservative collectors.
func buildBinaryGCASan(t *testing.T, src string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}

	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	em := llvm.NewEmitter()
	em.SetMemMode("gc")
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}

	dir := t.TempDir()
	llFile := filepath.Join(dir, "prog.ll")
	shimFile := filepath.Join(dir, "gcshim.c")
	asanOptFile := filepath.Join(dir, "asan_options.c")
	binFile := filepath.Join(dir, "prog")

	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	if err := os.WriteFile(shimFile, []byte(llvm.GCShimSource), 0644); err != nil {
		t.Fatalf("write GC shim: %v", err)
	}
	if err := os.WriteFile(asanOptFile, []byte(asanOptionsSource), 0644); err != nil {
		t.Fatalf("write asan_options.c: %v", err)
	}

	cflags, libs, err := llvm.LocateGC()
	if err != nil {
		t.Skipf("gc mode: %v", err)
	}
	clangArgs := []string{
		"-O1", "-g", "-fno-omit-frame-pointer",
		"-fsanitize=address", "-fsanitize=undefined",
		llFile, shimFile, asanOptFile, "-o", binFile,
	}
	clangArgs = append(clangArgs, cflags...)
	clangArgs = append(clangArgs, libs...)
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, "-l"+lib)
	}
	clangArgs = appendJSONParseTree(t, em, dir, clangArgs)
	clangArgs = appendBufferCodecs(t, em, dir, clangArgs)
	clangArgs = appendDtoa(t, em, dir, clangArgs)
	clangArgs = appendURLPattern(t, em, dir, clangArgs)
	out, err := exec.Command("clang", clangArgs...).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "library not found for -lgc") || strings.Contains(string(out), "cannot find -lgc") {
			t.Skip("libgc/bdw-gc not installed")
		}
		t.Fatalf("clang: %v\n%s", err, out)
	}
	return binFile
}

// writeMultiFile writes each file in files (keyed by relative path, e.g.
// "math.ts") into a fresh temp directory and returns the directory.
func writeMultiFile(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
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
	binFile := filepath.Join(dir, "prog")
	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	clangArgs := []string{"-O2", llFile, "-o", binFile}
	if em.UsesWorkers() {
		clangArgs = append(clangArgs, "-pthread")
	}
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, "-l"+lib)
	}
	clangArgs, bigintUsed := appendBigIntBackend(t, em, dir, clangArgs)
	clangArgs = appendJSONParseTree(t, em, dir, clangArgs)
	clangArgs = appendBufferCodecs(t, em, dir, clangArgs)
	clangArgs = appendDtoa(t, em, dir, clangArgs)
	clangArgs = appendURLPattern(t, em, dir, clangArgs)
	out, err := exec.Command("clang", clangArgs...).CombinedOutput()
	if err != nil {
		if bigintUsed {
			t.Skipf("bigint backend %q may not be installed: clang: %v\n%s", em.BigIntBackend(), err, out)
		}
		t.Fatalf("clang: %v\n%s", err, out)
	}
	return binFile
}

// resolveMultiFilePermissive is resolveMultiFile's `-globals=permissive`
// (TDD-00050) counterpart.
func resolveMultiFilePermissive(t *testing.T, files map[string]string, entryName string) (*ast.Program, error) {
	t.Helper()
	dir := writeMultiFile(t, files)
	return resolver.ResolveProgramWithOptions(filepath.Join(dir, entryName), true)
}

// buildBinaryMultiFilePermissive is buildBinaryMultiFile's
// `-globals=permissive` (TDD-00050) counterpart.
func buildBinaryMultiFilePermissive(t *testing.T, files map[string]string, entryName string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	dir := writeMultiFile(t, files)

	prog, err := resolver.ResolveProgramWithOptions(filepath.Join(dir, entryName), true)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	em := llvm.NewEmitter()
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}

	llFile := filepath.Join(dir, "prog.ll")
	binFile := filepath.Join(dir, "prog")
	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	clangArgs := []string{"-O2", llFile, "-o", binFile}
	if em.UsesWorkers() {
		clangArgs = append(clangArgs, "-pthread")
	}
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, "-l"+lib)
	}
	clangArgs, bigintUsed := appendBigIntBackend(t, em, dir, clangArgs)
	clangArgs = appendJSONParseTree(t, em, dir, clangArgs)
	clangArgs = appendBufferCodecs(t, em, dir, clangArgs)
	clangArgs = appendDtoa(t, em, dir, clangArgs)
	clangArgs = appendURLPattern(t, em, dir, clangArgs)
	out, err := exec.Command("clang", clangArgs...).CombinedOutput()
	if err != nil {
		if bigintUsed {
			t.Skipf("bigint backend %q may not be installed: clang: %v\n%s", em.BigIntBackend(), err, out)
		}
		t.Fatalf("clang: %v\n%s", err, out)
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
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	em := llvm.NewEmitter()
	em.SetRegexMode(mode)
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}
	dir := t.TempDir()
	llFile := filepath.Join(dir, "prog.ll")
	binFile := filepath.Join(dir, "prog")
	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	clangArgs := []string{"-O2", llFile, "-o", binFile}
	if em.UsesWorkers() {
		clangArgs = append(clangArgs, "-pthread")
	}
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, "-l"+lib)
	}
	clangArgs, bigintUsed := appendBigIntBackend(t, em, dir, clangArgs)
	clangArgs = appendJSONParseTree(t, em, dir, clangArgs)
	clangArgs = appendBufferCodecs(t, em, dir, clangArgs)
	clangArgs = appendDtoa(t, em, dir, clangArgs)
	clangArgs = appendURLPattern(t, em, dir, clangArgs)
	out, err := exec.Command("clang", clangArgs...).CombinedOutput()
	if err != nil {
		if bigintUsed {
			t.Skipf("bigint backend %q may not be installed: clang: %v\n%s", em.BigIntBackend(), err, out)
		}
		t.Fatalf("clang: %v\n%s", err, out)
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
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	em := llvm.NewEmitter()
	em.SetCompatMode("js")
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}
	dir := t.TempDir()
	llFile := filepath.Join(dir, "prog.ll")
	binFile := filepath.Join(dir, "prog")
	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	clangArgs := []string{"-O2", llFile, "-o", binFile}
	if em.UsesWorkers() {
		clangArgs = append(clangArgs, "-pthread")
	}
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, "-l"+lib)
	}
	clangArgs, bigintUsed := appendBigIntBackend(t, em, dir, clangArgs)
	clangArgs = appendJSONParseTree(t, em, dir, clangArgs)
	clangArgs = appendBufferCodecs(t, em, dir, clangArgs)
	clangArgs = appendDtoa(t, em, dir, clangArgs)
	clangArgs = appendURLPattern(t, em, dir, clangArgs)
	out, err := exec.Command("clang", clangArgs...).CombinedOutput()
	if err != nil {
		if bigintUsed {
			t.Skipf("bigint backend %q may not be installed: clang: %v\n%s", em.BigIntBackend(), err, out)
		}
		t.Fatalf("clang: %v\n%s", err, out)
	}
	return binFile
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

// compileAndRunWithStdin is like compileAndRun but feeds stdin to the binary.
func compileAndRunWithStdin(t *testing.T, src, stdin string) string {
	t.Helper()
	binFile := buildBinary(t, src)
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
