package tests

import "testing"

func TestE2EConsoleModuleConsoleClass(t *testing.T) {
	// The console module: the global console as its default export, and a
	// Console writing to given streams with bound methods, group indent,
	// counts and Node's errors; ignoreErrors swallows an async write error.
	assertSameAsNode(t, `
import { Console } from 'console';
import defaultConsole from 'console';
import { Writable } from 'stream';
const chunks: string[] = [];
const w = new Writable({ write(c, _e, cb) { chunks.push(String(c)); cb(); } });
const c = new Console(w);
c.log('a %d', 1, { x: 1 }); c.group('g'); c.warn('in'); c.groupEnd(); c.count(); c.count(); c.assert(false, 'no %s', 'way'); c.dir({ y: [1] });
const { log } = c; log('bound');
c.table([{ a: 1 }]); c.log(Symbol('s'));
console.log(JSON.stringify(chunks), defaultConsole === console, typeof Console);
try { new Console({} as any); } catch (e: any) { console.log(e.name, e.code, e.message); }
try { console.table([], false as any); } catch (e: any) { console.log(e.code, e.message); }
const bad = new Writable({ write(_c, _e, cb) { process.nextTick(cb, new Error('foobar')); } });
new Console(bad, bad, true).log('abc');
setTimeout(() => console.log('survived'), 20);
`)
}

func TestE2EStringTrimAliasesAndSymbolInspect(t *testing.T) {
	// trimLeft/trimRight are trimStart/trimEnd, the same functions;
	// util.inspect shows a symbol as Symbol(description).
	assertSameAsNode(t, `
import { inspect, format } from 'util';
console.log(JSON.stringify([" ab ".trimLeft(), " ab ".trimRight()]), String.prototype.trimLeft === String.prototype.trimStart, String.prototype.trimRight.name);
console.log(inspect(Symbol("z")), inspect([Symbol("a"), { k: Symbol.iterator }]), format("%s %o", Symbol("s"), Symbol("o")));
`)
}

func TestE2EConsoleTableCollectionIterator(t *testing.T) {
	// console.table of a Map or Set iterator lists the entries it has yet
	// to yield, without consuming them.
	assertSameAsNode(t, `
const m = new Map<string, number>([["a", 1], ["b", 2]]);
const it = m.entries(); it.next();
console.table(it);
console.log([...it]);
console.table(it);
console.table(m.values());
console.table(m.keys());
const s = new Set(["x", "y"]);
console.table(s.values());
console.table(s.entries());
`)
}

// The global console's Console is the console module's class: read, built
// with `new`, and extended.
func TestE2EGlobalConsoleConsole(t *testing.T) {
	src := `
class C extends console.Console {}
new C(process.stdout).log('hi');
console.log(typeof console.Console, new console.Console(process.stderr) instanceof console.Console);
`
	assertSameAsNode(t, src)
	assertSameAsNodeCLIJS(t, src)
}

// A first argument that is a string only at run time is util.format's
// format string: a variable, a spread and a console method read by value.
func TestE2EConsoleRuntimeFormatStringTS(t *testing.T) {
	assertSameAsNode(t, `
const fs: string[] = ["%s is %d years", "%s|%d|%i|%f", "%j %O", "%c[%s]", "100%% %s", "%x %s", "no spec", "%s %s %s"];
console.log(fs[0], "Ann", 42);
const args: any[] = ["%s-%s", "a", "b"];
console.log(...args);
const log = console.log;
log("%o!", 5);
console.error(fs[0], "Bo", 7);
const o: any = { a: 1, b: [1, 2] };
console.log(fs[1], "x", 3.7, "42.9abc", "1.5e3");
console.log(fs[2], o, o);
console.log(fs[3], "color:red", "t");
console.log(fs[4], "y", "extra", 9, o);
console.log(fs[5], "z");
console.log(fs[6], "w", 1);
console.log(fs[7], -0, null, undefined);
const n: any = 12n; console.log(fs[1], n, n, n, n);
console.log(fs[1], "only");
const a: any = 5; console.log(a, "%s");
`)
}
