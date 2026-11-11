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
