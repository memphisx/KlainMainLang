package resolver

import (
	"KlainMainLang/ast"
	"KlainMainLang/diag"
)

// unsupportedHostBases are the builtin classes that cannot be a class's
// base yet: their values are native records a class instance does not
// embed.
var unsupportedHostBases = map[string]bool{
	"Request": true, "Response": true, "ReadableStream": true, "WritableStream": true,
	"TransformStream": true, "DecompressionStream": true, "XMLHttpRequest": true,
}

// hostBaseDiags reports each class of prog extending one of
// unsupportedHostBases that the file does not declare itself.
func hostBaseDiags(prog *ast.Program, offset func(line, col int) int) []*diag.Diagnostic {
	own := map[string]bool{}
	ast.Inspect(prog, func(n ast.Node) bool {
		if cd, ok := n.(*ast.ClassDeclaration); ok {
			own[cd.Name] = true
		}
		return true
	})
	var out []*diag.Diagnostic
	ast.Inspect(prog, func(n ast.Node) bool {
		if cd, ok := n.(*ast.ClassDeclaration); ok && unsupportedHostBases[cd.BaseClass] && !own[cd.BaseClass] && !cd.BaseQualified {
			p := cd.GetPos()
			start := offset(p.Line, p.Col)
			out = append(out, diag.New(diag.HostBaseUnsupported, diag.Span{Pos: diag.Pos{Line: p.Line, Col: p.Col}, Start: start, End: start}, cd.BaseClass))
		}
		return true
	})
	return out
}

// constructDiags are a file's constructs valid in TypeScript that this
// compiler does not implement yet: a class base (hostBaseDiags) or a
// decorated member (decoratedMemberDiags) under decorators, the dialect
// ("" is experimental, the default).
func constructDiags(prog *ast.Program, offset func(line, col int) int, decorators string) []*diag.Diagnostic {
	ds := append(hostBaseDiags(prog, offset), decoratedMemberDiags(prog, offset, decorators == "standard")...)
	ds = append(ds, byteStreamDiags(prog, offset)...)
	ds = append(ds, speciesDiags(prog, offset)...)
	return append(ds, embedDirDiags(prog, offset)...)
}

// ConstructChecks is constructDiags over one source file's program, as an
// error (nil when there are none).
func ConstructChecks(prog *ast.Program, src, decorators string) error {
	if ds := constructDiags(prog, diag.Offsets(src), decorators); len(ds) > 0 {
		sortDiags(ds)
		return diag.Errors(ds)
	}
	return nil
}

// embedDirDiags reports each call of klain:assets' embedDir (a named or a
// namespace import) whose path is not a string literal: the directory is
// read when the program is compiled.
func embedDirDiags(prog *ast.Program, offset func(line, col int) int) []*diag.Diagnostic {
	names, spaces := map[string]bool{}, map[string]bool{}
	for _, st := range prog.Body {
		if im, ok := st.(*ast.ImportDeclaration); ok && im.Source == "klain:assets" && !im.TypeOnly {
			if im.Namespace != "" {
				spaces[im.Namespace] = true
			}
			for _, sp := range im.Specifiers {
				if sp.Imported == "embedDir" {
					names[sp.Local] = true
				}
			}
		}
	}
	if len(names) == 0 && len(spaces) == 0 {
		return nil
	}
	var out []*diag.Diagnostic
	ast.Inspect(prog, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpression)
		if !ok {
			return true
		}
		hit := false
		switch c := call.Callee.(type) {
		case *ast.Identifier:
			hit = names[c.Name]
		case *ast.MemberExpression:
			id, ok := c.Object.(*ast.Identifier)
			hit = ok && spaces[id.Name] && c.Property == "embedDir"
		}
		if !hit {
			return true
		}
		literal := false
		if len(call.Args) == 1 {
			switch a := call.Args[0].(type) {
			case *ast.StringLiteral:
				literal = true
			case *ast.TemplateLiteral:
				literal = len(a.Exprs) == 0
			}
		}
		if !literal {
			p := call.GetPos()
			start := offset(p.Line, p.Col)
			out = append(out, diag.New(diag.EmbedDirPath, diag.Span{Pos: diag.Pos{Line: p.Line, Col: p.Col}, Start: start, End: start}))
		}
		return true
	})
	return out
}

// byteStreamDiags reports each `new ReadableStream` whose underlying source
// literal has a type of "bytes": a byte stream.
func byteStreamDiags(prog *ast.Program, offset func(line, col int) int) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	ast.Inspect(prog, func(n ast.Node) bool {
		ne, ok := n.(*ast.NewExpression)
		if !ok || ne.ClassName != "ReadableStream" || ne.Qualified || len(ne.Args) == 0 || declaresClass(prog, "ReadableStream") {
			return true
		}
		ol, ok := ne.Args[0].(*ast.ObjectLiteral)
		if !ok {
			return true
		}
		for _, p := range ol.Properties {
			if lit, ok := p.Value.(*ast.StringLiteral); ok && p.Key == "type" && p.KeyExpr == nil && lit.Value == "bytes" {
				at := p.Value.GetPos()
				start := offset(at.Line, at.Col)
				out = append(out, diag.New(diag.ByteStreamUnsupported, diag.Span{Pos: diag.Pos{Line: at.Line, Col: at.Col}, Start: start, End: start}))
			}
		}
		return true
	})
	return out
}

// declaresClass reports a class named name that prog declares.
func declaresClass(prog *ast.Program, name string) bool {
	found := false
	ast.Inspect(prog, func(n ast.Node) bool {
		if cd, ok := n.(*ast.ClassDeclaration); ok && cd.Name == name {
			found = true
		}
		return !found
	})
	return found
}

// speciesDiags reports a `static get [Symbol.species]()` of a class
// extending a builtin whose constructor codegen cannot pick at compile
// time: one return of `this`, undefined or null, the builtin, or a class
// extending the same builtin is chosen; anything else is unsupported.
func speciesDiags(prog *ast.Program, offset func(line, col int) int) []*diag.Diagnostic {
	bases := map[string]string{}
	others := map[string]bool{}
	ast.Inspect(prog, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.ClassDeclaration:
			bases[d.Name] = d.BaseClass
		case *ast.FunctionDeclaration:
			if d.Name != "" {
				others[d.Name] = true
			}
		case *ast.VarDeclaration:
			others[d.Name] = true
		}
		return true
	})
	root := func(c string) string {
		for i := 0; i < 64; i++ {
			b, ok := bases[c]
			if !ok {
				return c
			}
			if b == "" {
				return ""
			}
			c = b
		}
		return c
	}
	var out []*diag.Diagnostic
	ast.Inspect(prog, func(n ast.Node) bool {
		cd, ok := n.(*ast.ClassDeclaration)
		if !ok || cd.BaseClass == "" {
			return true
		}
		builtin := root(cd.Name)
		if builtin == "" {
			return true
		}
		for _, m := range cd.Methods {
			if !m.IsStatic || m.AccessorKind != "get" || m.Name != "@@species" {
				continue
			}
			var ret ast.Expression
			if m.Body != nil && len(m.Body.Body) == 1 {
				if r, isR := m.Body.Body[0].(*ast.ReturnStatement); isR {
					ret = r.Value
					if ret == nil {
						continue // `return;` is undefined
					}
				}
			}
			fine := false
			switch v := ret.(type) {
			case *ast.ThisExpression, *ast.NullLiteral:
				fine = true
			case *ast.Identifier:
				_, isClass := bases[v.Name]
				switch {
				case v.Name == builtin || v.Name == "undefined":
					fine = true
				case isClass:
					fine = root(v.Name) == builtin
				default:
					// An import or another module's class: not judged here.
					fine = !others[v.Name] && !globalCtors[v.Name]
				}
			}
			if !fine {
				p := m.GetPos()
				if p.Line == 0 {
					p = cd.GetPos()
				}
				start := offset(p.Line, p.Col)
				out = append(out, diag.New(diag.SpeciesComputed, diag.Span{Pos: diag.Pos{Line: p.Line, Col: p.Col}, Start: start, End: start}, cd.Name, builtin, builtin))
			}
		}
		return true
	})
	return out
}

// globalCtors are global constructors a species getter may name that no
// class of a program extends together with Array, a typed array or
// Promise.
var globalCtors = map[string]bool{"Object": true, "Map": true, "Set": true, "Function": true, "Error": true, "Date": true, "RegExp": true, "String": true, "Number": true, "Boolean": true}
