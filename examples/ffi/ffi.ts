// node:ffi — call C functions from a shared library (or the process itself).
// dlopen(null) opens the current process image, so libc is reachable with no
// external library. Signatures are plain objects resolved at compile time and
// lowered to direct C-ABI calls; 64-bit integers and pointers travel as bigint.
import ffi from 'node:ffi';

const { lib, functions } = ffi.dlopen(null, {
  strlen: { arguments: ['string'], return: 'uint64' },
  pow: { arguments: ['float64', 'float64'], return: 'float64' },
  malloc: { arguments: ['uint64'], return: 'pointer' },
  free: { arguments: ['pointer'], return: 'void' },
  qsort: { arguments: ['pointer', 'uint64', 'uint64', 'function'], return: 'void' },
});

console.log('strlen("Thessaloniki") =', functions.strlen('Thessaloniki'));
console.log('pow(2, 16) =', functions.pow(2, 16));

const p = functions.malloc(64n);
console.log('malloc(64) returned a non-null pointer:', p > 0n);
functions.free(p);

// Bind one symbol at a time with getFunction; .pointer is its raw address.
const strchr = lib.getFunction('strchr', { arguments: ['string', 'int32'], return: 'string' });
console.log("strchr('hello', 'l') =", strchr('hello', 108));
console.log('shared libraries here end in', ffi.suffix);

// Raw memory helpers: peek/poke native memory through a bigint pointer.
const mem = functions.malloc(32n);
ffi.setInt32(mem, 0, 1234);
ffi.setFloat64(mem, 8, 2.5);
console.log('read back:', ffi.getInt32(mem, 0), ffi.getFloat64(mem, 8));
ffi.exportString('written from TS', mem, 32n);
console.log('C string in native memory:', ffi.toString(mem));
const bytes = ffi.toBuffer(mem, 7); // a copied Buffer of the first 7 bytes
console.log('first byte:', bytes[0]);
functions.free(mem);

// registerCallback: hand a closure to C as a real function pointer. Here a
// KlainMainLang comparator drives libc's qsort over a native int32 array.
const nums = functions.malloc(20n);
const start = [5, 3, 9, 1, 7];
for (let i = 0; i < 5; i++) ffi.setInt32(nums, i * 4, start[i]);
const cmp = lib.registerCallback(
  { arguments: ['pointer', 'pointer'], return: 'int32' },
  (a: bigint, b: bigint): number => ffi.getInt32(a) - ffi.getInt32(b)
);
functions.qsort(nums, 5n, 4n, cmp);
let sorted = '';
for (let i = 0; i < 5; i++) sorted = sorted + ffi.getInt32(nums, i * 4) + ' ';
console.log('qsort via a native-callable closure:', sorted.trim());
lib.unregisterCallback(cmp);
functions.free(nums);

lib.close();
