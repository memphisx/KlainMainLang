package llvm

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// stringCaseAllowance is the number of string-literal case values in this
// package's switches. A builtin's name belongs in its owner's table (an
// intrinsic, a method-type table), registered once, rather than in another
// `case "name":` arm that has to agree with the others (TDD-00230).
const stringCaseAllowance = 784

// TestStringCaseArmsOnlyShrink holds the count of string case values at the
// allowance: more fails, and fewer asks for the allowance to come down.
func TestStringCaseArmsOnlyShrink(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	n := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(x ast.Node) bool {
			sw, ok := x.(*ast.SwitchStmt)
			if !ok || sw.Tag == nil {
				return true
			}
			for _, c := range sw.Body.List {
				for _, v := range c.(*ast.CaseClause).List {
					if lit, ok := v.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						n++
					}
				}
			}
			return true
		})
	}
	switch {
	case n > stringCaseAllowance:
		t.Errorf("%d string case values, allowance %d: dispatch a builtin's name through its owner's table", n, stringCaseAllowance)
	case n < stringCaseAllowance:
		t.Errorf("%d string case values, allowance %d: lower the allowance", n, stringCaseAllowance)
	}
}
