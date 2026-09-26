package tests

import (
	"strings"
	"testing"
)

// --- EventEmitter<T> (TDD-00023) ---

func TestE2EEventEmitterBasicOnEmitString(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
const e = new EventEmitter()
e.on('msg', (data: string): void => {
  console.log('got: ' + data)
})
const result = e.emit('msg', 'hello')
console.log(result)
`, "got: hello\ntrue")
}

func TestE2EEventEmitterBasicOnEmitNumber(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
const e = new EventEmitter()
e.on('tick', (n: number): void => {
  console.log(n * 2)
})
e.emit('tick', 21)
`, "42")
}

func TestE2EEventEmitterMultipleListenersOrder(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
const e = new EventEmitter()
e.on('x', (data: string): void => { console.log('first: ' + data) })
e.on('x', (data: string): void => { console.log('second: ' + data) })
e.emit('x', 'go')
`, "first: go\nsecond: go")
}

func TestE2EEventEmitterEmitReturnsFalseWhenUnlistened(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
const e = new EventEmitter()
e.on('a', (data: string): void => { console.log(data) })
console.log(e.emit('a', 'hi'))
console.log(e.emit('b', 'nope'))
`, "hi\ntrue\nfalse")
}

func TestE2EEventEmitterOnce(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
const e = new EventEmitter()
e.once('x', (data: string): void => { console.log('once: ' + data) })
console.log(e.listenerCount('x'))
e.emit('x', 'a')
console.log(e.listenerCount('x'))
e.emit('x', 'b')
`, "1\nonce: a\n0")
}

func TestE2EEventEmitterOffRemovesOneListener(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
const e = new EventEmitter()
const listener1 = (data: string): void => { console.log('one: ' + data) }
const listener2 = (data: string): void => { console.log('two: ' + data) }
e.on('x', listener1)
e.on('x', listener2)
e.off('x', listener1)
e.emit('x', 'go')
console.log(e.listenerCount('x'))
`, "two: go\n1")
}

// A listener passed by a *named-function* reference (not a const-bound closure)
// must still match on off(): every reference to a named function now yields the
// same static closure header, so the pointer comparison off() does succeeds.
// Regressed when each reference malloc'd a fresh header.
func TestE2EEventEmitterOffByNamedFunctionRef(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
let count = 0
function onPing(msg: string): void { count = count + 1 }
const e = new EventEmitter()
e.on('ping', onPing)
e.emit('ping', 'a')
e.off('ping', onPing)
e.emit('ping', 'b')
console.log(count)
console.log(e.listenerCount('ping'))
`, "1\n0")
}

func TestE2EEventEmitterRemoveListenerAlias(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
const e = new EventEmitter()
const listener1 = (data: string): void => { console.log(data) }
e.on('x', listener1)
e.removeListener('x', listener1)
console.log(e.listenerCount('x'))
`, "0")
}

func TestE2EEventEmitterRemoveAllListenersOneEvent(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
const e = new EventEmitter()
e.on('a', (data: string): void => { console.log('a: ' + data) })
e.on('b', (data: string): void => { console.log('b: ' + data) })
e.removeAllListeners('a')
e.emit('a', 'x')
e.emit('b', 'y')
console.log(e.listenerCount('a'))
console.log(e.listenerCount('b'))
`, "b: y\n0\n1")
}

func TestE2EEventEmitterRemoveAllListenersNoArg(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
const e = new EventEmitter()
e.on('a', (data: string): void => { console.log('a: ' + data) })
e.on('b', (data: string): void => { console.log('b: ' + data) })
e.removeAllListeners()
console.log(e.emit('a', 'x'))
console.log(e.emit('b', 'y'))
`, "false\nfalse")
}

func TestE2EEventEmitterListenerCountAndEventNames(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
const e = new EventEmitter()
e.on('a', (data: string): void => {})
e.on('a', (data: string): void => {})
e.on('b', (data: string): void => {})
console.log(e.listenerCount('a'))
console.log(e.listenerCount('b'))
console.log(e.listenerCount('c'))
const names = e.eventNames()
console.log(names.length)
`, "2\n1\n0\n2")
}

func TestE2EEventEmitterChaining(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
const e = new EventEmitter()
e.on('a', (data: string): void => { console.log('a: ' + data) }).on('b', (data: string): void => { console.log('b: ' + data) })
e.emit('a', 'x')
e.emit('b', 'y')
`, "a: x\nb: y")
}

func TestE2EEventEmitterErrorEventThrowsWhenUnlistened(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
const e = new EventEmitter()
try {
  e.emit('error', 'boom')
} catch (err) {
  console.log('caught: ' + (err as Error).message)
}
`, "caught: Unhandled error. ('boom')")
}

func TestE2EEventEmitterErrorEventDoesNotThrowWhenListened(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
const e = new EventEmitter()
e.on('error', (msg: string): void => { console.log('handled: ' + msg) })
e.emit('error', 'boom')
console.log('after')
`, "handled: boom\nafter")
}

func TestE2EEventEmitterErrorPayloadRethrowsExactError(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
const e = new EventEmitter()
try {
  e.emit('error', new Error('bad thing'))
} catch (err) {
  console.log((err as Error).message)
  console.log(err instanceof Error)
}
`, "bad thing\ntrue")
}

func TestE2EEventEmitterErrorPayloadListenedWithUntypedListener(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
const e = new EventEmitter()
e.on('error', (err) => { console.log('handled: ' + err.message) })
e.emit('error', new Error('bad thing'))
console.log('after')
`, "handled: bad thing\nafter")
}

// --- class X extends EventEmitter<T> ---

func TestE2EEventEmitterClassExtendsBasic(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
class Downloader extends EventEmitter {
  name: string;
  constructor(name: string) {
    super();
    this.name = name;
  }
  start(): void {
    this.emit('progress', 'starting ' + this.name)
  }
}
const d = new Downloader('file.zip')
d.on('progress', (msg: string): void => { console.log(msg) })
d.start()
`, "starting file.zip")
}

func TestE2EEventEmitterClassExtendsFieldsCoexist(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
class Counter extends EventEmitter {
  count: number;
  constructor() {
    super();
    this.count = 0;
  }
  increment(): void {
    this.count = this.count + 1;
    this.emit('change', this.count)
  }
}
const c = new Counter()
c.on('change', (n: number): void => { console.log('now: ' + n) })
c.increment()
c.increment()
console.log(c.count)
`, "now: 1\nnow: 2\n2")
}

func TestE2EEventEmitterClassExtendsMultiLevel(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
class Base extends EventEmitter {
}
class Mid extends Base {
  trigger(n: number): void {
    this.emit('tick', n)
  }
}
class Leaf extends Mid {
}
const l = new Leaf()
l.on('tick', (n: number): void => { console.log(n) })
l.trigger(7)
console.log(l.listenerCount('tick'))
`, "7\n1")
}

func TestE2EEventEmitterClassExtendsWithVTable(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
class Shape extends EventEmitter {
  area(): number {
    return 0
  }
  describe(): void {
    console.log('shape area ' + this.area())
    this.emit('described', 'shape')
  }
}
class Square extends Shape {
  side: number;
  constructor(side: number) {
    super();
    this.side = side;
  }
  area(): number {
    return this.side * this.side
  }
}
const sq = new Square(4)
sq.on('described', (msg: string): void => { console.log('event: ' + msg) })
sq.describe()
`, "shape area 16\nevent: shape")
}

// --- negative / compile-error cases ---

// TDD-00157: declaring an EventEmitter method name is now a legal override,
// not a reserved-name collision. An override wins at the call site, and
// super.<method>(...) reaches the built-in behavior.
func TestE2EEventEmitterOverrideEmitCountsAndDelegates(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
class Bus extends EventEmitter {
  emitCount: number = 0;
  emit(event: string, payload: string): boolean {
    this.emitCount++;
    return super.emit(event, payload);
  }
}
const bus = new Bus();
bus.on("msg", (p: string) => console.log("got:", p));
bus.emit("msg", "hello");
bus.emit("msg", "world");
console.log("count:", bus.emitCount, "listeners:", bus.listenerCount("msg"));
`, "got: hello\ngot: world\ncount: 2 listeners: 1")
}

func TestE2EEventEmitterOverrideOnChains(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
class Bus extends EventEmitter {
  on(event: string, listener: (p: string) => void): Bus {
    console.log("attach:", event);
    return super.on(event, listener);
  }
}
const bus = new Bus();
bus.on("a", (p: string) => console.log("a:", p)).on("b", (p: string) => console.log("b:", p));
bus.emit("a", "1");
bus.emit("b", "2");
`, "attach: a\nattach: b\na: 1\nb: 2")
}

func TestE2EEventEmitterOverrideTwoLevelSuperChain(t *testing.T) {
	// B.emit -> A.emit -> built-in, and virtual dispatch through a base ref.
	assertOutputImports(t, `
import { EventEmitter } from 'events'
class A extends EventEmitter {
  emit(event: string, payload: string): boolean {
    console.log("A.emit");
    return super.emit(event, payload);
  }
}
class B extends A {
  emit(event: string, payload: string): boolean {
    console.log("B.emit");
    return super.emit(event, payload);
  }
}
const b = new B();
b.on("x", (p: string) => console.log("heard:", p));
const a: A = b;
a.emit("x", "via-A-ref");
`, "B.emit\nA.emit\nheard: via-A-ref")
}

func TestE2EEventEmitterGenericExtendsOnNonEventEmitterRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `
import { EventEmitter } from 'events'
class Bar {}
class Baz extends Bar<string> {}
`)
	if err == nil {
		t.Fatal("expected a compile error for generic extends on a non-EventEmitter base")
	}
	if !strings.Contains(err.Error(), "only EventEmitter<T> currently supports generic extends") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EEventEmitterConstructorRejectsArgs(t *testing.T) {
	_, err := parseAndCompileImports(t, `
import { EventEmitter } from 'events'
const e = new EventEmitter(1, 2)
`)
	if err == nil {
		t.Fatal("expected a compile error for new EventEmitter() with arguments")
	}
}

func TestE2EEventEmitterInstanceofWorks(t *testing.T) {
	// TDD-00097 Stage 7 lifted the old "instanceof EventEmitter is a compile
	// error" limitation this test used to assert.
	assertOutputImports(t, `
import { EventEmitter } from 'events'
const e = new EventEmitter()
console.log(e instanceof EventEmitter)
`, "true")
}

// TDD-00097 Stage 7: event-map payload typing + instanceof.

func TestE2EEventEmitterEventMap(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
const em = new EventEmitter<{ data: [s: string]; count: [n: number]; end: []; error: [err: Error] }>();
em.on("data", (s) => { console.log("data:", s.toUpperCase()); });
em.on("count", (n) => { console.log("count:", n * 2); });
em.on("end", () => { console.log("ended"); });
em.emit("data", "hello");
em.emit("count", 21);
em.emit("end");
try {
  em.emit("error", new Error("boom"));
} catch (e) {
  console.log("caught:", (e as Error).message);
}
`, "data: HELLO\ncount: 42\nended\ncaught: boom")
}

func TestE2EEventEmitterEventMapExtends(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
class Ticker extends EventEmitter<{ tick: [n: number]; done: [] }> {
  run(times: number): void {
    for (let i = 1; i <= times; i = i + 1) { this.emit("tick", i); }
    this.emit("done");
  }
}
const t = new Ticker();
t.on("tick", (n) => { console.log("tick", n); });
t.on("done", () => { console.log("done"); });
t.run(2);
`, "tick 1\ntick 2\ndone")
}

func TestE2EEventEmitterEventMapUndeclaredEventRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `
import { EventEmitter } from 'events'
const em = new EventEmitter<{ data: [s: string] }>();
em.emit("nope", "x");
`)
	if err == nil {
		t.Fatal("expected a compile error for an undeclared event-map event")
	}
	if !strings.Contains(err.Error(), "not declared in this EventEmitter's event map") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EEventEmitterInstanceof(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events';
const em = new EventEmitter();
class Sub extends EventEmitter {}
const s = new Sub();
const m = new Map<string, number>();
console.log(em instanceof EventEmitter, s instanceof EventEmitter, m instanceof EventEmitter);
`, "true true false")
}

func TestE2EStreamInstanceof(t *testing.T) {
	assertOutput(t, `
const rs = new ReadableStream<number>({});
const ws = new WritableStream<number>({});
const ts = new TransformStream<number, number>();
console.log(rs instanceof ReadableStream, ws instanceof WritableStream, ts instanceof TransformStream, rs instanceof WritableStream);
`, "true true true false")
}

func TestE2EEventEmitterMultiArgTuple(t *testing.T) {
	// TDD-00131: Node's multi-argument events — a tuple-payload event emits and
	// listens with one argument per element.
	assertOutputImports(t, `
import { EventEmitter } from 'events'
class Bus extends EventEmitter<{ data: [chunk: string, size: number]; done: [] }> {}
const b = new Bus()
b.on("data", (chunk: string, size: number) => {
  console.log(chunk + " " + size)
})
b.on("done", () => { console.log("done") })
b.emit("data", "hello", 5)
b.emit("data", "world", 10)
b.emit("done")
`, "hello 5\nworld 10\ndone")
}

func TestE2EEventEmitterMultiArgSingleTupleOnce(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
const bus = new EventEmitter<{ evt: [name: string, code: number, ok: boolean] }>()
bus.once("evt", (name: string, code: number, ok: boolean) => {
  console.log(name + " " + code + " " + ok)
})
bus.emit("evt", "req", 200, true)
bus.emit("evt", "req2", 500, false)
`, "req 200 true")
}

// A listener may take fewer parameters than the event passes, as in
// TypeScript and Node: the extra arguments are ignored.
func TestE2EEventEmitterMultiArgFewerListenerParams(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter } from 'events'
class Bus extends EventEmitter<{ data: [chunk: string, size: number] }> {}
const b = new Bus()
b.on("data", (chunk: string) => { console.log(chunk) })
b.emit("data", "hello", 5)
`, "hello")
}

// @types/node's default event map: an EventEmitter with no type argument
// takes any arguments per event; every emitter holds its listeners as
// dynamic functions, so removal order, once, 'error', events.once/on and
// array/nullable listener parameters all behave as in Node.
func TestE2EEventEmitterDefaultEventMap(t *testing.T) {
	assertOutputImports(t, `
import { EventEmitter, once, on } from 'events'
const e = new EventEmitter()
const f = (x: number) => console.log("f", x)
const g = (x: number) => console.log("g", x)
e.on("a", f); e.on("a", g); e.on("a", f)
e.emit("a", 1)
e.off("a", f)
e.emit("a", 2)
e.once("b", (...args) => console.log("b", args.length, args[0], args[1]))
console.log(e.emit("b", "x", 3), e.emit("b", "y"))
e.on("n", (a, b) => console.log("n", a, b))
e.emit("n", 1)
e.removeAllListeners("a")
console.log(e.eventNames())
try { e.emit("error", 42) } catch (err) { console.log((err as Error).message) }
const m = new EventEmitter<{ d: [xs: string[], n: number | null] }>()
m.on('d', (xs, n) => console.log(xs.join('+'), n))
m.emit('d', ['p', 'q'], null)
async function main(): Promise<void> {
  setTimeout(() => e.emit('ready', 'r', 2), 0)
  const args = await once(e, 'ready')
  console.log(args.length, args[0], args[1])
  setTimeout(() => { e.emit('tick', 1); e.emit('tick', 2, 'x') }, 0)
  let k = 0
  for await (const ev of on(e, 'tick')) { console.log(ev.length, ev[0]); if (++k >= 2) break }
}
main()
`, "f 1\ng 1\nf 1\nf 2\ng 2\nb 2 x 3\ntrue false\nn 1 undefined\n[ 'n' ]\nUnhandled error. (42)\np+q null\n2 r 2\n1 1\n2 2")
}
