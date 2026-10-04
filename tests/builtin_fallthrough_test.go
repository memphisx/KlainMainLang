package tests

import (
	"os/exec"
	"strings"
	"testing"
)

// Builtin calls through their declarations once the by-name fall-through
// is gone (ADR-01339). Outputs are checked against the local Node.

func TestE2EPromiseReactionsThroughDeclarations(t *testing.T) {
	assertSameAsNode(t, `
const p = Promise.resolve(3);
p.then(v => v * 2).then(v => console.log("then", v)).finally(() => console.log("fin"));
Promise.reject(new Error("x")).catch(e => console.log("caught", (e as Error).message));
async function f(): Promise<string> { return "a"; }
f().then(s => console.log(s.length));
`)
}

// A handler that throws rejects the promise its then() returned.
func TestE2EThenHandlerThrowRejectsDerivedPromise(t *testing.T) {
	assertSameAsNode(t, `
Promise.resolve(1).then((x: number) => { throw new Error("a"); }).catch((e: Error) => console.log("c1", e.message));
const p = Promise.resolve(2).then((x) => { if (x > 1) throw new Error("b"); return x; });
p.then(() => console.log("no"), (e: Error) => console.log("c2", e.message));
async function runAll() {
  try { await Promise.resolve(1).then((x: number) => { throw new Error("d"); }); } catch (e: any) { console.log("c3", e.message); }
}
runAll();
`)
}

func TestE2EToStringOnUnionsAndAny(t *testing.T) {
	assertSameAsNode(t, `
const r = /a+b/gi; console.log(r.toString());
const b = true; console.log(b.toString());
const n = 255; console.log(n.toString(), n.toString(16));
function f(v: boolean | number) { console.log(v.toString()); } f(false); f(7);
type PemInput = string | Buffer | Array<string | Buffer>;
function pem(input: PemInput | undefined): string {
  if (input === undefined) return "";
  if (typeof input === "string") return input;
  if (Buffer.isBuffer(input)) return input.toString();
  let out = "";
  for (const one of input as Array<string | Buffer>) out += typeof one === "string" ? one : one.toString();
  return out;
}
console.log(pem(Buffer.from("abc")), pem("x"), pem([Buffer.from("q"), "r"]));
`)
	assertSameAsNodeCompatJS(t, `
var q = [true, 3, "s"]; for (var i = 0; i < 3; i++) console.log(q[i].toString());
function g(a) { return a.toString(); } console.log(g(5), g(false));
var o = { toString: function () { return "mine"; } }; var oo = o; console.log(oo.toString());
var arr = [1, [2, 3]]; var a2 = arr; console.log(a2.toString());
try { var u; u.toString(); } catch (e) { console.log(e.message); }
`)
}

func TestE2EMatchAllIsARegExpStringIterator(t *testing.T) {
	assertSameAsNode(t, `
const s = "a-1 b-22 c-333";
const it = s.matchAll(/(\w)-(?<n>\d+)/g);
console.log(Object.prototype.toString.call(it));
for (const m of it) console.log(m[0], m[1], m.index, m.groups!.n, m.input === s);
const r = /x*/g;
console.log([..."abc".matchAll(r)].length, r.lastIndex);
const r2 = /\d/g; r2.lastIndex = 2;
console.log([..."1a2b3".matchAll(r2)].map(m => m[0]).join(","), r2.lastIndex);
const it2 = "q1q2".matchAll(/q(\d)/g);
const first = it2.next(); console.log(first.done, first.value![1]);
console.log([...it2].length);
console.log(Object.prototype.toString.call([1, 2].values()));
`)
}

func TestE2EMatchWithARegExpValue(t *testing.T) {
	assertSameAsNode(t, `
const re: RegExp = /b(c)/;
const m = "abc".match(re)!;
console.log(m.index, m.input, m[1], m);
const g: RegExp = /[ab]/g;
const gm = "abcab".match(g)!;
console.log(gm, gm.index, gm.input, gm.length);
console.log("zzz".match(g), "zzz".match(re), gm?.index);
`)
}

func TestE2ENewRegExpOfARegExp(t *testing.T) {
	assertSameAsNode(t, `
const r = /a(\d)/g; r.lastIndex = 3;
const c = new RegExp(r); console.log(c.lastIndex, c.flags, c.source, c === r);
const d = new RegExp(/x/gi, "m"); console.log(d.flags, d.source);
console.log(new RegExp("^[^]+$").test("a\nb"), /a[]b/.test("ab"), /[]a]/.test("a]"));
`)
}

func TestE2EBufferSearchFillAndSliceShare(t *testing.T) {
	assertSameAsNode(t, `
const b = Buffer.from("hello world");
console.log(b.indexOf("o"), b.lastIndexOf("o"), b.includes("wor"), b.indexOf(111));
const s = b.slice(0, 5); s[0] = 72; console.log(b.toString());
console.log(Buffer.alloc(4).fill("ab").toString());
function tot(parts: Uint8Array[]): number { let n = 0; for (const p of parts) n += p.length; return n; }
console.log(tot([Buffer.from("ab"), new Uint8Array([5])]));
console.log(new Uint8Array(3).fill(1), ArrayBuffer.isView(new Uint8Array(3).fill(1) as any));
`)
}

func TestE2EHostMethodsAsValues(t *testing.T) {
	assertSameAsNode(t, `
const m = new Map<string, number>([["a", 1]]);
console.log(typeof m.get, typeof /x/.test, typeof Promise.resolve(1).then, typeof new Set([1]).has);
console.log(m.get.call(m, "a"));
const it = [1, 2].values(); console.log(typeof it.next);
`)
}

func TestE2EFunctionCallApplyBindThroughDeclarations(t *testing.T) {
	assertSameAsNode(t, `
function add(a: number, b: number): number { return a + b; }
console.log(add.call(null, 1, 2), add.apply(null, [3, 4]), add.bind(null, 10)(5));
`)
}

func TestE2ENewArrayWithoutTypeArgument(t *testing.T) {
	assertSameAsNode(t, `
const u = new Array(3);
u[1] = "x"; u.push(5);
console.log(u.length, u[1], u[3], u[0]);
console.log(new Array<number>(3).fill(7), new Array(2).fill(0).length);
`)
}

func TestE2EURLPatternExecChurnASan(t *testing.T) {
	bin := buildBinaryASan(t, `
const p = new URLPattern("https://:host.example.com/:a/:b/:c(\\d+)?");
let n = 0;
for (let i = 0; i < 200; i++) {
  const m = p.exec("https://x.example.com/u/v/" + i);
  if (m !== null) n += Object.keys(m.pathname.groups).length;
}
console.log(n);
`)
	out, err := exec.Command(bin).CombinedOutput()
	got := string(out)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, got)
	}
	if strings.Contains(got, "AddressSanitizer") || strings.Contains(got, "runtime error") {
		t.Fatalf("sanitizer error:\n%s", got)
	}
	if strings.TrimSpace(got) != "600" {
		t.Fatalf("got %q", got)
	}
}

func TestE2EThenHandlerThrowChurnASan(t *testing.T) {
	bin := buildBinaryASan(t, `
let caught = 0;
const jobs: Promise<void>[] = [];
for (let i = 0; i < 100; i++) {
  jobs.push(Promise.resolve(i).then((x: number) => { if (x % 2 === 0) throw new Error("e" + x); }).catch(() => { caught++; }));
}
Promise.all(jobs).then(() => console.log(caught));
`)
	out, err := exec.Command(bin).CombinedOutput()
	got := string(out)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, got)
	}
	if strings.Contains(got, "AddressSanitizer") || strings.Contains(got, "runtime error") {
		t.Fatalf("sanitizer error:\n%s", got)
	}
	if strings.TrimSpace(got) != "50" {
		t.Fatalf("got %q", got)
	}
}
