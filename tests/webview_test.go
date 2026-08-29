package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/resolver"
)

// webview_test.go — TDD-00142's three-tier testing posture.
//
//   - TestE2EWebviewCompiles / …Bind / …Methods are COMPILE-TIER: they build a
//     real binary (the link line proves the C++/framework plumbing) but never
//     launch it, so they run everywhere a display isn't needed — including
//     headless Linux CI (where they Skip cleanly if the WebKitGTK dev packages
//     are absent).
//   - TestE2EWebviewSmoke is the WINDOWED smoke run: it creates a window, has
//     the page call a bound native function that terminate()s the loop, and
//     asserts the roundtrip output. It needs a display, so it is gated behind
//     KML_WEBVIEW_SMOKE=1 (set by hand on a dev machine) and thus excluded from
//     make test-par's automatic bucket. Verified by hand on macOS.

func TestE2EWebviewCompiles(t *testing.T) {
	src := `
import { Webview } from 'klain:webview'
const w = new Webview({ title: "T", width: 400, height: 300, debug: false })
w.navigate("http://127.0.0.1:8080/")
w.setTitle("Renamed")
w.setSize(500, 400)
w.init("console.log('x')")
w.eval("document.title='y'")
w.terminate()
`
	bin := buildBinaryImports(t, src)
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("webview binary not produced: %v", err)
	}
}

func TestE2EWebviewBindCompiles(t *testing.T) {
	src := `
import { Webview } from 'klain:webview'
const w = new Webview({ title: "T" })
w.bind("save", (args: string): string => {
  const req = JSON.parse(args)
  return JSON.stringify({ ok: true })
})
w.unbind("save")
w.html("<!doctype html><h1>hi</h1>")
w.terminate()
`
	bin := buildBinaryImports(t, src)
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("webview bind binary not produced: %v", err)
	}
}

// TestE2EWebviewPumpCompiles exercises the Stage 3 loop-fusion path: a webview
// program that also uses timers links the page-tick pump (compile-tier).
func TestE2EWebviewPumpCompiles(t *testing.T) {
	src := `
import { Webview } from 'klain:webview'
const w = new Webview({ title: "T" })
let n = 0
const iv = setInterval(() => { n = n + 1; if (n >= 2) { clearInterval(iv); w.terminate() } }, 20)
w.html("<!doctype html><h1>hi</h1>")
w.run()
`
	bin := buildBinaryImports(t, src)
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("webview pump binary not produced: %v", err)
	}
}

// TestE2EWebviewAsyncBindCompiles exercises the Stage 3 async-bind path: a
// bound callback returning a Promise<string> (compile-tier).
func TestE2EWebviewAsyncBindCompiles(t *testing.T) {
	src := `
import { Webview } from 'klain:webview'
const w = new Webview({ title: "T" })
w.bind("compute", async (args: string): Promise<string> => {
  return JSON.stringify({ ok: true })
})
w.html("<!doctype html><h1>hi</h1>")
w.run()
`
	bin := buildBinaryImports(t, src)
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("webview async-bind binary not produced: %v", err)
	}
}

// TestE2EWebviewTypedBindingsCompiles exercises Stage 5 typed bind: a `bindings`
// object (literal + variable form) with mixed parameter types, plus bindTyped.
func TestE2EWebviewTypedBindingsCompiles(t *testing.T) {
	// Exercises the variable `bindings` form (mixed param/return types) plus the
	// imperative bindTyped. (Two Webviews per program is rejected, so this uses
	// one window; the literal form is covered by the windowed smoke test.)
	src := `
import { Webview } from 'klain:webview'
interface Point { x: number; y: number }
const api = {
  add: (a: number, b: number): number => a + b,
  greet: (name: string): string => "hi " + name,
  mkPoint: (x: number, y: number): Point => ({ x: x, y: y }),
}
const w = new Webview({ title: "T", bindings: api })
w.bindTyped("mul", (a: number, b: number): number => a * b)
w.html("<h1>x</h1>")
w.terminate()
`
	bin := buildBinaryImports(t, src)
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("typed bindings binary not produced: %v", err)
	}
}

// TestE2ETupleJSONParse locks in the free side-benefit of the tuple projection
// that typed bind added: JSON.parse into a tuple type.
func TestE2ETupleJSONParse(t *testing.T) {
	out := compileAndRun(t, `
const t: [number, string] = JSON.parse('[42, "hello"]')
console.log(t[0] + " " + t[1])
`)
	if out != "42 hello" {
		t.Fatalf("tuple JSON.parse: got %q, want %q", out, "42 hello")
	}
}

// TestE2EWebviewBindingsNonFunctionRejected asserts a non-function bindings
// field is a clean compile-time rejection.
func TestE2EWebviewBindingsNonFunctionRejected(t *testing.T) {
	err := resolveAndEmitMultiFile(t, map[string]string{"main.ts": `
import { Webview } from 'klain:webview'
const w = new Webview({ title: "T", bindings: { x: 5 } })
`}, "main.ts")
	if err == nil || !strings.Contains(err.Error(), "must be a function") {
		t.Fatalf("expected a non-function bindings rejection, got: %v", err)
	}
}

// TestE2EWebviewAsyncTypedCompiles exercises Stage 6 async typed bind: a typed
// binding returning a Promise<T> (settled value JSON-encoded), plus a
// nested-tuple parameter.
func TestE2EWebviewAsyncTypedCompiles(t *testing.T) {
	src := `
import { Webview } from 'klain:webview'
const w = new Webview({ title: "T", bindings: {
  slowAdd: async (a: number, b: number): Promise<number> => a + b,
  sumPair: (p: [number, number]): number => p[0] + p[1],
} })
w.html("<h1>x</h1>")
w.terminate()
`
	bin := buildBinaryImports(t, src)
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("async-typed/nested-tuple binary not produced: %v", err)
	}
}

// TestWindowDTS asserts --emit-window-dts renders the expected TypeScript for a
// program's typed bindings (Stage 6). It resolves + emits directly and inspects
// the emitter's WindowDTS output.
func TestWindowDTS(t *testing.T) {
	dir := writeMultiFile(t, map[string]string{"main.ts": `
import { Webview } from 'klain:webview'
interface Point { x: number; y: number }
const w = new Webview({ title: "T", bindings: {
  add: (a: number, b: number): number => a + b,
  greet: (name: string): string => "hi " + name,
  mkPoint: (x: number, y: number): Point => ({ x: x, y: y }),
  fetchUser: async (id: number): Promise<Point> => ({ x: id, y: id }),
} })
w.terminate()
`})
	prog, err := resolver.ResolveProgram(filepath.Join(dir, "main.ts"))
	if err != nil {
		t.Fatal(err)
	}
	em := llvm.NewEmitter()
	if _, err := em.EmitProgram(prog); err != nil {
		t.Fatal(err)
	}
	dts := em.WindowDTS()
	for _, want := range []string{
		"interface Window {",
		"add(arg0: number, arg1: number): Promise<number>",
		"greet(arg0: string): Promise<string>",
		"mkPoint(arg0: number, arg1: number): Promise<{ x: number; y: number }>",
		"fetchUser(arg0: number): Promise<{ x: number; y: number }>",
	} {
		if !strings.Contains(dts, want) {
			t.Errorf(".d.ts missing %q\n%s", want, dts)
		}
	}
}

// TestE2EWebviewAsyncTypedSmoke is the Stage 6 windowed run: a page awaits an
// async typed binding (Promise<number>) and a nested-tuple-param binding.
func TestE2EWebviewAsyncTypedSmoke(t *testing.T) {
	if os.Getenv("KML_WEBVIEW_SMOKE") != "1" {
		t.Skip("windowed smoke test — set KML_WEBVIEW_SMOKE=1 to run")
	}
	src := `
import { Webview } from 'klain:webview'
const w = new Webview({ title: "AT", width: 320, height: 200, bindings: {
  slowAdd: async (a: number, b: number): Promise<number> => a + b,
  sumPair: (p: [number, number]): number => p[0] + p[1],
} })
w.bind("report", (raw: string): string => {
  console.log("page reported: " + raw)
  w.terminate()
  return "null"
})
w.init(` + "`" + `window.addEventListener('load', async () => {
  const s = await window.slowAdd(10, 7)
  const p = await window.sumPair([3, 4])
  window.report('slowAdd=' + s + ' sumPair=' + p)
})` + "`" + `)
w.html("<!doctype html><html><body>at</body></html>")
w.run()
console.log("run returned")
`
	runWebviewWindow(t, src, "slowAdd=17", "sumPair=7", "run returned")
}

// TestE2EWebviewTypedBindSmoke is the Stage 5 windowed run: a page calls typed
// bindings (scalar + object) and reports the decoded/encoded round-trip.
func TestE2EWebviewTypedBindSmoke(t *testing.T) {
	if os.Getenv("KML_WEBVIEW_SMOKE") != "1" {
		t.Skip("windowed smoke test — set KML_WEBVIEW_SMOKE=1 to run")
	}
	src := `
import { Webview } from 'klain:webview'
interface Point { x: number; y: number }
const w = new Webview({ title: "Typed", width: 320, height: 200, bindings: {
  add: (a: number, b: number): number => a + b,
  mkPoint: (x: number, y: number): Point => ({ x: x, y: y }),
} })
w.bind("report", (raw: string): string => {
  console.log("page reported: " + raw)
  w.terminate()
  return "null"
})
w.init(` + "`" + `window.addEventListener('load', async () => {
  const sum = await window.add(2, 3)
  const p = await window.mkPoint(4, 5)
  window.report('sum=' + sum + ' px=' + p.x)
})` + "`" + `)
w.html("<!doctype html><html><body>typed</body></html>")
w.run()
console.log("run returned")
`
	runWebviewWindow(t, src, "sum=5", "px=4", "run returned")
}

// TestE2EWebviewSecondWindowRejected asserts the one-window-per-process V1
// constraint is a clean compile-time rejection.
func TestE2EWebviewSecondWindowRejected(t *testing.T) {
	src := `
import { Webview } from 'klain:webview'
const a = new Webview({ title: "A" })
const b = new Webview({ title: "B" })
`
	err := resolveAndEmitMultiFile(t, map[string]string{"main.ts": src}, "main.ts")
	if err == nil {
		t.Fatal("expected a rejection for a second Webview construction")
	}
	if !strings.Contains(err.Error(), "one Webview window per process") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestE2EWebviewSmoke(t *testing.T) {
	if os.Getenv("KML_WEBVIEW_SMOKE") != "1" && runtime.GOOS != "darwin" {
		t.Skip("windowed smoke test — set KML_WEBVIEW_SMOKE=1 (needs a display)")
	}
	if os.Getenv("KML_WEBVIEW_SMOKE") != "1" {
		// Even on darwin, only run when explicitly asked (a window pops up).
		t.Skip("windowed smoke test — set KML_WEBVIEW_SMOKE=1 to run")
	}
	src := `
import { Webview } from 'klain:webview'
const w = new Webview({ title: "Smoke", width: 320, height: 200 })
w.bind("finish", (args: string): string => {
  console.log("native got: " + args)
  w.terminate()
  return JSON.stringify({ ok: true })
})
w.init("window.addEventListener('load', () => { window.finish(JSON.stringify(['hello'])); });")
w.html("<!doctype html><html><body>roundtrip</body></html>")
w.run()
console.log("run returned")
`
	runWebviewWindow(t, src, "native got:", "run returned")
}

// TestE2EWebviewPumpSmoke is the Stage 3 loop-fusion windowed run: a setInterval
// ticks on the GUI thread (no Worker) and terminate()s. Gated like the roundtrip
// smoke above.
func TestE2EWebviewPumpSmoke(t *testing.T) {
	if os.Getenv("KML_WEBVIEW_SMOKE") != "1" {
		t.Skip("windowed smoke test — set KML_WEBVIEW_SMOKE=1 to run")
	}
	src := `
import { Webview } from 'klain:webview'
const w = new Webview({ title: "Pump", width: 320, height: 200 })
let ticks = 0
const iv = setInterval(() => {
  ticks = ticks + 1
  console.log("tick " + ticks)
  if (ticks >= 3) { clearInterval(iv); w.terminate() }
}, 40)
w.html("<!doctype html><html><body>pump</body></html>")
w.run()
console.log("run returned after " + ticks + " ticks")
`
	runWebviewWindow(t, src, "tick 3", "run returned after 3 ticks")
}

// TestE2EWebviewAsyncBindSmoke is the Stage 3 async-bind windowed run: a bound
// async callback's Promise settles and its value reaches the page, which reports
// it back to native. Gated like the roundtrip smoke above.
func TestE2EWebviewAsyncBindSmoke(t *testing.T) {
	if os.Getenv("KML_WEBVIEW_SMOKE") != "1" {
		t.Skip("windowed smoke test — set KML_WEBVIEW_SMOKE=1 to run")
	}
	src := `
import { Webview } from 'klain:webview'
const w = new Webview({ title: "Async", width: 320, height: 200 })
w.bind("compute", async (args: string): Promise<string> => {
  return JSON.stringify({ doubled: 42 })
})
w.bind("report", (args: string): string => {
  console.log("page received: " + args)
  w.terminate()
  return JSON.stringify({ ok: true })
})
w.init("window.addEventListener('load', () => { window.compute('[]').then(r => window.report(JSON.stringify(r))) })")
w.html("<!doctype html><html><body>async</body></html>")
w.run()
console.log("run returned")
`
	runWebviewWindow(t, src, "page received:", "doubled")
}

// runWebviewWindow compiles src, runs the windowed binary, and asserts every
// needle appears in its output. Shared by the gated Stage 1–3 smoke tests.
func runWebviewWindow(t *testing.T, src string, needles ...string) {
	t.Helper()
	bin := buildBinaryImports(t, src)
	out, _ := exec.Command(bin).CombinedOutput()
	got := string(out)
	for _, n := range needles {
		if !strings.Contains(got, n) {
			t.Fatalf("windowed run missing %q; output:\n%s", n, got)
		}
	}
}
