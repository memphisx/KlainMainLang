package conform

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/parser"
)

// GlobalNames is every global value and type name TypeScript's library
// (tsLib: src/lib) and Node's declarations (typesNode: @types/node) declare:
// each library file's top-level declarations, the top-level declarations of
// a Node declaration file that is a script (no top-level import or export),
// and every `global { … }` block's. A name in none of them is one tsc
// reports as TS2304 whichever libraries a program is checked with.
// A name is marked value when some declaration of it is one (a variable, a
// function, a class, an enum, a namespace): the names tsc's spelling
// suggestion offers for a value reference.
func GlobalNames(tsLib, typesNode string) (names map[string]bool, err error) {
	names = map[string]bool{}
	libFiles, _ := filepath.Glob(filepath.Join(tsLib, "*.d.ts"))
	if len(libFiles) == 0 {
		return nil, fmt.Errorf("no TypeScript library at %s (run tools/conformance/fetch.sh)", tsLib)
	}
	for _, f := range libFiles {
		src, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		if err := addTopLevel(names, string(src)); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
	}
	var nodeFiles []string
	err = filepath.Walk(typesNode, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, ".d.ts") {
			nodeFiles = append(nodeFiles, p)
		}
		return err
	})
	if err != nil || len(nodeFiles) == 0 {
		return nil, fmt.Errorf("no Node declarations at %s (run tools/conformance/fetch.sh)", typesNode)
	}
	for _, f := range nodeFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		src := string(data)
		if !topLevelModule.MatchString(src) {
			if err := addTopLevel(names, src); err != nil {
				return nil, fmt.Errorf("%s: %w", f, err)
			}
		}
		for _, body := range globalBlocks(src) {
			if err := addTopLevel(names, body); err != nil {
				return nil, fmt.Errorf("%s (global block): %w", f, err)
			}
		}
	}
	return names, nil
}

// GlobalLines is names as lib/tsglobals.txt lists them, sorted: one name a
// line, a value's marked " value".
func GlobalLines(names map[string]bool) []string {
	var lines []string
	for n, value := range names {
		if value {
			n += " value"
		}
		lines = append(lines, n)
	}
	sort.Strings(lines)
	return lines
}

// topLevelModule matches a top-level import or export, which makes a
// declaration file a module whose declarations are not global.
var topLevelModule = regexp.MustCompile(`(?m)^(import|export)\b`)

// globalStart matches the opening of a `global { … }` augmentation.
var globalStart = regexp.MustCompile(`(?m)^\s*(declare\s+)?global\s*\{`)

// globalBlocks returns the body of every `global { … }` block in src. Braces
// inside comments and string literals do not count.
func globalBlocks(src string) []string {
	var out []string
	for _, loc := range globalStart.FindAllStringIndex(src, -1) {
		start := loc[1] - 1
		if end := matchingBrace(src, start); end > start {
			out = append(out, src[start+1:end])
		}
	}
	return out
}

// matchingBrace is the index of the `}` closing the `{` at open, or -1.
func matchingBrace(src string, open int) int {
	depth := 0
	for i := open; i < len(src); i++ {
		switch c := src[i]; {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return -1
			}
			i += end + 3
		case c == '"' || c == '\'' || c == '`':
			for i++; i < len(src) && src[i] != c; i++ {
				if src[i] == '\\' {
					i++
				}
			}
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// addTopLevel adds the names src's top-level statements declare, each true
// when it is a value's.
func addTopLevel(names map[string]bool, src string) error {
	prog, err := parser.ParseDeclarations(src)
	if err != nil {
		return err
	}
	nested := map[ast.Statement]bool{} // a namespace's members, not globals
	for _, g := range prog.NamespaceGroups {
		for _, m := range g.Members {
			nested[m] = true
		}
	}
	for _, st := range prog.Body {
		if nested[st] {
			continue
		}
		switch n := st.(type) {
		case *ast.InterfaceDeclaration:
			names[n.Name] = names[n.Name] || false
		case *ast.TypeAliasDeclaration:
			names[n.Name] = names[n.Name] || false
		case *ast.VarDeclaration:
			names[n.Name] = true
		case *ast.FunctionDeclaration:
			names[n.Name] = true
		case *ast.ClassDeclaration:
			names[n.Name] = true
		case *ast.EnumDeclaration:
			names[n.Name] = true
		}
	}
	for _, g := range prog.NamespaceGroups {
		names[strings.SplitN(g.Name, ".", 2)[0]] = true
	}
	for _, n := range prog.AmbientNames {
		names[n] = names[n] || false // an erased `declare type`/`declare class`
	}
	return nil
}

// moduleStart matches the opening of a `declare module "name" { … }` block.
var moduleStart = regexp.MustCompile(`(?m)^declare\s+module\s+["']([^"']+)["']\s*\{`)

var (
	moduleDecl = regexp.MustCompile(`^\s*(?:export\s+)?(?:declare\s+)?(?:abstract\s+)?(?:interface|class|type|enum)\s+([A-Za-z_$][\w$]*)`)
	moduleStar = regexp.MustCompile(`^\s*export\s+\*\s+from\s+["']([^"']+)["']`)
)

// ModuleExports is every type name each of Node's modules exports (an
// interface, class, type alias or enum), by module specifier (`http`,
// `node:http`), read from @types/node's `declare module` blocks: an ambient
// module's top-level declarations are its exports, and `export * from` adds
// another module's. A function or constant is not listed: it is a value only.
// The names are read from the text, since the files do not all parse
// (ADR-01144).
func ModuleExports(typesNode string) (map[string][]string, error) {
	exports := map[string]map[string]bool{}
	stars := map[string][]string{}
	err := filepath.Walk(typesNode, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".d.ts") {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		src := string(data)
		for _, m := range moduleStart.FindAllStringSubmatchIndex(src, -1) {
			name := src[m[2]:m[3]]
			open := m[1] - 1
			end := matchingBrace(src, open)
			if end < 0 {
				continue
			}
			if exports[name] == nil {
				exports[name] = map[string]bool{}
			}
			for _, line := range strings.Split(topLevelText(src[open+1:end]), "\n") {
				switch {
				case moduleStar.MatchString(line):
					stars[name] = append(stars[name], moduleStar.FindStringSubmatch(line)[1])
				case moduleDecl.MatchString(line):
					exports[name][moduleDecl.FindStringSubmatch(line)[1]] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var resolve func(name string, seen map[string]bool) map[string]bool
	resolve = func(name string, seen map[string]bool) map[string]bool {
		out := map[string]bool{}
		if seen[name] {
			return out
		}
		seen[name] = true
		for n := range exports[name] {
			out[n] = true
		}
		for _, s := range stars[name] {
			for n := range resolve(s, seen) {
				out[n] = true
			}
		}
		return out
	}
	result := map[string][]string{}
	for name := range exports {
		var names []string
		for n := range resolve(name, map[string]bool{}) {
			if n != "default" {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		result[name] = names
	}
	return result, nil
}

// topLevelText is body with every nested `{ … }` block's contents, comments
// and string literals removed, so each remaining line is a top-level one.
func topLevelText(body string) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(body); i++ {
		switch c := body[i]; {
		case c == '/' && i+1 < len(body) && body[i+1] == '/':
			for i < len(body) && body[i] != '\n' {
				i++
			}
			b.WriteByte('\n')
		case c == '/' && i+1 < len(body) && body[i+1] == '*':
			end := strings.Index(body[i+2:], "*/")
			if end < 0 {
				return b.String()
			}
			b.WriteString(strings.Repeat("\n", strings.Count(body[i:i+end+4], "\n")))
			i += end + 3
		case c == '"' || c == '\'' || c == '`':
			start := i
			for i++; i < len(body) && body[i] != c; i++ {
				if body[i] == '\\' {
					i++
				}
			}
			if depth == 0 && i < len(body) {
				b.WriteString(body[start : i+1])
			}
		case c == '{':
			if depth == 0 {
				b.WriteByte('{')
			}
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				b.WriteByte('}')
			}
		default:
			if depth == 0 || c == '\n' {
				b.WriteByte(c)
			}
		}
	}
	return b.String()
}

// referenceLib matches a `/// <reference lib="…" />` directive.
var referenceLib = regexp.MustCompile(`(?m)^/// <reference lib="([^"]+)" />`)

// LibValueNames is every global value name the TypeScript library files
// libs (`es2015`, `dom`, …, as a `lib` option names them) declare, with the
// files they reference: the library tsc checks a program with.
func LibValueNames(tsLib string, libs []string) (map[string]bool, error) {
	names := map[string]bool{}
	seen := map[string]bool{}
	for len(libs) > 0 {
		l := strings.ToLower(libs[0])
		libs = libs[1:]
		if l == "es6" {
			l = "es2015"
		}
		if seen[l] {
			continue
		}
		seen[l] = true
		data, err := os.ReadFile(filepath.Join(tsLib, l+".d.ts"))
		if err != nil {
			if data, err = os.ReadFile(filepath.Join(tsLib, l+".generated.d.ts")); err != nil {
				return nil, fmt.Errorf("no library %q in %s", l, tsLib)
			}
		}
		for _, m := range referenceLib.FindAllStringSubmatch(string(data), -1) {
			libs = append(libs, m[1])
		}
		if err := addTopLevel(names, string(data)); err != nil {
			return nil, fmt.Errorf("%s: %w", l, err)
		}
	}
	for n, value := range names {
		if !value {
			delete(names, n)
		}
	}
	return names, nil
}
