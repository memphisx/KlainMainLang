package tests

import (
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"testing"
)

// --- Lane A: arithmetic-correctness oracle fuzzing (TDD-00014) ---
//
// genArithExpr recursively builds a random arithmetic expression tree over
// small integer literals, returning both its KlainMainLang source text
// (fully parenthesized, so the generated precedence never depends on
// matching the parser's own precedence table) and its expected value
// computed with Go's native int64 arithmetic — the same wraparound
// add/sub/mul and truncating-toward-zero sdiv/srem this compiler's codegen
// targets for the default `number` (i64) type (see codegen/llvm/emit_exprs.go).
//
// Literal magnitude is clamped to keep the oracle unambiguous: LLVM's sdiv/
// srem are undefined behavior not just on a zero divisor (guarded at
// runtime since ADR-00069, so this generator substitutes 1 instead) but
// also when the dividend is exactly math.MinInt64 and the divisor is -1 —
// astronomically unlikely to occur by chance with clamped small literals,
// so deliberately not defended against here (see TDD-00014).
func genArithExpr(rng *rand.Rand, depth int) (string, int64) {
	if depth <= 0 || rng.Intn(3) == 0 {
		v := int64(rng.Intn(2001) - 1000) // [-1000, 1000]
		return fmt.Sprintf("%d", v), v
	}
	if rng.Intn(6) == 0 {
		// Unary negation. The space after '-' matters: a negative literal's
		// own leading '-' would otherwise sit directly adjacent (e.g.
		// "(--500)"), which the lexer reads as "--" (decrement) followed by
		// "500", not two separate unary minuses.
		s, v := genArithExpr(rng, depth-1)
		return fmt.Sprintf("(- %s)", s), -v
	}

	ops := []string{"+", "-", "*", "/", "%"}
	op := ops[rng.Intn(len(ops))]
	l, lv := genArithExpr(rng, depth-1)
	r, rv := genArithExpr(rng, depth-1)
	if (op == "/" || op == "%") && rv == 0 {
		rv = 1
		r = "1"
	}

	var want int64
	switch op {
	case "+":
		want = lv + rv
	case "-":
		want = lv - rv
	case "*":
		want = lv * rv
	case "/":
		want = lv / rv
	case "%":
		want = lv % rv
	}
	return fmt.Sprintf("(%s %s %s)", l, op, r), want
}

func FuzzArithmeticCorrectness(f *testing.F) {
	seeds := []int64{0, 1, 2, -1, 42, 1000, -1000, 1 << 31, -(1 << 31), 1 << 62}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, seed int64) {
		rng := rand.New(rand.NewSource(seed))
		expr, want := genArithExpr(rng, 5)
		src := fmt.Sprintf("console.log(%s);", expr)

		got := compileAndRun(t, src)
		wantStr := fmt.Sprintf("%d", want)
		if got != wantStr {
			t.Fatalf("seed %d: %s => got %q, want %q", seed, src, got, wantStr)
		}
	})
}

// --- Lane B: broad well-formedness/crash fuzzing (TDD-00014) ---
//
// genProgram builds a small, template-shaped but randomly parameterized
// program spanning more of the language than Lane A's arithmetic-only
// grammar — variable declarations, arithmetic updates, if/else, a bounded
// for loop, a bounded while loop, an array + for...of, and a function
// declaration + call — all with statically bounded loop trip counts so a
// buggy generated program can never hang the fuzzer. There is no
// correctness oracle here (unlike Lane A): the only assertions are that the
// Go compiler itself never panics, clang always accepts the emitted LLVM IR
// (a clang rejection is unambiguously a codegen bug, since the source is
// guaranteed syntactically valid by construction), and the resulting binary
// always exits 0 (the generator never throws or calls process.exit, so
// anything else is a crash).
func genProgram(rng *rand.Rand) string {
	lit := func() int { return rng.Intn(2001) - 1000 }

	var b strings.Builder
	fmt.Fprintf(&b, "function f0(x: number): number {\n  return x * 2 - 1;\n}\n\n")

	numVars := 2 + rng.Intn(3)
	names := make([]string, numVars)
	for i := 0; i < numVars; i++ {
		names[i] = fmt.Sprintf("v%d", i)
		fmt.Fprintf(&b, "let %s: number = %d;\n", names[i], lit())
	}

	updates := rng.Intn(5)
	ops := []string{"+", "-", "*"}
	for i := 0; i < updates; i++ {
		lhs := names[rng.Intn(numVars)]
		rhs := names[rng.Intn(numVars)]
		fmt.Fprintf(&b, "%s = %s %s %s;\n", lhs, lhs, ops[rng.Intn(len(ops))], rhs)
	}

	cmpOps := []string{"<", ">", "<=", ">=", "===", "!=="}
	fmt.Fprintf(&b, "if (%s %s %d) {\n  %s = f0(%s);\n} else {\n  %s = %s - 1;\n}\n",
		names[0], cmpOps[rng.Intn(len(cmpOps))], lit(), names[0], names[0], names[0], names[0])

	forIterations := rng.Intn(20)
	fmt.Fprintf(&b, "for (let i: number = 0; i < %d; i++) {\n  %s = %s + i;\n}\n", forIterations, names[0], names[0])

	whileIterations := rng.Intn(20)
	fmt.Fprintf(&b, "let w: number = 0;\nwhile (w < %d) {\n  %s = %s + 1;\n  w++;\n}\n", whileIterations, names[1%numVars], names[1%numVars])

	arrLen := 1 + rng.Intn(5)
	vals := make([]string, arrLen)
	for i := range vals {
		vals[i] = fmt.Sprintf("%d", lit())
	}
	fmt.Fprintf(&b, "const arr: number[] = [%s];\nfor (const x of arr) {\n  %s = %s + x;\n}\n", strings.Join(vals, ", "), names[0], names[0])

	for _, n := range names {
		fmt.Fprintf(&b, "console.log(%s);\n", n)
	}
	return b.String()
}

func FuzzProgramWellFormed(f *testing.F) {
	seeds := []int64{0, 1, 2, -1, 42, 1000, -1000}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, seed int64) {
		rng := rand.New(rand.NewSource(seed))
		src := genProgram(rng)

		binFile := buildBinary(t, src)
		cmd := exec.Command(binFile)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("seed %d: binary crashed: %v\noutput: %s\nsource:\n%s", seed, err, out, src)
		}
	})
}
