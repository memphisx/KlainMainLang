// Package resolver implements KlainMainLang's multi-file module resolution:
// parses the entry file plus everything it transitively imports, validates
// import/export usage, and merges everything into one *ast.Program ready
// for codegen/llvm — which never sees an *ast.ImportDeclaration or
// *ast.ExportDeclaration node; both are fully consumed here.
//
// Scope (see docs/adr for the full writeup):
//   - Whole-program compilation, not separate compilation units: every
//     reachable file is merged into one combined AST before codegen runs.
//     There is no linker step and no per-file LLVM module boundary.
//   - Imported (non-entry) files may only contain declarations (function/
//     const/let/var/interface/type/enum/class) plus their own imports — no
//     executable top-level statements. Only the entry file's own top-level
//     statements become the program's actual runtime behavior. This is a
//     deliberate simplification: real ES modules run a file's top-level
//     code once, the first time it's imported, in dependency order — that
//     "run once, in order, guard against re-running on cycles" semantics is
//     real design/implementation work of its own, deferred for now.
//   - True per-file module scope (TDD-00041): every top-level declaration
//     gets a file-private mangled name (see rename.go's mangleFileDecls),
//     and every reference to it — within its own file, or via another
//     file's `import` — is rewritten to match, via a real scope-aware walk
//     (rename.go's renameFile) rather than a blind find-and-replace. Two
//     unrelated files may freely declare the same top-level name; only an
//     importing file that binds two different files' exports to the same
//     local name (with no `as` to disambiguate) is rejected.
//   - Import aliasing (`import { a as b }`) is fully supported — it's a
//     direct, safe consequence of the per-file rename mechanism above:
//     `b` simply *is* the target's mangled name for `a` inside the
//     importing file, no separate rename step or shadowing risk involved.
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
	index    int               // assigned at first visit; used to build this file's mangled-name suffix (TDD-00041)
	mangled  map[string]string // original top-level declaration name -> this file's mangled name for it
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
	nextIndex := 0

	var visit func(path string, isEntry bool) error
	visit = func(path string, isEntry bool) error {
		if _, seen := files[path]; seen {
			return nil // already visited, or in progress (cycle) — safe to skip
		}
		// In-progress placeholder, guards against re-visiting on a cycle.
		// The index is assigned here (not when the file is finalized below)
		// so every file — including one still being visited via a cycle —
		// has a stable, unique mangled-name suffix (TDD-00041) from the
		// moment it's first seen.
		files[path] = &fileInfo{index: nextIndex}
		nextIndex++

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

		files[path] = &fileInfo{path: path, prog: prog, isEntry: isEntry, exported: exportedNames(prog), index: files[path].index}
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
				if !target.exported[spec.Imported] {
					return nil, fmt.Errorf("%d:%d: '%s' has no exported member '%s'",
						imp.GetPos().Line, imp.GetPos().Col, imp.Source, spec.Imported)
				}
			}
		}
	}

	allPaths := make([]string, 0, len(order)+1)
	allPaths = append(allPaths, order...)
	allPaths = append(allPaths, entryAbs)

	// TDD-00041: give every file's own top-level declarations a file-private
	// mangled name (also catches genuine in-file duplicate declarations,
	// same as the old global check did for the same-file case). Must fully
	// complete for every file before any file's imports are rewritten below
	// — an importing file needs the *target's* mangled names already computed.
	for _, path := range allPaths {
		info := files[path]
		mangled, err := mangleFileDecls(path, info.prog, info.index)
		if err != nil {
			return nil, err
		}
		info.mangled = mangled
	}

	// Build each file's own combined lookup table (its own mangled decls
	// plus its import bindings, honoring `as` aliasing) and rewrite every
	// reference in that file accordingly.
	for _, path := range allPaths {
		info := files[path]
		lookup := make(map[string]string, len(info.mangled))
		for orig, m := range info.mangled {
			lookup[orig] = m
		}
		dir := filepath.Dir(path)
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
				if _, dup := lookup[spec.Local]; dup {
					return nil, fmt.Errorf("%d:%d: '%s' is already declared in this file — use 'as' to import it under a different local name",
						imp.GetPos().Line, imp.GetPos().Col, spec.Local)
				}
				lookup[spec.Local] = target.mangled[spec.Imported]
			}
		}
		renameFile(info.prog, lookup)
	}

	// Defensive: mangled names are constructed to already be unique (each
	// file's own suffix), but assert it directly rather than trusting the
	// scheme blindly — this should never actually fire.
	declaredIn := map[string]string{}
	for _, path := range allPaths {
		for _, stmt := range files[path].prog.Body {
			name, ok := declNameOf(stmt)
			if !ok {
				continue
			}
			if prev, dup := declaredIn[name]; dup {
				return nil, fmt.Errorf("internal error: mangled name '%s' collided between %s and %s", name, prev, path)
			}
			declaredIn[name] = path
		}
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
			*ast.TypeAliasDeclaration, *ast.EnumDeclaration, *ast.ImportDeclaration, *ast.ClassDeclaration:
			continue
		default:
			return fmt.Errorf("%d:%d: imported files may only contain declarations (function/const/let/var/interface/type/enum/class) and imports — no executable top-level statements",
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
	case *ast.ClassDeclaration:
		return s.Name, true
	}
	return "", false
}

// setDeclName overwrites the name a top-level declaration statement
// introduces — declNameOf's rename counterpart, used to apply mangling.
func setDeclName(stmt ast.Statement, name string) {
	if exp, ok := stmt.(*ast.ExportDeclaration); ok {
		stmt = exp.Decl
	}
	switch s := stmt.(type) {
	case *ast.FunctionDeclaration:
		s.Name = name
	case *ast.VarDeclaration:
		s.Name = name
	case *ast.InterfaceDeclaration:
		s.Name = name
	case *ast.TypeAliasDeclaration:
		s.Name = name
	case *ast.EnumDeclaration:
		s.Name = name
	case *ast.ClassDeclaration:
		s.Name = name
	}
}

// mangleName builds fileIdx's file-private internal name for a top-level
// declaration originally named name — unique per file (TDD-00041), and
// valid as an LLVM/C identifier fragment (alphanumeric + underscore only).
func mangleName(name string, fileIdx int) string {
	return fmt.Sprintf("%s__kml_mod%d", name, fileIdx)
}

// mangleFileDecls assigns every top-level declaration in prog a file-private
// mangled name (renaming the declaration in place via setDeclName) and
// returns the original-name -> mangled-name map for this file. Errors on a
// genuine in-file duplicate declaration — two different files sharing a
// name is no longer an error (that's the whole point of TDD-00041), but two
// declarations of the same name within one file still is, same as before.
func mangleFileDecls(path string, prog *ast.Program, fileIdx int) (map[string]string, error) {
	mangled := map[string]string{}
	for _, stmt := range prog.Body {
		name, ok := declNameOf(stmt)
		if !ok {
			continue
		}
		if _, dup := mangled[name]; dup {
			return nil, fmt.Errorf("'%s' is declared more than once in %s", name, path)
		}
		newName := mangleName(name, fileIdx)
		mangled[name] = newName
		setDeclName(stmt, newName)
	}
	return mangled, nil
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
