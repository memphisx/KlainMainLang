package ast

import (
	"fmt"

	"KlainMainLang/diag"
)

// TypeAnnotationOf wraps a parsed type as a declaration's annotation, after
// rejecting the shapes code generation does not support, with the message
// and position the parser gives them.
func TypeAnnotationOf(n TypeNode, source string) (TypeNode, error) {
	return annotate(n, source, false)
}

// TypeAnnotationOfIn is TypeAnnotationOf inside the body of a class, where
// the `this` type names the class.
func TypeAnnotationOfIn(n TypeNode, source, thisClass string) (TypeNode, error) {
	return annotate(n, source, thisClass != "")
}

// NodeAnnotation wraps a type node without checking its shape: a
// declaration file's annotation, or one code generation builds.
func NodeAnnotation(n TypeNode, source string) TypeNode {
	if source == "jsdoc" {
		MarkJSDoc(n)
	}
	return n
}

func annotate(n TypeNode, source string, inClass bool) (TypeNode, error) {
	if err := checkTypeShape(n, inClass); err != nil {
		return nil, err
	}
	return NodeAnnotation(n, source), nil
}

// singleArgGenerics take exactly one type argument.
var singleArgGenerics = map[string]bool{
	"Promise": true, "Array": true, "Set": true, "EventEmitter": true,
	"ReadableStream": true, "ReadableStreamDefaultReader": true, "ReadableStreamDefaultController": true,
	"WritableStream": true, "WritableStreamDefaultWriter": true, "WritableStreamDefaultController": true,
}

// checkTypeShape reports the first shape in n code generation does not
// support.
func checkTypeShape(n TypeNode, inClass bool) error {
	var err error
	Inspect(n, func(x Node) bool {
		if err != nil {
			return false
		}
		switch t := x.(type) {
		case *KeywordType:
			if t.Keyword == "this" && !inClass {
				err = errAt(t.Range, diag.ThisTypeOutsideClass)
			}
		case *TypeReference:
			err = checkReferenceArity(t)
		case *ImportType:
			err = fmt.Errorf("an import type (`import(%q)`) is not supported", t.Argument)
		case *IndexSignature:
			err = checkIndexKey(t)
		}
		return err == nil
	})
	return err
}

// checkReferenceArity reports a builtin generic given too many or too few
// type arguments.
func checkReferenceArity(n *TypeReference) error {
	if len(n.TypeArgs) == 0 || len(n.Qualifier) > 0 {
		return nil
	}
	switch {
	case singleArgGenerics[n.Name]:
		if len(n.TypeArgs) > 1 {
			return errAtPos(n.ArgSeparators[0], diag.ExpectedCloseAngle, n.Name+"<T>")
		}
	case n.Name == "Map" || n.Name == "TransformStream":
		if len(n.TypeArgs) == 1 {
			return errAtPos(Pos{}, diag.ExpectedMapComma)
		}
		if len(n.TypeArgs) > 2 {
			return errAtPos(n.ArgSeparators[1], diag.ExpectedCloseAngle, "Map<K,V>")
		}
	}
	return nil
}

// checkIndexKey reports an index signature keyed by neither string nor
// number.
func checkIndexKey(n *IndexSignature) error {
	key := ""
	switch k := n.KeyType.(type) {
	case *KeywordType:
		key = k.Keyword
	case *TypeReference:
		key = k.Name
	}
	if key != "string" && key != "number" {
		return errAt(n.KeyType.GetLoc(), diag.IndexSignatureKey, key)
	}
	return nil
}

// stringKeyed reports an index signature over string keys.
func stringKeyed(n *IndexSignature) bool {
	switch k := n.KeyType.(type) {
	case *KeywordType:
		return k.Keyword == "string"
	case *TypeReference:
		return k.Name == "string"
	}
	return false
}

// AnyAnnotation is `any`.
func AnyAnnotation(source string) TypeNode {
	return NodeAnnotation(&KeywordType{Keyword: "any"}, source)
}

// AnyArrayAnnotation is `any[]`.
func AnyArrayAnnotation(source string) TypeNode {
	return NodeAnnotation(&ArrayType{ElementType: &KeywordType{Keyword: "any"}}, source)
}

// errAt reports m over a node's source range.
func errAt(r Loc, m *diag.Message, args ...any) *diag.Diagnostic {
	return diag.New(m, diag.Span{Pos: diag.Pos{Line: r.Pos.Line, Col: r.Pos.Col}, Start: r.Start, End: r.End}, args...)
}

// errAtPos reports m at a position whose byte offset the parser fills in.
func errAtPos(p Pos, m *diag.Message, args ...any) *diag.Diagnostic {
	return diag.New(m, diag.Span{Pos: diag.Pos{Line: p.Line, Col: p.Col}, Start: -1, End: -1}, args...)
}

// IndexSignatureValue checks an index signature's key type and returns its
// value type's annotation.
func IndexSignatureValue(n *IndexSignature, source string) (TypeNode, error) {
	if err := checkIndexKey(n); err != nil {
		return nil, err
	}
	return annotate(n.Type, source, false)
}

// InterfaceLegacy derives the member shapes code generation reads (Fields,
// Methods, IndexSig, CallSig) from an interface's members. jsdocType turns a
// member's `/** @type {…} */` width into its annotation. err is the first
// member shape those cannot represent; the rest are still derived.
func InterfaceLegacy(members []TypeMember, source string, jsdocType func(string) TypeNode) (fields []AnnotField, methods []InterfaceMethodSig, indexSig, callSig TypeNode, err error) {
	note := func(e error) {
		if err == nil {
			err = e
		}
	}
	conv := func(n TypeNode) TypeNode {
		if n == nil {
			return AnyAnnotation(source)
		}
		ta, e := annotate(n, source, true)
		if e != nil {
			note(e)
			return nil
		}
		return ta
	}
	for _, m := range members {
		switch m := m.(type) {
		case *IndexSignature:
			if indexSig != nil && !stringKeyed(m) {
				// A number index beside a string one: a key reads as its
				// string, so the string index holds every entry.
				continue
			}
			ta, e := IndexSignatureValue(m, source)
			if e != nil {
				note(e)
				continue
			}
			indexSig = ta
		case *CallSignature:
			if callSig == nil {
				// Further call signatures (an overloaded callable interface)
				// are erased: the first is the one call sites check against
				// (ADR-00446/ADR-00479).
				callSig = conv(&FunctionType{TypeParameters: m.TypeParameters, Parameters: m.Parameters, Type: m.Type, Range: m.Range})
			}
		case *ConstructSignature:
			if callSig == nil {
				callSig = conv(&ConstructorType{TypeParameters: m.TypeParameters, Parameters: m.Parameters, Type: m.Type, Range: m.Range})
			}
		case *MethodSignature:
			ms := InterfaceMethodSig{Name: m.Name, Optional: m.Optional}
			for i, sp := range m.Parameters {
				name := sp.Name
				if name == "" {
					name = fmt.Sprintf("__p%d", i)
				}
				ms.Params = append(ms.Params, Param{Name: name, Type: conv(sp.Type), Optional: sp.Optional, Rest: sp.Rest})
			}
			if m.Type != nil {
				ms.ReturnType = conv(m.Type)
			}
			// The signature's own function type, which the checker reads.
			ms.Type = conv(&FunctionType{TypeParameters: m.TypeParameters, Parameters: m.Parameters, This: m.This, Type: m.Type, Range: m.Range})
			methods = append(methods, ms)
		case *PropertySignature:
			var ft TypeNode
			if m.JSDocType != "" && jsdocType != nil {
				ft = jsdocType(m.JSDocType)
			} else {
				ft = conv(m.Type)
			}
			if ft == nil {
				continue
			}
			if m.Accessor != "" {
				// A get/set pair is one field of the getter's type.
				merged := false
				for i := range fields {
					if fields[i].Name == m.Name {
						if m.Accessor == "get" {
							fields[i].Type = ft
						}
						merged = true
					}
				}
				if merged {
					continue
				}
			}
			fields = append(fields, AnnotField{Name: m.Name, Type: ft, Optional: m.Optional})
		}
	}
	return fields, methods, indexSig, callSig, err
}
