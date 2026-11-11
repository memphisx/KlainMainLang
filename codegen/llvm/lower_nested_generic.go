package llvm

import "KlainMainLang/ast"

// lowerNestedGenericFuncs erases the type parameters of each generic function
// declaration inside a block (a function body, or a block statement
// anywhere) to any in its annotations, as a generic function expression's
// are: a nested function is compiled once, closing over its enclosing
// function's locals, so it cannot be specialized per instantiation the way a
// top-level generic declaration is. The checker has typed every call site
// already (warmFront); the declaration stays one, hoisted and named as before.
func lowerNestedGenericFuncs(prog *ast.Program) map[*ast.FunctionDeclaration]bool {
	erased := map[*ast.FunctionDeclaration]bool{}
	ast.Inspect(prog, func(n ast.Node) bool {
		b, ok := n.(*ast.BlockStatement)
		if !ok {
			return true
		}
		for _, st := range b.Body {
			fd, ok := st.(*ast.FunctionDeclaration)
			if !ok || len(fd.TypeParams) == 0 || fd.Body == nil {
				continue
			}
			fd.TypeParams, fd.TypeParamConstraints, fd.TypeParamDefaults = nil, nil, nil
			erased[fd] = true
		}
		return true
	})
	return erased
}

// hasNestedGenericFunc reports a generic function declaration inside a
// block, which lowerNestedGenericFuncs erases.
func hasNestedGenericFunc(prog *ast.Program) bool {
	found := false
	ast.Inspect(prog, func(n ast.Node) bool {
		if b, ok := n.(*ast.BlockStatement); ok && !found {
			for _, st := range b.Body {
				if fd, ok := st.(*ast.FunctionDeclaration); ok && len(fd.TypeParams) > 0 && fd.Body != nil {
					found = true
				}
			}
		}
		return !found
	})
	return found
}
