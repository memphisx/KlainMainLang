package tests

import (
	"strings"
	"testing"

	"KlainMainLang/parser"
)

// Automatic semicolon insertion and the scanner/parser split (TDD-00230 P1.1,
// P1.2): a statement ends at `;`, before `}` or the end of input, or at a line
// break; the restricted productions refuse a line break where the grammar
// does; a `>` or `/` is read the way the grammatical position needs. Expected
// outputs are Node's (26.10.0).

func TestE2EASIRestrictedPostfix(t *testing.T) {
	// A line break before `++`/`--` makes it the next statement's prefix
	// operator: `a\n++b` is `a; ++b`.
	assertOutput(t, `
let a = 1
let b = 2
a
++b
console.log(a, b)
let c = 5
c
--
b
console.log(c, b)
`, "1 3\n5 2")
}

func TestE2EASIReturnAndBreakAtALineBreak(t *testing.T) {
	// `return` and `break` end at a line break: the next line is its own
	// (here unreachable) statement, not the return value or a break label.
	assertOutput(t, `
function f(): string | undefined {
  return
  "never"
}
console.log(f() === undefined)
let seen = 0
outer: for (let i = 0; i < 2; i++) {
  for (let j = 0; j < 2; j++) {
    if (j === 1) {
      break
      seen
    }
  }
  seen = seen + 1
}
console.log(seen)
`, "true\n2")
}

func TestE2EParserDecidesRegexVersusDivision(t *testing.T) {
	// After `)` and after a block `}` the context-free reading of `/` is
	// division; where an operand is expected it starts a regex.
	assertOutput(t, `
const n = [1, 2].length
if (n) /re/.test("re") && console.log("regex after paren")
{}
/x/g.test("x") && console.log("regex after block")
const half = (n) / 2
console.log(half)
`, "regex after paren\nregex after block\n1")
}

func TestE2EGreaterThanGluedOnlyAsOperator(t *testing.T) {
	// Type arguments close with plain `>`s; an operator position glues them.
	assertOutput(t, `
const m: Map<string, Array<Array<number>>> = new Map()
m.set("k", [[1, 2]])
console.log(m.get("k")!.length, 8 >> 1, 8 >>> 1, 3 >= 2)
let s = 16
s >>= 2
let u = -16
u >>>= 28
console.log(s, u)
`, "1 4 4 true\n4 15")
}

func TestParseASIRejections(t *testing.T) {
	cases := []struct{ src, want string }{
		{"let x = 1 let y = 2\n", "expected ';', got let"},
		{"let x = 1\nx = 2 x = 3\n", "expected ';', got IDENT"},
		{"throw\nnew Error('x')\n", "line break not allowed after 'throw'"},
	}
	for _, c := range cases {
		_, err := parser.Parse(c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Parse(%q): got %v, want error containing %q", c.src, err, c.want)
		}
	}
}

func TestParseScanErrorIsReportedAndTerminates(t *testing.T) {
	// A scanning error past which the parser keeps looking must end the parse
	// with the scanner's message, not loop.
	_, err := parser.Parse("let o = { a: 1, \\u0062: 2 }\nlet p = [1\n")
	if err == nil || !strings.Contains(err.Error(), "unexpected character") {
		t.Fatalf("got %v, want the scanner's unexpected-character error", err)
	}
}

func TestE2EContextualAsyncAndAbstractAreNames(t *testing.T) {
	// `async`/`abstract` are keywords only where their keyword form follows on
	// the same line; everywhere else they are ordinary names.
	assertOutput(t, `
const o = {
  async load(n: number): Promise<number> { return n * 2 },
  async: 5,
  abstract: 6,
}
o.load(21).then((v) => console.log(v, o.async, o.abstract))
class K { async() { return "m" } abstract = 1 }
const k = new K()
console.log(k.async(), k.abstract)
const async = (x: number) => x + 1
console.log(async(1))
abstract class Shape { abstract area(): number }
class Sq extends Shape { area(): number { return 4 } }
console.log(new Sq().area())
`, "m 1\n2\n4\n42 5 6")
}

func TestE2EAsyncObjectMethodAsStreamSource(t *testing.T) {
	assertOutput(t, `
let n = 0
const rs = new ReadableStream<string>({
  async pull(c) {
    n++
    if (n > 3) { c.close(); return }
    c.enqueue('chunk' + n)
  },
})
async function drain(): Promise<void> {
  const reader = rs.getReader()
  while (true) {
    const r = await reader.read()
    if (r.done) break
    console.log(r.value)
  }
  console.log('done')
}
drain()
`, "chunk1\nchunk2\nchunk3\ndone")
}

func TestParseRestrictedArrowAndAsync(t *testing.T) {
	if _, err := parser.Parse("const f = (a: number)\n=> a + 1\n"); err == nil ||
		!strings.Contains(err.Error(), "line break not allowed before '=>'") {
		t.Errorf("a line break before => must be rejected, got %v", err)
	}
	// `async` then a line break then `function`: `async` is an identifier
	// expression statement and the function is an ordinary declaration.
	prog, err := parser.Parse("const async = 1\nasync\nfunction g(): number { return 7 }\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(prog.Body) != 3 {
		t.Fatalf("want 3 statements (decl, expression, function), got %d", len(prog.Body))
	}
}
