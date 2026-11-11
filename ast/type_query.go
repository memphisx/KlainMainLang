package ast

import "reflect"

// Syntactic questions about a type node, for code that asks what a type
// annotation spells rather than what the checker makes of it.

// Unparen is n without its enclosing parentheses.
func Unparen(n TypeNode) TypeNode {
	for {
		p, ok := n.(*ParenthesizedType)
		if !ok {
			return n
		}
		n = p.Type
	}
}

// NonNullishMembers is n's union members other than null and undefined
// (n itself when it is no union), and which of the two it spells.
func NonNullishMembers(n TypeNode) (members []TypeNode, hasNull, hasUndefined bool) {
	n = Unparen(n)
	u, ok := n.(*UnionType)
	if !ok {
		if IsKeywordNode(n, "null") {
			return nil, true, false
		}
		if IsKeywordNode(n, "undefined") {
			return nil, false, true
		}
		return []TypeNode{n}, false, false
	}
	for _, m := range u.Types {
		ms, nl, ud := NonNullishMembers(m)
		members = append(members, ms...)
		hasNull, hasUndefined = hasNull || nl, hasUndefined || ud
	}
	return members, hasNull, hasUndefined
}

// NonNullish is the one non-nullish member of n (`T | undefined` is T),
// else n itself.
func NonNullish(n TypeNode) TypeNode {
	if ms, _, _ := NonNullishMembers(n); len(ms) == 1 {
		return Unparen(ms[0])
	}
	return Unparen(n)
}

// IsKeywordNode reports whether n is the keyword type kw (`any`, `void`,
// `this`, …), spelled as a keyword or as a plain reference to it.
func IsKeywordNode(n TypeNode, kw string) bool {
	switch x := Unparen(n).(type) {
	case *KeywordType:
		return x.Keyword == kw
	case *TypeReference:
		return x.Name == kw && len(x.Qualifier) == 0 && len(x.TypeArgs) == 0
	}
	return false
}

// RefName is the name a plain, unqualified type reference spells, with
// no type arguments; "" for any other node.
func RefName(n TypeNode) string {
	if r, ok := Unparen(n).(*TypeReference); ok && len(r.Qualifier) == 0 && len(r.TypeArgs) == 0 {
		return r.Name
	}
	return ""
}

// ArrayElemNode is the element node of `T[]` or `Array<T>`, else nil.
func ArrayElemNode(n TypeNode) TypeNode {
	switch x := Unparen(n).(type) {
	case *ArrayType:
		return x.ElementType
	case *TypeReference:
		if x.Name == "Array" && len(x.Qualifier) == 0 && len(x.TypeArgs) == 1 {
			return x.TypeArgs[0]
		}
	}
	return nil
}

// SignatureReturn is the return type node of a function or constructor
// type, and which of the two it is; ok is false for any other node.
func SignatureReturn(n TypeNode) (ret TypeNode, ctor, ok bool) {
	switch x := Unparen(n).(type) {
	case *FunctionType:
		return x.Type, false, true
	case *ConstructorType:
		return x.Type, true, true
	}
	return nil, false, false
}

// MarkJSDoc marks n as the root of a type written in a JSDoc tag.
func MarkJSDoc(n TypeNode) {
	if n == nil {
		return
	}
	v := reflect.ValueOf(n)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return
	}
	if r := v.Elem().FieldByName("Range"); r.IsValid() && r.CanSet() {
		r.FieldByName("JSDoc").SetBool(true)
	}
}

// IsJSDoc reports a type written in a JSDoc tag (MarkJSDoc).
func IsJSDoc(n TypeNode) bool { return n != nil && n.GetLoc().JSDoc }

// HasModuleSyntax reports whether body has ES module syntax: an import or
// export, import.meta, or a top-level await.
func HasModuleSyntax(body []Statement) bool {
	found := false
	for _, st := range body {
		switch st.(type) {
		case *ImportDeclaration, *ExportDeclaration, *ExportFromDeclaration:
			return true
		}
		Inspect(st, func(n Node) bool {
			switch n.(type) {
			case *FunctionDeclaration, *FunctionExpression, *ArrowFunction, *ClassDeclaration:
				return false
			case *AwaitExpression, *ImportMetaUrl:
				found = true
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

// IsESM reports whether the file of top-level statement st is one Node runs
// as an ES module; a program compiled from text with no entry file is one
// when it has module syntax.
func (p *Program) IsESM(st Statement) bool {
	if f, ok := p.StmtFiles[st]; ok {
		return p.ESMFiles[f]
	}
	return len(p.StmtFiles) == 0 && HasModuleSyntax(p.Body)
}
