package tests

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/resolver"
)

// node:ffi Stage A (TDD-00164): dlopen/DynamicLibrary/getFunction/getSymbol/
// dlclose/suffix/types + typed C-ABI calls through statically-resolved
// signature objects. Runs on every host: Windows resolves through the
// LoadLibrary/GetProcAddress shim, where ffi.suffix is `dll`.

// hostSuffix mirrors ffi.suffix for assertions.
func hostSuffix() string {
	switch runtime.GOOS {
	case "darwin":
		return "dylib"
	case "windows":
		return "dll"
	}
	return "so"
}

func TestE2EFFILibcSelfProcess(t *testing.T) {
	// A `string` return is pointer-like in node:ffi: the char* arrives as a raw
	// pointer bigint (read it with ffi.toString), not an auto-marshalled JS
	// string — so assert its shape, whose value is non-deterministic.
	assertSameAsNodeFFI(t, `
import ffi from 'node:ffi';
const { lib, functions } = ffi.dlopen(null, {
  strlen: { arguments: ['string'], return: 'uint64' },
  pow: { arguments: ['float64', 'float64'], return: 'float64' },
  getpid: { return: 'int32' },
});
console.log(functions.strlen('hello'));
console.log(functions.pow(2, 10));
console.log(functions.getpid() > 0);
const strchr = lib.getFunction('strchr', { arguments: ['string', 'int32'], return: 'string' });
console.log(typeof strchr('hello', 108), strchr('hello', 108) > 0n);
console.log(lib.getSymbol('strlen') > 0n);
lib.close();
console.log('closed');
`, "5n\n1024\ntrue\nbigint true\ntrue\nclosed")
}

func TestE2EFFIPointerBigintRoundTrip(t *testing.T) {
	assertSameAsNodeFFI(t, `
import { dlopen, dlsym } from 'node:ffi';
const { lib, functions } = dlopen(null, {
  malloc: { arguments: ['uint64'], return: 'pointer' },
  free: { arguments: ['pointer'], return: 'void' },
  memset: { arguments: ['pointer', 'int32', 'uint64'], return: 'pointer' },
});
const p = functions.malloc(16n);
console.log(p > 0n);
console.log(functions.memset(p, 65, 8n) === p);
functions.free(p);
console.log(dlsym(lib, 'malloc') > 0n);
`, "true\ntrue\ntrue")
}

func TestE2EFFIMissingSymbolThrows(t *testing.T) {
	assertSameAsNodeFFI(t, `
import ffi from 'node:ffi';
const { lib } = ffi.dlopen(null);
try {
  lib.getSymbol('kml_definitely_not_a_symbol');
} catch (e) {
  console.log('caught');
}
lib.close();
`, "caught")
}

func TestE2EFFISuffixConstant(t *testing.T) {
	// ffi.types values are Node's own spellings: DOUBLE is "double" (not
	// "float64"), FLOAT is "float", BOOL is "bool".
	assertSameAsNodeFFI(t, `
import ffi from 'node:ffi';
console.log(ffi.suffix);
console.log(ffi.types.INT_32, ffi.types.DOUBLE, ffi.types.POINTER);
console.log(ffi.types.FLOAT, ffi.types.BOOL, ffi.types.FLOAT_32, ffi.types.FLOAT_64);
`, hostSuffix()+"\nint32 double pointer\nfloat bool float32 float64")
}

// buildFFITestLib compiles a small C shared library into t.TempDir and
// returns its absolute path.
func buildFFITestLib(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cFile := filepath.Join(dir, "testffi.c")
	libFile := filepath.Join(dir, "libtestffi."+hostSuffix())
	writeFile(t, cFile, `
int add_i32(int a, int b) { return a + b; }
long long mul_i64(long long a, long long b) { return a * b; }
float halve_f32(float x) { return x / 2.0f; }
short neg16(short x) { return (short)-x; }
void fill(unsigned char *buf, int n) { for (int i = 0; i < n; i++) buf[i] = (unsigned char)(i * 2); }
const char *greet(void) { return "hi from C"; }
unsigned int usub(unsigned int a, unsigned int b) { return a - b; }
int invoke2(int (*f)(int, int), int a, int b) { return f(a, b); }
double invoke_d(double (*f)(double), double x) { return f(x); }
void invoke_str(void (*f)(const char *), const char *s) { f(s); }
`)
	if out, err := exec.Command("clang", "-O2", "-shared", "-o", libFile, cFile).CombinedOutput(); err != nil {
		t.Fatalf("clang -shared: %v\n%s", err, out)
	}
	return libFile
}

func TestE2EFFICustomSharedLibrary(t *testing.T) {
	libFile := buildFFITestLib(t)
	// greet() returns `const char*` typed `str` — pointer-like, so a raw pointer
	// bigint, not the marshalled "hi from C".
	assertSameAsNodeFFI(t, fmt.Sprintf(`
import ffi, { DynamicLibrary } from 'node:ffi';
const lib = new DynamicLibrary(%q);
const fns = lib.getFunctions({
  add_i32: { arguments: [ffi.types.INT_32, 'i32'], return: ffi.types.INT_32 },
  mul_i64: { arguments: ['int64', 'int64'], return: 'int64' },
  halve_f32: { arguments: ['f32'], return: 'float' },
  neg16: { arguments: ['int16'], return: 'int16' },
  fill: { arguments: ['buffer', 'int32'], return: 'void' },
  greet: { return: 'str' },
  usub: { arguments: ['u32', 'u32'], return: 'u32' },
});
console.log(fns.add_i32(40, 2));
console.log(fns.mul_i64(3000000000n, 3n));
console.log(fns.halve_f32(21));
console.log(fns.neg16(-5));
console.log(typeof fns.greet(), fns.greet() > 0n);
console.log(fns.usub(2, 3));
const buf = new Uint8Array(5);
fns.fill(buf, 5);
console.log(buf[0], buf[3], buf[4]);
const add2 = lib.getFunction('add_i32', { arguments: ['int32', 'int32'], return: 'int32' });
console.log(add2(1, 2), add2.pointer > 0n);
ffi.dlclose(lib);
lib.close();
console.log('done');
`, libFile), "42\n9000000000n\n10.5\n5\nbigint true\n4294967295\n0 6 8\n3 true\ndone")
}

func TestE2EFFIPrimitiveAccessors(t *testing.T) {
	assertSameAsNodeFFI(t, `
import ffi from 'node:ffi';
const { functions } = ffi.dlopen(null, {
  malloc: { arguments: ['uint64'], return: 'pointer' },
  free: { arguments: ['pointer'], return: 'void' },
});
const p = functions.malloc(64n);
ffi.setInt8(p, 0, -7);
ffi.setUint8(p, 1, 250);
ffi.setInt16(p, 2, -1234);
ffi.setInt32(p, 4, 123456789);
ffi.setInt64(p, 8, 9007199254740993n);
ffi.setUint64(p, 16, 18446744073709551615n);
ffi.setFloat32(p, 24, 2.5);
ffi.setFloat64(p, 32, 3.14159);
console.log(ffi.getInt8(p, 0), ffi.getUint8(p, 1), ffi.getInt16(p, 2));
console.log(ffi.getInt32(p, 4));
console.log(ffi.getInt64(p, 8));
console.log(ffi.getUint64(p, 16));
console.log(ffi.getFloat32(p, 24), ffi.getFloat64(p, 32));
console.log(ffi.getUint8(p));
functions.free(p);
`, "-7 250 -1234\n123456789\n9007199254740993n\n18446744073709551615n\n2.5 3.14159\n249")
}

func TestE2EFFIStringBufferHelpers(t *testing.T) {
	// exportString's length is a number in Node (validateInteger throws on a
	// bigint) and must be large enough for the whole string plus its NUL — Node
	// throws ERR_OUT_OF_RANGE rather than truncating. The pointer helpers
	// (toString/toBuffer/…) do marshal, that is their purpose and Node-faithful.
	assertSameAsNodeFFI(t, `
import ffi from 'node:ffi';
const { functions } = ffi.dlopen(null, {
  malloc: { arguments: ['uint64'], return: 'pointer' },
  free: { arguments: ['pointer'], return: 'void' },
});
const p = functions.malloc(64n);
ffi.exportString('hello ffi', p, 64);
console.log(ffi.toString(p));
console.log(ffi.toString(0n) === null);
try {
  ffi.exportString('truncated!', p, 6);
  console.log('no throw');
} catch (e) {
  console.log('caught small string');
}
ffi.exportString('ABCDE', p, 64);
const copied = ffi.toBuffer(p, 5);
const view = ffi.toBuffer(p, 5, false);
ffi.setUint8(p, 0, 90);
console.log(copied[0], view[0]);
const ab = ffi.toArrayBuffer(p, 5);
console.log(ab.byteLength);
const u8 = new Uint8Array(5);
console.log(ffi.getRawPointer(u8) > 0n);
const srcBuf = new Uint8Array(4);
srcBuf[0] = 42; srcBuf[3] = 24;
ffi.exportArrayBufferView(srcBuf, p, 64);
console.log(ffi.getUint8(p, 0), ffi.getUint8(p, 3));
try {
  ffi.exportArrayBufferView(srcBuf, p, 2);
} catch (e) {
  console.log('caught small');
}
functions.free(p);
`, "hello ffi\ntrue\ncaught small string\n65 90\n5\ntrue\n42 24\ncaught small")
}

func TestE2EFFIRegisterCallbackQsort(t *testing.T) {
	assertSameAsNodeFFI(t, `
import ffi from 'node:ffi';
const { lib, functions } = ffi.dlopen(null, {
  malloc: { arguments: ['uint64'], return: 'pointer' },
  free: { arguments: ['pointer'], return: 'void' },
  qsort: { arguments: ['pointer', 'uint64', 'uint64', 'function'], return: 'void' },
});
const p = functions.malloc(20n);
const vals = [5, 3, 9, 1, 7];
for (let i = 0; i < 5; i++) ffi.setInt32(p, i * 4, vals[i]);
let compares = 0;
const cmp = lib.registerCallback(
  { arguments: ['pointer', 'pointer'], return: 'int32' },
  (a: bigint, b: bigint): number => {
    compares = compares + 1;
    return ffi.getInt32(a) - ffi.getInt32(b);
  }
);
functions.qsort(p, 5n, 4n, cmp);
let out = '';
for (let i = 0; i < 5; i++) out = out + ffi.getInt32(p, i * 4) + ' ';
console.log(out.trim(), compares > 0);
lib.unregisterCallback(cmp);
const cmp2 = lib.registerCallback({ arguments: ['pointer', 'pointer'], return: 'int32' },
  (a: bigint, b: bigint): number => ffi.getInt32(b) - ffi.getInt32(a));
functions.qsort(p, 5n, 4n, cmp2);
let out2 = '';
for (let i = 0; i < 5; i++) out2 = out2 + ffi.getInt32(p, i * 4) + ' ';
console.log(out2.trim(), cmp2 === cmp);
lib.refCallback(cmp2);
lib.unrefCallback(cmp2);
lib.unregisterCallback(cmp2);
functions.free(p);
`, "1 3 5 7 9 true\n9 7 5 3 1 true")
}

func TestE2EFFICallbackScalarAndString(t *testing.T) {
	libFile := buildFFITestLib(t)
	// A `string` callback parameter is pointer-like: the closure receives the
	// C char* as a raw pointer bigint (declared `bigint`), matching Node.
	assertSameAsNodeFFI(t, fmt.Sprintf(`
import ffi, { DynamicLibrary } from 'node:ffi';
const lib = new DynamicLibrary(%q);
const fns = lib.getFunctions({
  invoke2: { arguments: ['function', 'int32', 'int32'], return: 'int32' },
  invoke_d: { arguments: ['function', 'float64'], return: 'float64' },
  invoke_str: { arguments: ['function', 'string'], return: 'void' },
});
const mul = lib.registerCallback({ arguments: ['int32', 'int32'], return: 'int32' },
  (a: number, b: number): number => a * b);
console.log(fns.invoke2(mul, 6, 7));
const half = lib.registerCallback({ arguments: ['float64'], return: 'float64' },
  (x: number): number => x / 2);
console.log(fns.invoke_d(half, 21));
const shout = lib.registerCallback({ arguments: ['string'] },
  (s: bigint) => { console.log('from C:', typeof s, s > 0n); });
fns.invoke_str(shout, 'hello callback');
`, libFile), "42\n10.5\nfrom C: bigint true")
}

// The 16-concurrent-callbacks-per-signature ceiling is this compiler's own:
// callbacks are static trampoline families (no runtime codegen — ADR-00799/
// ADR-00800), where Node's libffi closures have no such limit. It cannot be
// diffed against node:ffi (Node does not throw), so it stays an ours-only check
// of a documented, deliberate divergence-by-necessity (FFI.md).
func TestE2EFFICallbackSlotLimit(t *testing.T) {
	assertOutputImports(t, `
import ffi from 'node:ffi';
const { lib } = ffi.dlopen(null);
let taken = 0;
try {
  for (let i = 0; i < 20; i++) {
    lib.registerCallback({ arguments: ['float64'], return: 'float64' }, (x: number): number => x);
    taken = taken + 1;
  }
} catch (e) {
  console.log('slots exhausted after', taken);
}
lib.close();
`, "slots exhausted after 16")
}

func TestE2EFFISymbolAccumulators(t *testing.T) {
	// Node enumerates `.symbols`/`.functions` keys most-recently-registered
	// first (reverse resolution order).
	assertSameAsNodeFFI(t, `
import ffi, { DynamicLibrary } from 'node:ffi';
const { lib } = ffi.dlopen(null, {
  strlen: { arguments: ['string'], return: 'uint64' },
});
lib.getSymbol('getpid');
const abs = lib.getFunction('abs', { arguments: ['int32'], return: 'int32' });
console.log(lib.functions.strlen('hello!'));
console.log(lib.functions.abs(-4));
const syms = lib.symbols;
console.log(syms.getpid > 0n, syms.strlen > 0n, syms.abs > 0n);
console.log(lib.getSymbols().getpid === syms.getpid);
const fns = lib.getFunctions();
console.log(fns.abs(-9));
console.log(Object.keys(syms).join(','));
// A separate library variable accumulates independently.
const lib2 = new DynamicLibrary(null);
lib2.getSymbol('strlen');
console.log(Object.keys(lib2.getSymbols()).length);
lib.close();
lib2.close();
`, "6n\n4\ntrue true true\ntrue\n9\nabs,getpid,strlen\n1")
}

// assertFFICodegenErrorImports resolves import-using source and asserts
// codegen rejects it with a message containing wantSubstr.
func assertFFICodegenErrorImports(t *testing.T, src, wantSubstr string) {
	t.Helper()
	d := tempDir(t)
	srcFile := filepath.Join(d, "main.ts")
	writeFile(t, srcFile, src)
	prog, err := resolver.ResolveProgram(srcFile)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := llvm.NewEmitter().EmitProgram(prog); err == nil {
		t.Fatalf("expected codegen error containing %q, got success", wantSubstr)
	} else if !strings.Contains(err.Error(), wantSubstr) {
		t.Fatalf("expected error containing %q, got: %v", wantSubstr, err)
	}
}

func TestE2EFFIStaticSignatureRejections(t *testing.T) {
	assertFFICodegenErrorImports(t, `
import ffi from 'node:ffi';
const sig = { arguments: ['int32'], return: 'int32' };
const { lib } = ffi.dlopen(null);
lib.getFunction('abs', sig);
`, "signature must be an object literal")
	assertFFICodegenErrorImports(t, `
import ffi from 'node:ffi';
const { lib } = ffi.dlopen(null);
lib.registerCallback({ arguments: ['pointer'], return: 'int32' }, (v: number): number => v * 2);
`, "must be a bigint for FFI type 'pointer'")
}

func TestE2EFFIFunctionObjects(t *testing.T) {
	// A bound native function is a real function object: typeof, name,
	// length, the own `pointer` property, per-name identity, and util.inspect's
	// `[Function: name] { pointer: … }` form (TDD-00229).
	assertSameAsNodeFFI(t, `
import ffi from 'node:ffi';
const lib = new ffi.DynamicLibrary(null);
const abs = lib.getFunction('abs', { arguments: ['int32'], return: 'int32' });
console.log(typeof abs, abs.name, abs.length, typeof abs.pointer, abs.pointer > 0n);
console.log(Object.keys(abs as any).join(','));
console.log(abs === lib.getFunction('abs', { arguments: ['i32'], return: 'i32' }));
console.log(abs === (lib.functions as any).abs, abs === lib.getFunctions({ abs: { arguments: ['int32'], return: 'int32' } }).abs);
const boxed: any = abs;
console.log(typeof boxed, boxed.name, boxed.length, boxed(-7), boxed.pointer === abs.pointer);
console.log(String(lib.path).length);
lib.close();
`, "function abs 1 bigint true\npointer\ntrue\ntrue true\nfunction abs 1 7 true\n0")
}

func TestE2EFFIArgumentValidation(t *testing.T) {
	// Node's per-type argument rules and messages, for direct calls and for
	// calls through `any` (the dynamic-ABI thunk) alike (TDD-00229).
	assertSameAsNodeFFI(t, `
import ffi from 'node:ffi';
function show(label: string, f: () => unknown) {
  try { console.log(label, f()); } catch (e: any) { console.log(label, e.name, e.code, e.message); }
}
const lib = new ffi.DynamicLibrary(null);
const abs = lib.getFunction('abs', { arguments: ['int32'], return: 'int32' });
const dyn: any = abs;
show('i32 frac', () => abs(1.5));
show('i32 range', () => abs(2 ** 31));
show('i32 -0', () => abs(-0));
show('i32 str', () => dyn('3'));
show('i32 bigint', () => dyn(3n));
show('count 0', () => dyn());
show('count 2', () => dyn(1, 2));
const labs = lib.getFunction('labs', { arguments: ['int64'], return: 'int64' });
show('i64 number', () => labs(5 as any));
show('i64 ok', () => labs(-5n));
show('i64 range', () => labs(2n ** 63n));
const up = lib.getFunction('toupper', { arguments: ['uint8'], return: 'uint8' });
show('u8 neg', () => up(-1));
show('u8 -0', () => up(-0));
show('u8 ok', () => up(97));
const fabs = lib.getFunction('fabs', { arguments: ['double'], return: 'double' });
show('f64 str', () => dyn === dyn && (fabs as any)('2'));
show('f64 nan', () => fabs(NaN));
const sl = lib.getFunction('strlen', { arguments: ['string'], return: 'uint64' });
show('ptr num', () => (sl as any)(5));
show('ptr negbig', () => (sl as any)(-1n));
show('ptr str', () => sl('hello'));
show('ptr buf', () => sl(Buffer.from('abc\0')));
lib.close();
`, strings.Join([]string{
		"i32 frac TypeError ERR_INVALID_ARG_VALUE Argument 0 must be an int32",
		"i32 range TypeError ERR_INVALID_ARG_VALUE Argument 0 must be an int32",
		"i32 -0 TypeError ERR_INVALID_ARG_VALUE Argument 0 must be an int32",
		"i32 str TypeError ERR_INVALID_ARG_VALUE Argument 0 must be an int32",
		"i32 bigint TypeError ERR_INVALID_ARG_VALUE Argument 0 must be an int32",
		"count 0 TypeError ERR_INVALID_ARG_VALUE Invalid argument count: expected 1, got 0",
		"count 2 TypeError ERR_INVALID_ARG_VALUE Invalid argument count: expected 1, got 2",
		"i64 number TypeError ERR_INVALID_ARG_VALUE Argument 0 must be an int64",
		"i64 ok 5n",
		"i64 range TypeError ERR_INVALID_ARG_VALUE Argument 0 must be an int64",
		"u8 neg TypeError ERR_INVALID_ARG_VALUE Argument 0 must be a uint8",
		"u8 -0 0",
		"u8 ok 65",
		"f64 str TypeError ERR_INVALID_ARG_VALUE Argument 0 must be a double",
		"f64 nan NaN",
		"ptr num TypeError ERR_INVALID_ARG_VALUE Argument 0 must be a buffer, an ArrayBuffer, a string, or a bigint",
		"ptr negbig TypeError ERR_INVALID_ARG_VALUE Argument 0 must be a non-negative pointer bigint",
		"ptr str 5n",
		"ptr buf 3n",
	}, "\n"))
}

func TestE2EFFISignatureAndLifecycleErrors(t *testing.T) {
	// Invalid-but-static signatures throw Node's runtime TypeErrors (unknown
	// keys are ignored, so `parameters`/`result` is a void() signature);
	// re-requesting a symbol with another libffi signature conflicts; a closed
	// library throws ERR_FFI_LIBRARY_CLOSED everywhere but `path` (TDD-00229).
	assertSameAsNodeFFI(t, `
import ffi from 'node:ffi';
function show(label: string, f: () => unknown) {
  try { f(); console.log(label, 'ok'); } catch (e: any) { console.log(label, e.name, e.code, e.message); }
}
const lib = new ffi.DynamicLibrary(null);
const abs = lib.getFunction('abs', { arguments: ['int32'], return: 'int32' });
show('int33', () => lib.getFunction('labs', { arguments: ['int33'], return: 'int64' } as any));
show('args str', () => lib.getFunction('labs', { arguments: 'x' } as any));
show('arg void', () => lib.getFunction('labs', { arguments: ['void'] } as any));
show('ret num', () => lib.getFunction('labs', { return: 5 } as any));
show('conflict', () => lib.getFunction('abs', { arguments: ['int64'], return: 'int64' }));
show('param keys', () => lib.getFunction('abs', { parameters: ['i32'], result: 'i32' } as any));
show('ptr family', () => { lib.getFunction('getenv', { arguments: ['string'], return: 'pointer' }); lib.getFunction('getenv', { arguments: ['buffer'], return: 'string' }); });
show('nul name', () => lib.getSymbol('get\0env'));
show('dlopen defs', () => ffi.dlopen(null, { nope: { arguments: ['int33'] } } as any));
lib.close();
show('closed call', () => abs(-1));
show('closed getSymbol', () => lib.getSymbol('abs'));
show('closed getFunction', () => lib.getFunction('abs', { arguments: ['int32'], return: 'int32' }));
show('closed symbols', () => lib.symbols);
show('closed getFunctions', () => lib.getFunctions());
show('closed path', () => lib.path);
show('close again', () => lib.close());
`, strings.Join([]string{
		"int33 TypeError ERR_INVALID_ARG_VALUE Unsupported FFI type: int33",
		"args str TypeError ERR_INVALID_ARG_VALUE Arguments list of function labs must be an array",
		"arg void TypeError ERR_INVALID_ARG_VALUE Argument 0 of function labs must not be 'void'; use an empty array for no-argument functions",
		"ret num TypeError ERR_INVALID_ARG_VALUE Return value type of function labs must be a string",
		"conflict TypeError ERR_INVALID_ARG_VALUE Function abs was already requested with a different signature",
		"param keys TypeError ERR_INVALID_ARG_VALUE Function abs was already requested with a different signature",
		"ptr family ok",
		"nul name TypeError ERR_INVALID_ARG_VALUE Symbol name must not contain null bytes",
		"dlopen defs TypeError ERR_INVALID_ARG_VALUE Unsupported FFI type: int33",
		"closed call Error ERR_FFI_LIBRARY_CLOSED Library is closed",
		"closed getSymbol Error ERR_FFI_LIBRARY_CLOSED Library is closed",
		"closed getFunction Error ERR_FFI_LIBRARY_CLOSED Library is closed",
		"closed symbols Error ERR_FFI_LIBRARY_CLOSED Library is closed",
		"closed getFunctions Error ERR_FFI_LIBRARY_CLOSED Library is closed",
		"closed path ok",
		"close again ok",
	}, "\n"))
}

func TestE2EFFIAccumulatorShapes(t *testing.T) {
	// The accumulators are fresh null-prototype objects; dlopen without
	// definitions hands back a frozen empty one, with definitions an unfrozen
	// one in literal order; a runtime-string name is recorded (TDD-00229).
	assertSameAsNodeFFI(t, `
import ffi from 'node:ffi';
const { lib, functions } = ffi.dlopen(null);
console.log(functions, Object.isFrozen(functions), Object.keys(lib.symbols).length);
const name = ['get', 'pid'].join('');
lib.getSymbol(name);
console.log(Object.keys(lib.symbols).join(','), lib.symbols.getpid > 0n, lib.symbols.nope);
console.log(lib.symbols === lib.symbols, Object.keys(lib.getFunctions()).length);
const d = ffi.dlopen(null, { labs: { arguments: ['int64'], return: 'int64' }, abs: { arguments: ['int32'], return: 'int32' } });
console.log(Object.keys(d.functions).join(','), Object.isFrozen(d.functions), d.functions.abs(-2), d.functions.labs(-3n));
console.log(lib);
lib.close();
d.lib.close();
`, "[Object: null prototype] {} true 0\ngetpid true undefined\nfalse 0\nlabs,abs false 2 3n\nDynamicLibrary { path: [Getter], symbols: [Getter] }")
}

func TestE2EFFIAccumulatorOrderMatchesNode(t *testing.T) {
	// `symbols`/`functions` enumerate in Node's std::unordered_map order —
	// platform-specific by nature (libc++ / libstdc++ / MSVC STL), so this
	// compares against real node:ffi on the host rather than a fixed string.
	assertMatchesNodeFFI(t, `
import ffi from 'node:ffi';
const names = ['abs', 'labs', 'strlen', 'getenv', 'getpid', 'toupper', 'tolower', 'malloc', 'free', 'calloc', 'realloc', 'memcpy', 'memset', 'strcmp', 'strncmp', 'qsort', 'atoi', 'atol', 'rand', 'srand', 'exit', 'abort', 'isdigit', 'isalpha'];
const lib = new ffi.DynamicLibrary(null);
for (let i = 0; i < names.length; i++) {
  lib.getSymbol(names[(i * 7) % names.length]);
  if (i % 5 === 0) console.log(Object.keys(lib.symbols).join(' '));
}
lib.getFunction('abs', { arguments: ['int32'], return: 'int32' });
lib.getFunctions({ labs: { arguments: ['int64'], return: 'int64' }, strlen: { arguments: ['string'], return: 'uint64' } });
console.log(Object.keys(lib.functions).join(' '));
console.log(Object.keys(lib.getSymbols()).join(' '));
lib.close();
`)
}

func TestE2EFFIExportStringEncodingsAndErrors(t *testing.T) {
	// exportString honours Buffer encodings (utf16le/ucs2 get a 2-byte
	// terminator) and validates like Node: a non-string source, a bigint /
	// negative / fractional length, an unknown encoding, and a length that
	// cannot hold the encoded string plus its terminator (TDD-00229).
	assertSameAsNodeFFI(t, `
import ffi from 'node:ffi';
const lib = new ffi.DynamicLibrary(null);
const malloc = lib.getFunction('malloc', { arguments: ['uint64'], return: 'pointer' });
const p = malloc(64n);
function show(label: string, f: () => void) {
  try { f(); console.log(label, 'ok'); } catch (e: any) { console.log(label, e.name, e.code, e.message); }
}
const bytes = (n: number): string => [...ffi.toBuffer(p, n)].join(',');
show('utf8', () => ffi.exportString('héllo', p, 10));
console.log(bytes(8));
show('utf16le', () => ffi.exportString('hé', p, 10, 'utf16le'));
console.log(bytes(6));
show('UCS-2', () => ffi.exportString('hé', p, 10, 'UCS-2'));
show('latin1', () => ffi.exportString('hé', p, 10, 'latin1'));
console.log(bytes(3));
show('hex', () => ffi.exportString('00ff10', p, 10, 'hex'));
console.log(bytes(4));
show('small', () => ffi.exportString('hello', p, 3));
show('small16', () => ffi.exportString('hi', p, 5, 'utf16le'));
show('bigint len', () => ffi.exportString('hi', p, 5n as any));
show('neg len', () => ffi.exportString('hi', p, -1));
show('frac len', () => ffi.exportString('hi', p, 2.5));
show('bad enc', () => ffi.exportString('hi', p, 5, 'nope'));
show('num str', () => ffi.exportString(5 as any, p, 5));
lib.close();
`, strings.Join([]string{
		"utf8 ok",
		"104,195,169,108,108,111,0,0",
		"utf16le ok",
		"104,0,233,0,0,0",
		"UCS-2 ok",
		"latin1 ok",
		"104,233,0",
		"hex ok",
		"0,255,16,0",
		`small RangeError ERR_OUT_OF_RANGE The value of "len" is out of range. It must be >= 6. Received 3`,
		`small16 RangeError ERR_OUT_OF_RANGE The value of "len" is out of range. It must be >= 6. Received 5`,
		`bigint len TypeError ERR_INVALID_ARG_TYPE The "len" argument must be of type number. Received type bigint (5n)`,
		`neg len RangeError ERR_OUT_OF_RANGE The value of "len" is out of range. It must be >= 0 && <= 9007199254740991. Received -1`,
		`frac len RangeError ERR_OUT_OF_RANGE The value of "len" is out of range. It must be an integer. Received 2.5`,
		"bad enc TypeError ERR_UNKNOWN_ENCODING Unknown encoding: nope",
		`num str TypeError ERR_INVALID_ARG_TYPE The "string" argument must be of type string. Received type number (5)`,
	}, "\n"))
}
