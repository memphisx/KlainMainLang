package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

func (e *Emitter) emitNumberStaticCall(property string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch property {
	case "isInteger":
		return e.emitNumberIsInteger(args, pos)
	case "isFinite":
		return e.emitNumberIsFinite(args, pos)
	case "isNaN":
		return e.emitNumberIsNaN(args, pos)
	case "isSafeInteger":
		return e.emitNumberIsSafeInteger(args, pos)
	case "parseInt":
		return e.emitParseInt(args, pos)
	case "parseFloat":
		return e.emitParseFloat(args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: Number.%s is not supported", pos.Line, pos.Col, property)
}

func (e *Emitter) emitNumberIsInteger(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Number.isInteger expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !val.Ty.Float {
		return Value{Ref: "1", Ty: TypeBool}, nil
	}
	e.ensureMathFuncs()
	floored := e.freshReg()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @floor(double %s)", floored, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = fcmp oeq double %s, %s", r, val.Ref, floored))
	return Value{Ref: r, Ty: TypeBool}, nil
}

func (e *Emitter) emitNumberIsNaN(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: isNaN expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !val.Ty.Float {
		return Value{Ref: "0", Ty: TypeBool}, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fcmp uno double %s, %s", r, val.Ref, val.Ref))
	return Value{Ref: r, Ty: TypeBool}, nil
}

func (e *Emitter) emitNumberIsFinite(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: isFinite expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !val.Ty.Float {
		return Value{Ref: "1", Ty: TypeBool}, nil
	}
	// x - x == 0.0 is true only for finite values (Inf → NaN, NaN → NaN)
	diff := e.freshReg()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fsub double %s, %s", diff, val.Ref, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = fcmp oeq double %s, 0.0", r, diff))
	return Value{Ref: r, Ty: TypeBool}, nil
}

func (e *Emitter) emitNumberIsSafeInteger(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Number.isSafeInteger expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	const maxSafe = "9007199254740991"
	if !val.Ty.Float {
		neg := e.freshReg()
		cmpNeg := e.freshReg()
		absVal := e.freshReg()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 0, %s", neg, val.Ref))
		e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", cmpNeg, val.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", absVal, cmpNeg, val.Ref, neg))
		e.emitInstr(fmt.Sprintf("%s = icmp sle i64 %s, %s", r, absVal, maxSafe))
		return Value{Ref: r, Ty: TypeBool}, nil
	}
	e.ensureMathFuncs()
	floored := e.freshReg()
	isInt := e.freshReg()
	absVal := e.freshReg()
	inRange := e.freshReg()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @floor(double %s)", floored, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = fcmp oeq double %s, %s", isInt, val.Ref, floored))
	e.emitInstr(fmt.Sprintf("%s = call double @fabs(double %s)", absVal, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = fcmp ole double %s, 9.007199254740991e+15", inRange, absVal))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", r, isInt, inRange))
	return Value{Ref: r, Ty: TypeBool}, nil
}

func (e *Emitter) emitParseInt(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: parseInt expects 1 or 2 arguments", pos.Line, pos.Col)
	}
	e.ensureStrtoll()
	strVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	radixRef := "10"
	if len(args) == 2 {
		rv, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		r32 := e.coerce(rv, TypeI32)
		radixRef = r32.Ref
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strtoll(ptr %s, ptr null, i32 %s)", r, strVal.Ref, radixRef))
	return Value{Ref: r, Ty: TypeI64}, nil
}

func (e *Emitter) emitParseFloat(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: parseFloat expects 1 argument", pos.Line, pos.Col)
	}
	e.ensureStrtod()
	strVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @strtod(ptr %s, ptr null)", r, strVal.Ref))
	return Value{Ref: r, Ty: TypeF64}, nil
}
