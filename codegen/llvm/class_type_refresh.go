package llvm

// A class type is a value: a signature, a field or a closure type that
// names a class before the class's layout is final (registerClasses' Pass
// 3 decides its vtable field) holds a snapshot with the wrong field
// indices. refreshClassTypes replaces every such snapshot in the
// registries with the final type once the layouts are known, before any
// body is emitted, so no later use reads a stale layout (ADR-01326).

// freshType is t with each class type it names, at any depth, replaced by
// the class's final type.
func (e *Emitter) freshType(t Type, depth int) Type {
	if depth > 8 {
		return t
	}
	if t.IsClass {
		return e.canonicalizeClassTy(t)
	}
	d := depth + 1
	ptr := func(p *Type) *Type {
		if p == nil {
			return nil
		}
		n := e.freshType(*p, d)
		return &n
	}
	slice := func(ts []Type) []Type {
		if ts == nil {
			return nil
		}
		out := make([]Type, len(ts))
		for i, x := range ts {
			out[i] = e.freshType(x, d)
		}
		return out
	}
	fields := func(fs []Field) []Field {
		if fs == nil {
			return nil
		}
		out := make([]Field, len(fs))
		for i, f := range fs {
			f.Ty = e.freshType(f.Ty, d)
			out[i] = f
		}
		return out
	}
	t.ElemType = ptr(t.ElemType)
	t.FuncRetType = ptr(t.FuncRetType)
	t.MapKey = ptr(t.MapKey)
	t.MapVal = ptr(t.MapVal)
	t.PromiseType = ptr(t.PromiseType)
	t.DynPropTy = ptr(t.DynPropTy)
	t.GeneratorElemType = ptr(t.GeneratorElemType)
	t.IterSrc = ptr(t.IterSrc)
	t.StreamChunk = ptr(t.StreamChunk)
	t.StreamOut = ptr(t.StreamOut)
	t.FuncParams = slice(t.FuncParams)
	t.UnionMembers = slice(t.UnionMembers)
	t.IntersectionMembers = slice(t.IntersectionMembers)
	t.Fields = fields(t.Fields)
	t.DictFields = fields(t.DictFields)
	return t
}

func (e *Emitter) freshSig(s FuncSig) FuncSig {
	params := make([]Type, len(s.ParamTypes))
	for i, p := range s.ParamTypes {
		params[i] = e.freshType(p, 0)
	}
	s.ParamTypes = params
	s.RetType = e.freshType(s.RetType, 0)
	return s
}

// refreshClassTypes is the sweep: function and method signatures,
// interfaces and module globals. A class's own type is left as registered
// (its field types resolve through canonicalizeClassTy where read), so
// every class type the sweep writes is the one registered, whatever order
// the maps are walked in.
func (e *Emitter) refreshClassTypes() {
	for k, s := range e.funcs {
		e.funcs[k] = e.freshSig(s)
	}
	for name, info := range e.classes {
		for k, s := range info.MethodSigs {
			info.MethodSigs[k] = e.freshSig(s)
		}
		for k, s := range info.StaticMethodSigs {
			info.StaticMethodSigs[k] = e.freshSig(s)
		}
		for k, s := range info.OverrideSigs {
			info.OverrideSigs[k] = e.freshSig(s)
		}
		e.classes[name] = info
	}
	for k, t := range e.interfaces {
		if t.IsClass {
			continue
		}
		e.interfaces[k] = e.freshType(t, 0)
	}
	for k, sym := range e.moduleGlobals {
		sym.Ty = e.freshType(sym.Ty, 0)
		e.moduleGlobals[k] = sym
	}
}
