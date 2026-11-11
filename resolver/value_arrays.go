package resolver

import (
	"KlainMainLang/ast"
	"KlainMainLang/diag"
)

// checkValueArrays checks each `/** @value */` declaration against the
// flat layout's surface: an array of a fixed-shape object type, built from
// an array literal with no spread or hole, never read from another function
// (module scope), and never destructured by a for...of.
func checkValueArrays(prog *ast.Program) error {
	classes := map[string]bool{}
	ast.Inspect(prog, func(n ast.Node) bool {
		if cd, ok := n.(*ast.ClassDeclaration); ok {
			classes[cd.Name] = true
		}
		return true
	})
	at := func(p ast.Pos) diag.Span {
		return diag.Span{Pos: diag.Pos{Line: p.Line, Col: p.Col}, Start: -1, End: -1}
	}
	values := map[string]bool{}
	var err error
	ast.Inspect(prog, func(n ast.Node) bool {
		v, ok := n.(*ast.VarDeclaration)
		if !ok || !v.ValueArr || err != nil {
			return err == nil
		}
		values[v.Name] = true
		var elem ast.TypeNode
		switch t := v.TypeAnnot.(type) {
		case *ast.ArrayType:
			elem = t.ElementType
		case *ast.TypeReference:
			if t.Name == "Array" && len(t.Qualifier) == 0 && len(t.TypeArgs) == 1 {
				elem = t.TypeArgs[0]
			}
		}
		if elem == nil {
			err = diag.New(diag.ValueArrayTarget, at(v.GetPos()))
			return false
		}
		switch t := elem.(type) {
		case *ast.TypeLiteral:
		case *ast.TypeReference:
			if classes[t.Name] || len(t.Qualifier) == 0 && dynamicTypeNames[t.Name] {
				err = diag.New(diag.ValueArrayElement, at(v.GetPos()))
			}
		default:
			err = diag.New(diag.ValueArrayElement, at(v.GetPos()))
		}
		if err != nil {
			return false
		}
		lit, isLit := v.Init.(*ast.ArrayLiteral)
		if !isLit {
			err = diag.New(diag.ValueArrayInit, at(v.GetPos()))
			return false
		}
		for _, el := range lit.Elements {
			if _, isSpread := el.(*ast.SpreadElement); isSpread || el == nil {
				err = diag.New(diag.ValueArrayInit, at(v.GetPos()))
				return false
			}
		}
		return true
	})
	if err != nil || len(values) == 0 {
		return err
	}
	// A module-scope one read inside a function.
	for _, st := range prog.Body {
		v, ok := st.(*ast.VarDeclaration)
		if !ok || !v.ValueArr {
			continue
		}
		ast.Inspect(prog, func(n ast.Node) bool {
			if err != nil {
				return false
			}
			switch f := n.(type) {
			case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction:
				ast.Inspect(f, func(x ast.Node) bool {
					if id, isID := x.(*ast.Identifier); isID && id.Name == v.Name && err == nil {
						err = diag.New(diag.ValueArrayShared, at(v.GetPos()))
					}
					return err == nil
				})
				return false
			}
			return true
		})
	}
	if err != nil {
		return err
	}
	// Each read is one of the layout's own operations.
	allowed := map[*ast.Identifier]bool{}
	ast.Inspect(prog, func(n ast.Node) bool {
		var obj ast.Expression
		switch x := n.(type) {
		case *ast.IndexExpression:
			obj = x.Object
		case *ast.MemberExpression:
			if x.Property == "length" && !x.Optional {
				obj = x.Object
			}
		case *ast.CallExpression:
			if m, ok := x.Callee.(*ast.MemberExpression); ok && m.Property == "push" && !m.Optional {
				obj = m.Object
			}
		case *ast.ForOfStatement:
			obj = x.Iterable
		}
		if id, ok := obj.(*ast.Identifier); ok && values[id.Name] {
			allowed[id] = true
		}
		return true
	})
	ast.Inspect(prog, func(n ast.Node) bool {
		if id, ok := n.(*ast.Identifier); ok && err == nil && values[id.Name] && !allowed[id] {
			err = diag.New(diag.ValueArrayUse, at(id.GetPos()), ast.Unmangle(id.Name))
		}
		return err == nil
	})
	if err != nil {
		return err
	}
	ast.Inspect(prog, func(n ast.Node) bool {
		if fo, ok := n.(*ast.ForOfStatement); ok && err == nil && fo.VarName == "" {
			if id, isID := fo.Iterable.(*ast.Identifier); isID && values[id.Name] {
				err = diag.New(diag.ValueArrayForOfPattern, at(fo.GetPos()))
			}
		}
		return err == nil
	})
	return err
}

// dynamicTypeNames are the element types with no fixed shape.
var dynamicTypeNames = map[string]bool{"any": true, "unknown": true, "object": true, "Object": true, "Array": true, "ReadonlyArray": true}
