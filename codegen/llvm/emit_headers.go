// emit_headers.go — the native side of Headers (TDD-00237 Stage 3).
// Headers is a class of the global module lib/node/kml_headers.ts. Request,
// Response and fetch are native: they hold a Headers and use it through its
// own members, called here from synthesized source:
//
//	new Headers(init)    a Request's, Response's or fetch's init.headers
//	h.has / h.set        the implied Content-Type and a redirect's Location
//	h.#pairs()           the list fetch sends, [name, value, …]
//	h.#fillRaw(raw)      a fetched response's headers, from libcurl's text
//	h.#seal(guard)       the guard, once the native side has filled the list
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
	"KlainMainLang/sema"
)

// headersClass is the program's Headers class. The resolver brings the
// module into any program naming Request, Response or fetch
// (lib.NativeGlobalUses).
func (e *Emitter) headersClass(pos ast.Pos) (Type, error) {
	cls, ok := e.globalClass("Headers")
	if !ok {
		return Type{}, fmt.Errorf("%d:%d: internal: the Headers class is not linked", pos.Line, pos.Col)
	}
	return cls, nil
}

// bindValue names an already evaluated v for synthesized source: a fresh
// internal binding whose slot holds it.
func (e *Emitter) bindValue(v Value, pos ast.Pos) ast.Expression {
	name := "__kml_bound_" + e.freshReg()[1:]
	if v.Ty.IsArray {
		e.define(name, Symbol{Ptr: e.newArrayHeaderSlotFromAggregate(v), Ty: v.Ty})
		return ast.NewIdentifier(name, pos)
	}
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, v.Ty.IR))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", v.Ty.IR, v.Ref, slot))
	e.define(name, Symbol{Ptr: slot, Ty: v.Ty})
	return ast.NewIdentifier(name, pos)
}

// emitSynthExpr emits source the compiler wrote itself, prepared as sema
// prepares a program.
func (e *Emitter) emitSynthExpr(x ast.Expression) (Value, error) {
	wrap := &ast.Program{Body: []ast.Statement{ast.NewExpressionStatement(x, x.GetPos())}}
	if err := sema.Prepare(wrap); err != nil {
		return Value{}, err
	}
	return e.emitExpr(wrap.Body[0].(*ast.ExpressionStatement).Expr)
}

// emitNewHeaders is `new Headers(init)`, or `new Headers()` for a nil init.
func (e *Emitter) emitNewHeaders(init *Value, pos ast.Pos) (Value, error) {
	cls, err := e.headersClass(pos)
	if err != nil {
		return Value{}, err
	}
	var args []ast.Expression
	if init != nil {
		args = append(args, e.bindValue(*init, pos))
	}
	return e.emitSynthExpr(ast.NewNewExpression(cls.ClassName, args, pos))
}

// headersMember is `h.method` for synthesized source.
func (e *Emitter) headersMember(h Value, method string, pos ast.Pos) ast.Expression {
	return ast.NewMemberExpression(e.bindValue(h, pos), method, pos)
}

// emitHeadersMethod is `h.method(args…)`.
func (e *Emitter) emitHeadersMethod(h Value, method string, pos ast.Pos, args ...ast.Expression) (Value, error) {
	return e.emitSynthExpr(ast.NewCallExpression(e.headersMember(h, method, pos), args, pos))
}

// emitHeadersSeal sets h's guard.
func (e *Emitter) emitHeadersSeal(h Value, guard string, pos ast.Pos) error {
	_, err := e.emitHeadersMethod(h, "#seal", pos, ast.NewStringLiteral(guard, pos))
	return err
}

// emitSetHeaderIfAbsent sets header name to the string value unless h has
// it, or value is null.
func (e *Emitter) emitSetHeaderIfAbsent(h Value, name, value string, pos ast.Pos) error {
	setL, doneL := e.freshLabel("hdr.set"), e.freshLabel("hdr.done")
	checkL := e.freshLabel("hdr.check")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(value), doneL, checkL))
	e.emitLabel(checkL)
	has, err := e.emitHeadersMethod(h, "has", pos, ast.NewStringLiteral(name, pos))
	if err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.coerce(has, TypeBool).Ref, doneL, setL))
	e.emitLabel(setL)
	v := e.bindValue(Value{Ref: value, Ty: TypePtr}, pos)
	if _, err := e.emitHeadersMethod(h, "set", pos, ast.NewStringLiteral(name, pos), v); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	return nil
}

// emitHeadersSlist is the curl_slist of h's list, as fetch sends it.
func (e *Emitter) emitHeadersSlist(h Value, pos ast.Pos) (string, error) {
	pairs := ast.NewCallExpression(e.headersMember(h, "#pairs", pos), nil, pos)
	data, n, _, err := e.resolveArrayForHOF(pairs, pos)
	if err != nil {
		return "", err
	}
	count := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sdiv i64 %s, 2", count, n))
	return e.buildCurlSlist(count, func(i string) (string, string) {
		ni, vi := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 2", ni, i))
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", vi, ni))
		return e.loadPtrAt(data, ni), e.loadPtrAt(data, vi)
	})
}

// loadPtrAt is the ptr element i of the array data.
func (e *Emitter) loadPtrAt(data, i string) string {
	gep, v := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", gep, data, i))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", v, gep))
	return v
}

// emitHeadersFromInit is the Headers of a Request's, Response's or fetch's
// init: `new Headers(init.headers)` when init has the member (an absent
// optional one is undefined, an empty list), else an empty Headers.
func (e *Emitter) emitHeadersFromInit(initVal Value, pos ast.Pos) (Value, error) {
	if idx, fieldTy, ok := initVal.Ty.FieldIndex("headers"); ok {
		hv := e.loadFieldValue(initVal, idx, fieldTy)
		return e.emitNewHeaders(&hv, pos)
	}
	return e.emitNewHeaders(nil, pos)
}

// emitResponseHeaders is a Response's headers: its own, or, for a fetched
// response read the first time, a Headers filled from the raw header text
// libcurl captured and stored as its own.
func (e *Emitter) emitResponseHeaders(resp Value, pos ast.Pos) (Value, error) {
	cls, err := e.headersClass(pos)
	if err != nil {
		return Value{}, err
	}
	pendIdx, pendTy, okP := resp.Ty.FieldIndex("__kml_pending")
	hIdx, hTy, okH := resp.Ty.FieldIndex("__kml_headers")
	if !okP || !okH {
		return Value{}, fmt.Errorf("%d:%d: internal: not a Response", pos.Line, pos.Col)
	}
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	own := e.loadFieldValue(resp, hIdx, hTy)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", own.Ref, slot))
	fillL, doneL := e.freshLabel("resp.hdr.fill"), e.freshLabel("resp.hdr.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(own.Ref), fillL, doneL))
	e.emitLabel(fillL)
	h, err := e.emitNewHeaders(nil, pos)
	if err != nil {
		return Value{}, err
	}
	pend := e.loadFieldValue(resp, pendIdx, pendTy)
	e.ensureFetchHeadersRaw()
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fetch_headers_raw(ptr %s)", raw, pend.Ref))
	if _, err := e.emitHeadersMethod(h, "#fillRaw", pos, e.bindValue(Value{Ref: raw, Ty: TypePtr}, pos)); err != nil {
		return Value{}, err
	}
	if err := e.emitHeadersSeal(h, "immutable", pos); err != nil {
		return Value{}, err
	}
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, resp.Ty.StructIR(), resp.Ref, hIdx))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", h.Ref, gep))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", h.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", out, slot))
	return Value{Ref: out, Ty: cls}, nil
}
