// WebAssembly: the JS API Node has (WebAssembly.Module, Instance, Memory,
// compile/instantiate), on a native engine. The module below is the binary
// of this text, compiled by any WebAssembly toolchain:
//
//   (import "env" "report" (func $report (param i32 i32)))
//   (memory (export "memory") 1)
//   (data (i32.const 0) "Kalimera from WebAssembly")
//   (func (export "greet") i32.const 0 i32.const 25 call $report)
//   (func (export "fib") (param i32) (result i64) ...)        ;; iterative
//   (func (export "sum") (param i32 i32) (result f64) ...)    ;; f64s in memory

const bytes = new Uint8Array([
  0, 97, 115, 109, 1, 0, 0, 0, 1, 20, 4, 96, 2, 127, 127, 0, 96, 0, 0, 96, 1,
  127, 1, 126, 96, 2, 127, 127, 1, 124, 2, 14, 1, 3, 101, 110, 118, 6, 114,
  101, 112, 111, 114, 116, 0, 0, 3, 4, 3, 1, 2, 3, 5, 3, 1, 0, 1, 7, 30, 4, 6,
  109, 101, 109, 111, 114, 121, 2, 0, 5, 103, 114, 101, 101, 116, 0, 1, 3,
  102, 105, 98, 0, 2, 3, 115, 117, 109, 0, 3, 10, 104, 3, 8, 0, 65, 0, 65, 25,
  16, 0, 11, 49, 1, 3, 126, 66, 0, 33, 1, 66, 1, 33, 2, 2, 64, 3, 64, 32, 0,
  69, 13, 1, 32, 1, 32, 2, 124, 33, 3, 32, 2, 33, 1, 32, 3, 33, 2, 32, 0, 65,
  1, 107, 33, 0, 12, 0, 11, 11, 32, 1, 11, 43, 1, 1, 124, 2, 64, 3, 64, 32, 1,
  69, 13, 1, 32, 2, 32, 0, 43, 3, 0, 160, 33, 2, 32, 0, 65, 8, 106, 33, 0, 32,
  1, 65, 1, 107, 33, 1, 12, 0, 11, 11, 32, 2, 11, 11, 31, 1, 0, 65, 0, 11, 25,
  75, 97, 108, 105, 109, 101, 114, 97, 32, 102, 114, 111, 109, 32, 87, 101,
  98, 65, 115, 115, 101, 109, 98, 108, 121, 0, 86, 4, 110, 97, 109, 101, 1,
  14, 2, 0, 6, 114, 101, 112, 111, 114, 116, 2, 3, 102, 105, 98, 2, 32, 2, 2,
  4, 0, 1, 110, 1, 1, 97, 2, 1, 98, 3, 1, 116, 3, 3, 0, 3, 112, 116, 114, 1,
  3, 108, 101, 110, 2, 3, 97, 99, 99, 3, 29, 2, 2, 2, 0, 4, 100, 111, 110,
  101, 1, 4, 110, 101, 120, 116, 3, 2, 0, 4, 100, 111, 110, 101, 1, 4, 110,
  101, 120, 116,
]);

console.log('valid:', WebAssembly.validate(bytes));
const module = new WebAssembly.Module(bytes);
console.log('imports:', WebAssembly.Module.imports(module));
console.log('exports:', WebAssembly.Module.exports(module).map((e) => `${e.name} (${e.kind})`).join(', '));

// A JS function the module calls back, reading a string out of its memory.
let memory: WebAssembly.Memory | undefined;
const instance = new WebAssembly.Instance(module, {
  env: {
    report: (ptr: number, len: number) => {
      const text = new TextDecoder().decode(new Uint8Array(memory!.buffer, ptr, len));
      console.log('wasm says:', text);
    },
  },
});
memory = instance.exports.memory as WebAssembly.Memory;
const exports = instance.exports as {
  greet: () => void;
  fib: (n: number) => bigint;
  sum: (ptr: number, len: number) => number;
};
exports.greet();

// i64 results arrive as BigInt.
console.log('fib(90) =', exports.fib(90));

// JS writes f64s into the module's memory; the module sums them.
const values = new Float64Array(memory.buffer, 1024, 4);
values.set([1.5, 2.25, 3, 4.25]);
console.log('sum =', exports.sum(1024, values.length));

// Bad bytes are a CompileError; the async API settles later.
try {
  new WebAssembly.Module(new Uint8Array([0, 1, 2, 3]));
} catch (e) {
  console.log('error:', (e as Error).name);
}
WebAssembly.instantiate(bytes, { env: { report: () => console.log('async instance reports') } }).then(({ instance }) => {
  (instance.exports.greet as () => void)();
});
