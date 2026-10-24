package tests

import "testing"

func TestE2ERegExpEmptyOptionalAndVFlag1(t *testing.T) {
	assertSameAsNode(t, `console.log(JSON.stringify(/^(.*)(.*)?$/.exec("ab")));
console.log(JSON.stringify(/(a)|b/.exec("b")));
console.log(/[\p{L}--[a-z]]/v.test("A"), /[\p{L}--[a-z]]/v.test("a"));
console.log(/[[a-z]&&[aeiou]]/v.test("e"), /[[a-z]&&[aeiou]]/v.test("b"));
console.log(/[\q{abc|d}]/v.test("abc"));
try { new RegExp("[a-z--]", "v"); console.log("accepted"); } catch (e: any) { console.log(e.constructor.name); }
try { new RegExp("[(]", "v"); console.log("accepted"); } catch (e: any) { console.log(e.constructor.name); }
console.log("aaa".replace(/(a)?/g, "[$1]"));
console.log(JSON.stringify(/(z)?b/.exec("b")), JSON.stringify("b".match(/(z)*b/)));
`)
}

func TestE2ERegExpEmptyOptionalAndVFlag2(t *testing.T) {
	assertSameAsNode(t, `const show = (...xs: any[]) => console.log(xs.map(x => JSON.stringify(x)).join(" "));
show(/((x)*)?y/.exec("y"));
show(/(?<n>.*)?$/.exec("")?.groups);
show(/(?:(a)|b)?c/.exec("c"));
show("a,b".split(/(,)?/));
show("xyz".replace(/(q)?y/, "[$1]"));
show([..."a1b2".matchAll(/([a-z])(\d)?/g)].map(m => [...m]));
show(/((a)?)?b/.exec("b"));
show(/(a?)?b/.exec("b"), /(a?)?b/.exec("ab"));
show(/[\p{Lu}&&\p{Script=Greek}]/v.test("Σ"), /[\p{Lu}&&\p{Script=Greek}]/v.test("A"));
show(/[[a-z]--[aeiou]--[xyz]]/v.test("b"), /[[a-z]--[aeiou]--[xyz]]/v.test("y"));
show("abcd".match(/[\q{abc|ab}d]/gv));
show(/[^\d\s]/v.test("x"), /[^[0-9]--[5]]/v.test("5"), /[^[0-9]--[5]]/v.test("4"));
show(/^[\w&&\d]+$/v.test("123"), /[a-c\-x]/v.test("-"));
for (const p of ["[a-z--]", "[(]", "[a&&&b]", "[a--b&&c]", "[ab--c]", "[a-z&&b]", "[a||b]", "[]]"]) {
  try { new RegExp(p, "v"); console.log(p, "ok"); } catch (e: any) { console.log(p, e.message); }
}
`)
}

func TestE2ERegExpUnicodeLoneBrackets(t *testing.T) {
	assertSameAsNode(t, `for (const p of ["]", "}", "a{"]) { try { new RegExp(p, "u"); console.log(p, "ok"); } catch (e: any) { console.log(p, e.message); } }
`)
}

func TestE2EURLPatternVFlagGroups(t *testing.T) {
	assertSameAsNode(t, `const show = (p: any, i: any) => { try { const r = new URLPattern(p).exec(i); console.log(JSON.stringify(r ? r.pathname : null)); } catch (e: any) { console.log(e.constructor.name); } };
show({ pathname: "/([[a-z]--a])" }, { pathname: "/a" });
show({ pathname: "/([[a-z]--a])" }, { pathname: "/z" });
show({ pathname: "/([\\d&&[0-1]])" }, { pathname: "/0" });
show({ pathname: "/([\\d&&[0-1]])" }, { pathname: "/3" });
show({ pathname: "/(a)?(b)" }, { pathname: "/b" });
show({ pathname: "/([a-z--])" }, { pathname: "/a" });
show({ pathname: "/foo/(.*)?" }, { pathname: "/foo/" });
`)
}

func TestE2EInterfaceOverNativeLayout1(t *testing.T) {
	assertSameAsNode(t, `interface Coded extends RangeError { code: string; info: { n: number }; retries?: number }
function mk(): Coded {
  const e = new RangeError("r") as Coded;
  e.code = "ERR_R"; e.info = { n: 3 };
  return e;
}
const c = mk();
console.log(c.code, c.info.n, c.retries, c.name, c instanceof RangeError);
c.retries = 2;
console.log(c.retries, Object.keys(c));
interface Stamp extends Date { label: string }
const d = new Date(0) as Stamp; d.label = "epoch";
console.log(d.getTime(), d.label);
`)
}

func TestE2EInterfaceOverNativeLayout2(t *testing.T) {
	assertSameAsNode(t, `interface NodeTypeError extends TypeError { code: string }
function nodeTypeError(code: string, message: string): NodeTypeError {
  const e = new TypeError(message) as NodeTypeError;
  e.code = code;
  return e;
}
const e = nodeTypeError("ERR_X", "bad thing");
console.log(e.constructor.name, e.name, e.code, e.message, e instanceof TypeError);
try { throw nodeTypeError("ERR_Y", "boom"); } catch (x: any) { console.log(x.code, String(x), x instanceof TypeError); }
const f = (): NodeTypeError => nodeTypeError("ERR_Z", "z");
console.log(f().code, Object.keys(f()));
`)
}

func TestE2ENodeCodedErrorConstructor(t *testing.T) {
	assertSameAsNode(t, `try { new URL("bad"); } catch (e: any) { console.log(e.constructor.name, e.name); console.log(String(e)); }
try { new URLPattern({ pathname: "/([a-z--])" }); } catch (e: any) { console.log(e.constructor.name, e.name, e.code); }
`)
}

func TestE2ETypeAndValueSameName(t *testing.T) {
	assertSameAsNode(t, `interface Point { x: number; y: number }
function Point(x: number, y: number): Point { return { x, y }; }
const p: Point = Point(1, 2);
console.log(p.x + p.y);
type Pair = [number, number];
const Pair = (a: number, b: number): Pair => [a, b];
console.log(Pair(3, 4));
`)
}

// A pattern syntax error carries V8's message.
func TestE2ERegExpSyntaxMessages(t *testing.T) {
	assertSameAsNode(t, `export {}
const cases: [string, string][] = [["{1}", "u"], ["*", ""], ["a**", ""], ["(", ""], [")", ""], ["[b-a]", ""], ["\\", ""],
  ["(?<a>x)(?<a>y)", ""], ["\\k<z>", "u"], ["(?<1>x)", ""], ["a{2,1}", ""], ["\\p{Foo}", "u"], ["(?", ""],
  ["(?<=a)+", ""], ["(?=a)+", ""], ["(?=a)+", "u"], ["[a", ""], ["\\p{L}+", "u"]]
for (const [p, f] of cases) {
  try { new RegExp(p, f); console.log("ok", p) } catch (e: any) { console.log(e.name, e.message) }
}
`)
}

// A replacement template expands every GetSubstitution form; a callback
// takes the captures, with a literal or a constructed RegExp.
func TestE2ERegExpReplacementForms(t *testing.T) {
	assertSameAsNode(t, `export {}
const s = "abc-def-ghi"
console.log(s.replace(/def/, "[$`+"`"+`|$'|$&|$$]"), s.replace(/(\w)(\w)/g, "$2$1"), s.replaceAll(/-/g, "<$`+"`"+`>"))
console.log("x1y".replace(/(\d)/, "$01$11$0$1$9"), "aXb".replace(/(a)(X)?(?:Q)?b/, "[$2|$3|$1]"))
console.log("abcdefghijk".replace(/(a)(b)(c)(d)(e)(f)(g)(h)(i)(j)(k)/, "$11-$10-$1$"))
const re = /([a-z])(\d)/g
console.log("a1b2".replace(re, (m: string, l: string, d: string, off: number, str: string) => d + l + "@" + off + "/" + str.length))
const re2 = new RegExp("(x)(y)?", "g")
console.log("xyx".replace(re2, (m: string, a: string, b: string, off: number) => "[" + a + "," + b + "," + off + "]"))
`)
}

// A match result keeps index, input and groups through a RegExpMatchArray
// typed return, whatever RegExp the function receives.
func TestE2ERegExpMatchThroughParam(t *testing.T) {
	assertSameAsNode(t, `export {}
function f(r: RegExp) { return "xyz".match(r) }
console.log(f(/y/), f(/y|z/g), f(/q/))
function g(r: RegExp): RegExpMatchArray | null { return "aXbX".match(r) }
console.log(g(/X/g), g(/(?<k>X)/), g(/X/)?.index)
console.log("source" in /x/g, typeof /x/)
`)
}

// An iteration of a repeated group that matches empty once the minimum is
// met fails, and a capture inside a repeated group from an earlier
// iteration is cleared, as ECMAScript's RepeatMatcher does.
func TestE2ERegExpEmptyIterations(t *testing.T) {
	assertSameAsNode(t, `export {}
console.log(/(a|)*/.exec("aa"), /(a|)+/.exec("aa"), /(a|)?/.exec(""), /(a*)*/.exec("b"))
console.log(/(?<x>a?)*b/.exec("aab")?.groups?.x, /(a|b|)*c/.exec("abc"), /(z)((a+)?(b+)?(c))*/.exec("zaacbbbcac"))
console.log("aa".replace(/(a|)*/, "[$1]"), "xaay".match(/(a|)*y/), /(\d{0,3})*-/.exec("123456-"))
console.log(/(a|)*?b/.exec("aab"), /([^,]*)*/.exec("a,b"), /(a)*/.exec("aaa"))
console.log(/(a|)+/.exec(""), /(a|){1,}b/.exec("aab"), /((a)|b)+/.exec("ab"), /((a)|(b))*/.exec("ab"))
console.log("x".replace(/(a|)+/g, "[$1]"), /(b|)+a/.exec("bba"), /(\w*?)+;/.exec("ab;"))
`)
}
