package ast_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"KlainMainLang/ast"
	"KlainMainLang/parser"
)

// TestWalkGenerated fails when walk_generated.go is stale: a node kind or a
// child field was added to nodes.go or type_nodes.go without `go generate ./ast`.
func TestWalkGenerated(t *testing.T) {
	out := filepath.Join(t.TempDir(), "walk.go")
	cmd := exec.Command("go", "run", "./gen", out, "nodes.go", "type_nodes.go")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generator: %v\n%s", err, b)
	}
	want, _ := os.ReadFile(out)
	got, _ := os.ReadFile("walk_generated.go")
	if !bytes.Equal(got, want) {
		t.Fatal("walk_generated.go is stale — run `go generate ./ast`")
	}
}

// reflectWalk is an independent oracle: every Node reachable from n through
// exported fields, pre-order, with the generator's exclusions (type syntax,
// Program's aliased and separate-module fields).
func reflectWalk(n ast.Node, out *[]ast.Node) {
	*out = append(*out, n)
	var walk func(v reflect.Value, owner string)
	walk = func(v reflect.Value, owner string) {
		switch v.Kind() {
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			if c, ok := v.Interface().(ast.Node); ok {
				if rv := reflect.ValueOf(c); rv.Kind() == reflect.Pointer && rv.IsNil() {
					return
				}
				reflectWalk(c, out)
			}
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if c, ok := v.Interface().(ast.Node); ok {
				reflectWalk(c, out)
				return
			}
			walk(v.Elem(), owner)
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), owner)
			}
		case reflect.Struct:
			t := v.Type()
			if t.Name() == "TypeAnnotation" || t.Name() == "Pos" {
				return
			}
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				if !f.IsExported() {
					continue
				}
				if t.Name() == "Program" && (f.Name == "DynamicImportNodes" || f.Name == "WorkerModules" || f.Name == "NamespaceGroups") {
					continue
				}
				if t.Name() == "FunctionDeclaration" && f.Name == "Overloads" {
					continue
				}
				walk(v.Field(i), t.Name())
			}
		}
	}
	walk(reflect.ValueOf(n).Elem(), "")
}

func TestInspectMatchesReflectionOracle(t *testing.T) {
	var files []string
	for _, root := range []string{"../examples", "../tests"} {
		filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err == nil && (strings.HasSuffix(p, ".ts") || strings.HasSuffix(p, ".js")) {
				files = append(files, p)
			}
			return nil
		})
	}
	// test sources embedded in Go files: every backquoted string that parses
	srcs := map[string]string{}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		srcs[f] = string(b)
	}
	goTests, _ := filepath.Glob("../tests/*_test.go")
	for _, f := range goTests {
		b, _ := os.ReadFile(f)
		parts := strings.Split(string(b), "`")
		for i := 1; i < len(parts); i += 2 {
			srcs[fmt.Sprintf("%s#%d", f, i)] = parts[i]
		}
	}
	checked, typeTrees := 0, 0
	for name, src := range srcs {
		prog, err := parser.Parse(src)
		if err != nil {
			continue
		}
		sameWalk(t, name, prog)
		// Type syntax hangs off the annotations, outside the statement walk:
		// check every type tree the same way.
		for _, root := range typeNodeRoots(prog) {
			sameWalk(t, name, root)
			typeTrees++
		}
		checked++
	}
	if checked < 1000 || typeTrees < 1000 {
		t.Fatalf("only %d sources parsed, %d type trees — corpus not found?", checked, typeTrees)
	}
	t.Logf("%d sources, %d type trees: Inspect == reflection oracle", checked, typeTrees)
}

func sameWalk(t *testing.T, name string, root ast.Node) {
	t.Helper()
	var want, got []ast.Node
	reflectWalk(root, &want)
	ast.Inspect(root, func(n ast.Node) bool { got = append(got, n); return true })
	if len(got) != len(want) {
		t.Fatalf("%s: Inspect visited %d nodes, oracle %d", name, len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: node %d differs: %T vs %T", name, i, got[i], want[i])
		}
	}
}

// typeNodeRoots returns the Node of every TypeAnnotation reachable from n.
func typeNodeRoots(n ast.Node) []ast.Node {
	var roots []ast.Node
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			walk(v.Elem())
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.Struct:
			if v.Type() == reflect.TypeOf(ast.TypeAnnotation{}) && v.CanAddr() {
				if ta := v.Addr().Interface().(*ast.TypeAnnotation); ta.TypeNode() != nil {
					roots = append(roots, ta.TypeNode())
				}
			}
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i))
				}
			}
		}
	}
	walk(reflect.ValueOf(n))
	return roots
}
