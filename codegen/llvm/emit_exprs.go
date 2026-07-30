// emit_exprs.go — core expression dispatch (emitExpr) plus the leaf literal
// emitters. Everything else expression-related lives in the other
// emit_exprs_*.go files: operators (emit_exprs_operators.go), assignment
// (emit_exprs_assign.go), member/index access (emit_exprs_member.go), type
// inference/conversion (emit_exprs_types.go), scalar coercion
// (emit_exprs_coerce.go), and variable declarations (emit_exprs_vardecl.go).
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strconv"
	"strings"
)

func (e *Emitter) emitExpr(expr ast.Expression) (Value, error) {
	switch ex := expr.(type) {
	case *ast.NumberLiteral:
		return e.emitNumberLit(ex)
	case *ast.StringLiteral:
		ptr := e.internString(ex.Value)
		return Value{Ref: ptr, Ty: TypePtr}, nil
	case *ast.BooleanLiteral:
		v := "0"
		if ex.Value {
			v = "1"
		}
		return Value{Ref: v, Ty: TypeBool}, nil
	case *ast.Identifier:
		return e.emitIdent(ex)
	case *ast.BinaryExpression:
		if ex.Op == "??" {
			return e.emitNullCoalesce(ex)
		}
		if ex.Op == "instanceof" {
			return e.emitInstanceOf(ex)
		}
		return e.emitBinary(ex)
	case *ast.UnaryExpression:
		return e.emitUnary(ex)
	case *ast.UpdateExpression:
		return e.emitUpdate(ex)
	case *ast.AssignmentExpression:
		return e.emitAssign(ex)
	case *ast.CallExpression:
		return e.emitCall(ex)
	case *ast.IndexExpression:
		return e.emitIndex(ex)
	case *ast.MemberExpression:
		return e.emitMember(ex)
	case *ast.SpreadElement:
		return Value{}, fmt.Errorf("%d:%d: spread element must be used inside an array literal", ex.GetPos().Line, ex.GetPos().Col)
	case *ast.ArrayLiteral:
		return Value{}, fmt.Errorf("%d:%d: array literal must be used in a variable declaration", ex.GetPos().Line, ex.GetPos().Col)
	case *ast.NewArrayExpression:
		return Value{}, fmt.Errorf("%d:%d: new Array() must be used in a variable declaration", ex.GetPos().Line, ex.GetPos().Col)
	case *ast.NewMapExpression:
		return Value{}, fmt.Errorf("%d:%d: new Map() must be used in a variable declaration", ex.GetPos().Line, ex.GetPos().Col)
	case *ast.NewSetExpression:
		return Value{}, fmt.Errorf("%d:%d: new Set() must be used in a variable declaration", ex.GetPos().Line, ex.GetPos().Col)
	case *ast.NewErrorExpression:
		return e.emitNewError(ex)
	case *ast.NewDateExpression:
		return e.emitNewDate(ex)
	case *ast.ObjectLiteral:
		return e.emitObjectLiteral(ex)
	case *ast.ArrowFunction:
		return e.emitArrowFunction(ex)
	case *ast.TemplateLiteral:
		return e.emitTemplateLiteral(ex)
	case *ast.ConditionalExpression:
		return e.emitConditional(ex)
	case *ast.NullLiteral:
		if ex.IsUndefined {
			return Value{Ref: "null", Ty: TypeUndefined}, nil
		}
		return Value{Ref: "null", Ty: TypeNull}, nil
	case *ast.AwaitExpression:
		return e.emitAwait(ex)
	case *ast.ThisExpression:
		return e.emitThisExpression(ex.GetPos())
	case *ast.NewExpression:
		return e.emitNewExpression(ex)
	case *ast.NewURLExpression:
		return e.emitNewURLExpression(ex)
	case *ast.NewURLSearchParamsExpression:
		return e.emitNewURLSearchParamsExpression(ex)
	case *ast.NewArrayBufferExpression:
		return e.emitNewArrayBufferExpression(ex)
	case *ast.NewTypedArrayExpression:
		return Value{}, fmt.Errorf("%d:%d: a TypedArray constructor must be used in a variable declaration", ex.GetPos().Line, ex.GetPos().Col)
	}
	return Value{}, fmt.Errorf("unknown expression type %T", expr)
}

func (e *Emitter) emitNumberLit(n *ast.NumberLiteral) (Value, error) {
	v := n.Value
	if strings.ContainsRune(v, '.') {
		return Value{Ref: v, Ty: TypeF64}, nil
	}
	// Hex (0x), binary (0b), octal (0o) — convert to decimal for LLVM IR.
	if len(v) >= 2 && v[0] == '0' && (v[1]|32 == 'x' || v[1]|32 == 'b' || v[1]|32 == 'o') {
		n64, err := strconv.ParseInt(v, 0, 64)
		if err != nil {
			return Value{}, fmt.Errorf("invalid numeric literal %q: %v", v, err)
		}
		return Value{Ref: fmt.Sprintf("%d", n64), Ty: TypeI64}, nil
	}
	return Value{Ref: v, Ty: TypeI64}, nil
}

func (e *Emitter) emitIdent(id *ast.Identifier) (Value, error) {
	sym, ok := e.lookup(id.Name)
	if !ok {
		// Bare NaN/Infinity globals (real JS also has these outside the
		// Number.* namespace) — only after a local lookup miss, so a
		// user-declared variable of the same name still shadows them.
		switch id.Name {
		case "NaN":
			return Value{Ref: "0x7FF8000000000000", Ty: TypeF64}, nil
		case "Infinity":
			return Value{Ref: "0x7FF0000000000000", Ty: TypeF64}, nil
		}
		return Value{}, fmt.Errorf("%d:%d: undefined variable '%s'", id.GetPos().Line, id.GetPos().Col, id.Name)
	}
	if sym.Ty.IsArray {
		// A named array variable is stored as two separate allocas
		// (Ptr/LenPtr — see emitArrayVarDecl/emitFunctionDecl's parameter
		// handling), not the single {ptr, i64} aggregate every other
		// context that evaluates an array as a plain value expects (return
		// values, .slice()/HOF results, struct field storage). A plain
		// `load sym.Ty.IR from sym.Ptr` — the path every other (non-array)
		// symbol takes below — would silently recover only the data
		// pointer and drop the length entirely. Rebuild the aggregate here
		// instead, once, so every caller of emitExpr on a bare array
		// identifier gets the same correctly-shaped Value a HOF result or
		// a return statement already would. See docs/adr/ADR-00061.md.
		ptrReg := e.freshReg()
		lenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptrReg, sym.Ptr))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, sym.LenPtr))
		r0 := e.freshReg()
		r1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, ptrReg))
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
		return Value{Ref: r1, Ty: sym.Ty}, nil
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", reg, sym.Ty.IR, sym.Ptr, sym.Ty.Align()))
	return Value{Ref: reg, Ty: sym.Ty}, nil
}
