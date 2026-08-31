package tests

import (
	"testing"
)

// --- RegExp (see docs/tdd/TDD-00035.md) — Stage 0: construction, literal
// syntax, field reads, and catchable compile errors. Stage 1: .test(). ---

func TestE2ERegExpConstructorFields(t *testing.T) {
	assertOutput(t, `
const r = new RegExp("a+b", "gim")
console.log(r.source)
console.log(r.flags)
console.log(r.global)
console.log(r.ignoreCase)
console.log(r.multiline)
console.log(r.dotAll)
console.log(r.lastIndex)
`, "a+b\ngim\ntrue\ntrue\ntrue\nfalse\n0")
}

func TestE2ERegExpConstructorNoFlags(t *testing.T) {
	assertOutput(t, `
const r = new RegExp("abc")
console.log(r.source)
console.log(r.flags)
console.log(r.global)
console.log(r.ignoreCase)
console.log(r.multiline)
console.log(r.dotAll)
`, "abc\n\nfalse\nfalse\nfalse\nfalse")
}

func TestE2ERegExpDotAllFlag(t *testing.T) {
	assertOutput(t, `
const r = new RegExp("a.b", "s")
console.log(r.dotAll)
console.log(r.global)
`, "true\nfalse")
}

func TestE2ERegExpLiteralSyntax(t *testing.T) {
	assertOutput(t, `
const lit = /hello[0-9]+/i
console.log(lit.source)
console.log(lit.flags)
console.log(lit.ignoreCase)
`, "hello[0-9]+\ni\ntrue")
}

func TestE2ERegExpLiteralEscapedSlash(t *testing.T) {
	assertOutput(t, `
const lit = /a\/b/
console.log(lit.source)
`, `a\/b`)
}

func TestE2ERegExpLiteralClassContainingSlash(t *testing.T) {
	// An unescaped '/' inside a [...] character class must not terminate
	// the literal — see readRegex's inClass tracking.
	assertOutput(t, `
const lit = /[a/b]/
console.log(lit.source)
`, "[a/b]")
}

func TestE2ERegExpDoesNotBreakDivision(t *testing.T) {
	// The lexer's regex-vs-division heuristic (lastSig) must still let a
	// plain division expression through unchanged.
	assertOutput(t, `
const a = 10
const b = 2
console.log(a / b)
console.log(a / b / 1)
`, "5\n5")
}

func TestE2ERegExpInvalidPatternThrowsSyntaxError(t *testing.T) {
	assertOutput(t, `
try {
  const r = new RegExp("(unterminated", "")
  console.log("no throw")
} catch (e) {
  console.log(e.name)
}
`, "SyntaxError")
}

func TestE2ERegExpInvalidCharacterClassThrows(t *testing.T) {
	assertOutput(t, `
try {
  const r = new RegExp("[a-", "")
  console.log("no throw")
} catch (e) {
  console.log(e.name)
}
`, "SyntaxError")
}

func TestE2ERegExpUnknownFlagLetterIsPermissive(t *testing.T) {
	// Unrecognized flag letters are silently ignored (permissive, matching
	// atob/decodeURI's existing "malformed input" convention) rather than
	// rejected — 'z' isn't a real JS regex flag at all.
	assertOutput(t, `
const r = new RegExp("abc", "z")
console.log(r.flags)
console.log(r.global)
`, "z\nfalse")
}

// --- Stage 1: .test(str): boolean ---

func TestE2ERegExpTestMatchAndNoMatch(t *testing.T) {
	assertOutput(t, `
const r = new RegExp("[0-9]+", "")
console.log(r.test("abc123"))
console.log(r.test("abcdef"))
`, "true\nfalse")
}

func TestE2ERegExpTestCaseInsensitiveAndAnchored(t *testing.T) {
	assertOutput(t, `
const r = /^hello/i
console.log(r.test("Hello world"))
console.log(r.test("say hello"))
`, "true\nfalse")
}

func TestE2ERegExpTestUnannotatedConstInfersBoolean(t *testing.T) {
	// Exercises emit_exprs_vardecl.go's MemberExpression-callee inference
	// fallback: an unannotated `const` holding a `.test()` result must
	// infer real boolean (i1) storage, not silently default to i64 — the
	// same class of gap ADR-00112/ADR-00113 already found and fixed for
	// other member-call return types.
	assertOutput(t, `
const r = /[a-z]+/
const matched = r.test("hello")
if (matched) {
  console.log("matched")
} else {
  console.log("no match")
}
console.log(matched)
`, "matched\ntrue")
}

func TestE2ERegExpTestRepeatedCallsDoNotLeakOrCorrupt(t *testing.T) {
	// Each .test() call creates and frees its own match_data (see
	// ensureRegexMatch's doc comment) — a loop calling .test() many times
	// on the same instance must keep working correctly, not just once.
	assertOutput(t, `
const r = /\d+/
let count = 0
const inputs: string[] = ["a1", "bb", "c3", "dd", "e5"]
for (const s of inputs) {
  if (r.test(s)) {
    count = count + 1
  }
}
console.log(count)
`, "3")
}

// TestE2ERegExpTestGlobalAdvancesLastIndex confirms a global regex's
// .test() shares .exec()'s lastIndex-driven iteration — a `while (re.test(s))`
// loop steps through every match and terminates, advancing lastIndex to each
// match's end, and resetting to 0 on the final no-match.
func TestE2ERegExpTestGlobalAdvancesLastIndex(t *testing.T) {
	assertOutput(t, `
const re = /\d+/g
const s = "a12b345c"
let count = 0
const ends: number[] = []
while (re.test(s)) {
  count = count + 1
  ends.push(re.lastIndex)
}
console.log(count)
console.log(ends.join(","))
console.log(re.lastIndex)
`, "2\n3,7\n0")
}

// TestE2ERegExpTestNonGlobalIgnoresLastIndex confirms a non-global regex's
// .test() never reads or writes lastIndex — it always matches from offset 0
// and returns the same result every call.
func TestE2ERegExpTestNonGlobalIgnoresLastIndex(t *testing.T) {
	assertOutput(t, `
const re = /\d+/
const s = "a12b345c"
console.log(re.test(s))
console.log(re.test(s))
console.log(re.lastIndex)
`, "true\ntrue\n0")
}

// --- Stage 2: .exec(str): string[] | null, and the general array-vs-null
// / array-truthiness fixes it needed (see ADR-00116). ---

func TestE2ERegExpExecMatchWithCaptureGroups(t *testing.T) {
	assertOutput(t, `
const r = /(\d+)-(\d+)/
const m = r.exec("range: 12-34 end")
console.log(m !== null)
console.log(m.length)
console.log(m[0])
console.log(m[1])
console.log(m[2])
`, "true\n3\n12-34\n12\n34")
}

func TestE2ERegExpExecNoMatchReturnsNull(t *testing.T) {
	assertOutput(t, `
const r = /\d+/
const m = r.exec("no digits here")
if (m === null) {
  console.log("null as expected")
} else {
  console.log("unexpected match")
}
`, "null as expected")
}

func TestE2ERegExpExecUnmatchedOptionalGroupIsEmptyString(t *testing.T) {
	assertOutput(t, `
const r = /a(b)?c/
const m = r.exec("ac")
console.log(m[0])
console.log(m[1])
`, "ac\n")
}

func TestE2ERegExpExecGlobalFlagAdvancesLastIndex(t *testing.T) {
	// The canonical while ((m = re.exec(s)) !== null) iteration idiom.
	assertOutput(t, `
const r = /\d+/g
let count = 0
let m = r.exec("a1 b22 c333")
while (m !== null) {
  count = count + 1
  console.log(m[0])
  m = r.exec("a1 b22 c333")
}
console.log(count)
console.log(r.lastIndex)
`, "1\n22\n333\n3\n0")
}

func TestE2ERegExpExecNonGlobalNeverTouchesLastIndex(t *testing.T) {
	assertOutput(t, `
const r = /a/
r.exec("banana")
console.log(r.lastIndex)
r.exec("banana")
console.log(r.lastIndex)
`, "0\n0")
}

func TestE2ERegExpExecGlobalLastIndexResetsAfterFailedMatch(t *testing.T) {
	assertOutput(t, `
const r = /z/g
r.exec("zzz")
console.log(r.lastIndex)
const m = r.exec("no z here... wait yes: z")
console.log(r.lastIndex)
const m2 = r.exec("nope")
console.log(m2 === null)
console.log(r.lastIndex)
`, "1\n4\ntrue\n0")
}

// --- General fixes found while wiring .exec()'s T[] | null (ADR-00116) ---

func TestE2EBareStringTruthinessCheck(t *testing.T) {
	// Previously emitted invalid IR ("icmp ne ptr %v, 0" instead of
	// "null") for ANY bare ptr-typed truthiness check, not just arrays —
	// a hard compile failure, found while wiring this stage.
	assertOutput(t, `
const s = "hello"
if (s) {
  console.log("truthy")
} else {
  console.log("falsy")
}
const empty = ""
if (empty) {
  console.log("wrong")
} else {
  console.log("empty string is falsy")
}
`, "truthy\nempty string is falsy")
}

func TestE2ENonNullableArrayAlwaysTruthy(t *testing.T) {
	// A non-Nullable array (the ordinary case) is always truthy regardless
	// of contents, matching real JS ("any array, even an empty one, is
	// truthy") — must not be confused with the Nullable null-sentinel
	// {ptr: null, len: 0} representation .exec() uses.
	assertOutput(t, `
const empty: number[] = []
const full: number[] = [1, 2, 3]
if (empty) {
  console.log("empty truthy")
} else {
  console.log("empty falsy - wrong")
}
if (full) {
  console.log("full truthy")
}
`, "empty truthy\nfull truthy")
}

func TestE2ENaNIsFalsy(t *testing.T) {
	// The consolidated toBool uses "fcmp one" (ordered-and-not-equal,
	// false for NaN) — the pre-consolidation emitToBool used "une"
	// (unordered-or-not-equal, wrongly true for NaN), a real bug found
	// and fixed while merging the two near-duplicate implementations. Uses
	// the bare `NaN` global rather than computing 0.0/0.0 — the latter
	// crashes today via a separate, unrelated, pre-existing bug (not
	// touched here; see ADR-00116's Side effects section).
	assertOutput(t, `
if (NaN) {
  console.log("wrong - NaN treated as truthy")
} else {
  console.log("NaN is falsy")
}
`, "NaN is falsy")
}

func TestE2EMapIsAlwaysTruthyRegardlessOfContent(t *testing.T) {
	// isPlainStringTy must not misclassify a Map (also bare IR=="ptr") as
	// a string and apply content-based (empty-string) falsiness to it —
	// any object, including an empty Map, is always truthy in real JS.
	assertOutput(t, `
const m = new Map<string, number>()
if (m) {
  console.log("empty map truthy")
} else {
  console.log("wrong - empty map falsy")
}
`, "empty map truthy")
}

// --- Stage 3: str.match(regexp), str.matchAll(regexp) ---

func TestE2EStringMatchNonGlobalIsExecShaped(t *testing.T) {
	assertOutput(t, `
const r = /(\d+)-(\d+)/
const m = "range: 12-34 end".match(r)
console.log(m !== null)
console.log(m.length)
console.log(m[0])
console.log(m[1])
console.log(m[2])
`, "true\n3\n12-34\n12\n34")
}

func TestE2EStringMatchNonGlobalNoMatchReturnsNull(t *testing.T) {
	assertOutput(t, `
const r = /(\d+)-(\d+)/
const m = "nothing here".match(r)
console.log(m === null)
`, "true")
}

func TestE2EStringMatchGlobalCollectsFullMatchesOnly(t *testing.T) {
	assertOutput(t, `
const g = /\d+/g
const all = "a1 b22 c333".match(g)
console.log(all.length)
console.log(all[0])
console.log(all[1])
console.log(all[2])
`, "3\n1\n22\n333")
}

func TestE2EStringMatchGlobalNoMatchesReturnsNullNotEmptyArray(t *testing.T) {
	// Real JS: str.match(/x/g) with zero matches returns null, not [] —
	// an early TDD-00035 design note got this backwards; corrected during
	// implementation. See ADR-00117.
	assertOutput(t, `
const g = /\d+/g
const none = "no digits".match(g)
console.log(none === null)
console.log(g.lastIndex)
`, "true\n0")
}

func TestE2EStringMatchAllReturnsMatchArrayPerMatch(t *testing.T) {
	assertOutput(t, `
const r = /(\w)-(\d+)/g
const all = "a-1 b-22 c-333".matchAll(r)
console.log(all.length)
for (const m of all) {
  console.log(m[0])
  console.log(m[1])
  console.log(m[2])
}
`, "3\na-1\na\n1\nb-22\nb\n22\nc-333\nc\n333")
}

func TestE2EStringMatchAllZeroMatchesIsEmptyArray(t *testing.T) {
	assertOutput(t, `
const r = /\d+/g
const none = "no digits".matchAll(r)
console.log(none.length)
`, "0")
}

func TestE2EStringMatchAllNonGlobalThrowsTypeError(t *testing.T) {
	assertOutput(t, `
try {
  const bad = /abc/
  const willThrow = "abc".matchAll(bad)
  console.log("no throw - wrong")
} catch (e) {
  console.log(e.name)
}
`, "TypeError")
}

func TestE2EStringMatchRejectsNonRegExpArgument(t *testing.T) {
	_, err := parseAndCompile(`
const m = "abc".match("a")
console.log(m)
`)
	if err == nil {
		t.Fatal("expected a compile error for match() called with a non-RegExp argument (no implicit string-to-RegExp coercion)")
	}
}

// --- Stage 4: str.replace(regexp, replacement), str.replaceAll(regexp, replacement) ---

func TestE2EStringReplaceNonGlobalReplacesFirstOnlyWithBackreferences(t *testing.T) {
	assertOutput(t, `
const r = /(\d+)-(\d+)/
console.log("range: 12-34 end".replace(r, "[$2:$1]"))
`, "range: [34:12] end")
}

func TestE2EStringReplaceNoMatchReturnsOriginalString(t *testing.T) {
	assertOutput(t, `
const r = /\d+/
console.log("no digits".replace(r, "X"))
`, "no digits")
}

func TestE2EStringReplaceGlobalReplacesAll(t *testing.T) {
	assertOutput(t, `
const g = /\d+/g
console.log("a1 b22 c333".replace(g, "N"))
`, "aN bN cN")
}

func TestE2EStringReplaceAmpersandBackreference(t *testing.T) {
	assertOutput(t, `
console.log("hello world".replace(/o/g, "[$&]"))
`, "hell[o] w[o]rld")
}

func TestE2EStringReplaceDollarDollarIsLiteralDollar(t *testing.T) {
	assertOutput(t, `
console.log("price".replace(/e/, "$$"))
`, "pric$")
}

func TestE2EStringReplaceInvalidGroupNumberStaysLiteral(t *testing.T) {
	// Only 2 capture groups exist — "$5" (no group 5) is left as literal
	// text, matching real JS, rather than reading out of bounds.
	assertOutput(t, `
console.log("ab".replace(/(a)(b)/, "$1-$2-$5"))
`, "a-b-$5")
}

func TestE2EStringReplaceAllRequiresGlobalRegExp(t *testing.T) {
	assertOutput(t, `
console.log("aaa".replaceAll(/a/g, "b"))
try {
  const bad = /a/
  console.log("x".replaceAll(bad, "y"))
} catch (e) {
  console.log(e.name)
}
`, "bbb\nTypeError")
}

func TestE2EStringReplaceCallbackBasic(t *testing.T) {
	assertOutput(t, `
const r = /\d+/
console.log("value: 42".replace(r, (m: string) => "[" + m + "]"))
`, "value: [42]")
}

func TestE2EStringReplaceCallbackReceivesOffsetAndString(t *testing.T) {
	assertOutput(t, `
const g = /\d+/g
console.log("a1 b22 c333".replace(g, (m: string, offset: number, s: string) => m + "@" + offset))
`, "a1@1 b22@4 c333@8")
}

func TestE2EStringReplaceCallbackTwoParamArity(t *testing.T) {
	assertOutput(t, `
console.log("hi".replace(/i/, (m: string, off: number) => m + off))
`, "hi1")
}

func TestE2EStringReplaceCallbackCalledExactlyOncePerMatch(t *testing.T) {
	// A real side-effect-counting check — the 3-pass replace-all algorithm
	// must invoke the callback exactly once per match, not twice (unlike
	// the pure two-pass shape str.match()/str.matchAll() safely reuse).
	assertOutput(t, `
let callCount = 0
const result = "x1 x2 x3".replace(/x\d/g, (m: string) => {
  callCount = callCount + 1
  return m.toUpperCase()
})
console.log(result)
console.log(callCount)
`, "X1 X2 X3\n3")
}

func TestE2EStringReplaceAllCallback(t *testing.T) {
	assertOutput(t, `
console.log("cat dog cat".replaceAll(/cat/g, (m: string) => "CAT"))
`, "CAT dog CAT")
}

func TestE2EStringReplaceCallbackRejectsTooManyParams(t *testing.T) {
	_, err := parseAndCompile(`
console.log("x".replace(/x/, (a: string, b: number, c: string, d: string) => "y"))
`)
	if err == nil {
		t.Fatal("expected a compile error for a replace() callback declaring more than 3 parameters")
	}
}

// --- Stage 5: str.split(regexp), str.search(regexp) ---

func TestE2EStringSplitBasic(t *testing.T) {
	assertOutput(t, `
const parts = "a1b22c333d".split(/\d+/)
console.log(parts.length)
console.log(parts[0])
console.log(parts[1])
console.log(parts[2])
console.log(parts[3])
`, "4\na\nb\nc\nd")
}

func TestE2EStringSplitNoMatchReturnsWholeStringAsOneElement(t *testing.T) {
	assertOutput(t, `
const none = "hello".split(/\d+/)
console.log(none.length)
console.log(none[0])
`, "1\nhello")
}

func TestE2EStringSplitIgnoresGlobalFlag(t *testing.T) {
	// str.split() finds every match regardless of the regex's own `g`
	// flag — a non-global regex still splits on all occurrences, matching
	// real JS (split() has its own local search loop, independent of
	// lastIndex/global).
	assertOutput(t, `
const parts = "a,b,c".split(/,/)
console.log(parts.length)
console.log(parts[0])
console.log(parts[1])
console.log(parts[2])
`, "3\na\nb\nc")
}

func TestE2EStringSplitEmptyStringNoMatch(t *testing.T) {
	assertOutput(t, `
const parts = "".split(/x/)
console.log(parts.length)
console.log(parts[0])
`, "1\n")
}

func TestE2EStringSearchFindsAndMisses(t *testing.T) {
	assertOutput(t, `
console.log("hello world".search(/world/))
console.log("hello world".search(/xyz/))
console.log("hello world".search(/^hello/))
`, "6\n-1\n0")
}

func TestE2EStringSearchDoesNotAffectLastIndex(t *testing.T) {
	// Real JS: .search() always searches from offset 0 and restores
	// whatever lastIndex held beforehand — invisible to later .exec()/
	// .test() iteration on the same RegExp instance.
	assertOutput(t, `
const g = /o/g
g.exec("boo")
console.log(g.lastIndex)
console.log("hello world".search(g))
console.log(g.lastIndex)
`, "2\n4\n2")
}

func TestE2EStringSearchPlainStringArgumentStillWorks(t *testing.T) {
	// No regression to the pre-existing (pre-RegExp) .indexOf()-shaped
	// behavior for a plain-string argument.
	assertOutput(t, `
console.log("hello world".search("world"))
`, "6")
}

// TestE2ERegExpDialectModeMatrix exercises the -regex dialect modes
// (TDD-00067 Options A + B): es-ascii (compile-option alignment), es-unicode
// (adds PCRE2_UTF/UCP code-point matching + NEWLINE_ANY), and pcre (raw
// PCRE2, today's legacy behavior kept as an explicit opt-in). The default
// (empty mode) resolves to es-unicode. Each case pins a behavior that
// differs by mode, so a regression in the option/context wiring surfaces
// here rather than only in the conformance aggregate.
func TestE2ERegExpDialectModeMatrix(t *testing.T) {
	cases := []struct {
		name, mode, src, want string
	}{
		// PCRE2_DOLLAR_ENDONLY (gated on absent `m`): `$` anchors at true
		// end in the ES modes; raw PCRE2 matches before a trailing newline.
		{"dollar-endonly/es-unicode", "es-unicode", `console.log(/foo$/.test("foo\n"))`, "false"},
		{"dollar-endonly/es-ascii", "es-ascii", `console.log(/foo$/.test("foo\n"))`, "false"},
		{"dollar-endonly/pcre-legacy", "pcre", `console.log(/foo$/.test("foo\n"))`, "true"},
		// With `m`, ES and PCRE2 agree `$` matches at line boundaries, so
		// DOLLAR_ENDONLY must NOT be applied — a true-end-only `$` here would
		// be a regression.
		{"dollar-multiline-still-lineboundary/es-unicode", "es-unicode", `console.log(/foo$/m.test("foo\nbar"))`, "true"},
		// PCRE2_MATCH_UNSET_BACKREF: a backref to a group that didn't
		// participate matches empty in ES; raw PCRE2 fails the match.
		{"unset-backref/es-unicode", "es-unicode", `console.log(/(a)?b\1/.test("b"))`, "true"},
		{"unset-backref/es-ascii", "es-ascii", `console.log(/(a)?b\1/.test("b"))`, "true"},
		{"unset-backref/pcre-legacy", "pcre", `console.log(/(a)?b\1/.test("b"))`, "false"},
		// PCRE2_ALT_BSUX: `\uXXXX` is a valid ECMAScript escape in the ES
		// modes (raw PCRE2 without ALT_BSUX rejects `\u` and would throw a
		// SyntaxError at construction).
		{"u-escape/es-unicode", "es-unicode", `console.log(/\u0041/.test("A"))`, "true"},
		{"u-escape/es-ascii", "es-ascii", `console.log(/\u0041/.test("A"))`, "true"},
		// PCRE2_UTF: `.` and classes span whole code points in es-unicode,
		// raw bytes in es-ascii — "café" is 4 code points / 5 UTF-8 bytes.
		{"utf-dot/es-unicode", "es-unicode", `console.log("café".replace(/./g, "X").length)`, "4"},
		{"utf-dot/es-ascii", "es-ascii", `console.log("café".replace(/./g, "X").length)`, "5"},
		// The default (unset) resolves to the highest implemented ES stage,
		// now ecmascript (Option C). Code-point `.` still yields 4 here, same as
		// es-unicode — see TestE2ERegExpEcmascriptMode for what the default's
		// normalization pass changes (the exact `.` line-terminator semantics).
		{"default-resolves-ecmascript", "", `console.log("café".replace(/./g, "X").length)`, "4"},
		// NEWLINE_ANY (es-unicode) excludes `\r` from `.`; es-ascii, with no
		// compile context, still matches it — the documented es-ascii caveat.
		{"dot-cr/es-unicode", "es-unicode", `console.log("a\rb".replace(/./g, "X").charCodeAt(1))`, "13"},
		{"dot-cr/es-ascii-caveat", "es-ascii", `console.log("a\rb".replace(/./g, "X").charCodeAt(1))`, "88"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := compileAndRunRegexMode(t, tc.src, tc.mode)
			if got != tc.want {
				t.Errorf("-regex=%q: got %q, want %q", tc.mode, got, tc.want)
			}
		})
	}
}

// TestE2ERegExpEmptyGlobalMatchTerminates pins the fix for the global empty-
// match infinite loop (TDD-00067 Stage 3): before the AdvanceStringIndex-
// style empty-match advance landed, a zero-length-capable global pattern
// (`/x*/g`, `/(?=a)/g`) drove match()/matchAll()/replaceAll() into an
// infinite loop, since lastIndex never advanced past the empty match. Each
// expected value is cross-checked against real Node — see the ADR. The
// advance steps by a whole code point in the UTF-matching default mode, so
// the multibyte case both terminates and counts by code point.
func TestE2ERegExpEmptyGlobalMatchTerminates(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"match-star", `console.log("abc".match(/x*/g).length)`, "4"},
		{"replaceAll-star", `console.log("abc".replaceAll(/x*/g, "-"))`, "-a-b-c-"},
		{"matchAll-nonempty", `console.log("aXbXc".matchAll(/X/g).length)`, "2"},
		{"lookahead-empty", `console.log("a".match(/(?=a)/g).length)`, "1"},
		// Multibyte subject: the empty-match advance steps a whole UTF-8 code
		// point, so iteration terminates and produces one empty match per code
		// point (3 chars + the end position = 4), never landing mid-code-point.
		{"multibyte-star", `console.log("aéb".match(/x*/g).length)`, "4"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Default mode (es-unicode); the bug and fix are mode-independent.
			got := compileAndRun(t, tc.src)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestE2ERegExpEcmascriptMode exercises the ecmascript dialect (TDD-00067
// Option C v1) — the default mode — whose source-normalization pass rewrites
// an unescaped top-level `.` (when the `s` flag is absent) to ECMAScript's
// exact "any character except a line terminator" class, fixing the es-unicode
// `NEWLINE_ANY` over-exclusion of `\x0b`/`\x0c`/`\x85`. Values cross-checked
// against Node. The es-unicode counterpart shows the pre-normalization
// divergence the pass repairs.
func TestE2ERegExpEcmascriptMode(t *testing.T) {
	cases := []struct{ name, mode, src, want string }{
		// VT (\x0b): ES `.` matches it; es-unicode's NEWLINE_ANY excludes it.
		{"dot-vt/ecmascript", "ecmascript", `console.log("a\x0bb".replace(/./g, "X"))`, "XXX"},
		{"dot-vt/es-unicode-caveat", "es-unicode", `console.log("a\x0bb".replace(/./g, "X").length)`, "3"},
		// FF (\x0c): same divergence.
		{"dot-ff/ecmascript", "ecmascript", `console.log(/a.b/.test("a\x0cb"))`, "true"},
		// `.` still excludes the true ES line terminator \n in both.
		{"dot-newline/ecmascript", "ecmascript", `console.log(/a.b/.test("a\nb"))`, "false"},
		// The normalization is faithful for escapes, classes, and dotAll:
		{"escaped-dot/ecmascript", "ecmascript", `console.log("a.b.c".split(/\./).length)`, "3"},
		{"class-dot/ecmascript", "ecmascript", `console.log(/[.]/.test("."))`, "true"},
		{"dotall/ecmascript", "ecmascript", `console.log(/x.y/s.test("x\ny"))`, "true"},
		// The A+B fixes still hold under ecmascript (it is es-unicode + the pass).
		{"dollar-endonly/ecmascript", "ecmascript", `console.log(/foo$/.test("foo\n"))`, "false"},
		{"unset-backref/ecmascript", "ecmascript", `console.log(/(a)?b\1/.test("b"))`, "true"},
		{"u-escape/ecmascript", "ecmascript", `console.log(/A/.test("A"))`, "true"},
		// `.source` reports the ORIGINAL pattern, not the normalized form.
		{"source-unchanged/ecmascript", "ecmascript", `console.log(new RegExp("a.b").source)`, "a.b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := compileAndRunRegexMode(t, tc.src, tc.mode)
			if got != tc.want {
				t.Errorf("-regex=%q: got %q, want %q", tc.mode, got, tc.want)
			}
		})
	}
}

// TestE2ERegExpUTF16IndexMode exercises the es-utf16 dialect (TDD-00067
// Stage 3): identical matching to es-unicode, but every user-visible offset
// boundary — str.search's return, the RegExp's lastIndex field, and a
// replace callback's `offset` argument — reports true UTF-16 code-unit
// positions instead of PCRE2's UTF-8 byte offsets. Each case is paired with
// its es-unicode (byte-space) counterpart so the conversion, not just a
// coincidentally-equal number, is what's being pinned. Test subjects use
// 'é' (U+00E9, 2 UTF-8 bytes / 1 UTF-16 unit) and '😀' (U+1F600, 4 bytes /
// 2 units — a surrogate pair).
func TestE2ERegExpUTF16IndexMode(t *testing.T) {
	cases := []struct{ name, mode, src, want string }{
		// str.search: byte offset of the match start vs UTF-16 code-unit offset.
		{"search/es-unicode-bytes", "es-unicode", `console.log("é1".search(/1/))`, "2"},
		{"search/es-utf16-units", "es-utf16", `console.log("é1".search(/1/))`, "1"},
		{"search-astral/es-unicode-bytes", "es-unicode", `console.log("😀1".search(/1/))`, "4"},
		{"search-astral/es-utf16-units", "es-utf16", `console.log("😀1".search(/1/))`, "2"},
		// No-match sentinel (-1) passes through unconverted in both modes.
		{"search-nomatch/es-utf16", "es-utf16", `console.log("xé".search(/z/))`, "-1"},
		// lastIndex after a global .exec(): byte end offset vs UTF-16 units.
		{"lastindex/es-unicode-bytes", "es-unicode", `const r=/1/g; r.exec("é1"); console.log(r.lastIndex)`, "3"},
		{"lastindex/es-utf16-units", "es-utf16", `const r=/1/g; r.exec("é1"); console.log(r.lastIndex)`, "2"},
		// A hand-set UTF-16 lastIndex is converted back to a byte start offset,
		// so the next global match resumes at the right code point.
		{"lastindex-roundtrip/es-utf16", "es-utf16", `const r=/\d/g; r.lastIndex=2; console.log(r.exec("é1é2")[0])`, "2"},
		// Empty-capable global match terminates in es-utf16 too: an empty match
		// at end-of-string advances to a byte start > strlen (utf16_to_byte
		// extends past the terminator), so PCRE2 rejects it and the loop ends
		// rather than re-finding the same end-of-string empty match forever. The
		// advance steps a whole code point, so an astral char (PCRE2_UTF sees it
		// as one code point) yields one empty match, not two.
		{"empty-at-end/es-utf16", "es-utf16", `console.log("aéb".match(/x*/g).length)`, "4"},
		{"empty-astral/es-utf16", "es-utf16", `console.log("😀x".match(/y*/g).length)`, "3"},
		// replace callback offset argument: byte vs UTF-16 units. replace keeps
		// the unmatched "é" prefix and substitutes the matched "1" with the
		// callback result "#<offset>".
		{"callback-offset/es-unicode", "es-unicode", `console.log("é1".replace(/1/, (m: string, o: number) => "#" + o.toString()))`, "é#2"},
		{"callback-offset/es-utf16", "es-utf16", `console.log("é1".replace(/1/, (m: string, o: number) => "#" + o.toString()))`, "é#1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := compileAndRunRegexMode(t, tc.src, tc.mode)
			if got != tc.want {
				t.Errorf("-regex=%q: got %q, want %q", tc.mode, got, tc.want)
			}
		})
	}
}
