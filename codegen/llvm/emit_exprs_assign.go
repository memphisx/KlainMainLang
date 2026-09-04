package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// isLogicalAssignOp reports whether op is one of the three logical
// assignment operators (&&=, ||=, ??=) — genuinely short-circuiting, unlike
// every other compound-assignment operator, so they can't reuse emitArith's
// eager-evaluate-both-sides shape at all (see emitLogicalCompoundAssign).
func isLogicalAssignOp(op string) bool {
	return op == "&&=" || op == "||=" || op == "??="
}

// tryEmitAccessorAssign attempts `obj.prop = rhs` / `obj.prop OP= rhs`
// against a registered class accessor (TDD-00030). handled is false when
// objVal's class has no accessor at all for this property name — the
// caller should fall through to its own existing FieldIndex-based
// plain-field path unchanged, exactly as if this function had never been
// called.
func (e *Emitter) tryEmitAccessorAssign(objVal Value, prop, op string, rhsExpr ast.Expression, pos ast.Pos) (handled bool, result Value, err error) {
	getter, setter, ok := e.classAccessorSigs(objVal.Ty.ClassName, prop)
	if !ok {
		return false, Value{}, nil
	}
	if setter == nil {
		return true, Value{}, fmt.Errorf("%d:%d: property '%s' has no setter", pos.Line, pos.Col, prop)
	}
	// emitLogicalCompoundAssign is generic over "any assignable ptr+Type
	// lvalue" specifically because it can cheaply re-load the same memory
	// location across its short-circuit branches — an accessor has no such
	// location, and invoking a getter/setter more than once per
	// source-level use risks an observable double-invocation of user code
	// with side effects. Not attempted for V1 — see docs/tdd/TDD-00030.md.
	if isLogicalAssignOp(op) {
		return true, Value{}, fmt.Errorf("%d:%d: logical assignment ('%s') on a getter/setter property is not yet supported", pos.Line, pos.Col, op)
	}

	paramTy := setter.ParamTypes[0]
	var rhs Value
	if op == "=" {
		// Hint-aware (TDD-00028/TDD-00007): `obj.prop = [1,2,3]`/`obj.prop
		// = {...}` coerces against the setter's own declared parameter type.
		rhs, err = e.emitExprWithObjectHint(rhsExpr, paramTy)
		if err != nil {
			return true, Value{}, err
		}
	} else {
		if getter == nil {
			return true, Value{}, fmt.Errorf("%d:%d: property '%s' has no getter (required to read the current value for '%s')", pos.Line, pos.Col, prop, op)
		}
		cur, err := e.emitClassCall(objVal.Ty, objVal, accessorMethodName("get", prop), nil, pos, false)
		if err != nil {
			return true, Value{}, err
		}
		rhsVal, err := e.emitExpr(rhsExpr)
		if err != nil {
			return true, Value{}, err
		}
		if err := dateCompoundAssignGuard(op, paramTy.IsDate, rhsVal.Ty.IsDate); err != nil {
			return true, Value{}, fmt.Errorf("%d:%d: %s", pos.Line, pos.Col, err)
		}
		rhsVal = e.coerce(rhsVal, paramTy)
		rhs, err = e.emitArith(strings.TrimSuffix(op, "="), cur, rhsVal, paramTy, pos)
		if err != nil {
			return true, Value{}, err
		}
	}
	rhs = e.coerce(rhs, paramTy)
	if _, err := e.emitClassSetterCall(objVal.Ty, objVal, accessorMethodName("set", prop), rhs, pos); err != nil {
		return true, Value{}, err
	}
	return true, rhs, nil
}

// emitLogicalCompoundAssign implements &&=/||=/??= against an lvalue whose
// storage is already resolved to a single ptr+Type pair — the shape shared
// by every assignable form this compiler has (scalar variable, array
// element, object field, static field). The right side is only evaluated
// (and only then stored) down the branch where the operator's own
// short-circuit rule requires it; the other branch leaves the existing
// value at ptr untouched. Reloading from ptr at the merge point yields the
// correct final value either way, so no separate result temporary is
// needed — unlike emitNullCoalesce/emitConditional, which need one since
// their two branches produce a value that was never already sitting in a
// shared memory location. `??=` against a non-ptr-typed location can never
// trigger (mirrors emitNullCoalesce's own "left can never be null" fast
// path for non-ptr types) — the right side is never evaluated, exactly like
// bare `x ?? y`.
func (e *Emitter) emitLogicalCompoundAssign(op, ptr string, ty Type, rhsExpr ast.Expression) (Value, error) {
	curReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", curReg, ty.IR, ptr, ty.Align()))
	cur := Value{Ref: curReg, Ty: ty}

	if op == "??=" && ty.IR != "ptr" {
		return cur, nil
	}

	var cond Value
	switch op {
	case "&&=":
		cond = e.toBool(cur)
	case "||=":
		notReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notReg, e.toBool(cur).Ref))
		cond = Value{Ref: notReg, Ty: TypeBool}
	case "??=":
		nullReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", nullReg, cur.Ref))
		cond = Value{Ref: nullReg, Ty: TypeBool}
	default:
		return Value{}, fmt.Errorf("unknown logical assignment operator '%s'", op)
	}

	storeL := e.freshLabel("logassign.store")
	mergeL := e.freshLabel("logassign.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond.Ref, storeL, mergeL))

	e.emitLabel(storeL)
	rhs, err := e.emitExpr(rhsExpr)
	if err != nil {
		return Value{}, err
	}
	rhs = e.coerce(rhs, ty)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, rhs.Ref, ptr, ty.Align()))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, ty.IR, ptr, ty.Align()))
	return Value{Ref: result, Ty: ty}, nil
}

func (e *Emitter) emitAssign(ex *ast.AssignmentExpression) (Value, error) {
	// A namespace-member assignment (`X.member = v` / `A.B.member = v`,
	// TDD-00148/ADR-00474) rewrites to an assignment to the desugared flat
	// binding, mirroring the call/value-read dispatch in emitCall/emitMember.
	if memEx, ok := ex.Left.(*ast.MemberExpression); ok {
		if id, isID := memEx.Object.(*ast.Identifier); isID {
			if members, nsName := e.namespaceMembers(id.Name); members != nil && !e.isShadowedByLocal(id.Name) {
				if exported, present := members[memEx.Property]; present {
					if !exported && e.curNamespace != nsName {
						return Value{}, fmt.Errorf("%d:%d: '%s.%s' is not exported from namespace '%s'", ex.GetPos().Line, ex.GetPos().Col, id.Name, memEx.Property, nsName)
					}
					rewritten := ast.NewAssignmentExpression(ex.Op, ast.NewIdentifier(ast.NamespaceMangle(nsName, memEx.Property), ex.GetPos()), ex.Right, ex.GetPos())
					return e.emitAssign(rewritten)
				}
			}
		} else if members, nsName := e.namespaceByChain(memEx.Object); members != nil {
			if exported, present := members[memEx.Property]; present {
				if !exported && e.curNamespace != nsName {
					return Value{}, fmt.Errorf("%d:%d: '%s.%s' is not exported from namespace '%s'", ex.GetPos().Line, ex.GetPos().Col, nsName, memEx.Property, nsName)
				}
				rewritten := ast.NewAssignmentExpression(ex.Op, ast.NewIdentifier(ast.NamespaceMangle(nsName, memEx.Property), ex.GetPos()), ex.Right, ex.GetPos())
				return e.emitAssign(rewritten)
			}
		}
	}

	// TDD-00098 stage 6, browser Worker surface. Parent side:
	// `w.onmessage = ...` / `w.onerror = ...` on a Worker-typed receiver.
	// Worker side: a bare (or self.) `onmessage = ...` at the module top
	// level. All gated behind shadowing checks so a user binding wins.
	if ex.Op == "=" {
		if memEx, ok := ex.Left.(*ast.MemberExpression); ok {
			if memEx.Property == "onmessage" || memEx.Property == "onerror" {
				if id, ok := memEx.Object.(*ast.Identifier); ok && id.Name == "self" && !e.isShadowedByLocal("self") && e.currentWorkerMod != "" {
					if memEx.Property == "onerror" {
						return Value{}, fmt.Errorf("%d:%d: self.onerror is not supported inside a worker — an uncaught worker exception is reported to the parent", ex.GetPos().Line, ex.GetPos().Col)
					}
					return e.emitWorkerSideOnMessageAssign(ex.Right, ex.GetPos())
				}
				if e.inferExprType(memEx.Object).IsWorker {
					return e.emitWorkerHandlerAssign(memEx.Object, memEx.Property, ex.Right, ex.GetPos())
				}
				// TDD-00099: BroadcastChannel / MessagePort onmessage.
				if memEx.Property == "onmessage" {
					if objTy := e.inferExprType(memEx.Object); objTy.IsBroadcastChannel || objTy.IsMessagePort {
						return e.emitChanOnMessageAssign(memEx.Object, ex.Right, ex.GetPos())
					}
				}
			}
		}
		if id, ok := ex.Left.(*ast.Identifier); ok && id.Name == "onmessage" && e.currentWorkerMod != "" && !e.isShadowedByLocal("onmessage") {
			if _, bound := e.lookup("onmessage"); !bound {
				return e.emitWorkerSideOnMessageAssign(ex.Right, ex.GetPos())
			}
		}
	}
	// process.exitCode = N → store the deferred exit code (ADR-00334).
	if memEx, ok := ex.Left.(*ast.MemberExpression); ok && memEx.Property == "exitCode" {
		if id, ok := memEx.Object.(*ast.Identifier); ok && id.Name == "process" && !e.isShadowedByLocal(id.Name) {
			if ex.Op != "=" {
				return Value{}, fmt.Errorf("%d:%d: compound assignment to process.exitCode is not supported", ex.GetPos().Line, ex.GetPos().Col)
			}
			val, err := e.emitExpr(ex.Right)
			if err != nil {
				return Value{}, err
			}
			val = e.coerce(val, TypeI64)
			e.usedProcessLifecycle = true
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr @__kml_process_exit_code, align 8", val.Ref))
			return val, nil
		}
	}
	// res.statusCode = N → store into the ServerResponse's `status` field
	// (Node names the property `statusCode`; the object field is `status`).
	// TDD-00131: the imperative alternative to writeHead(status).
	if memEx, ok := ex.Left.(*ast.MemberExpression); ok && memEx.Property == "statusCode" {
		if objTy := e.inferExprType(memEx.Object); objTy.IsServerResponse {
			if ex.Op != "=" {
				return Value{}, fmt.Errorf("%d:%d: compound assignment to res.statusCode is not supported — assign a status code directly", ex.GetPos().Line, ex.GetPos().Col)
			}
			resVal, err := e.emitExpr(memEx.Object)
			if err != nil {
				return Value{}, err
			}
			val, err := e.emitExpr(ex.Right)
			if err != nil {
				return Value{}, err
			}
			val = e.coerce(val, TypeI64)
			ty := ServerResponseType()
			idx, _, _ := ty.FieldIndex("status")
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, ty.StructIR(), resVal.Ref, idx))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", val.Ref, gep))
			return val, nil
		}
	}
	// process.env.KEY = val / process.env["KEY"] = val → setenv (ADR-00333).
	// Checked before the generic member/index assignment paths, since
	// `process.env` is a pseudo-namespace, not a real object. Only plain `=`
	// (a compound op on an env var is rejected — read-modify-write of a
	// possibly-unset getenv result is a footgun better made explicit).
	if memEx, ok := ex.Left.(*ast.MemberExpression); ok && e.isProcessEnvExpr(memEx.Object) {
		if ex.Op != "=" {
			return Value{}, fmt.Errorf("%d:%d: compound assignment to process.env is not supported — read process.env.%s and assign explicitly", ex.GetPos().Line, ex.GetPos().Col, memEx.Property)
		}
		return e.emitProcessEnvSet(e.internString(memEx.Property), ex.Right, ex.GetPos())
	}
	if idxEx, ok := ex.Left.(*ast.IndexExpression); ok && e.isProcessEnvExpr(idxEx.Object) {
		if ex.Op != "=" {
			return Value{}, fmt.Errorf("%d:%d: compound assignment to process.env is not supported", ex.GetPos().Line, ex.GetPos().Col)
		}
		keyVal, err := e.emitExpr(idxEx.Index)
		if err != nil {
			return Value{}, err
		}
		keyVal = e.coerce(keyVal, TypePtr)
		return e.emitProcessEnvSet(keyVal.Ref, ex.Right, ex.GetPos())
	}
	// `F.prototype = value` on a recognized vanilla-JS constructor function
	// (TDD-00155 Stage 4, `-compat=js`) re-points the prototype bag.
	if memEx, ok := ex.Left.(*ast.MemberExpression); ok {
		if id, ok := memEx.Object.(*ast.Identifier); ok && e.compatJS() && e.jsProtoCtor[id.Name] && memEx.Property == "prototype" {
			if ex.Op != "=" {
				return Value{}, fmt.Errorf("%d:%d: compound assignment to a prototype is not supported", ex.GetPos().Line, ex.GetPos().Col)
			}
			rhs, err := e.emitExprWithObjectHint(ex.Right, TypeAny)
			if err != nil {
				return Value{}, err
			}
			return e.emitProtoBagWrite(id.Name, rhs, ex.GetPos())
		}
	}
	// Static field assignment: ClassName.staticField = val (or compound
	// ops) — TDD-00009 Stage 4. A bare class-name identifier is a
	// compile-time namespace, never a real runtime value, so this must be
	// checked before memEx.Object is evaluated generically below (same
	// reasoning the read-side check in emitMember follows).
	if memEx, ok := ex.Left.(*ast.MemberExpression); ok {
		if id, ok := memEx.Object.(*ast.Identifier); ok {
			if info, found := e.classes[id.Name]; found {
				return e.emitStaticFieldAssign(info, id.Name, memEx.Property, ex.Op, ex.Right, ex.GetPos())
			}
		}
	}
	// Dynamic object bracket assignment: obj[expr] = val (or compound ops) —
	// a computed-key object literal is a real Map<string,V>, so this must be
	// checked before emitIndexPtr, which only understands array storage.
	if idxEx, ok := ex.Left.(*ast.IndexExpression); ok {
		if id, ok := idxEx.Object.(*ast.Identifier); ok {
			if sym, found := e.lookup(id.Name); found && sym.Ty.IsDynamicObject {
				mapPtr := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", mapPtr, sym.Ptr))
				return e.emitDynamicObjectAssign(sym.Ty, mapPtr, idxEx.Index, ex.Op, ex.Right, ex.GetPos())
			}
		} else if objTy := e.inferExprType(idxEx.Object); objTy.IsDynamicObject {
			objVal, err := e.emitExpr(idxEx.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitDynamicObjectAssign(objVal.Ty, objVal.Ref, idxEx.Index, ex.Op, ex.Right, ex.GetPos())
		}
	}
	// Bracket assignment on a bare any/unknown base: runtime-keyed write into
	// the D1 dynamic object model (TDD-00155 Stage 1).
	if idxEx, ok := ex.Left.(*ast.IndexExpression); ok {
		if baseTy := e.inferExprType(idxEx.Object); isUnconstrainedDynamic(baseTy) {
			if ex.Op != "=" {
				return Value{}, fmt.Errorf("%d:%d: compound assignment ('%s') on a dynamic property is not yet supported", ex.GetPos().Line, ex.GetPos().Col, ex.Op)
			}
			objVal, err := e.emitExpr(idxEx.Object)
			if err != nil {
				return Value{}, err
			}
			keyRef, err := e.dynAnyKeyRef(idxEx.Index, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
			rhs, err := e.emitExprWithObjectHint(ex.Right, TypeAny)
			if err != nil {
				return Value{}, err
			}
			return e.emitDynAnyMemberSet(objVal, keyRef, rhs, ex.GetPos())
		}
	}
	// Tuple element assignment: t[0] = val (TDD-00066). A tuple is a fixed-shape
	// struct, so a constant index maps to that field — checked before array
	// indexing, whose {ptr,i64} storage a tuple doesn't have.
	if idxEx, ok := ex.Left.(*ast.IndexExpression); ok {
		if tupleTy := e.inferExprType(idxEx.Object); tupleTy.IsTuple {
			return e.emitTupleElemAssign(idxEx, tupleTy, ex.Op, ex.Right)
		}
	}
	// Fixed-object constant-key assignment: `o["a"] = v` / `o[0] = v` on a
	// static-shape object (or class instance) is the same as `o.a = v`
	// (ADR-00608) — rewrite to a member-target assignment so it reuses the
	// accessor / readonly / nullable / frozen machinery unchanged.
	if idxEx, ok := ex.Left.(*ast.IndexExpression); ok {
		if objTy := e.inferExprType(idxEx.Object); objTy.IsObject && !objTy.IsArray && !objTy.IsDynamicObject {
			if key, ok := constObjectKey(idxEx.Index); ok {
				member := ast.NewMemberExpression(idxEx.Object, key, idxEx.GetPos())
				rewritten := ast.NewAssignmentExpression(ex.Op, member, ex.Right, ex.GetPos())
				return e.emitAssign(rewritten)
			}
		}
	}
	// Array element assignment: arr[i] = val  or  arr[i] += val
	if idxEx, ok := ex.Left.(*ast.IndexExpression); ok {
		gepReg, elemTy, err := e.emitIndexPtr(idxEx)
		if err != nil {
			return Value{}, err
		}
		// TDD-00101: BigInt64Array/BigUint64Array/Uint8ClampedArray element
		// stores go through the conversion layer (bigint unwrap / ToUint8Clamp)
		// instead of the plain coerce below.
		if taTy := e.inferExprType(idxEx.Object); taTy.IsTypedArray && (taTy.BigIntElem || taTy.Clamped) {
			if ex.Op != "=" {
				if taTy.BigIntElem {
					// Load the raw i64 element, wrap it into a bigint (signed for
					// BigInt64Array, unsigned for BigUint64Array), apply the op via
					// the bigint runtime, then store the unwrapped result.
					e.ensureBigInt()
					curReg := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align %d", curReg, gepReg, elemTy.Align()))
					fromFn := "__kml_bigint_from_i64"
					if !elemTy.Signed {
						fromFn = "__kml_bigint_from_u64" // BigUint64Array
					}
					curBig := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = call ptr @%s(i64 %s)", curBig, fromFn, curReg))
					rhsVal, err := e.emitExpr(ex.Right)
					if err != nil {
						return Value{}, err
					}
					if !rhsVal.Ty.IsBigInt {
						return Value{}, fmt.Errorf("%d:%d: a BigInt64Array/BigUint64Array element compound assignment needs a bigint right-hand side", ex.GetPos().Line, ex.GetPos().Col)
					}
					res, err := e.emitBigIntBinary(strings.TrimSuffix(ex.Op, "="), Value{Ref: curBig, Ty: BigIntType()}, rhsVal, ex.GetPos())
					if err != nil {
						return Value{}, err
					}
					stored, err := e.coerceTypedArrayStore(res, taTy, ex.GetPos())
					if err != nil {
						return Value{}, err
					}
					e.storeArrayElem(gepReg, elemTy, stored)
					return res, nil
				}
				// Clamped compound: load the u8, apply the op as a plain
				// number, then clamp-store the result.
				curReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", curReg, elemTy.IR, gepReg, elemTy.Align()))
				cur := Value{Ref: curReg, Ty: elemTy}
				rhsVal, err := e.emitExpr(ex.Right)
				if err != nil {
					return Value{}, err
				}
				res, err := e.emitArith(strings.TrimSuffix(ex.Op, "="), e.coerce(cur, TypeI64), e.coerce(rhsVal, TypeI64), TypeI64, ex.GetPos())
				if err != nil {
					return Value{}, err
				}
				stored, err := e.coerceTypedArrayStore(res, taTy, ex.GetPos())
				if err != nil {
					return Value{}, err
				}
				e.storeArrayElem(gepReg, elemTy, stored)
				return stored, nil
			}
			rhsVal, err := e.emitExpr(ex.Right)
			if err != nil {
				return Value{}, err
			}
			stored, err := e.coerceTypedArrayStore(rhsVal, taTy, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
			e.storeArrayElem(gepReg, elemTy, stored)
			return rhsVal, nil
		}
		if isLogicalAssignOp(ex.Op) {
			return e.emitLogicalCompoundAssign(ex.Op, gepReg, elemTy, ex.Right)
		}
		var rhs Value
		if ex.Op == "=" {
			// Hint-aware (TDD-00028): `arr[i] = [1,2,3]` when elemTy is
			// itself array-typed builds/coerces against elemTy instead of
			// erroring — the same reasoning emitExprWithObjectHint already
			// established for object literals (TDD-00007).
			rhs, err = e.emitExprWithObjectHint(ex.Right, elemTy)
			if err != nil {
				return Value{}, err
			}
		} else {
			// Compound: load current element, apply op, store
			curReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", curReg, elemTy.IR, gepReg, elemTy.Align()))
			cur := Value{Ref: curReg, Ty: elemTy}
			rhsVal, err := e.emitExpr(ex.Right)
			if err != nil {
				return Value{}, err
			}
			if err := dateCompoundAssignGuard(ex.Op, elemTy.IsDate, rhsVal.Ty.IsDate); err != nil {
				return Value{}, fmt.Errorf("%d:%d: %s", ex.GetPos().Line, ex.GetPos().Col, err)
			}
			rhsVal = e.coerce(rhsVal, elemTy)
			rhs, err = e.emitArith(strings.TrimSuffix(ex.Op, "="), cur, rhsVal, elemTy, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
		}
		// coerceChecked (not plain coerce): a value that can't convert to the
		// element type — e.g. `const a: number[] = []; a[i] = 'str'` — is a clean
		// compile error, not an aggregate/pointer stored into a scalar slot
		// (invalid IR: "constant expression type mismatch"). ADR-00688. In
		// `-compat=js` the element type is `any`, which every value coerces into
		// (boxes), so this only rejects a genuine strict-mode type mismatch.
		rhs, err = e.coerceChecked(rhs, elemTy, ex.GetPos(), "array-element assignment")
		if err != nil {
			return Value{}, err
		}
		e.storeArrayElem(gepReg, elemTy, rhs)
		return rhs, nil
	}

	// Array variable reassignment: arr = val (the whole array, not
	// arr[i] = val). A separate branch from the generic scalar-variable
	// case below, because this compiler represents an array as two
	// allocas (Ptr/LenPtr — see the project's own Array value duality note), not
	// the single alloca every other assignable form uses; the generic
	// scalar path's single "store %s %s, ptr sym.Ptr" would try to store a
	// whole {ptr,i64} aggregate into sym.Ptr's plain "alloca ptr" slot, a
	// hard clang-stage type mismatch. Found while wiring TDD-00028's
	// array-literal-as-general-expression fix — a real, pre-existing,
	// unrelated bug (arr = otherArrayVar already failed identically before
	// any array-literal changes, confirmed directly).
	if ident, ok := ex.Left.(*ast.Identifier); ok {
		if sym, found := e.lookup(ident.Name); found && sym.Ty.IsArray {
			if ex.Op != "=" {
				return Value{}, fmt.Errorf("%d:%d: compound assignment ('%s') is not supported on an array variable", ex.GetPos().Line, ex.GetPos().Col, ex.Op)
			}
			if sym.IsConst {
				return Value{}, fmt.Errorf("%d:%d: cannot assign to '%s' because it is a constant", ex.GetPos().Line, ex.GetPos().Col, ident.Name)
			}
			// `xs = JSON.parse(s)` / `xs = await res.json()` into an array-typed
			// binding projects against the binding's declared element type — the
			// same type-directed deserialization a typed var-decl gets, extended
			// to a bare reassignment (ADR-00571).
			if val, ok, err := e.emitDeclJSONProjection(ex.Right, sym.Ty); ok {
				if err != nil {
					return Value{}, err
				}
				newHeader := e.boxArrayValue(val)
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newHeader, sym.Ptr))
				return val, nil
			}
			val, err := e.emitExprWithObjectHint(ex.Right, sym.Ty)
			if err != nil {
				return Value{}, err
			}
			if !val.Ty.IsArray {
				return Value{}, fmt.Errorf("%d:%d: cannot assign a non-array value to array variable '%s'", ex.GetPos().Line, ex.GetPos().Col, ident.Name)
			}
			// Object-reference model (TDD-00127): rebind the variable's slot to a
			// fresh header holding the new value. This is a *local* rebind — for
			// a parameter, `a = a.filter(...)` gives this frame a new array
			// without clobbering the caller's (whereas an in-place `a.push(...)`
			// still writes through to the caller). Whole-variable reassignment
			// does not alias, matching the pre-header behaviour.
			newHeader := e.boxArrayValue(val)
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newHeader, sym.Ptr))
			return val, nil
		}
	}

	// Object field assignment: obj.field = val  or  pts[i].field = val  (or compound ops)
	if memEx, ok := ex.Left.(*ast.MemberExpression); ok {
		objVal, err := e.emitExpr(memEx.Object)
		if err != nil {
			return Value{}, err
		}
		if objVal.Ty.IsDynamicObject {
			keyExpr := ast.NewStringLiteral(memEx.Property, memEx.GetPos())
			return e.emitDynamicObjectAssign(objVal.Ty, objVal.Ref, keyExpr, ex.Op, ex.Right, ex.GetPos())
		}
		// Property write on a bare any/unknown base: a runtime tag dispatch
		// into the D1 dynamic object model (TDD-00155 Stage 1).
		if isUnconstrainedDynamic(objVal.Ty) {
			if ex.Op != "=" && !isLogicalAssignOp(ex.Op) {
				// `-compat=js` (TDD-00076 A2): read-modify-write through the
				// runtime dispatch (`this.x *= k`); strict keeps the rejection.
				if !e.compatJS() {
					return Value{}, fmt.Errorf("%d:%d: compound assignment ('%s') on a dynamic property is not yet supported", ex.GetPos().Line, ex.GetPos().Col, ex.Op)
				}
				cur, err := e.emitDynAnyMemberGetNamed(objVal, e.internString(memEx.Property), memEx.Property, ex.GetPos())
				if err != nil {
					return Value{}, err
				}
				rhsVal, err := e.emitExprWithObjectHint(ex.Right, TypeAny)
				if err != nil {
					return Value{}, err
				}
				res, err := e.emitAnyBinary(strings.TrimSuffix(ex.Op, "="), cur, rhsVal, ex.GetPos())
				if err != nil {
					return Value{}, err
				}
				return e.emitDynAnyMemberSetNamed(objVal, e.internString(memEx.Property), memEx.Property, res, ex.GetPos())
			}
			rhs, err := e.emitExprWithObjectHint(ex.Right, TypeAny)
			if err != nil {
				return Value{}, err
			}
			return e.emitDynAnyMemberSetNamed(objVal, e.internString(memEx.Property), memEx.Property, rhs, ex.GetPos())
		}
		if !objVal.Ty.IsObject {
			return Value{}, fmt.Errorf("field assignment on non-object")
		}
		// A URL's components are derived from one parse, so a component setter
		// re-parses the URL and re-derives every field (ADR-00572) rather than a
		// bare field store that would desync them. Compound ops aren't supported.
		if objVal.Ty.IsURL {
			if ex.Op != "=" {
				return Value{}, fmt.Errorf("%d:%d: compound assignment to a URL component ('%s') is not supported", ex.GetPos().Line, ex.GetPos().Col, memEx.Property)
			}
			return e.emitURLComponentSet(objVal, memEx.Property, ex.Right, ex.GetPos())
		}
		// TDD-00030: a class accessor (getter/setter) is checked before the
		// plain-field FieldIndex path below — an accessor-only property
		// name is never a real Field, so FieldIndex would otherwise report
		// "no field" for it. Every non-accessor class, and every non-class
		// object, falls through unchanged.
		if objVal.Ty.IsClass {
			if handled, result, err := e.tryEmitAccessorAssign(objVal, memEx.Property, ex.Op, ex.Right, ex.GetPos()); err != nil {
				return Value{}, err
			} else if handled {
				return result, nil
			}
		}
		idx, fieldTy, ok := objVal.Ty.FieldIndex(memEx.Property)
		if !ok {
			return Value{}, fmt.Errorf("no field '%s'", memEx.Property)
		}
		if objVal.Ty.IsClass {
			if err := e.checkFieldVisibility(objVal.Ty.ClassName, memEx.Property, ex.GetPos()); err != nil {
				return Value{}, err
			}
			if err := e.checkReadonlyWrite(objVal.Ty.ClassName, memEx.Property, ex.GetPos()); err != nil {
				return Value{}, err
			}
		}
		e.emitFrozenCheck(objVal.Ref)
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, objVal.Ty.StructIR(), objVal.Ref, idx))
		if isLogicalAssignOp(ex.Op) {
			// A nullable-scalar field's { i1, T } presence-flagged slot needs the
			// dedicated present-bit path (TDD-00064 Stage 3); the generic
			// emitLogicalCompoundAssign reads the whole aggregate as a non-`ptr`
			// value and would no-op `??=` (it can never equal null). Every other
			// field type uses the generic path unchanged.
			if isNullableScalar(fieldTy) {
				return e.emitNullableScalarNullishOrLogicalAssignAt(gepReg, fieldTy, ex.Op, ex.Right)
			}
			return e.emitLogicalCompoundAssign(ex.Op, gepReg, fieldTy, ex.Right)
		}
		// A plain `=` into a nullable-scalar field boxes straight from the RHS
		// expression, preserving a null-valued source lvalue's null-ness
		// (TDD-00064 Stage 3) — reading it back into a Value first would
		// auto-unwrap to a present payload.
		if ex.Op == "=" && isNullableScalar(fieldTy) {
			if err := e.storeScalarOrNullableFieldExpr(gepReg, fieldTy, ex.Right); err != nil {
				return Value{}, err
			}
			return e.loadScalarOrNullableField(gepReg, fieldTy), nil
		}
		var rhs Value
		if ex.Op == "=" {
			// Hint-aware (TDD-00028/TDD-00007): `obj.field = [1,2,3]`/`obj.field
			// = {...}` coerces against the field's own declared type.
			rhs, err = e.emitExprWithObjectHint(ex.Right, fieldTy)
			if err != nil {
				return Value{}, err
			}
		} else {
			// A nullable-scalar field's current value loads as its { i1, T }
			// aggregate; compound arithmetic runs on the demoted payload (a
			// null reads as its zero, lenient) and re-boxes on store.
			cur := e.loadScalarOrNullableField(gepReg, fieldTy)
			arithTy := fieldTy
			if isNullableScalar(fieldTy) {
				cur = e.nullableScalarPayloadOf(cur)
				arithTy = fieldTy.withoutNullable()
			}
			rhsVal, err := e.emitExpr(ex.Right)
			if err != nil {
				return Value{}, err
			}
			if err := dateCompoundAssignGuard(ex.Op, fieldTy.IsDate, rhsVal.Ty.IsDate); err != nil {
				return Value{}, fmt.Errorf("%d:%d: %s", ex.GetPos().Line, ex.GetPos().Col, err)
			}
			rhsVal = e.coerce(rhsVal, arithTy)
			rhs, err = e.emitArith(strings.TrimSuffix(ex.Op, "="), cur, rhsVal, arithTy, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
		}
		e.storeScalarOrNullableField(gepReg, fieldTy, rhs)
		return rhs, nil
	}

	// Array destructuring assignment: [a, b] = expr (ADR-00160), extended
	// with a rest target `[a, ...rest] = expr` (ADR-00161, same
	// independent-copy semantics as the declaration form's own rest —
	// unpackArrayPatternInto, emit_arrays_core.go). V1 scope, narrower
	// than the declaration form otherwise: every non-rest target must be a
	// plain, already-declared, non-array, non-const variable — no nested
	// patterns, no per-element default value. The array-literal parser
	// itself has no hole syntax, so `[, b] = arr` is already a clean
	// parse-time rejection, not something to special-case here. A pattern
	// position past the source array's actual length reads as zero, same
	// reasoning and bounds check as unpackArrayPatternInto.
	if arrLit, ok := ex.Left.(*ast.ArrayLiteral); ok {
		if ex.Op != "=" {
			return Value{}, fmt.Errorf("%d:%d: compound assignment is not supported in a destructuring assignment", ex.GetPos().Line, ex.GetPos().Col)
		}
		dataPtr, lenVal, elemTy, err := e.resolveArrayDataPtr(ex.Right, ex.GetPos())
		if err != nil {
			return Value{}, err
		}
		if err := e.emitDestructAssignArrayElems(dataPtr, lenVal, elemTy, arrLit.Elements, ex.GetPos()); err != nil {
			return Value{}, err
		}
		return Value{Ty: TypeVoid}, nil
	}

	// Object destructuring assignment: ({ x, y } = expr) etc. — see
	// emitDestructAssignObjectProps.
	if objLit, ok := ex.Left.(*ast.ObjectLiteral); ok {
		if ex.Op != "=" {
			return Value{}, fmt.Errorf("%d:%d: compound assignment is not supported in a destructuring assignment", ex.GetPos().Line, ex.GetPos().Col)
		}
		if objLit.HasComputedKey() {
			return Value{}, fmt.Errorf("%d:%d: a computed key is not supported in a destructuring assignment", ex.GetPos().Line, ex.GetPos().Col)
		}
		objPtr, objTy, err := e.resolveObjectPtr(ex.Right, ex.GetPos())
		if err != nil {
			return Value{}, err
		}
		if err := e.emitDestructAssignObjectProps(objPtr, objTy, objLit.Properties, ex.GetPos()); err != nil {
			return Value{}, err
		}
		return Value{Ty: TypeVoid}, nil
	}


	// Scalar variable assignment
	ident, ok := ex.Left.(*ast.Identifier)
	if !ok {
		return Value{}, fmt.Errorf("can only assign to identifiers or array elements")
	}
	sym, ok := e.lookup(ident.Name)
	if !ok {
		return Value{}, fmt.Errorf("undefined variable '%s'", ident.Name)
	}

	if sym.IsConst {
		return Value{}, fmt.Errorf("%d:%d: cannot assign to '%s' because it is a constant", ex.GetPos().Line, ex.GetPos().Col, ident.Name)
	}

	// A nullable-scalar local stores into its { i1, T } slot (TDD-00064) —
	// intercepted before the bare-scalar load/store path below, which would
	// mis-shape the aggregate. A bare-storage nullable scalar (param/field,
	// Stage 3) is deliberately excluded and keeps the generic path.
	if sym.isNullableScalarLocal() {
		return e.emitNullableScalarAssign(sym, ex)
	}

	if sym.Ty.IsDynamic && ex.Op != "=" && !isLogicalAssignOp(ex.Op) {
		// `-compat=js` (TDD-00076 A2): compound assignment dispatches the
		// operator at runtime; strict keeps the rejection.
		if !e.compatJS() {
			return Value{}, fmt.Errorf("%d:%d: compound assignment ('%s') on any/unknown is not yet supported", ex.GetPos().Line, ex.GetPos().Col, ex.Op)
		}
		cur := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, sym.Ptr))
		rhsVal, err := e.emitExprWithObjectHint(ex.Right, TypeAny)
		if err != nil {
			return Value{}, err
		}
		res, err := e.emitAnyBinary(strings.TrimSuffix(ex.Op, "="), Value{Ref: cur, Ty: TypeAny}, rhsVal, ex.GetPos())
		if err != nil {
			return Value{}, err
		}
		boxed, err := e.emitBoxValue(res)
		if err != nil {
			return Value{}, err
		}
		if fresh, ok := e.lookup(ident.Name); ok {
			sym = fresh
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, sym.Ptr))
		return boxed, nil
	}

	if isLogicalAssignOp(ex.Op) {
		return e.emitLogicalCompoundAssign(ex.Op, sym.Ptr, sym.Ty, ex.Right)
	}

	var rhs Value
	if ex.Op == "=" {
		var err error
		// `obj = JSON.parse(s)` / `obj = await res.json()` into an object-typed
		// binding projects against the binding's declared shape — the bare-
		// reassignment counterpart of the typed var-decl path (ADR-00571).
		if sym.Ty.IsObject {
			if val, ok, perr := e.emitDeclJSONProjection(ex.Right, sym.Ty); ok {
				if perr != nil {
					return Value{}, perr
				}
				e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", sym.Ty.IR, val.Ref, sym.Ptr, sym.Ty.Align()))
				return val, nil
			}
		}
		// Hint-aware (TDD-00028/TDD-00007): sym.Ty is never array-typed
		// here (that case is handled by its own branch above), so this
		// only matters for an object-literal-typed scalar variable, but
		// costs nothing to route through uniformly.
		rhs, err = e.emitExprWithObjectHint(ex.Right, sym.Ty)
		if err != nil {
			return Value{}, err
		}
	} else {
		loadReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loadReg, sym.Ty.IR, sym.Ptr, sym.Ty.Align()))
		cur := Value{Ref: loadReg, Ty: sym.Ty}

		rhsVal, err := e.emitExpr(ex.Right)
		if err != nil {
			return Value{}, err
		}
		if err := dateCompoundAssignGuard(ex.Op, sym.Ty.IsDate, rhsVal.Ty.IsDate); err != nil {
			return Value{}, fmt.Errorf("%d:%d: %s", ex.GetPos().Line, ex.GetPos().Col, err)
		}
		rhsVal = e.coerce(rhsVal, sym.Ty)

		op := strings.TrimSuffix(ex.Op, "=")
		rhs, err = e.emitArith(op, cur, rhsVal, sym.Ty, ex.GetPos())
		if err != nil {
			return Value{}, err
		}
	}

	if sym.Ty.IsDynamic {
		var err error
		if sym.Ty.UnionMembers != nil && !unionAllowsAssignmentFrom(sym.Ty, rhs.Ty) {
			return Value{}, fmt.Errorf("%d:%d: value's type is not a member of '%s's declared union type", ex.GetPos().Line, ex.GetPos().Col, ident.Name)
		}
		rhs, err = e.emitBoxValue(rhs)
		if err != nil {
			return Value{}, err
		}
	} else {
		rhs = e.coerce(rhs, sym.Ty)
		// A binding's type is fixed at its declaration (annotated, or inferred
		// from its initializer / first assignment). Assigning an incompatible
		// type (`let x = 5; x = 'hi'`, or an untyped `let x; x = 1; x = 's'`)
		// leaves rhs with a storage IR that differs from the slot's — a bare
		// `store <sym.IR> <rhs>` that clang rejects as invalid. Reject cleanly
		// instead, matching TypeScript (which infers the binding's type and
		// errors on the incompatible assignment) and this compiler's two-tier
		// rule: a variable that genuinely holds different types must be
		// annotated `: any` (works today) or compiled with `-compat=js`. The
		// composite/aggregate targets (array/object/dynamic/nullable) all
		// return from their own branches above, so this scalar-store site is
		// the only place an IR mismatch can reach the emitter.
		if rhs.Ty.IR != sym.Ty.IR {
			describe := func(t Type) string {
				if k := scalarTypeKind(t); k != "" {
					return k
				}
				return t.IR
			}
			return Value{}, fmt.Errorf("%d:%d: cannot assign a %s value to '%s' (a %s) — a variable's type is fixed at its declaration; annotate it `: any` for a value that holds different types, or compile with -compat=js", ex.GetPos().Line, ex.GetPos().Col, describe(rhs.Ty), pureDisplayName(ident.Name), describe(sym.Ty))
		}
	}
	// Evaluating the RHS may have promoted `ident` to a heap cell for closure
	// capture (updateSymbolInPlace changes its storage pointer) — re-resolve it so
	// the store targets the cell the closure now reads. Fixes the self-referential
	// `id = setInterval(() => { ... clearInterval(id) ... }, ...)`, where the
	// closure captured id during RHS evaluation and the assignment otherwise wrote
	// to the stale pre-promotion slot, leaving the closure's id at its old value.
	if fresh, ok := e.lookup(ident.Name); ok {
		sym = fresh
	}
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", sym.Ty.IR, rhs.Ref, sym.Ptr, sym.Ty.Align()))
	return rhs, nil
}

// --- Destructuring-assignment recursion (ADR-00160 successor) ---
//
// The assignment form `[a, [b]] = e` / `({ x, y: { z } } = e)` reaches parity
// with the declaration form's nesting (unpack*PatternInto): every target is an
// already-declared variable, and a target position may itself be a nested array
// or object pattern. The source-element load and out-of-bounds handling mirror
// unpackNestedArrayElem exactly (OOB reads a deterministic zero / empty).

// emitDestructAssignArrayElems assigns each position of `elems` (a
// destructuring-assignment array pattern) from the source array at
// (dataPtr, lenVal, elemTy). A trailing `...rest` collects the remainder.
func (e *Emitter) emitDestructAssignArrayElems(dataPtr, lenVal string, elemTy Type, elems []ast.Expression, pos ast.Pos) error {
	for i, elemExpr := range elems {
		if spread, isSpread := elemExpr.(*ast.SpreadElement); isSpread {
			if i != len(elems)-1 {
				return fmt.Errorf("%d:%d: a rest target must be the last element of a destructuring assignment", spread.GetPos().Line, spread.GetPos().Col)
			}
			id, ok := spread.Arg.(*ast.Identifier)
			if !ok {
				return fmt.Errorf("%d:%d: a rest target must be a plain, already-declared array variable", spread.Arg.GetPos().Line, spread.Arg.GetPos().Col)
			}
			sym, found := e.lookup(id.Name)
			if !found {
				return fmt.Errorf("%d:%d: undefined variable '%s'", spread.Arg.GetPos().Line, spread.Arg.GetPos().Col, id.Name)
			}
			if sym.IsConst {
				return fmt.Errorf("%d:%d: cannot assign to '%s' because it is a constant", spread.Arg.GetPos().Line, spread.Arg.GetPos().Col, id.Name)
			}
			if !sym.Ty.IsArray {
				return fmt.Errorf("%d:%d: '%s' is not an array — a rest target must already be declared as one", spread.Arg.GetPos().Line, spread.Arg.GetPos().Col, id.Name)
			}
			e.ensureMalloc()
			e.ensureMemcpy()
			rawLen, isNegLen, restLen := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %d", rawLen, lenVal, i))
			e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNegLen, rawLen))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", restLen, isNegLen, rawLen))
			byteCount, newPtr := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", byteCount, restLen, elemTy.Align()))
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", newPtr, byteCount))
			srcGep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", srcGep, elemTy.IR, dataPtr, i))
			e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", newPtr, srcGep, byteCount))
			restHeader := e.newArrayHeader(newPtr, restLen)
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", restHeader, sym.Ptr))
			break
		}

		target, def := unwrapDestructDefault(elemExpr)
		switch t := target.(type) {
		case *ast.ArrayLiteral:
			if def != nil {
				return fmt.Errorf("%d:%d: a default value on a nested destructuring pattern is not yet supported", t.GetPos().Line, t.GetPos().Col)
			}
			if !elemTy.IsArray || elemTy.ElemType == nil {
				return fmt.Errorf("%d:%d: cannot array-destructure a non-array element", t.GetPos().Line, t.GetPos().Col)
			}
			subPtr, subLen := e.destructLoadNestedArray(dataPtr, lenVal, elemTy, i)
			if err := e.emitDestructAssignArrayElems(subPtr, subLen, *elemTy.ElemType, t.Elements, pos); err != nil {
				return err
			}
		case *ast.ObjectLiteral:
			if def != nil {
				return fmt.Errorf("%d:%d: a default value on a nested destructuring pattern is not yet supported", t.GetPos().Line, t.GetPos().Col)
			}
			if !elemTy.IsObject {
				return fmt.Errorf("%d:%d: cannot object-destructure a non-object element", t.GetPos().Line, t.GetPos().Col)
			}
			subObj := e.destructLoadNestedObject(dataPtr, lenVal, elemTy, i)
			if err := e.emitDestructAssignObjectProps(subObj, elemTy, t.Properties, pos); err != nil {
				return err
			}
		case *ast.Identifier:
			if err := e.destructAssignArrayLeaf(dataPtr, lenVal, elemTy, i, t, def); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%d:%d: a destructuring assignment target must be a plain variable or a nested pattern", elemExpr.GetPos().Line, elemExpr.GetPos().Col)
		}
	}
	return nil
}

// emitDestructAssignObjectProps assigns each property of `props` (an
// object-destructuring-assignment pattern) from the source object at objPtr.
func (e *Emitter) emitDestructAssignObjectProps(objPtr string, objTy Type, props []ast.ObjectProperty, pos ast.Pos) error {
	structIR := objTy.StructIR()
	for _, prop := range props {
		if _, isSpread := prop.Value.(*ast.SpreadElement); isSpread {
			return fmt.Errorf("%d:%d: a rest target ('...') is not supported in a destructuring assignment", pos.Line, pos.Col)
		}
		idx, fieldTy, ok := objTy.FieldIndex(prop.Key)
		if !ok {
			return fmt.Errorf("%d:%d: object has no field '%s'", pos.Line, pos.Col, prop.Key)
		}
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, structIR, objPtr, idx))

		target, def := unwrapDestructDefault(prop.Value)
		switch t := target.(type) {
		case *ast.ArrayLiteral:
			if def != nil {
				return fmt.Errorf("%d:%d: a default value on a nested destructuring pattern is not yet supported", pos.Line, pos.Col)
			}
			if !fieldTy.IsArray || fieldTy.ElemType == nil {
				return fmt.Errorf("%d:%d: cannot array-destructure a non-array field '%s'", pos.Line, pos.Col, prop.Key)
			}
			// An array-typed field is stored as the `{ptr, i64}` aggregate inline
			// in the struct — load it directly (not via loadArrayElem, which reads
			// an element out of an array).
			aggReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load {ptr, i64}, ptr %s, align 8", aggReg, gepReg))
			subPtr, subLen := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", subPtr, aggReg))
			e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", subLen, aggReg))
			if err := e.emitDestructAssignArrayElems(subPtr, subLen, *fieldTy.ElemType, t.Elements, pos); err != nil {
				return err
			}
		case *ast.ObjectLiteral:
			if def != nil {
				return fmt.Errorf("%d:%d: a default value on a nested destructuring pattern is not yet supported", pos.Line, pos.Col)
			}
			if !fieldTy.IsObject {
				return fmt.Errorf("%d:%d: cannot object-destructure a non-object field '%s'", pos.Line, pos.Col, prop.Key)
			}
			subObj := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", subObj, gepReg))
			if err := e.emitDestructAssignObjectProps(subObj, fieldTy, t.Properties, pos); err != nil {
				return err
			}
		case *ast.Identifier:
			sym, found := e.lookup(t.Name)
			if !found {
				return fmt.Errorf("%d:%d: undefined variable '%s'", t.GetPos().Line, t.GetPos().Col, t.Name)
			}
			if sym.IsConst {
				return fmt.Errorf("%d:%d: cannot assign to '%s' because it is a constant", t.GetPos().Line, t.GetPos().Col, t.Name)
			}
			if fieldTy.IsArray || sym.Ty.IsArray {
				return fmt.Errorf("%d:%d: destructuring assignment into/from an array-typed field is not yet supported", pos.Line, pos.Col)
			}
			if def != nil {
				// An object-field default fires when the field is null — so it
				// needs the field to be a nullable reference (the only shape with
				// a reliable "not provided" signal), matching the declaration form.
				if !(fieldTy.Nullable && fieldTy.IR == "ptr") {
					return fmt.Errorf("%d:%d: a destructuring default requires field '%s' to be a nullable reference type (string | null, T[] | null, an interface/class type | null) — no other field type has a reliable way to tell a real value apart from 'not provided'", pos.Line, pos.Col, prop.Key)
				}
				valReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", valReg, gepReg))
				isNull := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, valReg))
				absentL, presentL, afterL := e.freshLabel("destro.absent"), e.freshLabel("destro.present"), e.freshLabel("destro.after")
				e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, absentL, presentL))
				e.emitLabel(absentL)
				defVal, err := e.emitExpr(def)
				if err != nil {
					return err
				}
				defVal = e.coerce(defVal, sym.Ty)
				e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", sym.Ty.IR, defVal.Ref, sym.Ptr, sym.Ty.Align()))
				e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
				e.emitLabel(presentL)
				pv := e.coerce(Value{Ref: valReg, Ty: fieldTy}, sym.Ty)
				e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", sym.Ty.IR, pv.Ref, sym.Ptr, sym.Ty.Align()))
				e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
				e.emitLabel(afterL)
				break
			}
			valReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", valReg, fieldTy.IR, gepReg, fieldTy.Align()))
			val := e.coerce(Value{Ref: valReg, Ty: fieldTy}, sym.Ty)
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", sym.Ty.IR, val.Ref, sym.Ptr, sym.Ty.Align()))
		default:
			return fmt.Errorf("%d:%d: a destructuring assignment target must be a plain variable or a nested pattern", pos.Line, pos.Col)
		}
	}
	return nil
}

// destructAssignArrayLeaf assigns the source array's element i into the
// already-declared scalar/pointer variable `id` (out-of-bounds reads a
// deterministic zero, the same convention the declaration form uses).
func (e *Emitter) destructAssignArrayLeaf(dataPtr, lenVal string, elemTy Type, i int, id *ast.Identifier, def ast.Expression) error {
	sym, found := e.lookup(id.Name)
	if !found {
		return fmt.Errorf("%d:%d: undefined variable '%s'", id.GetPos().Line, id.GetPos().Col, id.Name)
	}
	if sym.IsConst {
		return fmt.Errorf("%d:%d: cannot assign to '%s' because it is a constant", id.GetPos().Line, id.GetPos().Col, id.Name)
	}
	if sym.Ty.IsArray {
		return fmt.Errorf("%d:%d: '%s' is an array — destructuring assignment into an array-typed target is not yet supported", id.GetPos().Line, id.GetPos().Col, id.Name)
	}
	inBounds := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %d, %s", inBounds, i, lenVal))
	okL, oobL, afterL := e.freshLabel("destrassign.ok"), e.freshLabel("destrassign.oob"), e.freshLabel("destrassign.after")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inBounds, okL, oobL))
	e.emitLabel(okL)
	gepReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gepReg, elemTy.IR, dataPtr, i))
	okVal := e.coerce(e.loadArrayElem(gepReg, elemTy), sym.Ty)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", sym.Ty.IR, okVal.Ref, sym.Ptr, sym.Ty.Align()))
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
	// Out of bounds: the array-pattern default fires here (the length is the
	// presence signal, so no nullable-reference requirement — unlike an object
	// field), else a deterministic zero.
	e.emitLabel(oobL)
	if def != nil {
		defVal, err := e.emitExpr(def)
		if err != nil {
			return err
		}
		defVal = e.coerce(defVal, sym.Ty)
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", sym.Ty.IR, defVal.Ref, sym.Ptr, sym.Ty.Align()))
	} else {
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", sym.Ty.IR, sym.Ty.zeroLiteral(), sym.Ptr, sym.Ty.Align()))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
	e.emitLabel(afterL)
	return nil
}

// destructLoadNestedArray loads source element i as a nested array's (ptr, len),
// OOB → (null, 0). Mirrors unpackNestedArrayElem's SubArray branch.
func (e *Emitter) destructLoadNestedArray(dataPtr, lenVal string, elemTy Type, i int) (string, string) {
	inBounds := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %d, %s", inBounds, i, lenVal))
	okL, oobL, afterL := e.freshLabel("destrn.ok"), e.freshLabel("destrn.oob"), e.freshLabel("destrn.after")
	subPtrA, subLenA := e.freshReg(), e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", subPtrA))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", subLenA))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inBounds, okL, oobL))
	e.emitLabel(okL)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gep, elemTy.IR, dataPtr, i))
	val := e.loadArrayElem(gep, elemTy)
	p, l := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", p, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", l, val.Ref))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", p, subPtrA))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", l, subLenA))
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
	e.emitLabel(oobL)
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", subPtrA))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", subLenA))
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
	e.emitLabel(afterL)
	subPtr, subLen := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", subPtr, subPtrA))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", subLen, subLenA))
	return subPtr, subLen
}

// destructLoadNestedObject loads source element i as a nested object pointer,
// OOB → a zeroed stand-in object (so the recursive unpack never derefs null).
func (e *Emitter) destructLoadNestedObject(dataPtr, lenVal string, elemTy Type, i int) string {
	inBounds := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %d, %s", inBounds, i, lenVal))
	okL, oobL, afterL := e.freshLabel("destrno.ok"), e.freshLabel("destrno.oob"), e.freshLabel("destrno.after")
	subObjA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", subObjA))
	zeroObj := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", zeroObj, elemTy.StructIR()))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inBounds, okL, oobL))
	e.emitLabel(okL)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gep, elemTy.IR, dataPtr, i))
	objp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", objp, gep))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", objp, subObjA))
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
	e.emitLabel(oobL)
	e.emitInstr(fmt.Sprintf("store %s zeroinitializer, ptr %s, align 8", elemTy.StructIR(), zeroObj))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", zeroObj, subObjA))
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
	e.emitLabel(afterL)
	subObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", subObj, subObjA))
	return subObj
}

// unwrapDestructDefault splits a `target = default` assignment-pattern element
// into (target, default); a plain target returns (target, nil).
func unwrapDestructDefault(elem ast.Expression) (ast.Expression, ast.Expression) {
	if ae, ok := elem.(*ast.AssignmentExpression); ok && ae.Op == "=" {
		return ae.Left, ae.Right
	}
	return elem, nil
}
