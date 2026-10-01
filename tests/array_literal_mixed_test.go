package tests

import "testing"

// A literal mixing a string with two object shapes holds boxed elements:
// each keeps its own kind (every element used to read as the first's type).
func TestE2EArrayLiteralStringAndObjectShapes(t *testing.T) {
	assertSameAsNode(t, `
function f(key: any): string {
    if (typeof key !== 'object') return 'notobj ' + typeof key;
    if (key.passphrase === undefined) return 'undef ' + key.key;
    return 'has ' + String(key.passphrase);
}
const key = 'PEM';
for (const k of [key, { key }, { key, passphrase: 'bad' }]) console.log(f(k as any));
const xs = [1, 'two', { three: 3 }];
console.log(xs.length, typeof xs[0], typeof xs[1], typeof xs[2]);
`)
}
