package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emit_usp.go — URLSearchParams as an ordered name/value pair list (TDD-00203),
// backed by the __kml_usp_* C runtime (emit_usp_runtime.go / urlsearchparams.c).
// Replaces the former Map<string,string> backing so duplicate keys and cross-key
// insertion order round-trip faithfully (`?a=1&b=2&a=3` → getAll("a") == ["1","3"]).
// The full method surface, iteration (for-of/spread), .size, and toString are
// re-owned here against that ABI rather than inherited from Map machinery.

// resolveUSPHandle evaluates a URLSearchParams-typed expression to its handle
// pointer (the value IS the handle, a bare ptr — see URLSearchParamsType).
func (e *Emitter) resolveUSPHandle(objExpr ast.Expression) (string, error) {
	v, err := e.emitExpr(objExpr)
	if err != nil {
		return "", err
	}
	return e.coerce(v, TypePtr).Ref, nil
}

// emitNewURLSearchParamsExpression implements `new URLSearchParams(init)` for
// every init form: absent/"" (empty), a query string (leading '?' tolerated),
// an array of [name, value] pairs, a record object, or another URLSearchParams
// (copied). The result is the pair-list handle.
func (e *Emitter) emitNewURLSearchParamsExpression(ex *ast.NewURLSearchParamsExpression) (Value, error) {
	e.ensureURLSearchParams()
	handle := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_usp_create()", handle))
	if ex.Init == nil {
		return Value{Ref: handle, Ty: URLSearchParamsType()}, nil
	}

	switch init := ex.Init.(type) {
	case *ast.ArrayLiteral:
		// [[k,v], [k,v], …] — append each pair in order.
		for _, el := range init.Elements {
			pair, ok := el.(*ast.ArrayLiteral)
			if !ok || len(pair.Elements) != 2 {
				return Value{}, fmt.Errorf("%d:%d: URLSearchParams array init expects [name, value] pairs", ex.GetPos().Line, ex.GetPos().Col)
			}
			if err := e.uspAppendExprPair(handle, pair.Elements[0], pair.Elements[1]); err != nil {
				return Value{}, err
			}
		}
	case *ast.ObjectLiteral:
		// { k: v, … } — each own property becomes one pair, in source order.
		for _, p := range init.Properties {
			if p.KeyExpr != nil || p.AccessorKind != "" || p.Key == "" {
				return Value{}, fmt.Errorf("%d:%d: URLSearchParams object init supports plain string-keyed properties only", ex.GetPos().Line, ex.GetPos().Col)
			}
			kRef := e.internString(p.Key)
			vVal, err := e.emitExpr(p.Value)
			if err != nil {
				return Value{}, err
			}
			vStr, err := e.emitValueToString(vVal)
			if err != nil {
				return Value{}, err
			}
			e.emitInstr(fmt.Sprintf("call void @__kml_usp_append(ptr %s, ptr %s, ptr %s)", handle, kRef, vStr.Ref))
		}
	default:
		// A URLSearchParams value → copy; otherwise treat as a query string.
		if t := e.inferExprType(ex.Init); t.IsURLSearchParams {
			src, err := e.resolveUSPHandle(ex.Init)
			if err != nil {
				return Value{}, err
			}
			e.uspCopyInto(handle, src)
		} else {
			val, err := e.emitExpr(ex.Init)
			if err != nil {
				return Value{}, err
			}
			stripped, err := e.emitStripLeadingQuestionMark(e.coerce(val, TypePtr))
			if err != nil {
				return Value{}, err
			}
			e.emitInstr(fmt.Sprintf("call void @__kml_usp_parse(ptr %s, ptr %s)", handle, stripped.Ref))
		}
	}
	return Value{Ref: handle, Ty: URLSearchParamsType()}, nil
}

// uspAppendExprPair evaluates two expressions to strings and appends them.
func (e *Emitter) uspAppendExprPair(handle string, kExpr, vExpr ast.Expression) error {
	kVal, err := e.emitExpr(kExpr)
	if err != nil {
		return err
	}
	kStr, err := e.emitValueToString(kVal)
	if err != nil {
		return err
	}
	vVal, err := e.emitExpr(vExpr)
	if err != nil {
		return err
	}
	vStr, err := e.emitValueToString(vVal)
	if err != nil {
		return err
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_usp_append(ptr %s, ptr %s, ptr %s)",
		handle, kStr.Ref, vStr.Ref))
	return nil
}

// uspCopyInto appends every pair of src into dst (a runtime loop over the
// source's size), used by the copy-construct form.
func (e *Emitter) uspCopyInto(dst, src string) {
	idx := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idx))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idx))
	sz := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_usp_size(ptr %s)", sz, src))
	condL := e.freshLabel("uspcopy.cond")
	bodyL := e.freshLabel("uspcopy.body")
	doneL := e.freshLabel("uspcopy.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, idx))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, i, sz))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))
	e.emitLabel(bodyL)
	k := e.freshReg()
	v := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_usp_key_at(ptr %s, i64 %s)", k, src, i))
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_usp_val_at(ptr %s, i64 %s)", v, src, i))
	e.emitInstr(fmt.Sprintf("call void @__kml_usp_append(ptr %s, ptr %s, ptr %s)", dst, k, v))
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, idx))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
}

// buildURLSearchParamsFromQuery constructs a searchParams handle from a raw
// query string (curl's decoded query, no leading '?'), guarded by `present`
// (an i1) — an absent query yields an empty list. Used by URL construction.
func (e *Emitter) buildURLSearchParamsFromQuery(queryRaw, present string) string {
	e.ensureURLSearchParams()
	handle := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_usp_create()", handle))
	parseL := e.freshLabel("usp.fromquery.parse")
	doneL := e.freshLabel("usp.fromquery.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", present, parseL, doneL))
	e.emitLabel(parseL)
	e.emitInstr(fmt.Sprintf("call void @__kml_usp_parse(ptr %s, ptr %s)", handle, queryRaw))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	return handle
}

// emitURLSearchParamsCall dispatches every URLSearchParams method to the
// __kml_usp_* ABI. Returns (Value, handled, error): handled=false lets the caller
// fall through (no such method) with an accurate error.
func (e *Emitter) emitURLSearchParamsCall(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, bool, error) {
	e.ensureURLSearchParams()
	handle, err := e.resolveUSPHandle(objExpr)
	if err != nil {
		return Value{}, true, err
	}
	// Names and values are stringified (JS coerces `params.append('n', 1)` to
	// "1"); a bare coerce-to-ptr would treat a number's f64 as a pointer (invalid
	// IR). emitValueToString handles number/bool/string/etc uniformly.
	arg := func(i int) (string, error) {
		v, err := e.emitExpr(args[i])
		if err != nil {
			return "", err
		}
		s, err := e.emitValueToString(v)
		if err != nil {
			return "", err
		}
		return s.Ref, nil
	}
	switch method {
	case "append":
		if len(args) != 2 {
			return Value{}, true, fmt.Errorf("%d:%d: URLSearchParams.append(name, value) takes 2 arguments", pos.Line, pos.Col)
		}
		k, err := arg(0)
		if err != nil {
			return Value{}, true, err
		}
		v, err := arg(1)
		if err != nil {
			return Value{}, true, err
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_usp_append(ptr %s, ptr %s, ptr %s)", handle, k, v))
		return Value{Ty: TypeVoid}, true, nil
	case "set":
		if len(args) != 2 {
			return Value{}, true, fmt.Errorf("%d:%d: URLSearchParams.set(name, value) takes 2 arguments", pos.Line, pos.Col)
		}
		k, err := arg(0)
		if err != nil {
			return Value{}, true, err
		}
		v, err := arg(1)
		if err != nil {
			return Value{}, true, err
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_usp_set(ptr %s, ptr %s, ptr %s)", handle, k, v))
		return Value{Ty: TypeVoid}, true, nil
	case "get":
		if len(args) != 1 {
			return Value{}, true, fmt.Errorf("%d:%d: URLSearchParams.get(name) takes 1 argument", pos.Line, pos.Col)
		}
		k, err := arg(0)
		if err != nil {
			return Value{}, true, err
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_usp_get(ptr %s, ptr %s)", r, handle, k))
		// Absent → null ptr, which the string machinery treats as null (ADR-00724);
		// a string value is a bare ptr here, so this is a `string | null`.
		return Value{Ref: r, Ty: TypePtr}, true, nil
	case "getAll":
		if len(args) != 1 {
			return Value{}, true, fmt.Errorf("%d:%d: URLSearchParams.getAll(name) takes 1 argument", pos.Line, pos.Col)
		}
		k, err := arg(0)
		if err != nil {
			return Value{}, true, err
		}
		out := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca {ptr, i64}, align 8", out))
		e.emitInstr(fmt.Sprintf("call void @__kml_usp_get_all(ptr %s, ptr %s, ptr %s)", out, handle, k))
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load {ptr, i64}, ptr %s, align 8", r, out))
		return Value{Ref: r, Ty: ArrayOf(TypePtr)}, true, nil
	case "has":
		// WHATWG has(name[, value]) — the optional value narrows the match.
		if len(args) < 1 || len(args) > 2 {
			return Value{}, true, fmt.Errorf("%d:%d: URLSearchParams.has(name[, value]) takes 1 or 2 arguments", pos.Line, pos.Col)
		}
		k, err := arg(0)
		if err != nil {
			return Value{}, true, err
		}
		i32 := e.freshReg()
		b := e.freshReg()
		if len(args) == 2 {
			v, err := arg(1)
			if err != nil {
				return Value{}, true, err
			}
			e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_usp_has2(ptr %s, ptr %s, ptr %s)", i32, handle, k, v))
		} else {
			e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_usp_has(ptr %s, ptr %s)", i32, handle, k))
		}
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", b, i32))
		return Value{Ref: b, Ty: TypeBool}, true, nil
	case "delete":
		// WHATWG delete(name[, value]) — the optional value narrows the removal.
		if len(args) < 1 || len(args) > 2 {
			return Value{}, true, fmt.Errorf("%d:%d: URLSearchParams.delete(name[, value]) takes 1 or 2 arguments", pos.Line, pos.Col)
		}
		k, err := arg(0)
		if err != nil {
			return Value{}, true, err
		}
		if len(args) == 2 {
			v, err := arg(1)
			if err != nil {
				return Value{}, true, err
			}
			e.emitInstr(fmt.Sprintf("call void @__kml_usp_delete2(ptr %s, ptr %s, ptr %s)", handle, k, v))
		} else {
			e.emitInstr(fmt.Sprintf("call void @__kml_usp_delete(ptr %s, ptr %s)", handle, k))
		}
		return Value{Ty: TypeVoid}, true, nil
	case "sort":
		e.emitInstr(fmt.Sprintf("call void @__kml_usp_sort(ptr %s)", handle))
		return Value{Ty: TypeVoid}, true, nil
	case "toString":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_usp_to_string(ptr %s)", r, handle))
		return Value{Ref: r, Ty: TypePtr}, true, nil
	case "keys":
		return e.uspArray(handle, "__kml_usp_keys", ArrayOf(TypePtr)), true, nil
	case "values":
		return e.uspArray(handle, "__kml_usp_values", ArrayOf(TypePtr)), true, nil
	case "entries":
		return e.uspArray(handle, "__kml_usp_entries", ArrayOf(TupleType([]Type{TypePtr, TypePtr}))), true, nil
	case "forEach":
		if len(args) != 1 {
			return Value{}, true, fmt.Errorf("%d:%d: URLSearchParams.forEach(cb) takes 1 argument", pos.Line, pos.Col)
		}
		cb, err := e.resolveCallbackWithHints(args[0], []Type{TypePtr, TypePtr, URLSearchParamsType()})
		if err != nil {
			return Value{}, true, err
		}
		v, err := e.emitURLSearchParamsForEach(handle, cb)
		return v, true, err
	}
	return Value{}, false, nil
}

// uspArray calls a {ptr,i64}-returning __kml_usp_* accessor and wraps it as the
// given array type.
func (e *Emitter) uspArray(handle, cfunc string, ty Type) Value {
	// The C writes the {ptr,i64} result through an out-parameter (ABI-safe on
	// Windows x64; see ensureURLSearchParams' declares) — alloca, call, load.
	out := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca {ptr, i64}, align 8", out))
	e.emitInstr(fmt.Sprintf("call void @%s(ptr %s, ptr %s)", cfunc, out, handle))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load {ptr, i64}, ptr %s, align 8", r, out))
	return Value{Ref: r, Ty: ty}
}

// emitURLSearchParamsForEach loops the pair list, calling cb(value, name, params).
func (e *Emitter) emitURLSearchParamsForEach(handle string, cb Callback) (Value, error) {
	idx := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idx))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idx))
	sz := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_usp_size(ptr %s)", sz, handle))
	condL := e.freshLabel("uspforeach.cond")
	bodyL := e.freshLabel("uspforeach.body")
	doneL := e.freshLabel("uspforeach.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, idx))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, i, sz))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))
	e.emitLabel(bodyL)
	k := e.freshReg()
	v := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_usp_key_at(ptr %s, i64 %s)", k, handle, i))
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_usp_val_at(ptr %s, i64 %s)", v, handle, i))
	cbArgs := []Value{{Ref: v, Ty: TypePtr}}
	if cb.arity() >= 2 {
		cbArgs = append(cbArgs, Value{Ref: k, Ty: TypePtr})
	}
	if cb.arity() >= 3 {
		cbArgs = append(cbArgs, Value{Ref: handle, Ty: URLSearchParamsType()})
	}
	if _, err := e.emitCBCall(cb, cbArgs); err != nil {
		return Value{}, err
	}
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, idx))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
	return Value{Ty: TypeVoid}, nil
}

// emitURLSearchParamsSize implements the `.size` getter → the pair count as a
// number (f64, this compiler's number type).
func (e *Emitter) emitURLSearchParamsSize(objExpr ast.Expression) (Value, error) {
	handle, err := e.resolveUSPHandle(objExpr)
	if err != nil {
		return Value{}, err
	}
	i64 := e.freshReg()
	f := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_usp_size(ptr %s)", i64, handle))
	e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", f, i64))
	return Value{Ref: f, Ty: TypeF64}, nil
}
