// emit_tuple.go — tuple types `[T0, T1, ...]` (TDD-00066). A tuple is stored as
// a fixed-shape struct with synthetic positional field names "0","1",...
// (TupleType, types.go), so construction, element access, destructuring, and
// serialization all lean on the existing object/struct machinery; only the
// tuple-specific surface (a constant-index read, positional destructuring, and
// array-shaped rendering) lives here or is flagged via Type.IsTuple.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strconv"
	"strings"
)

// emitTupleLiteral builds a tuple value from an array literal's element
// expressions against the declared tuple type — element i is stored into field
// "i". A nullable-scalar element is boxed and a null-valued source lvalue keeps
// its null-ness, exactly like an object-literal field (storeScalarOrNullableFieldExpr).
func (e *Emitter) emitTupleLiteral(elems []ast.Expression, ty Type) (Value, error) {
	if len(elems) != len(ty.Fields) {
		return Value{}, fmt.Errorf("a %d-tuple literal must have exactly %d elements, got %d", len(ty.Fields), len(ty.Fields), len(elems))
	}
	// calloc, not malloc: an object/tuple struct must read back deterministic
	// zeros for any slot a store doesn't fully cover — same reasoning as
	// emitObjectLiteral (ADR-00157).
	e.ensureCalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", dataReg, ty.StructSize()))
	structIR := ty.StructIR()
	for i, elemExpr := range elems {
		if _, isSpread := elemExpr.(*ast.SpreadElement); isSpread {
			return Value{}, fmt.Errorf("a spread element is not supported in a tuple literal")
		}
		fieldTy := ty.Fields[i].Ty
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, structIR, dataReg, i))
		if err := e.storeScalarOrNullableFieldExpr(gepReg, fieldTy, elemExpr); err != nil {
			return Value{}, err
		}
	}
	return Value{Ref: dataReg, Ty: ty}, nil
}

// unpackTuplePatternInto binds an array-destructuring pattern positionally
// against a tuple value at objPtr: pattern position i binds field "i". Unlike
// the array-source path (unpackArrayPatternInto), a tuple's arity is known at
// compile time and each position has its own type, so there is no runtime
// bounds check and no per-position default fallback — a pattern longer than the
// tuple is a compile-time error. Supports plain identifier targets, holes, and
// a nested sub-pattern on a tuple/object/array element; a rest element is a
// clean rejection in V1 (TDD-00066).
func (e *Emitter) unpackTuplePatternInto(objPtr string, tupleTy Type, elems []ast.ArrayPatternElem, pos ast.Pos) error {
	if len(elems) > len(tupleTy.Fields) {
		return fmt.Errorf("%d:%d: destructuring pattern has %d elements but the tuple has only %d", pos.Line, pos.Col, len(elems), len(tupleTy.Fields))
	}
	structIR := tupleTy.StructIR()
	for i, elem := range elems {
		if elem.Rest {
			return fmt.Errorf("%d:%d: a rest element in a tuple destructuring pattern is not yet supported", pos.Line, pos.Col)
		}
		if elem.Name == "" && elem.SubArray == nil && elem.SubObject == nil {
			continue // hole
		}
		fieldTy := tupleTy.Fields[i].Ty
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, structIR, objPtr, i))

		if elem.SubArray != nil || elem.SubObject != nil {
			if err := e.unpackNestedTupleElem(gepReg, fieldTy, elem, pos); err != nil {
				return err
			}
			continue
		}

		if fieldTy.IsArray {
			// An array element binds under the object-reference model (TDD-00127):
			// a stable slot holding a pointer to a fresh header wrapping the
			// element's aggregate, so its .length/indexing/mutators work.
			val := e.loadScalarOrNullableField(gepReg, fieldTy) // {ptr,i64} aggregate
			slot := e.newArrayHeaderSlotFromAggregate(val)
			e.define(elem.Name, Symbol{Ptr: slot, Ty: fieldTy})
			continue
		}

		val := e.loadScalarOrNullableField(gepReg, fieldTy)
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, StructFieldIR(fieldTy), fieldTy.Align()))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(fieldTy), val.Ref, slot, fieldTy.Align()))
		e.define(elem.Name, Symbol{Ptr: slot, Ty: fieldTy, NullableBoxed: isNullableScalar(fieldTy)})
	}
	return nil
}

// unpackNestedTupleElem destructures a nested sub-pattern against a tuple
// element that is itself a tuple/object (loaded by pointer) or an array.
func (e *Emitter) unpackNestedTupleElem(gepReg string, fieldTy Type, elem ast.ArrayPatternElem, pos ast.Pos) error {
	switch {
	case elem.SubArray != nil && fieldTy.IsTuple:
		sub := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", sub, gepReg))
		return e.unpackTuplePatternInto(sub, fieldTy, elem.SubArray, pos)
	case elem.SubArray != nil && fieldTy.IsArray:
		val := e.loadScalarOrNullableField(gepReg, fieldTy) // {ptr,i64}
		ptrReg := e.freshReg()
		lenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, val.Ref))
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))
		elemTy := TypeI64
		if fieldTy.ElemType != nil {
			elemTy = *fieldTy.ElemType
		}
		return e.unpackArrayPatternInto(ptrReg, lenReg, elemTy, elem.SubArray)
	case elem.SubObject != nil && fieldTy.IsObject:
		sub := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", sub, gepReg))
		return e.unpackObjectPatternInto(sub, fieldTy, elem.SubObject, pos)
	default:
		return fmt.Errorf("%d:%d: nested destructuring pattern does not match the tuple element's type", pos.Line, pos.Col)
	}
}

// tupleConstIndex extracts a non-negative compile-time-constant integer index
// from a tuple index expression, or ok=false when the index isn't a plain
// integer literal (a tuple has no runtime array to index dynamically).
func tupleConstIndex(expr ast.Expression) (int64, bool) {
	nl, ok := expr.(*ast.NumberLiteral)
	if !ok || strings.ContainsRune(nl.Value, '.') {
		return 0, false
	}
	n, err := strconv.ParseInt(nl.Value, 0, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// emitTupleToString renders a tuple as its elements joined by commas —
// real JS's `String([a, b])` (and thus `${tuple}` interpolation and, here,
// console.log). An element is rendered by the ordinary value-to-string path
// (a nested string is not quoted, matching JS's own comma join).
func (e *Emitter) emitTupleToString(v Value) (Value, error) {
	acc := Value{Ref: e.internString(""), Ty: TypePtr}
	structIR := v.Ty.StructIR()
	for i, field := range v.Ty.Fields {
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, structIR, v.Ref, i))
		fieldVal := e.loadScalarOrNullableField(gepReg, field.Ty)
		if i > 0 {
			var err error
			if acc, err = e.emitStringConcat(acc, Value{Ref: e.internString(","), Ty: TypePtr}); err != nil {
				return Value{}, err
			}
		}
		elemStr, err := e.emitValueToString(fieldVal)
		if err != nil {
			return Value{}, err
		}
		if acc, err = e.emitStringConcat(acc, elemStr); err != nil {
			return Value{}, err
		}
	}
	return acc, nil
}

// emitTupleIndex emits `t[i]` for a constant index into a tuple — a field
// access on the underlying struct, since a tuple has no array backing buffer.
func (e *Emitter) emitTupleIndex(ex *ast.IndexExpression, tupleTy Type) (Value, error) {
	idx, ok := tupleConstIndex(ex.Index)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: a tuple can only be indexed by a constant integer literal", ex.GetPos().Line, ex.GetPos().Col)
	}
	if idx >= int64(len(tupleTy.Fields)) {
		return Value{}, fmt.Errorf("%d:%d: tuple index %d is out of range (the tuple has %d element(s))", ex.GetPos().Line, ex.GetPos().Col, idx, len(tupleTy.Fields))
	}
	objVal, err := e.emitExpr(ex.Object)
	if err != nil {
		return Value{}, err
	}
	fieldTy := tupleTy.Fields[idx].Ty
	gepReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, tupleTy.StructIR(), objVal.Ref, idx))
	if fieldTy.IsArray {
		// An array element's slot is the 16-byte {ptr, i64} aggregate; load it
		// as such so length survives (same as an array-typed object field).
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", reg, StructFieldIR(fieldTy), gepReg, fieldTy.Align()))
		return Value{Ref: reg, Ty: fieldTy}, nil
	}
	return e.loadScalarOrNullableField(gepReg, fieldTy), nil
}
