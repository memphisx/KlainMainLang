package llvm

import (
	"KlainMainLang/ast"
)

// specializeMixins rewrites each call of a mixin function (a top-level
// function with a parameter some class in its body extends, `function
// M(Base) { return class extends Base {} }`) whose argument there is a
// top-level class into a call of a copy of the function made for that
// class, where the parameter names the class. A class's layout is fixed at
// compile time; the copy's class extends one that is known. Runs before
// the checker, so the copies are typed as written.
func specializeMixins(prog *ast.Program) {
	classes := map[string]bool{}
	fns := map[string]ast.Statement{} // a function, or a const holding one
	for _, st := range prog.Body {
		switch d := st.(type) {
		case *ast.ClassDeclaration:
			classes[d.Name] = true
		case *ast.FunctionDeclaration:
			fns[d.Name] = d
		case *ast.VarDeclaration:
			if d.Kind == "const" && mixinParams(d) != nil {
				fns[d.Name] = d
			}
		}
	}
	// The parameter index each mixin's classes extend.
	mixin := map[string][]int{}
	for name, fn := range fns {
		for i, p := range mixinParams(fn) {
			if p.Rest || p.Name == "" {
				continue
			}
			found := false
			ast.Inspect(fn, func(n ast.Node) bool {
				if cd, ok := n.(*ast.ClassDeclaration); ok && cd.BaseClass == p.Name && !cd.BaseQualified {
					found = true
				}
				return !found
			})
			if found {
				mixin[name] = append(mixin[name], i)
			}
		}
	}
	if len(mixin) == 0 {
		return
	}
	// A constant holding a mixin's result is a class too (`const AU =
	// A(User)`), so mixins compose (`T(AU)`).
	for _, st := range prog.Body {
		if vd, ok := st.(*ast.VarDeclaration); ok && vd.Kind == "const" {
			if call, ok := vd.Init.(*ast.CallExpression); ok {
				if id, ok := call.Callee.(*ast.Identifier); ok && mixin[id.Name] != nil {
					classes[vd.Name] = true
				}
			}
		}
	}
	copies := map[string]ast.Statement{}
	var added []ast.Statement
	ast.Inspect(prog, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpression)
		if !ok {
			return true
		}
		id, ok := call.Callee.(*ast.Identifier)
		if !ok || mixin[id.Name] == nil {
			return true
		}
		fn := fns[id.Name]
		params := mixinParams(fn)
		subs := map[string]string{}
		key := id.Name
		for _, i := range mixin[id.Name] {
			if i >= len(call.Args) {
				return true
			}
			arg, ok := call.Args[i].(*ast.Identifier)
			if !ok || !classes[arg.Name] {
				return true
			}
			subs[params[i].Name] = arg.Name
			key += "__kml_mx_" + arg.Name
		}
		if _, ok := copies[key]; !ok {
			c := ast.Clone(fn)
			switch d := c.(type) {
			case *ast.FunctionDeclaration:
				d.Name = key
				substituteBase(d.Body, subs)
			case *ast.VarDeclaration:
				d.Name = key
				substituteBase(d.Init, subs)
			}
			copies[key] = c
			added = append(added, c)
		}
		id.Name = key
		return true
	})
	// A mixin no longer named anywhere (every call specialized) is gone:
	// its class extends a value no copy is made for.
	used := map[string]bool{}
	ast.Inspect(prog, func(n ast.Node) bool {
		if id, ok := n.(*ast.Identifier); ok {
			used[id.Name] = true
		}
		return true
	})
	var body, consts []ast.Statement
	for _, st := range prog.Body {
		switch d := st.(type) {
		case *ast.FunctionDeclaration:
			if mixin[d.Name] != nil && !used[d.Name] {
				continue
			}
		case *ast.VarDeclaration:
			if mixin[d.Name] != nil && !used[d.Name] {
				continue
			}
		}
		body = append(body, st)
	}
	// A function copy is hoisted; a constant copy goes before the first
	// statement, as its original was declared before any call of it.
	for _, c := range added {
		if _, ok := c.(*ast.VarDeclaration); ok {
			consts = append(consts, c)
		} else {
			body = append(body, c)
		}
	}
	prog.Body = append(consts, body...)
}

// mixinParams is the parameters of a function declaration, or of the
// arrow or function expression a constant holds; nil for anything else.
func mixinParams(st ast.Node) []ast.Param {
	switch d := st.(type) {
	case *ast.FunctionDeclaration:
		return d.Params
	case *ast.VarDeclaration:
		switch f := d.Init.(type) {
		case *ast.ArrowFunction:
			return f.Params
		case *ast.FunctionExpression:
			return f.Params
		}
	}
	return nil
}

// substituteBase makes every read of a substituted parameter in body,
// and every class extending one, name its class.
func substituteBase(body ast.Node, subs map[string]string) {
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ClassDeclaration:
			if c, ok := subs[x.BaseClass]; ok && !x.BaseQualified {
				x.BaseClass = c
			}
		case *ast.Identifier:
			if c, ok := subs[x.Name]; ok {
				x.Name = c
			}
		}
		return true
	})
}

// classOfCallConst is the class a top-level constant name holds when it is
// initialized by a call of a top-level function every return of which
// names that one class (`const AU = A(User)`, A returning its class).
func (e *Emitter) classOfCallConst(name string, classes map[string]*ast.ClassDeclaration) (string, bool) {
	if e.prog == nil {
		return "", false
	}
	var call *ast.CallExpression
	fns := map[string]*ast.FunctionDeclaration{}
	for _, st := range e.prog.Body {
		switch d := st.(type) {
		case *ast.VarDeclaration:
			if d.Name == name && d.Kind == "const" {
				call, _ = d.Init.(*ast.CallExpression)
			}
		case *ast.FunctionDeclaration:
			fns[d.Name] = d
		}
	}
	if call == nil {
		return "", false
	}
	id, ok := call.Callee.(*ast.Identifier)
	if !ok || fns[id.Name] == nil {
		return "", false
	}
	class := ""
	same := true
	ast.Inspect(fns[id.Name].Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FunctionDeclaration, *ast.ArrowFunction, *ast.FunctionExpression:
			return false // another function's returns
		case *ast.ReturnStatement:
			r, ok := x.Value.(*ast.Identifier)
			if !ok || classes[r.Name] == nil || class != "" && class != r.Name {
				same = false
			} else {
				class = r.Name
			}
		}
		return same
	})
	return class, same && class != ""
}
