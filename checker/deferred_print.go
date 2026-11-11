package checker

import (
	"strconv"
	"strings"

	"KlainMainLang/ast"
)

// deferredStr prints a deferred type as tsc does a generic one: its
// declaration's type node, each name bound when it was deferred printed as
// the type it is bound to (`[...A, L]`, `T[K]`, `Extract<keyof T,
// string>`), an alias instance by its alias.
func (t *Type) deferredStr(seen map[*Type]bool) string {
	if t.deferAlias != "" {
		if len(t.deferAliasArgs) == 0 {
			return t.deferAlias
		}
		args := make([]string, len(t.deferAliasArgs))
		for i, a := range t.deferAliasArgs {
			args[i] = a.str(seen)
		}
		return t.deferAlias + "<" + strings.Join(args, ", ") + ">"
	}
	if t.deferNode == nil {
		return "unknown"
	}
	p := deferredPrinter{t: t, seen: seen}
	return p.node(t.deferNode)
}

type deferredPrinter struct {
	t    *Type
	seen map[*Type]bool
}

// name is how the deferred type's node reads a bare name: the type the
// environment or substitution binds it to, else the name itself.
func (p deferredPrinter) name(n string) string {
	if v, ok := p.t.deferEnv[n]; ok && v != nil {
		return v.str(p.seen)
	}
	for k, v := range p.t.deferSubst {
		if k.Flags&TypeParam != 0 && k.Symbol != nil && symbolName(k.Symbol) == n && v != nil && v != k {
			return v.str(p.seen)
		}
	}
	return n
}

func (p deferredPrinter) list(ns []ast.TypeNode, sep string) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = p.node(n)
	}
	return strings.Join(parts, sep)
}

func (p deferredPrinter) node(n ast.TypeNode) string {
	switch x := n.(type) {
	case nil:
		return "unknown"
	case *ast.KeywordType:
		return x.Keyword
	case *ast.TypeReference:
		name := sourceName(x.Name)
		if len(x.Qualifier) > 0 {
			name = strings.Join(append(append([]string(nil), x.Qualifier...), x.Name), ".")
		} else if len(x.TypeArgs) == 0 {
			if v := p.name(x.Name); v != x.Name {
				return v
			}
			return name
		}
		if len(x.TypeArgs) == 0 {
			return name
		}
		return name + "<" + p.list(x.TypeArgs, ", ") + ">"
	case *ast.ArrayType:
		el := p.node(x.ElementType)
		switch x.ElementType.(type) {
		case *ast.UnionType, *ast.IntersectionType, *ast.FunctionType, *ast.ConditionalType:
			el = "(" + el + ")"
		}
		return el + "[]"
	case *ast.TupleType:
		return "[" + p.list(x.Elements, ", ") + "]"
	case *ast.NamedTupleMember:
		switch {
		case x.Rest:
			return "..." + x.Name + ": " + p.node(x.Type)
		case x.Optional:
			return x.Name + "?: " + p.node(x.Type)
		}
		return x.Name + ": " + p.node(x.Type)
	case *ast.OptionalType:
		return p.node(x.Type) + "?"
	case *ast.RestType:
		return "..." + p.node(x.Type)
	case *ast.UnionType:
		return p.list(x.Types, " | ")
	case *ast.IntersectionType:
		return p.list(x.Types, " & ")
	case *ast.ParenthesizedType:
		return p.node(x.Type)
	case *ast.TypeOperator:
		return x.Operator + " " + p.node(x.Type)
	case *ast.IndexedAccessType:
		return p.node(x.ObjectType) + "[" + p.node(x.IndexType) + "]"
	case *ast.ConditionalType:
		return p.node(x.CheckType) + " extends " + p.node(x.ExtendsType) + " ? " + p.node(x.TrueType) + " : " + p.node(x.FalseType)
	case *ast.InferType:
		return "infer " + x.Name
	case *ast.MappedType:
		var b strings.Builder
		b.WriteString("{ ")
		switch x.ReadonlyMod {
		case 1:
			b.WriteString("readonly ")
		case -1:
			b.WriteString("-readonly ")
		}
		b.WriteString("[" + x.KeyName + " in " + p.node(x.Constraint) + "]")
		switch x.OptionalMod {
		case 1:
			b.WriteString("?")
		case -1:
			b.WriteString("-?")
		}
		b.WriteString(": " + p.node(x.Type) + "; }")
		return b.String()
	case *ast.LiteralType:
		if x.Kind == "string" {
			return strconv.Quote(x.Value)
		}
		return x.Value
	case *ast.TemplateLiteralType:
		var b strings.Builder
		b.WriteString("`" + x.Head)
		for _, s := range x.Spans {
			b.WriteString("${" + p.node(s.Type) + "}" + s.Literal)
		}
		b.WriteString("`")
		return b.String()
	case *ast.TypeQuery:
		return "typeof " + strings.Join(append([]string{x.Name}, x.Path...), ".")
	}
	return "unknown"
}
