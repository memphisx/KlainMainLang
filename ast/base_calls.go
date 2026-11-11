package ast

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
}
