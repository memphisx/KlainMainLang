// emit_fetch_request.go — `new Request(url)`/`new Request(url, init)`
// (TDD-00040): a plain heap object (url/method/headers/body, all readable
// via the ordinary object field-access path, exactly like Response/URL),
// reusing fetch(url, init)'s own existing init-field-extraction pattern
// (loadFieldValue, emit_fetch.go) almost verbatim.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitNewRequestExpression builds a FetchRequestType() object: method
// defaults to "GET", headers to an empty Headers, body to null — the same
// defaults real fetch(url) has today when no init is given.
func (e *Emitter) emitNewRequestExpression(ex *ast.NewRequestExpression) (Value, error) {
	urlVal, err := e.emitExpr(ex.URL)
	if err != nil {
		return Value{}, err
	}
	urlVal = e.coerce(urlVal, TypePtr)

	methodVal := Value{Ref: e.internString("GET"), Ty: TypePtr}
	bodyVal := Value{Ref: "null", Ty: TypePtr}

	e.ensureMapStrHelpers()
	emptyHeaders := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", emptyHeaders))
	headersVal := Value{Ref: emptyHeaders, Ty: HeadersType()}

	if ex.Init != nil {
		initVal, err := e.emitExpr(ex.Init)
		if err != nil {
			return Value{}, err
		}
		if !initVal.Ty.IsObject {
			return Value{}, fmt.Errorf("%d:%d: Request's second argument must be an object with an optional method/headers/body field", ex.GetPos().Line, ex.GetPos().Col)
		}
		if idx, fieldTy, ok := initVal.Ty.FieldIndex("method"); ok {
			if !isPlainStringType(fieldTy) {
				return Value{}, fmt.Errorf("%d:%d: Request's init.method must be a string", ex.GetPos().Line, ex.GetPos().Col)
			}
			methodVal = e.loadFieldValue(initVal, idx, fieldTy)
		}
		if idx, fieldTy, ok := initVal.Ty.FieldIndex("headers"); ok {
			if !isHeaderMapType(fieldTy) {
				return Value{}, fmt.Errorf("%d:%d: Request's init.headers must be Map<string, string> or Headers", ex.GetPos().Line, ex.GetPos().Col)
			}
			rawHeaders := e.loadFieldValue(initVal, idx, fieldTy)
			headersVal, err = e.emitHeadersFromMapValue(rawHeaders)
			if err != nil {
				return Value{}, err
			}
		}
		if idx, fieldTy, ok := initVal.Ty.FieldIndex("body"); ok {
			if !isPlainStringType(fieldTy) {
				return Value{}, fmt.Errorf("%d:%d: Request's init.body must be a string", ex.GetPos().Line, ex.GetPos().Col)
			}
			bodyVal = e.loadFieldValue(initVal, idx, fieldTy)
		}
	}

	e.ensureMalloc()
	ty := FetchRequestType()
	objReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", objReg, ty.StructSize()))
	structIR := ty.StructIR()

	storeField := func(name string, val Value) {
		idx, fieldTy, _ := ty.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, objReg, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, val.Ref, gep, fieldTy.Align()))
	}
	storeField("url", urlVal)
	storeField("method", methodVal)
	storeField("headers", headersVal)
	storeField("body", bodyVal)

	return Value{Ref: objReg, Ty: ty}, nil
}
