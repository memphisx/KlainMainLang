package ast

import "fmt"

// LowerBaseCalls binds each class base written as a call (`class Admin
// extends M(User) {}`) to a constant declared just before the class
// (`const Admin__kml_base = M(User)`), the base it then names: JavaScript
// evaluates the heritage expression once, where the class is defined.
func LowerBaseCalls(prog *Program) {
	lower := func(body []Statement) []Statement {
		var out []Statement
		for _, st := range body {
			if cd, ok := st.(*ClassDeclaration); ok && cd.BaseCall != nil {
				name := cd.Name + "__kml_base"
				out = append(out, NewVarDeclaration("const", name, nil, cd.BaseCall, cd.BaseCall.GetPos()))
				cd.BaseClass, cd.BaseCall = name, nil
			}
			if cd, ok := st.(*ClassDeclaration); ok && cd.BaseExpr != nil {
				name := cd.Name + "__kml_base"
				out = append(out, NewVarDeclaration("const", name, AnyAnnotation(""), cd.BaseExpr, cd.BaseExpr.GetPos()))
				cd.BaseClass, cd.BaseExpr = name, nil
			}
			out = append(out, st)
		}
		return out
	}
	prog.Body = lower(prog.Body)
	Inspect(prog, func(n Node) bool {
		if b, ok := n.(*BlockStatement); ok {
			b.Body = lower(b.Body)
		}
		return true
	})
	lowerExprBases(prog)
}

// heritageOf is a class's heritage still written as an expression: a call
// or any other one, and clears it.
func heritageOf(cd *ClassDeclaration) Expression {
	switch {
	case cd.BaseCall != nil:
		x := Expression(cd.BaseCall)
		cd.BaseCall = nil
		return x
	case cd.BaseExpr != nil:
		x := cd.BaseExpr
		cd.BaseExpr = nil
		return x
	}
	return nil
}

// lowerExprBases lowers a class expression's heritage expression. `var K =
// class extends E {}` binds E before its statement, so the class keeps the
// binding's name. Any other class expression evaluates E into a variable
// of its own as it is evaluated: `(__kml_baseN = E, class extends
// __kml_baseN {})`.
func lowerExprBases(prog *Program) {
	seq := 0
	var hoisted []Statement
	stmts := func(body []Statement) []Statement {
		var out []Statement
		for _, st := range body {
			if v, ok := st.(*VarDeclaration); ok {
				if ce, ok := v.Init.(*ClassExpression); ok {
					if x := heritageOf(ce.Decl); x != nil {
						seq++
						name := fmt.Sprintf("%s__kml_base%d", v.Name, seq)
						out = append(out, NewVarDeclaration("const", name, AnyAnnotation(""), x, x.GetPos()))
						ce.Decl.BaseClass = name
					}
				}
			}
			out = append(out, st)
		}
		return out
	}
	prog.Body = stmts(prog.Body)
	Inspect(prog, func(n Node) bool {
		if b, ok := n.(*BlockStatement); ok {
			b.Body = stmts(b.Body)
		}
		return true
	})
	var rw func(n Node) Node
	rw = func(n Node) Node {
		RewriteChildren(n, rw)
		ce, ok := n.(*ClassExpression)
		if !ok {
			return n
		}
		x := heritageOf(ce.Decl)
		if x == nil {
			return n
		}
		seq++
		name := fmt.Sprintf("__kml_base%d", seq)
		pos := x.GetPos()
		hoisted = append(hoisted, NewVarDeclaration("var", name, AnyAnnotation(""), nil, pos))
		ce.Decl.BaseClass = name
		return NewSequenceExpression([]Expression{NewAssignmentExpression("=", NewIdentifier(name, pos), x, pos), ce}, pos)
	}
	for i, st := range prog.Body {
		prog.Body[i] = rw(st).(Statement)
	}
	prog.Body = append(hoisted, prog.Body...)
}
