package ast

import (
	"fmt"

	"KlainMainLang/diag"
)

// TypeAnnotationOf converts a parsed type to the TypeAnnotation code
// generation reads, until the checker resolves types from the nodes
// themselves. Every simplification the annotation model makes happens here:
// a type predicate becomes boolean (void for an assertion), `readonly` and
// `new` are dropped, a generic function type's parameters erase to any, a
// template literal type is string, a qualified name keeps its last segment,
// `T[]` on a name is spelled into the name, null and undefined fold into
// Nullable/Undefined, and the builtin generics fill ElemType/KeyType. So do
// the shapes the annotation model cannot represent, reported as errors.
//
// Each returned annotation's TypeNode() is the node it stands for; source is
// stamped into every Source field.
func TypeAnnotationOf(n TypeNode, source string) (*TypeAnnotation, error) {
	return typeConverter{source: source}.conv(n)
}

// TypeAnnotationOfIn is TypeAnnotationOf inside the body of class thisClass,
// where the `this` type lowers to that class.
func TypeAnnotationOfIn(n TypeNode, source, thisClass string) (*TypeAnnotation, error) {
	return typeConverter{source: source, thisClass: thisClass}.conv(n)
}

// NodeAnnotation wraps a type node without converting it: a declaration
// file's annotation, which only the checker reads (TDD-00230 P3.1).
func NodeAnnotation(n TypeNode, source string) *TypeAnnotation {
	return &TypeAnnotation{Source: source, node: n}
}

type typeConverter struct{ source, thisClass string }

func (c typeConverter) conv(n TypeNode) (*TypeAnnotation, error) {
	ta, err := c.convNode(n)
	if err != nil {
		return nil, err
	}
	ta.node = n
	return ta, nil
}

// singleArgGenerics take exactly one type argument, read into ElemType.
var singleArgGenerics = map[string]bool{
	"Promise": true, "Array": true, "Set": true, "EventEmitter": true,
	"ReadableStream": true, "ReadableStreamDefaultReader": true, "ReadableStreamDefaultController": true,
	"WritableStream": true, "WritableStreamDefaultWriter": true, "WritableStreamDefaultController": true,
}

func (c typeConverter) convNode(n TypeNode) (*TypeAnnotation, error) {
	switch n := n.(type) {
	case *KeywordType:
		if n.Keyword == "this" {
			if c.thisClass == "" {
				return nil, errAt(n.Range, diag.ThisTypeUnsupported)
			}
			return &TypeAnnotation{Name: c.thisClass, Source: c.source, IsThis: true}, nil
		}
		return &TypeAnnotation{Name: n.Keyword, Source: c.source}, nil
	case *LiteralType:
		switch n.Kind {
		case "string":
			return &TypeAnnotation{Source: c.source, IsStringLiteral: true, LiteralValue: n.Value}, nil
		case "number":
			return &TypeAnnotation{Source: c.source, IsNumberLiteral: true, LiteralValue: n.Value}, nil
		}
		return &TypeAnnotation{Name: n.Value, Source: c.source}, nil
	case *TypeReference:
		return c.convReference(n)
	case *ArrayType:
		if nameSpelledArray(n) {
			elem, err := c.conv(n.ElementType)
			if err != nil {
				return nil, err
			}
			return &TypeAnnotation{Name: elem.Name + "[]", Qualifier: elem.Qualifier, Source: c.source}, nil
		}
		elem, err := c.conv(n.ElementType)
		if err != nil {
			return nil, err
		}
		return &TypeAnnotation{Source: c.source, ElemType: elem}, nil
	case *TupleType:
		if len(n.Elements) == 0 {
			return &TypeAnnotation{Source: c.source, EmptyTuple: true}, nil
		}
		elems, err := c.convList(n.Elements)
		if err != nil {
			return nil, err
		}
		return &TypeAnnotation{Source: c.source, TupleElems: elems}, nil
	case *NamedTupleMember:
		// The label documents the element; an optional or rest element
		// has no storage in the fixed-shape tuple model.
		if n.Optional || n.Rest {
			return nil, tupleElementError(n.Range, n.Rest)
		}
		return c.conv(n.Type)
	case *OptionalType:
		return nil, tupleElementError(n.Range, false)
	case *RestType:
		return nil, tupleElementError(n.Range, true)
	case *UnionType:
		return c.convUnion(n)
	case *IntersectionType:
		members, err := c.convList(n.Types)
		if err != nil {
			return nil, err
		}
		// A distinct copy of the first member heads the list, so no member
		// is its own IntersectionMembers entry (a cycle resolveType would
		// recurse into forever).
		head := *members[0]
		head.IntersectionMembers = members
		return &head, nil
	case *ParenthesizedType:
		return c.conv(n.Type)
	case *FunctionType:
		ta, err := c.convFunction(n.TypeParameters, n.Parameters, n.Type)
		if err != nil || n.This == nil {
			return ta, err
		}
		// `(this: T, …) => R`: the receiver is the leading parameter.
		tt, err := c.conv(n.This)
		if err != nil {
			return nil, err
		}
		ta.FuncParams = append([]TypeAnnotation{*tt}, ta.FuncParams...)
		if len(ta.FuncParamOptional) > 0 {
			ta.FuncParamOptional = append([]bool{false}, ta.FuncParamOptional...)
		}
		ta.FuncThis = true
		return ta, nil
	case *ConstructorType:
		return c.convFunction(n.TypeParameters, n.Parameters, n.Type)
	case *TypeLiteral:
		return c.convTypeLiteral(n)
	case *MappedType:
		src, err := c.conv(n.Constraint)
		if err != nil {
			return nil, err
		}
		value, err := c.conv(n.Type)
		if err != nil {
			return nil, err
		}
		return &TypeAnnotation{
			Source: c.source, IsMapped: true, MappedKeyVar: n.KeyName,
			MappedSource: src, MappedValue: value,
			MappedOptional: n.Optional, MappedReadonly: n.Readonly,
		}, nil
	case *ConditionalType:
		parts, err := c.convList([]TypeNode{n.CheckType, n.ExtendsType, n.TrueType, n.FalseType})
		if err != nil {
			return nil, err
		}
		return &TypeAnnotation{
			Source: c.source, IsConditional: true,
			CheckType: parts[0], ExtendsType: parts[1], TrueType: parts[2], FalseType: parts[3],
		}, nil
	case *InferType:
		return &TypeAnnotation{Source: c.source, IsInfer: true, InferName: n.Name}, nil
	case *TypeOperator:
		operand, err := c.conv(n.Type)
		if err != nil {
			return nil, err
		}
		if n.Operator == "keyof" {
			return &TypeAnnotation{Source: c.source, IsKeyof: true, KeyofOperand: operand}, nil
		}
		return operand, nil // readonly: immutability is not enforced
	case *IndexedAccessType:
		obj, err := c.conv(n.ObjectType)
		if err != nil {
			return nil, err
		}
		key, err := c.conv(n.IndexType)
		if err != nil {
			return nil, err
		}
		return &TypeAnnotation{Source: c.source, IsIndexedAccess: true, IndexObject: obj, IndexKey: key}, nil
	case *ImportType:
		return nil, fmt.Errorf("an import type (`import(%q)`) is not supported", n.Argument)
	case *TypeQuery:
		return &TypeAnnotation{Source: c.source, IsTypeof: true, TypeofName: n.Name, TypeofPath: n.Path}, nil
	case *TemplateLiteralType:
		for _, s := range n.Spans {
			if _, err := c.conv(s.Type); err != nil {
				return nil, err
			}
		}
		return &TypeAnnotation{Name: "string", Source: c.source}, nil
	case *TypePredicate:
		if n.Type != nil {
			if _, err := c.conv(n.Type); err != nil {
				return nil, err
			}
		}
		if n.Asserts {
			return &TypeAnnotation{Name: "void", Source: c.source}, nil
		}
		return &TypeAnnotation{Name: "boolean", Source: c.source}, nil
	}
	return nil, fmt.Errorf("%d:%d: unsupported type syntax %T", n.GetPos().Line, n.GetPos().Col, n)
}

func tupleElementError(r Loc, rest bool) error {
	if rest {
		return errAt(r, diag.RestTupleElement)
	}
	return errAt(r, diag.OptionalTupleElement)
}

// errAt reports m over a node's source range.
func errAt(r Loc, m *diag.Message, args ...any) *diag.Diagnostic {
	return diag.New(m, diag.Span{Pos: diag.Pos{Line: r.Pos.Line, Col: r.Pos.Col}, Start: r.Start, End: r.End}, args...)
}

// errAtPos reports m at a position whose byte offset the parser fills in.
func errAtPos(p Pos, m *diag.Message, args ...any) *diag.Diagnostic {
	return diag.New(m, diag.Span{Pos: diag.Pos{Line: p.Line, Col: p.Col}, Start: -1, End: -1}, args...)
}

// nameSpelledArray reports whether an array type is `T[]` written directly
// on a name (a reference without type arguments, a keyword, `true`/`false`,
// or such an array itself), which the annotation spells into its Name.
func nameSpelledArray(n *ArrayType) bool {
	switch e := n.ElementType.(type) {
	case *TypeReference:
		return len(e.TypeArgs) == 0
	case *KeywordType:
		return true
	case *LiteralType:
		return e.Kind == "boolean"
	case *ArrayType:
		return nameSpelledArray(e)
	}
	return false
}

func (c typeConverter) convList(ns []TypeNode) ([]*TypeAnnotation, error) {
	out := make([]*TypeAnnotation, 0, len(ns))
	for _, n := range ns {
		ta, err := c.conv(n)
		if err != nil {
			return nil, err
		}
		out = append(out, ta)
	}
	return out, nil
}

func (c typeConverter) convReference(n *TypeReference) (*TypeAnnotation, error) {
	if n.Name == "TemplateStringsArray" && len(n.TypeArgs) == 0 && len(n.Qualifier) == 0 {
		// A tag's strings: a (readonly) string array.
		return &TypeAnnotation{Name: "string[]", Source: c.source}, nil
	}
	if len(n.TypeArgs) == 0 {
		return &TypeAnnotation{Name: n.Name, Qualifier: n.Qualifier, Source: c.source}, nil
	}
	switch {
	case singleArgGenerics[n.Name]:
		inner, err := c.conv(n.TypeArgs[0])
		if err != nil {
			return nil, err
		}
		if len(n.TypeArgs) > 1 {
			p := n.ArgSeparators[0]
			return nil, errAtPos(p, diag.ExpectedCloseAngle, n.Name+"<T>")
		}
		return &TypeAnnotation{Name: n.Name, ElemType: inner, Source: c.source}, nil
	case n.Name == "Map" || n.Name == "TransformStream":
		keyTy, err := c.conv(n.TypeArgs[0])
		if err != nil {
			return nil, err
		}
		if len(n.TypeArgs) == 1 {
			return nil, errAtPos(Pos{}, diag.ExpectedMapComma)
		}
		valTy, err := c.conv(n.TypeArgs[1])
		if err != nil {
			return nil, err
		}
		if len(n.TypeArgs) > 2 {
			p := n.ArgSeparators[1]
			return nil, errAtPos(p, diag.ExpectedCloseAngle, "Map<K,V>")
		}
		return &TypeAnnotation{Name: "Map", KeyType: keyTy, ElemType: valTy, Source: c.source}, nil
	}
	// Any other generic, user-defined or not: TypeArgs carries every
	// argument, ElemType the first.
	args, err := c.convList(n.TypeArgs)
	if err != nil {
		return nil, err
	}
	return &TypeAnnotation{Name: n.Name, Qualifier: n.Qualifier, ElemType: args[0], TypeArgs: args, Source: c.source}, nil
}

// convUnion keeps every member but null and undefined, which set
// Nullable/Undefined on the result instead. A single remaining member is the
// result itself; with several, a distinct copy of the first heads the
// UnionMembers list (see IntersectionType above for why a copy).
func (c typeConverter) convUnion(n *UnionType) (*TypeAnnotation, error) {
	first, err := c.conv(n.Types[0])
	if err != nil {
		return nil, err
	}
	nullable, undef := first.Nullable, first.Undefined
	var members []*TypeAnnotation
	for i, t := range n.Types {
		ta := first
		if i > 0 {
			if ta, err = c.conv(t); err != nil {
				return nil, err
			}
		}
		// `void` beside other members is undefined as a value (`Socket |
		// undefined | void`, a callback's result).
		if ta.Name == "null" || ta.Name == "undefined" || ta.Name == "void" && len(n.Types) > 1 {
			nullable = true
			undef = undef || ta.Name != "null"
			continue
		}
		members = append(members, ta)
	}
	switch len(members) {
	case 0:
		first.Nullable = true
		first.Undefined = undef
		return first, nil
	case 1:
		members[0].Nullable = nullable
		members[0].Undefined = undef
		return members[0], nil
	}
	head := *members[0]
	head.Nullable = nullable
	head.Undefined = undef
	head.UnionMembers = members
	return &head, nil
}

// convParams converts signature parameters. An optional parameter's type is
// `T | undefined`, matching the nullable-scalar ABI a body emits for a
// `?`-parameter, so a binding's slot type agrees with the closure it holds.
func (c typeConverter) convParams(ps []*SignatureParameter) (params []TypeAnnotation, optional []bool, rest bool, err error) {
	for _, p := range ps {
		pt := &TypeAnnotation{Name: "any", Source: c.source} // an unannotated parameter
		if p.Type != nil {
			t, err := c.conv(p.Type)
			if err != nil {
				return nil, nil, false, err
			}
			pt = t
		}
		if p.Optional {
			pt.Nullable = true
			pt.Undefined = true
		}
		params = append(params, *pt)
		optional = append(optional, p.Optional)
		rest = rest || p.Rest
	}
	return params, optional, rest, nil
}

// convSignature converts a parameter list and return type to a function-type
// annotation. A nil return type (a signature member without `: R`) is void.
func (c typeConverter) convSignature(ps []*SignatureParameter, ret TypeNode) (*TypeAnnotation, error) {
	params, optional, rest, err := c.convParams(ps)
	if err != nil {
		return nil, err
	}
	retType := &TypeAnnotation{Name: "void", Source: c.source}
	if ret != nil {
		if retType, err = c.conv(ret); err != nil {
			return nil, err
		}
	}
	return &TypeAnnotation{Source: c.source, IsFuncType: true, FuncParams: params, FuncParamOptional: optional, FuncRetType: retType, FuncHasRest: rest}, nil
}

// convGeneric converts a signature with a type-parameter list. The
// parameters erase to any: generic functions here are monomorphized
// declarations, never first-class values, so a signature's own type
// parameters have nothing to bind to.
func (c typeConverter) convGeneric(tps []*TypeParameter, sig func() (*TypeAnnotation, error)) (*TypeAnnotation, error) {
	set := map[string]bool{}
	for _, tp := range tps {
		if tp.Constraint != nil {
			if _, err := c.conv(tp.Constraint); err != nil {
				return nil, err
			}
		}
		set[tp.Name] = true
	}
	ta, err := sig()
	if err != nil {
		return nil, err
	}
	if len(tps) > 0 {
		EraseTypeParams(ta, set)
	}
	return ta, nil
}

func (c typeConverter) convFunction(tps []*TypeParameter, ps []*SignatureParameter, ret TypeNode) (*TypeAnnotation, error) {
	return c.convGeneric(tps, func() (*TypeAnnotation, error) { return c.convSignature(ps, ret) })
}

// IndexSignatureValue checks an index signature's key type and returns its
// value type's annotation.
func IndexSignatureValue(n *IndexSignature, source string) (*TypeAnnotation, error) {
	return typeConverter{source: source}.convIndexSignature(n)
}

func (c typeConverter) convIndexSignature(n *IndexSignature) (*TypeAnnotation, error) {
	key := ""
	switch k := n.KeyType.(type) {
	case *KeywordType:
		key = k.Keyword
	case *TypeReference:
		key = k.Name
	}
	if key != "string" && key != "number" {
		return nil, errAt(n.KeyType.GetLoc(), diag.IndexSignatureKey, key)
	}
	return c.conv(n.Type)
}

// SignatureMember returns the function-type annotation of a call, construct
// or method signature member.
func SignatureMember(m TypeMember, source string) (*TypeAnnotation, error) {
	return typeConverter{source: source}.convSignatureMember(m)
}

func (c typeConverter) convSignatureMember(m TypeMember) (*TypeAnnotation, error) {
	var ta *TypeAnnotation
	var err error
	switch m := m.(type) {
	case *CallSignature:
		ta, err = c.convSignature(m.Parameters, m.Type)
	case *ConstructSignature:
		ta, err = c.convSignature(m.Parameters, m.Type)
	case *MethodSignature:
		ta, err = c.convGeneric(m.TypeParameters, func() (*TypeAnnotation, error) { return c.convSignature(m.Parameters, m.Type) })
	default:
		return nil, fmt.Errorf("%d:%d: not a signature member", m.GetPos().Line, m.GetPos().Col)
	}
	return ta, err
}

// convTypeLiteral converts an object type. A lone call (or construct)
// signature makes it the equivalent function type; an index signature makes
// it a map-backed dynamic object and cannot be combined with named members.
func (c typeConverter) convTypeLiteral(n *TypeLiteral) (*TypeAnnotation, error) {
	var fields []AnnotField
	var indexSig, callSig *TypeAnnotation
	for _, m := range n.Members {
		switch m := m.(type) {
		case *IndexSignature:
			valTy, err := c.convIndexSignature(m)
			if err != nil {
				return nil, err
			}
			if indexSig != nil {
				return nil, errAt(n.Range, diag.ObjectIndexSigTwice)
			}
			indexSig = valTy
		case *CallSignature, *ConstructSignature:
			if callSig != nil {
				return nil, errAt(n.Range, diag.ObjectCallSigTwice)
			}
			sig, err := c.convSignatureMember(m)
			if err != nil {
				return nil, err
			}
			callSig = sig
		case *MethodSignature:
			sig, err := c.convSignatureMember(m)
			if err != nil {
				return nil, err
			}
			fields = append(fields, AnnotField{Name: m.Name, Type: sig})
		case *PropertySignature:
			ft := &TypeAnnotation{Name: "any", Source: c.source} // `name;` is any
			if m.Type != nil {
				t, err := c.conv(m.Type)
				if err != nil {
					return nil, err
				}
				ft = t
			}
			fields = append(fields, AnnotField{Name: m.Name, Type: ft, Optional: m.Optional})
		}
	}
	if callSig != nil {
		if len(fields) > 0 || indexSig != nil {
			return nil, errAt(n.Range, diag.ObjectCallSigMixed)
		}
		return callSig, nil
	}
	return &TypeAnnotation{Source: c.source, Fields: fields, IndexSig: indexSig}, nil
}

// EraseTypeParams rewrites bare references to the given type-parameter names
// into any inside an annotation (see convGeneric).
func EraseTypeParams(ta *TypeAnnotation, params map[string]bool) {
	to := make(map[string]string, len(params))
	for p := range params {
		to[p] = "any"
	}
	EraseTypeParamsTo(ta, to)
}

// EraseTypeParamsTo is EraseTypeParams with each parameter's replacement
// name (its constraint's, or "any").
func EraseTypeParamsTo(ta *TypeAnnotation, params map[string]string) {
	if ta == nil {
		return
	}
	if r, ok := params[ta.Name]; ok {
		ta.Name = r
	} else if len(ta.Name) > 2 && ta.Name[len(ta.Name)-2:] == "[]" {
		if r, ok := params[ta.Name[:len(ta.Name)-2]]; ok {
			ta.Name = r + "[]"
		}
	}
	EraseTypeParamsTo(ta.ElemType, params)
	EraseTypeParamsTo(ta.KeyType, params)
	EraseTypeParamsTo(ta.FuncRetType, params)
	for i := range ta.FuncParams {
		EraseTypeParamsTo(&ta.FuncParams[i], params)
	}
	for _, m := range ta.TypeArgs {
		EraseTypeParamsTo(m, params)
	}
	for _, m := range ta.UnionMembers {
		EraseTypeParamsTo(m, params)
	}
	for _, m := range ta.IntersectionMembers {
		EraseTypeParamsTo(m, params)
	}
	for _, m := range ta.TupleElems {
		EraseTypeParamsTo(m, params)
	}
	for i := range ta.Fields {
		EraseTypeParamsTo(ta.Fields[i].Type, params)
	}
}

// InterfaceLegacy derives the member shapes code generation reads (Fields,
// Methods, IndexSig, CallSig) from an interface's members. jsdocType turns a
// member's `/** @type {…} */` width into its annotation. err is the first
// member shape those cannot represent; the rest are still derived.
func InterfaceLegacy(members []TypeMember, source string, jsdocType func(string) *TypeAnnotation) (fields []AnnotField, methods []InterfaceMethodSig, indexSig, callSig *TypeAnnotation, err error) {
	c := typeConverter{source: source}
	note := func(e error) {
		if err == nil {
			err = e
		}
	}
	for _, m := range members {
		switch m := m.(type) {
		case *IndexSignature:
			if indexSig != nil {
				note(errAt(m.Range, diag.InterfaceIndexSigTwice))
				continue
			}
			ta, e := c.convIndexSignature(m)
			if e != nil {
				note(e)
				continue
			}
			indexSig = ta
		case *CallSignature, *ConstructSignature:
			if callSig != nil {
				// Further call/construct signatures (an overloaded callable
				// interface) are erased: the first is the one call sites check
				// against (ADR-00446/ADR-00479).
				continue
			}
			ta, e := c.convSignatureMember(m)
			if e != nil {
				note(e)
				continue
			}
			callSig = ta
		case *MethodSignature:
			ms, e := c.legacyMethod(m)
			if e != nil {
				note(e)
				continue
			}
			methods = append(methods, ms)
		case *PropertySignature:
			var ft *TypeAnnotation
			switch {
			case m.JSDocType != "" && jsdocType != nil:
				ft = jsdocType(m.JSDocType)
			case m.Type == nil:
				ft = &TypeAnnotation{Name: "any", Source: source}
			default:
				t, e := c.conv(m.Type)
				if e != nil {
					note(e)
					continue
				}
				ft = t
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
	if callSig != nil && (len(fields) > 0 || len(methods) > 0 || indexSig != nil) {
		note(errAt(members[0].GetLoc(), diag.InterfaceCallSigMixed))
	}
	return fields, methods, indexSig, callSig, err
}

// legacyMethod converts a method signature to the parameter-list shape an
// interface method has for code generation; a generic method's type
// parameters erase to any (ADR-00469).
func (c typeConverter) legacyMethod(m *MethodSignature) (InterfaceMethodSig, error) {
	ms := InterfaceMethodSig{Name: m.Name}
	tps := map[string]bool{}
	for _, tp := range m.TypeParameters {
		tps[tp.Name] = true
	}
	for i, sp := range m.Parameters {
		var pt *TypeAnnotation
		if sp.Type != nil {
			t, err := c.conv(sp.Type)
			if err != nil {
				return ms, err
			}
			pt = t
		}
		name := sp.Name
		if name == "" {
			name = fmt.Sprintf("__p%d", i)
		}
		ms.Params = append(ms.Params, Param{Name: name, Type: pt, Optional: sp.Optional, Rest: sp.Rest})
	}
	if m.Type != nil {
		rt, err := c.conv(m.Type)
		if err != nil {
			return ms, err
		}
		ms.ReturnType = rt
	}
	if len(tps) > 0 {
		for i := range ms.Params {
			EraseTypeParams(ms.Params[i].Type, tps)
		}
		EraseTypeParams(ms.ReturnType, tps)
	}
	return ms, nil
}
