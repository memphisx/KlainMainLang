package llvm

import (
	"KlainMainLang/ast"
	_ "embed"
	"fmt"
)

// emit_urlpattern.go — `new URLPattern(init)` / .test / .exec (TDD-00100).
// Component patterns compile once at construction inside the embedded C
// runtime (urlpatternsrc/urlpattern.c, the json_parse.c pattern), which
// declares its own pcre2/curl prototypes; input URLs are parsed with the
// same libcurl URL API `new URL(...)` uses.

//go:embed urlpatternsrc/urlpattern.c
var urlPatternSource string

// URLPatternSource returns the C source implementing the __kml_urlpattern_*
// ABI. main.go writes and compiles it (alongside the program, like the JSON
// parse-tree file) when UsesURLPattern() is set.
func URLPatternSource() string { return urlPatternSource }

// UsesURLPattern reports whether any URLPattern construction reached codegen,
// so main.go knows to compile the C runtime in.
func (e *Emitter) UsesURLPattern() bool { return e.usesURLPattern }

// urlPatternComponents maps init-object keys to the component indices the C
// runtime uses (KML_UP_NCOMP order). username/password/baseURL are
// deliberately absent — see TDD-00100's scope.
var urlPatternComponents = []struct {
	name string
	idx  int
}{
	{"protocol", 0}, {"hostname", 1}, {"port", 2},
	{"pathname", 3}, {"search", 4}, {"hash", 5},
}

// ensureURLPattern declares the __kml_urlpattern_* ABI once and marks the
// program as needing urlpattern.c compiled+linked (pcre2 for the compiled
// component regexes, curl for input-URL parsing — both already conditional
// links elsewhere).
func (e *Emitter) ensureURLPattern() {
	e.usesURLPattern = true
	e.requireLink("pcre2-8")
	e.requireLink("curl")
	e.ensureMapStrHelpers() // the C runtime calls __kml_map_str_create/set
	if e.declaredURLPattern {
		return
	}
	e.declaredURLPattern = true
	e.emitGlobal(`declare ptr @__kml_urlpattern_create()`)
	e.emitGlobal(`declare ptr @__kml_urlpattern_set(ptr, i64, ptr)`)
	e.emitGlobal(`declare i1 @__kml_urlpattern_test(ptr, ptr)`)
	e.emitGlobal(`declare ptr @__kml_urlpattern_exec(ptr, ptr)`)
}

// emitNewURLPatternExpression implements `new URLPattern()` / `new
// URLPattern({ pathname: "/books/:id", ... })`. The init must be an object
// literal over the six supported component keys (values may be runtime
// strings); an omitted component defaults to "*". An invalid component
// pattern throws a catchable TypeError at construction, the spec's own error
// type for a bad pattern.
func (e *Emitter) emitNewURLPatternExpression(ex *ast.NewURLPatternExpression) (Value, error) {
	e.ensureURLPattern()
	e.ensureMalloc()
	e.ensureExceptionHelpers()
	pos := ex.GetPos()

	componentExprs := map[string]ast.Expression{}
	if ex.Init != nil {
		obj, ok := ex.Init.(*ast.ObjectLiteral)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: new URLPattern requires an object-literal init ({ pathname: ..., ... }) — the constructor-string form is not supported", pos.Line, pos.Col)
		}
		for _, prop := range obj.Properties {
			if prop.KeyExpr != nil || prop.Key == "" {
				return Value{}, fmt.Errorf("%d:%d: new URLPattern init supports plain named components only (no computed keys or spreads)", pos.Line, pos.Col)
			}
			supported := false
			for _, c := range urlPatternComponents {
				if c.name == prop.Key {
					supported = true
					break
				}
			}
			if !supported {
				return Value{}, fmt.Errorf("%d:%d: new URLPattern: unsupported component %q (supported: protocol, hostname, port, pathname, search, hash)", pos.Line, pos.Col, prop.Key)
			}
			componentExprs[prop.Key] = prop.Value
		}
	}

	handle := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_urlpattern_create()", handle))

	patternRefs := map[string]string{}
	for _, c := range urlPatternComponents {
		var patRef string
		if valExpr, ok := componentExprs[c.name]; ok {
			val, err := e.emitExpr(valExpr)
			if err != nil {
				return Value{}, err
			}
			patRef = e.coerce(val, TypePtr).Ref
		} else {
			patRef = e.internString("*")
		}
		patternRefs[c.name] = patRef

		errPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_urlpattern_set(ptr %s, i64 %d, ptr %s)", errPtr, handle, c.idx, patRef))
		bad := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", bad, errPtr))
		badL := e.freshLabel("urlpat.bad")
		okL := e.freshLabel("urlpat.ok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
		e.emitLabel(badL)
		errObj := e.buildErrorObj(errorKindIDs["TypeError"], errPtr, e.internString("TypeError"))
		e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errObj))
		e.emitTerminator("unreachable")
		e.emitLabel(okL)
	}

	upTy := URLPatternType()
	structIR := upTy.StructIR()
	objReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", objReg, upTy.StructSize()))
	storeField := func(name, ref string) {
		idx, fieldTy, _ := upTy.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, objReg, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, ref, gep, fieldTy.Align()))
	}
	for _, c := range urlPatternComponents {
		storeField(c.name, patternRefs[c.name])
	}
	storeField("__kml_handle", handle)

	return Value{Ref: objReg, Ty: upTy}, nil
}

// emitURLPatternHandle loads the hidden __kml_handle out of a URLPattern
// receiver expression.
func (e *Emitter) emitURLPatternHandle(objExpr ast.Expression) (string, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return "", err
	}
	upTy := URLPatternType()
	idx, fieldTy, _ := upTy.FieldIndex("__kml_handle")
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, upTy.StructIR(), objVal.Ref, idx))
	handle := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align %d", handle, gep, fieldTy.Align()))
	return handle, nil
}

// emitURLPatternTest implements urlPattern.test(url): false for a
// non-matching or unparseable input, never a throw.
func (e *Emitter) emitURLPatternTest(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: URLPattern.test takes exactly 1 argument", pos.Line, pos.Col)
	}
	handle, err := e.emitURLPatternHandle(mem.Object)
	if err != nil {
		return Value{}, err
	}
	urlVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	urlVal = e.coerce(urlVal, TypePtr)
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_urlpattern_test(ptr %s, ptr %s)", res, handle, urlVal.Ref))
	return Value{Ref: res, Ty: TypeBool}, nil
}

// emitURLPatternExec implements urlPattern.exec(url): a Map<string,string>
// of every named group across all components on a match, null otherwise —
// the merged-Map narrowing of the spec's URLPatternResult (TDD-00100).
func (e *Emitter) emitURLPatternExec(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: URLPattern.exec takes exactly 1 argument", pos.Line, pos.Col)
	}
	handle, err := e.emitURLPatternHandle(mem.Object)
	if err != nil {
		return Value{}, err
	}
	urlVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	urlVal = e.coerce(urlVal, TypePtr)
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_urlpattern_exec(ptr %s, ptr %s)", res, handle, urlVal.Ref))
	return Value{Ref: res, Ty: MapType(TypePtr, TypePtr)}, nil
}
