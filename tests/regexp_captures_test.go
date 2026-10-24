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

// A repeated non-capturing group clears its captures each iteration: a
// capture from an earlier iteration is undefined after the match.
func TestE2ERegexRepeatedGroupClearsCaptures(t *testing.T) {
	assertSameAsNode(t, `
export {}
console.log(JSON.stringify(/(?:(a)|b)*/.exec("ab")))
console.log(JSON.stringify(/(z)((a+)?(b+)?(c))*/.exec("zaacbbbcac")))
console.log(JSON.stringify(/(?:(a)|(b))+/.exec("aba")))
console.log(JSON.stringify("abab".match(/((a)|(b))+/)))
const j = (x: unknown) => console.log(JSON.stringify(x))
j("ab".replace(/(?:(a)|b)*/, "[$1]"))
j([..."abxab".matchAll(/(?:(a)|b)+/g)].map(m => [m[0], m[1]]))
j("1a2b3".split(/(?:(a)|b)/))
j(/(?:(?<x>a)|b)*/.exec("ab")!.groups)
j(/(?:(?:(a)|b)c)*/.exec("acbc"))
j(/(?:(a)|b)*?c/.exec("abc"))
j(/(?:(a)|b){2,3}/.exec("abb"))
j(/(?:(a)|(b))*/.exec("ba"))
const g = /(?:(x)|y)+/g
j(g.exec("xy xy"))
j(g.exec("xy xy"))
j(/(?:(a)|b)*/.exec("ba"))
j(/(?:(a)b)*/.exec("abab"))
j(/(?:(a)|ab)*c/.exec("aabc"))
`)
}
