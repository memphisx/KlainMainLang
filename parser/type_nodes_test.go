package parser_test

import (
	"fmt"
	"strings"
	"testing"

	"KlainMainLang/ast"
	"KlainMainLang/parser"
)

// showType renders a type node tree compactly, for shape assertions.
func showType(n ast.Node) string {
	list := func(ns []ast.TypeNode) string {
		var parts []string
		for _, x := range ns {
			parts = append(parts, showType(x))
		}
		return strings.Join(parts, ", ")
	}
	sig := func(tps []*ast.TypeParameter, ps []*ast.SignatureParameter, ret ast.TypeNode) string {
		var b strings.Builder
		if len(tps) > 0 {
			var parts []string
			for _, tp := range tps {
				parts = append(parts, showType(tp))
			}
			b.WriteString("<" + strings.Join(parts, ", ") + ">")
		}
		var parts []string
		for _, p := range ps {
			parts = append(parts, showType(p))
		}
		b.WriteString("(" + strings.Join(parts, ", ") + ")")
		if ret != nil {
			b.WriteString(": " + showType(ret))
		}
		return b.String()
	}
	opt := func(b bool) string {
		if b {
			return "?"
		}
		return ""
	}
	dots := func(b bool) string {
		if b {
			return "..."
		}
		return ""
	}
	switch n := n.(type) {
	case *ast.KeywordType:
		return "kw:" + n.Keyword
	case *ast.TypeReference:
		name := strings.Join(append(append([]string{}, n.Qualifier...), n.Name), ".")
		if len(n.TypeArgs) > 0 {
			return "ref:" + name + "<" + list(n.TypeArgs) + ">"
		}
		return "ref:" + name
	case *ast.ArrayType:
		return "array(" + showType(n.ElementType) + ")"
	case *ast.TupleType:
		return "tuple(" + list(n.Elements) + ")"
	case *ast.NamedTupleMember:
		return dots(n.Rest) + n.Name + opt(n.Optional) + ": " + showType(n.Type)
	case *ast.OptionalType:
		return "optional(" + showType(n.Type) + ")"
	case *ast.RestType:
		return "rest(" + showType(n.Type) + ")"
	case *ast.UnionType:
		return "union(" + list(n.Types) + ")"
	case *ast.IntersectionType:
		return "and(" + list(n.Types) + ")"
	case *ast.ParenthesizedType:
		return "paren(" + showType(n.Type) + ")"
	case *ast.TypeParameter:
		if n.Constraint != nil {
			return n.Name + " extends " + showType(n.Constraint)
		}
		return n.Name
	case *ast.SignatureParameter:
		s := dots(n.Rest) + n.Name + opt(n.Optional)
		if n.Name != "" {
			s += ": "
		}
		return s + showType(n.Type)
	case *ast.FunctionType:
		return "fn" + sig(n.TypeParameters, n.Parameters, n.Type)
	case *ast.ConstructorType:
		return "new" + sig(n.TypeParameters, n.Parameters, n.Type)
	case *ast.TypeLiteral:
		var parts []string
		for _, m := range n.Members {
			parts = append(parts, showType(m))
		}
		return "{" + strings.Join(parts, "; ") + "}"
	case *ast.PropertySignature:
		return n.Name + opt(n.Optional) + ": " + showType(n.Type)
	case *ast.MethodSignature:
		return "method " + n.Name + opt(n.Optional) + sig(n.TypeParameters, n.Parameters, n.Type)
	case *ast.CallSignature:
		return "call" + sig(nil, n.Parameters, n.Type)
	case *ast.ConstructSignature:
		return "construct" + sig(nil, n.Parameters, n.Type)
	case *ast.IndexSignature:
		return "[" + n.KeyName + ": " + showType(n.KeyType) + "]: " + showType(n.Type)
	case *ast.MappedType:
		ro := ""
		if n.Readonly {
			ro = "readonly "
		}
		return "mapped(" + ro + n.KeyName + " in " + showType(n.Constraint) + opt(n.Optional) + ": " + showType(n.Type) + ")"
	case *ast.ConditionalType:
		return "cond(" + list([]ast.TypeNode{n.CheckType, n.ExtendsType, n.TrueType, n.FalseType}) + ")"
	case *ast.InferType:
		return "infer " + n.Name
	case *ast.TypeOperator:
		return n.Operator + "(" + showType(n.Type) + ")"
	case *ast.IndexedAccessType:
		return "index(" + showType(n.ObjectType) + ", " + showType(n.IndexType) + ")"
	case *ast.TypeQuery:
		return "typeof " + strings.Join(append([]string{n.Name}, n.Path...), ".")
	case *ast.LiteralType:
		return n.Kind + ":" + n.Value
	case *ast.TemplateLiteralType:
		s := "template(" + n.Head
		for _, sp := range n.Spans {
			s += "${" + showType(sp.Type) + "}" + sp.Literal
		}
		return s + ")"
	case *ast.TypePredicate:
		s := "pred("
		if n.Asserts {
			s += "asserts "
		}
		if n.This {
			s += "this"
		} else {
			s += n.ParameterName
		}
		if n.Type != nil {
			s += " is " + showType(n.Type)
		}
		return s + ")"
	}
	return fmt.Sprintf("?%T", n)
}

// declType parses `let v: <typ>;` and returns the annotation.
func declType(t *testing.T, typ string) *ast.TypeAnnotation {
	t.Helper()
	prog, err := parser.Parse("let v: " + typ + ";")
	if err != nil {
		t.Fatalf("%s: %v", typ, err)
	}
	return prog.Body[0].(*ast.VarDeclaration).TypeAnnot
}

func TestTypeNodeShapes(t *testing.T) {
	for _, c := range []struct{ typ, want string }{
		{"number", "kw:number"},
		{"ns.Box<A>[]", "array(ref:ns.Box<ref:A>)"},
		{"T[][]", "array(array(ref:T))"},
		{"T[][\"k\"]", "index(array(ref:T), string:k)"},
		{"A | B & C | null", "union(ref:A, and(ref:B, ref:C), kw:null)"},
		{"(A | B)[]", "array(paren(union(ref:A, ref:B)))"},
		{"(x?: number, ...r: string[]) => void", "fn(x?: kw:number, ...r: array(kw:string)): kw:void"},
		// A lone name is a parameter's name, as in TypeScript: `number: any`.
		{"(number) => string", "fn(number: kw:any): kw:string"},
		{"<T extends object>(x: T) => T", "fn<T extends kw:object>(x: ref:T): ref:T"},
		{"new (x: number) => Foo", "new(x: kw:number): ref:Foo"},
		{"{ a?: string; 'b-c': number; m<T>(x: T): T; n?(): void }", "{a?: kw:string; b-c: kw:number; method m<T>(x: ref:T): ref:T; method n?(): kw:void}"},
		{"{ [k: string]: number }", "{[k: kw:string]: kw:number}"},
		{"{ (n: number): string }", "{call(n: kw:number): kw:string}"},
		{"{ new (): X }", "{construct(): ref:X}"},
		{"{ readonly [K in keyof T]?: T[K] }", "mapped(readonly K in keyof(ref:T)?: index(ref:T, ref:K))"},
		{"T extends Promise<infer R> ? R : never", "cond(ref:T, ref:Promise<infer R>, ref:R, kw:never)"},
		{"[name: string, age: number]", "tuple(name: kw:string, age: kw:number)"},
		{"readonly [number, string]", "readonly(tuple(kw:number, kw:string))"},
		{"`id-${number}px`", "template(id-${kw:number}px)"},
		{"typeof a.b", "typeof a.b"},
		{"\"n\" | -1 | true", "union(string:n, number:-1, boolean:true)"},
	} {
		ta := declType(t, c.typ)
		if got := showType(ta.TypeNode()); got != c.want {
			t.Errorf("%s:\n got  %s\n want %s", c.typ, got, c.want)
		}
	}
}

func TestTypePredicateNodes(t *testing.T) {
	for src, want := range map[string]string{
		"function f(x: unknown): x is string { return true }":        "pred(x is kw:string)",
		"function f(x: unknown): asserts x { }":                      "pred(asserts x)",
		"function f(x: unknown): asserts x is number { }":            "pred(asserts x is kw:number)",
		"class C { f(): asserts this is D { } }":                     "pred(asserts this is ref:D)",
		"function f(object: unknown): object is Foo { return true }": "pred(object is ref:Foo)",
	} {
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		var ret *ast.TypeAnnotation
		switch d := prog.Body[0].(type) {
		case *ast.FunctionDeclaration:
			ret = d.ReturnType
		case *ast.ClassDeclaration:
			ret = d.Methods[0].ReturnType
		}
		if got := showType(ret.TypeNode()); got != want {
			t.Errorf("%s: got %s, want %s", src, got, want)
		}
	}
}

// The annotation of every sub-type points at its own node, and a node's
// range covers exactly its source text.
func TestTypeNodeRangesAndBackPointers(t *testing.T) {
	src := "let v: Map<string, (x: number) => void>[] | null;"
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	ta := prog.Body[0].(*ast.VarDeclaration).TypeAnnot
	u := ta.TypeNode().(*ast.UnionType)
	if r := u.Range; src[r.Start:r.End] != "Map<string, (x: number) => void>[] | null" || r.Pos != (ast.Pos{Line: 1, Col: 8}) {
		t.Errorf("union range %+v = %q", r, src[r.Start:r.End])
	}
	arr := u.Types[0].(*ast.ArrayType)
	ref := arr.ElementType.(*ast.TypeReference)
	fn := ref.TypeArgs[1].(*ast.FunctionType)
	if r := fn.Range; src[r.Start:r.End] != "(x: number) => void" {
		t.Errorf("function type range %q", src[r.Start:r.End])
	}
	if !ta.Nullable || ta.ElemType == nil || ta.ElemType.Name != "Map" {
		t.Fatalf("annotation %+v", ta)
	}
	if ta.ElemType.TypeNode() != ref || ta.ElemType.ElemType.TypeNode() != fn {
		t.Errorf("sub-annotations do not point at their nodes")
	}
}

// Shapes the annotation model can't represent are rejected by the
// conversion, with the message and position the parser used to give.
func TestTypeConversionErrors(t *testing.T) {
	for typ, want := range map[string]string{
		"{ [k: boolean]: number }":           "1:14: only a string or number index signature",
		"{ (n: number): string; a: number }": "1:8: a call signature combined with other object-type members",
		"Promise<A, B>":                      "1:17: expected '>' to close Promise<T>",
		"Map<string>":                        "expected ',' in Map<K,V>",
		"Map<A, B, C>":                       "1:16: expected '>' to close Map<K,V>",
		"[a?: number]":                       "1:9: an optional tuple element is not yet supported",
		"<T>string":                          "expected a function type after a type parameter list",
	} {
		_, err := parser.Parse("let v: " + typ + ";")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", typ, err, want)
		}
	}
}
