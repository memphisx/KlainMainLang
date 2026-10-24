package llvm

import (
	"KlainMainLang/checker"
	"KlainMainLang/lib"
	_ "embed"
	"fmt"
	"reflect"
	"strconv"
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
	// fnFlagThroughBox: the env is { tag, box } — a dynamic function's
	// closure thunk (emitAnyToClosure).
	fnFlagThroughBox = 0x400
	// fnFlagBoundBox: a dynamic bound function (@__kml_dyn_bound).
	fnFlagBoundBox = 0x800
)

type fnMetaEntry struct {
	sym    string // LLVM symbol of the code pointer, with the leading '@'
	name   string
	length int
	kind   int
	src    string // its source text (Function.prototype.toString), "" for none
	island string // the dynamic-only file it was registered in (unitRow.island)
}

// fnMetaSource gives the function registered under sym its source text,
// as a later registerFnMeta of the node it was compiled from knows it.
func (e *Emitter) fnMetaSource(sym string, src ast.SourceRange) {
	text := src.String()
	if text == "" {
		return
	}
	for i := len(e.fnMetas) - 1; i >= 0; i-- {
		if e.fnMetas[i].sym == sym {
			if e.fnMetas[i].src == "" {
				e.fnMetas[i].src = text
			}
			return
		}
	}
}

// registerFnMeta records a function's metadata under its code symbol. The
// first registration of a symbol wins (a memoized trampoline is registered
// once however many references reach it), once per lazy shared library:
// a dynamic-only file's own table does not stand in for the executable's.
func (e *Emitter) registerFnMeta(sym, name string, length, kind int) {
	if e.fnMetaSeen == nil {
		e.fnMetaSeen = map[string]bool{}
	}
	if e.fnMetaSeen[e.curDynFile+"\x00"+sym] {
		return
	}
	e.fnMetaSeen[e.curDynFile+"\x00"+sym] = true
	// A `[Symbol.x]` member (named "@@x") is named `[Symbol.x]`, as in JS.
	if strings.HasPrefix(name, "@@") {
		name = "[Symbol." + strings.TrimPrefix(name, "@@") + "]"
	}
	// A library function a builtin lowers to (`// kml:lower process.nextTick`)
	// is named for the builtin: `nextTick`.
	if path, ok := lib.LoweredFuncUses()[name]; ok && len(path) > 0 {
		name = path[len(path)-1]
	}
	e.fnMetas = append(e.fnMetas, fnMetaEntry{sym: sym, name: name, length: length, kind: kind, island: e.curDynFile})
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
		if i == 0 && sig.This {
			continue // the `this` parameter
		}
		if sig.HasRest && i == len(sig.ParamTypes)-1 {
			break
		}
		if i < len(sig.Defaults) && sig.Defaults[i] != nil || i < len(sig.HasDefault) && sig.HasDefault[i] {
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
	e.ensureDynObj()           // and a function's property bag with dynobj.c
	e.emitGlobal("declare ptr @__kml_fn_inspect_hdr(ptr)")
	e.emitGlobal("declare ptr @__kml_fn_inspect_dyn(ptr, i64)")
	e.emitGlobal("declare ptr @__kml_fn_name_hdr(ptr)")
	e.emitGlobal("declare i64 @__kml_fn_length_hdr(ptr)")
	e.emitGlobal("declare ptr @__kml_fn_name_dyn(ptr)")
	e.emitGlobal("declare ptr @__kml_fn_tostring_dyn(ptr)")
	e.emitGlobal("declare i64 @__kml_fn_static_get(ptr, ptr)")
	e.emitGlobal("declare i64 @__kml_fn_length_dyn(ptr)")
	e.emitGlobal("declare ptr @__kml_fn_props_dyn(ptr)")
	e.emitGlobal("declare ptr @__kml_fn_props_dyn_make(ptr, ptr, ptr, ptr)")
	e.emitGlobal("declare i1 @__kml_fn_has_builtin(ptr, i1)")
	e.emitGlobal("declare i1 @__kml_fn_has_proto_member(ptr)")
	e.emitGlobal("declare i1 @__kml_array_has_key(i64, ptr, i1, i64)")
	e.emitGlobal("declare i64 @__kml_fn_thunk_box(ptr, ptr)")
}

// emitFnPropsBag returns the own-property bag of a boxed function's record
// (a tag-12 payload pointer), creating it on first use.
func (e *Emitter) emitFnPropsBag(rec string) string {
	e.ensureFnMeta()
	e.ensureDynObj()
	e.ensureDynJSONC() // a function with properties renders them through the object inspector
	e.ensureFnBagSeed()
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fn_props_dyn_make(ptr %s, ptr @__kml_dynobj_new, ptr @__kml_dynobj_get, ptr @__kml_fn_bag_seed)", bag, rec))
	return bag
}

// ensureFnBagSeed declares @__kml_fn_bag_seed(bag, rec) (fnmeta.c): a
// function's new property bag starts with its own `length` and `name`.
func (e *Emitter) ensureFnBagSeed() {
	if e.fnDecls["__kml_fn_bag_seed"] {
		return
	}
	e.fnDecls["__kml_fn_bag_seed"] = true
	e.emitGlobal("declare void @__kml_fn_bag_seed(ptr, ptr)")
}

// FnMetaCFlags passes the NaN-box double offset and the configurable
// attribute bit fnmeta.c's bag seed uses, and the null and undefined words.
func (e *Emitter) FnMetaCFlags() []string {
	return []string{
		"-DKML_NB_DOUBLE_OFFSET=" + strconv.FormatInt(nbDoubleOffset, 10) + "LL",
		"-DKML_DYN_ATTR_CONFIGURABLE=" + strconv.Itoa(dynAttrConfigurable),
		"-DKML_NB_NULL=" + strconv.FormatInt(nbNull, 10) + "LL",
		"-DKML_NB_UNDEFINED=" + strconv.FormatInt(nbUndefined, 10) + "LL",
	}
}

// UsesFnMeta reports whether fnmeta.c must be linked.
func (e *Emitter) UsesFnMeta() bool { return e.usedFnMeta }

// emitFnMetaFinalize registers the code-pointer → metadata rows fnmeta.c
// looks up.
// Only a program that reached ensureFnMeta pays for it.
func (e *Emitter) emitFnMetaFinalize() {
	if !e.usedFnMeta {
		return
	}
	// Registered rows keyed by the code pointer (unitreg.go); fnmeta.c
	// finds a function's row there. The registry links even with no rows.
	e.ensureUnitReg()
	for _, m := range e.fnMetas {
		src := "null"
		if m.src != "" {
			src = e.internString(m.src)
		}
		e.curDynFile = m.island
		e.addUnitRowLit(unitKindFnMeta, "{ i64, ptr, i64, i64, ptr }", fmt.Sprintf("{ i64 ptrtoint (ptr %s to i64), ptr %s, i64 %d, i64 %d, ptr %s }",
			m.sym, e.internString(m.name), m.length, m.kind, src))
		e.curDynFile = ""
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
	e.objLitMethods = map[ast.Node]bool{}
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
			if lit.Decl != nil && anonymousClass(lit) {
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
							// A function (not an arrow) called as the object's
							// method: Node's frame names its receiver.
							if fe, ok := p.Value.(*ast.FunctionExpression); ok {
								e.objLitMethods[fe] = true
							}
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
			switch el := v.Interface().(type) {
			case ast.ArrayPatternElem:
				if el.Default != nil && el.Name != "" && el.SubArray == nil && el.SubObject == nil {
					name(el.Default, unmangleTopLevelName(el.Name))
				}
			case ast.DestructProp:
				if el.Default != nil && el.Local != "" && el.SubArray == nil && el.SubObject == nil {
					name(el.Default, unmangleTopLevelName(el.Local))
				}
			}
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
	// A generator's destructured parameters, already moved into the
	// statements its start runs (desugarGeneratorPatternParams).
	for _, sts := range e.genPatternInits {
		walk(reflect.ValueOf(sts))
	}
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
	if !v.Ty.Nullable() && !v.Ty.IsUndefined() {
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

// paramDefaultType is TypeScript's type for an unannotated parameter that has
// a literal default (`p = '!'` is a string, `n = 0` a number, `b = true` a
// boolean, `xs = [1]` a number[]): the widened type of the initializer.
// A module-level binding's type also counts; anything else leaves the
// caller's own default in place (it may reference sibling parameters, not yet
// in scope).
func (e *Emitter) paramDefaultType(p ast.Param) (Type, bool) {
	if (p.ArrayPattern != nil || p.ObjectPattern != nil) && p.Type == nil && p.Default != nil && !p.Rest && !e.compatJS() {
		return e.patternDefaultType(p)
	}
	if p.Type != nil || p.Default == nil || p.Rest || p.ArrayPattern != nil || p.ObjectPattern != nil {
		return Type{}, false
	}
	// JavaScript binds whatever is passed (`f(null)`): the call sites decide.
	if e.compatJS() {
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
		if t := e.inferArrayType(d); t.IsArray() && t.ElemType != nil {
			return t, true
		}
	case *ast.ObjectLiteral:
		if t := e.inferObjectType(d); t.IsObject() && !t.IsDynamicObject() {
			return t, true
		}
	case *ast.Identifier:
		// A module-level binding's type is fixed before any function is
		// emitted (TDD-00093), so `c = SUFFIX` takes SUFFIX's type.
		if g, ok := e.moduleGlobals[d.Name]; ok && g.Ty.IR != "" && !g.Ty.IsDynamic() {
			return g.Ty, true
		}
	}
	// Any other default (`s = "x" + n`): the primitive the checker gives it,
	// as TypeScript types the parameter by its default.
	if c := e.front(); c != nil && !e.compatJS() {
		if t := c.TypeOf(p.Default); !c.Unanswered(t) && t.Flags&(checker.Union|checker.Any|checker.Unknown) == 0 {
			switch {
			case t.Flags&(checker.String|checker.StringLiteral) != 0:
				return TypePtr, true
			case t.Flags&(checker.Number|checker.NumberLiteral) != 0 && !t.AnyArith:
				return TypeF64, true
			case t.Flags&(checker.Boolean|checker.BooleanLiteral) != 0:
				return TypeBool, true
			}
			// An object, function or array default (`a = console.log`,
			// `a = v`): the checker's type as the oracle represents it, a
			// box when none holds it — never the number fallback a value
			// of another kind cannot convert to.
			if t.Flags&checker.Object != 0 {
				if rt, ok := e.repr().of(t); ok && rt.IR != "" && rt.IR != "void" {
					return rt, true
				}
				return TypeAny, true
			}
		}
	}
	return Type{}, false
}

// patternDefaultType is an unannotated destructured parameter's type from
// its default, as the checker gives it: an array pattern over an array
// literal is the tuple of its widened elements (`[x, y] = [1, "a"]` is
// [number, string]), held as an array when they are of one kind; an object
// pattern takes the literal's object type.
func (e *Emitter) patternDefaultType(p ast.Param) (Type, bool) {
	// The checker's type, mapped as the oracle maps it, when it has one.
	// An object pattern keeps the literal's own layout below: a defaulted
	// key the argument omits must read absent, which the optional field of
	// a layout from `{ a = 1 } = {}` does not hold for a number.
	if c := e.front(); c != nil {
		if ct := c.PatternDefaultType(p); !c.Unanswered(ct) {
			if t, ok := e.repr().of(ct); ok && !t.IsDynamic() {
				return t, true
			}
		}
	}
	switch d := p.Default.(type) {
	case *ast.ArrayLiteral:
		if p.ArrayPattern == nil || len(d.Elements) == 0 {
			return Type{}, false
		}
		elems := make([]Type, 0, len(d.Elements))
		for _, x := range d.Elements {
			if _, spread := x.(*ast.SpreadElement); spread {
				return Type{}, false
			}
			et := e.inferExprType(x)
			if et.IsStrLiteral() {
				et = TypePtr
			}
			elems = append(elems, et)
		}
		// An inferred tuple of one element kind is the array codegen
		// builds (reprCtx.of); mixed kinds keep their positions.
		for _, et := range elems[1:] {
			if reprKey(et) != reprKey(elems[0]) {
				return TupleType(elems), true
			}
		}
		if t := e.inferArrayType(d); t.IsArray() && t.ElemType != nil {
			return t, true
		}
		return Type{}, false
	case *ast.ObjectLiteral:
		if t := e.inferObjectType(d); p.ObjectPattern != nil && t.IsObject() && !t.IsDynamicObject() {
			return t, true
		}
	}
	return Type{}, false
}
