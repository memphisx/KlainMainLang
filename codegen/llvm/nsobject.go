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

// liveBindingsRule builds the object of an `Object.assign(lit)` marked
// LiveBindings: the literal as a dynamic object, its accessors live
// bindings.
func (e *Emitter) liveBindingsRule(ex *ast.CallExpression) (Value, bool, error) {
	if ex.Intrinsic != ast.LiveBindingsIntrinsic || len(ex.Args) != 1 {
		return Value{}, false, nil
	}
	lit, isLit := ex.Args[0].(*ast.ObjectLiteral)
	if !isLit {
		return Value{}, false, nil
	}
	bag, err := e.emitDynObjLiteral(lit)
	if err != nil {
		return Value{}, true, err
	}
	e.declareFn("__kml_dynobj_mark_live", "declare void @__kml_dynobj_mark_live(ptr)")
	e.ensureNanBox()
	pay := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", pay, bag.Ref))
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_mark_live(ptr %s)", e.emitIntToPtr(pay)))
	return bag, true, nil
}

// emitNSObject reads namespace ns's object.
func (e *Emitter) emitNSObject(ns string, pos ast.Pos) (Value, error) {
	return e.emitIdent(ast.NewIdentifier(e.prog.NamespaceObjects[ns], pos))
}
