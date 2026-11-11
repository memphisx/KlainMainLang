package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
)

// requireWasmtime skips a test when the wasmtime C API is not installed
// (README Requirements): a WebAssembly program links it.
func requireWasmtime(t *testing.T) {
	t.Helper()
	cflags, _ := llvm.LocateWasm()
	for _, f := range cflags {
		if dir, ok := strings.CutPrefix(f, "-I"); ok {
			if _, err := os.Stat(filepath.Join(dir, "wasmtime.h")); err == nil {
				return
			}
		}
	}
	t.Skip("wasmtime C API not installed")
}

// simpleWasm is a module exporting add(i32, i32) -> i32.
const simpleWasm = `new Uint8Array([0,97,115,109,1,0,0,0,1,7,1,96,2,127,127,1,127,3,2,1,0,7,7,1,3,97,100,100,0,0,10,9,1,7,0,32,0,32,1,106,11])`

// richWasm imports env.log (i32, f64) -> i32, env.mem, a mutable i32
// global env.g and an i64 global env.k, and exports a funcref table, a
// mutable global and functions over all of them (see its uses below).
const richWasm = `new Uint8Array([0,97,115,109,1,0,0,0,1,52,10,96,2,127,124,1,127,96,2,127,127,1,127,96,1,127,1,127,96,2,127,127,0,96,0,1,127,96,1,126,1,126,96,0,0,96,0,2,127,126,96,1,111,1,111,96,3,127,127,127,1,127,2,41,4,3,101,110,118,3,108,111,103,0,0,3,101,110,118,3,109,101,109,2,1,1,4,3,101,110,118,1,103,3,127,1,3,101,110,118,1,107,3,126,0,3,13,12,1,1,2,3,4,4,5,1,6,7,8,9,4,4,1,112,0,2,6,6,1,127,1,65,7,11,7,100,13,3,116,98,108,1,0,7,99,111,117,110,116,101,114,3,2,3,97,100,100,0,1,7,99,97,108,108,76,111,103,0,3,5,115,116,111,114,101,0,4,4,103,114,111,119,0,5,5,114,101,97,100,71,0,6,3,98,105,103,0,7,3,100,105,118,0,8,4,116,114,97,112,0,9,4,112,97,105,114,0,10,5,105,100,101,110,116,0,11,7,99,97,108,108,73,100,120,0,12,9,8,1,0,65,0,11,2,1,2,10,99,12,7,0,32,0,32,1,106,11,7,0,32,0,32,1,108,11,15,0,32,0,68,0,0,0,0,0,0,4,64,16,0,11,9,0,32,0,32,1,58,0,0,11,6,0,65,1,64,0,11,4,0,35,0,11,7,0,32,0,35,1,124,11,7,0,32,0,32,1,109,11,3,0,0,11,6,0,65,1,66,2,11,4,0,32,0,11,11,0,32,1,32,2,32,0,17,1,0,11,0,32,4,110,97,109,101,1,16,3,0,3,108,111,103,1,3,97,100,100,2,3,109,117,108,7,7,2,0,1,103,1,1,107])`

// The JS API over a module: compile, validate, its import and export
// descriptions, an instance's exported function, and the errors.
func TestE2EWasmModuleBasics(t *testing.T) {
	t.Parallel()
	requireWasmtime(t)
	assertSameAsNodeCLIJS(t, `
const bytes = `+simpleWasm+`;
console.log(WebAssembly.validate(bytes), WebAssembly.validate(new Uint8Array([1, 2, 3])));
const m = new WebAssembly.Module(bytes);
console.log(WebAssembly.Module.exports(m), WebAssembly.Module.imports(m), WebAssembly.Module.customSections(m, 'x'));
const inst = new WebAssembly.Instance(m);
console.log(Object.keys(inst.exports), inst.exports.add(10, 20), inst.exports.add.name, inst.exports.add.length);
console.log(inst.exports.add === inst.exports.add, Object.isFrozen(inst.exports), Object.getPrototypeOf(inst.exports));
try { new WebAssembly.Module(new Uint8Array([1, 2, 3, 4])); } catch (e) { console.log(e.name, e instanceof WebAssembly.CompileError); }
try { new WebAssembly.Module(5); } catch (e) { console.log(e.name, e.message); }
try { new WebAssembly.Instance({}); } catch (e) { console.log(e.name, e.message); }
console.log(String(m), String(inst), String(WebAssembly));
`)
}

// compile and instantiate settle as V8's: a compile in a later task, an
// instantiation of a compiled module at once. (Which of two
// compiles in flight settles first is V8's background timing, so each
// waits for the one before.)
func TestE2EWasmAsyncOrder(t *testing.T) {
	t.Parallel()
	requireWasmtime(t)
	assertSameAsNodeCLIJS(t, `
const bytes = `+simpleWasm+`;
WebAssembly.compile(bytes).then(async (m) => {
  console.log('compiled', m instanceof WebAssembly.Module);
  const r = await WebAssembly.instantiate(bytes);
  console.log('both', r.instance.exports.add(1, 2), r.module instanceof WebAssembly.Module);
  await WebAssembly.compile(new Uint8Array([0, 97, 115, 109, 9])).catch(e => console.log('rejected', e.name));
  await WebAssembly.instantiate(new Uint8Array([1])).catch(e => console.log('rejected', e.name));
});
const m = new WebAssembly.Module(bytes);
WebAssembly.instantiate(m).then(i => console.log('inst', i.exports.add(2, 2), i instanceof WebAssembly.Instance));
Promise.resolve().then(() => console.log('tick'));
`)
}

// Imports (a JS function, a memory, globals), tables, i64 as BigInt,
// externref identity, traps as RuntimeError, multi-value results and a JS
// exception passing back out through wasm.
func TestE2EWasmImportsMemoryTables(t *testing.T) {
	t.Parallel()
	requireWasmtime(t)
	assertSameAsNodeCLIJS(t, `
const bytes = `+richWasm+`;
const mem = new WebAssembly.Memory({ initial: 1, maximum: 4 });
const g = new WebAssembly.Global({ value: 'i32', mutable: true }, 42);
const logged = [];
const env = { log: (a, b) => { logged.push([a, b]); return a * 2; }, mem, g, k: 100n };
const instance = new WebAssembly.Instance(new WebAssembly.Module(bytes), { env });
const e = instance.exports;
console.log(Object.keys(e).join(','));
console.log(e.add(2, 3), e.callLog(21), logged, e.readG());
g.value = 9; console.log(e.readG(), g.value, e.counter.value, e.counter instanceof WebAssembly.Global);
try { e.counter.value = 'x'; console.log(e.counter.value); } catch (x) { console.log(x.name); }
e.store(3, 255); console.log(new Uint8Array(mem.buffer)[3], mem.buffer.byteLength);
console.log(e.grow(), mem.buffer.byteLength, mem.grow(1), mem.buffer.byteLength);
try { mem.grow(10); } catch (x) { console.log(x.name, x.message); }
console.log(e.big(5n), typeof e.big(5n), e.big(-1n));
try { e.big(5); } catch (x) { console.log(x.name, x.message); }
try { e.div(1, 0); } catch (x) { console.log(x.name, x.message, x instanceof WebAssembly.RuntimeError); }
try { e.trap(); } catch (x) { console.log(x.name, x.message); }
console.log(e.pair());
const o = { a: 1 }; console.log(e.ident(o) === o, e.ident(null), e.ident(undefined));
console.log(e.tbl.length, e.tbl.get(0) === e.add, e.tbl.get(1)(6, 7), e.tbl.get(1).name, e.callIdx(1, 3, 4));
try { e.tbl.get(5); } catch (x) { console.log(x.name, x.message); }
try { new WebAssembly.Instance(new WebAssembly.Module(bytes), { env: { ...env, log: 1 } }); } catch (x) { console.log(x.name, x.message); }
try { new WebAssembly.Instance(new WebAssembly.Module(bytes), {}); } catch (x) { console.log(x.name, x.message); }
const thrower = new WebAssembly.Instance(new WebAssembly.Module(bytes), { env: { ...env, log: () => { throw new Error('boom'); } } });
try { thrower.exports.callLog(1); } catch (x) { console.log('rethrown', x.message); }
console.log(String(e.tbl), String(mem), String(g));
`)
}

// A module crosses to a worker in a message and instantiates there: the
// same compiled module (Node's test-worker-message-port-wasm-module).
func TestE2EWasmModuleToWorker(t *testing.T) {
	t.Parallel()
	requireWasmtime(t)
	assertSameAsNodeCLIJS(t, `
const { Worker } = require('worker_threads');
const wasmModule = new WebAssembly.Module(`+simpleWasm+`);
const worker = new Worker(`+"`"+`
const { parentPort } = require('worker_threads');
parentPort.once('message', ({ wasmModule }) => {
  const instance = new WebAssembly.Instance(wasmModule);
  parentPort.postMessage([instance.exports.add(10, 20), __filename, __dirname]);
});
`+"`"+`, { eval: true });
worker.once('message', (num) => console.log(num));
worker.postMessage({ wasmModule });
`)
}

// A shared memory crosses to a worker as the same bytes; an unshared one
// cannot be cloned.
func TestE2EWasmSharedMemoryToWorker(t *testing.T) {
	t.Parallel()
	requireWasmtime(t)
	assertSameAsNodeCLIJS(t, `const { Worker } = require('worker_threads');
const mem = new WebAssembly.Memory({ initial: 1, maximum: 4, shared: true });
const view = new Int32Array(mem.buffer);
const w = new Worker("const { parentPort } = require('worker_threads');\n" +
  "parentPort.once('message', (m) => {\n" +
  "  Atomics.add(new Int32Array(m.buffer), 0, 41);\n" +
  "  parentPort.postMessage([m instanceof WebAssembly.Memory, m.buffer instanceof SharedArrayBuffer]);\n" +
  "});\n", { eval: true });
w.on('message', (r) => {
  console.log(r, view[0], Atomics.load(view, 0));
  try { w.postMessage(new WebAssembly.Memory({ initial: 1 })); } catch (e) { console.log(e.name, e.message); }
  w.terminate();
});
view[0] = 1;
w.postMessage(mem);
`)
}

// compileStreaming checks its Response as Node does.
func TestE2EWasmStreamingResponseChecks(t *testing.T) {
	t.Parallel()
	requireWasmtime(t)
	assertSameAsNodeCLIJS(t, `const b = new Uint8Array([0,97,115,109,1,0,0,0]);
(async () => {
for (const h of [{}, {'content-type':'text/plain'}, {'content-type':'application/wasm; charset=x'}, {'content-type':' Application/WASM '}]) {
  try { await WebAssembly.compileStreaming(new Response(b, {headers:h})); console.log('ok'); } catch (e) { console.log(e.name, e.message); }
}
try { await WebAssembly.compileStreaming(new Response(b, {status:404, headers:{'content-type':'application/wasm'}})); } catch (e) { console.log(e.name, e.message); }
try { await WebAssembly.compileStreaming(42); } catch (e) { console.log(e.name, e.message); }
})();
`)
}
