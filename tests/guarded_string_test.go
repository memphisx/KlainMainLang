package tests

import "testing"

// A `string | undefined` local read past a guard excluding undefined is the
// string, whatever was last stored (an index read that may miss included);
// a read past no guard keeps undefined.
func TestE2EGuardedStringSinceStore(t *testing.T) {
	got := compileAndRun(t, `
const m: { [k: string]: string } = { a: 'queryA' };
function f(k: string, n: number): string {
  let b: string | undefined;
  if (n > 0) b = m[k]; else throw new Error('x');
  if (b !== undefined) { return b + '!'; }
  if (!b) return 'none:' + b;
  return 'unreached';
}
function g(k: string): string {
  let b: string | undefined = m[k];
  if (typeof b === 'string') return b.toUpperCase();
  b = m['zz'];
  return 'miss:' + b;
}
console.log(f('a', 1), f('q', 1), g('a'), g('q'));
`)
	if want := "queryA! none:undefined QUERYA miss:undefined"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
