package llvm

// emit_classes_jsinfer.go — a vanilla-JS class under `-compat=js`: it
// declares no fields; they come into being via `this.NAME = expr` in the
// constructor, and its parameters are typed by its `new` sites.
//
//	class Point { constructor(x, y) { this.x = x; this.y = y } }
//
// The checker answers both, as TypeScript's checkJs does
// (checker/jsmode.go): a field's first store types it, and stores of
// different types, like call sites that disagree, make it any.

import (
	"fmt"

	"KlainMainLang/ast"
)

// jsApplyCtorParamTypes types a JavaScript constructor's unannotated
// parameters from its `new` sites, as the checker reads them.
func (e *Emitter) jsApplyCtorParamTypes(ctor *ast.FunctionDeclaration, sig *FuncSig) {
	c := e.front()
	if c == nil {
		return
	}
	for i := range sig.ParamTypes {
		if i >= len(ctor.Params) {
			break
		}
		p := ctor.Params[i]
		if p.Type != nil || p.Rest || p.Default != nil || p.ArrayPattern != nil || p.ObjectPattern != nil {
			continue
		}
		if t, ok := e.jsCallSiteType(c.JSCallSiteParam(ctor, i)); ok {
			sig.ParamTypes[i] = optionalParamType(p, t)
		}
	}
}

// jsInferConstructorFields are cd's fields its constructor creates, as the
// checker types them. seen holds inherited and declared field names: a
// store to one of those is a field write, not a new field.
func (e *Emitter) jsInferConstructorFields(cd *ast.ClassDeclaration, seen map[string]bool) ([]Field, error) {
	c := e.front()
	if c == nil {
		return nil, nil
	}
	var fields []Field
	for _, p := range c.JSConstructorFields(cd) {
		name := p.Name
		if seen[name] || name == "__proto__" {
			continue
		}
		ownMethod := false
		for _, m := range cd.Methods {
			// The instance's own method shadowing the class's: an added
			// property, which a call of the name prefers.
			ownMethod = ownMethod || m.Name == name && !m.IsStatic
		}
		if ownMethod {
			continue
		}
		switch name {
		case ClassTagField, ClassVTableField:
			pos := cd.Constructor.GetPos()
			return nil, fmt.Errorf("%d:%d: class '%s' cannot introduce a field named '%s' — reserved for the compiler's internal use", pos.Line, pos.Col, cd.Name, name)
		}
		ty := TypeAny
		if !c.Unanswered(p.Type) {
			ty, _ = e.jsCallSiteType(p.Type)
		}
		fields = append(fields, Field{Name: name, Ty: ty})
	}
	return fields, nil
}

func hasSpreadArg(args []ast.Expression) bool {
	for _, a := range args {
		if _, ok := a.(*ast.SpreadElement); ok {
			return true
		}
	}
	return false
}
