package llvm

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestRepresentationConversionIsConvert: one function, convert, changes a
// value's representation (convertChecked adds the mismatch diagnostic).
// The JS abstract operations at argument positions are named for theirs
// (toIndexArg, toNumberArgRef, toStringArg, toTypedArrayElem); no
// function or type is named coerce.
func TestRepresentationConversionIsConvert(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			var id *ast.Ident
			switch d := n.(type) {
			case *ast.FuncDecl:
				id = d.Name
			case *ast.TypeSpec:
				id = d.Name
			}
			if id != nil && strings.Contains(strings.ToLower(id.Name), "coerc") {
				t.Errorf("%s: %s", fset.Position(id.Pos()), id.Name)
			}
			return true
		})
	}
}
