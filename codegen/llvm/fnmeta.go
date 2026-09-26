package llvm

import (
	_ "embed"
	"fmt"
	"reflect"
	"strings"

	"KlainMainLang/ast"
)

// fnmeta.go — the object side of function values (TDD-00229 Stage A): each
// emitted function's `name`, `length` and kind, looked up at runtime by the
// function's *code pointer* (a closure header's fnptr, a dynamic record's
// fnptr) rather than stored in the header. Headers keep their {fnptr, env}
// layout — dozens of runtime-internal sites build them — and a code pointer
// that was never registered (a promise resolver, a stream callback) reads as
// an anonymous plain function, which is also how Node renders those.
//
// Two kinds of registration look *through* the env instead of naming the
// code itself: a bind trampoline (env[0] is the target header; the name
// becomes `bound <target>`), and a closure/dynamic adapter (the env is the
// adapted header).

//go:embed fnmetasrc/fnmeta.c
var fnMetaSource string

// FnMetaSource returns the C runtime that renders/inspects function values.
func FnMetaSource() string { return fnMetaSource }

// Function kinds, matching fnmeta.c.
const (
	fnKindPlain    = 0
	fnKindAsync    = 1
	fnKindGen      = 2
	fnKindAsyncGen = 3
	fnKindClass    = 4
	// fnFlagBound: env[0] is the target header; name is "bound " + target's.
	fnFlagBound = 0x100
	// fnFlagThroughEnv: the env is the adapted function's closure header.
	fnFlagThroughEnv = 0x200
)

type fnMetaEntry struct {
	sym    string // LLVM symbol of the code pointer, with the leading '@'
	name   string
	length int
	kind   int
}

// registerFnMeta records a function's metadata under its code symbol. The
// first registration of a symbol wins (a memoized trampoline is registered
// once however many references reach it).
func (e *Emitter) registerFnMeta(sym, name string, length, kind int) {
	if e.fnMetaSeen == nil {
		e.fnMetaSeen = map[string]bool{}
	}
	if e.fnMetaSeen[sym] {
		return
	}
	e.fnMetaSeen[sym] = true
	e.fnMetas = append(e.fnMetas, fnMetaEntry{sym: sym, name: name, length: length, kind: kind})
}

// fnLengthFromParams is Function.prototype.length: the parameters before the
// first one with a default or a rest one. A TS optional (`x?`) parameter is a
// plain parameter once types are stripped, so it counts.
func fnLengthFromParams(params []ast.Param) int {
	n := 0
	for _, p := range params {
		if p.Rest || p.Default != nil {
			break
		}
		n++
	}
	return n
}

// fnLengthFromSig is fnLengthFromParams over a resolved signature.
func fnLengthFromSig(sig FuncSig) int {
	n := 0
	for i := range sig.ParamTypes {
		if sig.HasRest && i == len(sig.ParamTypes)-1 {
			break
		}
		if i < len(sig.Defaults) && sig.Defaults[i] != nil {
			break
		}
		n++
	}
	return n
}

func fnKindOf(isAsync, isGen bool) int {
	switch {
	case isAsync && isGen:
		return fnKindAsyncGen
	case isAsync:
		return fnKindAsync
	case isGen:
		return fnKindGen
	}
	return fnKindPlain
}

// ensureFnMeta marks the program as rendering/inspecting function values:
// the metadata table is emitted at finalize and fnmeta.c is linked.
func (e *Emitter) ensureFnMeta() {
	if e.usedFnMeta {
		return
	}
	e.usedFnMeta = true
	e.ensureStrHeaderRuntime() // fnmeta.c builds its results with __kml_str_from_cstr
	e.emitGlobal("declare ptr @__kml_fn_inspect_hdr(ptr)")
	e.emitGlobal("declare ptr @__kml_fn_inspect_dyn(ptr, i64)")
	e.emitGlobal("declare ptr @__kml_fn_name_hdr(ptr)")
	e.emitGlobal("declare i64 @__kml_fn_length_hdr(ptr)")
	e.emitGlobal("declare ptr @__kml_fn_name_dyn(ptr)")
	e.emitGlobal("declare i64 @__kml_fn_length_dyn(ptr)")
}

// UsesFnMeta reports whether fnmeta.c must be linked.
func (e *Emitter) UsesFnMeta() bool { return e.usedFnMeta }

// emitFnMetaFinalize emits the code-pointer → metadata table fnmeta.c scans.
// Only a program that reached ensureFnMeta pays for it.
func (e *Emitter) emitFnMetaFinalize() {
	if !e.usedFnMeta {
		return
	}
	var rows []string
	for _, m := range e.fnMetas {
		rows = append(rows, fmt.Sprintf("{ ptr, ptr, i64, i64 } { ptr %s, ptr %s, i64 %d, i64 %d }",
			m.sym, e.internString(m.name), m.length, m.kind))
	}
	e.emitGlobal(fmt.Sprintf("@__kml_fnmeta_tab = constant [%d x { ptr, ptr, i64, i64 }] [%s]",
		len(rows), strings.Join(rows, ", ")))
	e.emitGlobal(fmt.Sprintf("@__kml_fnmeta_count = constant i64 %d", len(rows)))
	// A function object's own-property bag renders through dynjson.c's
	// object inspector — linked only when the program uses it; otherwise no
	// function can carry properties and the hook renders nothing.
	if e.UsesDynJSON() {
		e.emitGlobal(`
define ptr @__kml_fn_props_inspect(ptr %props, i64 %depth) {
entry:
  %n = getelementptr i8, ptr %props, i64 24
  %cnt = load i64, ptr %n, align 8
  %empty = icmp eq i64 %cnt, 0
  br i1 %empty, label %none, label %some
some:
  %s = call ptr @__kml_dynobj_inspect_at(ptr %props, i64 %depth)
  ret ptr %s
none:
  ret ptr ` + e.internString("") + `
}`)
	} else {
		e.emitGlobal(`
define ptr @__kml_fn_props_inspect(ptr %props, i64 %depth) {
entry:
  ret ptr ` + e.internString("") + `
}`)
	}
}

// inferFunctionNames computes ECMAScript NamedEvaluation for every anonymous
// function literal in the program: the binding, property key, assignment
// target, parameter or class field it is the direct initializer of (through
// parentheses-free TS wrappers — `as`, `!` — which type stripping removes).
// A reflective walk, so AST node types added later are traversed without
// this pass having to learn about them.
func (e *Emitter) inferFunctionNames(prog *ast.Program) {
	e.fnLitNames = map[ast.Node]string{}
	name := func(expr ast.Expression, n string) {
		for {
			switch w := expr.(type) {
			case *ast.AsExpression:
				expr = w.Expr
				continue
			case *ast.NonNullExpression:
				expr = w.Arg
				continue
			}
			break
		}
		switch lit := expr.(type) {
		case *ast.ArrowFunction:
			e.fnLitNames[lit] = n
		case *ast.FunctionExpression:
			if lit.Name == "" {
				e.fnLitNames[lit] = n
			}
		case *ast.ClassExpression:
			if lit.Decl != nil && lit.Decl.Name == "" {
				e.fnLitNames[lit] = n
			}
		}
	}
	visited := map[uintptr]bool{}
	nodeIface := reflect.TypeOf((*ast.Node)(nil)).Elem()
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
			return
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			p := v.Pointer()
			if visited[p] {
				return
			}
			visited[p] = true
			if v.Type().Implements(nodeIface) {
				switch n := v.Interface().(type) {
				case *ast.VarDeclaration:
					if n.Init != nil {
						name(n.Init, unmangleTopLevelName(n.Name))
					}
				case *ast.AssignmentExpression:
					if id, ok := n.Left.(*ast.Identifier); ok && (n.Op == "=" || n.Op == "&&=" || n.Op == "||=" || n.Op == "??=") {
						name(n.Right, unmangleTopLevelName(id.Name))
					}
				case *ast.ObjectLiteral:
					for _, p := range n.Properties {
						if p.KeyExpr == nil && p.Key != "" && p.AccessorKind == "" {
							name(p.Value, p.Key)
						}
					}
				case *ast.ClassDeclaration:
					for _, f := range n.Fields {
						if f.Initializer != nil {
							name(f.Initializer, f.Name)
						}
					}
				}
			}
			walk(v.Elem())
			return
		case reflect.Struct:
			if v.Type() == reflect.TypeOf(ast.Param{}) {
				p := v.Interface().(ast.Param)
				if p.Default != nil && p.ArrayPattern == nil && p.ObjectPattern == nil {
					name(p.Default, p.Name)
				}
			}
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i))
				}
			}
			return
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
			return
		case reflect.Map:
			it := v.MapRange()
			for it.Next() {
				walk(it.Value())
			}
		}
	}
	walk(reflect.ValueOf(prog))
}

// fnLitName returns a literal's inferred name ("" when anonymous).
func (e *Emitter) fnLitName(lit ast.Node) string {
	return e.fnLitNames[lit]
}

// emitInspectFunc renders a static function value (a closure header) the way
// util.inspect does. A nullable slot (`(() => void) | undefined`) holding no
// function renders `undefined`.
func (e *Emitter) emitInspectFunc(v Value) Value {
	e.ensureFnMeta()
	if !v.Ty.Nullable && !v.Ty.IsUndefined {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fn_inspect_hdr(ptr %s)", r, v.Ref))
		return Value{Ref: r, Ty: TypePtr}
	}
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, v.Ref))
	res, _ := e.emitStrBranch(isNull,
		func() (string, error) { return e.internString("undefined"), nil },
		func() (string, error) {
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fn_inspect_hdr(ptr %s)", r, v.Ref))
			return r, nil
		})
	return Value{Ref: res, Ty: TypePtr}
}

// emitIntToPtr reinterprets an i64 payload register as a pointer.
func (e *Emitter) emitIntToPtr(i64Reg string) string {
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", p, i64Reg))
	return p
}

// emitInspectFFIFunc renders a bound native function (an extended tag-12
// record) at nesting depth: `[Function: abs] { pointer: 123n }` (TDD-00229).
func (e *Emitter) emitInspectFFIFunc(v Value, depth int) Value {
	e.ensureFnMeta()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fn_inspect_dyn(ptr %s, i64 %d)", r, v.Ref, depth))
	return Value{Ref: r, Ty: TypePtr}
}

// paramDefaultType is TypeScript's type for an unannotated parameter that has
// a literal default (`p = '!'` is a string, `n = 0` a number, `b = true` a
// boolean, `xs = [1]` a number[]): the widened type of the initializer.
// A module-level binding's type also counts; anything else leaves the
// caller's own default in place (it may reference sibling parameters, not yet
// in scope).
func (e *Emitter) paramDefaultType(p ast.Param) (Type, bool) {
	if p.Type != nil || p.Default == nil || p.Rest || p.ArrayPattern != nil || p.ObjectPattern != nil {
		return Type{}, false
	}
	switch d := p.Default.(type) {
	case *ast.StringLiteral, *ast.TemplateLiteral:
		return TypePtr, true
	case *ast.NumberLiteral:
		if d.IsBigInt {
			return BigIntType(), true
		}
		return TypeF64, true
	case *ast.BooleanLiteral:
		return TypeBool, true
	case *ast.ArrayLiteral:
		if t := e.inferArrayType(d); t.IsArray && t.ElemType != nil {
			return t, true
		}
	case *ast.ObjectLiteral:
		if t := e.inferObjectType(d); t.IsObject && !t.IsDynamicObject {
			return t, true
		}
	case *ast.Identifier:
		// A module-level binding's type is fixed before any function is
		// emitted (TDD-00093), so `c = SUFFIX` takes SUFFIX's type.
		if g, ok := e.moduleGlobals[d.Name]; ok && g.Ty.IR != "" && !g.Ty.IsDynamic {
			return g.Ty, true
		}
	}
	return Type{}, false
}
