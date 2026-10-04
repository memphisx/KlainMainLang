package tests

import (
	"KlainMainLang/codegen/llvm"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildCLI builds the klainmain CLI binary once per test that needs it — the
// island backend (TDD-00056) runs in main.go's clang orchestration, not the
// in-process emitter the other E2E helpers use, so these tests shell out.
func buildCLI(t *testing.T) string {
	t.Helper()
	root := findRepoRoot(t)
	bin := filepath.Join(tempDir(t), "klainmain"+llvm.HostExeSuffix())
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build klainmain: %v\n%s", err, out)
	}
	return bin
}

// findRepoRoot walks up from the working directory to the module root (the
// directory holding go.mod). Robust to how the suite is launched: `go test
// ./tests/` runs from the package dir, while `make test-par` executes the
// precompiled test binary from the repo root — a fixed `..` is wrong for one of
// them, but the go.mod probe finds the root in both.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found walking up from working directory")
		}
		dir = parent
	}
}

// TestE2EDynamicImportIsolated exercises the full lazy dynamic import (TDD-00056):
// the target is compiled to a shared-library island loaded on first import, its
// top-level runs lazily (not at startup), and its typed exports are read back
// through the result object.
func TestE2EDynamicImportIsolated(t *testing.T) {
	cli := buildCLI(t)
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "mod.ts"),
		"export const answer: number = 42;\nexport const greeting: string = \"kalimera\";\nconsole.log(\"island top-level\");\n")
	writeFile(t, filepath.Join(dir, "entry.ts"),
		"async function main(): Promise<void> {\n"+
			"  console.log(\"before\");\n"+
			"  const m = await import('./mod');\n"+
			"  console.log(m.greeting, m.answer);\n"+
			"}\nmain();\n")

	entry := filepath.Join(dir, "entry.ts")
	compile := exec.Command(cli, "-dynamic-import=isolated", entry)
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	bin := filepath.Join(dir, "entry")
	run := exec.Command(bin)
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	got := string(out)
	// Laziness: the island's top-level must run AFTER "before", not at startup.
	want := "before\nisland top-level\nkalimera 42\n"
	if got != want {
		t.Errorf("lazy dynamic import output:\ngot  %q\nwant %q", got, want)
	}
}

// TestE2EDynamicImportIsolatedNonASCIIPath: the island is located beside the
// executable, so the loader must survive an install directory whose name is not
// representable in a narrow code page (on Windows that means the wide
// GetModuleFileNameW/LoadLibraryW boundary, not the ANSI one).
func TestE2EDynamicImportIsolatedNonASCIIPath(t *testing.T) {
	cli := buildCLI(t)
	dir := filepath.Join(tempDir(t), "καλημέρα-日本")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(dir, "mod.ts"), "export const answer: number = 7;\n")
	writeFile(t, filepath.Join(dir, "entry.ts"),
		"async function main(): Promise<void> {\n"+
			"  const m = await import('./mod');\n"+
			"  console.log(m.answer);\n"+
			"}\nmain();\n")
	compile := exec.Command(cli, "-dynamic-import=isolated", filepath.Join(dir, "entry.ts"))
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	out, err := exec.Command(filepath.Join(dir, "entry")).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if string(out) != "7\n" {
		t.Errorf("got %q, want %q", out, "7\n")
	}
}

// TestE2EDynamicImportNonLiteralRejected confirms a runtime-computed specifier
// is a clean compile error, not a silent gap.
func TestE2EDynamicImportNonLiteralRejected(t *testing.T) {
	cli := buildCLI(t)
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "entry.ts"),
		"const p = \"./mod\";\nasync function main(): Promise<void> { await import(p); }\nmain();\n")
	compile := exec.Command(cli, "-dynamic-import=isolated", filepath.Join(dir, "entry.ts"))
	out, err := compile.CombinedOutput()
	if err == nil {
		t.Fatalf("expected compile failure for non-literal specifier, got success")
	}
	if !strings.Contains(string(out), "string-literal specifier") {
		t.Errorf("expected string-literal-specifier error, got: %s", out)
	}
}

// TestE2EDynamicImportIsolatedIslandNoAny: a lazy island compiles under the
// program's own modes, so --no-any rejects an `any` in the imported module
// exactly as it would in the entry file.
func TestE2EDynamicImportIsolatedIslandNoAny(t *testing.T) {
	cli := buildCLI(t)
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "mod.ts"), "let x: any = 1;\nexport const answer: number = 42;\nconsole.log(x);\n")
	writeFile(t, filepath.Join(dir, "entry.ts"),
		"async function main(): Promise<void> {\n"+
			"  const m = await import('./mod');\n"+
			"  console.log(m.answer);\n"+
			"}\nmain();\n")
	out, err := exec.Command(cli, "--no-any", "-dynamic-import=isolated", filepath.Join(dir, "entry.ts")).CombinedOutput()
	if err == nil {
		t.Fatalf("expected --no-any to reject the island's any, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "banned under --no-any") || !strings.Contains(string(out), "mod.ts") {
		t.Errorf("expected the island's --no-any error, got: %s", out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// runIsolatedIsland compiles entry.ts (with the given island files) under
// -dynamic-import=isolated and runs it, returning combined output and exit code.
func runIsolatedIsland(t *testing.T, files map[string]string) (string, int) {
	t.Helper()
	return runDynImport(t, "isolated", files)
}

// runDynImport compiles entry.ts (with the given files) under
// -dynamic-import=mode and runs it, returning combined output and exit code.
func runDynImport(t *testing.T, mode string, files map[string]string) (string, int) {
	t.Helper()
	cli := buildCLI(t)
	dir := tempDir(t)
	for name, src := range files {
		writeFile(t, filepath.Join(dir, name), src)
	}
	compile := exec.Command(cli, "-dynamic-import="+mode, filepath.Join(dir, "entry.ts"))
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	run := exec.Command(filepath.Join(dir, "entry"))
	out, _ := run.CombinedOutput()
	return string(out), run.ProcessState.ExitCode()
}

// --- TDD-00225: an island with a top-level await is driven by the importer's
// loop (ADR-01056) — the importer's own microtasks and timers interleave with
// the island's awaits, as in Node, instead of waiting behind a nested loop. ---

func TestE2EDynamicImportIsolatedIslandTopLevelAwaitInterleaves(t *testing.T) {
	out, code := runIsolatedIsland(t, map[string]string{
		"mod.ts": "const later = (ms: number) => new Promise<number>((res) => setTimeout(() => res(42), ms));\n" +
			"console.log(\"island: start\");\n" +
			"export const v: number = await later(30);\n" +
			"console.log(\"island: after await\", v);\n" +
			"export const after: string = \"done\";\n",
		"entry.ts": "console.log(\"main: start\");\n" +
			"setTimeout(() => console.log(\"main: timer 10\"), 10);\n" +
			"Promise.resolve().then(() => console.log(\"main: microtask\"));\n" +
			"const m = await import('./mod');\n" +
			"console.log(\"main: got\", m.v, m.after);\n" +
			"setTimeout(() => console.log(\"main: timer after\"), 5);\n",
	})
	want := "main: start\nisland: start\nmain: microtask\nmain: timer 10\nisland: after await 42\nmain: got 42 done\nmain: timer after\n"
	if out != want || code != 0 {
		t.Errorf("exit %d, output:\ngot  %q\nwant %q", code, out, want)
	}
}

// import() from inside an async function: the importer's synchronous tail and
// its timer run while the island waits.
func TestE2EDynamicImportIsolatedIslandAwaitFromAsyncFunction(t *testing.T) {
	out, code := runIsolatedIsland(t, map[string]string{
		"mod.ts": "const later = (ms: number) => new Promise<number>((res) => setTimeout(() => res(42), ms));\n" +
			"console.log(\"island: start\");\n" +
			"export const v: number = await later(30);\n" +
			"console.log(\"island: after await\", v);\n",
		"entry.ts": "async function load(): Promise<number> {\n" +
			"  console.log(\"load: before\");\n" +
			"  const m = await import('./mod');\n" +
			"  console.log(\"load: after\", m.v);\n" +
			"  return m.v;\n}\n" +
			"setTimeout(() => console.log(\"main: timer 15\"), 15);\n" +
			"load().then((v) => console.log(\"main: loaded\", v));\n" +
			"console.log(\"main: sync end\");\n",
	})
	want := "load: before\nisland: start\nmain: sync end\nmain: timer 15\nisland: after await 42\nload: after 42\nmain: loaded 42\n"
	if out != want || code != 0 {
		t.Errorf("exit %d, output:\ngot  %q\nwant %q", code, out, want)
	}
}

// A throw after the island's top-level await rejects the import() promise
// with the island's Error object.
func TestE2EDynamicImportIsolatedIslandThrowRejectsImport(t *testing.T) {
	out, code := runIsolatedIsland(t, map[string]string{
		"mod.ts": "export const x: number = 1;\n" +
			"await new Promise<void>((r) => setTimeout(r, 5));\n" +
			"throw new RangeError(\"island exploded\");\n",
		"entry.ts": "try {\n  const m = await import('./mod');\n  console.log(\"unexpected\", m.x);\n} catch (e) {\n" +
			"  console.log(\"caught:\", (e as Error).name, (e as Error).message, e instanceof RangeError);\n}\nconsole.log(\"after\");\n",
	})
	want := "caught: RangeError island exploded true\nafter\n"
	if out != want || code != 0 {
		t.Errorf("exit %d, output:\ngot  %q\nwant %q", code, out, want)
	}
}

// An island whose top-level await can never settle: the importer's await of
// import() is an unsettled top-level await — Node's warning, exit code 13.
func TestE2EDynamicImportIsolatedIslandUnsettledExits13(t *testing.T) {
	out, code := runIsolatedIsland(t, map[string]string{
		"mod.ts":   "export const y: number = 2;\nconsole.log(\"island: start\");\nawait new Promise<void>(() => {});\n",
		"entry.ts": "console.log(\"before\");\nconst m = await import('./mod');\nconsole.log(\"never\", m.y);\n",
	})
	if code != 13 || !strings.Contains(out, "unsettled top-level await") || strings.Contains(out, "never") {
		t.Errorf("exit %d, output:\n%s", code, out)
	}
}

// --- bundled (the default; TDD-00238 Stage 5): the target is compiled in,
// runs on first import(), and shares every module instance with the
// program, as in Node. lazy has the same semantics with the target's code
// in a shared library, so every test runs under both. Expected outputs are
// Node 24's. ---

// sharedModeImport runs files under each Node-semantics backend.
func sharedModeImport(t *testing.T, files map[string]string, check func(t *testing.T, out string, code int)) {
	for _, mode := range []string{"bundled", "lazy"} {
		t.Run(mode, func(t *testing.T) {
			out, code := runDynImport(t, mode, files)
			check(t, out, code)
		})
	}
}

// A builtin module the program and the target both import is one instance.
func TestE2EDynamicImportBundledSharesModules(t *testing.T) {
	sharedModeImport(t, map[string]string{
		"mod.ts": "import { EventEmitter } from 'node:events';\n" +
			"export const seen: number = EventEmitter.defaultMaxListeners;\n",
		"entry.ts": "import { EventEmitter } from 'node:events';\n" +
			"EventEmitter.defaultMaxListeners = 5;\n" +
			"const m = await import('./mod');\nconsole.log('seen', m.seen);\n",
	}, func(t *testing.T, out string, code int) {
		if want := "seen 5\n"; out != want || code != 0 {
			t.Errorf("exit %d, output:\ngot  %q\nwant %q", code, out, want)
		}
	})
}

// The target runs once, after the importer's synchronous code; a user file
// the program already imports is not run again; an import() never reached
// runs nothing; a top-level throw rejects every import() of the target.
func TestE2EDynamicImportBundledEvaluation(t *testing.T) {
	sharedModeImport(t, map[string]string{
		"shared.ts": "export let counter: number = 0;\n" +
			"export function bump(): number { counter++; return counter; }\n" +
			"console.log('shared evaluated');\n",
		"a.ts": "import { bump } from './shared';\nconsole.log('a top start');\n" +
			"export const fromA: number = bump();\n" +
			"await new Promise<void>((r) => setTimeout(r, 10));\n" +
			"console.log('a after await');\nexport const label: string = 'A';\n",
		"never.ts": "console.log('never must not print');\nexport const x: number = 1;\n",
		"bad.ts":   "console.log('bad runs');\nthrow new Error('boom');\n",
		"entry.ts": "import { bump } from './shared';\nconsole.log('main start', bump());\n" +
			"if (process.argv.length > 99) { await import('./never'); }\n" +
			"const p1 = import('./a');\nconst p2 = import('./a');\nconsole.log('after import calls');\n" +
			"const m1 = await p1;\nconst m2 = await p2;\nconsole.log('a', m1.fromA, m1.label, m2.fromA);\n" +
			"console.log('bump now', bump());\n" +
			"try { await import('./bad'); } catch (e) { console.log('caught', (e as Error).message); }\n" +
			"try { await import('./bad'); } catch (e) { console.log('caught again', (e as Error).message); }\n",
	}, func(t *testing.T, out string, code int) {
		want := "shared evaluated\nmain start 1\nafter import calls\na top start\na after await\na 2 A 2\n" +
			"bump now 3\nbad runs\ncaught boom\ncaught again boom\n"
		if out != want || code != 0 {
			t.Errorf("exit %d, output:\ngot  %q\nwant %q", code, out, want)
		}
	})
}

// The target is evaluated, and import() settles, on the microtasks Node's
// module loader takes.
func TestE2EDynamicImportBundledMicrotaskOrder(t *testing.T) {
	sharedModeImport(t, map[string]string{
		"t.ts": "console.log('t evaluated');\nexport const z: number = 1;\n",
		"entry.ts": "import('./t').then(() => console.log('import settled'));\n" +
			"import('./t').then(() => console.log('import2 settled'));\n" +
			"let p: Promise<void> = Promise.resolve();\n" +
			"for (let i = 0; i < 12; i++) { const k = i; p = p.then(() => { console.log('micro', k); }); }\n",
	}, func(t *testing.T, out string, code int) {
		want := "micro 0\nmicro 1\nmicro 2\nmicro 3\nt evaluated\nmicro 4\nmicro 5\nmicro 6\nmicro 7\nmicro 8\nmicro 9\n" +
			"import settled\nimport2 settled\nmicro 10\nmicro 11\n"
		if out != want || code != 0 {
			t.Errorf("exit %d, output:\ngot  %q\nwant %q", code, out, want)
		}
	})
}

// A target's top-level await parks on the program's one event loop.
func TestE2EDynamicImportBundledTopLevelAwaitInterleaves(t *testing.T) {
	sharedModeImport(t, map[string]string{
		"mod.ts": "const later = (ms: number) => new Promise<number>((res) => setTimeout(() => res(42), ms));\n" +
			"console.log(\"island: start\");\n" +
			"export const v: number = await later(30);\n" +
			"console.log(\"island: after await\", v);\n" +
			"export const after: string = \"done\";\n",
		"entry.ts": "console.log(\"main: start\");\n" +
			"setTimeout(() => console.log(\"main: timer 10\"), 10);\n" +
			"Promise.resolve().then(() => console.log(\"main: microtask\"));\n" +
			"const m = await import('./mod');\n" +
			"console.log(\"main: got\", m.v, m.after);\n" +
			"setTimeout(() => console.log(\"main: timer after\"), 5);\n",
	}, func(t *testing.T, out string, code int) {
		want := "main: start\nmain: microtask\nisland: start\nmain: timer 10\nisland: after await 42\nmain: got 42 done\nmain: timer after\n"
		if out != want || code != 0 {
			t.Errorf("exit %d, output:\ngot  %q\nwant %q", code, out, want)
		}
	})
}

// A target whose top-level await never settles: Node's warning, exit 13.
func TestE2EDynamicImportBundledUnsettledExits13(t *testing.T) {
	sharedModeImport(t, map[string]string{
		"mod.ts":   "export const y: number = 2;\nconsole.log(\"island: start\");\nawait new Promise<void>(() => {});\n",
		"entry.ts": "console.log(\"before\");\nconst m = await import('./mod');\nconsole.log(\"never\", m.y);\n",
	}, func(t *testing.T, out string, code int) {
		if code != 13 || !strings.Contains(out, "unsettled top-level await") || strings.Contains(out, "never") {
			t.Errorf("exit %d, output:\n%s", code, out)
		}
	})
}
