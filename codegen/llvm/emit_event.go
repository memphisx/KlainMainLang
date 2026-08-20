// emit_event.go — WHATWG Event / CustomEvent objects (TDD-00081 Stage 1). Each
// is a plain fixed-shape heap object (type/defaultPrevented/stopImmediate, plus
// CustomEvent's detail), tagged IsEvent so preventDefault/stop* dispatch on it.
// Field reads (`e.type`, `e.detail`, `e.defaultPrevented`) go through the normal
// object machinery. The EventTarget bus that dispatches these is Stage 2.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// storeEventField GEPs field `name` of an Event/CustomEvent object and stores a
// value of the given IR into it.
func (e *Emitter) storeEventField(ty Type, objReg, name, ir, val string) {
	idx, fieldTy, _ := ty.FieldIndex(name)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, ty.StructIR(), objReg, idx))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ir, val, gep, fieldTy.Align()))
}

func (e *Emitter) emitNewEventExpression(ex *ast.NewEventExpression) (Value, error) {
	typeVal, err := e.emitExpr(ex.TypeArg)
	if err != nil {
		return Value{}, err
	}
	typeVal = e.coerce(typeVal, TypePtr)
	e.ensureMalloc()
	ty := EventType()
	objReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", objReg, ty.StructSize()))
	e.storeEventField(ty, objReg, "type", "ptr", typeVal.Ref)
	e.storeEventField(ty, objReg, "defaultPrevented", "i1", "0")
	e.storeEventField(ty, objReg, "stopImmediate", "i1", "0")
	return Value{Ref: objReg, Ty: ty}, nil
}

func (e *Emitter) emitNewCustomEventExpression(ex *ast.NewCustomEventExpression) (Value, error) {
	typeVal, err := e.emitExpr(ex.TypeArg)
	if err != nil {
		return Value{}, err
	}
	typeVal = e.coerce(typeVal, TypePtr)

	// The detail field's type is inferred from the value; absent detail is null.
	detailTy := TypePtr
	detailRef := "null"
	if ex.Detail != nil {
		detailVal, err := e.emitExpr(ex.Detail)
		if err != nil {
			return Value{}, err
		}
		detailTy = detailVal.Ty
		detailRef = detailVal.Ref
	}

	e.ensureMalloc()
	ty := CustomEventType(detailTy)
	objReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", objReg, ty.StructSize()))
	e.storeEventField(ty, objReg, "type", "ptr", typeVal.Ref)
	e.storeEventField(ty, objReg, "detail", StructFieldIR(detailTy), detailRef)
	e.storeEventField(ty, objReg, "defaultPrevented", "i1", "0")
	e.storeEventField(ty, objReg, "stopImmediate", "i1", "0")
	return Value{Ref: objReg, Ty: ty}, nil
}

// emitEventMethod dispatches an Event/CustomEvent method call. V1: preventDefault
// sets defaultPrevented; stopImmediatePropagation sets the internal flag the
// Stage-2 dispatch loop will honor; stopPropagation is a no-op (single-target
// dispatch, no capture/bubble tree).
func (e *Emitter) emitEventMethod(objExpr ast.Expression, method string, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	switch method {
	case "preventDefault":
		e.storeEventField(objVal.Ty, objVal.Ref, "defaultPrevented", "i1", "1")
	case "stopImmediatePropagation":
		e.storeEventField(objVal.Ty, objVal.Ref, "stopImmediate", "i1", "1")
	case "stopPropagation":
		// no-op
	default:
		return Value{}, fmt.Errorf("%d:%d: Event has no method '%s'", pos.Line, pos.Col, method)
	}
	return Value{Ty: TypeVoid}, nil
}
