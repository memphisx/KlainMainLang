package tests

import (
	"strings"
	"testing"
)

// Tests that exercise declared builtins which had no test of their own, so
// every implemented status row's declarations are named by a test or an
// example (lib/conform TestImplementedBuiltinsHaveDeclarations).

// bt puts backticks back into a raw-string program (template-tag calls).
func bt(src string) string { return strings.ReplaceAll(src, "§", "`") }

// ffi.exportBuffer / exportArrayBuffer copy a view's or buffer's bytes to a
// native address, and refuse a length too small for them.
func TestE2EDeclFFIExportBufferAndArrayBuffer(t *testing.T) {
	assertSameAsNodeFFI(t, `
import ffi from 'node:ffi';
const { functions } = ffi.dlopen(null, {
  malloc: { arguments: ['uint64'], return: 'pointer' },
  free: { arguments: ['pointer'], return: 'void' },
});
const p = functions.malloc(32n);
const buf = Buffer.from([10, 20, 30, 40, 50]);
ffi.exportBuffer(buf, p, 32);
console.log(ffi.getUint8(p, 0), ffi.getUint8(p, 4));
ffi.exportBuffer(buf.subarray(1, 3), p, 2);
console.log(ffi.getUint8(p, 0), ffi.getUint8(p, 1), ffi.getUint8(p, 2));
const ab = new ArrayBuffer(4);
new Uint8Array(ab).set([7, 8, 9, 6]);
ffi.exportArrayBuffer(ab, p, 4);
console.log(ffi.getUint8(p, 0), ffi.getUint8(p, 1), ffi.getUint8(p, 2), ffi.getUint8(p, 3));
try {
  ffi.exportBuffer(buf, p, 3);
  console.log('no throw');
} catch (e) {
  console.log('caught small buffer');
}
try {
  ffi.exportArrayBuffer(ab, p, 1);
  console.log('no throw');
} catch (e) {
  console.log('caught small arraybuffer');
}
functions.free(p);
`, "10 50\n20 30 30\n7 8 9 6\ncaught small buffer\ncaught small arraybuffer")
}

// fs.writevSync / readvSync / fdatasyncSync, the ownership calls
// (chownSync, fchownSync, lchownSync) and lutimesSync, with their errors.
func TestE2EDeclFSSyncVectoredOwnershipAndTimes(t *testing.T) {
	assertSameAsNode(t, `
import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';

const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'declcov-'));
const file = path.join(dir, 'a.txt');
const fd = fs.openSync(file, 'w+');
const w1 = Buffer.from('hello ');
const w2 = Buffer.from('world!');
console.log(fs.writevSync(fd, [w1, w2]));
console.log(fs.writevSync(fd, [Buffer.from('\n')], 12));
fs.fdatasyncSync(fd);
fs.fsyncSync(fd);
const r1 = Buffer.alloc(5);
const r2 = Buffer.alloc(7);
console.log(fs.readvSync(fd, [r1, r2], 0));
console.log(r1.toString(), JSON.stringify(r2.toString()));
const r3 = Buffer.alloc(3);
console.log(fs.readvSync(fd, [r3], 6), r3.toString());
console.log(fs.readvSync(fd, [Buffer.alloc(4)], 100));
fs.closeSync(fd);

const st = fs.statSync(file);
fs.chownSync(file, st.uid, st.gid);
fs.lchownSync(file, st.uid, st.gid);
const fd2 = fs.openSync(file, 'r');
fs.fchownSync(fd2, st.uid, st.gid);
fs.closeSync(fd2);
const st2 = fs.statSync(file);
console.log(st2.uid === st.uid, st2.gid === st.gid);

const link = path.join(dir, 'link');
fs.symlinkSync(file, link);
fs.lutimesSync(link, 1000, 2000);
const ls = fs.lstatSync(link);
console.log(ls.atimeMs, ls.mtimeMs);
const ts = fs.statSync(file);
console.log(ts.mtimeMs !== 2000000);

try {
  fs.readvSync(9999, [Buffer.alloc(1)]);
} catch (e: any) {
  console.log(e.code, e.syscall);
}
try {
  fs.chownSync(path.join(dir, 'missing'), 0, 0);
} catch (e: any) {
  console.log(e.code, e.syscall);
}
try {
  fs.lutimesSync(path.join(dir, 'missing'), 1, 1);
} catch (e: any) {
  console.log(e.code, e.syscall);
}
fs.rmSync(dir, { recursive: true });
`)
}

// The callback forms open / writev / readv / fsync / close, and readv's
// error on a closed descriptor.
func TestE2EDeclFSCallbackVectored(t *testing.T) {
	assertSameAsNode(t, `
import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';

const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'declcov-'));
const file = path.join(dir, 'b.txt');
fs.open(file, 'w+', (err, fd) => {
  console.log(err);
  fs.writev(fd, [Buffer.from('abc'), Buffer.from('defg')], (err2, written, bufs) => {
    console.log(err2, written, bufs.length);
    fs.readv(fd, [Buffer.alloc(2), Buffer.alloc(10)], 0, (err3, bytesRead, bufs2) => {
      console.log(err3, bytesRead, bufs2.length);
      console.log(bufs2[0].toString(), (bufs2[1] as Buffer).subarray(0, 5).toString());
      fs.readv(fd, [Buffer.alloc(3)], 4, (err4, n4, b4) => {
        console.log(err4, n4, b4[0].toString());
        fs.fsync(fd, (err5) => {
          console.log(err5);
          fs.close(fd, (err6) => {
            console.log(err6);
            fs.readv(fd, [Buffer.alloc(1)], (err7: any) => {
              console.log(err7.code, err7.syscall);
              fs.rmSync(dir, { recursive: true });
            });
          });
        });
      });
    });
  });
});
`)
}

// util/types: the predicates, as an import of the module and as the same
// functions util.types holds.
func TestE2EDeclUtilTypesModule(t *testing.T) {
	assertSameAsNode(t, `
import * as types from 'util/types';
import { isDate, isRegExp, isPromise, isMap, isSet, isTypedArray, isUint8Array, isArrayBuffer, isNativeError } from 'util/types';
import * as util from 'util';

console.log(isDate(new Date()), isDate({}));
console.log(isRegExp(/a/), isRegExp('a'));
console.log(isPromise(Promise.resolve(1)), isPromise({ then() {} }));
console.log(isMap(new Map()), isSet(new Set()), isMap(new Set()));
console.log(isTypedArray(new Uint8Array(2)), isUint8Array(new Uint8Array(2)), isUint8Array(new Int8Array(2)));
console.log(isArrayBuffer(new ArrayBuffer(2)), isNativeError(new TypeError('x')), isNativeError({ message: 'x' }));
console.log(types.isDate === util.types.isDate, types.isMap(new Map()));
`)
}

// Number.MIN_VALUE, Math.tan and the Math logarithm constants.
func TestE2EDeclNumberMathConstants(t *testing.T) {
	assertSameAsNode(t, `
console.log(Number.MIN_VALUE, Number.MIN_VALUE > 0, Number.MIN_VALUE / 2);
console.log(Number.MIN_VALUE === 5e-324, -Number.MIN_VALUE);
console.log(Math.tan(0), Math.tan(Math.PI / 4).toFixed(6), Math.tan(1).toFixed(12));
console.log(Math.tan(-1.5).toFixed(9), Math.tan(Math.PI / 2) > 1e15);
console.log(Math.LN2, Math.LN10, Math.LOG2E, Math.LOG10E);
console.log(Math.LN2 * Math.LOG2E, Math.exp(Math.LN2).toFixed(12), Math.log(10) === Math.LN10);
console.log((Math.LOG10E * Math.LN10).toFixed(12));
`)
}

// performance.getEntriesByName / clearMeasures / timerify.
func TestE2EDeclPerformanceEntriesAndTimerify(t *testing.T) {
	assertSameAsNode(t, `
import { performance } from 'perf_hooks';
performance.clearMarks();
performance.clearMeasures();
performance.mark('a');
performance.mark('b');
const m = performance.measure('a-to-b', 'a', 'b');
console.log(m.name, m.entryType, m.duration >= 0);
performance.measure('second', 'a');
console.log(performance.getEntriesByName('a-to-b').length, performance.getEntriesByName('a-to-b', 'measure').length, performance.getEntriesByName('a-to-b', 'mark').length);
console.log(performance.getEntriesByName('a').map((e) => e.entryType).join(','));
console.log(performance.getEntriesByType('measure').map((e) => e.name).join(','));
console.log(performance.getEntries().length);
performance.clearMeasures('second');
console.log(performance.getEntriesByType('measure').map((e) => e.name).join(','));
performance.clearMeasures();
console.log(performance.getEntriesByType('measure').length, performance.getEntriesByType('mark').length);
performance.clearMarks('a');
console.log(performance.getEntriesByType('mark').map((e) => e.name).join(','));

function add(x: number, y: number): number { return x + y; }
const timed = performance.timerify(add);
console.log(timed(2, 3), typeof timed, timed !== add);
`)
}

// node:sqlite's SQLTagStore (db.createTagStore) and backup().
func TestE2EDeclSqliteTagStoreAndBackup(t *testing.T) {
	nodeSupportsSqlite(t)
	assertSameAsNode(t, bt(`
import { DatabaseSync, backup, constants } from 'node:sqlite';
import type { SQLTagStore } from 'node:sqlite';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

const db = new DatabaseSync(':memory:');
db.exec('CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)');
const sql: SQLTagStore = db.createTagStore();
console.log(sql.db === db, sql.capacity, sql.size);
console.log(sql.run§INSERT INTO t (name) VALUES (${'alice'})§.changes);
console.log(sql.run§INSERT INTO t (name) VALUES (${'bob'})§.changes);
console.log(sql.size);
console.log(JSON.stringify(sql.get§SELECT * FROM t WHERE name = ${'bob'}§));
console.log(JSON.stringify(sql.all§SELECT name FROM t WHERE id >= ${1} ORDER BY id§));
console.log(sql.get§SELECT * FROM t WHERE name = ${'nobody'}§);
let n = 0;
for (const row of sql.iterate§SELECT id FROM t ORDER BY id§) n += (row as any).id;
console.log(n, sql.size);
sql.clear();
console.log(sql.size);
const small = db.createTagStore(1);
small.get§SELECT 1 AS a§;
small.get§SELECT 2 AS b§;
console.log(small.capacity, small.size);

const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'declcov-'));
const dest = path.join(dir, 'copy.db');
const pages: number[] = [];
backup(db, dest, { rate: 1, progress: (p) => pages.push(p.totalPages) }).then((total) => {
  console.log(typeof total, total > 0, pages.length > 0);
  const copy = new DatabaseSync(dest);
  console.log(JSON.stringify(copy.prepare('SELECT id, name FROM t ORDER BY id').all()));
  copy.close();
  db.close();
  fs.rmSync(dir, { recursive: true });
  console.log(typeof constants.SQLITE_CHANGESET_OMIT, constants.SQLITE_CHANGESET_ABORT);
});
`))
}

// klain:tui's TextInput clips to one line where Text wraps to its box.
func TestE2EDeclTuiTextInput(t *testing.T) {
	out := runTui(t, `
import { Box, Text, TextInput, render } from 'klain:tui'
render(Box({ flexDirection: 'column', width: 12 }, [
  TextInput('abcdefghijklmnopqrstuvwxyz'),
  Text('abcdefghijklmnopqrstuvwxyz'),
]))
`)
	if got := strings.Count(out, "abcdefghijkl"); got != 2 {
		t.Errorf("first 12 columns painted %d times, want 2 (input and text): %q", got, out)
	}
	if got := strings.Count(out, "mnopqrstuvwx"); got != 1 {
		t.Errorf("second row painted %d times, want 1 (wrapped text only): %q", got, out)
	}
}

// klain:tui's TextInput with wrap: true wraps to its box like Text.
func TestE2ETuiTextInputWrap(t *testing.T) {
	out := runTui(t, `
import { Box, TextInput, render } from 'klain:tui'
render(Box({ flexDirection: 'column', width: 12 }, [
  TextInput('abcdefghijklmnopqrstuvwxyz', { wrap: true }),
]))
`)
	for _, row := range []string{"abcdefghijkl", "mnopqrstuvwx", "yz"} {
		if !strings.Contains(out, row) {
			t.Errorf("wrapped row %q not painted: %q", row, out)
		}
	}
}

// crypto: Hmac, Cipheriv / Decipheriv (CBC and GCM) and randomFillSync /
// randomFill with typed-array element offsets.
func TestE2EDeclCryptoHmacCipherRandomFill(t *testing.T) {
	assertSameAsNode(t, `
import { createHmac, Hmac, createCipheriv, createDecipheriv, Cipheriv, Decipheriv, randomFillSync, randomFill, getCiphers } from 'node:crypto';

const h: Hmac = createHmac('sha256', 'secret');
console.log(h instanceof Hmac);
h.update('hello ');
h.update('world');
console.log(h.digest('hex'));
console.log(createHmac('sha1', Buffer.from('k')).update('data').digest('base64'));

const key = Buffer.alloc(32, 7);
const iv = Buffer.alloc(16, 3);
const c: Cipheriv = createCipheriv('aes-256-cbc', key, iv);
console.log(c instanceof Cipheriv);
const enc = Buffer.concat([c.update('The quick brown fox', 'utf8'), c.final()]);
console.log(enc.toString('hex'));
const d: Decipheriv = createDecipheriv('aes-256-cbc', key, iv);
console.log(d instanceof Decipheriv);
const dec = Buffer.concat([d.update(enc), d.final()]);
console.log(dec.toString());

const gcm = createCipheriv('aes-128-gcm', Buffer.alloc(16, 1), Buffer.alloc(12, 2));
gcm.setAAD(Buffer.from('hdr'));
const g1 = Buffer.concat([gcm.update('secret message'), gcm.final()]);
const tag = gcm.getAuthTag();
console.log(g1.toString('hex'), tag.toString('hex'));
const dgcm = createDecipheriv('aes-128-gcm', Buffer.alloc(16, 1), Buffer.alloc(12, 2));
dgcm.setAAD(Buffer.from('hdr'));
dgcm.setAuthTag(tag);
console.log(Buffer.concat([dgcm.update(g1), dgcm.final()]).toString());
const bad = createDecipheriv('aes-128-gcm', Buffer.alloc(16, 1), Buffer.alloc(12, 2));
bad.setAuthTag(Buffer.alloc(16));
bad.update(g1);
try { bad.final(); console.log('no throw'); } catch (e: any) { console.log('auth fail', e.message); }
try { createCipheriv('aes-256-cbc', Buffer.alloc(5), iv); } catch (e: any) { console.log(e.code, e.message); }
try { createCipheriv('nope-cipher', key, iv); } catch (e: any) { console.log(e.code, e.message); }
console.log(getCiphers().includes('aes-256-cbc'));

const buf = Buffer.alloc(16);
const r = randomFillSync(buf);
console.log(r === buf, buf.length, buf.some((b) => b !== 0));
const part = Buffer.alloc(8);
randomFillSync(part, 4);
console.log(part.subarray(0, 4).every((b) => b === 0));
const u32 = new Uint32Array(4);
console.log(randomFillSync(u32, 1, 2) === u32, u32[0] === 0, u32[1] !== 0, u32[2] !== 0, u32[3] === 0);
try { randomFillSync(Buffer.alloc(4), 5); } catch (e: any) { console.log(e.code); }
try { randomFillSync('x' as any); } catch (e: any) { console.log(e.code); }
const u16 = new Uint16Array(6);
randomFill(u16, 2, 2, (err, filled) => {
  console.log(err, filled === u16, u16[0] === 0, u16[1] === 0, u16[4] === 0, u16[5] === 0);
});
`)
}

// process.hrtime through its declaration (a callable interface with
// bigint()): an Array, indexed, destructured and diffed in a function as at
// the top level; Array.isArray of any tuple, its argument evaluated.
func TestE2EDeclProcessHrtime(t *testing.T) {
	assertSameAsNode(t, `
const t = process.hrtime();
const [s, ns] = process.hrtime();
console.log(Array.isArray(t), t.length, typeof s, typeof ns, process.hrtime(t).length, typeof process.hrtime.bigint());
function el() { const st = process.hrtime(); return process.hrtime(st)[0] >= 0 && process.hrtime()[1] >= 0; }
console.log(el());
function f(): [number, number] { console.log("ran"); return [1, 2]; }
console.log(Array.isArray(f()), Array.isArray(new Uint8Array(1)));
`)
}

// A function's and a generator object's Object.prototype.toString tag,
// util.inspect prefix and the util/types predicates reading them.
func TestE2EFunctionKindTags(t *testing.T) {
	assertSameAsNodeImports(t, `
import { types } from "util";
const tag = (v: any) => Object.prototype.toString.call(v);
function* g() { yield 1; }
async function af() {}
async function* ag() {}
console.log(tag(g), tag(af), tag(async () => {}), tag(g()), tag(ag), tag(ag()), tag(() => 1));
const x: any = g(); const y: any = ag();
console.log(x, y, x[Symbol.toStringTag], String(x));
console.log(types.isGeneratorFunction(g), types.isAsyncFunction(af), types.isGeneratorObject(g()), types.isGeneratorFunction(af));
`)
}

// A function's own properties: defined or assigned, read through a typed
// adapter (performance.timerify's result), and listed by Object.keys.
func TestE2EFunctionOwnPropertiesKeys(t *testing.T) {
	assertSameAsNodeImports(t, `
import { performance } from "perf_hooks";
function add(a: number, b: number) { return a + b; }
const t = performance.timerify(add);
console.log(t.name, t.length, Object.keys(t), t(1, 2));
const g = (...a: any[]) => 1;
Object.defineProperties(g, { name: { value: "n2", enumerable: true }, length: { value: 5, enumerable: true } });
console.log(g.name, g.length, Object.keys(g));
function f() {}
(f as any).x = 1;
console.log(Object.keys(f), Object.keys(() => 1));
`)
}

// Array.prototype.flat through lib.es2019's FlatArray: the result type the
// checker gives (a generic `this` parameter inferred from the receiver),
// mixed-depth elements unboxed, at the top level and in a function.
func TestE2EArrayFlatFlatArray(t *testing.T) {
	assertSameAsNode(t, `
const a = [[1, 2], [3, [4, 5]]];
const x = a.flat();
const y = a.flat(2);
const z = [["a"], ["b"]].flat();
function f() { return x.length + y.length + z.join("").length; }
console.log(x, y, z, f());
function g(n: number[][]) { const q: number[] = n.flat(); return q; }
function m() { const b = [[1, 2], [3, [4, 5]]]; const w = b.flat(2); return w[3] + 1; }
console.log(g([[1], [2, 3]]), m());
`)
}

// Math's transcendental functions return V8's fdlibm bits, not the
// platform libm's (Math.tan(1) is 1.5574077246549023), called and passed
// as values.
func TestE2EMathFdlibmBits(t *testing.T) {
	assertSameAsNode(t, `
const xs = [1, 0.5, 2, 10, -3, 100, 1e10, 1e300, 5e-324, -0, Math.PI / 2, 0.7853981633974483];
const fs = [Math.tan, Math.sin, Math.cos, Math.exp, Math.expm1, Math.log, Math.log1p, Math.log2, Math.log10,
  Math.asin, Math.acos, Math.atan, Math.sinh, Math.cosh, Math.tanh, Math.asinh, Math.acosh, Math.atanh];
for (const f of fs) console.log(xs.map(x => f(x)).join(" "));
console.log(Math.tan(1), Math.atan2(1, 3), Math.atan2(-0, -1));
`)
}
