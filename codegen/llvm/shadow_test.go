package llvm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"KlainMainLang/ast"
	"KlainMainLang/parser"
	"KlainMainLang/shadow"
)

// literalOracle disagrees with codegen on purpose: every expression is an
// i8, a `number` annotation a pointer, and it has no answer for anything
// else.
type literalOracle struct{}

func (literalOracle) ExprType(expr ast.Expression) (Type, bool) {
	return Type{IR: "i8"}, true
}
func (literalOracle) AnnotationType(ta *ast.TypeAnnotation) (Type, bool) {
	return TypePtr, ta.Name == "number"
}
func (literalOracle) GlobalType(*ast.VarDeclaration) (Type, bool, bool) { return Type{}, false, false }
func (literalOracle) ReturnType(*ast.BlockStatement) (Type, bool, bool) { return Type{}, false, false }
func (literalOracle) Reference(*ast.Identifier) (bool, bool)            { return false, false }

func emitWith(t *testing.T, src string, oracle ShadowOracle, rec *shadow.Recorder) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEmitter()
	if oracle != nil {
		e.SetShadow(oracle, rec)
	}
	ir, err := e.EmitProgram(prog)
	if err != nil {
		t.Fatal(err)
	}
	return ir
}

// The oracle is only asked: the emitted IR is the same with it as without,
// and each disagreement is recorded once, with the fields that differ.
func TestShadowRecordsDisagreementsWithoutChangingIR(t *testing.T) {
	src := "let n: number = 1 + 2\nconst xs = [n, 4].map(x => x * 2)\nconsole.log(xs.length, `${n}`)\n"
	var buf bytes.Buffer
	rec := shadow.NewRecorder(&buf, "unit.ts")
	with := emitWith(t, src, literalOracle{}, rec)
	without := emitWith(t, src, nil, nil)
	if with != without {
		t.Fatal("asking the oracle changed the emitted IR")
	}
	var ds []shadow.Disagreement
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var d shadow.Disagreement
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			t.Fatalf("bad record %q: %v", line, err)
		}
		ds = append(ds, d)
	}
	seen := map[string]bool{}
	for _, d := range ds {
		key := d.Decider + " " + d.Kind
		seen[key] = true
		if d.Unit != "unit.ts" || d.Line == 0 || d.Old == d.New || len(d.Fields) == 0 {
			t.Errorf("record %+v", d)
		}
	}
	for _, want := range []string{"inferExprType CallExpression", "resolveType KeywordType"} {
		if !seen[want] {
			t.Errorf("no %s disagreement in %v", want, ds)
		}
	}
	// Records are per node: each appears once however often codegen asked.
	nodes := map[string]int{}
	for _, d := range ds {
		nodes[fmt.Sprintf("%s %s %d:%d", d.Decider, d.Kind, d.Line, d.Col)]++
	}
	for k, n := range nodes {
		if n != 1 {
			t.Errorf("%s recorded %d times", k, n)
		}
	}
}

func TestReprKey(t *testing.T) {
	a := Type{IR: "i64", Signed: true}
	b := Type{IR: "i64", Signed: true, Nullable: true}
	if reprKey(a) == reprKey(b) || reprKey(a) != reprKey(Type{IR: "i64", Signed: true}) {
		t.Errorf("keys %q %q", reprKey(a), reprKey(b))
	}
	if got := diffFields(a, b); strings.Join(got, ",") != "Nullable" {
		t.Errorf("diff %v", got)
	}
	arr := Type{IR: "ptr", IsArray: true, ElemType: &Type{IR: "double", Float: true}}
	if !strings.Contains(reprKey(arr), "ElemType={IR=double Float=true}") {
		t.Errorf("array key %q", reprKey(arr))
	}
}
