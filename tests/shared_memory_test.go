package tests

import (
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/parser"
)

// assertChanCompileError parses+emits a single-file program and asserts a
// compile-stage error containing wantSub.
func assertChanCompileError(t *testing.T, src, wantSub string) {
	t.Helper()
	prog, err := parser.Parse(src)
	if err == nil {
		_, err = llvm.NewEmitter().EmitProgram(prog)
	}
	if err == nil {
		t.Fatalf("expected a compile error containing %q, got success", wantSub)
	}
	if !strings.Contains(err.Error(), wantSub) {
		t.Fatalf("expected error containing %q, got: %v", wantSub, err)
	}
}

// --- SharedArrayBuffer / Atomics / BroadcastChannel / MessageChannel ---
// TDD-00099.

func TestE2ESharedArrayBufferLocalViews(t *testing.T) {
	assertOutput(t, `
const sab = new SharedArrayBuffer(16);
console.log(sab.byteLength);
const a = new Int32Array(sab);
a[0] = 42;
a[3] = 7;
const b = new Int32Array(sab);
console.log(b[0]);
console.log(b[3]);
const bytes = new Uint8Array(sab);
console.log(bytes[0]);
`, "16\n42\n7\n42")
}

func TestE2ESharedArrayBufferAcrossWorker(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"adder.ts": `
import { parentPort, workerData } from 'worker_threads';
const sab: SharedArrayBuffer = workerData;
parentPort.on('message', (delta: number) => {
    const view = new Int32Array(sab);
    view[0] = view[0] + delta;
    parentPort.postMessage(1);
});
`,
		"main.ts": `
import { Worker } from 'worker_threads';
const sab = new SharedArrayBuffer(8);
const init = new Int32Array(sab);
init[0] = 100;
const w = new Worker('./adder.ts', { workerData: sab });
w.on('message', (ok: number) => {
    const view = new Int32Array(sab);
    console.log("after worker: " + view[0]);
    w.terminate();
});
w.postMessage(23);
`,
	}, "main.ts", "after worker: 123")
}

func TestE2EAtomicsSingleThreadOps(t *testing.T) {
	assertOutput(t, `
const sab = new SharedArrayBuffer(16);
const ta = new Int32Array(sab);
Atomics.store(ta, 0, 10);
console.log(Atomics.load(ta, 0));
console.log(Atomics.add(ta, 0, 5));
console.log(Atomics.sub(ta, 0, 3));
console.log(Atomics.exchange(ta, 0, 99));
console.log(Atomics.compareExchange(ta, 0, 99, 7));
console.log(Atomics.compareExchange(ta, 0, 99, 1));
console.log(Atomics.load(ta, 0));
console.log(Atomics.or(ta, 1, 6));
console.log(Atomics.and(ta, 1, 4));
console.log(Atomics.xor(ta, 1, 5));
console.log(Atomics.load(ta, 1));
console.log(Atomics.notify(ta, 0));
console.log(Atomics.wait(ta, 0, 42));
console.log(Atomics.wait(ta, 0, 7, 50));
`, "10\n10\n15\n12\n99\n7\n7\n0\n6\n4\n1\n0\nnot-equal\ntimed-out")
}

func TestE2EAtomicsWaitNotifyAcrossWorker(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"waiter.ts": `
import { parentPort, workerData } from 'worker_threads';
const sab: SharedArrayBuffer = workerData;
parentPort.on('message', (go: number) => {
    const ta = new Int32Array(sab);
    const r: string = Atomics.wait(ta, 0, 0);
    parentPort.postMessage(r + ":" + Atomics.load(ta, 0));
});
`,
		"main.ts": `
import { Worker } from 'worker_threads';
const sab = new SharedArrayBuffer(8);
const w = new Worker('./waiter.ts', { workerData: sab });
w.on('message', (r: string) => {
    console.log("worker result: " + r);
    w.terminate();
});
w.postMessage(1);
setTimeout(() => {
    const ta = new Int32Array(sab);
    Atomics.store(ta, 0, 77);
    console.log("notified " + Atomics.notify(ta, 0));
}, 100);
`,
	}, "main.ts", "notified 1\nworker result: ok:77")
}

func TestE2EBroadcastChannelSameThread(t *testing.T) {
	assertOutput(t, `
const a = new BroadcastChannel('room');
const b = new BroadcastChannel('room');
const other = new BroadcastChannel('elsewhere');
b.onmessage = (e: { data: number }) => {
    console.log("b got " + e.data);
    b.close();
    other.close();
};
other.onmessage = (e: { data: number }) => { console.log("must not fire"); };
a.postMessage(41);
`, "b got 41")
}

func TestE2EBroadcastChannelAcrossWorker(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"listener.ts": `
import { parentPort } from 'worker_threads';
const bc = new BroadcastChannel('news');
bc.onmessage = (e: { data: string }) => {
    parentPort.postMessage("worker got: " + e.data);
    bc.close();
};
parentPort.on('message', (go: number) => {});
`,
		"main.ts": `
import { Worker } from 'worker_threads';
const w = new Worker('./listener.ts');
w.on('message', (r: string) => {
    console.log(r);
    w.terminate();
});
const bc = new BroadcastChannel('news');
setTimeout(() => { bc.postMessage("flash"); }, 100);
`,
	}, "main.ts", "worker got: flash")
}

func TestE2EMessageChannelSameThread(t *testing.T) {
	assertOutput(t, `
const ch = new MessageChannel<string>();
ch.port1.onmessage = (e: { data: string }) => {
    console.log("port1 got: " + e.data);
    ch.port1.close();
    ch.port2.close();
};
ch.port2.postMessage("ping");
`, "port1 got: ping")
}

func TestE2EMessagePortAcrossWorker(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"echoer.ts": `
import { parentPort, workerData } from 'worker_threads';
const port: MessagePort<string> = workerData;
port.onmessage = (e: { data: string }) => {
    port.postMessage("echo: " + e.data);
};
parentPort.on('message', (go: number) => {});
`,
		"main.ts": `
import { Worker } from 'worker_threads';
const ch = new MessageChannel<string>();
const w = new Worker('./echoer.ts', { workerData: ch.port2 });
ch.port1.onmessage = (e: { data: string }) => {
    console.log("main got: " + e.data);
    ch.port1.close();
    ch.port2.close();
    w.terminate();
};
setTimeout(() => { ch.port1.postMessage("over the wall"); }, 100);
`,
	}, "main.ts", "main got: echo: over the wall")
}

func TestE2EAtomicsRejectsFloatTypedArray(t *testing.T) {
	assertChanCompileError(t, `
const f = new Float64Array(4);
Atomics.add(f, 0, 1);
`, "integer TypedArray")
}

func TestE2EAtomicsWaitRequiresInt32Array(t *testing.T) {
	assertChanCompileError(t, `
const ta = new Uint8Array(4);
Atomics.wait(ta, 0, 0);
`, "Int32Array")
}

func TestE2EBroadcastChannelTypeMismatch(t *testing.T) {
	assertChanCompileError(t, `
const a = new BroadcastChannel('room');
const b = new BroadcastChannel('room');
a.postMessage(1);
b.postMessage("nope");
`, "one message type per channel name")
}

// The free-variable-scanner fix found while wiring SharedArrayBuffer views
// into worker handlers: a `new Int32Array(buf)` inside a closure body must
// capture buf.
func TestE2ETypedArrayConstructionCapturesBufferInClosure(t *testing.T) {
	assertOutput(t, `
const buf = new ArrayBuffer(8);
const init = new Int32Array(buf);
init[0] = 5;
const read = () => {
    const view = new Int32Array(buf);
    return view[0];
};
console.log(read());
`, "5")
}
