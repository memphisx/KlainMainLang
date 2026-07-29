package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
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

// --- Lane C: Promise.all/.race/.allSettled oracle fuzzing (ADR-00073) ---
//
// Scoped to these builtins' *ordinary-promise* branch only — an
// Array<Promise<number>> of deterministic, already-resolved async function
// calls — the one branch with a real, cheap-to-compute oracle. The
// Array<Promise<Response>> branch depends on real timing/network state and
// is already covered by tests/fetch_test.go's targeted E2E tests against a
// local httptest.Server instead; that shape doesn't fit go test -fuzz's
// corpus-replay model (non-deterministic timing, stateful server per run).
//
// Motivation for a fuzz lane specifically, rather than more hand-written
// cases like tests/async_test.go's: Promise.all/.race/.allSettled
// (emit_promise.go) each emit their own fresh compile-time labels/registers
// per *call site* (emitPromiseLoop's cond/body/done labels, the settled/ok/
// fail/merge labels inside .allSettled's Response branch, etc.) — a
// category of bug (label/register collisions across multiple textual call
// sites in one function, or across back-to-back combinator calls of
// different kinds sharing global lazily-emitted runtime declarations like
// ensurePromiseCombinators) that a single hand-written test per combinator
// can't realistically exercise by construction. This lane chains a random
// number of combinator calls — same kind and different kinds, at varying
// array lengths — inside one generated program specifically to hit that.
//
// genPromiseCombinatorCall picks an array length in [1, 6] (never 0 — an
// empty array hangs Promise.race forever by design, a documented limitation
// per TDD-00016's Open Questions, not a bug to rediscover here) and a
// combinator kind, and returns both the KlainMainLang source for one
// combinator call (using a freshly-declared array, never reusing a
// previous call's — reusing one would be the also-documented
// consume-on-read use-after-free from TDD-00016, not a real bug either) and
// the exact console.log lines it must produce, computed the same way
// elemFn's own declared body (`n * 2 + 1`) will actually transform each
// value at runtime.
func genPromiseCombinatorCall(rng *rand.Rand, varName string) (decl string, wantLines []string) {
	n := 1 + rng.Intn(6)
	vals := make([]int64, n)
	for i := range vals {
		vals[i] = int64(rng.Intn(2001) - 1000)
	}
	transform := func(v int64) int64 { return v*2 + 1 }

	var b strings.Builder
	fmt.Fprintf(&b, "const %s: Array<Promise<number>> = [];\n", varName)
	for _, v := range vals {
		fmt.Fprintf(&b, "%s.push(elemFn(%d));\n", varName, v)
	}

	resultVar := varName + "_r"
	switch rng.Intn(3) {
	case 0: // .all: every value, in order
		fmt.Fprintf(&b, "const %s = await Promise.all(%s);\nconsole.log(%s.length);\nfor (const x of %s) { console.log(x); }\n",
			resultVar, varName, resultVar, resultVar)
		wantLines = append(wantLines, fmt.Sprintf("%d", n))
		for _, v := range vals {
			wantLines = append(wantLines, fmt.Sprintf("%d", transform(v)))
		}
	case 1: // .race: honestly the first element (nothing to actually race)
		fmt.Fprintf(&b, "const %s = await Promise.race(%s);\nconsole.log(%s);\n", resultVar, varName, resultVar)
		wantLines = append(wantLines, fmt.Sprintf("%d", transform(vals[0])))
	case 2: // .allSettled: every entry always "fulfilled", in order
		fmt.Fprintf(&b, "const %s = await Promise.allSettled(%s);\nfor (const s of %s) { console.log(s.status); console.log(s.value); }\n",
			resultVar, varName, resultVar)
		for _, v := range vals {
			wantLines = append(wantLines, "fulfilled", fmt.Sprintf("%d", transform(v)))
		}
	}
	return b.String(), wantLines
}

func FuzzPromiseCombinatorsOrdinary(f *testing.F) {
	seeds := []int64{0, 1, 2, -1, 42, 1000, -1000}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, seed int64) {
		rng := rand.New(rand.NewSource(seed))
		numCalls := 1 + rng.Intn(3) // chain 1-3 combinator calls in one program

		var src strings.Builder
		src.WriteString("async function elemFn(n: number): Promise<number> { return n * 2 + 1 }\n")
		src.WriteString("async function main2(): Promise<void> {\n")
		var wantLines []string
		for i := 0; i < numCalls; i++ {
			decl, lines := genPromiseCombinatorCall(rng, fmt.Sprintf("arr%d", i))
			src.WriteString(decl)
			wantLines = append(wantLines, lines...)
		}
		src.WriteString("}\nmain2()\n")

		got := compileAndRun(t, src.String())
		// compileAndRun trims *all* trailing newlines off the program's
		// real stdout — matched here so a trailing empty console.log("")
		// line (e.g. an absent header value on the last chained call)
		// doesn't cause a false mismatch against wantLines' own trailing
		// empty entries.
		want := strings.TrimRight(strings.Join(wantLines, "\n"), "\n")
		if got != want {
			t.Fatalf("seed %d:\nsource:\n%s\ngot:\n%s\nwant:\n%s", seed, src.String(), got, want)
		}
	})
}

// --- Lane D: fetch(url, init) oracle fuzzing (ADR-00074) ---
//
// Same motivation as Lane C above, applied to fetch's new init argument
// (custom method/headers/body): buildFetchHeaderList (emit_fetch.go) emits
// its own fresh compile-time labels/registers per call site, so chaining
// several fetch(url, init) calls with different init shapes (method only,
// headers only, body only, all three, none) in one program is exactly the
// kind of case a single hand-written test per shape can't fully cover.
// Unlike Lane C, this needs a real (local, deterministic) HTTP server to
// round-trip against — built once, outside f.Fuzz, and reused across every
// iteration; only the request contents vary per iteration, not the server.
func newFuzzFetchEchoServer() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			Method  string `json:"method"`
			Body    string `json:"body"`
			XCustom string `json:"x_custom"`
			Auth    string `json:"authorization"`
		}{
			Method:  r.Method,
			Body:    string(body),
			XCustom: r.Header.Get("X-Custom-Header"),
			Auth:    r.Header.Get("Authorization"),
		})
	})
	return httptest.NewServer(mux)
}

// fuzzRandString returns a random alphanumeric string — deliberately
// restricted to a charset that's unambiguous everywhere it gets embedded
// (a KML string literal, JSON, and a raw HTTP header value/wire format all
// at once), so the oracle never has to reason about escaping differences
// between those three encodings.
func fuzzRandString(rng *rand.Rand, n int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = charset[rng.Intn(len(charset))]
	}
	return string(b)
}

func FuzzFetchInitOracle(f *testing.F) {
	srv := newFuzzFetchEchoServer()
	f.Cleanup(srv.Close)

	seeds := []int64{0, 1, 2, -1, 42, 1000, -1000}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, seed int64) {
		rng := rand.New(rand.NewSource(seed))
		numCalls := 1 + rng.Intn(3) // chain 1-3 fetch(url, init) calls in one program
		methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH"}

		var src strings.Builder
		src.WriteString("interface EchoResp { method: string; body: string; x_custom: string; authorization: string }\n")
		src.WriteString("async function main2(): Promise<void> {\n")
		var wantLines []string

		for i := 0; i < numCalls; i++ {
			method := "GET"
			methodExplicit := false
			var initFields []string
			if rng.Intn(2) == 0 {
				method = methods[rng.Intn(len(methods))]
				methodExplicit = true
				initFields = append(initFields, fmt.Sprintf("method: %q", method))
			}
			xCustom, auth := "", ""
			if rng.Intn(2) == 0 {
				xCustom = fuzzRandString(rng, 1+rng.Intn(10))
				auth = fuzzRandString(rng, 1+rng.Intn(10))
				fmt.Fprintf(&src, "const h%d: Map<string, string> = new Map<string, string>();\n", i)
				fmt.Fprintf(&src, "h%d.set(\"X-Custom-Header\", %q);\n", i, xCustom)
				fmt.Fprintf(&src, "h%d.set(\"Authorization\", %q);\n", i, auth)
				initFields = append(initFields, fmt.Sprintf("headers: h%d", i))
			}
			body := ""
			if rng.Intn(2) == 0 {
				body = fuzzRandString(rng, 1+rng.Intn(20))
				initFields = append(initFields, fmt.Sprintf("body: %q", body))
				if !methodExplicit {
					// Confirmed directly (this fuzz lane's own first run
					// caught it): setting CURLOPT_POSTFIELDS implicitly
					// switches libcurl into POST mode unless overridden by
					// an explicit CURLOPT_CUSTOMREQUEST — real, well-known
					// curl behavior, not a compiler bug. A body with no
					// explicit method really does arrive as POST — but an
					// *explicit* method: "GET" (methodExplicit=true) still
					// wins over that default and really does arrive as GET
					// (also confirmed directly — the fuzzer's own second
					// found case, distinct from the first).
					method = "POST"
				}
			}

			call := fmt.Sprintf("await fetch(%q)", srv.URL+"/echo")
			if len(initFields) > 0 {
				call = fmt.Sprintf("await fetch(%q, { %s })", srv.URL+"/echo", strings.Join(initFields, ", "))
			}
			fmt.Fprintf(&src, "const r%d: Response = %s;\n", i, call)
			fmt.Fprintf(&src, "const d%d: EchoResp = r%d.json();\n", i, i)
			fmt.Fprintf(&src, "console.log(d%d.method);\nconsole.log(d%d.body);\nconsole.log(d%d.x_custom);\nconsole.log(d%d.authorization);\n", i, i, i, i)

			wantLines = append(wantLines, method, body, xCustom, auth)
		}
		src.WriteString("}\nmain2()\n")

		got := compileAndRun(t, src.String())
		// compileAndRun trims *all* trailing newlines off the program's
		// real stdout — matched here so a trailing empty console.log("")
		// line (e.g. an absent header value on the last chained call)
		// doesn't cause a false mismatch against wantLines' own trailing
		// empty entries.
		want := strings.TrimRight(strings.Join(wantLines, "\n"), "\n")
		if got != want {
			t.Fatalf("seed %d:\nsource:\n%s\ngot:\n%s\nwant:\n%s", seed, src.String(), got, want)
		}
	})
}
