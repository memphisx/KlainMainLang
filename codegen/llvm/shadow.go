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
var NewShadowOracle func(prog *ast.Program, opts options.Options, r reprCtx) ShadowOracle

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
	e.shadowOracle, e.shadowRec = NewShadowOracle(prog, e.opts, e.repr()), rec
	return func() {
		// One tally record per decider and node kind: how often it was asked,
		// so the report can show where the two agree.
		keys := make([]string, 0, len(e.shadowAsked))
		for k := range e.shadowAsked {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			dk := strings.SplitN(k, "\x00", 2)
			rec.Record(shadow.Disagreement{Decider: dk[0], Kind: dk[1], Asked: e.shadowAsked[k]})
		}
		e.shadowAsked, e.shadowSeen = nil, nil
		e.shadowOracle, e.shadowRec = nil, nil
		closer.Close()
	}, nil
}

// shadowCompare records a disagreement between old (the decider's answer) and
// the oracle's, about node. "none" stands for a decider's no-answer.
func (e *Emitter) shadowCompare(decider string, node ast.Node, old Type, oldOK bool, neu Type, newOK bool) {
	if e.shadowAsked == nil {
		e.shadowAsked, e.shadowSeen = map[string]int{}, map[shadowNode]bool{}
	}
	if sn := (shadowNode{decider, node}); !e.shadowSeen[sn] {
		e.shadowSeen[sn] = true
		e.shadowAsked[decider+"\x00"+nodeKind(node)]++
	}
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
	if _, isID := node.(*ast.Identifier); isID && intPromoted(old, neu) {
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
		Module:  e.inLib,
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
	if e.analysisDepth > 0 {
		// An analysis pre-pass asks before the bindings it reads are in
		// scope: not the answer codegen emits by.
		return
	}
	if e.foreignThisBody {
		// A method body compiled for any receiver (its shape record):
		// emission never asks the checker there (reprOracle).
		return
	}
	// A function read while its own return type is inferred answers the
	// recursion guard's void placeholder: not the type codegen emits by.
	if id, ok := expr.(*ast.Identifier); ok && old.IsFunc() && old.FuncRetType != nil && old.FuncRetType.IR == "void" && len(e.sigInferInProgress) > 0 {
		for fd := range e.sigInferInProgress {
			if fd.Name == id.Name && fd.ReturnType == nil {
				return
			}
		}
	}
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
//
// It runs only where codegen fell back to the old inference, so every call
// is a row: an answer the checker gives but codegen cannot use, or none.
func (e *Emitter) shadowReturnType(body *ast.BlockStatement, old Type, oldOK bool) {
	if e.foreignThisBody {
		// A method body compiled for any receiver (its shape record), as
		// shadowExprType: emission never asks the checker there.
		return
	}
	neu, hasValue, ok := e.shadowOracle.ReturnType(body)
	if !ok {
		pos := body.GetPos()
		e.shadowRec.Record(shadow.Disagreement{
			Module:  e.inLib,
			Decider: "inferUnannotatedReturnType", Kind: nodeKind(body), Line: pos.Line, Col: pos.Col,
			Old: reprKey(old), New: "unanswered", Fields: []string{"unanswered"},
		})
		return
	}
	e.shadowCompare("inferUnannotatedReturnType", body, old, oldOK, neu, hasValue)
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
		Module:  e.inLib,
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
		// A class is nominal: its name stands for its layout, which can be
		// large and nest other classes.
		ty, isType := v.Interface().(Type)
		class := isType && ty.IsClass()
		// A plain `any` box holds undefined and null itself: marking it
		// nullable or undefined-able (an optional `any` parameter) is the
		// same representation.
		box := isType && ty.IsDynamic()
		plainAny := box && v.FieldByName("UnionMembers").Len() == 0
		b.WriteByte('{')
		first := true
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			fv := v.Field(i)
			if !f.IsExported() || fv.IsZero() || (fv.Kind() == reflect.Slice && fv.Len() == 0) || skipKeyField(f.Type) || metadataField[f.Name] {
				continue
			}
			if class && (f.Name == "Fields" || f.Name == "HasVTable") {
				continue
			}
			if plainAny && f.Name == "Absent" {
				continue
			}
			// A union box tags which absence it holds: whether that is null
			// or undefined is the value's, not the representation's, so its
			// key says only that it is nullable.
			if box && f.Name == "Absent" {
				if !first {
					b.WriteByte(' ')
				}
				first = false
				b.WriteString("Absent=nullable")
				continue
			}
			if !first {
				b.WriteByte(' ')
			}
			first = false
			b.WriteString(f.Name + "=")
			if f.Name == "UnionMembers" {
				// A box holds its members unordered, and tags a string as a
				// string, literal or not: `boolean | number` and `number |
				// boolean` are one representation, as are `'ipc' | string`
				// and `string`.
				seen := map[string]bool{}
				var keys []string
				for i := 0; i < fv.Len(); i++ {
					m := fv.Index(i)
					if mt, ok := m.Interface().(Type); ok && mt.IsStrLiteral() {
						mt.setKind(kindStrLiteral, false)
						mt.LitValue = ""
						m = reflect.ValueOf(mt)
					}
					var mb strings.Builder
					writeKey(&mb, m, depth+1)
					if k := mb.String(); !seen[k] {
						seen[k] = true
						keys = append(keys, k)
					}
				}
				sort.Strings(keys)
				b.WriteString("[" + strings.Join(keys, " ") + "]")
				continue
			}
			writeKey(b, fv, depth+1)
		}
		b.WriteByte('}')
	case reflect.Interface, reflect.Func, reflect.Chan:
		b.WriteString("·")
	default:
		fmt.Fprint(b, v.Interface())
	}
}

// metadataField names the Type fields a call site reads for defaults and
// arity, a use site for validation (an intersection's members, already
// merged into its fields), a named structural type's re-resolution handle
// (RefName: the same fields under its interface's name) or a string
// literal's value, not for the value's representation.
var metadataField = map[string]bool{"FuncParamNames": true, "FuncParamOptional": true, "Inferred": true, "IntersectionMembers": true, "RefName": true,
	// A string literal type is a string at run time; its value is the
	// compile-time knowledge a discriminant reads.
	"IsStrLiteral": true, "LitValue": true}

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

// shadowNode is one question: a decider asked about a node.
type shadowNode struct {
	decider string
	node    ast.Node
}

// analysisPass marks an analysis pre-pass (escape and purity checks, the
// empty-container and widened-binding inference, the JavaScript call-site
// parameter collection) until the returned func
// runs: inferExprType there is not compared (shadowExprType).
func (e *Emitter) analysisPass() func() {
	e.analysisDepth++
	return func() { e.analysisDepth-- }
}

// intPromoted reports a number a binding holds as an integer where codegen
// proved the value integral (an integer-valued local): an
// optimization over reprOf's double that behaves the same, not a
// disagreement about the representation of the type.
func intPromoted(old, neu Type) bool {
	o, n := old.withoutNullable(), neu.withoutNullable()
	return o.Nullable() == n.Nullable() && !o.IsDynamic() && !o.Float && (o.IR == "i64" || o.IR == "i32" || o.IR == "i16" || o.IR == "i8") &&
		n.Float && n.IR == "double" && old.Nullable() == neu.Nullable() && old.IsUndefined() == neu.IsUndefined()
}

// shadowSwitchFallback records a switched expression kind the checker
// left unanswered, answered by its fallback (settledExprKind).
func (e *Emitter) shadowSwitchFallback(expr ast.Expression, fallback Type) {
	if e.shadowRec == nil || e.analysisDepth > 0 {
		return // an analysis pre-pass asks before its bindings are in scope
	}
	pos := expr.GetPos()
	e.shadowRec.Record(shadow.Disagreement{
		Module:  e.inLib,
		Decider: "switchFallback", Kind: nodeKind(expr), Line: pos.Line, Col: pos.Col,
		Old: reprKey(fallback), New: "unanswered", Fields: []string{"unanswered"},
	})
}
