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
// signature objects. POSIX-only for now (no LoadLibrary shim on Windows).

func skipFFIOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("node:ffi is POSIX-only for now (no LoadLibrary shim)")
	}
}

// hostSuffix mirrors ffi.suffix for assertions.
func hostSuffix() string {
	if runtime.GOOS == "darwin" {
		return "dylib"
	}
	return "so"
}

func TestE2EFFILibcSelfProcess(t *testing.T) {
	skipFFIOnWindows(t)
	assertOutputImports(t, `
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
console.log(strchr('hello', 108));
console.log(lib.getSymbol('strlen') > 0n);
lib.close();
console.log('closed');
`, "5n\n1024\ntrue\nllo\ntrue\nclosed")
}

func TestE2EFFIPointerBigintRoundTrip(t *testing.T) {
	skipFFIOnWindows(t)
	assertOutputImports(t, `
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
	skipFFIOnWindows(t)
	assertOutputImports(t, `
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
	skipFFIOnWindows(t)
	assertOutputImports(t, `
import ffi from 'node:ffi';
console.log(ffi.suffix);
console.log(ffi.types.INT_32, ffi.types.DOUBLE, ffi.types.POINTER);
`, hostSuffix()+"\nint32 float64 pointer")
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
	skipFFIOnWindows(t)
	libFile := buildFFITestLib(t)
	assertOutputImports(t, fmt.Sprintf(`
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
console.log(fns.greet());
console.log(fns.usub(2, 3));
const buf = new Uint8Array(5);
fns.fill(buf, 5);
console.log(buf[0], buf[3], buf[4]);
const add2 = lib.getFunction('add_i32', { arguments: ['int32', 'int32'], return: 'int32' });
console.log(add2(1, 2), add2.pointer > 0n);
ffi.dlclose(lib);
lib.close();
console.log('done');
`, libFile), "42\n9000000000n\n10.5\n5\nhi from C\n4294967295\n0 6 8\n3 true\ndone")
}

func TestE2EFFIPrimitiveAccessors(t *testing.T) {
	skipFFIOnWindows(t)
	assertOutputImports(t, `
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
	skipFFIOnWindows(t)
	assertOutputImports(t, `
import ffi from 'node:ffi';
const { functions } = ffi.dlopen(null, {
  malloc: { arguments: ['uint64'], return: 'pointer' },
  free: { arguments: ['pointer'], return: 'void' },
});
const p = functions.malloc(64n);
ffi.exportString('hello ffi', p, 64n);
console.log(ffi.toString(p));
console.log(ffi.toString(0n) === null);
ffi.exportString('truncated!', p, 6);
console.log(ffi.toString(p));
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
`, "hello ffi\ntrue\ntrunc\n65 90\n5\ntrue\n42 24\ncaught small")
}

func TestE2EFFIRegisterCallbackQsort(t *testing.T) {
	skipFFIOnWindows(t)
	assertOutputImports(t, `
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

func TestE2EFFICallbackScalarStringAndSlots(t *testing.T) {
	skipFFIOnWindows(t)
	libFile := buildFFITestLib(t)
	assertOutputImports(t, fmt.Sprintf(`
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
  (s: string) => { console.log('from C: ' + s); });
fns.invoke_str(shout, 'hello callback');
// Slot exhaustion: 16 concurrently-live callbacks per signature shape.
let taken = 0;
try {
  for (let i = 0; i < 20; i++) {
    lib.registerCallback({ arguments: ['float64'], return: 'float64' }, (x: number): number => x);
    taken = taken + 1;
  }
} catch (e) {
  console.log('slots exhausted after', taken);
}
`, libFile), "42\n10.5\nfrom C: hello callback\nslots exhausted after 15")
}

func TestE2EFFISymbolAccumulators(t *testing.T) {
	skipFFIOnWindows(t)
	assertOutputImports(t, `
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
`, "6n\n4\ntrue true true\ntrue\n9\nstrlen,getpid,abs\n1")
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
	skipFFIOnWindows(t)
	assertFFICodegenErrorImports(t, `
import ffi from 'node:ffi';
const sig = { arguments: ['int32'], return: 'int32' };
const { lib } = ffi.dlopen(null);
lib.getFunction('abs', sig);
`, "signature must be an object literal")
	assertFFICodegenErrorImports(t, `
import ffi from 'node:ffi';
const { functions } = ffi.dlopen(null, { myabs: { arguments: ['int33'], return: 'int32' } });
`, "unknown FFI type name 'int33'")
	assertFFICodegenErrorImports(t, `
import ffi from 'node:ffi';
const { lib } = ffi.dlopen(null);
lib.registerCallback({ arguments: ['pointer'], return: 'int32' }, (v: number): number => v * 2);
`, "must be a bigint for FFI type 'pointer'")
}
