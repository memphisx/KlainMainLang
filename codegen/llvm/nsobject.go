package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// wholeNamespace is the namespace expr names when it is a namespace
// reference itself and the program holds its object (ast.AddNamespaceObjects),
// or "".
func (e *Emitter) wholeNamespace(expr ast.Expression) string {
	if e.prog == nil || len(e.prog.NamespaceObjects) == 0 {
		return ""
	}
	if ns := e.prog.WholeNamespace(expr); e.prog.NamespaceObjects[ns] != "" {
		return ns
	}
	return ""
}

func init() {
	intrinsics[ast.ModuleNamespaceIntrinsic] = intrinsic{emit: (*Emitter).emitModuleNamespace, ty: anyResult}
	intrinsics[ast.LiveBindingsIntrinsic] = intrinsic{emit: (*Emitter).emitLiveBindings, ty: anyResult}
}

// anyResult types a call whose value is a box.
func anyResult(*Emitter, *ast.CallExpression) Type { return TypeAny }

// plainCall is ex as the plain call it was synthesized from: a shape its
// intrinsic does not take.
func plainCall(ex *ast.CallExpression) *ast.CallExpression {
	c := *ex
	c.Intrinsic = ""
	return &c
}

// emitLiveBindings builds the object of an `Object.assign(lit)` marked
// LiveBindings: the literal as a dynamic object, its accessors live
// bindings.
func (e *Emitter) emitLiveBindings(ex *ast.CallExpression) (Value, error) {
	var lit *ast.ObjectLiteral
	if len(ex.Args) == 1 {
		lit, _ = ex.Args[0].(*ast.ObjectLiteral)
	}
	if lit == nil {
		return e.emitCall(plainCall(ex))
	}
	bag, err := e.emitDynObjLiteral(lit)
	if err != nil {
		return Value{}, err
	}
	e.declareFn("__kml_dynobj_mark_live", "declare void @__kml_dynobj_mark_live("+irPtr+")")
	e.ensureNanBox()
	pay := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", pay, bag.Ref))
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_mark_live("+irPtr+" %s)", e.emitIntToPtr(pay)))
	return bag, nil
}

// emitNSObject reads namespace ns's object.
func (e *Emitter) emitNSObject(ns string, pos ast.Pos) (Value, error) {
	return e.emitIdent(ast.NewIdentifier(e.prog.NamespaceObjects[ns], pos))
}
