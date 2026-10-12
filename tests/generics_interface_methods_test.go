package tests

import (
	"fmt"
	"strings"
	"testing"
)

// ADR-01403: a generic interface whose methods return another of its own
// instantiations (`then<U>(…): Deferred<U>`, three mentions per method)
// is instantiated once per type arguments, not rebuilt at every mention:
// chained calls on it compiled in seconds per line before. Method chains
// keep their members, and a method named like an array built-in (`map`) is
// the object's own.
func TestE2EGenericInterfaceSelfReturningMethods(t *testing.T) {
	var chains strings.Builder
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&chains, "  const a%d = r1.then(tf, tf, tf).then(tf, tf, tf);\n", i)
		fmt.Fprintf(&chains, "  const b%d = s1.then(tfD, tf, tf).then(tf, tf, tf);\n", i)
	}
	src := `
interface Box<T> {
  v: T;
  map<U>(f: (x: T) => U): Box<U>;
}
function box<T>(v: T): Box<T> {
  return { v: v, map: <U>(f: (x: T) => U): Box<U> => box(f(v)) };
}
interface Deferred<T> {
  then<U>(ok?: (value: T) => Deferred<U>, err?: (e: any) => Deferred<U>, prog?: (p: any) => void): Deferred<U>;
  then<U>(ok?: (value: T) => Deferred<U>, err?: (e: any) => U, prog?: (p: any) => void): Deferred<U>;
  then<U>(ok?: (value: T) => U, err?: (e: any) => Deferred<U>, prog?: (p: any) => void): Deferred<U>;
  then<U>(ok?: (value: T) => U, err?: (e: any) => U, prog?: (p: any) => void): Deferred<U>;
  done<U>(ok?: (value: T) => any, err?: (e: any) => any, prog?: (p: any) => void): void;
}
interface Thenable<T> {
  then<U>(ok?: (value: T) => U, err?: (e: any) => U, prog?: (p: any) => void): Thenable<U>;
}
declare function tf(): Thenable<number>;
declare function tfD(): Deferred<number>;
function chained(r1: Thenable<number>, s1: Deferred<number>): void {
` + chains.String() + `}
const b: Box<number> = box(2);
const c = b.map((x) => x + 1);
console.log(c.v, c.map((x) => x * 10).v);
console.log(b.map((x) => x + 1).map((x) => x * 10).map((x) => x - 1).v);
console.log(typeof chained);
`
	assertOutput(t, src, `3 30
29
function`)
}
