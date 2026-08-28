package tests

import (
	"strings"
	"testing"
)

// --- EventEmitter<T> (TDD-00023) ---

func TestE2EEventEmitterBasicOnEmitString(t *testing.T) {
	assertOutput(t, `
const e = new EventEmitter<string>()
e.on('msg', (data: string): void => {
  console.log('got: ' + data)
})
const result = e.emit('msg', 'hello')
console.log(result)
`, "got: hello\ntrue")
}

func TestE2EEventEmitterBasicOnEmitNumber(t *testing.T) {
	assertOutput(t, `
const e = new EventEmitter<number>()
e.on('tick', (n: number): void => {
  console.log(n * 2)
})
e.emit('tick', 21)
`, "42")
}

func TestE2EEventEmitterMultipleListenersOrder(t *testing.T) {
	assertOutput(t, `
const e = new EventEmitter<string>()
e.on('x', (data: string): void => { console.log('first: ' + data) })
e.on('x', (data: string): void => { console.log('second: ' + data) })
e.emit('x', 'go')
`, "first: go\nsecond: go")
}

func TestE2EEventEmitterEmitReturnsFalseWhenUnlistened(t *testing.T) {
	assertOutput(t, `
const e = new EventEmitter<string>()
e.on('a', (data: string): void => { console.log(data) })
console.log(e.emit('a', 'hi'))
console.log(e.emit('b', 'nope'))
`, "hi\ntrue\nfalse")
}

func TestE2EEventEmitterOnce(t *testing.T) {
	assertOutput(t, `
const e = new EventEmitter<string>()
e.once('x', (data: string): void => { console.log('once: ' + data) })
console.log(e.listenerCount('x'))
e.emit('x', 'a')
console.log(e.listenerCount('x'))
e.emit('x', 'b')
`, "1\nonce: a\n0")
}

func TestE2EEventEmitterOffRemovesOneListener(t *testing.T) {
	assertOutput(t, `
const e = new EventEmitter<string>()
const listener1 = (data: string): void => { console.log('one: ' + data) }
const listener2 = (data: string): void => { console.log('two: ' + data) }
e.on('x', listener1)
e.on('x', listener2)
e.off('x', listener1)
e.emit('x', 'go')
console.log(e.listenerCount('x'))
`, "two: go\n1")
}

func TestE2EEventEmitterRemoveListenerAlias(t *testing.T) {
	assertOutput(t, `
const e = new EventEmitter<string>()
const listener1 = (data: string): void => { console.log(data) }
e.on('x', listener1)
e.removeListener('x', listener1)
console.log(e.listenerCount('x'))
`, "0")
}

func TestE2EEventEmitterRemoveAllListenersOneEvent(t *testing.T) {
	assertOutput(t, `
const e = new EventEmitter<string>()
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
	assertOutput(t, `
const e = new EventEmitter<string>()
e.on('a', (data: string): void => { console.log('a: ' + data) })
e.on('b', (data: string): void => { console.log('b: ' + data) })
e.removeAllListeners()
console.log(e.emit('a', 'x'))
console.log(e.emit('b', 'y'))
`, "false\nfalse")
}

func TestE2EEventEmitterListenerCountAndEventNames(t *testing.T) {
	assertOutput(t, `
const e = new EventEmitter<string>()
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
	assertOutput(t, `
const e = new EventEmitter<string>()
e.on('a', (data: string): void => { console.log('a: ' + data) }).on('b', (data: string): void => { console.log('b: ' + data) })
e.emit('a', 'x')
e.emit('b', 'y')
`, "a: x\nb: y")
}

func TestE2EEventEmitterErrorEventThrowsWhenUnlistened(t *testing.T) {
	assertOutput(t, `
const e = new EventEmitter<string>()
try {
  e.emit('error', 'boom')
} catch (err) {
  console.log('caught: ' + err.message)
}
`, "caught: boom")
}

func TestE2EEventEmitterErrorEventDoesNotThrowWhenListened(t *testing.T) {
	assertOutput(t, `
const e = new EventEmitter<string>()
e.on('error', (msg: string): void => { console.log('handled: ' + msg) })
e.emit('error', 'boom')
console.log('after')
`, "handled: boom\nafter")
}

func TestE2EEventEmitterErrorPayloadRethrowsExactError(t *testing.T) {
	assertOutput(t, `
const e = new EventEmitter<Error>()
try {
  e.emit('error', new Error('bad thing'))
} catch (err) {
  console.log(err.message)
  console.log(err instanceof Error)
}
`, "bad thing\ntrue")
}

func TestE2EEventEmitterErrorPayloadListenedWithUntypedListener(t *testing.T) {
	assertOutput(t, `
const e = new EventEmitter<Error>()
e.on('error', (err) => { console.log('handled: ' + err.message) })
e.emit('error', new Error('bad thing'))
console.log('after')
`, "handled: bad thing\nafter")
}

// --- class X extends EventEmitter<T> ---

func TestE2EEventEmitterClassExtendsBasic(t *testing.T) {
	assertOutput(t, `
class Downloader extends EventEmitter<string> {
  name: string;
  constructor(name: string) {
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
	assertOutput(t, `
class Counter extends EventEmitter<number> {
  count: number;
  constructor() {
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
	assertOutput(t, `
class Base extends EventEmitter<number> {
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
	assertOutput(t, `
class Shape extends EventEmitter<string> {
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

func TestE2EEventEmitterReservedMethodNameRejected(t *testing.T) {
	_, err := parseAndCompile(`
class Foo extends EventEmitter<string> {
  on(): void {}
}
`)
	if err == nil {
		t.Fatal("expected a compile error for a method name reserved by EventEmitter")
	}
	if !strings.Contains(err.Error(), "reserved by EventEmitter") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EEventEmitterGenericExtendsOnNonEventEmitterRejected(t *testing.T) {
	_, err := parseAndCompile(`
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
	_, err := parseAndCompile(`
const e = new EventEmitter<string>(1, 2)
`)
	if err == nil {
		t.Fatal("expected a compile error for new EventEmitter() with arguments")
	}
}

func TestE2EEventEmitterListenerWrongArityRejected(t *testing.T) {
	_, err := parseAndCompile(`
const e = new EventEmitter<string>()
e.on('x', (): void => {})
`)
	if err == nil {
		t.Fatal("expected a compile error for a zero-arg listener")
	}
	if !strings.Contains(err.Error(), "must take exactly 1 argument") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EEventEmitterInstanceofWorks(t *testing.T) {
	// TDD-00097 Stage 7 lifted the old "instanceof EventEmitter is a compile
	// error" limitation this test used to assert.
	assertOutput(t, `
const e = new EventEmitter<string>()
console.log(e instanceof EventEmitter)
`, "true")
}

// TDD-00097 Stage 7: event-map payload typing + instanceof.

func TestE2EEventEmitterEventMap(t *testing.T) {
	assertOutput(t, `
const em = new EventEmitter<{ data: string; count: number; end: void; error: Error }>();
em.on("data", (s) => { console.log("data:", s.toUpperCase()); });
em.on("count", (n) => { console.log("count:", n * 2); });
em.on("end", () => { console.log("ended"); });
em.emit("data", "hello");
em.emit("count", 21);
em.emit("end");
try {
  em.emit("error", new Error("boom"));
} catch (e) {
  console.log("caught:", e.message);
}
`, "data: HELLO\ncount: 42\nended\ncaught: boom")
}

func TestE2EEventEmitterEventMapExtends(t *testing.T) {
	assertOutput(t, `
class Ticker extends EventEmitter<{ tick: number; done: void }> {
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
	_, err := parseAndCompile(`
const em = new EventEmitter<{ data: string }>();
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
	assertOutput(t, `
const em = new EventEmitter<number>();
class Sub extends EventEmitter<string> {}
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
	assertOutput(t, `
class Bus extends EventEmitter<{ data: [string, number]; done: void }> {}
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
	assertOutput(t, `
const bus = new EventEmitter<[string, number, boolean]>()
bus.once("evt", (name: string, code: number, ok: boolean) => {
  console.log(name + " " + code + " " + ok)
})
bus.emit("evt", "req", 200, true)
bus.emit("evt", "req2", 500, false)
`, "req 200 true")
}

func TestE2EEventEmitterMultiArgWrongArity(t *testing.T) {
	_, err := parseAndCompile(`
class Bus extends EventEmitter<{ data: [string, number] }> {}
const b = new Bus()
b.on("data", (chunk: string) => { console.log(chunk) })
`)
	if err == nil {
		t.Fatal("expected a compile error for a listener arity not matching the tuple payload, got none")
	}
}
