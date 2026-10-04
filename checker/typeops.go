package checker

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
)

// typeops.go — the type operators: `keyof T`, `T[K]`, mapped types
// (`{ [P in K]: X }`) and conditional types (`A extends B ? X : Y`, with
// `infer`). Each is evaluated when its operands are known. An operand that
// names a type parameter makes the type deferred: it keeps its node and the
// bindings in scope, and instantiate evaluates it again once the
// parameters are bound (`Promise.all<T>`'s result, read at the call).

// deferredType is the type n denotes once the type parameters it reads are
// bound. Interned per node and bindings.
func (c *Checker) deferredType(n ast.TypeNode, scope *binder.Scope) *Type {
	env := map[string]*Type{}
	for _, e := range c.env {
		for k, v := range e {
			env[k] = v
		}
	}
	subst := map[*Type]*Type{}
	for _, s := range c.substs {
		for k, v := range s {
			subst[k] = v
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "d%p|%p|", n, scope)
	names := make([]string, 0, len(env))
	for k := range env {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		b.WriteString(k + "=" + strconv.Itoa(env[k].ID) + ";")
	}
	keys := make([]*Type, 0, len(subst))
	for k := range subst {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].ID < keys[j].ID })
	for _, k := range keys {
		b.WriteString(strconv.Itoa(k.ID) + ">" + strconv.Itoa(subst[k].ID) + ";")
	}
	return c.in.intern(b.String(), func() *Type {
		return &Type{Flags: Deferred, deferNode: n, deferScope: scope, deferEnv: env, deferSubst: subst}
	})
}

// instantiateDeferred evaluates a deferred type with m's types bound.
func (c *Checker) instantiateDeferred(t *Type, m map[*Type]*Type) *Type {
	env := map[string]*Type{}
	for k, v := range t.deferEnv {
		env[k] = c.instantiate(v, m)
	}
	subst := map[*Type]*Type{}
	for k, v := range m {
		subst[k] = v
	}
	for k, v := range t.deferSubst {
		subst[k] = c.instantiate(v, m)
	}
	savedEnv, savedSubsts := c.env, c.substs
	c.env, c.substs = []map[string]*Type{env}, []map[*Type]*Type{subst}
	r := c.typeFromNode(t.deferNode, t.deferScope)
	c.env, c.substs = savedEnv, savedSubsts
	return r
}

// generic reports whether t reads a type parameter other than an `infer`
// placeholder being matched, so a type operator over it is deferred.
func (c *Checker) generic(t *Type) bool {
	if !hasTypeParam(t) {
		return false
	}
	if len(c.inferPlaceholders) == 0 {
		return true
	}
	m := map[*Type]*Type{}
	for tp := range c.inferPlaceholders {
		m[tp] = c.unknownT
	}
	return hasTypeParam(c.instantiate(t, m))
}

// lookupSubst is the type a deferred type's evaluation binds tp to.
func (c *Checker) lookupSubst(tp *Type) *Type {
	for i := len(c.substs) - 1; i >= 0; i-- {
		if r, ok := c.substs[i][tp]; ok {
			return r
		}
	}
	return nil
}

// keyofType is `keyof t`: its property names as string literals, and
// `string | number` or `number` for its index signatures.
func (c *Checker) keyofType(t *Type) *Type {
	switch {
	case t.Flags&Any != 0:
		return c.in.union(c.strT, c.numT, c.symT)
	case t.Flags&Union != 0:
		// The keys every member has.
		var common []*Type
		for i, m := range t.Types {
			k := c.keyofType(m)
			if c.Unanswered(k) {
				return k
			}
			if i == 0 {
				common = members(k)
				continue
			}
			var keep []*Type
			for _, x := range common {
				for _, y := range members(k) {
					if x == y || c.assignableTo(x, y) == yes {
						keep = append(keep, x)
						break
					}
				}
			}
			common = keep
		}
		return c.in.union(common...)
	}
	o := t
	if t.Flags&Object == 0 || t.Kind == Array || t.Kind == Tuple {
		if at := c.apparentType(t); at != nil {
			o = at
		}
	}
	if o.Flags&Object == 0 {
		return c.neverT
	}
	var ks []*Type
	if t.Flags&Object != 0 && (t.Kind == Array || t.Kind == Tuple) {
		ks = append(ks, c.numT)
	}
	for _, p := range o.Props {
		ks = append(ks, c.in.literal(StringLiteral, p.Name))
	}
	if o.StringIndex != nil {
		ks = append(ks, c.strT, c.numT)
	} else if o.NumberIndex != nil {
		ks = append(ks, c.numT)
	}
	return c.in.union(ks...)
}

// indexedAccess is `o[k]`.
func (c *Checker) indexedAccess(o, k *Type) *Type {
	if o.Flags&Any != 0 {
		return c.anyT
	}
	var rs []*Type
	for _, om := range members(o) {
		for _, km := range members(k) {
			r := c.indexOne(om, km)
			if c.Unanswered(r) {
				return r
			}
			rs = append(rs, r)
		}
	}
	return c.in.union(rs...)
}

func (c *Checker) indexOne(o, k *Type) *Type {
	if o.Flags&Object == 0 {
		at := c.apparentType(o)
		if at == nil {
			return c.unanswered
		}
		o = at
	}
	switch {
	case k.Flags&StringLiteral != 0:
		if (o.Kind == Array || o.Kind == Tuple) && k.Value == "length" {
			if o.Kind == Tuple {
				return c.in.literal(NumberLiteral, strconv.Itoa(len(o.Elems)))
			}
			return c.numT
		}
		if o.Kind == Tuple {
			if i, err := strconv.Atoi(k.Value); err == nil && i >= 0 && i < len(o.Elems) {
				return o.Elems[i]
			}
		}
		src := o
		if o.Kind == Array || o.Kind == Tuple {
			if at := c.apparentType(o); at != nil {
				src = at
			}
		}
		if p := src.Prop(k.Value); p != nil {
			return p.Type
		}
		if src.StringIndex != nil {
			return src.StringIndex
		}
	case k.Flags&NumberLiteral != 0:
		switch o.Kind {
		case Tuple:
			if i, err := strconv.Atoi(k.Value); err == nil && i >= 0 && i < len(o.Elems) {
				return o.Elems[i]
			}
		case Array:
			return o.Elem
		}
		if p := o.Prop(k.Value); p != nil {
			return p.Type
		}
		if o.NumberIndex != nil {
			return o.NumberIndex
		}
		if o.StringIndex != nil {
			return o.StringIndex
		}
	case k.Flags&NumberLike != 0:
		switch o.Kind {
		case Tuple:
			return c.in.union(o.Elems...)
		case Array:
			return o.Elem
		}
		if o.NumberIndex != nil {
			return o.NumberIndex
		}
		if o.StringIndex != nil {
			return o.StringIndex
		}
	case k.Flags&String != 0:
		if o.StringIndex != nil {
			return o.StringIndex
		}
	}
	return c.unanswered
}

// withoutOptional drops the undefined `-?` removes.
func (c *Checker) withoutOptional(t *Type) *Type {
	if t.Flags&Union == 0 {
		return t
	}
	var ms []*Type
	for _, m := range t.Types {
		if !m.IsMissing() && m.Flags&Undefined == 0 {
			ms = append(ms, m)
		}
	}
	return c.in.union(ms...)
}

// mappedType is `{ [P in K]: X }`. Over `keyof T` it is homomorphic: an
// array or tuple T maps its elements, an object T keeps each property's
// modifiers unless the mapping adds or removes them.
func (c *Checker) mappedType(n *ast.MappedType, scope *binder.Scope) *Type {
	value := func(key *Type) *Type {
		pop := c.pushEnv([]string{n.KeyName}, []*Type{key})
		defer pop()
		return c.typeFromNode(n.Type, scope)
	}
	optional := func(t *Type, was bool) (*Type, bool) {
		switch {
		case n.OptionalMod > 0 || n.OptionalMod == 0 && was:
			return c.in.union(t, c.missingT), true
		case n.OptionalMod < 0:
			return c.withoutOptional(t), false
		}
		return t, false
	}
	if op, ok := n.Constraint.(*ast.TypeOperator); ok && op.Operator == "keyof" {
		src := c.typeFromNode(op.Type, scope)
		switch {
		case c.Unanswered(src):
			return src
		case c.generic(src):
			return c.deferredType(n, scope)
		case src.Flags&Object != 0 && src.Kind == Array:
			e := value(c.numT)
			if c.Unanswered(e) {
				return e
			}
			return c.in.array(e)
		case src.Flags&Object != 0 && src.Kind == Tuple:
			es := make([]*Type, len(src.Elems))
			for i := range src.Elems {
				e := value(c.in.literal(NumberLiteral, strconv.Itoa(i)))
				if c.Unanswered(e) {
					return e
				}
				es[i] = e
			}
			return c.in.tuple(es)
		case src.Flags&Object != 0 && (src.Kind == Anonymous || src.Kind == Interface || src.Kind == Instance):
			var props []*Property
			for _, p := range src.Props {
				vt := value(c.in.literal(StringLiteral, p.Name))
				if c.Unanswered(vt) {
					return vt
				}
				vt, opt := optional(vt, p.Optional)
				ro := p.Readonly
				if n.ReadonlyMod != 0 {
					ro = n.ReadonlyMod > 0
				}
				props = append(props, &Property{Name: p.Name, Type: vt, Optional: opt, Readonly: ro})
			}
			var str, num *Type
			if src.StringIndex != nil {
				if str = value(c.strT); c.Unanswered(str) {
					return str
				}
			}
			if src.NumberIndex != nil {
				if num = value(c.numT); c.Unanswered(num) {
					return num
				}
			}
			return c.in.indexedOn(c.in.objectOf(props, src.partial, false), str, num)
		}
	}
	keys := c.typeFromNode(n.Constraint, scope)
	switch {
	case c.Unanswered(keys):
		return keys
	case c.generic(keys):
		return c.deferredType(n, scope)
	}
	var props []*Property
	var str, num *Type
	for _, k := range members(keys) {
		vt := value(k)
		if c.Unanswered(vt) {
			return vt
		}
		switch {
		case k.Flags&(StringLiteral|NumberLiteral) != 0:
			vt, opt := optional(vt, false)
			props = append(props, &Property{Name: k.Value, Type: vt, Optional: opt, Readonly: n.ReadonlyMod > 0})
		case k.Flags&String != 0:
			str = vt
		case k.Flags&NumberLike != 0:
			num = vt
		case k.Flags&(ESSymbol|Never) != 0:
		default:
			return c.unanswered
		}
	}
	return c.in.indexedOn(c.in.object(props), str, num)
}

// conditionalType is `A extends B ? X : Y`. A naked type parameter as A
// distributes over a union.
func (c *Checker) conditionalType(n *ast.ConditionalType, scope *binder.Scope) *Type {
	check := c.typeFromNode(n.CheckType, scope)
	if c.Unanswered(check) {
		return check
	}
	if ref, ok := n.CheckType.(*ast.TypeReference); ok && len(ref.TypeArgs) == 0 && len(ref.Qualifier) == 0 && !c.generic(check) {
		if bind := c.typeParamBinder(ref.Name, scope); bind != nil {
			if check.Flags&Never != 0 {
				return c.neverT
			}
			if check.Flags&Union != 0 {
				var rs []*Type
				for _, m := range check.Types {
					pop := bind(m)
					r := c.conditionalOnce(n, scope, m)
					pop()
					if c.Unanswered(r) {
						return r
					}
					rs = append(rs, r)
				}
				return c.in.union(rs...)
			}
		}
	}
	return c.conditionalOnce(n, scope, check)
}

// typeParamBinder rebinds the type parameter name names in scope (an
// alias's, through the environment, or a function's, through the
// substitution) for one member of a distribution; nil when name is not a
// type parameter.
func (c *Checker) typeParamBinder(name string, scope *binder.Scope) func(*Type) func() {
	if sym := resolveTypeName(name, scope); sym != nil && sym.Flags&binder.TypeParameter != 0 {
		tp := c.typeParamType(sym)
		return func(m *Type) func() {
			c.substs = append(c.substs, map[*Type]*Type{tp: m})
			return func() { c.substs = c.substs[:len(c.substs)-1] }
		}
	}
	if c.lookupEnv(name) != nil {
		return func(m *Type) func() { return c.pushEnv([]string{name}, []*Type{m}) }
	}
	return nil
}

func (c *Checker) conditionalOnce(n *ast.ConditionalType, scope *binder.Scope, check *Type) *Type {
	if c.generic(check) {
		return c.deferredType(n, scope)
	}
	var names []string
	var infers []*Type
	collectInfers(n.ExtendsType, func(it *ast.InferType) {
		names = append(names, it.Name)
		infers = append(infers, c.in.intern(fmt.Sprintf("infer%p", it), func() *Type { return &Type{Flags: TypeParam, Value: it.Name} }))
	})
	pop := c.pushEnv(names, infers)
	if c.inferPlaceholders == nil {
		c.inferPlaceholders = map[*Type]bool{}
	}
	var added []*Type
	for _, tp := range infers {
		if !c.inferPlaceholders[tp] {
			c.inferPlaceholders[tp] = true
			added = append(added, tp)
		}
	}
	ext := c.typeFromNode(n.ExtendsType, scope)
	for _, tp := range added {
		delete(c.inferPlaceholders, tp)
	}
	pop()
	if c.Unanswered(ext) {
		return ext
	}
	m := map[*Type]*Type{}
	inferred := len(infers) > 0
	if len(infers) > 0 {
		cands := map[*Type][]*Type{}
		c.unify(ext, check, &Type{TypeParams: infers}, cands)
		for _, tp := range infers {
			if cs := cands[tp]; len(cs) > 0 {
				m[tp] = c.in.union(cs...)
			} else {
				m[tp] = c.unknownT
				inferred = false
			}
		}
	}
	inst := c.instantiate(ext, m)
	if c.generic(inst) {
		return c.deferredType(n, scope)
	}
	branch := n.FalseType
	switch {
	case check.Flags&Any != 0:
		// any takes both branches.
		t := c.withInfers(names, infers, m, func() *Type { return c.typeFromNode(n.TrueType, scope) })
		f := c.typeFromNode(n.FalseType, scope)
		if c.Unanswered(t) || c.Unanswered(f) {
			return c.unanswered
		}
		return c.in.union(t, f)
	default:
		switch c.assignableTo(check, inst) {
		case yes:
			return c.withInfers(names, infers, m, func() *Type { return c.typeFromNode(n.TrueType, scope) })
		case maybe:
			// A relation the checker does not decide (a generic method
			// against another): every `infer` matched, so the shape did.
			if inferred {
				return c.withInfers(names, infers, m, func() *Type { return c.typeFromNode(n.TrueType, scope) })
			}
			if c.lacksRequired(check, inst) || disjointKinds(check, inst) {
				return c.typeFromNode(branch, scope) // `Response extends { then(…) }`
			}
			return c.unanswered
		}
	}
	return c.typeFromNode(branch, scope)
}

// withInfers evaluates f with each `infer` name bound to its inference.
func (c *Checker) withInfers(names []string, infers []*Type, m map[*Type]*Type, f func() *Type) *Type {
	args := make([]*Type, len(infers))
	for i, tp := range infers {
		args[i] = m[tp]
	}
	pop := c.pushEnv(names, args)
	defer pop()
	return f()
}

// collectInfers visits the `infer` declarations of a conditional type's
// extends clause, not those of a conditional type nested in it.
func collectInfers(n ast.TypeNode, f func(*ast.InferType)) {
	switch n := n.(type) {
	case *ast.InferType:
		f(n)
	case *ast.TypeReference:
		for _, a := range n.TypeArgs {
			collectInfers(a, f)
		}
	case *ast.ArrayType:
		collectInfers(n.ElementType, f)
	case *ast.TupleType:
		for _, e := range n.Elements {
			collectInfers(e, f)
		}
	case *ast.NamedTupleMember:
		collectInfers(n.Type, f)
	case *ast.RestType:
		collectInfers(n.Type, f)
	case *ast.OptionalType:
		collectInfers(n.Type, f)
	case *ast.UnionType:
		for _, m := range n.Types {
			collectInfers(m, f)
		}
	case *ast.IntersectionType:
		for _, m := range n.Types {
			collectInfers(m, f)
		}
	case *ast.ParenthesizedType:
		collectInfers(n.Type, f)
	case *ast.TypeOperator:
		collectInfers(n.Type, f)
	case *ast.IndexedAccessType:
		collectInfers(n.ObjectType, f)
		collectInfers(n.IndexType, f)
	case *ast.FunctionType:
		for _, p := range n.Parameters {
			if p.Type != nil {
				collectInfers(p.Type, f)
			}
		}
		if n.Type != nil {
			collectInfers(n.Type, f)
		}
	case *ast.TypeLiteral:
		for _, m := range n.Members {
			switch m := m.(type) {
			case *ast.PropertySignature:
				if m.Type != nil {
					collectInfers(m.Type, f)
				}
			case *ast.MethodSignature:
				for _, p := range m.Parameters {
					if p.Type != nil {
						collectInfers(p.Type, f)
					}
				}
				if m.Type != nil {
					collectInfers(m.Type, f)
				}
			}
		}
	}
}

// lacksRequired reports whether s has no member at all for a property t
// requires: s cannot extend t, even where the relation is otherwise left
// open (a library interface's members).
func (c *Checker) lacksRequired(s, t *Type) bool {
	var req []*Property
	for _, tm := range members(t) {
		if tm.Flags&Object == 0 {
			continue
		}
		for _, p := range tm.Props {
			if !p.Optional {
				req = append(req, p)
			}
		}
	}
	if len(req) == 0 {
		return false
	}
	for _, sm := range members(s) {
		src := sm
		if sm.Flags&Object == 0 || sm.Kind == Array || sm.Kind == Tuple {
			if at := c.apparentType(sm); at != nil {
				src = at
			} else if sm.Flags&Object == 0 {
				continue
			}
		}
		if src.StringIndex != nil {
			return false // a partial type's unmodelled members are computed (symbol) names
		}
		for _, p := range req {
			if src.Prop(p.Name) == nil && c.objectMember(p.Name) == nil {
				return true
			}
		}
	}
	return false
}

// disjointKinds reports whether every member of s is an object and every
// member of t a primitive, null or undefined: s cannot extend t.
func disjointKinds(s, t *Type) bool {
	for _, m := range members(s) {
		if m.Flags&Object == 0 {
			return false
		}
	}
	for _, m := range members(t) {
		if m.Flags&(Object|NonPrimitive|Any|Unknown|TypeParam|Deferred) != 0 {
			return false
		}
	}
	return true
}
