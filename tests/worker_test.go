package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/resolver"
)

// --- Worker (worker_threads) — TDD-00098 Stage 3 ---

// workerCompileError resolves+emits a multi-file program and asserts a
// compile-stage error containing wantSub (from either the resolver or
// codegen), optionally under -mm=gc.
func workerCompileError(t *testing.T, files map[string]string, entryName, mm, wantSub string) {
	t.Helper()
	dir := writeMultiFile(t, files)
	prog, err := resolver.ResolveProgram(filepath.Join(dir, entryName))
	if err == nil {
		em := llvm.NewEmitter()
		if mm != "" {
			em.SetMemMode(mm)
		}
		_, err = em.EmitProgram(prog)
	}
	if err == nil {
		t.Fatalf("expected a compile error containing %q, got success", wantSub)
	}
	if !strings.Contains(err.Error(), wantSub) {
		t.Fatalf("expected error containing %q, got: %v", wantSub, err)
	}
}

func TestE2EWorkerEchoRoundTripAndExit(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"echo_worker.ts": `
import { parentPort, workerData } from 'worker_threads';
const greeting: string = workerData;
console.log("worker started with: " + greeting);
parentPort.on('message', (msg: number) => {
    parentPort.postMessage(msg * 2);
});
`,
		"main.ts": `
import { Worker } from 'worker_threads';
const w = new Worker('./echo_worker.ts', { workerData: "hello" });
w.on('message', (n: number) => {
    console.log("got back: " + n);
    if (n >= 8) { w.terminate(); } else { w.postMessage(n); }
});
w.on('exit', (code: number) => { console.log("worker exited: " + code); });
w.postMessage(1);
`,
	}, "main.ts", "worker started with: hello\ngot back: 2\ngot back: 4\ngot back: 8\nworker exited: 1")
}

func TestE2EWorkerTwoConcurrentWorkersThrowCatch(t *testing.T) {
	// The thread_local regression test: two workers concurrently pushing/
	// popping their own setjmp stacks (nested try/catch, 20000 throws each).
	// With process-global exception state this corrupts and crashes.
	assertMultiFileOutput(t, map[string]string{
		"thrower.ts": `
import { parentPort, workerData } from 'worker_threads';
const id: number = workerData;
parentPort.on('message', (rounds: number) => {
    let caught = 0;
    for (let i = 0; i < rounds; i++) {
        try {
            if (i % 2 === 0) { throw new Error("even"); }
            try { throw new Error("nested"); } catch (inner) { caught++; }
        } catch (outer) {
            caught++;
        }
    }
    parentPort.postMessage(caught);
});
`,
		"main.ts": `
import { Worker } from 'worker_threads';
const a = new Worker('./thrower.ts', { workerData: 1 });
const b = new Worker('./thrower.ts', { workerData: 2 });
let total = 0;
let done = 0;
const finish = () => {
    done++;
    if (done === 2) { console.log("total: " + total); }
};
a.on('message', (c: number) => { total += c; a.terminate(); finish(); });
b.on('message', (c: number) => { total += c; b.terminate(); finish(); });
a.postMessage(20000);
b.postMessage(20000);
`,
	}, "main.ts", "total: 40000")
}

func TestE2EWorkerObjectAndArrayPayload(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"shapes_worker.ts": `
import { parentPort } from 'worker_threads';
parentPort.on('message', (req: { name: string, nums: number[] }) => {
    let sum = 0;
    for (const n of req.nums) { sum += n; }
    parentPort.postMessage("hello " + req.name + ", sum=" + sum);
});
`,
		"main.ts": `
import { Worker } from 'worker_threads';
const w = new Worker('./shapes_worker.ts');
w.on('message', (reply: string) => {
    console.log(reply);
    w.terminate();
});
w.postMessage({ name: "Thessaloniki", nums: [1, 2, 3, 4, 5] });
`,
	}, "main.ts", "hello Thessaloniki, sum=15")
}

func TestE2EWorkerUsesTimersInternally(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"timer_worker.ts": `
import { parentPort } from 'worker_threads';
parentPort.on('message', (n: number) => {
    setTimeout(() => { parentPort.postMessage(n + 100); }, 30);
});
`,
		"main.ts": `
import { Worker } from 'worker_threads';
const w = new Worker('./timer_worker.ts');
w.on('message', (n: number) => { console.log("delayed: " + n); w.terminate(); });
w.on('exit', (c: number) => { console.log("bye " + c); });
w.postMessage(7);
`,
	}, "main.ts", "delayed: 107\nbye 1")
}

func TestE2EWorkerFunctionPayloadRejected(t *testing.T) {
	workerCompileError(t, map[string]string{
		"echo_worker.ts": `
import { parentPort } from 'worker_threads';
parentPort.on('message', (msg: number) => { parentPort.postMessage(msg); });
`,
		"main.ts": `
import { Worker } from 'worker_threads';
const w = new Worker('./echo_worker.ts');
w.postMessage((x: number) => x + 1);
`,
	}, "main.ts", "", "cannot be a function")
}

func TestE2EWorkerGCModeRoundTrip(t *testing.T) {
	// -mm=gc + Worker (TDD-00098 stage 4): per-thread GC_register_my_thread
	// in the trampoline, and every fiber-swap stackbottom repoint goes
	// through the lock-guarded per-thread GC_set_stackbottom path. The
	// worker allocates enough garbage to force collections while messaging.
	assertMultiFileOutputGC(t, map[string]string{
		"gc_worker.ts": `
import { parentPort } from 'worker_threads';
parentPort.on('message', (n: number) => {
    let acc = 0;
    for (let i = 0; i < 2000; i++) {
        const arr: number[] = [i, i + 1, i + 2];
        const s = "x" + i;
        acc += arr[0] + s.length;
    }
    parentPort.postMessage(n + acc);
});
`,
		"main.ts": `
import { Worker } from 'worker_threads';
const w = new Worker('./gc_worker.ts');
w.on('message', (n: number) => { console.log("r: " + n); w.terminate(); });
w.on('exit', (c: number) => { console.log("done " + c); });
w.postMessage(0);
`,
	}, "main.ts", "r: 2007890\ndone 1")
}

// assertMultiFileOutputGC is assertMultiFileOutput under -mm=gc: gcshim +
// LocateGC linking, skipping (not failing) when bdw-gc isn't installed —
// same posture as the single-file GC helpers in compiler_test.go.
func assertMultiFileOutputGC(t *testing.T, files map[string]string, entryName, want string) {
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
	if em.UsesWorkers() {
		clangArgs = append(clangArgs, "-pthread")
	}
	clangArgs = append(clangArgs, cflags...)
	clangArgs = append(clangArgs, libs...)
	for _, lib := range em.LinkLibs() {
		clangArgs = append(clangArgs, "-l"+lib)
	}
	clangArgs = appendJSONParseTree(t, em, dir, clangArgs)
	clangArgs = appendDtoa(t, em, dir, clangArgs)
	out, err := exec.Command("clang", clangArgs...).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "library not found for -lgc") || strings.Contains(string(out), "cannot find -lgc") {
			t.Skipf("bdw-gc not installed: %v", err)
		}
		t.Fatalf("clang: %v\n%s", err, out)
	}
	result, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	compareLines(t, strings.TrimRight(string(result), "\n"), want)
}

func TestE2EWorkerModuleCannotAlsoBeImported(t *testing.T) {
	workerCompileError(t, map[string]string{
		"w.ts": `
import { parentPort } from 'worker_threads';
export function helper(): number { return 1 }
parentPort.on('message', (msg: number) => { parentPort.postMessage(msg); });
`,
		"main.ts": `
import { Worker } from 'worker_threads';
import { helper } from './w';
const w = new Worker('./w.ts');
`,
	}, "main.ts", "", "cannot also be imported")
}

func TestE2EWorkerNestedWorkerRejected(t *testing.T) {
	workerCompileError(t, map[string]string{
		"inner.ts": `
import { parentPort } from 'worker_threads';
parentPort.on('message', (msg: number) => { parentPort.postMessage(msg); });
`,
		"outer.ts": `
import { Worker, parentPort } from 'worker_threads';
const inner = new Worker('./inner.ts');
parentPort.on('message', (msg: number) => { parentPort.postMessage(msg); });
`,
		"main.ts": `
import { Worker } from 'worker_threads';
const w = new Worker('./outer.ts');
`,
	}, "main.ts", "", "cannot spawn workers of its own")
}

func TestE2EWorkerUnannotatedMessageHandlerRejected(t *testing.T) {
	workerCompileError(t, map[string]string{
		"w.ts": `
import { parentPort } from 'worker_threads';
parentPort.on('message', (msg) => { parentPort.postMessage(1); });
`,
		"main.ts": `
import { Worker } from 'worker_threads';
const w = new Worker('./w.ts');
w.postMessage(1);
`,
	}, "main.ts", "", "requires an annotated message parameter")
}

func TestE2EWorkerParentPortOutsideWorkerRejected(t *testing.T) {
	workerCompileError(t, map[string]string{
		"main.ts": `
import { parentPort } from 'worker_threads';
parentPort.postMessage(1);
`,
	}, "main.ts", "", "only available inside a worker module")
}

func TestE2EWorkerUncaughtExceptionFiresErrorEvent(t *testing.T) {
	// TDD-00098 stage 5: an uncaught throw on a worker thread kills only
	// that worker — the parent gets 'error' (the message string) then
	// 'exit' with code 1, and keeps running.
	assertMultiFileOutput(t, map[string]string{
		"bad_worker.ts": `
import { parentPort } from 'worker_threads';
parentPort.on('message', (n: number) => {
    throw new Error("boom " + n);
});
`,
		"main.ts": `
import { Worker } from 'worker_threads';
const w = new Worker('./bad_worker.ts');
w.on('error', (msg: string) => { console.log("worker error: " + msg); });
w.on('exit', (c: number) => { console.log("worker gone: " + c); console.log("parent still alive"); });
w.postMessage(7);
`,
	}, "main.ts", "worker error: boom 7\nworker gone: 1\nparent still alive")
}

func TestE2EWorkerUncaughtExceptionNoListenerKillsProcess(t *testing.T) {
	// Without an 'error' listener the parent prints the uncaught message and
	// exits 1 — Node's own default for an unhandled worker error.
	binFile := buildBinaryMultiFile(t, map[string]string{
		"bad_worker.ts": `
import { parentPort } from 'worker_threads';
parentPort.on('message', (n: number) => {
    throw new Error("kaput");
});
`,
		"main.ts": `
import { Worker } from 'worker_threads';
const w = new Worker('./bad_worker.ts');
w.postMessage(1);
`,
	}, "main.ts")
	out, err := exec.Command(binFile).CombinedOutput()
	if err == nil {
		t.Fatalf("expected exit code 1, got success (output: %s)", out)
	}
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 1 {
		t.Fatalf("expected exit code 1, got %v (output: %s)", err, out)
	}
	if !strings.Contains(string(out), "Uncaught (in worker): kaput") {
		t.Fatalf("expected uncaught-in-worker message, got: %s", out)
	}
}

func TestE2EWorkerBrowserShapeOnMessage(t *testing.T) {
	// TDD-00098 stage 6: the browser surface — ambient `new Worker` (no
	// import), `w.onmessage`/`e.data`, and inside the worker a bare
	// `onmessage = (e: { data: T }) => ...` plus bare `postMessage(...)`.
	assertMultiFileOutput(t, map[string]string{
		"triple_worker.ts": `
onmessage = (e: { data: number }) => {
    postMessage(e.data * 3);
};
`,
		"main.ts": `
const w = new Worker('./triple_worker.ts');
w.onmessage = (e) => {
    console.log("browser got: " + e.data);
    w.terminate();
};
w.postMessage(14);
`,
	}, "main.ts", "browser got: 42")
}

func TestE2EWorkerBrowserShapeOnError(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"bad_worker.ts": `
onmessage = (e: { data: number }) => {
    throw new Error("browser boom");
};
`,
		"main.ts": `
const w = new Worker('./bad_worker.ts');
w.onerror = (e) => { console.log("err: " + e.message); };
w.postMessage(1);
`,
	}, "main.ts", "err: browser boom")
}

func TestE2EWorkerBrowserSelfSurface(t *testing.T) {
	// self.onmessage / self.postMessage, the WorkerGlobalScope spelling.
	assertMultiFileOutput(t, map[string]string{
		"self_worker.ts": `
self.onmessage = (e: { data: string }) => {
    self.postMessage(e.data + "!");
};
`,
		"main.ts": `
const w = new Worker('./self_worker.ts');
w.onmessage = (e) => { console.log(e.data); w.terminate(); };
w.postMessage("hi");
`,
	}, "main.ts", "hi!")
}
