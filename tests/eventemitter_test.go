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

func TestE2EEventEmitterInstanceofRejected(t *testing.T) {
	_, err := parseAndCompile(`
const e = new EventEmitter<string>()
console.log(e instanceof EventEmitter)
`)
	if err == nil {
		t.Fatal("expected a compile error for instanceof EventEmitter")
	}
	if !strings.Contains(err.Error(), "not a registered class") {
		t.Errorf("unexpected error message: %v", err)
	}
}
