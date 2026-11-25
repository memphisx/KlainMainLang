package tests

import "testing"

// exec() results carry index, input and groups (a null-prototype dictionary
// of the named groups, undefined without any), as in Node.
func TestE2ERegExpExecIndexInputGroups(t *testing.T) {
	assertSameAsNode(t, `
const re = /(?<y>\d+)-(\d+)(?<opt>x)?/;
const m = re.exec("a 2024-07 z");
console.log(m);
if (m) {
  console.log(m.index, m.input, m.groups, m[1], m.length);
  console.log(m.groups?.y, m.groups?.opt);
  const [all, y] = m;
  console.log(all, y);
}
const m2 = /b/.exec("ab");
console.log(m2, m2?.index);
console.log(/z/.exec("ab"));
const g = /o/g; let r: RegExpExecArray | null;
while ((r = g.exec("foo boo")) !== null) console.log(r.index, g.lastIndex);
function first(x: RegExpExecArray): number { return x.index; }
const m3 = /c/.exec("abc"); if (m3) console.log(first(m3));
console.log(JSON.stringify(/(a)/.exec("xa")));
`)
}

// Optional access on a dictionary that may be undefined.
func TestE2EDictionaryOptionalAccess(t *testing.T) {
	assertSameAsNode(t, `
const m = /(?<y>\d+)/.exec("a1");
if (m) { console.log(m.groups!.y); const gg = m.groups!; console.log(gg.y, gg["y"]); }
const d: Record<string, string> | undefined = { a: "x" };
console.log(d?.a);
`)
}

// str.match() with a non-global regex literal is exec, index and all.
func TestE2EStringMatchLiteralExecExtras(t *testing.T) {
	assertSameAsNode(t, `
const m = "x 2024-07".match(/(?<y>\d+)-(\d+)/);
console.log(m, m?.index);
if (m) console.log(m.groups?.y, m.index, m.input);
console.log("aXbX".match(/X/g), "q".match(/z/));
`)
}

// The d flag's indices: each group's [start, end] (undefined unmatched),
// their named groups, through exec, match, matchAll, a boxed receiver and
// console.log.
func TestE2ERegExpHasIndices(t *testing.T) {
	assertSameAsNode(t, `
const m = /(?<y>\d{4})-(\d{2})?x?/d.exec("on 2026-11 ok")!;
console.log(m.indices![0], m.indices![1], m.indices![2], m.indices!.groups);
const n = /a(b)?/d.exec("xa")!;
console.log(n.indices![1], n.indices!.length);
console.log(/a/d.hasIndices, /a/.hasIndices, /a/dg.flags, /a/s.dotAll);
for (const r of "a1b2".matchAll(/[a-z](\d)/dg)) console.log(r.indices![1]);
console.log(/(?<q>x)/d.exec("ax"));
console.log("zz".match(/z/d)!.indices, /z/.exec("z")!.indices);
const anyr: any = /b(c)/d;
console.log(anyr.exec("abc").indices[1]);
console.log(/(é+)(b)/d.exec("ααéébx")!.indices![2]);
`)
}
