// Package resolver implements KlainMainLang's multi-file module resolution:
// parses the entry file plus everything it transitively imports, validates
// import/export usage, and merges everything into one *ast.Program ready
// for codegen/llvm — which never sees an *ast.ImportDeclaration or
// *ast.ExportDeclaration node; both are fully consumed here.
//
// V1 scope (see docs/adr for the full writeup):
//   - Whole-program compilation, not separate compilation units: every
//     reachable file is merged into one combined AST before codegen runs.
//     There is no linker step and no per-file LLVM module boundary.
//   - Imported (non-entry) files may only contain declarations (function/
//     const/let/var/interface/type/enum) plus their own imports — no
//     executable top-level statements. Only the entry file's own top-level
//     statements become the program's actual runtime behavior. This is a
//     deliberate simplification: real ES modules run a file's top-level
//     code once, the first time it's imported, in dependency order — that
//     "run once, in order, guard against re-running on cycles" semantics is
//     real design/implementation work of its own, deferred for now.
//   - All top-level declaration names must be unique across every merged
//     file (not just within one file) — there is no true per-file module
//     scope yet, so two different (even unrelated) files cannot declare the
//     same-named function/interface/enum/etc. if both end up reachable
//     from the same entry file. A real per-file-scoped implementation
//     (mangled internal names, explicit alias resolution) is future work.
//   - Import aliasing (`import { a as b }`) is parsed but rejected with a
//     clear error — no AST-level renaming is attempted, since a naive
//     rename risks colliding with local shadowing in the importing file.
//   - Only relative paths (`./`, `../`) are supported, resolved against the
//     importing file's own directory, with `.ts` auto-appended if the path
//     has no extension. No `node_modules`, no index-file resolution — there
//     is no package ecosystem here.
package resolver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/parser"
)

type fileInfo struct {
	path     string
	prog     *ast.Program
	isEntry  bool
	exported map[string]bool
}

// ResolveProgram parses entryPath and everything it transitively imports,
// validates import/export usage, and returns one merged *ast.Program.
func ResolveProgram(entryPath string) (*ast.Program, error) {
	entryAbs, err := filepath.Abs(entryPath)
	if err != nil {
		return nil, fmt.Errorf("resolving entry path: %w", err)
	}

	files := map[string]*fileInfo{}
	var order []string // dependency-first visitation order of non-entry files

	var visit func(path string, isEntry bool) error
	visit = func(path string, isEntry bool) error {
		if _, seen := files[path]; seen {
			return nil // already visited, or in progress (cycle) — safe to skip
		}
		files[path] = &fileInfo{} // in-progress placeholder, guards against re-visiting on a cycle

		src, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		prog, err := parser.Parse(string(src))
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if !isEntry {
			if err := validateDeclarationsOnly(prog); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		}

		dir := filepath.Dir(path)
		for _, stmt := range prog.Body {
			imp, ok := stmt.(*ast.ImportDeclaration)
			if !ok {
				continue
			}
			resolved, err := resolveImportPath(dir, imp.Source)
			if err != nil {
				return fmt.Errorf("%d:%d: %w", imp.GetPos().Line, imp.GetPos().Col, err)
			}
			if err := visit(resolved, false); err != nil {
				return err
			}
		}

		files[path] = &fileInfo{path: path, prog: prog, isEntry: isEntry, exported: exportedNames(prog)}
		if !isEntry {
			order = append(order, path)
		}
		return nil
	}

	if err := visit(entryAbs, true); err != nil {
		return nil, err
	}

	// Validate every import statement's specifiers against the file it resolves to.
	for _, info := range files {
		dir := filepath.Dir(info.path)
		for _, stmt := range info.prog.Body {
			imp, ok := stmt.(*ast.ImportDeclaration)
			if !ok {
				continue
			}
			resolved, err := resolveImportPath(dir, imp.Source)
			if err != nil {
				return nil, fmt.Errorf("%d:%d: %w", imp.GetPos().Line, imp.GetPos().Col, err)
			}
			target := files[resolved]
			for _, spec := range imp.Specifiers {
				if spec.Local != spec.Imported {
					return nil, fmt.Errorf("%d:%d: import aliasing ('%s as %s') is not yet supported",
						imp.GetPos().Line, imp.GetPos().Col, spec.Imported, spec.Local)
				}
				if !target.exported[spec.Imported] {
					return nil, fmt.Errorf("%d:%d: '%s' has no exported member '%s'",
						imp.GetPos().Line, imp.GetPos().Col, imp.Source, spec.Imported)
				}
			}
		}
	}

	// Global name-uniqueness check across every merged file's declarations.
	declaredIn := map[string]string{}
	checkNames := func(path string, prog *ast.Program) error {
		for _, stmt := range prog.Body {
			name, ok := declNameOf(stmt)
			if !ok {
				continue
			}
			if prevPath, dup := declaredIn[name]; dup {
				return fmt.Errorf("'%s' is declared in both %s and %s — top-level names must be unique across all imported files (V1 scope; see resolver package docs)",
					name, prevPath, path)
			}
			declaredIn[name] = path
		}
		return nil
	}
	for _, path := range order {
		if err := checkNames(path, files[path].prog); err != nil {
			return nil, err
		}
	}
	if err := checkNames(entryAbs, files[entryAbs].prog); err != nil {
		return nil, err
	}

	// Merge: every non-entry file's declarations, then the entry file's own
	// full statement list — dropping ImportDeclaration and unwrapping
	// ExportDeclaration everywhere, since codegen/llvm knows neither node.
	merged := &ast.Program{}
	for _, path := range order {
		merged.Body = append(merged.Body, unwrap(files[path].prog.Body)...)
	}
	merged.Body = append(merged.Body, unwrap(files[entryAbs].prog.Body)...)
	return merged, nil
}

// validateDeclarationsOnly enforces the V1 restriction that imported
// (non-entry) files may only contain declarations and imports.
func validateDeclarationsOnly(prog *ast.Program) error {
	for _, stmt := range prog.Body {
		s := stmt
		if exp, ok := s.(*ast.ExportDeclaration); ok {
			s = exp.Decl
		}
		switch s.(type) {
		case *ast.FunctionDeclaration, *ast.VarDeclaration, *ast.InterfaceDeclaration,
			*ast.TypeAliasDeclaration, *ast.EnumDeclaration, *ast.ImportDeclaration:
			continue
		default:
			return fmt.Errorf("%d:%d: imported files may only contain declarations (function/const/let/var/interface/type/enum) and imports — no executable top-level statements",
				stmt.GetPos().Line, stmt.GetPos().Col)
		}
	}
	return nil
}

// declNameOf returns the name a top-level declaration statement introduces,
// unwrapping ExportDeclaration first.
func declNameOf(stmt ast.Statement) (string, bool) {
	if exp, ok := stmt.(*ast.ExportDeclaration); ok {
		stmt = exp.Decl
	}
	switch s := stmt.(type) {
	case *ast.FunctionDeclaration:
		return s.Name, true
	case *ast.VarDeclaration:
		return s.Name, true
	case *ast.InterfaceDeclaration:
		return s.Name, true
	case *ast.TypeAliasDeclaration:
		return s.Name, true
	case *ast.EnumDeclaration:
		return s.Name, true
	}
	return "", false
}

// exportedNames returns the set of top-level names a file exports.
func exportedNames(prog *ast.Program) map[string]bool {
	names := map[string]bool{}
	for _, stmt := range prog.Body {
		if _, ok := stmt.(*ast.ExportDeclaration); ok {
			if name, ok := declNameOf(stmt); ok {
				names[name] = true
			}
		}
	}
	return names
}

// resolveImportPath resolves a relative import specifier against the
// importing file's directory, auto-appending ".ts" if omitted, and confirms
// the resulting file exists.
func resolveImportPath(dir, source string) (string, error) {
	if !strings.HasPrefix(source, "./") && !strings.HasPrefix(source, "../") {
		return "", fmt.Errorf("import path '%s' must start with './' or '../' — bare/package-style imports are not supported", source)
	}
	joined := filepath.Join(dir, source)
	if filepath.Ext(joined) == "" {
		joined += ".ts"
	}
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("cannot find module '%s' (resolved to %s)", source, abs)
	}
	return abs, nil
}

// unwrap strips ImportDeclaration nodes and unwraps ExportDeclaration nodes
// from a file's statement list, for merging into the combined program.
func unwrap(stmts []ast.Statement) []ast.Statement {
	out := make([]ast.Statement, 0, len(stmts))
	for _, s := range stmts {
		if _, ok := s.(*ast.ImportDeclaration); ok {
			continue
		}
		if exp, ok := s.(*ast.ExportDeclaration); ok {
			out = append(out, exp.Decl)
			continue
		}
		out = append(out, s)
	}
	return out
}
