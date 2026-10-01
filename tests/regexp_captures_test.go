package tests

import "testing"

// A RegExp separator's capture groups are spliced into split()'s result,
// and an unmatched group is undefined wherever it surfaces.
func TestE2ERegExpSplitCapturesAndUnmatchedGroups(t *testing.T) {
	assertSameAsNode(t, `
console.log('a1b2c'.split(/(\d)/), 'a1b'.split(/(\d)(x)?/), 'abc'.split(/(?:)/), 'a,b'.split(/(,)/, 2), ''.split(/(x)/), 'x'.split(/(x)/));
console.log('aXbYc'.split(/([XY])/).join('|'), 'ab'.split(/(?:)/u));
const m = /(a)(b)?/.exec('xac')!;
console.log(m[2], m[2] === undefined, m.length, m[2] ?? 'dflt', typeof m[2], m.slice(1), m.join('-'), JSON.stringify([...m]));
console.log('ac'.replace(/(a)(b)?/, (s, p1, p2) => '[' + p1 + '|' + p2 + ']'), 'ac'.replace(/(a)(b)?/, '<$1$2>'), 'ac'.replace(/(a)(b)?/g, '[$2]'));
const x: (string | null)[] = ['x', null];
console.log(x.join('-'), [{}, null].join('|'));
`)
}

// A `string | RegExp` (or `any`) separator splits by what it holds.
func TestE2ESplitUnionSeparator(t *testing.T) {
	assertSameAsNode(t, `
function sp(s: string, sep: string | RegExp) { return s.split(sep); }
console.log(sp('a1b2c', /\d/), sp('a-b', '-'));
const seps: (string | RegExp)[] = [',', /[;|]/];
console.log(seps.map(s => 'a,b;c|d'.split(s)), 'a1b'.split(/(\d)/ as any), 'x y'.split(' ' as any), 'q'.split(5 as any));
const s: any = 'a1b2'; const r: any = /\d/;
console.log(s.split(r), s.split(r, 1));
`)
}
