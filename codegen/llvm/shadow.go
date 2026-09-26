// shadow.go — ask the new front end every question an old decider answers,
// and record where the two disagree (TDD-00230 P0.2; package shadow).
package llvm

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/options"
	"KlainMainLang/shadow"
)

// ShadowOracle is the new front end's side of the comparison: each method
// answers the question of one old decider, with the new pass's type mapped to
// the representation codegen uses. ok=false means the new pass has no answer
// there yet, and nothing is recorded.
type ShadowOracle interface {
	// ExprType answers inferExprType.
	ExprType(expr ast.Expression) (Type, bool)
	// AnnotationType answers resolveType.
	AnnotationType(ta *ast.TypeAnnotation) (Type, bool)
	// GlobalType answers reliableGlobalType: the storage type of a top-level
	// declaration, or promotable=false when it stays a local.
	GlobalType(v *ast.VarDeclaration) (ty Type, promotable bool, ok bool)
	// ReturnType answers inferUnannotatedReturnType for a function body.
	ReturnType(body *ast.BlockStatement) (ty Type, hasValue bool, ok bool)
	// Reference answers emitIdent's name resolution: whether the identifier
	// is bound by a declaration in the program (or names a global).
	Reference(id *ast.Identifier) (declared bool, ok bool)
}

// NewShadowOracle builds the oracle for a program. It is nil until a new pass
// registers one; with it nil, or KML_SHADOW unset, no question is asked.
var NewShadowOracle func(prog *ast.Program, opts options.Options) ShadowOracle

// SetShadow makes e ask oracle and record disagreements in rec (tests use it
// to drive the harness directly).
func (e *Emitter) SetShadow(oracle ShadowOracle, rec *shadow.Recorder) {
	e.shadowOracle, e.shadowRec = oracle, rec
}

// startShadow turns the comparison on for a compile run when KML_SHADOW is set
// and an oracle is registered. The returned function closes the record file.
func (e *Emitter) startShadow(prog *ast.Program) (func(), error) {
	if e.shadowOracle != nil || NewShadowOracle == nil || !shadow.Enabled() {
		return func() {}, nil
	}
	rec, closer, err := shadow.FromEnv()
	if err != nil {
		return func() {}, err
	}
	e.shadowOracle, e.shadowRec = NewShadowOracle(prog, e.opts), rec
	return func() {
		e.shadowOracle, e.shadowRec = nil, nil
		closer.Close()
	}, nil
}

// shadowCompare records a disagreement between old (the decider's answer) and
// the oracle's, about node. "none" stands for a decider's no-answer.
func (e *Emitter) shadowCompare(decider string, node ast.Node, old Type, oldOK bool, neu Type, newOK bool) {
	oldKey, newKey := "none", "none"
	var fields []string
	if oldOK {
		oldKey = reprKey(old)
	}
	if newOK {
		newKey = reprKey(neu)
	}
	if oldKey == newKey {
		return
	}
	if oldOK && newOK {
		fields = diffFields(old, neu)
	} else {
		fields = []string{"presence"}
	}
	var pos ast.Pos
	if node != nil && !reflect.ValueOf(node).IsNil() {
		pos = node.GetPos()
	}
	e.shadowRec.Record(shadow.Disagreement{
		Decider: decider, Kind: nodeKind(node), Line: pos.Line, Col: pos.Col,
		Old: oldKey, New: newKey, Fields: fields,
	})
}

func nodeKind(n ast.Node) string {
	if n == nil {
		return "none"
	}
	return strings.TrimPrefix(fmt.Sprintf("%T", n), "*ast.")
}

// shadowExprType is the inferExprType hook.
func (e *Emitter) shadowExprType(expr ast.Expression, old Type) {
	if neu, ok := e.shadowOracle.ExprType(expr); ok {
		e.shadowCompare("inferExprType", expr, old, true, neu, true)
	}
}

// shadowAnnotationType is the resolveType hook. An annotation codegen
// synthesised has no node, and is not compared.
func (e *Emitter) shadowAnnotationType(ta *ast.TypeAnnotation, old Type) {
	n := ta.TypeNode()
	if n == nil {
		return
	}
	if neu, ok := e.shadowOracle.AnnotationType(ta); ok {
		e.shadowCompare("resolveType", n, old, true, neu, true)
	}
}

// shadowGlobalType is the reliableGlobalType hook.
func (e *Emitter) shadowGlobalType(v *ast.VarDeclaration, old Type, oldOK bool) {
	if neu, promotable, ok := e.shadowOracle.GlobalType(v); ok {
		e.shadowCompare("reliableGlobalType", v, old, oldOK, neu, promotable)
	}
}

// shadowReturnType is the inferUnannotatedReturnType hook.
func (e *Emitter) shadowReturnType(body *ast.BlockStatement, old Type, oldOK bool) {
	if neu, hasValue, ok := e.shadowOracle.ReturnType(body); ok {
		e.shadowCompare("inferUnannotatedReturnType", body, old, oldOK, neu, hasValue)
	}
}

// shadowReference is emitIdent's hook: declared is codegen's answer to
// whether id is bound by a declaration (a local or module binding, a
// function, a namespace member) rather than a global it knows or an
// undefined name.
func (e *Emitter) shadowReference(id *ast.Identifier, declared bool) {
	if e.shadowOracle == nil {
		return
	}
	neu, ok := e.shadowOracle.Reference(id)
	if !ok || neu == declared {
		return
	}
	answer := func(d bool) string {
		if d {
			return "declared"
		}
		return "global"
	}
	pos := id.GetPos()
	e.shadowRec.Record(shadow.Disagreement{
		Decider: "emitIdent", Kind: "Identifier", Line: pos.Line, Col: pos.Col,
		Old: answer(declared) + " " + id.Name, New: answer(neu) + " " + id.Name, Fields: []string{answer(declared) + "→" + answer(neu)},
	})
}

// reprKey renders a Type canonically: every non-zero field, nested types
// included, in declaration order. Two types with the same key are the same
// representation. AST expressions a type carries (default-value
// expressions) are left out: they are not part of the representation.
func reprKey(t Type) string {
	var b strings.Builder
	writeKey(&b, reflect.ValueOf(t), 0)
	return b.String()
}

var astExprType = reflect.TypeOf((*ast.Expression)(nil)).Elem()

func writeKey(b *strings.Builder, v reflect.Value, depth int) {
	if depth > 6 {
		b.WriteString("…")
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			b.WriteString("nil")
			return
		}
		writeKey(b, v.Elem(), depth+1)
	case reflect.Slice:
		b.WriteByte('[')
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				b.WriteByte(' ')
			}
			writeKey(b, v.Index(i), depth+1)
		}
		b.WriteByte(']')
	case reflect.Map:
		keys := make([]string, 0, v.Len())
		it := v.MapRange()
		vals := map[string]reflect.Value{}
		for it.Next() {
			k := fmt.Sprint(it.Key().Interface())
			keys = append(keys, k)
			vals[k] = it.Value()
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for _, k := range keys {
			b.WriteString(k + ":")
			writeKey(b, vals[k], depth+1)
			b.WriteByte(' ')
		}
		b.WriteByte('}')
	case reflect.Struct:
		t := v.Type()
		b.WriteByte('{')
		first := true
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			fv := v.Field(i)
			if !f.IsExported() || fv.IsZero() || skipKeyField(f.Type) {
				continue
			}
			if !first {
				b.WriteByte(' ')
			}
			first = false
			b.WriteString(f.Name + "=")
			writeKey(b, fv, depth+1)
		}
		b.WriteByte('}')
	case reflect.Interface, reflect.Func, reflect.Chan:
		b.WriteString("·")
	default:
		fmt.Fprint(b, v.Interface())
	}
}

// skipKeyField reports whether a field holds AST expressions or behaviour
// rather than representation.
func skipKeyField(t reflect.Type) bool {
	for t.Kind() == reflect.Slice || t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t == astExprType || t.Kind() == reflect.Func || t.Kind() == reflect.Chan ||
		(t.Kind() == reflect.Interface && t.Implements(astExprType))
}

// diffFields names the top-level fields of two Types whose keys differ.
func diffFields(a, b Type) []string {
	av, bv := reflect.ValueOf(a), reflect.ValueOf(b)
	t := av.Type()
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() || skipKeyField(f.Type) {
			continue
		}
		var ka, kb strings.Builder
		writeKey(&ka, av.Field(i), 0)
		writeKey(&kb, bv.Field(i), 0)
		if ka.String() != kb.String() {
			out = append(out, f.Name)
		}
	}
	return out
}
