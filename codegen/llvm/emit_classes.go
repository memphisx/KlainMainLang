// emit_classes.go — TDD-00009 Stages 1-3: class registration (fields, ctor
// and method signatures, inheritance/override analysis), constructor/method
// emission, `this`/`super`, `new ClassName(args)`, method-call dispatch
// (static or vtable-indirect), Stage 1a's class-based for...of iterator
// protocol, and `instanceof` (including through inheritance).
package llvm

import (
	"KlainMainLang/checker"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
)

// MethodSlot is the shared dispatch record for one method name across an
// entire inheritance tree (TDD-00009 Stage 3), identified by the class that
// first introduces it. It's stored by pointer in every class's
// MethodDispatchSlot map that has the method available (inherited or own),
// so a deep override anywhere in the subtree retroactively marks every
// class between the introduction point and that override as needing
// indirection for this one method name — this is why the whole-program
// override analysis (registerClasses' Pass 1) must fully complete before
// any call-site codegen decision reads Virtual/Index.
//
// Two unrelated classes that happen to declare a same-named method with no
// common ancestor declaring it never share a MethodSlot (each gets its own,
// fresh at its own introduction) — only a genuine override chain along a
// single-inheritance lineage does, so a method name is never marked Virtual
// spuriously just because some unrelated class elsewhere in the program
// happens to reuse the same name.
type MethodSlot struct {
	Name    string
	Virtual bool
	// Index is this method's vtable slot, valid only once Virtual — assigned
	// in registerClasses' Pass 2, after the whole program's override
	// analysis (Pass 1) has finished for every class.
	Index int
}

// ClassInfo is the registered shape + behavior of one user-defined class,
// populated by registerClasses before any function or method body is
// emitted (mirroring how registerInterfaces/registerFunctions front-load
// their own registries). Ty is also stored into e.interfaces[name] so
// annotationType's existing named-type lookup resolves a class name in any
// type annotation with no changes of its own.
type ClassInfo struct {
	Ty Type // ClassType(name, inherited, own, hasVTable) — the instance's storage type
	// TypeSubs are a generic class instantiation's type arguments, by
	// parameter name: a method body compiled again (a foreign receiver's
	// adapter) binds them as the instantiation's own did.
	TypeSubs map[string]Type
	// GenericOf is the generic class an instantiation is of (`Box` for
	// `Box<number>`'s), "" for any other class.
	GenericOf   string
	Constructor *ast.FunctionDeclaration
	// ImplicitCtor marks a Constructor this compiler synthesized for a class
	// that declares none (JavaScript's `constructor(...args)`, length 0).
	ImplicitCtor bool
	CtorSig      FuncSig // RetType always TypeVoid; zero value if Constructor == nil

	// MethodSigs/Methods hold the class's full *effective* method table —
	// own declarations plus everything inherited (overridden entries use
	// the overriding declaration/signature) — so every existing lookup
	// site written before Stage 3 (emit_call.go, emit_exprs_types.go,
	// emit_stmts.go's for...of) keeps working with no changes: a method
	// inherited-but-not-overridden is found here exactly like an own one.
	Methods    map[string]*ast.FunctionDeclaration
	MethodSigs map[string]FuncSig
	// OverrideSigs are the own signatures of this class's overrides whose
	// parameters differ from the inherited method's (TS method parameters
	// are bivariant): the method keeps the inherited signature, and its
	// symbol is an adapter to the body compiled under its own.
	OverrideSigs map[string]FuncSig
	// GenMethodInfo holds one GeneratorInfo per generator method (`*m()`,
	// TDD-00063 Stage 2b) this class declares — keyed by method name. A call
	// to such a method constructs a generator instance (emitClassCall's own
	// interception) rather than running the method body directly. Empty for a
	// class with no generator methods.
	GenMethodInfo map[string]*GeneratorInfo
	// MethodImplementor names which class's @Implementor_methodName
	// function actually runs for a given method name on this class (the
	// nearest declaration at-or-above this class in the chain).
	MethodImplementor map[string]string
	// MethodDispatchSlot is nil for a method never overridden anywhere in
	// this class's tree (always a direct call to MethodImplementor); non-nil
	// (and possibly Virtual) otherwise. Shared by pointer — see MethodSlot.
	MethodDispatchSlot map[string]*MethodSlot
	// MethodOrder is method names in first-introduction order (inherited
	// names first, in the base's own order, then this class's newly
	// introduced names) — purely so vtable slot assignment and emission are
	// deterministic across compiler runs, not something callers need for
	// correctness.
	MethodOrder []string

	// TagID is this class's compile-time-assigned runtime identity (TDD-00009
	// Stage 2): a small monotonic integer, one per class, stored into every
	// instance's hidden ClassTagField at construction time and compared
	// against by instanceof.
	TagID int64

	// BaseClass is "" for a root class. AncestorChain is root-first, not
	// including this class itself. Descendants is every class that
	// transitively extends this one (any order), not including itself —
	// both TDD-00009 Stage 3, used by instanceof's inheritance-aware cases.
	BaseClass     string
	AncestorChain []string
	Descendants   []string
	// RootClass is the ultimate no-base ancestor (itself, for a root class)
	// — every class sharing a RootClass shares one uniform HasVTable/
	// VTableSize decision (see registerClasses' Pass 2).
	RootClass string

	// InheritedFields/OwnFields are kept separate (rather than only the
	// flattened FlatFields) so Pass 3 can rebuild this class's final Ty via
	// ClassType(name, InheritedFields, OwnFields, HasVTable) once HasVTable
	// is known. FlatFields (both, concatenated) is what a *descendant*
	// inherits as its own InheritedFields.
	InheritedFields []Field
	OwnFields       []Field
	FlatFields      []Field

	// HasVTable is uniform across every class sharing a RootClass (see
	// registerClasses' Pass 2) — true when at least one method anywhere in
	// the tree is overridden by a descendant, or the tree is a builtin
	// module's. VTableSize is this class's slot count: its base's, then
	// the virtual slots it introduces.
	HasVTable  bool
	VTableSize int

	// --- TDD-00009 Stage 4 ---

	// IsAbstract marks an `abstract class` — cannot be directly
	// instantiated (emitNewExpression), and is exempt from the
	// "every method must have a real implementation" completeness check
	// every other (concrete) class is held to.
	IsAbstract bool
	// IsErrorSubclass marks `class X extends Error` (TDD-00155 Stage 6): the
	// instance layout is prefix-compatible with errorObjType (the class tag
	// slot IS the error kind slot), TagID lives in the >=1000 error-subclass
	// range, and throw/catch/instanceof treat instances as real errors.
	IsErrorSubclass bool
	// OwnKeyOrder is an accessor-bearing object literal's property names in
	// source order (its synthetic class, emit_objects_accessors.go): its
	// accessors are own, enumerable properties, not a prototype's. Nil for
	// a class.
	OwnKeyOrder []string
	// HostBase is the builtin host type a class extends (`extends Map<K, V>`,
	// TDD-00247), nil for any other class: the instance's host body follows
	// its tag word (hostBodyFields), and a member the class does not declare
	// is the host's, on that body.
	HostBase *Type
	// HostAnnot is HostBase as an annotation (`Map<string, number>`): an
	// instance viewed as its host (`x as Map<string, number>`).
	HostAnnot ast.TypeNode
	// ErrorBaseKind is the builtin error an error subclass extends
	// directly ("Error", "TypeError", "RangeError", …): its default `.name`,
	// and the builtin kind `instanceof` also matches.
	ErrorBaseKind string
	// Implements is the class's `implements A, B, ...` clause — purely a
	// compile-time self-check (registerClasses' Pass 1), never affecting
	// codegen/dispatch: does this class already structurally satisfy each
	// named interface's fields and method signatures.
	Implements []string

	// FieldOrigin names which class actually declared an (instance) field,
	// since InheritedFields/OwnFields/FlatFields don't otherwise track
	// per-field provenance.
	FieldOrigin map[string]string
	// DeclareFields are `declare` fields (own and inherited): no own
	// property until assigned.
	DeclareFields map[string]bool

	// StaticFieldTypes/StaticFieldOwner mirror FlatFields/FieldOrigin's
	// shape for `static` fields, but storage-wise a static field is a
	// plain LLVM global (@ClassName_static_name), not a struct slot — an
	// inherited, non-redeclared static field is the *same* global as its
	// base's (StaticFieldOwner names which class's global backs a given
	// name), not a per-subclass copy, matching real JS/TS static-field
	// sharing semantics. StaticFieldTypes holds the effective (inherited or
	// own) type per name; OwnStaticFieldTypes holds only this class's own
	// newly-declared static fields (what actually needs a global emitted
	// for it — see emitClassStaticFieldGlobals).
	StaticFieldTypes    map[string]Type
	OwnStaticFieldTypes map[string]Type
	OwnStaticFieldOrder []string // the own static fields in declaration order
	StaticFieldOwner    map[string]string

	// StaticMethodSigs/StaticMethodImplementor mirror MethodSigs/
	// MethodImplementor's inherit-then-override shape, but a static method
	// call is a bare class-name receiver — never polymorphic, so there is
	// no vtable/MethodDispatchSlot concept for statics at all, always a
	// direct call to StaticMethodImplementor.
	StaticMethodSigs        map[string]FuncSig
	StaticMethodImplementor map[string]string
}

// canonicalizeClassTy swaps a class-typed Type for the live, fully-resolved
// registry entry (e.classes[ClassName].Ty) whenever one is available.
//
// Why this exists: Field.Ty is stored by value, not by reference, so a
// self- or mutually-referential class field (`class Node { nextNode: Node |
// null }`) necessarily captures a *snapshot* of the referenced class's Type
// at the moment it was resolved — which, for a genuine self-reference, is
// always the placeholder registerClasses seeds before that class's own
// fields exist yet (see registerClasses's Pass 0). That snapshot's Fields is
// permanently stale (empty, for direct self-reference) no matter what
// e.classes is later updated to, because Go copies the value in rather than
// aliasing it. Left alone, this makes a *second* field access chained off
// the first (`node.nextNode.value`) fail with "no field 'value'" even
// though node.nextNode is a perfectly valid Node pointer at runtime.
//
// The fix is narrow rather than architectural: every place that returns a
// field's type as the type of the expression *for a caller to potentially
// drill into further* (emitMember, emitOptionalMember, typeOfExpr's
// field-read case) re-resolves a class-typed field's type through this
// helper before handing it back, so the caller always sees the final,
// fully-resolved field list — without changing Field/Type's storage shape
// or touching any of the many other call sites that read a field's type
// for a non-chaining purpose (coercion, alignment, GEP storage width),
// where the snapshot's IR-level shape (always irPtr for any class) was
// already correct and sufficient.
// boxSelfReference boxes a value typed by a named shape's self-reference
// placeholder (`next: N | null` captured inside N before N's fields
// existed) with the live layout, for the run-time walkers that follow a
// recursive shape to any depth (JSON.stringify, inspect). A static walker
// over the live layout would unfold the recursion without end.
func (e *Emitter) boxSelfReference(v Value) (Value, bool) {
	ty := v.Ty
	if ty.IsClass() || ty.RefName == "" || !ty.IsObject() || len(ty.UserFields()) > 0 || !e.hasLiveShape(ty.RefName) {
		return v, false
	}
	v.Ty = e.canonicalizeClassTy(ty)
	return e.convert(v, TypeAny), true
}

// hasLiveShape reports whether the named shape's registry entry has fields.
func (e *Emitter) hasLiveShape(name string) bool {
	live, ok := e.interfaces[name]
	return ok && live.IsObject() && len(live.UserFields()) > 0
}

func (e *Emitter) canonicalizeClassTy(ty Type) Type {
	// A self-referential array field (`class Node { children: Node[] }`) captures
	// the same stale placeholder snapshot in its ElemType. Re-resolve the element
	// so indexing it (`node.children[0].val`) sees the full field list, mirroring
	// the direct-class-field case below.
	if ty.IsArray() && ty.ElemType != nil && (ty.ElemType.IsClass() || ty.ElemType.RefName != "") {
		canonElem := e.canonicalizeClassTy(*ty.ElemType)
		out := ty
		out.ElemType = &canonElem
		return out
	}
	// A dictionary or Map of a class (`NodeJS.Dict<C>`, `Map<K, C>`) as a
	// field type captures the same snapshot in its value type.
	if ty.MapVal != nil && (ty.MapVal.IsClass() || ty.MapVal.RefName != "") {
		canonVal := e.canonicalizeClassTy(*ty.MapVal)
		out := ty
		out.MapVal = &canonVal
		return out
	}
	// A named structural type (interface / object type alias) whose field
	// snapshot is a stale self-reference placeholder (empty Fields captured
	// before the interface's own fields existed) re-resolves through the live
	// e.interfaces entry — the structural sibling of the IsClass case below.
	// Keyed by RefName, which never implies IsClass. Only swap in a richer live
	// entry; if the registry entry is itself the empty placeholder (a genuinely
	// field-less interface) the snapshot is already correct.
	if !ty.IsClass() && ty.RefName != "" && len(ty.UserFields()) == 0 {
		if live, ok := e.interfaces[ty.RefName]; ok && live.IsObject() && len(live.UserFields()) > 0 {
			canon := live
			canon.setNullable(ty.Nullable())
			canon.setUndefined(ty.IsUndefined())
			canon.setKind(kindNull, ty.IsNull())
			canon.setNullAndUndef(ty.NullAndUndef())
			return canon
		}
	}
	if ty.IsClass() {
		// A class still registering has published its info before its
		// type: the snapshot stands until then.
		if info, ok := e.classes[ty.ClassName]; ok && info.Ty.IsClass() {
			// Nullable is a property of the field's own annotation (`Node |
			// null`), not of the class itself — info.Ty is the bare class
			// registry entry and is never Nullable, so swapping it in
			// wholesale would silently discard a caller's `| null`
			// annotation (found via instanceof's null-check reduction for a
			// nullable class-typed field, TDD-00009 Stage 2).
			canon := info.Ty
			canon.setNullable(ty.Nullable())
			// Carry the nullish *kind* too: a `C | undefined` field (an optional
			// `inner?: C`, TDD-00187) must stay undefined, not collapse to `null`
			// (ADR-00834) — else it renders/compares as `null`, not `undefined`.
			canon.setUndefined(ty.IsUndefined())
			canon.setKind(kindNull, ty.IsNull())
			canon.setNullAndUndef(ty.NullAndUndef())
			// An instance of a class extending Promise carries what it settles
			// to (promiseSubclassOf).
			canon.PromiseType = ty.PromiseType
			return canon
		}
	}
	return ty
}

// buildParamSig resolves a parameter list into a FuncSig's parameter half
// (types/names/defaults/rest), the same per-parameter rules
// registerFunctions uses for top-level functions: an explicit annotation is
// resolved via annotationType; a rest parameter with no annotation defaults to
// number[]; a destructured one the type its pattern gives it (fd's, when
// the list is a declaration's); anything else unannotated defaults to
// (inferred) TypeI64.
func (e *Emitter) buildParamSig(params []ast.Param, fd *ast.FunctionDeclaration) FuncSig {
	var sig FuncSig
	for i, p := range params {
		var pty Type
		if p.Type != nil {
			pty = e.annotationType(p.Type)
		} else if p.Rest {
			pty = untypedRestType()
		} else if dt, ok := e.paramDefaultType(p); ok {
			pty = dt // `p = '!'` is a string, as for a function
		} else if pt, ok := e.patternParamTypeOf(fd, i); ok {
			pty = pt
		} else if e.compatJS() {
			// A JavaScript parameter takes any value (implicit any), as a
			// function's does (buildFunctionSig).
			pty = TypeAny
		} else {
			pty = TypeI64
			pty.Inferred = true
		}
		sig.ParamTypes = append(sig.ParamTypes, optionalParamType(p, pty))
		sig.ParamNames = append(sig.ParamNames, p.Name)
		sig.Defaults = append(sig.Defaults, callSiteDefault(p)) // a destructured parameter's
		sig.HasDefault = append(sig.HasDefault, p.Default != nil)
		sig.Optional = append(sig.Optional, p.Optional || bodyDefaulted(p))
	}
	if len(params) > 0 && params[len(params)-1].Rest {
		sig.HasRest = true
	}
	return sig
}

// adaptOverrideParams gives an override whose parameter is stored
// differently from the overridden one's (`_write(chunk: Buffer, …)` over
// `_write(chunk: any, …)`, which TypeScript's bivariant method parameters
// allow) the base's parameter, converted to its own declared type at entry,
// so both share one vtable signature.
func (e *Emitter) adaptOverrideParams(m, base *ast.FunctionDeclaration) {
	if base == nil || m.IsStatic || m.AccessorKind != "" || m.Body == nil {
		return
	}
	var prologue []ast.Statement
	for i := range m.Params {
		if i >= len(base.Params) {
			break
		}
		p, bp := &m.Params[i], base.Params[i]
		if p.Rest || bp.Rest || p.Type == nil || bp.Type == nil || p.ArrayPattern != nil || p.ObjectPattern != nil || p.Default != nil {
			continue
		}
		own, theirs := e.annotationType(p.Type), e.annotationType(bp.Type)
		// A callback whose own parameters are stored differently
		// (`(e?: Error | null) => void` over `(e?: any) => void`) is the
		// same pointer but must be converted, as an argument would be.
		closureDiffers := false
		if own.IsFunc() && theirs.IsFunc() {
			closureDiffers, _ = funcAdapterPlan(theirs, own)
		}
		if !closureDiffers && own.IR == theirs.IR && own.IsArray() == theirs.IsArray() && own.IsDynamic() == theirs.IsDynamic() {
			continue
		}
		tmp := fmt.Sprintf("__kml_adapt%d", i)
		prologue = append(prologue, ast.NewVarDeclaration("let", p.Name, p.Type, ast.NewIdentifier(tmp, m.GetPos()), m.GetPos()))
		p.Name, p.Type = tmp, bp.Type
		p.Optional = p.Optional || bp.Optional
	}
	if len(prologue) > 0 {
		m.Body.Body = append(prologue, m.Body.Body...)
	}
}

// sigCompatible reports whether an overriding method's signature is
// call-compatible with the signature it overrides — same parameter count
// and per-parameter IR shape, same return IR shape. An indirect vtable call
// site only ever knows the *introducing* declaration's signature, so every
// override sharing that slot must agree on the actual LLVM call shape, or
// the indirect call itself would be unsound.
// padOverrideParams gives an override that declares fewer parameters than
// the method it overrides (`_read()` over `_read(size: number)`, which
// TypeScript allows) the base's remaining parameters as unused ones, so both
// share one vtable signature. A rest parameter on either side is left alone.
func padOverrideParams(m, base *ast.FunctionDeclaration) {
	if base == nil || m.IsStatic || m.AccessorKind != "" || len(m.Params) >= len(base.Params) {
		return
	}
	for _, p := range m.Params {
		if p.Rest {
			return
		}
	}
	for i := len(m.Params); i < len(base.Params); i++ {
		bp := base.Params[i]
		if bp.Rest {
			return
		}
		m.Params = append(m.Params, ast.Param{
			Name:     fmt.Sprintf("__kml_pad%d", i),
			Type:     bp.Type,
			Optional: bp.Optional || bp.Default != nil,
		})
	}
}

func sigCompatible(base, override FuncSig) bool {
	if len(base.ParamTypes) != len(override.ParamTypes) {
		return false
	}
	for i := range base.ParamTypes {
		if base.ParamTypes[i].IR != override.ParamTypes[i].IR {
			return false
		}
	}
	return base.RetType.IR == override.RetType.IR
}

// accessorMethodName returns the internal dispatch key a getter/setter for
// property `prop` is registered under (TDD-00030) — both the Go-side
// MethodSigs/Methods/etc. map key *and*, directly, the emitted LLVM
// function name's own distinguishing suffix (registerClasses/
// emitClassDecl/emitClassCall all use this same string as a plain method
// name, so it has to be valid on both sides — unlike a Go map key, an LLVM
// symbol can't contain a space). kind is "get" or "set". The `__kml_`
// prefix follows the same "reserved, can't collide with a real
// user-declared name" convention already established by
// ClassTagField/ClassVTableField (types.go).
func accessorMethodName(kind, prop string) string {
	return "__kml_" + kind + "_" + prop
}

// llvmSafeSymbol replaces characters that are illegal in a bare (unquoted) LLVM
// identifier before a name is used to build a function symbol: a private name's
// `#` prefix (TDD-00021), and the `@@` of a well-known-symbol method key
// (`@@asyncIterator`, TDD-00089). Both substitutes use the `__kml_` reserved
// namespace (like accessorMethodName/ClassTagField), so they can't collide with
// a real user-declared name; the un-substituted key stays the dispatch key in
// MethodSigs.
//
// Any *other* rune outside LLVM's bare-identifier set ([A-Za-z0-9_$.]) — a
// non-ASCII identifier char, which JS/TS allow (`const ф = 1`, CJK names) — is
// escaped to `__kml_u<hex>_` (ADR-00899). Without this a Cyrillic/CJK global or
// function name emits an illegal token (`@__kml_global_а...`) that clang rejects
// with "expected '=' in global variable". The escape is deterministic and lands
// in the reserved namespace, so it stays injective against real identifiers.
// ctorSymbolSuffix names a class's constructor function (`@Point__kml_ctor`).
// A method is `@Class_name`, so the suffix sits in the reserved `__kml_`
// namespace: a method literally named `constructor` (a computed
// `["constructor"]() {}`) must not take the constructor's symbol.
const ctorSymbolSuffix = "__kml_ctor"

// ctorMayOverride reports whether class name's constructor has a
// `return <expr>`, which may override the allocated instance: the
// constructor then leaves the instance it returns in @__kml_ctor_ret.
func (e *Emitter) ctorMayOverride(name string) bool {
	info, ok := e.classes[name]
	if !ok {
		return false
	}
	if (e.compatJS() || e.foreignCtorName(name)) && info.BaseClass != "" && info.BaseClass != name && (e.ctorMayOverride(info.BaseClass) || e.valueBases[fnBaseOf(info.BaseClass)] && !builtinValueBases[fnBaseOf(info.BaseClass)]) {
		// Its `this` is what the base's construction returned.
		return true
	}
	if info.Constructor == nil || info.Constructor.Body == nil {
		return false
	}
	found := false
	ast.Inspect(info.Constructor.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ArrowFunction, *ast.ClassExpression, *ast.ClassDeclaration:
			return false
		case *ast.ReturnStatement:
			if x.Value != nil && (e.compatJS() || e.ctorReturnMayOverride(x.Value)) {
				found = true
			}
		}
		return !found
	})
	return found
}

// foreignCtorClass reports whether cd's constructor, or one of its bases',
// may return an object that is not an instance of the class: its instances
// are then held as any.
func (e *Emitter) foreignCtorClass(cd *ast.ClassDeclaration) bool {
	c := e.front()
	if c == nil {
		return false
	}
	if e.foreignCtor == nil {
		e.foreignCtor = map[*ast.ClassDeclaration]bool{}
	}
	if v, ok := e.foreignCtor[cd]; ok {
		return v
	}
	e.foreignCtor[cd] = false // a cycle reads false
	v := c.CtorReturnsForeign(cd)
	if !v && cd.BaseClass != "" && e.prog != nil {
		for _, st := range e.prog.Body {
			if b, ok := st.(*ast.ClassDeclaration); ok && b.Name == cd.BaseClass && e.foreignCtorClass(b) {
				v = true
			}
		}
	}
	e.foreignCtor[cd] = v
	return v
}

// foreignCtorName is foreignCtorClass of the class registered as name.
func (e *Emitter) foreignCtorName(name string) bool {
	if e.prog == nil {
		return false
	}
	for _, st := range e.prog.Body {
		if cd, ok := st.(*ast.ClassDeclaration); ok && cd.Name == name {
			return e.foreignCtorClass(cd)
		}
	}
	return false
}

// ctorReturnMayOverride reports a constructor's `return v` under
// TypeScript typing that can replace the instance: an object, not a
// primitive, null or undefined (which leave it).
func (e *Emitter) ctorReturnMayOverride(v ast.Expression) bool {
	for as, ok := v.(*ast.AsExpression); ok; as, ok = v.(*ast.AsExpression) {
		v = as.Expr
	}
	t := e.typeOfExpr(v)
	return !(t.IsUndefined() || t.IR == "void" || t.IsNull() || t.IsSymbol() || t.IsBigInt() || isPlainStringTy(t) && !t.IsClass() && !t.IsObject() || t.IR == "double" || t.IR == "i1" || t.IsInteger())
}

// emitCtorOverride is a `new` site's result after the constructor ran:
// the instance the constructor returned, else the allocated one.
func (e *Emitter) emitCtorOverride(className, dataReg string) string {
	if !e.ctorMayOverride(className) {
		return dataReg
	}
	ov := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" @__kml_ctor_ret, align 8", ov))
	e.emitInstr("store " + irPtr + " null, " + irPtr + " @__kml_ctor_ret, align 8")
	isNull := e.ptrIsNull(ov)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", r, isNull, dataReg, ov))
	return r
}

func llvmSafeSymbol(s string) string {
	s = strings.ReplaceAll(s, "@@", "__kml_wks_")
	s = strings.ReplaceAll(s, "#", "__kml_priv_")
	if isBareLLVMIdent(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if r < 128 && (r == '_' || r == '$' || r == '.' ||
			(r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')) {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "__kml_u%x_", r)
		}
	}
	return b.String()
}

// isBareLLVMIdent reports whether every byte of s is legal in an unquoted LLVM
// identifier. Fast-path guard so the common all-ASCII symbol keeps zero allocs.
func isBareLLVMIdent(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' || c == '$' || c == '.' ||
			(c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			continue
		}
		return false
	}
	return true
}

// classAccessorSigs returns the getter/setter FuncSig for property `prop`
// on class `className`, if either is registered (TDD-00030's
// accessorMethodName-keyed dispatch, stored in the class's ordinary
// MethodSigs table — see registerClasses). ok is false when neither
// exists, meaning `prop` is either a plain field or a genuinely unknown
// property — the caller should fall through to its own existing
// FieldIndex-based path unchanged in that case, exactly as if this
// function had never been called.
func (e *Emitter) classAccessorSigs(className, prop string) (getter, setter *FuncSig, ok bool) {
	info, found := e.classes[className]
	if !found {
		return nil, nil, false
	}
	if g, has := info.MethodSigs[accessorMethodName("get", prop)]; has {
		getter = &g
	}
	if s, has := info.MethodSigs[accessorMethodName("set", prop)]; has {
		setter = &s
	}
	return getter, setter, getter != nil || setter != nil
}

// functionProtoMembers are Function.prototype's members, which every class
// value inherits.
var functionProtoMembers = map[string]bool{
	"apply": true, "bind": true, "call": true, "toString": true, "constructor": true,
	"hasOwnProperty": true, "isPrototypeOf": true, "propertyIsEnumerable": true,
	"toLocaleString": true, "valueOf": true, "__proto__": true,
}

// emitStaticFieldRead evaluates `ClassName.staticField` (TDD-00009 Stage
// 4): a plain load off whichever class's global actually owns the storage
// (StaticFieldOwner — an inherited, non-redeclared static field shares its
// base's global rather than getting its own).
func (e *Emitter) emitStaticFieldRead(info ClassInfo, className, fieldName string, pos ast.Pos) (Value, error) {
	fieldTy, ok := info.StaticFieldTypes[fieldName]
	if !ok {
		if _, isGetter := info.StaticMethodSigs[accessorMethodName("get", fieldName)]; isGetter {
			return e.emitStaticMethodCall(info, className, accessorMethodName("get", fieldName), nil, pos)
		}
		if _, isMethod := info.StaticMethodSigs[fieldName]; isMethod && e.staticMethodOverridable(className, fieldName) {
			return e.emitStaticMethodOverrideRead(info, className, fieldName, pos)
		}
		if sig, isMethod := info.StaticMethodSigs[fieldName]; isMethod {
			// A static method read as a value (`const f = C.m`): its function
			// object, one header per method, as a named function's. An own
			// `static name()` or `length()` replaces the class's own.
			return e.emitNamedFuncValue(staticMethodName(info.StaticMethodImplementor[fieldName], fieldName), sig, fieldName), nil
		}
		switch fieldName {
		case "name":
			// A class's `name`: its source name.
			return Value{Ref: e.internString(e.classDisplayName(className)), Ty: TypePtr}, nil
		case "length":
			// A class's `length`: its own constructor's parameters before
			// the first defaulted or rest one.
			return e.countToNumber(Value{Ref: fmt.Sprint(e.ctorLength(className)), Ty: TypeI64}), nil
		}
		if e.extendsObject(className) {
			// A static Object has (`C.keys`): inherited, as in JavaScript.
			return e.emitExpr(ast.NewMemberExpression(e.builtinIdent("Object", pos), fieldName, pos))
		}
		if b := e.builtinRootBase(className); b != "" && !e.blockDone {
			// A static of the builtin the class extends
			// (`MyU8.BYTES_PER_ELEMENT`, `MyMap.groupBy`): inherited.
			if v, err := e.emitExpr(ast.NewMemberExpression(e.builtinIdent(b, pos), fieldName, pos)); err == nil {
				return v, nil
			}
		}
		if e.compatJS() || functionProtoMembers[fieldName] {
			// A class is an object: a property it was given at run time
			// (`Object.defineProperty(C, "k", …)`), else its prototype
			// chain's (Function.prototype's `call`, `bind`), else undefined.
			return e.emitDynAnyMemberGetNamed(e.emitClassRef(className), e.internString(fieldName), fieldName, pos)
		}
		return Value{}, internalAt(pos, "class '%s' has no static field '%s'", className, fieldName)
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", reg, storageIR(fieldTy), e.staticFieldReadPtr(className, fieldName), fieldTy.Align()))
	return Value{Ref: reg, Ty: fieldTy}, nil
}

// staticFieldReadPtr is the storage `className.fieldName` reads: an
// inherited static field is the base's until a class between them assigns
// its own (`B.n = 5` gives B an own `n`, as JavaScript's constructor
// prototype chain does).
func (e *Emitter) staticFieldReadPtr(className, fieldName string) string {
	owner := e.classes[className].StaticFieldOwner[fieldName]
	ptr := e.staticSlotPtr(owner, fieldName)
	var chain []string
	for c := className; c != "" && c != owner; c = e.classes[c].BaseClass {
		chain = append(chain, c)
	}
	for i := len(chain) - 1; i >= 0; i-- {
		own := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", own, e.staticOwnPtr(chain[i], fieldName)))
		sel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", sel, own, e.staticSlotPtr(chain[i], fieldName), ptr))
		ptr = sel
	}
	return ptr
}

// staticFieldWritePtr is the storage an assignment to
// `className.fieldName` writes, marking an inherited field the class's own.
func (e *Emitter) staticFieldWritePtr(className, fieldName string) string {
	owner := e.classes[className].StaticFieldOwner[fieldName]
	if owner == className {
		return e.staticSlotPtr(owner, fieldName)
	}
	e.heapStore("i1", "true", e.staticOwnPtr(className, fieldName), 1)
	return e.staticSlotPtr(className, fieldName)
}

// emitStaticFieldAssign evaluates `ClassName.staticField = val` (or a
// compound op) — TDD-00009 Stage 4. Same shape as the instance field-write
// path in emit_exprs_assign.go, minus the GEP: a static field's storage is
// a plain global, addressed directly by name.
func (e *Emitter) emitStaticFieldAssign(info ClassInfo, className, fieldName, op string, rhsExpr ast.Expression, pos ast.Pos) (Value, error) {
	fieldTy, ok := info.StaticFieldTypes[fieldName]
	if !ok {
		if sig, isSetter := info.StaticMethodSigs[accessorMethodName("set", fieldName)]; isSetter {
			// A static setter: its argument is the assigned value (a compound
			// assignment reads the getter first); the expression's value is
			// that value.
			value := rhsExpr
			if op != "=" {
				value = ast.NewBinaryExpression(op[:len(op)-1], ast.NewMemberExpression(ast.NewIdentifier(className, pos), fieldName, pos), rhsExpr, pos)
			}
			v, err := e.emitExpr(value)
			if err != nil {
				return Value{}, err
			}
			if len(sig.ParamTypes) == 1 {
				v = e.convert(v, sig.ParamTypes[0])
			}
			tmp := fmt.Sprintf("__kml_ssetv_%d", e.dynFnCtr)
			e.dynFnCtr++
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, StructFieldIR(v.Ty)))
			e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align 8", StructFieldIR(v.Ty), v.Ref, slot))
			// A nullable scalar's slot holds its { i1, payload } pair, read
			// back as one (null stays null in the setter).
			e.define(tmp, Symbol{Ptr: slot, Ty: v.Ty})
			if _, err := e.emitStaticMethodCall(info, className, accessorMethodName("set", fieldName), []ast.Expression{ast.NewIdentifier(tmp, pos)}, pos); err != nil {
				return Value{}, err
			}
			return v, nil
		}
		if e.compatJS() {
			// A class is an object: the property goes on it at run time.
			ref := ast.NewMemberExpression(ast.NewIdentifier(className, pos), fieldName, pos)
			if isLogicalAssignOp(op) {
				return e.emitExpr(ast.NewBinaryExpression(strings.TrimSuffix(op, "="), ref, ast.NewAssignmentExpression("=", ref, rhsExpr, pos), pos))
			}
			value := rhsExpr
			if op != "=" {
				value = ast.NewBinaryExpression(op[:len(op)-1], ref, rhsExpr, pos)
			}
			v, err := e.emitExpr(value)
			if err != nil {
				return Value{}, err
			}
			return e.emitDynAnyMemberSetNamed(e.emitClassRef(className), e.internString(fieldName), fieldName, v, pos)
		}
		return Value{}, internalAt(pos, "class '%s' has no static field '%s'", className, fieldName)
	}
	inherited := info.StaticFieldOwner[fieldName] != className
	if isLogicalAssignOp(op) {
		if inherited {
			// `B.n ||= x` is `B.n || (B.n = x)`: only a write makes it B's own.
			ref := ast.NewMemberExpression(ast.NewIdentifier(className, pos), fieldName, pos)
			return e.emitExpr(ast.NewBinaryExpression(strings.TrimSuffix(op, "="), ref, ast.NewAssignmentExpression("=", ref, rhsExpr, pos), pos))
		}
		return e.emitLogicalCompoundAssign(op, e.staticFieldWritePtr(className, fieldName), fieldTy, rhsExpr)
	}
	globalPtr := e.staticFieldReadPtr(className, fieldName)

	var rhs Value
	var err error
	if op == "=" {
		rhs, err = e.emitExpr(rhsExpr)
		if err != nil {
			return Value{}, err
		}
	} else {
		curReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", curReg, storageIR(fieldTy), globalPtr, fieldTy.Align()))
		cur := Value{Ref: curReg, Ty: fieldTy}
		rhsVal, err := e.emitExpr(rhsExpr)
		if err != nil {
			return Value{}, err
		}
		if err := dateCompoundAssignGuard(op, fieldTy.IsDate(), rhsVal.Ty.IsDate()); err != nil {
			return Value{}, internalAt(pos, "%s", err)
		}
		rhsVal = e.convert(rhsVal, fieldTy)
		rhs, err = e.emitArith(strings.TrimSuffix(op, "="), cur, rhsVal, fieldTy, pos)
		if err != nil {
			return Value{}, err
		}
	}
	rhs = e.convert(rhs, fieldTy)
	e.heapStore(storageIR(fieldTy), rhs.Ref, e.staticFieldWritePtr(className, fieldName), fieldTy.Align())
	return rhs, nil
}

// hasTopLevelSuperCall reports whether a constructor body contains a
// `super(...)` call as one of its own top-level statements — a shallow,
// non-nested presence check (consistent with this project's existing
// "cheapest useful check" pattern, e.g. Stage 1's flat "constructor
// required if fields present" rule), not full control-flow analysis.
func hasTopLevelSuperCall(body *ast.BlockStatement) bool {
	return topLevelSuperCallIndex(body) >= 0
}

// accessorDecl is cd's own get or set accessor of name.
func accessorDecl(cd *ast.ClassDeclaration, kind, name string, static bool) *ast.FunctionDeclaration {
	for _, m := range cd.Methods {
		if m.AccessorKind == kind && m.Name == name && m.IsStatic == static {
			return m
		}
	}
	return nil
}

// bareFieldType is the representation of a class field declared with
// neither a type nor an initializer: the checker's type for it, else any.
func (e *Emitter) bareFieldType(cd *ast.ClassDeclaration, name string) Type {
	if c := e.front(); c != nil {
		if t := c.FieldType(cd, name); t != nil && !c.Unanswered(t) {
			if ty, ok := reprOf(t); ok {
				return ty
			}
		}
	}
	return TypeAny
}

// containsSuperCall reports whether a constructor body calls super(...)
// anywhere, arrow functions included (they share the constructor's this).
func containsSuperCall(body *ast.BlockStatement) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		case *ast.CallExpression:
			if _, ok := n.Callee.(*ast.SuperExpression); ok {
				found = true
			}
		}
		return !found
	})
	return found
}

// derivedNoSuperMessage is Node's message for a derived constructor that
// reads this or completes before calling super().
const derivedNoSuperMessage = "Must call super constructor in derived class before accessing 'this' or returning from derived constructor"

// guardMissingSuper rewrites a derived constructor body that never calls
// super() the way Node runs it: its statements run until the first one that
// reads this, which throws a ReferenceError, and completing or returning
// throws the same error; an exception the body throws propagates as is.
//
//	let threw = false
//	try { <statements before this> throw RE } catch (e) { threw = true; throw e } finally { if (!threw) throw RE }
func guardMissingSuper(body *ast.BlockStatement) {
	pos := body.GetPos()
	refErr := func() ast.Statement {
		return ast.NewThrowStatement(ast.NewNewErrorExpression("ReferenceError", ast.NewStringLiteral(derivedNoSuperMessage, pos), pos), pos)
	}
	var run []ast.Statement
	for _, st := range body.Body {
		if containsThis(st) {
			break
		}
		run = append(run, st)
	}
	run = append(run, refErr())
	const flag = "__kml_ctor_threw"
	threw := func() *ast.Identifier { return ast.NewIdentifier(flag, pos) }
	catch := &ast.CatchClause{Param: "__kml_ctor_err", Pos: pos, Body: ast.NewBlockStatement([]ast.Statement{
		ast.NewExpressionStatement(ast.NewAssignmentExpression("=", threw(), ast.NewBooleanLiteral(true, pos), pos), pos),
		ast.NewThrowStatement(ast.NewIdentifier("__kml_ctor_err", pos), pos),
	}, pos)}
	finally := ast.NewBlockStatement([]ast.Statement{
		ast.NewIfStatement(ast.NewUnaryExpression("!", true, threw(), pos), ast.NewBlockStatement([]ast.Statement{refErr()}, pos), nil, pos),
	}, pos)
	body.Body = []ast.Statement{
		ast.NewVarDeclaration("let", flag, nil, ast.NewBooleanLiteral(false, pos), pos),
		ast.NewTryStatement(ast.NewBlockStatement(run, pos), catch, finally, pos),
	}
}

// superAgainMessage is Node's message for a second super() call.
const superAgainMessage = "Super constructor may only be called once"

// countSuperCalls counts the super(...) call sites of a constructor body,
// arrow functions included.
func countSuperCalls(body *ast.BlockStatement) int {
	n := 0
	ast.Inspect(body, func(x ast.Node) bool {
		switch x := x.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		case *ast.CallExpression:
			if _, ok := x.Callee.(*ast.SuperExpression); ok {
				n++
			}
		}
		return true
	})
	return n
}

// guardRepeatedSuper makes a constructor with several super() sites throw
// Node's ReferenceError when a second one runs: after each statement that
// calls super(), a flag set by the first call is checked.
//
//	let done = false
//	super(); if (done) throw RE; done = true
func guardRepeatedSuper(body *ast.BlockStatement) {
	pos := body.GetPos()
	const flag = "__kml_super_done"
	ast.Inspect(body, func(x ast.Node) bool {
		switch blk := x.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		case *ast.BlockStatement:
			var out []ast.Statement
			for _, st := range blk.Body {
				out = append(out, st)
				if !stmtCallsSuperDirectly(st) {
					continue
				}
				out = append(out,
					ast.NewIfStatement(ast.NewIdentifier(flag, pos), ast.NewBlockStatement([]ast.Statement{
						ast.NewThrowStatement(ast.NewNewErrorExpression("ReferenceError", ast.NewStringLiteral(superAgainMessage, pos), pos), pos),
					}, pos), nil, pos),
					ast.NewExpressionStatement(ast.NewAssignmentExpression("=", ast.NewIdentifier(flag, pos), ast.NewBooleanLiteral(true, pos), pos), pos))
			}
			blk.Body = out
		}
		return true
	})
	body.Body = append([]ast.Statement{ast.NewVarDeclaration("let", flag, nil, ast.NewBooleanLiteral(false, pos), pos)}, body.Body...)
}

// stmtCallsSuperDirectly reports a simple statement (not a block, branch or
// loop, whose own blocks are visited) that calls super(...).
func stmtCallsSuperDirectly(st ast.Statement) bool {
	switch st.(type) {
	case *ast.ExpressionStatement, *ast.VarDeclaration, *ast.VarDeclarationList, *ast.ReturnStatement:
	default:
		return false
	}
	found := false
	ast.Inspect(st, func(x ast.Node) bool {
		switch x := x.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		case *ast.CallExpression:
			if _, ok := x.Callee.(*ast.SuperExpression); ok {
				found = true
			}
		}
		return !found
	})
	return found
}

// topLevelSuperCallIndex returns the statement index of the first top-level
// `super(...)` call in body, or -1 if there is none — the insertion anchor
// for field initializers (TDD-00063 Stage 1), which must run immediately
// after super() returns (so `this` exists) and before the rest of the
// constructor body. Same shallow, non-nested scan as hasTopLevelSuperCall.
func topLevelSuperCallIndex(body *ast.BlockStatement) int {
	for i, s := range body.Body {
		es, ok := s.(*ast.ExpressionStatement)
		if !ok {
			continue
		}
		call, ok := es.Expr.(*ast.CallExpression)
		if !ok {
			continue
		}
		if _, ok := call.Callee.(*ast.SuperExpression); ok {
			return i
		}
	}
	return -1
}

// classFieldInitStmts desugars a class's own instance-field initializers
// (TDD-00063 Stage 1) into `this.<field> = <initExpr>` expression statements,
// in field-declaration order — the exact order the spec runs them. Static
// fields and fields without an initializer are skipped. The returned
// statements are spliced into the constructor body by registerClasses (after
// super(), or at the top for a base class) and then emitted by the ordinary
// constructor path with no further special-casing.
// classFieldNullEvolves reports whether a `null`/`undefined`-initialized
// unannotated field is later reassigned to a concrete value anywhere in the
// class — a plain `this.<field> = <non-nullish>` or a logical-compound-assign
// (`??=`/`||=`/`&&=`) in the constructor, a method, or a static block. Such a
// field is TypeScript's evolving-any and is widened to a boxed `any` slot
// (TDD-00208). Mirrors the local-binding rule (ADR-00923).
func (e *Emitter) classFieldNullEvolves(cd *ast.ClassDeclaration, fieldName string) bool {
	found := false
	var walkExpr func(ast.Expression)
	assignTriggers := func(as *ast.AssignmentExpression) bool {
		mem, ok := as.Left.(*ast.MemberExpression)
		if !ok || mem.Property != fieldName {
			return false
		}
		if _, ok := mem.Object.(*ast.ThisExpression); !ok {
			return false
		}
		if as.Op == "??=" || as.Op == "||=" || as.Op == "&&=" {
			return true
		}
		if as.Op == "=" {
			rt := e.typeOfExpr(as.Right)
			return !rt.IsNull() && !rt.IsUndefined()
		}
		return false
	}
	walkExpr = func(expr ast.Expression) {
		if expr == nil || found {
			return
		}
		switch ex := expr.(type) {
		case *ast.AssignmentExpression:
			if assignTriggers(ex) {
				found = true
				return
			}
			walkExpr(ex.Left)
			walkExpr(ex.Right)
		case *ast.BinaryExpression:
			walkExpr(ex.Left)
			walkExpr(ex.Right)
		case *ast.ConditionalExpression:
			walkExpr(ex.Test)
			walkExpr(ex.Consequent)
			walkExpr(ex.Alternate)
		case *ast.SequenceExpression:
			for _, s := range ex.Exprs {
				walkExpr(s)
			}
		case *ast.UnaryExpression:
			walkExpr(ex.Arg)
		case *ast.SpreadElement:
			walkExpr(ex.Arg)
		case *ast.CallExpression:
			walkExpr(ex.Callee)
			for _, a := range ex.Args {
				walkExpr(a)
			}
		case *ast.MemberExpression:
			walkExpr(ex.Object)
		case *ast.IndexExpression:
			walkExpr(ex.Object)
			walkExpr(ex.Index)
		case *ast.ArrayLiteral:
			for _, el := range ex.Elements {
				walkExpr(el)
			}
		case *ast.ObjectLiteral:
			for _, p := range ex.Properties {
				walkExpr(p.KeyExpr)
				walkExpr(p.Value)
			}
		case *ast.TemplateLiteral:
			for _, s := range ex.Exprs {
				walkExpr(s)
			}
		}
	}
	var walkStmts func([]ast.Statement)
	walkStmt := func(s ast.Statement) { walkStmts([]ast.Statement{s}) }
	walkStmts = func(stmts []ast.Statement) {
		for _, stmt := range stmts {
			if found {
				return
			}
			switch s := stmt.(type) {
			case *ast.ExpressionStatement:
				walkExpr(s.Expr)
			case *ast.VarDeclaration:
				walkExpr(s.Init)
			case *ast.VarDeclarationList:
				for _, d := range s.Decls {
					walkExpr(d.Init)
				}
			case *ast.ReturnStatement:
				walkExpr(s.Value)
			case *ast.ThrowStatement:
				walkExpr(s.Argument)
			case *ast.BlockStatement:
				if s != nil {
					walkStmts(s.Body)
				}
			case *ast.IfStatement:
				walkExpr(s.Test)
				walkStmt(s.Consequent)
				walkStmt(s.Alternate)
			case *ast.ForStatement:
				walkStmt(s.Init)
				walkExpr(s.Test)
				for _, u := range s.Update {
					walkExpr(u)
				}
				walkStmt(s.Body)
			case *ast.ForOfStatement:
				walkExpr(s.Iterable)
				walkStmt(s.Body)
			case *ast.ForInStatement:
				walkStmt(s.Body)
			case *ast.WhileStatement:
				walkExpr(s.Test)
				walkStmt(s.Body)
			case *ast.DoWhileStatement:
				walkStmt(s.Body)
				walkExpr(s.Test)
			case *ast.SwitchStatement:
				walkExpr(s.Discriminant)
				for _, c := range s.Cases {
					walkExpr(c.Test)
					walkStmts(c.Body)
				}
			case *ast.TryStatement:
				if s.Body != nil {
					walkStmts(s.Body.Body)
				}
				if s.Catch != nil && s.Catch.Body != nil {
					walkStmts(s.Catch.Body.Body)
				}
				if s.Finally != nil {
					walkStmts(s.Finally.Body)
				}
			case *ast.LabeledStatement:
				walkStmt(s.Body)
			}
		}
	}
	if cd.Constructor != nil && cd.Constructor.Body != nil {
		walkStmts(cd.Constructor.Body.Body)
	}
	for _, m := range cd.Methods {
		if m.Body != nil {
			walkStmts(m.Body.Body)
		}
	}
	for _, sb := range cd.StaticBlocks {
		if sb != nil {
			walkStmts(sb.Body)
		}
	}
	return found
}

func classFieldInitStmts(cd *ast.ClassDeclaration) []ast.Statement {
	var stmts []ast.Statement
	for _, f := range cd.Fields {
		if f.Static || f.Initializer == nil {
			continue
		}
		pos := f.Initializer.GetPos()
		target := ast.NewMemberExpression(ast.NewThisExpression(pos), f.Name, pos)
		assign := ast.NewAssignmentExpression("=", target, f.Initializer, pos)
		stmts = append(stmts, ast.NewExpressionStatement(assign, pos))
	}
	return stmts
}

// inlineFieldInitsAtSuper rewrites each `super(…)` in cd's constructor that
// is not a top-level statement (arrow functions included: they share the
// constructor's this) to `(super(…), this.f = init, …, this)`: the own field
// initializers run right after the base constructor returns, and the call
// still evaluates to the this it initialized.
func inlineFieldInitsAtSuper(cd *ast.ClassDeclaration) {
	var rewrite func(ast.Node) ast.Node
	rewrite = func(n ast.Node) ast.Node {
		switch n.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ClassDeclaration, *ast.ClassExpression:
			return n
		}
		ast.RewriteChildren(n, rewrite)
		call, ok := n.(*ast.CallExpression)
		if !ok {
			return n
		}
		if _, super := call.Callee.(*ast.SuperExpression); !super {
			return n
		}
		pos := call.GetPos()
		exprs := []ast.Expression{call}
		for _, st := range classFieldInitStmts(cd) {
			exprs = append(exprs, st.(*ast.ExpressionStatement).Expr)
		}
		return ast.NewSequenceExpression(append(exprs, ast.NewThisExpression(pos)), pos)
	}
	ast.RewriteChildren(cd.Constructor.Body, rewrite)
}

// classStaticFieldInitStmts lowers each `static x = expr` field to a
// `ClassName.x = expr` assignment, in field-declaration order — the statements
// emitClassStaticInit runs (ahead of any `static {}` block) so a static field's
// initializer executes once at program start (ADR-00375). Reuses the same
// static-field member-assignment codegen a `static {}` block already uses.
func classStaticFieldInitStmts(cd *ast.ClassDeclaration) []ast.Statement {
	var stmts []ast.Statement
	for _, f := range cd.Fields {
		if !f.Static || f.Initializer == nil {
			continue
		}
		pos := f.Initializer.GetPos()
		target := ast.NewMemberExpression(ast.NewIdentifier(cd.Name, pos), f.Name, pos)
		assign := ast.NewAssignmentExpression("=", target, f.Initializer, pos)
		stmts = append(stmts, ast.NewExpressionStatement(assign, pos))
	}
	return stmts
}

// classHasStaticFieldInit reports whether cd declares at least one `static x =
// expr` field — used to decide whether a class needs a staticinit function even
// when it has no `static {}` block.
func classHasStaticFieldInit(cd *ast.ClassDeclaration) bool {
	for _, f := range cd.Fields {
		if f.Static && f.Initializer != nil {
			return true
		}
	}
	return false
}

// registerClasses pre-scans all top-level class declarations, resolving
// field/constructor/method shapes and (TDD-00009 Stage 3) the whole-program
// inheritance graph before any function or method body is emitted —
// mirroring registerInterfaces (field resolution) and registerFunctions
// (signature building, including best-effort unannotated-return-type
// inference).
//
// Five passes:
//
//	Pass 0: seed every class name under a placeholder type (so a self- or
//	  forward-referential field resolves), then validate every `extends`
//	  target and compute a base-before-derived topological processing order
//	  (also catching cycles and unknown base names).
//	Pass 1: per class, in topological order — flatten inherited fields,
//	  resolve this class's own new fields, build the effective method table
//	  (inherit then apply overrides, detecting + validating them), and
//	  resolve constructor rules (explicit + super()-presence check, implicit
//	  pass-through synthesis, or none needed).
//	Pass 1.5: now that every class's AncestorChain is known, compute
//	  Descendants (the inverse relation) for instanceof's dynamic case.
//	Pass 2: number every Virtual slot after the base class's slots, and
//	  decide HasVTable uniformly per inheritance tree (true if some slot
//	  anywhere in the tree ended up Virtual, or the tree is the library's).
//	Pass 3: finalize each class's real Ty (field layout now depends on
//	  Pass 2's HasVTable decision) and publish it into e.interfaces.
//
// registerClassNamePlaceholders records the same name-only ClassType placeholder
// registerClasses' Pass 0 does, but a step earlier — before registerInterfaces —
// so a class used as an interface/type-alias field type is already resolvable as
// a ptr (canonicalized to its full field-bearing type on access). Generic
// classes are skipped (they go to e.genericClasses, instantiated on demand).
func (e *Emitter) registerClassNamePlaceholders(prog *ast.Program) {
	for _, stmt := range prog.Body {
		cd, ok := stmt.(*ast.ClassDeclaration)
		if !ok || len(cd.TypeParams) > 0 {
			continue
		}
		e.interfaces[cd.Name] = ClassType(cd.Name, nil, nil, false)
	}
}

// genericStaticsClass names the companion class holding a generic class's
// static methods.
func genericStaticsClass(name string) string {
	return name + "__kml_statics"
}

// genericStaticMember is the companion class holding generic class id's
// static member prop, when it has one.
func (e *Emitter) genericStaticMember(id *ast.Identifier, prop string) (ClassInfo, string, bool) {
	if _, generic := e.genericClasses[id.Name]; !generic || e.isShadowedByLocal(id.Name) {
		return ClassInfo{}, "", false
	}
	name := genericStaticsClass(id.Name)
	info, ok := e.classes[name]
	if !ok {
		return ClassInfo{}, "", false
	}
	if _, f := info.StaticFieldTypes[prop]; f {
		return info, name, true
	}
	for _, k := range []string{prop, accessorMethodName("get", prop), accessorMethodName("set", prop)} {
		if _, m := info.StaticMethodSigs[k]; m {
			return info, name, true
		}
	}
	return ClassInfo{}, "", false
}

// methodsMayReadArgs reports whether a method or constructor call stores
// its argument count: when a class of the program reads `arguments`, and
// always in program-independent code, whose callee may be overridden by a
// class of whatever program it is shared with.
func (e *Emitter) methodsMayReadArgs() bool { return e.methodsReadArgs || e.programIndependent() }

// objectBaseClass is the class a class extending the builtin Object
// extends instead: Object constructs a plain object for a derived class's
// new.target, which is what an empty base's construction yields.
const objectBaseClass = "Object__kml_base"

// objectBase points each class extending the builtin Object (no program
// binding of that name, which would be module-mangled) at objectBaseClass,
// declared once ahead of the program.
// settleMemberDefinitions keeps the class members JavaScript's definition
// order leaves (JS lane): on the prototype (or the class, for statics) a
// method replaces whatever its name held, an accessor replaces a method and
// merges with the other half of an accessor; a field, defined on the
// instance (or the class) afterwards, hides an accessor of its name. (An
// instance field leaves the prototype's accessor in JavaScript, seen only
// by reflection on the prototype.)
func settleMemberDefinitions(cd *ast.ClassDeclaration) {
	type slot struct{ method, get, set int }
	slots := map[string]*slot{}
	key := func(m *ast.FunctionDeclaration) string {
		if m.IsStatic {
			return "static " + m.Name
		}
		return m.Name
	}
	for i, m := range cd.Methods {
		if m.Body == nil && m.AccessorKind == "" {
			continue // an overload signature
		}
		k := key(m)
		sl := slots[k]
		if sl == nil {
			sl = &slot{-1, -1, -1}
			slots[k] = sl
		}
		switch m.AccessorKind {
		case "get":
			sl.method, sl.get = -1, i
		case "set":
			sl.method, sl.set = -1, i
		default:
			sl.method, sl.get, sl.set = i, -1, -1
		}
	}
	fields := map[string]bool{}
	for _, f := range cd.Fields {
		k := f.Name
		if f.Static {
			k = "static " + k
		}
		fields[k] = true
	}
	kept := cd.Methods[:0:0]
	changed := false
	for i, m := range cd.Methods {
		sl := slots[key(m)]
		live := sl == nil || i == sl.method || i == sl.get || i == sl.set
		if m.AccessorKind != "" && fields[key(m)] {
			live = false
		}
		if m.Body == nil && m.AccessorKind == "" && sl != nil && sl.method < 0 {
			live = false // overloads of a method an accessor replaced
		}
		if live {
			kept = append(kept, m)
		} else {
			changed = true
		}
	}
	if changed {
		cd.Methods = kept
	}
}

// undeclaredHeritage finds each top-level class extending a name nothing
// declares (JavaScript: its definition's read of the name is a
// ReferenceError). The class registers as Object-based; its definition
// reads the name first (undeclaredBases).
func (e *Emitter) undeclaredHeritage(prog *ast.Program) {
	c := e.front()
	if c == nil {
		return
	}
	declared := map[string]bool{}
	params := func(ps []ast.Param) {
		for _, p := range ps {
			declared[p.Name] = true
		}
	}
	ast.Inspect(prog, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.VarDeclaration:
			declared[d.Name] = true
		case *ast.ClassDeclaration:
			declared[d.Name] = true
		case *ast.FunctionDeclaration:
			declared[d.Name] = true
			params(d.Params)
		case *ast.FunctionExpression:
			params(d.Params)
		case *ast.ArrowFunction:
			params(d.Params)
		}
		return true
	})
	for _, stmt := range prog.Body {
		cd, ok := stmt.(*ast.ClassDeclaration)
		if !ok || cd.BaseClass == "" || cd.BaseQualified || len(cd.TypeParams) > 0 || declared[cd.BaseClass] {
			continue
		}
		b := cd.BaseClass
		if b == "Object" || builtinValueBases[b] || e.isErrorRootKind(b) || e.hostBaseOf(cd) != nil || e.isHostLayoutBase(b) || c.GlobalInterface(b) != nil || c.AmbientName(b) {
			continue
		}
		if e.undeclaredBases == nil {
			e.undeclaredBases = map[string]string{}
		}
		e.undeclaredBases[cd.Name] = b
		cd.BaseClass = "Object"
	}
}

func objectBase(prog *ast.Program) {
	found := false
	for _, stmt := range prog.Body {
		if cd, ok := stmt.(*ast.ClassDeclaration); ok && cd.BaseClass == "Object" && !cd.BaseQualified && len(cd.BaseTypeArgs) == 0 {
			cd.BaseClass, found = objectBaseClass, true
		}
	}
	if found {
		base := ast.NewClassDeclaration(objectBaseClass, "", nil, false, nil, nil, nil, nil, nil, ast.Pos{})
		prog.Body = append([]ast.Statement{base}, prog.Body...)
	}
}

// fnBaseSuffix names the class a class extending a constructor function
// of the program (`class C extends F`) extends instead: an empty base,
// whose super() call runs F with the instance as `this`
// (emitFnBaseSuperCall), its prototype F.prototype.
const fnBaseSuffix = "__kml_fnbase"

// fnBases points each class extending a top-level function declaration at
// that function's empty base class, declared once ahead of the program;
// it returns the bases by function. A class extending a top-level variable
// (`var B = function () {}.bind()`) extends the value it holds when the
// class is defined (valueBases) the same way.
func fnBases(prog *ast.Program) (map[string]string, map[string]bool) {
	fns := map[string]bool{}
	classes := map[string]bool{}
	nonCtor := map[string]bool{}
	for _, stmt := range prog.Body {
		switch d := stmt.(type) {
		case *ast.FunctionDeclaration:
			if !d.IsGenerator && !d.IsAsync {
				fns[d.Name] = true
			} else if d.Body != nil {
				nonCtor[d.Name] = true // a value heritage rejects
			}
		case *ast.ClassDeclaration:
			classes[d.Name] = true
		}
	}
	vals := map[string]bool{}
	for n := range nonCtor {
		vals[n] = true
	}
	for _, stmt := range prog.Body {
		if v, ok := stmt.(*ast.VarDeclaration); ok && !v.Ambient && !fns[v.Name] && !classes[v.Name] {
			if _, isClass := v.Init.(*ast.ClassExpression); !isClass {
				vals[v.Name] = true
			}
		}
	}
	// A class in a function extending one of its parameters or variables
	// (`function g(B) { class T extends B {} }`): the value it holds.
	allClasses := map[string]bool{}
	ast.Inspect(prog, func(n ast.Node) bool {
		if cd, ok := n.(*ast.ClassDeclaration); ok {
			allClasses[cd.Name] = true
		}
		return true
	})
	localBase := func(params []ast.Param, body ast.Node) {
		if body == nil {
			return
		}
		locals := map[string]bool{}
		for _, p := range params {
			locals[p.Name] = true
		}
		ast.Inspect(body, func(n ast.Node) bool {
			if v, ok := n.(*ast.VarDeclaration); ok {
				if _, isClass := v.Init.(*ast.ClassExpression); !isClass {
					locals[v.Name] = true
				}
			}
			return true
		})
		ast.Inspect(body, func(n ast.Node) bool {
			if cd, ok := n.(*ast.ClassDeclaration); ok && !cd.BaseQualified && locals[cd.BaseClass] && !allClasses[cd.BaseClass] && !fns[cd.BaseClass] {
				vals[cd.BaseClass] = true
			}
			return true
		})
	}
	ast.Inspect(prog, func(n ast.Node) bool {
		switch f := n.(type) {
		case *ast.FunctionDeclaration:
			if f.Body != nil {
				localBase(f.Params, f.Body)
			}
		case *ast.FunctionExpression:
			if f.Body != nil {
				localBase(f.Params, f.Body)
			}
		case *ast.ArrowFunction:
			if f.Block != nil {
				localBase(f.Params, f.Block)
			}
		}
		return true
	})
	values := map[string]bool{}
	nested := map[string]bool{}
	ast.Inspect(prog, func(n ast.Node) bool {
		if cd, ok := n.(*ast.ClassDeclaration); ok && vals[cd.BaseClass] && !cd.BaseQualified {
			fns[cd.BaseClass], values[cd.BaseClass], nested[cd.BaseClass] = true, true, true
		}
		return true
	})
	for _, stmt := range prog.Body {
		if cd, ok := stmt.(*ast.ClassDeclaration); ok && !cd.BaseQualified && builtinValueBases[cd.BaseClass] && !fns[cd.BaseClass] && !classes[cd.BaseClass] {
			vals[cd.BaseClass] = true // the global itself, a value
		}
		if cd, ok := stmt.(*ast.ClassDeclaration); ok && vals[cd.BaseClass] && !cd.BaseQualified {
			fns[cd.BaseClass], values[cd.BaseClass] = true, true
		}
	}
	var out map[string]string
	var bases []ast.Statement
	var classDecls []*ast.ClassDeclaration
	for _, stmt := range prog.Body {
		if cd, ok := stmt.(*ast.ClassDeclaration); ok {
			classDecls = append(classDecls, cd)
		}
	}
	ast.Inspect(prog, func(n ast.Node) bool {
		if cd, ok := n.(*ast.ClassDeclaration); ok && nested[cd.BaseClass] {
			classDecls = append(classDecls, cd)
		}
		return true
	})
	for _, cd := range classDecls {
		if !fns[cd.BaseClass] || cd.BaseQualified {
			continue
		}
		fn := cd.BaseClass
		if out == nil {
			out = map[string]string{}
		}
		if _, done := out[fn]; !done {
			out[fn] = fn + fnBaseSuffix
			bases = append(bases, ast.NewClassDeclaration(fn+fnBaseSuffix, "", nil, false, nil, nil, nil, nil, nil, ast.Pos{}))
		}
	}
	prog.Body = append(bases, prog.Body...)
	return out, values
}

// nonConstructingBuiltins are the global functions whose [[Construct]]
// always throws a TypeError ("X is not a constructor") when a new.target
// reaches it, as a subclass's super() does: valid heritage, never built.
var nonConstructingBuiltins = map[string]bool{"Symbol": true, "BigInt": true}

// builtinValueBases are the builtin globals a class extends as values, read
// at its definition: the non-constructing ones, and Proxy, a constructor
// without a `prototype` (the heritage's TypeError).
var builtinValueBases = map[string]bool{"Symbol": true, "BigInt": true, "Proxy": true}

// classBase is the base class cd's registry entry extends: a constructor
// function's empty base for `extends F` (fnBases), else cd's own.
func (e *Emitter) classBase(cd *ast.ClassDeclaration) string {
	if b, ok := e.fnBaseClasses[cd.BaseClass]; ok && !cd.BaseQualified {
		return b
	}
	return cd.BaseClass
}

// fnBaseRoot is the constructor function className's base chain ends at,
// when no class along it declares a constructor, or "".
func (e *Emitter) fnBaseRoot(className string) string {
	for i := 0; i < 64 && className != ""; i++ {
		if fn := fnBaseOf(className); fn != "" {
			return fn
		}
		info, ok := e.classes[className]
		if !ok || info.Constructor != nil {
			return ""
		}
		className = info.BaseClass
	}
	return ""
}

// fnBaseOf is the constructor function className's empty base stands for,
// or "".
func fnBaseOf(className string) string {
	if strings.HasSuffix(className, fnBaseSuffix) {
		return strings.TrimSuffix(className, fnBaseSuffix)
	}
	return ""
}

// classOwnStaticKeys are class c's own keys as an object: length, name and
// prototype, its static methods and accessors, and its static fields (only
// these, when enumerable).
func (e *Emitter) classOwnStaticKeys(c string, enumerable bool) []string {
	info := e.classes[c]
	var keys []string
	if !enumerable {
		keys = append(keys, "length", "name", "prototype")
		ms := make([]string, 0, len(info.StaticMethodSigs))
		for m := range info.StaticMethodSigs {
			ms = append(ms, m)
		}
		sort.Strings(ms) // one emitted order, whatever the map's
		for _, m := range ms {
			if info.StaticMethodImplementor[m] != c {
				continue
			}
			k := strings.TrimPrefix(strings.TrimPrefix(m, "__kml_get_"), "__kml_set_")
			if !strings.HasPrefix(k, "#") && !strings.HasPrefix(k, "__kml_") {
				keys = append(keys, k)
			}
		}
	}
	for _, f := range info.OwnStaticFieldOrder {
		if !strings.HasPrefix(f, "#") {
			keys = append(keys, f)
		}
	}
	return keys
}

// builtinRootBase is the builtin with a layout of its own (a typed array,
// Map, ArrayBuffer, …) className's base chain ends at, or "".
func (e *Emitter) builtinRootBase(className string) string {
	for i := 0; i < 64 && className != ""; i++ {
		b := e.classes[className].BaseClass
		if _, user := e.classes[b]; b != "" && !user {
			if typedArrayNames[b] || e.isHostLayoutBase(b) || b == "ArrayBuffer" || b == "SharedArrayBuffer" {
				return b
			}
			return ""
		}
		className = b
	}
	return ""
}

// extendsObject reports className's base chain ending at the builtin
// Object (objectBase).
func (e *Emitter) extendsObject(className string) bool {
	for i := 0; i < 64 && className != ""; i++ {
		if className == objectBaseClass {
			return true
		}
		className = e.classes[className].BaseClass
	}
	return false
}

func (e *Emitter) registerClasses(prog *ast.Program) error {
	ast.Inspect(prog, func(n ast.Node) bool {
		if cd, ok := n.(*ast.ClassDeclaration); ok {
			dropReplacedMethods(cd)
		}
		return true
	})
	ast.Inspect(prog, func(n ast.Node) bool {
		if cd, ok := n.(*ast.ClassDeclaration); ok {
			members := append([]*ast.FunctionDeclaration{cd.Constructor}, cd.Methods...)
			for _, m := range members {
				if functionUsesArguments(m) {
					e.methodsReadArgs = true
				}
			}
		}
		return !e.methodsReadArgs
	})
	if e.compatJS() {
		e.undeclaredHeritage(prog)
		ast.Inspect(prog, func(n ast.Node) bool {
			if cd, ok := n.(*ast.ClassDeclaration); ok {
				settleMemberDefinitions(cd)
			}
			return true
		})
	}
	objectBase(prog)
	if e.compatJS() {
		// Registration sees each `extends F` as its empty base; the
		// declaration keeps F for the checker's later queries.
		e.fnBaseClasses, e.valueBases = fnBases(prog)
		var restore []*ast.ClassDeclaration
		for _, stmt := range prog.Body {
			if cd, ok := stmt.(*ast.ClassDeclaration); ok && !cd.BaseQualified {
				if b, ok := e.fnBaseClasses[cd.BaseClass]; ok {
					cd.BaseClass = b
					restore = append(restore, cd)
				}
			}
		}
		defer func() {
			for _, cd := range restore {
				cd.BaseClass = fnBaseOf(cd.BaseClass)
			}
		}()
	}
	classDeclByName := make(map[string]*ast.ClassDeclaration)
	for _, stmt := range prog.Body {
		if cd, ok := stmt.(*ast.ClassDeclaration); ok {
			if err := addLowerTypeInit(cd); err != nil {
				return err
			}
			// A generic class's field/param/return types reference an
			// unresolvable bare type-parameter name — it's kept entirely out
			// of this registration pipeline (no placeholder, no topo-order
			// entry) and instantiated on demand at each `new
			// ClassName<T>(...)` construction site instead (TDD-00010 V1,
			// see emit_generics.go). Deliberately out of V1 scope: a generic
			// class can't be an `extends` base or target here.
			if len(cd.TypeParams) > 0 {
				if cd.BaseClass != "" {
					return internalAt(cd.GetPos(), "generic class '%s' cannot use 'extends' — not yet supported", cd.Name)
				}
				if cd.IsAbstract {
					return internalAt(cd.GetPos(), "generic class '%s' cannot be abstract — not yet supported", cd.Name)
				}
				// A static member cannot name the class's type parameters, so
				// the statics (methods, fields, static blocks) compile once,
				// as a non-generic companion class the static sites resolve
				// to (genericStaticsClass): one class object, as in JS.
				var statics, members []*ast.FunctionDeclaration
				for _, m := range cd.Methods {
					if m.IsStatic {
						statics = append(statics, m)
					} else {
						members = append(members, m)
					}
				}
				var staticFields, ownFields []ast.AnnotField
				for _, f := range cd.Fields {
					if f.Static {
						staticFields = append(staticFields, f)
					} else {
						ownFields = append(ownFields, f)
					}
				}
				if len(statics) > 0 || len(staticFields) > 0 || len(cd.StaticBlocks) > 0 {
					companion := ast.NewClassDeclaration(genericStaticsClass(cd.Name), "", nil, false, nil, staticFields, nil, statics, cd.StaticBlocks, cd.GetPos())
					prog.Body = append(prog.Body, companion)
					classDeclByName[companion.Name] = companion
					e.interfaces[companion.Name] = ClassType(companion.Name, nil, nil, false)
					generic := *cd
					generic.Methods = members
					generic.Fields = ownFields
					generic.StaticBlocks = nil
					// Both stand for the declaration: a builtin module's
					// keep its module (TDD-00238).
					if key := e.libStmts[cd]; key != "" {
						e.libStmts[companion] = key
						e.libStmts[&generic] = key
					}
					// Its statics initialize where the declaration runs.
					e.staticCompanion[cd], e.staticCompanion[&generic] = companion, companion
					cd = &generic
				}
				e.genericClasses[cd.Name] = cd
				continue
			}
			classDeclByName[cd.Name] = cd
			// Placeholder: correct IR/IsObject/IsClass/ClassName, no fields
			// yet — see canonicalizeClassTy's doc comment for why this must
			// exist before any field/param/return type is resolved.
			e.interfaces[cd.Name] = ClassType(cd.Name, nil, nil, false)
		}
	}

	// Pass 0b: validate `extends` targets, detect cycles, compute a
	// base-before-derived topological order via DFS.
	var topoOrder []string
	visitState := make(map[string]int) // 0 unvisited, 1 in-progress, 2 done
	var visit func(name string) error
	visit = func(name string) error {
		cd := classDeclByName[name]
		switch visitState[name] {
		case 1:
			return internalAt(cd.GetPos(), "class '%s' has a cyclic 'extends' chain", name)
		case 2:
			return nil
		}
		visitState[name] = 1
		if e.isErrorRootKind(cd.BaseClass) {
			// A synthetic root (TDD-00155 Stage 6): Error (or a builtin kind
			// of it) is never a registered class — nothing to recurse into;
			// its fields are grafted in Pass 1.
			if len(cd.BaseTypeArgs) > 0 {
				return internalAt(cd.GetPos(), "class %s extends non-generic %s with type arguments (TS2315 first)", name, cd.BaseClass)
			}
		} else if _, user := classDeclByName[cd.BaseClass]; !user && cd.BaseClass != "" && e.hostBaseOf(cd) != nil {
			// A builtin host base (TDD-00247): never a registered class;
			// its body is grafted in Pass 1.
		} else if _, user := classDeclByName[cd.BaseClass]; !user && cd.BaseClass != "" && e.isHostLayoutBase(cd.BaseClass) {
			return internalAt(cd.GetPos(), "class '%s' extends the builtin '%s'", ast.Unmangle(name), cd.BaseClass)
		} else if cd.BaseClass != "" {
			// (Type arguments on a base that declares no type parameters
			// are TS2315; JavaScript erases them.)
			if _, ok := classDeclByName[cd.BaseClass]; !ok {
				if k, ok := e.classOfCallConst(cd.BaseClass, classDeclByName); ok {
					// A constant holding the class a function returns
					// (`const AU = A(User)`): that class.
					cd.BaseClass = k
				}
			}
			if _, ok := classDeclByName[cd.BaseClass]; !ok {
				// A base known only at run time that specializeDynamicBases
				// left as written: it can hold a class that names its own
				// function's locals, which no other scope can name.
				return unsupportedAt(cd.GetPos(), "a class extending '%s', a value that can hold a class naming its own function's locals,", ast.Unmangle(cd.BaseClass))
			}
			if err := visit(cd.BaseClass); err != nil {
				return err
			}
		}
		visitState[name] = 2
		topoOrder = append(topoOrder, name)
		return nil
	}
	for _, stmt := range prog.Body {
		if cd, ok := stmt.(*ast.ClassDeclaration); ok && len(cd.TypeParams) == 0 {
			if err := visit(cd.Name); err != nil {
				return err
			}
		}
	}

	// Pass 1: per class, topological (base before derived).
	savedInLib := e.inLib
	defer func() { e.inLib = savedInLib }()
	for _, name := range topoOrder {
		cd := classDeclByName[name]
		e.inLib = e.libStmts[cd]

		var baseInfo ClassInfo
		// `extends Error` (TDD-00155 Stage 6): Error is a synthetic root
		// whose "inherited fields" are the error struct's own — minus its
		// kind slot, which the class tag field occupies byte-for-byte. The
		// subclass instance is therefore prefix-compatible with every
		// throw/catch/message reader, and its TagID doubles as its error
		// kind (allocated in a dedicated >=1000 range).
		isErrorRoot := e.isErrorRootKind(cd.BaseClass)
		var hostBase *Type
		if _, user := classDeclByName[cd.BaseClass]; !user && !isErrorRoot && cd.BaseClass != "" {
			hostBase = e.hostBaseOf(cd)
		}
		haveBase := cd.BaseClass != "" && !isErrorRoot && hostBase == nil
		if haveBase {
			baseInfo = e.classes[cd.BaseClass]
			hostBase = baseInfo.HostBase
		}
		var hostAnnot ast.TypeNode
		if haveBase {
			hostAnnot = baseInfo.HostAnnot
		} else if hostBase != nil {
			hostAnnot = hostAnnotation(cd, *hostBase)
		}
		if hostBase != nil && !haveBase {
			baseInfo.FlatFields = hostBodyFields(*hostBase)
			baseInfo.FieldOrigin = map[string]string{}
			for _, f := range baseInfo.FlatFields {
				baseInfo.FieldOrigin[f.Name] = cd.Name
			}
		}
		// A subclass of an Error subclass is an Error subclass too.
		isErrorSub := isErrorRoot || haveBase && baseInfo.IsErrorSubclass
		if isErrorRoot {
			baseInfo.FlatFields = append([]Field{}, errorObjType.Fields[1:]...)
			baseInfo.FieldOrigin = map[string]string{}
			for _, f := range errorObjType.Fields[1:] {
				baseInfo.FieldOrigin[f.Name] = cd.Name
			}
		}

		// Flatten inherited visible fields ahead of this class's own new
		// ones; reject a new field colliding (by name) with a reserved or
		// inherited one. Static fields (f.Static) are handled entirely
		// separately below — they're never part of instance layout.
		inheritedFields := append([]Field{}, baseInfo.FlatFields...)
		seen := make(map[string]bool, len(inheritedFields))
		for _, f := range inheritedFields {
			seen[f.Name] = true
		}
		fieldOrigin := make(map[string]string, len(inheritedFields))
		for k, v := range baseInfo.FieldOrigin {
			fieldOrigin[k] = v
		}
		declareFields := make(map[string]bool, len(baseInfo.DeclareFields))
		for k, v := range baseInfo.DeclareFields {
			declareFields[k] = v
		}
		staticFieldTypes := make(map[string]Type, len(baseInfo.StaticFieldTypes))
		for k, v := range baseInfo.StaticFieldTypes {
			staticFieldTypes[k] = v
		}
		staticFieldOwner := make(map[string]string, len(baseInfo.StaticFieldOwner))
		for k, v := range baseInfo.StaticFieldOwner {
			staticFieldOwner[k] = v
		}
		ownStaticFieldTypes := make(map[string]Type)
		var ownStaticFieldOrder []string

		var ownFields []Field
		for _, f := range cd.Fields {
			if (f.Name == ClassTagField || f.Name == ClassVTableField) && !f.Static {
				return internalAt(cd.GetPos(), "class %s declares the reserved field %s", cd.Name, f.Name)
			}
			if f.Static {
				// A static field's initializer (`static x = expr`) runs in the
				// class's staticinit (emitClassStaticInit), lowered to a
				// `ClassName.x = expr` assignment ahead of any `static {}` block
				// (ADR-00375). An unannotated static field takes its type from
				// the initializer, the same inference an instance `x = expr`
				// field (line below) and a `let x = expr` use.
				var fty Type
				if f.Type != nil {
					fty = e.annotationType(f.Type)
					if f.Initializer == nil {
						// `static x: number;` is undefined until assigned
						// (no initialization check covers a static field).
						fty = undefinedableElem(fty)
					}
				} else if f.Initializer != nil {
					fty = e.typeOfExpr(f.Initializer)
					if fty.IR == "" || fty.IR == "void" {
						// An initializer of no value (a call that only
						// throws): the slot holds whatever is assigned later.
						fty = TypeAny
					}
				} else {
					fty = TypeAny // `static x;`: undefined until assigned
				}
				staticFieldTypes[f.Name] = fty
				staticFieldOwner[f.Name] = cd.Name
				ownStaticFieldTypes[f.Name] = fty
				ownStaticFieldOrder = append(ownStaticFieldOrder, f.Name)
				continue
			}
			if seen[f.Name] {
				// A subclass redeclaring an inherited field (`code: string` over
				// a base's) names the same property: it keeps the inherited
				// slot when its storage is the same. A property Node's Error
				// does not have (`code`, `errno`, …, this compiler's slots for
				// system errors) is the subclass's own field, in its
				// declaration order; the system slot steps aside.
				if i := errorSystemSlot(inheritedFields, f.Name); i >= 0 && isErrorSub {
					inheritedFields[i].Name = "__kml_err_" + f.Name
					if e.errorShadowedSlots == nil {
						e.errorShadowedSlots = map[string]bool{}
					}
					e.errorShadowedSlots[f.Name] = true
					delete(fieldOrigin, f.Name)
				} else {
					// Its type is assignable to the base's (the checker's
					// TS2416 otherwise): the inherited slot holds it, in
					// the base's representation.
					continue
				}
			}
			seen[f.Name] = true
			// An unannotated field (`x = expr`, TDD-00063 Stage 1) takes its
			// type from its initializer, the same compile-time inference a
			// `let x = expr` uses; an annotated field keeps its declared type.
			var fty Type
			if f.Type != nil {
				fty = e.annotationType(f.Type)
				// `tag?: T` widens to `T | undefined` (TDD-00187 Stage 2);
				// the instance's calloc zero reads back as absent.
				if f.Optional {
					fty = optionalFieldType(fty)
				} else if f.Initializer == nil && !f.Declare && !f.Static && !ctorAssigns(cd, f.Name) {
					// `o!: T` (or a field a method assigns): an own property
					// holding undefined until something assigns it, as in
					// Node; its reads may see undefined (unassignedField).
					fty = optionalFieldType(fty)
					e.markUnassignedField(cd.Name, f.Name)
				}
			} else if f.Initializer == nil {
				// Neither typed nor initialized (`x;`): the checker's type,
				// what the constructor assigns or else any.
				fty = e.bareFieldType(cd, f.Name)
			} else {
				// A field initializer may reference `this` (`f = Object.freeze(this)`,
				// `f = this`), whose type is the class instance itself — a
				// self-reference. Field types are collected before any constructor
				// scope exists, so `lookup("this")` would otherwise miss and default
				// the field to the numeric `i64`, then store the instance pointer
				// into that i64 slot (invalid IR). Bind `this` to the class's own
				// name-placeholder type for the duration of inference; the field
				// keeps a nominal (`ClassName`) class type whose fields
				// canonicalizeClassTy re-resolves on demand at each drilling access
				// (ADR-00946/00949, the class self-reference machinery).
				e.pushScope()
				e.define("this", Symbol{Ty: ClassType(cd.Name, nil, nil, false)})
				fty = e.typeOfExpr(f.Initializer)
				e.popScope()
				if fty.IR == "" || fty.IR == "void" {
					fty = TypeAny // an initializer of no value (a call that only throws)
				}
				// `n = this.s`: an earlier field's type (the placeholder `this`
				// has no fields yet).
				if m, ok := f.Initializer.(*ast.MemberExpression); ok {
					if _, isThis := m.Object.(*ast.ThisExpression); isThis {
						for _, of := range ownFields {
							if of.Name == m.Property {
								fty = of.Ty
							}
						}
					}
				}
				// A call of a function registered after classes (`f = mk()`,
				// `server = http.createServer(…)`) has its declared return type.
				if rt, ok := e.declaredCallReturnType(prog, f.Initializer); ok {
					fty = rt
				}
				// Evolving-any for a `null`/`undefined`-initialized unannotated
				// field, the class-field counterpart of the local-binding
				// widening (ADR-00923): if a method or the constructor later
				// reassigns `this.<field>` to a non-nullish value (or logically
				// compound-assigns it), the field is a boxed `any` slot
				// (TDD-00208) rather than a `null`-typed `ptr` — so
				// `this.#field ??= 1` stores 1 instead of a `double` into a
				// null slot (invalid IR).
				if (fty.IsNull() || fty.IsUndefined()) && e.classFieldNullEvolves(cd, f.Name) {
					fty = TypeAny
				}
				if e.jsFieldTakesOtherKind(prog, f.Name, fty) {
					fty = TypeAny
				}
			}
			if f.Type == nil && f.Optional {
				// An unannotated field a `delete` names (JavaScript): absent
				// once deleted.
				fty = optionalFieldType(fty)
			}
			// A `name?: T` field is optional: an own property from
			// construction (every declared field is), until `delete` removes
			// it (TDD-00256's presence word, set whole by emitNew).
			ownFields = append(ownFields, Field{Name: f.Name, Ty: fty, Optional: f.Optional && !f.Static})
			fieldOrigin[f.Name] = cd.Name
			if f.Declare && !f.Static {
				declareFields[f.Name] = true
			}
		}
		// JS-compat field inference (TDD-00022 sub-problem 2, `-compat=js`
		// only): a class with a constructor and no declared instance fields
		// collects them from the constructor's `this.NAME = expr` assignments
		// — the vanilla-JS class shape. See emit_classes_jsfields.go.
		if e.compatJS() && len(ownFields) == 0 && cd.Constructor != nil {
			inferred, err := e.jsInferConstructorFields(cd, seen)
			if err != nil {
				return err
			}
			for _, f := range inferred {
				if e.jsFieldTakesOtherKind(prog, f.Name, f.Ty) {
					f.Ty = TypeAny
				}
				seen[f.Name] = true
				ownFields = append(ownFields, f)
				fieldOrigin[f.Name] = cd.Name
			}
		}
		// The presence word (TDD-00256) is part of the flat layout, where
		// ClassType puts it, so a subclass inherits it at the same index.
		flatFields := withClassKeyPresence(append(append([]Field{}, inheritedFields...), ownFields...))

		// Provisional Ty for this class's own unannotated-return-type
		// inference during this pass only — HasVTable is always false here
		// (irrelevant to field name/type lookup by name, only affects a
		// hidden field's presence); Pass 3 rebuilds the real Ty once
		// HasVTable is known and publishes it into e.interfaces/info.Ty.
		provisionalTy := ClassType(cd.Name, inheritedFields, ownFields, false)
		e.interfaces[cd.Name] = provisionalTy

		ancestorChain := append([]string{}, baseInfo.AncestorChain...)
		rootClass := cd.Name
		if haveBase {
			ancestorChain = append(ancestorChain, cd.BaseClass)
			rootClass = baseInfo.RootClass
		}

		info := ClassInfo{
			BaseClass:               e.classBase(cd),
			AncestorChain:           ancestorChain,
			RootClass:               rootClass,
			InheritedFields:         inheritedFields,
			OwnFields:               ownFields,
			FlatFields:              flatFields,
			Methods:                 make(map[string]*ast.FunctionDeclaration),
			MethodSigs:              make(map[string]FuncSig),
			GenMethodInfo:           make(map[string]*GeneratorInfo),
			MethodImplementor:       make(map[string]string),
			MethodDispatchSlot:      make(map[string]*MethodSlot),
			TagID:                   e.classTypeID(cd.Name),
			IsErrorSubclass:         isErrorSub,
			HostBase:                hostBase,
			HostAnnot:               hostAnnot,
			ErrorBaseKind:           errorBaseKind(isErrorRoot, cd.BaseClass),
			IsAbstract:              cd.IsAbstract,
			Implements:              cd.Implements,
			FieldOrigin:             fieldOrigin,
			DeclareFields:           declareFields,
			StaticFieldTypes:        staticFieldTypes,
			OwnStaticFieldTypes:     ownStaticFieldTypes,
			OwnStaticFieldOrder:     ownStaticFieldOrder,
			StaticFieldOwner:        staticFieldOwner,
			StaticMethodSigs:        make(map[string]FuncSig),
			StaticMethodImplementor: make(map[string]string),
		}
		// An error subclass's TagID doubles as its runtime error kind — the
		// >=1000 range keeps it disjoint from the builtin kinds (0–8) so a
		// caught instance never satisfies `instanceof TypeError` and friends.
		// A library error class's kind is stable across programs (TDD-00238).
		if isErrorSub && ast.IsLibraryName(cd.Name) {
			info.TagID = e.stableTypeID("E"+cd.Name) & kmlHdrIDMask
		} else if isErrorSub {
			info.TagID = errorSubclassTagBase + e.errSubCount
			e.errSubCount++
		}
		if !isErrorRoot && isErrorSub {
			info.ErrorBaseKind = baseInfo.ErrorBaseKind
		}

		if haveBase {
			for mname, sig := range baseInfo.MethodSigs {
				info.MethodSigs[mname] = sig
				info.MethodImplementor[mname] = baseInfo.MethodImplementor[mname]
				info.MethodDispatchSlot[mname] = baseInfo.MethodDispatchSlot[mname]
				info.Methods[mname] = baseInfo.Methods[mname]
			}
			// A generator method (TDD-00063 Stage 2b) is inherited by name too
			// — a subclass calling it constructs the same generator instance,
			// dispatched by the base's own GeneratorInfo (its body func and
			// receiver binding are the base's).
			for mname, gi := range baseInfo.GenMethodInfo {
				info.GenMethodInfo[mname] = gi
			}
			info.MethodOrder = append(info.MethodOrder, baseInfo.MethodOrder...)
			for mname, sig := range baseInfo.StaticMethodSigs {
				info.StaticMethodSigs[mname] = sig
				info.StaticMethodImplementor[mname] = baseInfo.StaticMethodImplementor[mname]
			}
		}

		// Publish the (still-being-populated) ClassInfo now, before the per-method
		// loop that infers each unannotated return type. MethodSigs is a map, so the
		// stored copy shares it by reference — a method whose body calls an
		// EARLIER-declared sibling (`method() { return this.#m(); }`) can then
		// resolve that sibling's return type during inference instead of falling back
		// to the i64 default. Without this e.classes[cd.Name] was empty until after
		// the loop, so `this.#m()` inferred i64 and a string-returning private method
		// emitted `ret ptr` in an i64 function (invalid IR). ADR-00906. The final
		// store below re-publishes the completed struct (same maps).
		e.classes[cd.Name] = info
		e.classDeclName[cd] = cd.Name
		e.classSrc[cd.Name] = cd.Src
		// A method may call a sibling declared after it (`method() { return
		// this.m(); }` before `m() { … }`), whose signature the loop below
		// has not registered yet. Infer every unannotated method's result
		// first, into a side table inference reads for a method not yet
		// registered, until no result changes (a chain of calls).
		e.inferForwardMethodResults(cd, provisionalTy)
		defer e.dropForwardMethodResults(cd.Name)
		ownDeclared := make(map[string]bool, len(cd.Methods))
		ownStaticDeclared := make(map[string]bool, len(cd.Methods))
		for _, m := range cd.Methods {
			padOverrideParams(m, info.Methods[m.Name])
			e.adaptOverrideParams(m, info.Methods[m.Name])
			if m.AccessorKind == "" {
				// A method reading `arguments` takes its implicit rest, as a
				// function does (TDD-00210).
				maybeAddArgumentsRestParam(m)
			}
			sig := e.methodSig(cd, m, provisionalTy)

			// Generator method (TDD-00063 Stage 2b): calling `obj.m(...)`
			// constructs a generator instance rather than running the body, so
			// its registered return type is the generator instance type, and
			// its GeneratorInfo (body func name, element/param types, receiver
			// binding) is recorded for emitClassCall's own construction path.
			// V1 scope: instance methods only.
			if m.IsGenerator {
				if m.IsStatic {
					return internalAt(m.GetPos(), "a static generator method in a class declared inside a function, or reading `arguments`, is not yet supported ('%s' on class '%s')", m.Name, cd.Name)
				}
				if m.Body == nil {
					return internalAt(m.GetPos(), "an abstract generator method ('%s' on class '%s') is not supported", m.Name, cd.Name)
				}
				genInfo, gerr := e.buildGeneratorMethodInfo(m, cd.Name, provisionalTy)
				if gerr != nil {
					return gerr
				}
				info.GenMethodInfo[m.Name] = genInfo
				sig.RetType = genInfo.GenTy
			}

			if m.AccessorKind != "" {
				// TDD-00030: a getter/setter is registered as an ordinary
				// method under a space-mangled dispatch key ("get x"/
				// "set x" — a real identifier can never contain a space,
				// so this can never collide with a genuine user-declared
				// method name), which is what lets every other piece of
				// method machinery below (inheritance, override/vtable
				// analysis, visibility, abstract-completeness) apply with
				// zero changes of its own.
				nParams := len(m.Params)
				if takesStaticThis(m) {
					nParams-- // its receiver (staticThisToClass)
				}
				if m.AccessorKind == "get" {
					if nParams != 0 {
						return internalAt(m.GetPos(), "getter '%s' on class '%s' must take no parameters", m.Name, cd.Name)
					}
					if sig.RetType.IR == "void" {
						// A getter falling off its end, or one that only
						// throws (the checker reports one TypeScript requires
						// to return): undefined.
						sig.RetType = TypeAny
					} else if e.compatJS() && (sig.RetType.IsNull() || sig.RetType.IsUndefined()) && m.ReturnType == nil {
						// One returning only null or undefined: the property
						// holds any value its setter is given (`??=`).
						sig.RetType = TypeAny
					}
				} else { // "set"
					if nParams != 1 {
						return internalAt(m.GetPos(), "setter '%s' on class '%s' must take exactly one parameter", m.Name, cd.Name)
					}
					// A setter's return value is always discarded, matching
					// real JS — regardless of any declared/inferred return.
					sig.RetType = TypeVoid
				}
				if m.IsStatic {
					// A static accessor: a static method under the same
					// space-mangled key, called where `C.x` is read or
					// assigned (emitStaticFieldRead/Assign).
					mangled := accessorMethodName(m.AccessorKind, m.Name)
					if _, ok := info.StaticFieldTypes[m.Name]; ok {
						return internalAt(m.GetPos(), "class '%s' cannot declare static accessor '%s' — a static field with that name already exists", cd.Name, m.Name)
					}
					if ownStaticDeclared[mangled] {
						return internalAt(m.GetPos(), "class '%s' declares more than one static %s accessor for '%s'", cd.Name, m.AccessorKind, m.Name)
					}
					ownStaticDeclared[mangled] = true
					info.StaticMethodSigs[mangled] = sig
					info.StaticMethodImplementor[mangled] = cd.Name
					continue
				}

				// Mutual exclusion: an accessor name can't collide with a
				// plain field or a plain method (own or inherited) under
				// the same, unmangled property name.
				if _, _, ok := provisionalTy.FieldIndex(m.Name); ok {
					return internalAt(m.GetPos(), "class '%s' cannot declare accessor '%s' — a field with that name already exists", cd.Name, m.Name)
				}
				if _, ok := info.MethodSigs[m.Name]; ok {
					return internalAt(m.GetPos(), "class '%s' cannot declare accessor '%s' — a method with that name already exists", cd.Name, m.Name)
				}

				mangled := accessorMethodName(m.AccessorKind, m.Name)
				otherKind := "set"
				if m.AccessorKind == "set" {
					otherKind = "get"
				}
				if otherSig, ok := info.MethodSigs[accessorMethodName(otherKind, m.Name)]; ok {
					var getterRet, setterParam Type
					if m.AccessorKind == "get" {
						getterRet, setterParam = sig.RetType, otherSig.ParamTypes[0]
					} else {
						getterRet, setterParam = otherSig.RetType, sig.ParamTypes[0]
					}
					// An unannotated setter parameter has the getter's type, as
					// in TypeScript.
					if getterRet.IR != setterParam.IR && getterRet.IR != "void" {
						if setter := accessorDecl(cd, "set", m.Name, false); e.compatJS() && setterParam.IsDynamic() && setter != nil && len(setter.Params) == 1 && setter.Params[0].Type == nil {
							// JavaScript: the setter takes any value, and the
							// getter's result is read as one.
							if m.AccessorKind == "get" {
								sig.RetType = setterParam
							} else {
								otherSig.RetType = setterParam
								info.MethodSigs[accessorMethodName(otherKind, m.Name)] = otherSig
							}
							getterRet = setterParam
						} else if setter != nil && len(setter.Params) == 1 && setter.Params[0].Type == nil {
							if m.AccessorKind == "set" {
								sig.ParamTypes[0] = getterRet
							} else {
								otherSig.ParamTypes[0] = getterRet
								info.MethodSigs[accessorMethodName(otherKind, m.Name)] = otherSig
							}
							setterParam = getterRet
						}
					}
				}

				if ownDeclared[mangled] {
					return internalAt(m.GetPos(), "class '%s' declares more than one %s accessor for '%s'", cd.Name, m.AccessorKind, m.Name)
				}
				ownDeclared[mangled] = true

				if existingSig, overriding := info.MethodSigs[mangled]; overriding {
					if !sigCompatible(existingSig, sig) {
						return internalAt(m.GetPos(), "accessor '%s' on class '%s' overrides an inherited accessor with an incompatible signature", m.Name, cd.Name)
					}
					slot := info.MethodDispatchSlot[mangled]
					if slot == nil {
						slot = &MethodSlot{Name: mangled}
						info.MethodDispatchSlot[mangled] = slot
					}
					slot.Virtual = true
				} else {
					info.MethodDispatchSlot[mangled] = &MethodSlot{Name: mangled, Virtual: e.libStmts[cd] != ""}
					info.MethodOrder = append(info.MethodOrder, mangled)
				}
				info.MethodImplementor[mangled] = cd.Name
				info.MethodSigs[mangled] = sig
				info.Methods[mangled] = m
				continue
			}

			// Generator method (TDD-00063 Stage 2b): registered like a plain
			// method (name, sig, implementor, visibility) so dispatch-key and
			// visibility checks work, but deliberately given NO vtable slot —
			// emitClassCall intercepts it (via GenMethodInfo) and constructs a
			// generator instance, so it is never dispatched through a
			// @Class_method symbol or a vtable entry (its real body is the
			// @__generator_method_* function). Claiming a slot would make the
			// vtable emitter reference a symbol that does not exist.
			if m.IsGenerator {
				if ownDeclared[m.Name] {
					return internalAt(m.GetPos(), "class '%s' declares more than one method named '%s'", cd.Name, m.Name)
				}
				ownDeclared[m.Name] = true
				info.MethodImplementor[m.Name] = cd.Name
				info.MethodSigs[m.Name] = sig
				info.Methods[m.Name] = m
				continue
			}

			if m.IsStatic {
				if ownStaticDeclared[m.Name] {
					return internalAt(m.GetPos(), "class '%s' declares more than one static method named '%s'", cd.Name, m.Name)
				}
				ownStaticDeclared[m.Name] = true
				info.StaticMethodSigs[m.Name] = sig
				info.StaticMethodImplementor[m.Name] = cd.Name
				continue
			}

			if ownDeclared[m.Name] {
				return internalAt(m.GetPos(), "class '%s' declares more than one method named '%s'", cd.Name, m.Name)
			}
			ownDeclared[m.Name] = true
			// Symmetric to the accessor branch's own field/method collision
			// check above: a plain method can't reuse a name already
			// claimed by an accessor (own-earlier-in-this-class, or
			// inherited — both already live in info.MethodSigs by now).
			if _, ok := info.MethodSigs[accessorMethodName("get", m.Name)]; ok {
				return internalAt(m.GetPos(), "class '%s' cannot declare method '%s' — a getter with that name already exists", cd.Name, m.Name)
			}
			if _, ok := info.MethodSigs[accessorMethodName("set", m.Name)]; ok {
				return internalAt(m.GetPos(), "class '%s' cannot declare method '%s' — a setter with that name already exists", cd.Name, m.Name)
			}

			if existingSig, overriding := info.MethodSigs[m.Name]; overriding {
				if !sigCompatible(existingSig, sig) {
					needed, supported := funcAdapterPlan(overrideFuncType(sig), funcTypeFromSig(existingSig))
					if !needed || !supported || (m.IsAsync && !trailingOptionalOnly(overrideFuncType(sig), funcTypeFromSig(existingSig))) || existingSig.This || sig.This {
						return internalAt(m.GetPos(), "method '%s' on class '%s' overrides an inherited method with an incompatible signature", m.Name, cd.Name)
					}
					if info.OverrideSigs == nil {
						info.OverrideSigs = map[string]FuncSig{}
					}
					info.OverrideSigs[m.Name] = sig
					sig = existingSig
				}
				slot := info.MethodDispatchSlot[m.Name]
				if slot == nil {
					slot = &MethodSlot{Name: m.Name}
					info.MethodDispatchSlot[m.Name] = slot
				}
				slot.Virtual = true
			} else {
				// A builtin module's method may be overridden by a subclass
				// the module never sees: always dispatched (TDD-00238).
				info.MethodDispatchSlot[m.Name] = &MethodSlot{Name: m.Name, Virtual: e.libStmts[cd] != ""}
				info.MethodOrder = append(info.MethodOrder, m.Name)
			}
			info.MethodImplementor[m.Name] = cd.Name
			info.MethodSigs[m.Name] = sig
			info.Methods[m.Name] = m
		}

		// Abstract-completeness check (TDD-00009 Stage 4): a concrete
		// (non-abstract) class must not leave any inherited-or-own method
		// as a bare signature (Body == nil) — every abstract method
		// somewhere in its chain must have a real override by the time a
		// class can actually be instantiated. An abstract class is exempt
		// (that's the whole point — it's allowed to leave stubs open for
		// its own subclasses).

		// Constructor rules (TDD-00009 Stage 3): see hasTopLevelSuperCall's
		// doc comment for why the super()-presence check is shallow.
		var baseCtor *ast.FunctionDeclaration
		var baseCtorSig FuncSig
		if haveBase {
			baseCtor = baseInfo.Constructor
			baseCtorSig = baseInfo.CtorSig
		}
		switch {
		case cd.Constructor != nil:
			if cd.BaseClass != "" && !isErrorRoot && !containsSuperCall(cd.Constructor.Body) {
				guardMissingSuper(cd.Constructor.Body)
			}
			if n := countSuperCalls(cd.Constructor.Body); cd.BaseClass != "" && (n > 1 || n == 1 && !hasTopLevelSuperCall(cd.Constructor.Body)) {
				guardRepeatedSuper(cd.Constructor.Body)
			}
			callsSuper := hasTopLevelSuperCall(cd.Constructor.Body)
			nestedSuper := cd.BaseClass != "" && !callsSuper && containsSuperCall(cd.Constructor.Body)
			if nestedSuper {
				// `const r = super()`, `super()?.a`, `if (c) super()`: each
				// call runs the own field initializers right after it.
				inlineFieldInitsAtSuper(cd)
				callsSuper = true
			}
			// A derived class must call super() (TS2377) even when its base
			// has no constructor of its own: that call is then a no-op
			// (emitSuperCall).
			if baseCtor == nil && callsSuper && !isErrorRoot && !haveBase && hostBase == nil {
				return internalAt(cd.Constructor.GetPos(), "constructor of class '%s' calls super(...) but the class has no base class", cd.Name)
			}
			info.Constructor = cd.Constructor
			// One reading `arguments` takes its implicit rest, as a method does.
			maybeAddArgumentsRestParam(cd.Constructor)
			sig := e.buildParamSig(cd.Constructor.Params, cd.Constructor)
			sig.RetType = TypeVoid
			// `-compat=js`: overlay call-site-inferred types onto unannotated
			// constructor parameters (TDD-00022 sub-problem 1, ctor slice).
			if e.compatJS() {
				e.jsApplyCtorParamTypes(cd.Constructor, &sig)
			}
			info.CtorSig = sig
			// TDD-00063 Stage 1: splice own field initializers in right after
			// super() returns (`this` now exists), or at the top for a base
			// class, before the rest of the constructor body runs.
			if inits := classFieldInitStmts(cd); len(inits) > 0 && !nestedSuper {
				at := topLevelSuperCallIndex(cd.Constructor.Body) + 1 // -1 → 0 when there is no super()
				stmts := cd.Constructor.Body.Body
				spliced := make([]ast.Statement, 0, len(stmts)+len(inits))
				spliced = append(spliced, stmts[:at]...)
				spliced = append(spliced, inits...)
				spliced = append(spliced, stmts[at:]...)
				cd.Constructor.Body.Body = spliced
			}

		case (len(ownFields) > 0 || len(classFieldInitStmts(cd)) > 0 || (e.standardDecorators() && classHasStandardDecorators(cd))):
			// A standard-decorated class needs a constructor even with no fields,
			// so its per-instance decorator effects (field-init transforms,
			// addInitializer callbacks) run in the constructor tail (TDD-00161
			// Stage 5).
			// TDD-00063 Stage 1 (relaxed, ADR-00373): a class with own fields
			// and no explicit constructor no longer needs *every* field to carry
			// an initializer — synthesize a constructor that runs whatever
			// initializers exist (`classFieldInitStmts` skips uninitialized
			// fields) and leaves the rest at their calloc'd deterministic-zero
			// value (the same ADR-00157 under-assigned-field convention a class
			// *with* a constructor already relies on). This accepts the very
			// common `class C { x: number }` bare-field-declaration form. The
			// synthesized ctor forwards to super(...) first when the base has a
			// constructor (the pass-through shape the case below builds, with the
			// initializers appended).
			var stmts []ast.Statement
			var params []ast.Param
			if baseCtor == nil && hostBase != nil && !haveBase && hostBase.IsPromise() {
				// Promise's constructor, forwarded: `(executor)`.
				params = []ast.Param{{Name: "executor", Type: promiseExecutorAnnotation(cd)}}
				superCall := ast.NewCallExpression(ast.NewSuperExpression(cd.GetPos()), []ast.Expression{ast.NewIdentifier("executor", cd.GetPos())}, cd.GetPos())
				stmts = append(stmts, ast.NewExpressionStatement(superCall, cd.GetPos()))
			} else if baseCtor == nil && hostBase != nil && !haveBase && (hostBase.IsDate() || hostBase.IsArray()) {
				// Date's arguments set the body at construction (emitNew);
				// this constructor runs the initializers alone.
			} else if baseCtor == nil && hostBase != nil && !haveBase && hostBase.IsRegExp() {
				// RegExp's constructor, forwarded: `(pattern?, flags?)`.
				params = []ast.Param{
					{Name: "pattern", Type: ast.AnyAnnotation("ts"), Optional: true},
					{Name: "flags", Type: nameAnnotation("string"), Optional: true},
				}
				superCall := ast.NewCallExpression(ast.NewSuperExpression(cd.GetPos()), []ast.Expression{ast.NewIdentifier("pattern", cd.GetPos()), ast.NewIdentifier("flags", cd.GetPos())}, cd.GetPos())
				stmts = append(stmts, ast.NewExpressionStatement(superCall, cd.GetPos()))
			} else if baseCtor == nil && hostBase != nil && !haveBase {
				// Map's or Set's constructor, forwarded: `(entries?)`.
				params = []ast.Param{{Name: "entries", Type: hostEntriesAnnotation(cd), Optional: true}}
				superCall := ast.NewCallExpression(ast.NewSuperExpression(cd.GetPos()), []ast.Expression{ast.NewIdentifier("entries", cd.GetPos())}, cd.GetPos())
				stmts = append(stmts, ast.NewExpressionStatement(superCall, cd.GetPos()))
			} else if baseCtor == nil && isErrorRoot {
				// A builtin Error's constructor, forwarded: `(message?, options?)`,
				// DOMException's `(message?, name?)`.
				second := ast.Param{Name: "options", Type: ast.AnyAnnotation("ts"), Optional: true}
				if cd.BaseClass == "DOMException" {
					second = ast.Param{Name: "name", Type: nameAnnotation("string"), Optional: true}
				}
				params = []ast.Param{{Name: "message", Type: nameAnnotation("string"), Optional: true}, second}
				superCall := ast.NewCallExpression(ast.NewSuperExpression(cd.GetPos()), []ast.Expression{ast.NewIdentifier("message", cd.GetPos()), ast.NewIdentifier(second.Name, cd.GetPos())}, cd.GetPos())
				stmts = append(stmts, ast.NewExpressionStatement(superCall, cd.GetPos()))
			} else if baseCtor != nil {
				params = make([]ast.Param, len(baseCtorSig.ParamNames))
				superArgs := make([]ast.Expression, len(baseCtorSig.ParamNames))
				for i, pname := range baseCtorSig.ParamNames {
					params[i] = ast.Param{Name: pname}
					superArgs[i] = ast.NewIdentifier(pname, cd.GetPos())
					if baseCtorSig.HasRest && i == len(baseCtorSig.ParamNames)-1 {
						params[i].Rest = true
						superArgs[i] = ast.NewSpreadElement(superArgs[i], cd.GetPos())
					}
				}
				superCall := ast.NewCallExpression(ast.NewSuperExpression(cd.GetPos()), superArgs, cd.GetPos())
				stmts = append(stmts, ast.NewExpressionStatement(superCall, cd.GetPos()))
			}
			stmts = append(stmts, classFieldInitStmts(cd)...)
			body := ast.NewBlockStatement(stmts, cd.GetPos())
			info.Constructor = &ast.FunctionDeclaration{Name: "constructor", Params: params, Body: body}
			info.ImplicitCtor = true
			if baseCtor != nil {
				info.CtorSig = baseCtorSig
			} else {
				sig := e.buildParamSig(params, nil)
				sig.RetType = TypeVoid
				info.CtorSig = sig
			}

		case len(ownFields) == 0 && baseCtor != nil:
			// Implicit pass-through constructor: `constructor(...args) {
			// super(...args) }`, forwarding every base parameter 1:1 — only
			// needed when this class adds no fields of its own. A base whose
			// constructor takes a rest parameter (or reads `arguments`) gets
			// its rest spread back into super().
			params := make([]ast.Param, len(baseCtorSig.ParamNames))
			superArgs := make([]ast.Expression, len(baseCtorSig.ParamNames))
			for i, pname := range baseCtorSig.ParamNames {
				params[i] = ast.Param{Name: pname}
				superArgs[i] = ast.NewIdentifier(pname, cd.GetPos())
				if baseCtorSig.HasRest && i == len(baseCtorSig.ParamNames)-1 {
					params[i].Rest = true
					superArgs[i] = ast.NewSpreadElement(superArgs[i], cd.GetPos())
				}
			}
			superCall := ast.NewCallExpression(ast.NewSuperExpression(cd.GetPos()), superArgs, cd.GetPos())
			body := ast.NewBlockStatement([]ast.Statement{ast.NewExpressionStatement(superCall, cd.GetPos())}, cd.GetPos())
			info.Constructor = &ast.FunctionDeclaration{Name: "constructor", Params: params, Body: body}
			info.ImplicitCtor = true
			info.CtorSig = baseCtorSig

		}

		e.classes[cd.Name] = info
		e.classDeclName[cd] = cd.Name
		e.classSrc[cd.Name] = cd.Src
	}

	// Pass 1.5: Descendants is the inverse of AncestorChain, needed by
	// instanceof's dynamic (any/unknown) case.
	descendants := make(map[string][]string)
	for _, name := range topoOrder {
		for _, anc := range e.classes[name].AncestorChain {
			descendants[anc] = append(descendants[anc], name)
		}
	}
	for _, name := range topoOrder {
		info := e.classes[name]
		info.Descendants = descendants[name]
		e.classes[name] = info
	}

	// Pass 2: number the Virtual slots and decide HasVTable — deferred until
	// every class's own override analysis (Pass 1) is done, since a deep
	// override can retroactively mark an ancestor's slot Virtual after that
	// ancestor was itself already processed. A class numbers the slots it
	// introduces after its base's, as single inheritance does, so its slots
	// depend only on its ancestors: a builtin module's class has the same
	// slots in every program, whatever subclasses the program declares
	// (TDD-00238). HasVTable is uniform across a tree (the field layout
	// shares the vtable field); a tree rooted in a builtin module always
	// has one, since its layout cannot depend on the program's subclasses.
	numbered := map[*MethodSlot]bool{}
	treeHasVTable := map[string]bool{}
	for _, name := range topoOrder {
		info := e.classes[name]
		size := 0
		if base, ok := e.classes[info.BaseClass]; ok && info.BaseClass != "" {
			size = base.VTableSize
		}
		for _, mname := range info.MethodOrder {
			slot := info.MethodDispatchSlot[mname]
			if slot != nil && slot.Virtual && !numbered[slot] {
				numbered[slot] = true
				slot.Index = size
				size++
			}
		}
		info.VTableSize = size
		e.classes[name] = info
		if size > 0 || e.libStmts[classDeclByName[info.RootClass]] != "" {
			treeHasVTable[info.RootClass] = true
		}
	}
	for _, name := range topoOrder {
		info := e.classes[name]
		if treeHasVTable[info.RootClass] {
			info.HasVTable = true
			e.classes[name] = info
		}
	}

	// Pass 3: finalize each class's real Ty now that HasVTable is known,
	// and publish it into e.interfaces so every other type-resolution call
	// site (unchanged since before Stage 3) sees the final shape.
	for _, name := range topoOrder {
		info := e.classes[name]
		info.Ty = ClassType(name, info.InheritedFields, info.OwnFields, info.HasVTable)
		e.classes[name] = info
		e.interfaces[name] = info.Ty
	}
	for _, name := range topoOrder {
		if cd := classDeclByName[name]; cd != nil && cd.LowerType != "" {
			e.emitLowerTypeLayout(cd, name, e.classes[name])
		}
	}

	return nil
}

// emitClassDecl emits one class's constructor (if declared or synthesized —
// see registerClasses' constructor rules) and every own-declared method as
// ordinary LLVM functions named "@ClassName__kml_ctor" /
// "@ClassName_methodName". A method inherited-but-not-overridden gets no
// function of its own here — calls to it resolve (via emitClassCall) to its
// nearest ancestor's own function instead.
func (e *Emitter) emitClassDecl(cd *ast.ClassDeclaration) error {
	info := e.classes[cd.Name]
	// TDD-00161: class decorators run on a non-generic class (Stage 4). A
	// generic class's per-instantiation monomorphization has no single
	// decorator target, so a decorated generic class is a clean rejection.
	if len(cd.Decorators) > 0 && len(cd.TypeParams) > 0 {
		return internalAt(cd.GetPos(), "decorated generic class (the resolver rejects it)")
	}
	for _, m := range cd.Methods {
		if len(m.Decorators) == 0 || methodDecoratorSupported(cd, m) {
			continue
		}
		// The standard dialect additionally supports instance getter/setter
		// decorators (routed through the accessor slot).
		if e.standardDecorators() && m.AccessorKind != "" && !m.IsStatic && len(cd.TypeParams) == 0 {
			continue
		}
		if e.standardDecorators() && staticMethodDecoratorSupported(cd, m) {
			continue
		}
		if len(cd.TypeParams) == 0 {
			return internalAt(m.GetPos(), "decorated member %s", m.Name)
		}
		return internalAt(m.GetPos(), "decorated generic class method (the resolver rejects it)")
	}
	if len(cd.AutoAccessors) > 0 && !e.standardDecorators() {
		return internalAt(cd.GetPos(), "auto-accessor under experimental decorators")
	}
	if info.Constructor != nil {
		llvmName := cd.Name + ctorSymbolSuffix
		e.currentCtorClass, e.overrideThis, e.overrideThisStatic = cd.Name, "", nil
		e.memberFrame.node, e.memberFrame.name = cd, "new "+ast.Unmangle(cd.Name)
		err := e.emitClassMember(llvmName, info.Ty, info.Constructor.Params, info.CtorSig, info.Constructor.Body, TypeVoid, info.Constructor.GetPos(), false, false)
		e.currentCtorClass, e.overrideThis, e.overrideThisStatic = "", "", nil
		if err != nil {
			return err
		}
	}
	for _, m := range cd.Methods {
		// An abstract method (Body == nil) has nothing to emit — see
		// registerClasses' abstract-completeness check for why every
		// concrete class is guaranteed to have a real override elsewhere
		// by the time any of *its* methods reach this loop.
		if m.Body == nil {
			continue
		}
		// TDD-00063 Stage 2b: a generator method emits its own fiber-backed
		// body (via the shared generator machinery), binding `this` from the
		// receiver stored at construction — not an ordinary method function. An
		// `async *m()` (TDD-00085 Stage 4) rides the same path: emitGeneratorFunctionDecl
		// handles the async fiber (yield + await + Promise<{value,done}> .next())
		// off the method's GeneratorInfo.IsAsync flag.
		if m.IsGenerator {
			genInfo := info.GenMethodInfo[m.Name]
			genInfo.ThisTy = &info.Ty // final class type (registration used the pre-vtable provisional)
			if err := e.emitGeneratorFunctionDecl(m, genInfo); err != nil {
				return err
			}
			continue
		}
		if m.IsStatic {
			key := m.Name
			if m.AccessorKind != "" {
				key = accessorMethodName(m.AccessorKind, m.Name)
			}
			sig := info.StaticMethodSigs[key]
			llvmName := llvmSafeSymbol(staticMethodName(cd.Name, key))
			e.memberFrame.node, e.memberFrame.name = cd, ast.Unmangle(cd.Name)+"."+m.Name
			if err := e.emitClassMember(llvmName, info.Ty, m.Params, sig, m.Body, sig.RetType, m.GetPos(), true, m.IsAsync); err != nil {
				return err
			}
			continue
		}
		// TDD-00030: an accessor's real dispatch key (and therefore its
		// emitted LLVM function name) is accessorMethodName(...), not the
		// plain source property name — must match what emitClassCall
		// constructs its call sites against.
		methodKey := m.Name
		if m.AccessorKind != "" {
			methodKey = accessorMethodName(m.AccessorKind, m.Name)
		}
		sig := info.MethodSigs[methodKey]
		llvmName := llvmSafeSymbol(cd.Name + "_" + methodKey)
		if own, ok := info.OverrideSigs[methodKey]; ok && info.MethodImplementor[methodKey] == cd.Name {
			// The body under its own signature; the method's symbol adapts
			// the inherited signature's arguments to it.
			implName := llvmName + "__kml_ovr"
			e.memberFrame.node, e.memberFrame.name = cd, ast.Unmangle(cd.Name)+"."+m.Name
			if err := e.emitClassMember(implName, info.Ty, m.Params, own, m.Body, own.RetType, m.GetPos(), false, m.IsAsync); err != nil {
				return err
			}
			callee := func() (string, string) { return "@" + implName, "%env" }
			if _, ok := e.emitAdapterFunc("@"+llvmName, overrideFuncType(own), funcTypeFromSig(sig), callee); !ok {
				return internalAt(m.GetPos(), "method '%s' on class '%s' overrides an inherited method with an incompatible signature", m.Name, cd.Name)
			}
			continue
		}
		e.memberFrame.node, e.memberFrame.name = cd, ast.Unmangle(cd.Name)+"."+m.Name
		if err := e.emitClassMember(llvmName, info.Ty, m.Params, sig, m.Body, sig.RetType, m.GetPos(), false, m.IsAsync); err != nil {
			return err
		}
	}
	return nil
}

// emitClassStaticFieldGlobals emits `@ClassName_static_name = global TY
// zeroinitializer` for every static field this class itself declares
// (TDD-00009 Stage 4) — an inherited, non-redeclared static field shares
// its base's global instead (see StaticFieldOwner), so nothing is emitted
// for it here.
func (e *Emitter) emitClassStaticFieldGlobals(className string) {
	if e.recordStaticClass(className) {
		return // per evaluation, in its records' areas (TDD-00242)
	}
	info := e.classes[className]
	// Sorted: map order would make the emitted IR differ from build to build.
	names := make([]string, 0, len(info.OwnStaticFieldTypes))
	for name := range info.OwnStaticFieldTypes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		ty := info.OwnStaticFieldTypes[name]
		e.emitGlobal(fmt.Sprintf("@%s = %sglobal %s zeroinitializer, align %d", llvmSafeSymbol(className+"_static_"+name), e.isolateTLS(), storageIR(ty), ty.Align()))
	}
	// An inherited one: the class's own copy once assigned, and whether it
	// has been.
	var inherited []string
	for name := range info.StaticFieldTypes {
		if _, own := info.OwnStaticFieldTypes[name]; !own {
			inherited = append(inherited, name)
		}
	}
	sort.Strings(inherited)
	for _, name := range inherited {
		ty := info.StaticFieldTypes[name]
		e.emitGlobal(fmt.Sprintf("@%s = %sglobal %s zeroinitializer, align %d", llvmSafeSymbol(className+"_static_"+name), e.isolateTLS(), storageIR(ty), ty.Align()))
		e.emitGlobal(fmt.Sprintf("@%s = %sglobal i1 false, align 1", llvmSafeSymbol(className+"_static_"+name+"__own"), e.isolateTLS()))
	}
}

// emitClassStaticInit emits `@ClassName_staticinit()`, running every
// static {} block this class declares, concatenated in declaration order
// (TDD-00009 Stage 4) — called once from EmitProgram's Pass 3, before any
// top-level statement runs. A static context: no "this"/"super" (there is
// no receiver).
func (e *Emitter) emitClassStaticInit(cd *ast.ClassDeclaration) error {
	restoreBuilder := e.beginThunkEmit()
	// A fresh context: none of the enclosing function's state.
	savedCtx := e.funcContext
	e.funcContext = &funcContext{
		currentCtorClass: savedCtx.currentCtorClass, // set by the caller for a constructor
		inModuleTask:     savedCtx.inModuleTask,
		memberClass:      cd.Name,
	}
	defer func() { e.funcContext = savedCtx }()
	if mf := e.memberFrame; mf.node != nil {
		e.memberFrame.node = nil
		e.enterFrame(mf.node, mf.name)
	}
	e.currentRetType = TypeVoid
	e.pushScope()
	e.bindStaticReceiver(cd.Name)

	// Static field initializers run first, in declaration order (ADR-00375),
	// then the `static {}` block(s). A class mixing the two runs all field
	// initializers ahead of any block rather than interleaving by exact source
	// position (an AnnotField carries no position) — a rare combination.
	for _, stmt := range classStaticFieldInitStmts(cd) {
		if err := e.emitStmt(stmt); err != nil {
			return err
		}
	}
	for _, block := range cd.StaticBlocks {
		for _, stmt := range block.Body {
			if err := e.emitStmt(stmt); err != nil {
				return err
			}
		}
	}
	e.emitTerminator("ret void")

	llvmName := cd.Name + "_staticinit"
	e.writeFunc(fmt.Sprintf("void @%s()", llvmName), e.allocas.String()+e.body.String())

	restoreBuilder()
	e.blockDone = false

	return nil
}

// emitClassVTable emits `@ClassName_vtable = global [N x ptr] [...]` for a
// class in a HasVTable tree (TDD-00009 Stage 3) — one entry per globally
// (per-tree) assigned slot index, pointing at whichever concrete function
// this class would actually run for that slot (its own override if it has
// one, else the nearest ancestor's implementation), or `ptr null` for a
// slot this class's own available methods never reach (never called, since
// static method-name lookup against this class's own type would already
// reject a call to a method it doesn't have). A no-op for a class outside
// any HasVTable tree. An abstract class has one too, since `new` through
// any constructs it (emitDynConstructDispatch); a slot of an abstract
// method nothing in its chain implements stays null, as an optional one's.
func (e *Emitter) emitClassVTable(className string) {
	info := e.classes[className]
	if !info.HasVTable {
		return
	}
	slots := make([]string, info.VTableSize)
	for i := range slots {
		slots[i] = irPtr + " null"
	}
	for _, mname := range info.MethodOrder {
		slot := info.MethodDispatchSlot[mname]
		if slot == nil || !slot.Virtual {
			continue
		}
		if m := info.Methods[mname]; m != nil && m.Body == nil {
			continue // an optional or abstract method no class in the chain implements
		}
		slots[slot.Index] = fmt.Sprintf(irPtr+" @%s", llvmSafeSymbol(info.MethodImplementor[mname]+"_"+mname))
	}
	e.emitGlobal(fmt.Sprintf("@%s_vtable ="+irGlobalAS+" global [%d x "+irPtr+"] [%s]", className, info.VTableSize, strings.Join(slots, ", ")))
}

// emitClassMember emits one method or constructor as a plain LLVM function,
// with an implicit receiver spliced in as LLVM parameter slot 0 (bound to
// scope symbol "this") ahead of the member's own declared parameters. When
// the enclosing class has a base (TDD-00009 Stage 3), scope symbol "super"
// is also bound — to the exact same underlying instance pointer as "this",
// just typed as the base class, so super.method(...)/super(...) resolve
// against the base's own effective method table while still operating on
// the one real instance. This is a deliberately separate function from
// emitFunctionDecl rather than a refactor of it: methods are structurally
// never async (the parser hardcodes isAsync=false for every class member),
// so there's no async/coroutine branch to thread a receiver through, and
// duplicating the smaller non-async subset carries far lower regression
// risk than adding a conditional receiver parameter to a function every
// top-level function already depends on (same reasoning docs/adr/00/ADR-00061.md
// gives for resolveObjectPtr's ObjectLiteral case duplicating
// emitObjectLiteral).
func (e *Emitter) emitClassMember(llvmName string, classTy Type, params []ast.Param, sig FuncSig, body *ast.BlockStatement, retType Type, pos ast.Pos, isStatic, isAsync bool) error {
	restoreBuilder := e.beginThunkEmit()
	// A fresh context (TDD-00223 §2: sawAwait asks whether THIS body
	// awaited), restored by defer.
	savedCtx := e.funcContext
	e.funcContext = &funcContext{
		currentCtorClass: savedCtx.currentCtorClass, // set by the caller for a constructor
		inModuleTask:     savedCtx.inModuleTask,
		memberClass:      classTy.ClassName,
	}
	defer func() { e.funcContext = savedCtx }()
	if mf := e.memberFrame; mf.node != nil {
		e.memberFrame.node = nil
		e.enterFrame(mf.node, mf.name)
	}
	e.pushScope()
	// Eager-boxing capture set for this body, as a function's (see
	// hoistedCaptures): a local a nested closure captures is boxed at its
	// declaration, not at the first capture, which may sit in one branch.
	savedHoistedCaptures := e.hoistedCaptures
	savedForwardClosures, savedForwardBoxes := e.forwardClosures, e.forwardBoxes
	defer func() {
		e.hoistedCaptures = savedHoistedCaptures
		e.forwardClosures, e.forwardBoxes = savedForwardClosures, savedForwardBoxes
	}()
	e.hoistedCaptures = nil
	e.setForwardClosures(nil)
	if body != nil {
		paramNames := make([]string, len(params))
		for i, p := range params {
			paramNames[i] = p.Name
		}
		e.hoistedCaptures = capturedLocalNames(body.Body, paramNames)
		e.hoistFunctionDecls(body.Body)
		e.setForwardClosures(body.Body)
		// Its vars a hoisted function reads before they run, as a
		// function body's.
		savedEarly := e.earlyVars
		e.earlyVars = earlyReadVars(body.Body)
		defer func() { e.earlyVars = savedEarly }()
		// Its own function declarations, as a function body's.
		if err := e.pushNestedFuncScope(params, body.Body); err != nil {
			return err
		}
		defer e.popNestedFuncScope()
		// Its bindings a later store widens (`let x = null; x = v`), as a
		// function body's.
		savedWidened := e.widenedBindings
		e.widenedBindings = e.crossTypeWidenedBindings(body.Body)
		defer func() { e.widenedBindings = savedWidened }()
	}

	// Async method (TDD-00063 Stage 2a): compiles to a coroutine exactly like
	// a top-level async function — the IR return type is `ptr` (the coro
	// handle), the logical Promise<T>'s T is tracked in currentPromiseTy, and
	// the prologue/epilogue bracket the body. See emit_func.go's identical
	// setup for a top-level async function.
	e.isAsync = isAsync
	e.coroHdl = ""
	e.currentPromiseTy = TypeVoid
	e.coroRetLabel = ""
	e.asyncAdoptSlot = ""
	if isAsync {
		if retType.IsPromise() && retType.PromiseType != nil {
			e.currentPromiseTy = *retType.PromiseType
		}
		e.currentRetType = TypePtr
		e.coroRetLabel = e.freshLabel("coro.ret")
		// Async methods emit the same inline catch-and-settle task-struct promise
		// async functions do (TDD-00087 follow-up), so a value typed `Promise<T>`
		// has one representation regardless of whether it came from a function or a
		// method — its `await`/`.then` reads the right slot. (Previously methods used
		// the pre-TDD-00084 bare-slot model, which stored the value at offset 0 and
		// broke any code that awaited through a `Promise<T>`-typed binding.)
		e.emitInlineAsyncPrologue()
	} else {
		e.currentRetType = retType
	}

	var llvmParams []string
	if isStatic {
		// No implicit receiver at all — a static member belongs to the
		// class itself, not an instance; a class per evaluation reads the
		// record its call passed.
		e.bindStaticReceiver(classTy.ClassName)
	} else {
		// Implicit receiver: LLVM parameter slot 0, never part of the AST's
		// own parameter list.
		llvmParams = append(llvmParams, irPtr+" %p_this")
		thisPtr := "%v_this"
		e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", thisPtr))
		e.emitInstr(fmt.Sprintf("store "+irPtr+" %%p_this, "+irPtr+" %s, align 8", thisPtr))
		if declared, ok := e.declaredMethodThis(body, classTy); ok {
			// `m(this: Other)`: the receiver a call passes (`m.call(o)`) is
			// read as the declared class.
			classTy = declared
		}
		if e.methodThisAny(body) {
			// `m(this: any)`: the receiver, boxed.
			boxed, err := e.emitBoxValue(Value{Ref: "%p_this", Ty: classTy})
			if err != nil {
				return err
			}
			anySlot := "%v_this_any"
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", anySlot))
			e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", boxed.Ref, anySlot))
			thisPtr, classTy = anySlot, TypeAny
		}
		e.define("this", Symbol{Ptr: thisPtr, Ty: classTy})
		if body != nil && arrowsCaptureThis(body) {
			// An arrow reads `this`: box the receiver at entry (which
			// dominates the whole body), as a captured parameter is, not at
			// the first capture, which may sit in one branch.
			thisReg, atEntry := "%p_this", true
			if classTy.IsDynamic() {
				// `this: any`: the boxed receiver, at the body's start.
				thisReg, atEntry = e.freshReg(), false
				e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", thisReg, thisPtr))
			}
			e.boxHoistedCapture("this", classTy, thisReg, true, atEntry)
		}
		if classInfo, ok := e.classes[classTy.ClassName]; ok && classInfo.BaseClass != "" {
			baseTy := e.classes[classInfo.BaseClass].Ty
			e.define("super", Symbol{Ptr: thisPtr, Ty: baseTy})
			if e.currentCtorClass != "" && ctorGuardsThis(body) {
				slot := e.freshReg()
				e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", slot))
				e.emitInstr(fmt.Sprintf("store i1 false, "+irPtr+" %s, align 1", slot))
				e.thisInitSlot = slot
			}
		}
	}

	// A body reading `arguments` destructures its pattern parameters after
	// building it (patternStep).
	var unpackLater *[]func() error
	if body != nil && paramsUseArguments(params, body.Body) {
		unpackLater = &[]func() error{}
	}
	for i, p := range params {
		pty := sig.ParamTypes[i]
		// TDD-00062 (Staged V2): a bare `any`/`unknown` method parameter is now
		// allowed — its { i8, i64 } argument is boxed at the call site (the
		// static-method path in emitStaticMethodCall and the instance path in
		// emitClassCall both box IsDynamic params) and bound here exactly like
		// a constrained-union param.
		if err := validateCompositeType(pty, pos.Line, pos.Col); err != nil {
			return err
		}
		if pty.IsArray() {
			llvmParams = append(llvmParams,
				fmt.Sprintf(irPtr+" %%p_%s_ptr", p.Name),
				fmt.Sprintf("i64 %%p_%s_len", p.Name),
			)
			// Object-reference array param (TDD-00127): see bindArrayParam. Same
			// shape as a top-level function, for a class method/constructor.
			if p.ArrayPattern != nil {
				e.bindRawPatternParam(unpackLater, p, pty, "")
				if err := e.patternStep(unpackLater, func() error {
					dataPtrReg := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %%p_%s_ptr, align 8", dataPtrReg, p.Name))
					return e.unpackArrayPatternInto(dataPtrReg, "%p_"+p.Name+"_len", *pty.ElemType, p.ArrayPattern)
				}); err != nil {
					return err
				}
				continue
			}
			if err := e.bindArrayParamPatternLater(unpackLater, p, pty); err != nil {
				return err
			}
		} else if isNullableScalar(pty) {
			// Nullable-scalar method/constructor parameter (TDD-00064 Stage 3),
			// same presence-flagged { i1, T } aggregate a top-level function's
			// parameter uses.
			llvmParams = append(llvmParams, nullableScalarParamDecl(p.Name, pty))
			e.defineNullableScalarParam(p.Name, "%v_"+p.Name, pty)
		} else {
			llvmParams = append(llvmParams, fmt.Sprintf("%s %%p_%s", pty.IR, p.Name))
			ptrName := "%v_" + p.Name
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", ptrName, pty.IR, pty.Align()))
			e.emitInstr(fmt.Sprintf("store %s %%p_%s, "+irPtr+" %s, align %d", pty.IR, p.Name, ptrName, pty.Align()))
			if p.ArrayPattern != nil || p.ObjectPattern != nil {
				e.bindRawPatternParam(unpackLater, p, pty, ptrName)
			}
			if p.ArrayPattern != nil && pty.IsDynamic() {
				// An `any` argument: destructured as a dynamic value, as a
				// function's parameter is.
				if err := e.patternStep(unpackLater, func() error {
					return e.unpackDynArrayParam(ptrName, pty, p.ArrayPattern, pos)
				}); err != nil {
					return err
				}
				continue
			}
			if p.ObjectPattern != nil {
				if err := e.patternStep(unpackLater, func() error {
					objPtrReg := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align 8", objPtrReg, patternParamIR(pty), ptrName))
					return e.unpackObjectPatternInto(objPtrReg, pty, p.ObjectPattern, pos)
				}); err != nil {
					return err
				}
				continue
			}
			if e.hoistedCaptures[p.Name] {
				// Captured by a nested closure: boxed at entry, as a
				// function's parameter is.
				e.boxHoistedCapture(p.Name, pty, "%p_"+p.Name, false, true)
			} else {
				e.define(p.Name, Symbol{Ptr: ptrName, Ty: pty})
			}
		}
	}

	// A method body referencing `arguments` gets the same synthesized
	// parameter array a plain function body does (ADR-00387/ADR-00464).
	if err := e.synthesizeArgumentsObject(&ast.FunctionDeclaration{Params: params, Body: body}, sig); err != nil {
		return err
	}
	if err := e.runPatternSteps(unpackLater); err != nil {
		return err
	}
	if err := e.emitParamDefaultPrologue(params, sig.ParamTypes, body, body.GetPos()); err != nil {
		return err
	}
	if body != nil {
		e.dropReplacedParams(params, body.Body)
	}

	for _, stmt := range body.Body {
		if err := e.emitStmt(stmt); err != nil {
			return err
		}
	}

	// TDD-00161 Stage 5: standard-dialect per-instance construction effects
	// (field-initializer transforms + addInitializer callbacks) run at the end
	// of the constructor. e.currentCtorClass is set only while emitting a
	// constructor, so this is a no-op for methods. Skipped if the body already
	// terminated (e.g. an unconditional throw).
	if e.currentCtorClass != "" && !e.blockDone {
		// A derived constructor ending before super() bound `this`: the
		// ReferenceError.
		e.emitThisBoundCheck()
		if err := e.emitStandardConstructorTail(e.currentCtorClass); err != nil {
			return err
		}
		e.emitOverrideThisReturn()
	}

	if isAsync {
		e.emitInlineAsyncEpilogue()
		// An async method whose body awaited runs as a coroutine (TDD-00223 §2).
		e.writeAsyncDefinition(llvmName, strings.Join(llvmParams, ", "), e.sawAwait)
	} else {
		e.emitValuelessRet() // fall-off returns undefined/zero (ADR-01061)
		e.writeFunc(fmt.Sprintf("%s @%s(%s)",
			retType.LLVMRetType(), llvmName, strings.Join(llvmParams, ", ")), e.allocas.String()+e.body.String())
	}
	restoreBuilder()
	e.blockDone = false

	return nil
}

// emitModuleThis is a module's own `this` (at its top level, or in an
// arrow there): undefined in an ES module, and CommonJS's module.exports —
// one object per file, made on first read — in a file Node runs as
// CommonJS. False when ex is in a function or class of its own.
func (e *Emitter) emitModuleThis(ex *ast.ThisExpression) (Value, bool) {
	c := e.front()
	if c == nil || e.prog == nil {
		return Value{}, false
	}
	var n ast.Node = ex
	var top ast.Statement
	for {
		p := c.ParentOf(n)
		if p == nil {
			return Value{}, false
		}
		switch p.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ClassDeclaration, *ast.ClassExpression:
			return Value{}, false
		case *ast.Program:
			top, _ = n.(ast.Statement)
		}
		if top != nil {
			break
		}
		n = p
	}
	file := e.prog.StmtFiles[top]
	if e.prog.IsESM(top) {
		return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, true
	}
	if e.moduleExports == nil {
		e.moduleExports = map[string]string{}
	}
	g, ok := e.moduleExports[file]
	if !ok {
		g = fmt.Sprintf("@__kml_module_exports%d", len(e.moduleExports))
		e.moduleExports[file] = g
		e.emitGlobal(g + " = internal" + irGlobalAS + " global i64 0, align 8")
	}
	e.ensureNanBox()
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", cur, g))
	isNew := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isNew, cur))
	mk, done := e.freshLabel("modthis.new"), e.freshLabel("modthis.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNew, mk, done))
	e.emitLabel(mk)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_dynobj_new()", bag))
	boxed := e.emitNbTagPtr(bag, kmlTagDynObject)
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", boxed, g))
	e.emitTerminator("br label %" + done)
	e.emitLabel(done)
	v := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", v, g))
	return Value{Ref: v, Ty: TypeAny}, true
}

// emitThisExpression evaluates `this` — valid only inside a method or
// constructor body, where emitClassMember has already bound scope symbol
// "this" to the receiver. Same load-from-alloca shape any other
// object-typed identifier read uses.
// ctorGuardsThis reports a derived constructor body that may read `this`
// (or a super property) before super() binds it, or call super() twice:
// anything but one leading `super(…)` statement whose arguments read
// neither.
func ctorGuardsThis(body *ast.BlockStatement) bool {
	if body == nil || len(body.Body) == 0 || countSuperCalls(body) == 0 || superCallInArrow(body) {
		// (A body with no super() call is guardMissingSuper's, or a
		// constructor codegen made, whose base binds `this` itself; an
		// arrow's super() binds it in a function of its own.)
		return false
	}
	st, ok := body.Body[0].(*ast.ExpressionStatement)
	if !ok {
		return true
	}
	call, ok := st.Expr.(*ast.CallExpression)
	if !ok {
		return true
	}
	if _, ok := call.Callee.(*ast.SuperExpression); !ok {
		return true
	}
	for _, a := range call.Args {
		if containsThis(a) || containsSuper(a) {
			return true
		}
	}
	return countSuperCalls(body) != 1
}

// superCallInArrow reports a super(…) call inside an arrow function of the
// constructor body.
func superCallInArrow(body *ast.BlockStatement) bool {
	found := false
	ast.Inspect(body, func(x ast.Node) bool {
		switch x := x.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		case *ast.ArrowFunction:
			found = found || countSuperCalls(ast.NewBlockStatement([]ast.Statement{ast.NewExpressionStatement(x, x.GetPos())}, x.GetPos())) > 0
			return false
		}
		return !found
	})
	return found
}

// containsSuper reports a super reference in n outside nested non-arrow
// functions and classes.
func containsSuper(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(x ast.Node) bool {
		switch x.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		case *ast.SuperExpression:
			found = true
		}
		return !found
	})
	return found
}

// emitThisBoundCheck throws the derived constructor's ReferenceError when
// `this` is read before super() bound it.
func (e *Emitter) emitThisBoundCheck() {
	if e.thisInitSlot == "" {
		return
	}
	b := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", b, e.thisInitSlot))
	okL, badL := e.freshLabel("this.bound"), e.freshLabel("this.unbound")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", b, okL, badL))
	e.emitLabel(badL)
	e.emitThrowKind("ReferenceError", derivedNoSuperMessage)
	e.emitLabel(okL)
}

// emitThisBind records super() binding `this`: a second binding throws.
func (e *Emitter) emitThisBind() {
	if e.thisInitSlot == "" {
		return
	}
	b := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", b, e.thisInitSlot))
	okL, badL := e.freshLabel("this.bind"), e.freshLabel("this.rebind")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", b, badL, okL))
	e.emitLabel(badL)
	e.emitThrowKind("ReferenceError", superAgainMessage)
	e.emitLabel(okL)
	e.emitInstr(fmt.Sprintf("store i1 true, "+irPtr+" %s, align 1", e.thisInitSlot))
}

func (e *Emitter) emitThisExpression(pos ast.Pos) (Value, error) {
	sym, ok := e.lookup("this")
	if !ok {
		return Value{}, internalAt(pos, "'this' is only valid inside a method or constructor body")
	}
	e.emitThisBoundCheck()
	reg := e.freshReg()
	// A dynamic `this` (a prototype-class constructor/method under
	// `-compat=js`, TDD-00155 Stage 4) is a boxed { i8, i64 } slot, not a
	// class-instance pointer.
	if sym.Ty.IsDynamic() {
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", reg, sym.Ptr))
		return Value{Ref: reg, Ty: sym.Ty}, nil
	}
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", reg, sym.Ptr))
	return Value{Ref: reg, Ty: sym.Ty}, nil
}

// emitNewTarget is `new.target`: in a class constructor (and its arrows) the
// class `new` named, which is the constructor of the instance being built
// (`this.constructor`, a derived class's through super()); in a prototype
// constructor (`-compat=js`) the function when `this` was made for it by
// `new`, undefined through `Base.call(this)`; elsewhere undefined, since no
// `new` runs the code.
func (e *Emitter) emitNewTarget(ex *ast.NewTargetExpression) (Value, error) {
	pos := ex.GetPos()
	undef := ast.NewNullLiteral(true, pos)
	switch ex.Ctx {
	case ast.NewTargetConstructor:
		if _, ok := e.lookup("this"); ok {
			// The instance's class, before super() too: no TDZ check.
			saved := e.thisInitSlot
			e.thisInitSlot = ""
			defer func() { e.thisInitSlot = saved }()
			return e.emitExpr(ast.NewMemberExpression(ast.NewThisExpression(pos), "constructor", pos))
		}
	case ast.NewTargetFunction:
		if fn := e.newTargetFunc(); fn != "" && ex.FnID != 0 && ex.FnID == e.newTargetDecl.NewTargetID {
			if _, ok := e.lookup("this"); !ok {
				break
			}
			if e.funcContext != nil && e.newTargetSlot != "" {
				// The constructor's own body: what its `new` or `super()`
				// passed.
				v, z, r := e.freshReg(), e.freshReg(), e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", v, e.newTargetSlot))
				e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", z, v))
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", r, z, nbUndefined, v))
				return Value{Ref: r, Ty: TypeAny}, nil
			}
			f := ast.NewIdentifier(fn, pos)
			proto, err := e.emitObjectGetPrototypeOf([]ast.Expression{ast.NewThisExpression(pos)}, pos)
			if err != nil {
				return Value{}, err
			}
			made := ast.NewBinaryExpression("===", e.bindValue(proto, pos), ast.NewMemberExpression(f, "prototype", pos), pos)
			return e.emitExpr(ast.NewConditionalExpression(made, f, undef, pos))
		}
	}
	return e.emitExpr(undef)
}

// newTargetFunc is the prototype constructor whose body (or an arrow in
// it) is being emitted, else "".
func (e *Emitter) newTargetFunc() string { return e.newTargetFn }

// heldIdent is the type a name's binding is held as in the scope being
// emitted.
func (e *Emitter) heldIdent(id *ast.Identifier) (Type, bool) {
	sym, ok := e.lookup(id.Name)
	if !ok {
		return Type{}, false
	}
	// Asked out of its function's emission (a global's type computed from a
	// function body's returns), the name may find a module global the
	// local shadows: no answer then.
	if o := e.reprOracle(); o != nil && (strings.HasPrefix(sym.Ptr, "@") || e.foundAtTop(id.Name)) {
		if bs, _ := o.b.Resolve(id); bs != nil && bs.Scope != nil && bs.Scope.Kind != binder.ModuleScope {
			return Type{}, false
		}
	}
	return e.identStorageType(id), true
}

// newTargetHeld reports whether emitNewTarget holds a function's
// new.target as the function (the body compiled to be constructed, with a
// receiver), not undefined.
func (e *Emitter) newTargetHeld(ex *ast.NewTargetExpression) bool {
	if fn := e.newTargetFunc(); fn == "" || ex.FnID == 0 || e.newTargetDecl == nil || ex.FnID != e.newTargetDecl.NewTargetID {
		return false
	}
	_, ok := e.lookup("this")
	return ok
}

// dropReplacedMethods removes each method a later one of the same name
// and placement (static or not) replaces: JavaScript defines the class's
// methods in order, so the last definition is the one it keeps
// (TypeScript's TS2393 rejects the program before code generation).
// Accessors and computed keys keep their own rules.
func dropReplacedMethods(cd *ast.ClassDeclaration) {
	plain := func(m *ast.FunctionDeclaration) bool {
		return m != nil && m.Body != nil && m.Key == nil && m.AccessorKind == "" && !strings.HasPrefix(m.Name, "#")
	}
	type key struct {
		name   string
		static bool
	}
	last := map[key]int{}
	for i, m := range cd.Methods {
		if plain(m) {
			last[key{m.Name, m.IsStatic}] = i
		}
	}
	if len(last) == 0 {
		return
	}
	kept := cd.Methods[:0:0]
	for i, m := range cd.Methods {
		if plain(m) && last[key{m.Name, m.IsStatic}] != i {
			continue
		}
		kept = append(kept, m)
	}
	cd.Methods = kept
}

// emitNewExpression evaluates `new ClassName(args)`: malloc an instance
// sized to the class's field layout, initialize the hidden tag (and, for a
// HasVTable class — TDD-00009 Stage 3 — the hidden vtable pointer), call
// its constructor (if any) with the fresh pointer as the implicit receiver,
// and return the pointer.
// newOfNamespace reports `new` of a builtin namespace object (`new Math()`)
// no class or local of that name shadows.
func (e *Emitter) newOfNamespace(ex *ast.NewExpression) bool {
	if _, ns := namespaceValueExport(ex.ClassName); !ns || ex.Callee != nil || ex.Qualified {
		return false
	}
	if _, user := e.classes[ex.ClassName]; user {
		return false
	}
	_, local := e.lookup(ex.ClassName)
	return !local
}

func (e *Emitter) emitNewExpression(ex *ast.NewExpression) (Value, error) {
	defer e.callSite(ex.GetPos())()
	// An existing instance the constructor runs on (emitClassCallOn), taken
	// before any argument's own `new` can see it.
	into := e.newInto
	e.newInto = ""
	if nx, ok := e.iterableCtorArg(ex); ok {
		ex = nx
	}
	// className is the actual LLVM-symbol-bearing/e.classes-registry name —
	// ex.ClassName itself for a plain class, but a mangled per-instantiation
	// name for a generic one (TDD-00010 V1); user-facing error messages
	// below still reference ex.ClassName, the name the source actually
	// wrote.
	// new Promise((resolve, reject) => …) — the executor constructor (TDD-00087),
	// not a user class.
	if ex.ClassName == "Promise" {
		return e.emitNewPromise(ex)
	}
	if bufferCallee(ex.ClassName, ex.Callee) && !e.isShadowedByLocal("Buffer") {
		if call, ok := e.bufferCtorCall(ex.Args, ex.GetPos()); ok {
			return e.emitExpr(call)
		}
	}
	if e.newOfNamespace(ex) {
		// `new Math()`: a namespace object constructs nothing — its
		// TypeError once the arguments are evaluated.
		for _, a := range ex.Args {
			if sp, ok := a.(*ast.SpreadElement); ok {
				a = sp.Arg
			}
			if _, err := e.emitExpr(a); err != nil {
				return Value{}, err
			}
		}
		e.emitThrowTypeError(ex.ClassName + " is not a constructor")
		return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil
	}
	if name := protoCtorCallee(ex); ex.ClassName == "" && e.jsProtoCtor[name] {
		// `new (F as any)(…)`: a constructor function.
		nx := *ex
		nx.ClassName, nx.Callee = name, nil
		return e.emitProtoCtorNew(&nx)
	}
	if owner, ok := streamLockCtors[ex.ClassName]; ok && !ex.Qualified && ex.Callee == nil {
		if _, user := e.classes[ex.ClassName]; !user {
			return e.emitNewStreamLock(ex, owner)
		}
	}
	// new Proxy(target, handler) — TDD-00155 Stage 7, not a user class.
	if ex.ClassName == "Proxy" {
		return e.emitNewProxy(ex)
	}
	// new Response(body?, init?) — Fetch's constructor, unless a user class
	// of that name shadows it.
	if _, user := e.classes[ex.ClassName]; ex.ClassName == "Response" && !user && !ex.Qualified {
		return e.emitNewResponse(ex)
	}
	// new FinalizationRegistry<T>(cb) — TDD-00163, not a user class.
	if ex.ClassName == "FinalizationRegistry" {
		return e.emitNewFinalizationRegistry(ex)
	}
	if e.isGlobalObjectCtor(ex.ClassName, ex.Qualified) {
		return e.emitObjectCtor(ex.Args, ex.GetPos())
	}
	if e.isPrimWrapperCtor(ex.ClassName, ex.Qualified) {
		return e.emitNewPrimWrapper(ex)
	}
	if target, ok := e.constClassAlias(ex); ok {
		// `const K = C; new K(…)` constructs C.
		alias := *ex
		alias.ClassName = target
		return e.emitNewExpression(&alias)
	}
	if instTy, ok := e.dynNewInstanceType(ex); ok {
		return e.emitDynNew(ex, instTy)
	}
	className := ex.ClassName
	info, ok := e.classes[ex.ClassName]
	if !ok {
		// A generic class (TDD-00010 V1) is never itself entered into
		// e.classes — only its concrete instantiations are, keyed by their
		// mangled name — so a construction site against the bare generic
		// name always misses the lookup above on its first-ever use.
		if genDecl, isGeneric := e.genericClasses[ex.ClassName]; isGeneric {
			// An omitted type argument takes its parameter's default
			// (`class C<T = any>`).
			typeArgs, ok := e.newTypeArgs(genDecl, ex)
			if !ok {
				if len(ex.TypeArgs) > 0 {
					// A wrong count is the checker's TS2558.
					e.typeGap("type arguments", ex.GetPos())
				}
				// A type parameter nothing infers is its default, else
				// `unknown`, as TypeScript instantiates it.
				typeArgs = make([]ast.TypeNode, len(genDecl.TypeParams))
				for i := range typeArgs {
					if i < len(genDecl.TypeParamDefaults) && genDecl.TypeParamDefaults[i] != nil {
						typeArgs[i] = genDecl.TypeParamDefaults[i]
					} else {
						typeArgs[i] = &ast.KeywordType{Keyword: "unknown"}
					}
				}
			}
			subs := e.buildTypeArgSubs(genDecl.TypeParams, typeArgs)

			mangled, err := e.instantiateGenericClass(genDecl, subs)
			if err != nil {
				return Value{}, err
			}
			className = mangled
			info = e.classes[mangled]
		} else if e.jsProtoCtor[ex.ClassName] {
			// A vanilla-JS prototype constructor (TDD-00155 Stage 4).
			return e.emitProtoCtorNew(ex)
		} else if _, isGen := e.lookupGenerator(ex.ClassName); isGen && e.compatJS() {
			// A generator function is not a constructor: a TypeError once
			// the arguments are evaluated.
			for _, a := range ex.Args {
				if _, err := e.emitExpr(a); err != nil {
					return Value{}, err
				}
			}
			e.emitThrowTypeError(unmangleTopLevelName(ex.ClassName) + " is not a constructor")
			e.emitLabel(e.freshLabel("notctor.dead"))
			return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil
		} else {
			// A name no class of the program declares (an ambient
			// declaration's, or one only the environment provides): read
			// as JavaScript reads it, a global property or its
			// ReferenceError, and constructed through the value.
			return e.emitDynNew(ex, e.checkerInstanceType(ex))
		}
	}

	// Zeroing allocation (calloc, or — under -optimize-memory, for a
	// non-escaping binding of a class whose ctor/methods provably never
	// leak `this` — an entry-block alloca + zero store): this compiler
	// doesn't verify every field is assigned on every path through the
	// constructor (no definite-assignment check), so an under-assigned
	// field must read back a deterministic zero rather than malloc garbage
	// — same real bug as object literals' omitted `?:` fields, found
	// investigating destructuring defaults, see ADR-00157.
	if e.pendingStackAllocLit != nil && e.pendingStackAllocLit == ast.Expression(ex) && !e.classStackEligible(className, info) {
		e.pendingStackAllocLit = nil // ineligible class: keep the heap path
	}
	var dataReg string
	if into != "" {
		dataReg = into
	} else {
		dataReg = e.structAlloc(ex, info.Ty)
		e.initInstanceWords(dataReg, className, info)
	}
	if into == "" {
		if err := e.storeClassCaptures(ex, className, dataReg, info.Ty); err != nil {
			return Value{}, err
		}
		// A newTarget other than the class (Reflect.construct) gives the
		// instance its prototype before the constructor runs.
		e.emitTakePendingProto(dataReg)
	}

	// A class extending Map or Set (TDD-00247): its body, after the tag
	// word, is an empty collection before the constructor runs; without a
	// constructor of its own, `new M(entries)` seeds it as `new Map(entries)`
	// does.
	if info.HostBase != nil {
		body := e.emitHostBody(dataReg, *info.HostBase)
		if info.Constructor == nil && info.HostBase.IsRegExp() {
			if err := e.emitHostRegExpBody(body, ex.Args, ex.GetPos()); err != nil {
				return Value{}, err
			}
			return Value{Ref: dataReg, Ty: info.Ty}, nil
		}
		if (info.HostBase.IsDate() || info.HostBase.IsArray() || info.HostBase.IsPrimWrapper() || info.HostBase.IsArrayBuffer()) && (info.Constructor == nil || info.ImplicitCtor) {
			// No constructor of its own: the arguments are Date's (Array's).
			// An implicit one (field initializers) then runs without them.
			var err error
			if info.HostBase.IsArray() {
				err = e.emitHostArrayBody(body, *info.HostBase, ex.Args, ex.GetPos())
			} else if info.HostBase.IsPrimWrapper() {
				err = e.emitHostPrimBody(body, *info.HostBase, ex.Args, ex.GetPos())
			} else if info.HostBase.IsArrayBuffer() {
				err = e.emitHostBufferBody(body, *info.HostBase, ex.Args, ex.GetPos())
			} else {
				err = e.emitHostDateBody(body, ex.Args, ex.GetPos())
			}
			if err != nil {
				return Value{}, err
			}
			if info.Constructor == nil {
				return Value{Ref: dataReg, Ty: info.Ty}, nil
			}
			bare := *ex
			bare.Args = nil
			ex = &bare
		}
		if info.Constructor == nil && info.HostBase.IsPromise() {
			if err := e.emitHostPromiseBody(body, *info.HostBase, ex.Args, ex.GetPos()); err != nil {
				return Value{}, err
			}
			return Value{Ref: dataReg, Ty: info.Ty}, nil
		}
		if info.Constructor == nil {
			if len(ex.Args) > 1 {
				// Extra arguments: evaluated, then ignored.
				kept, err := e.dropArgsPast(ex.Args, 1, ex.GetPos())
				if err != nil {
					return Value{}, err
				}
				trimmed := *ex
				trimmed.Args = kept
				ex = &trimmed
			}
			if len(ex.Args) == 1 && !isUndefinedLiteral(e, ex.Args[0]) {
				if err := e.emitHostBodySeed(body, *info.HostBase, ex.Args[0], ex.GetPos()); err != nil {
					return Value{}, err
				}
			}
			return Value{Ref: dataReg, Ty: info.Ty}, nil
		}
	}

	// An error subclass (TDD-00155 Stage 6): default the error-struct prefix
	// before the constructor — name is the class's source name, message the
	// empty string. A constructor-less subclass takes JS's optional message
	// argument directly.
	if info.IsErrorSubclass {
		storeField := func(field, ref string) {
			idx, _, ok := info.Ty.FieldIndex(field)
			if ok {
				gep := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gep, info.Ty.StructIR(), dataReg, idx))
				e.heapStore(irPtr, ref, gep, 8)
			}
		}
		// JS: `.name` is inherited from the builtin's prototype ("Error",
		// "TypeError", …) unless the class assigns `this.name` itself.
		storeField("name", e.internString(errorInitialName(info.ErrorBaseKind)))
		storeField("message", e.internString(""))
		// Its frames, from the `new`'s (Node's stack omits the constructors).
		if _, _, ok := info.Ty.FieldIndex(errorFramesField); ok {
			storeField("stack", "null")
			storeField(errorFramesField, "null")
			e.emitInstr(fmt.Sprintf("call void @__kml_error_capture("+irPtr+" %s)", dataReg))
		}
		if info.Constructor == nil {
			if e.aggregateRooted(info) {
				// AggregateError's (errors, message, options).
				if len(ex.Args) == 0 {
					e.emitThrowTypeError("undefined is not iterable (cannot read property Symbol(Symbol.iterator))")
					return Value{Ref: dataReg, Ty: info.Ty}, nil
				}
				if err := e.emitAggregateErrors(dataReg, ex.Args[0]); err != nil {
					return Value{}, err
				}
				ex = &ast.NewExpression{ClassName: ex.ClassName, Args: ex.Args[1:]}
			}
			if len(ex.Args) > 2 {
				// Extra arguments: evaluated, then ignored.
				kept, err := e.dropArgsPast(ex.Args, 2, ex.GetPos())
				if err != nil {
					return Value{}, err
				}
				trimmed := *ex
				trimmed.Args = kept
				ex = &trimmed
			}
			if len(ex.Args) == 2 {
				slot := e.freshReg()
				e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
				e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", dataReg, slot))
				if err := e.emitErrorSecondArg(Symbol{Ptr: slot, Ty: info.Ty}, info, ex.Args[1]); err != nil {
					return Value{}, err
				}
			}
			if len(ex.Args) >= 1 {
				msg, err := e.emitExpr(ex.Args[0])
				if err != nil {
					return Value{}, err
				}
				msgRef, err := e.emitErrorMessage(msg)
				if err != nil {
					return Value{}, err
				}
				storeField("message", msgRef)
			}
			return Value{Ref: dataReg, Ty: info.Ty}, nil
		}
	}

	if info.Constructor != nil {
		sig := info.CtorSig
		// Default parameters are supported (ADR-00599): trailing params with a
		// default or a `?` may be omitted, and a default may reference an earlier
		// scalar/string parameter — the same arg/default/optional switch and
		// paramDefaultScratch the method call paths use. A rest-parameter
		// constructor stays out of scope (it never worked under the old
		// exact-count loop either).
		regularCount := len(sig.ParamTypes)
		if sig.HasRest {
			regularCount-- // the rest slot takes the remaining arguments
		}
		cargs, doneSplit, serr := e.splitSpreadArgs(ex.Args, sig.HasRest, regularCount, ex.GetPos())
		if serr != nil {
			return Value{}, serr
		}
		defer doneSplit()
		minRequired := regularCount
		for minRequired > 0 && ((minRequired-1 < len(sig.Defaults) && sig.Defaults[minRequired-1] != nil) ||
			(minRequired-1 < len(sig.Optional) && sig.Optional[minRequired-1])) {
			minRequired--
		}
		var extraArgs []ast.Expression
		if e.compatJS() && !sig.HasRest && len(cargs) > len(sig.ParamTypes) {
			extraArgs = cargs[len(sig.ParamTypes):]
			cargs = cargs[:len(sig.ParamTypes):len(sig.ParamTypes)] // the AST node keeps every argument
		}
		if e.compatJS() {
			minRequired = 0 // untyped JavaScript may omit any argument
		}
		if len(cargs) < minRequired || !sig.HasRest && len(cargs) > len(sig.ParamTypes) {
			return Value{}, internalAt(ex.GetPos(), "arity: %s constructor expects %d argument(s), got %d", ex.ClassName, len(sig.ParamTypes), len(cargs))
		}
		argParts := []string{irPtr + " " + dataReg}
		scratch := e.newParamDefaultScratch(sig.ParamNames)
		for i := 0; i < regularCount; i++ {
			paramTy := sig.ParamTypes[i]
			var a ast.Expression
			fromDefault := false
			switch {
			case i < len(cargs) && !undefinedFillsDefault(cargs[i], sig, i):
				a = cargs[i]
			case i < len(sig.Defaults) && sig.Defaults[i] != nil:
				a = sig.Defaults[i]
				fromDefault = true
			case i < len(sig.Optional) && sig.Optional[i] || e.compatJS():
				// ADR-00164: an omitted `param?: T` gets T's zero value.
				if paramTy.IsArray() {
					argParts = append(argParts, irPtr+" "+e.omittedArrayArgHeader(paramTy), "i64 0")
				} else if isNullableScalar(paramTy) {
					// An omitted optional `T | undefined` param is a genuinely
					// absent { i1, T } aggregate (present = false) — TDD-00187.
					argParts = append(argParts, nullableScalarStorageIR(paramTy)+" zeroinitializer")
					scratch.bindNullable(i, "zeroinitializer", paramTy)
				} else if paramTy.IsDynamic() {
					// An omitted `any` argument is the boxed undefined.
					undef := fmt.Sprintf("%d", nbUndefined)
					argParts = append(argParts, "i64 "+undef)
					scratch.bind(i, Value{Ref: undef, Ty: paramTy})
				} else {
					argParts = append(argParts, fmt.Sprintf("%s %s", paramTy.IR, paramTy.zeroLiteral()))
					scratch.bind(i, Value{Ref: paramTy.zeroLiteral(), Ty: paramTy})
				}
				continue
			default:
				return Value{}, internalAt(ex.GetPos(), "arity: %s constructor missing argument %d with no default", ex.ClassName, i+1)
			}
			if !fromDefault {
				// An argument that may be undefined at run time takes the
				// default when it is.
				if v, ok, err := e.emitArgOrDefault(a, paramTy, sig, i, func() { scratch.enter(true) }, func() { scratch.leave(true) }); ok || err != nil {
					if err != nil {
						return Value{}, err
					}
					argParts = append(argParts, fmt.Sprintf("%s %s", v.Ty.IR, v.Ref))
					scratch.bind(i, v) // an any binds as its box
					continue
				}
			}
			// An array-typed constructor parameter decomposes into two LLVM
			// params (ptr, i64 len) at the callee side, exactly like an
			// array-typed method parameter.
			if paramTy.IsArray() {
				scratch.enter(fromDefault)
				val, err := e.emitExprWithObjectHint(a, paramTy)
				scratch.leave(fromDefault)
				if err != nil {
					return Value{}, err
				}
				if !val.Ty.IsArray() && paramTy.Nullable() && (val.Ty.IsNull() || val.Ty.IsUndefined()) {
					// `null` for a `T[] | null` parameter: the absent (null) header.
					val = e.emitAbsentArrayValue(val.Ty, paramTy)
				}
				if !val.Ty.IsArray() {
					return Value{}, internalAt(a.GetPos(), "expression does not yield an array")
				}
				header, lenReg := e.packArrayArg(a, val, paramTy)
				argParts = append(argParts, irPtr+" "+header, "i64 "+lenReg)
				scratch.bindArray(i, header, paramTy)
				continue
			}
			// Nullable-scalar constructor parameter (TDD-00064 Stage 3).
			if isNullableScalar(paramTy) {
				scratch.enter(fromDefault)
				agg, err := e.emitNullableScalarBoxedValue(a, paramTy)
				scratch.leave(fromDefault)
				if err != nil {
					return Value{}, err
				}
				argParts = append(argParts, fmt.Sprintf("%s %s", nullableScalarStorageIR(paramTy), agg))
				scratch.bindNullable(i, agg, paramTy)
				continue
			}
			scratch.enter(fromDefault)
			val, err := e.emitExprWithObjectHint(a, paramTy)
			scratch.leave(fromDefault)
			if err != nil {
				return Value{}, err
			}
			// The ADR-00042 unannotated-parameter guard, applied to
			// constructor calls (previously the function-call paths only —
			// a string argument to a number-defaulted constructor parameter
			// silently stored the pointer as a number).
			if paramTy.Inferred && !isSafeNumericArg(val.Ty) {
				paramName := fmt.Sprintf("%d", i+1)
				if i < len(sig.ParamNames) {
					paramName = "'" + sig.ParamNames[i] + "'"
				}
				return Value{}, internalAt(a.GetPos(), "constructor parameter %s of '%s' has no type annotation (defaults to number) but was called with a non-numeric argument here — add an explicit type annotation", paramName, ex.ClassName)
			}
			val = e.convert(val, paramTy)
			if err := argReprMismatch(val, paramTy, a, i); err != nil {
				return Value{}, err
			}
			argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
			scratch.bind(i, val) // an any binds as its box
		}
		// Extra arguments untyped JavaScript passes are evaluated, in
		// order, and dropped.
		for _, x := range extraArgs {
			if _, err := e.emitExpr(x); err != nil {
				return Value{}, err
			}
		}
		if sig.HasRest {
			var restArgs []ast.Expression
			if len(cargs) > regularCount {
				restArgs = cargs[regularCount:]
			}
			parts, err := e.packRestArgs(restArgs, sig.ParamTypes[len(sig.ParamTypes)-1])
			if err != nil {
				return Value{}, err
			}
			argParts = append(argParts, parts...)
		}
		e.noteArgc(e.methodsMayReadArgs() || sigCountsArgs(sig), cargs, regularCount)
		e.emitInstr(fmt.Sprintf("call void @%s%s(%s)", className, ctorSymbolSuffix, strings.Join(argParts, ", ")))
		dataReg = e.emitCtorOverride(className, dataReg)
		if e.ctorMayOverride(className) && (e.dynConstructing || e.typeOfExpr(ex).IsDynamic()) {
			// The constructor may return another object: the value of
			// `new` is it when it did, else the instance (boxed).
			inst, err := e.emitBoxValue(Value{Ref: dataReg, Ty: info.Ty})
			if err != nil {
				return Value{}, err
			}
			ov := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" @__kml_ctor_ret_any, align 8", ov))
			e.emitInstr("store i64 0, " + irPtr + " @__kml_ctor_ret_any, align 8")
			// Only an object overrides (0 is the unset slot; a primitive,
			// null or undefined is ignored).
			e.ensureNanBox()
			e.declareFn("__kml_any_is_bigint", "declare i32 @__kml_any_is_bigint(i64)")
			tag, isHeap, big, isBig, notBig, obj := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_nb_tag(i64 %s)", tag, ov))
			e.emitInstr(fmt.Sprintf("%s = icmp uge i8 %s, 6", isHeap, tag))
			e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_any_is_bigint(i64 %s)", big, ov))
			e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", isBig, big))
			e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notBig, isBig))
			e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", obj, isHeap, notBig))
			unsetOrPrim, nz := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", nz, ov))
			e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", unsetOrPrim, obj, nz))
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", r, unsetOrPrim, ov, inst.Ref))
			return Value{Ref: r, Ty: TypeAny}, nil
		}
	} else if fn := e.fnBaseRoot(className); fn != "" {
		// No constructor down to a constructor function's base: the
		// implicit ones pass the arguments on to it, run on the instance.
		tb, err := e.emitBoxValue(Value{Ref: dataReg, Ty: info.Ty})
		if err != nil {
			return Value{}, err
		}
		e.emitNewTargetFromInstance(tb, ex.GetPos())
		if e.valueBases[fn] {
			if err := e.emitValueBaseSuper(fn, tb, ex.Args, ex.GetPos()); err != nil {
				return Value{}, err
			}
			if e.compatJS() && !builtinValueBases[fn] {
				// What a constructor value built is the instance.
				ov := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" @__kml_ctor_ret_any, align 8", ov))
				e.emitInstr("store i64 0, " + irPtr + " @__kml_ctor_ret_any, align 8")
				set, r := e.freshReg(), e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", set, ov))
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", r, set, ov, tb.Ref))
				return Value{Ref: r, Ty: TypeAny}, nil
			}
		} else if _, err := e.emitProtoCtorRun(fn, tb, ex.Args, ex.GetPos()); err != nil {
			return Value{}, err
		}
	} else if len(ex.Args) != 0 {
		// The default constructor ignores its arguments, evaluated.
		if _, err := e.dropArgsPast(ex.Args, 0, ex.GetPos()); err != nil {
			return Value{}, err
		}
	}

	return Value{Ref: dataReg, Ty: info.Ty}, nil
}

// emitClassCall is the shared TDD-00009 Stages 1-3 method-call-dispatch
// core: given an already-evaluated receiver value, resolve methodName
// against objTy's effective method table (own or inherited — MethodSigs
// already holds both, see ClassInfo's doc comment) and emit either:
//   - a direct static call `call RETTY @Implementor_methodName(ptr this,
//     ARGS...)` — the common case, identical in shape to Stage 1/2 and used
//     whenever the method is never overridden anywhere in objTy's
//     inheritance tree, or when forceDirect is set (an explicit
//     super.method(...) call always bypasses dispatch, matching JS/TS
//     semantics, even if the method is Virtual); or
//   - an indirect vtable call — load the vtable pointer field off the
//     instance, index into it at this method's globally-assigned slot,
//     load the function pointer, indirect-call it (same shape
//     emitClosureCallByPtr already establishes for closures) — only when
//     the method is Virtual anywhere in the tree and forceDirect is false.
//
// objExpr is not evaluated here — callers with an unevaluated receiver
// expression (a plain `obj.method(args)` call site) should use
// emitClassMethodCall instead, which evaluates it once and delegates here.
// paramDefaultScratch lets a class method/constructor parameter default
// reference an earlier scalar/string parameter (ADR-00598, extended to class
// calls). Each earlier scalar param's final value (argument, its own default, or
// the optional zero) is materialized and exposed under the parameter's name —
// but only while a default is being emitted, so an ordinary argument is still
// evaluated in the caller's scope and never sees a sibling parameter.
type paramDefaultScratch struct {
	e     *Emitter
	names []string
	syms  map[string]Symbol
}

func (e *Emitter) newParamDefaultScratch(names []string) *paramDefaultScratch {
	return &paramDefaultScratch{e: e, names: names, syms: map[string]Symbol{}}
}

func (s *paramDefaultScratch) enter(active bool) {
	if active && len(s.syms) > 0 {
		s.e.pushScope()
		for n, sym := range s.syms {
			s.e.define(n, sym)
		}
	}
}

func (s *paramDefaultScratch) leave(active bool) {
	if active && len(s.syms) > 0 {
		s.e.popScope()
	}
}

// emitClassFieldCall is `obj.f(args)` where f is a field holding a function
// (`this.handler = (x) => …`): the field's value called with obj as `this`.
func (e *Emitter) emitClassFieldCall(info ClassInfo, thisVal Value, idx int, fty Type, name string, args []ast.Expression, pos ast.Pos) (Value, error) {
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gep, info.Ty.StructIR(), thisVal.Ref, idx))
	fv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align 8", fv, StructFieldIR(fty), gep))
	if fty.IsFunc() && !fty.IsDynamic() {
		// Named by its receiver, as V8 names a method call's callee.
		e.callAs = callAs{ast.Unmangle(info.Ty.ClassName), name}
		return e.emitClosureCallByPtr(fv, fty, args, pos)
	}
	argv, n, err := e.emitDynArgv(args, pos)
	if err != nil {
		return Value{}, err
	}
	recv, err := e.emitBoxValue(thisVal)
	if err != nil {
		return Value{}, err
	}
	return e.emitDynFnBoxCallN(Value{Ref: fv, Ty: fty}, recv, argv, n, "this."+name+" is not a function", pos)
}

// bind materializes parameter i's final scalar value for a later default to use.
func (s *paramDefaultScratch) bind(i int, val Value) {
	if i >= len(s.names) {
		return
	}
	slot := s.e.freshReg()
	s.e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, val.Ty.IR, val.Ty.Align()))
	s.e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align %d", val.Ty.IR, val.Ref, slot, val.Ty.Align()))
	s.syms[s.names[i]] = Symbol{Ptr: slot, Ty: val.Ty}
}

// bindArray materializes an array parameter i via a slot holding its header
// pointer (arrayDataLenSlots derives data/len from it), so a later default can
// read `a.length`, `a[i]`, etc. (ADR-00610).
func (s *paramDefaultScratch) bindArray(i int, header string, arrTy Type) {
	if i >= len(s.names) {
		return
	}
	slot := s.e.freshReg()
	s.e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
	s.e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", header, slot))
	s.syms[s.names[i]] = Symbol{Ptr: slot, Ty: arrTy}
}

// bindNullable materializes a nullable-scalar parameter i via its { i1, T }
// aggregate slot, so a later default can `??`/`=== null`/narrow it (ADR-00611).
func (s *paramDefaultScratch) bindNullable(i int, agg string, pty Type) {
	if i >= len(s.names) {
		return
	}
	slot := s.e.freshReg()
	s.e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, nullableScalarStorageIR(pty), storageAlign(pty)))
	s.e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align %d", nullableScalarStorageIR(pty), agg, slot, storageAlign(pty)))
	s.syms[s.names[i]] = Symbol{Ptr: slot, Ty: pty}
}

func (e *Emitter) emitClassCall(objTy Type, thisVal Value, methodName string, args []ast.Expression, pos ast.Pos, forceDirect bool) (Value, error) {
	if _, err := e.ensureGenericInstance(objTy.ClassName); err != nil {
		return Value{}, err
	}
	if thisVal.Ty.IsDynamic() && thisVal.Ty.IR == "i64" {
		// A receiver read through the generic getter (a member of a
		// possibly-undefined object, untyped JavaScript) arrives boxed.
		thisVal = e.convert(thisVal, objTy)
	}
	info, ok := e.classes[objTy.ClassName]
	if !ok {
		return Value{}, internalAt(pos, "unknown class '%s'", objTy.ClassName)
	}
	sig, ok := info.MethodSigs[methodName]
	ovrTarget := ""
	if c := e.ovrCall; c != nil {
		e.ovrCall = nil
		sig, ovrTarget, ok = c.sig, c.target, true
	} else if ok && !forceDirect && !e.noWideOverride && !sig.HasRest && len(args) > len(sig.ParamTypes) {
		if v, done, err := e.emitWideOverrideCall(objTy, thisVal, methodName, args, sig, pos); done || err != nil {
			return v, err
		}
	}
	if ok {
		trimmed, err := e.dropExtraArgs(sig, args)
		if err != nil {
			return Value{}, err
		}
		args = trimmed
	}
	if !ok {
		if idx, fty, isField := info.Ty.FieldIndex(methodName); isField && (isUnconstrainedDynamic(fty) || fty.IsFunc()) {
			return e.emitClassFieldCall(info, thisVal, idx, fty, methodName, args, pos)
		}
		if info.HostBase != nil {
			// The host base's method, on the instance's host body.
			return e.emitHostBaseCall(thisVal, *info.HostBase, methodName, args, pos)
		}
		// A method the instance was given as its own property (the checker
		// rejected an undeclared one before codegen).
		box, err := e.emitBoxValue(thisVal)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyMethodCall(box, methodName, args, pos)
	}
	// A call prefers a method the instance was given as its own property.
	// Library code is shared between programs, so it reads whether the
	// program assigns the name from a flag the program's unit defines.
	if !forceDirect && !e.inOwnMethodCheck && !strings.HasPrefix(methodName, "#") {
		inLib := e.inLib != ""
		inProg := !inLib && !e.programIndependent() && e.jsMemberWritten(methodName)
		if inLib || inProg {
			// Both arms take the arguments: one holding a function literal is
			// evaluated once, here, so its closure is not emitted twice.
			spilled, err := e.spillFunctionLiteralArgs(sig, args)
			if err != nil {
				return Value{}, err
			}
			switch {
			case inLib && !argsHoldFunctionLiteral(spilled):
				return e.emitOwnOrClassMethodCall(objTy, thisVal, methodName, spilled, sig.RetType, pos, e.memberWrittenFlag(methodName))
			case inProg:
				return e.emitOwnOrClassMethodCall(objTy, thisVal, methodName, spilled, sig.RetType, pos, "")
			}
			// (A spread ahead of a literal, in a library: the class's method.)
			args = spilled
		}
	}
	implementor := info.MethodImplementor[methodName]
	// TDD-00161 Stage 2: a decorated method's calls route through its runtime
	// slot so a decorator that replaced the descriptor's `value` takes effect.
	// A `super.method()` call (forceDirect) deliberately bypasses this — it
	// targets the parent implementation directly, not the instance's descriptor.
	if !forceDirect {
		if slot, ok := e.decoratedMethodSlots[implementor][methodName]; ok {
			return e.emitDecoratedMethodCall(objTy, thisVal, methodName, args, slot, pos)
		}
	}
	// Generator method (TDD-00063 Stage 2b): calling it constructs a generator
	// instance (receiver stored into __this, args into __paramN), not an
	// ordinary method call — the returned value is the generator, iterated via
	// the same .next()/for...of machinery a free-function generator uses.
	if !forceDirect && e.generatorOverridden(objTy.ClassName, methodName) {
		// A generator method one of its subclasses overrides (or one
		// overriding it as a generator): the receiver's own method, as
		// JavaScript looks it up.
		box, err := e.emitBoxValue(thisVal)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyMethodCall(box, methodName, args, pos)
	}
	if genInfo := info.GenMethodInfo[methodName]; genInfo != nil {
		return e.emitGeneratorConstructionWithThis(genInfo, thisVal.Ref, args, pos)
	}
	// regularCount excludes the rest slot itself (its own ParamTypes entry
	// is the declared array type, e.g. number[] — never one-to-one with a
	// single positional call argument). Found and fixed alongside tagged
	// template literals (TDD-00059): this whole function had no notion of
	// sig.HasRest at all — a class method with a rest parameter either hit
	// the strict arg-count check below (wrong count whenever the call
	// supplies anything other than exactly one rest argument) or, when the
	// count happened to match by coincidence, treated the rest slot's own
	// array *type* as if it applied to one positional scalar argument,
	// producing "expression does not yield an array". The free-function
	// call path (emitCallToFuncSig, emit_call.go) already gets this right;
	// this mirrors that function's own regularCount/rest-packing shape
	// rather than inventing a second one.
	regularCount := len(sig.ParamTypes)
	if sig.HasRest {
		regularCount--
	}
	// Spread arguments (TDD-00106): split as on every call path.
	args, doneSplit, serr := e.splitSpreadArgs(args, sig.HasRest, regularCount, pos)
	if serr != nil {
		return Value{}, serr
	}
	defer doneSplit()
	// minRequired excludes any trailing regular param that has a default
	// expression — found alongside the rest-param fix above: emitClassCall
	// had no default-value handling at all (sig.Defaults was written by
	// buildParamSig but never read anywhere in this file), so a class
	// method call omitting a trailing defaulted argument (real, common
	// TS/JS usage) hit this same strict arg-count check unconditionally.
	// The free-function call path (emitCallToFuncSig) already falls back
	// to evaluating the default expression at the call site; mirrored here.
	minRequired := regularCount
	for minRequired > 0 && ((minRequired-1 < len(sig.Defaults) && sig.Defaults[minRequired-1] != nil) ||
		(minRequired-1 < len(sig.Optional) && sig.Optional[minRequired-1])) {
		minRequired--
	}
	var extraArgs []ast.Expression
	if e.compatJS() && !sig.HasRest && len(args) > len(sig.ParamTypes) {
		extraArgs = args[len(sig.ParamTypes):]
		args = args[:len(sig.ParamTypes):len(sig.ParamTypes)]
	}
	if e.compatJS() {
		minRequired = 0 // untyped JavaScript may omit any argument
	}
	if sig.HasRest {
		if len(args) < minRequired {
			return Value{}, internalAt(pos, "arity: %s.%s expects at least %d argument(s), got %d", objTy.ClassName, methodName, minRequired, len(args))
		}
	} else if len(args) < minRequired || len(args) > len(sig.ParamTypes) {
		return Value{}, internalAt(pos, "arity: %s.%s expects %d argument(s), got %d", objTy.ClassName, methodName, len(sig.ParamTypes), len(args))
	}

	argParts := []string{irPtr + " " + thisVal.Ref}
	scratch := e.newParamDefaultScratch(sig.ParamNames)
	for i := 0; i < regularCount; i++ {
		paramTy := sig.ParamTypes[i]
		var a ast.Expression
		fromDefault := false
		switch {
		case i < len(args) && !undefinedFillsDefault(args[i], sig, i):
			a = args[i]
		case i < len(sig.Defaults) && sig.Defaults[i] != nil:
			a = sig.Defaults[i]
			fromDefault = true
		case i < len(sig.Optional) && sig.Optional[i] || e.compatJS():
			// ADR-00164: an omitted `param?: T` argument gets T's zero
			// value, the same undefined stand-in ADR-00157/ADR-00158 use.
			// Array-typed params decompose into two LLVM params (ptr, i64
			// len) at the callee side, so their "zero value" is an empty
			// array (null ptr, 0 len), not a single zeroLiteral() operand.
			if paramTy.IsArray() {
				argParts = append(argParts, irPtr+" "+e.omittedArrayArgHeader(paramTy), "i64 0")
			} else if isNullableScalar(paramTy) {
				// An omitted optional `T | undefined` param is a genuinely
				// absent { i1, T } aggregate (present = false) — TDD-00187.
				argParts = append(argParts, nullableScalarStorageIR(paramTy)+" zeroinitializer")
				scratch.bindNullable(i, "zeroinitializer", paramTy)
			} else if paramTy.IsDynamic() {
				// An omitted `any` argument is the boxed undefined.
				undef := fmt.Sprintf("%d", nbUndefined)
				argParts = append(argParts, "i64 "+undef)
				scratch.bind(i, Value{Ref: undef, Ty: paramTy})
			} else {
				argParts = append(argParts, fmt.Sprintf("%s %s", paramTy.IR, paramTy.zeroLiteral()))
				scratch.bind(i, Value{Ref: paramTy.zeroLiteral(), Ty: paramTy})
			}
			continue
		default:
			return Value{}, internalAt(pos, "arity: %s.%s missing argument %d with no default", objTy.ClassName, methodName, i+1)
		}
		if !fromDefault {
			// An argument that may be undefined at run time takes the
			// default when it is.
			if v, ok, err := e.emitArgOrDefault(a, paramTy, sig, i, func() { scratch.enter(true) }, func() { scratch.leave(true) }); ok || err != nil {
				if err != nil {
					return Value{}, err
				}
				argParts = append(argParts, fmt.Sprintf("%s %s", v.Ty.IR, v.Ref))
				scratch.bind(i, v) // an any binds as its box
				continue
			}
		}
		// An array-typed method parameter decomposes into two LLVM params
		// (ptr, i64 len) at the callee side — emitClassMember's own
		// parameter-binding loop already does this (mirrors
		// emitFunctionDeclAs). This call-site had never been taught the
		// matching decomposition: found while wiring getters/setters
		// (setters can take an array-typed value), but real and
		// pre-existing independent of that — any class method call passing
		// an array argument (`f.addAll([1,2,3])`) was already a hard
		// clang-stage crash (`{ptr,i64}` where a single `ptr` operand was
		// expected), the exact same class of bug ADR-00105/ADR-00107 fixed
		// throughout emit_arrays_*.go for the free-function/array-of-arrays
		// case — this was simply the one remaining call site that hadn't
		// been touched, since no test exercised a class method with an
		// array parameter before now.
		if paramTy.IsArray() {
			scratch.enter(fromDefault)
			val, err := e.emitExprWithObjectHint(a, paramTy)
			scratch.leave(fromDefault)
			if err != nil {
				return Value{}, err
			}
			if !val.Ty.IsArray() && paramTy.Nullable() && (val.Ty.IsNull() || val.Ty.IsUndefined()) {
				// `null` for a `T[] | null` parameter: the absent (null) header.
				val = e.emitAbsentArrayValue(val.Ty, paramTy)
			}
			if !val.Ty.IsArray() {
				return Value{}, internalAt(a.GetPos(), "expression does not yield an array")
			}
			header, lenReg := e.packArrayArg(a, val, paramTy)
			argParts = append(argParts, irPtr+" "+header, "i64 "+lenReg)
			scratch.bindArray(i, header, paramTy)
			continue
		}
		// A nullable-scalar method parameter takes its boxed { i1, T }
		// aggregate (TDD-00064 Stage 3).
		if isNullableScalar(paramTy) {
			scratch.enter(fromDefault)
			agg, err := e.emitNullableScalarBoxedValue(a, paramTy)
			scratch.leave(fromDefault)
			if err != nil {
				return Value{}, err
			}
			argParts = append(argParts, fmt.Sprintf("%s %s", nullableScalarStorageIR(paramTy), agg))
			scratch.bindNullable(i, agg, paramTy)
			continue
		}
		// A function-typed parameter contextually types an untyped arrow /
		// function-expression argument's parameters from its own declared
		// signature (`bus.on("x", (a, b) => ...)`).
		// Every argument is built against its parameter's type, as the
		// free-function path does: an object literal into an `any` parameter
		// is a dynamic object, not a static one boxed.
		scratch.enter(fromDefault)
		val, err := e.emitExprWithObjectHint(a, paramTy)
		scratch.leave(fromDefault)
		if err != nil {
			return Value{}, err
		}
		// A dynamic/union parameter (the { i8, i64 } box) needs its argument
		// boxed, not coerced — same reasoning and shape as the free-function
		// (emit_call.go) and static-method (emitStaticMethodCall) paths. The
		// coerce backstop would box it too, but only the explicit path here
		// also validates a constrained union's member set.
		if paramTy.IsDynamic() {
			if paramTy.UnionMembers != nil {
				val = e.relayoutForUnion(val, paramTy)
			}
			if val, err = e.emitBoxValue(val); err != nil {
				return Value{}, err
			}
		} else if paramTy.IR != "" {
			val = e.convert(val, paramTy)
			if err := argReprMismatch(val, paramTy, a, i); err != nil {
				return Value{}, err
			}
		}
		argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
		scratch.bind(i, val) // an any binds as its box
	}
	// Extra arguments untyped JavaScript passes are evaluated, in
	// order, and dropped.
	for _, x := range extraArgs {
		if _, err := e.emitExpr(x); err != nil {
			return Value{}, err
		}
	}
	// Pack rest args into a temporary heap array — identical shape to
	// emitCallToFuncSig's own rest-packing (emit_call.go).
	if sig.HasRest {
		// An omitted optional parameter before the rest leaves it empty.
		var restArgs []ast.Expression
		if len(args) > regularCount {
			restArgs = args[regularCount:]
		}
		parts, err := e.packRestArgs(restArgs, sig.ParamTypes[len(sig.ParamTypes)-1])
		if err != nil {
			return Value{}, err
		}
		argParts = append(argParts, parts...)
	}
	e.noteArgc(e.methodsMayReadArgs() || sigCountsArgs(sig), args, regularCount)
	argsIR := strings.Join(argParts, ", ")

	slot := info.MethodDispatchSlot[methodName]
	if ovrTarget != "" {
		// The override's own body, which takes the arguments past the
		// inherited signature's (wideOverride).
		if sig.RetType.IR == "void" {
			e.emitInstr(fmt.Sprintf("call void @%s(%s)", ovrTarget, argsIR))
			return Value{Ty: TypeVoid}, nil
		}
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call %s @%s(%s)", reg, sig.RetType.LLVMRetType(), ovrTarget, argsIR))
		if sig.RetType.IsArray() {
			return e.arrayValueFromHeaderReg(reg, sig.RetType), nil
		}
		return Value{Ref: reg, Ty: e.thisRetType(sig, objTy)}, nil
	}
	if forceDirect || slot == nil || !slot.Virtual {
		if m := info.Methods[methodName]; m != nil && m.Body == nil {
			// An optional or abstract method no class in the chain
			// implements: the member is undefined, and calling it is a
			// TypeError naming the callee as written.
			what := methodName
			if me, ok := e.curCallee.(*ast.MemberExpression); ok && me.Property == methodName {
				what = calleeText(me)
			}
			e.emitThrowTypeError(what + " is not a function")
			e.emitLabel(e.freshLabel("optcall.dead"))
			if sig.RetType.IR == "void" {
				return Value{Ty: TypeVoid}, nil
			}
			return Value{Ref: zeroRef(sig.RetType), Ty: sig.RetType}, nil
		}
		llvmName := llvmSafeSymbol(info.MethodImplementor[methodName] + "_" + methodName)
		if sig.RetType.IR == "void" {
			e.emitInstr(fmt.Sprintf("call void @%s(%s)", llvmName, argsIR))
			return Value{Ty: TypeVoid}, nil
		}
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call %s @%s(%s)", reg, sig.RetType.LLVMRetType(), llvmName, argsIR))
		if sig.RetType.IsArray() {
			// Array return ABI is a header pointer (TDD-00213 Stage 3): deref + alias.
			return e.arrayValueFromHeaderReg(reg, sig.RetType), nil
		}
		return Value{Ref: reg, Ty: e.thisRetType(sig, objTy)}, nil
	}

	fnPtr := e.emitVirtualTarget(info, thisVal.Ref, slot.Index, methodName)
	if e.someClassLacks(methodName) {
		// A class leaving an abstract or optional method unimplemented has
		// an empty slot: calling it through one of its instances is a
		// TypeError.
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, null", isNull, fnPtr))
		missL, okL := e.freshLabel("vcall.missing"), e.freshLabel("vcall.ok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, missL, okL))
		e.emitLabel(missL)
		what := methodName
		if me, ok := e.curCallee.(*ast.MemberExpression); ok && me.Property == methodName {
			what = calleeText(me)
		}
		e.emitThrowTypeError(what + " is not a function")
		e.emitLabel(okL)
	}

	paramTyStrs := []string{irPtr}
	for _, p := range sig.ParamTypes {
		if p.IsArray() {
			paramTyStrs = append(paramTyStrs, irPtr, "i64")
			continue
		}
		paramTyStrs = append(paramTyStrs, storageIR(p))
	}
	fnTypePart := "(" + strings.Join(paramTyStrs, ", ") + ")"
	if sig.RetType.IR == "void" {
		e.emitInstr(fmt.Sprintf("call void %s %s(%s)", fnTypePart, fnPtr, argsIR))
		return Value{Ty: TypeVoid}, nil
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s %s %s(%s)", reg, sig.RetType.LLVMRetType(), fnTypePart, fnPtr, argsIR))
	if sig.RetType.IsArray() {
		// Array return ABI is a header pointer (TDD-00213 Stage 3): deref + alias.
		return e.arrayValueFromHeaderReg(reg, sig.RetType), nil
	}
	return Value{Ref: reg, Ty: e.thisRetType(sig, objTy)}, nil
}

// wideOverrides are, for a call on a className receiver passing more than
// the inherited signature's n parameters, the overrides an instance it holds
// may run that declare more (`m(x = 2)` over `m()`): each one's class.
func (e *Emitter) wideOverrides(className, methodName string, n int) []string {
	var out []string
	wide := func(k string) bool {
		own, ok := e.classes[k].OverrideSigs[methodName]
		return ok && len(own.ParamTypes) > n && !own.HasRest
	}
	if impl := e.classes[className].MethodImplementor[methodName]; impl != "" {
		if _, ok := e.classes[impl]; ok && wide(impl) {
			out = append(out, impl)
		}
	}
	names := make([]string, 0, len(e.classes))
	for name := range e.classes {
		names = append(names, name)
	}
	sort.Strings(names) // map order would make the IR differ build to build
	for _, k := range names {
		if k != className && e.classes[k].MethodImplementor[methodName] == k && e.classDerives(k, className) && wide(k) {
			out = append(out, k)
		}
	}
	return out
}

// ovrCallReq is a call emitClassCall makes to an override's own body.
type ovrCallReq struct {
	sig    FuncSig
	target string
}

// emitWideOverrideCall emits a call passing more arguments than the
// inherited signature (sig) declares, when some override the receiver may
// run declares them: the virtual slot's function names the override, whose
// own body takes every argument; any other implementation takes the
// inherited signature's, the rest evaluated and dropped, as in JavaScript.
func (e *Emitter) emitWideOverrideCall(objTy Type, thisVal Value, methodName string, args []ast.Expression, sig FuncSig, pos ast.Pos) (Value, bool, error) {
	ks := e.wideOverrides(objTy.ClassName, methodName, len(sig.ParamTypes))
	if len(ks) == 0 {
		return Value{}, false, nil
	}
	for _, a := range args {
		if _, spread := a.(*ast.SpreadElement); spread {
			return Value{}, false, nil
		}
	}
	info := e.classes[objTy.ClassName]
	callOwn := func(k string, args []ast.Expression) (Value, error) {
		e.ovrCall = &ovrCallReq{sig: e.classes[k].OverrideSigs[methodName], target: llvmSafeSymbol(k+"_"+methodName) + "__kml_ovr"}
		defer func() { e.ovrCall = nil }()
		return e.emitClassCall(e.classes[k].Ty, thisVal, methodName, args, pos, false)
	}
	slot := info.MethodDispatchSlot[methodName]
	if len(ks) == 1 && ks[0] == info.MethodImplementor[methodName] && !e.overriddenBelow(objTy.ClassName, methodName) {
		v, err := callOwn(ks[0], args)
		return v, true, err
	}
	if slot == nil || !slot.Virtual {
		return Value{}, false, nil
	}
	// Arguments evaluated once, in order, ahead of the dispatch.
	bound := make([]ast.Expression, len(args))
	for i, a := range args {
		v, err := e.emitExpr(a)
		if err != nil {
			return Value{}, true, err
		}
		bound[i] = e.bindValue(v, a.GetPos())
	}
	fnPtr := e.emitVirtualTarget(info, thisVal.Ref, slot.Index, methodName)
	resTy := e.classes[ks[0]].OverrideSigs[methodName].RetType
	var resSlot string
	if resTy.IR != "void" {
		resSlot = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", resSlot, resTy.LLVMRetType()))
	}
	store := func(v Value) {
		if resSlot == "" {
			return
		}
		v = e.convert(v, resTy)
		e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align 8", resTy.LLVMRetType(), v.Ref, resSlot))
	}
	end := e.freshLabel("wideovr.end")
	for _, k := range ks {
		yes, no := e.freshLabel("wideovr.own"), e.freshLabel("wideovr.next")
		c := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, addrspacecast (ptr @%s to "+irPtr+")", c, fnPtr, llvmSafeSymbol(k+"_"+methodName)))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", c, yes, no))
		e.emitLabel(yes)
		v, err := callOwn(k, bound)
		if err != nil {
			return Value{}, true, err
		}
		store(v)
		e.emitTerminator("br label %" + end)
		e.emitLabel(no)
	}
	// Any other implementation: the inherited signature's arguments.
	e.noWideOverride = true
	v, err := e.emitClassCall(objTy, thisVal, methodName, bound[:len(sig.ParamTypes)], pos, false)
	e.noWideOverride = false
	if err != nil {
		return Value{}, true, err
	}
	store(v)
	e.emitTerminator("br label %" + end)
	e.emitLabel(end)
	if resSlot == "" {
		return Value{Ty: TypeVoid}, true, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align 8", r, resTy.LLVMRetType(), resSlot))
	if resTy.IsArray() {
		return e.arrayValueFromHeaderReg(r, resTy), true, nil
	}
	return Value{Ref: r, Ty: taskTaggedRet(e.classes[ks[0]].OverrideSigs[methodName])}, true, nil
}

// overriddenBelow reports whether a class derived from className implements
// methodName itself.
func (e *Emitter) overriddenBelow(className, methodName string) bool {
	impl := e.classes[className].MethodImplementor[methodName]
	for name, ci := range e.classes {
		if name != className && ci.MethodImplementor[methodName] != impl && e.classDerives(name, className) {
			return true
		}
	}
	return false
}

// thisRetType is a method call's result type: the receiver's class for a
// method returning the polymorphic `this` it inherits, else its own.
func (e *Emitter) thisRetType(sig FuncSig, recv Type) Type {
	if sig.RetThis && recv.IsClass() && sig.RetType.IsClass() && recv.ClassName != sig.RetType.ClassName &&
		e.classDerives(recv.ClassName, sig.RetType.ClassName) {
		r := recv
		r.setNullable(false)
		r.setUndefined(false)
		r.setKind(kindNull, false)
		return r
	}
	return taskTaggedRet(sig)
}

// emitClassMethodCall emits `objExpr.methodName(args)` — objExpr is
// evaluated generically via emitExpr (so this works on any expression, not
// just a bare identifier), then dispatch is delegated to emitClassCall.
func (e *Emitter) emitClassMethodCall(objTy Type, objExpr ast.Expression, methodName string, args []ast.Expression, pos ast.Pos) (Value, error) {
	thisVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	// A function literal argument stays with the typed call, which inlines
	// what it can see of it.
	if tags := e.foreignMethodTags(objTy, methodName); tags != nil && !argsHoldFunctionLiteral(args) {
		return e.emitClassCallOrForeign(objTy, thisVal, methodName, args, pos, tags)
	}
	return e.emitClassCall(objTy, thisVal, methodName, args, pos, false)
}

// foreignMethodTags is the header words of objTy's class and its
// subclasses, when another class of the program also has a method named
// methodName: a value typed as the class may be another class's instance
// (a cast through any, a structurally compatible class), and the method a
// call runs is the instance's own, by name, as JavaScript looks it up. nil
// when no other class has the name (the call is the typed class's).
func (e *Emitter) foreignMethodTags(objTy Type, methodName string) []int64 {
	return e.foreignClassTags(objTy, func(ci ClassInfo) bool {
		_, has := ci.Methods[methodName]
		return has
	})
}

// foreignFieldTags is foreignMethodTags for a field read or write through a
// receiver other than `this`: an unrelated class of the program lays out the
// slot differently (another field there, or none), so a value typed as the
// class but holding that class's instance (a cast through any) must be read
// by name, as JavaScript does.
func (e *Emitter) foreignFieldTags(recv ast.Expression, objTy Type, name string) []int64 {
	if _, isThis := recv.(*ast.ThisExpression); isThis {
		return nil
	}
	if id, ok := recv.(*ast.Identifier); ok {
		if sym, ok := e.lookup(id.Name); ok && e.exactClassSlots[sym.Ptr] {
			return nil
		}
	}
	idx, fty, ok := objTy.FieldIndex(name)
	if !ok {
		return nil
	}
	return e.foreignClassTags(objTy, func(ci ClassInfo) bool {
		i2, t2, ok := ci.Ty.FieldIndex(name)
		return !ok || i2 != idx || t2.IR != fty.IR
	})
}

// libraryForeignClasses are the builtin library's classes a value typed as
// another library class may hold: a cluster worker's `worker.process` is
// typed ChildProcess but is the process object itself in the worker, as in
// Node (lib/node/cluster.ts).
var libraryForeignClasses = map[string]map[string]bool{
	"ChildProcess": {"ProcessObject": true},
}

// foreignClassTags is the header words of objTy's class and its
// subclasses, when an unrelated class of the program satisfies has; nil
// otherwise.
func (e *Emitter) foreignClassTags(objTy Type, has func(ClassInfo) bool) []int64 {
	if !objTy.IsClass() || objTy.IsDynamic() || !hasObjHeader(objTy) || e.programIndependent() || e.inLib != "" {
		return nil
	}
	base := objTy.ClassName
	// The builtin library's classes are not cast between, except the pairs
	// libraryForeignClasses names: otherwise only the program's own classes
	// take the check.
	libForeign := libraryForeignClasses[ast.Unmangle(base)]
	if ast.IsLibraryName(base) && libForeign == nil {
		return nil
	}
	if _, ok := e.classes[base]; !ok {
		return nil
	}
	var tags []int64
	other := false
	names := make([]string, 0, len(e.classes))
	for n := range e.classes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		ci := e.classes[n]
		if n == base || e.classDerives(n, base) {
			tags = append(tags, e.objHeaderWord(ci.Ty))
			continue
		}
		if e.classDerives(base, n) {
			continue // an ancestor's instance is not typed as the class
		}
		if ast.IsLibraryName(n) && !libForeign[ast.Unmangle(n)] {
			continue
		}
		if has(ci) {
			other = true
		}
	}
	if !other {
		return nil
	}
	return tags
}

// emitClassHeaderIs branches on whether objVal (non-null) carries one of
// tags, as emitClassCallOrForeign does; a null receiver counts as own, so
// its error is the typed access's. It returns the i1 register.
func (e *Emitter) emitClassHeaderIs(thisVal Value, tags []int64) string {
	ownSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", ownSlot))
	e.emitInstr(fmt.Sprintf("store i1 true, "+irPtr+" %s, align 1", ownSlot))
	hdrL, testL := e.freshLabel("clsfld.hdr"), e.freshLabel("clsfld.test")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(thisVal.Ref), testL, hdrL))
	e.emitLabel(hdrL)
	h := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", h, thisVal.Ref))
	h = e.headerIdentity(h)
	acc := "false"
	for _, t := range tags {
		c, o := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", c, h, t))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", o, acc, c))
		acc = o
	}
	e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", acc, ownSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", testL))
	e.emitLabel(testL)
	own := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", own, ownSlot))
	return own
}

// emitClassFieldOrForeign reads a field through the class's layout when the
// instance is the class's (or a subclass's), and otherwise the instance's
// own property by name, as JavaScript looks it up.
func (e *Emitter) emitClassFieldOrForeign(objVal Value, prop string, fieldTy Type, pos ast.Pos, tags []int64, own func() (Value, error)) (Value, error) {
	isOwn := e.emitClassHeaderIs(objVal, tags)
	ownL, forL, doneL := e.freshLabel("clsfld.own"), e.freshLabel("clsfld.foreign"), e.freshLabel("clsfld.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isOwn, ownL, forL))
	e.emitLabel(ownL)
	ov, err := own()
	if err != nil {
		return Value{}, err
	}
	resTy := ov.Ty
	valIR := genElemValIR(resTy)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, valIR))
	e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align 8", valIR, ov.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(forL)
	box, err := e.emitBoxValue(objVal)
	if err != nil {
		return Value{}, err
	}
	dv, err := e.emitDynAnyMemberGetNamed(box, e.internString(prop), prop, pos)
	if err != nil {
		return Value{}, err
	}
	cv := e.convert(dv, resTy)
	e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align 8", valIR, cv.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align 8", r, valIR, slot))
	return Value{Ref: r, Ty: resTy}, nil
}

// emitClassFieldWriteOrForeign is emitClassFieldOrForeign for an
// assignment to the field: own writes through the class's layout, and a
// foreign instance takes the write as its own property by name.
func (e *Emitter) emitClassFieldWriteOrForeign(objVal Value, prop string, ex *ast.AssignmentExpression, tags []int64, own func() (Value, error)) (Value, error) {
	isOwn := e.emitClassHeaderIs(objVal, tags)
	ownL, forL, doneL := e.freshLabel("clsfld.own"), e.freshLabel("clsfld.foreign"), e.freshLabel("clsfld.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isOwn, ownL, forL))
	e.emitLabel(ownL)
	ov, err := own()
	if err != nil {
		return Value{}, err
	}
	resTy := ov.Ty
	var slot, valIR string
	if resTy.IR != "" && resTy.IR != "void" && ov.Ref != "" {
		valIR = genElemValIR(resTy)
		slot = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, valIR))
		e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align 8", valIR, ov.Ref, slot))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(forL)
	box, err := e.emitBoxValue(objVal)
	if err != nil {
		return Value{}, err
	}
	var dv Value
	if ex.Op != "=" {
		dv, err = e.emitDynAnyCompoundAssign(box, e.internString(prop), prop, ex.Op, ex.Right, ex.GetPos())
	} else {
		var rhs Value
		rhs, err = e.emitExpr(ex.Right)
		if err == nil {
			dv, err = e.emitDynAnyMemberSetNamed(box, e.internString(prop), prop, rhs, ex.GetPos())
		}
	}
	if err != nil {
		return Value{}, err
	}
	if slot != "" {
		cv := e.convert(dv, resTy)
		e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align 8", valIR, cv.Ref, slot))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	if slot == "" {
		return Value{Ty: resTy}, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align 8", r, valIR, slot))
	return Value{Ref: r, Ty: resTy}, nil
}

// emitClassCallOrForeign calls the class's method when the instance's
// header is one of tags (the class or a subclass, or a null receiver, whose
// error is the typed call's), and otherwise the instance's own method by
// name, through the dynamic call.
func (e *Emitter) emitClassCallOrForeign(objTy Type, thisVal Value, methodName string, args []ast.Expression, pos ast.Pos, tags []int64) (Value, error) {
	if thisVal.Ty.IsDynamic() && thisVal.Ty.IR == "i64" {
		thisVal = e.convert(thisVal, objTy) // read through the generic getter (emitClassCall)
	}
	// Only one arm runs, so each argument is evaluated once (none is a
	// function literal, whose closure each arm would emit).
	ownSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", ownSlot))
	e.emitInstr(fmt.Sprintf("store i1 true, "+irPtr+" %s, align 1", ownSlot))
	hdrL, testL := e.freshLabel("clsfor.hdr"), e.freshLabel("clsfor.test")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(thisVal.Ref), testL, hdrL))
	e.emitLabel(hdrL)
	h := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", h, thisVal.Ref))
	h = e.headerIdentity(h)
	acc := "false"
	for _, t := range tags {
		c, o := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", c, h, t))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", o, acc, c))
		acc = o
	}
	e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", acc, ownSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", testL))
	e.emitLabel(testL)
	own := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", own, ownSlot))
	ownL, forL, doneL := e.freshLabel("clsfor.own"), e.freshLabel("clsfor.foreign"), e.freshLabel("clsfor.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", own, ownL, forL))
	e.emitLabel(ownL)
	sv, err := e.emitClassCall(objTy, thisVal, methodName, args, pos, false)
	if err != nil {
		return Value{}, err
	}
	retTy := sv.Ty
	var slot, valIR string
	if retTy.IR != "" && retTy.IR != "void" {
		valIR = genElemValIR(retTy)
		slot = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, valIR))
		e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align 8", valIR, sv.Ref, slot))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(forL)
	box, err := e.emitBoxValue(thisVal)
	if err != nil {
		return Value{}, err
	}
	dv, err := e.emitDynAnyMethodCall(box, methodName, args, pos)
	if err != nil {
		return Value{}, err
	}
	if slot != "" {
		cv := e.convert(dv, retTy)
		e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align 8", valIR, cv.Ref, slot))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	if slot == "" {
		return Value{Ty: retTy}, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align 8", r, valIR, slot))
	return Value{Ref: r, Ty: retTy}, nil
}

// emitOwnOrClassMethodCall calls the instance's own property of the
// method's name when it has one (`obj.m = fn`), its class's method
// otherwise. A non-empty flag (memberWrittenFlag) gates the lookup: an i8
// global, nonzero when the program assigns the name.
func (e *Emitter) emitOwnOrClassMethodCall(objTy Type, thisVal Value, methodName string, args []ast.Expression, retTy Type, pos ast.Pos, flag string) (Value, error) {
	e.ensureShapeRuntime()
	ownL, classL, doneL := e.freshLabel("ownmeth.own"), e.freshLabel("ownmeth.class"), e.freshLabel("ownmeth.done")
	var slot string
	valIR := genElemValIR(retTy) // a register's form: an array's {ptr, i64}
	if retTy.IR != "" && retTy.IR != "void" {
		slot = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, valIR))
	}
	if flag != "" {
		fv := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i8, "+irPtr+" %s, align 1", fv, flag))
		set := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i8 %s, 0", set, fv))
		checkL := e.freshLabel("ownmeth.check")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", set, checkL, classL))
		e.emitLabel(checkL)
	}
	// An own property of the name, or a prototype the instance was given
	// resolving it before its class's.
	has := e.freshReg()
	e.declareFn("__kml_shape_method_shadowed", "declare i32 @__kml_shape_method_shadowed("+irPtr+", "+irPtr+")")
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_shape_method_shadowed("+irPtr+" %s, "+irPtr+" %s)", has, thisVal.Ref, e.internString(methodName)))
	own := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", own, has))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", own, ownL, classL))
	e.emitLabel(ownL)
	box, err := e.emitBoxValue(thisVal)
	if err != nil {
		return Value{}, err
	}
	// The own property, called as a plain function value: no builtin's
	// method of the same name applies to it.
	dv, err := e.emitDynAnyMethodCallPlain(box, methodName, args, pos)
	if err != nil {
		return Value{}, err
	}
	if slot != "" {
		cv := e.convert(dv, retTy)
		e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align 8", valIR, cv.Ref, slot))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(classL)
	e.inOwnMethodCheck = true
	sv, err := e.emitClassCall(objTy, thisVal, methodName, args, pos, false)
	e.inOwnMethodCheck = false
	if err != nil {
		return Value{}, err
	}
	if slot != "" {
		e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align 8", valIR, sv.Ref, slot))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	if slot == "" {
		return Value{Ty: TypeVoid}, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align 8", r, valIR, slot))
	return Value{Ref: r, Ty: sv.Ty}, nil
}

// spillFunctionLiteralArgs evaluates, typed by their parameters and in
// order, the arguments up to the last one holding a function literal (the
// literal itself or `{ cb: () => … }`) into locals, and returns the
// arguments with those replaced by references to them: each is evaluated
// once, before either arm of the own-method check, left to right as
// JavaScript evaluates them. The check itself reads no getter, so nothing
// observes the arguments running ahead of it.
func (e *Emitter) spillFunctionLiteralArgs(sig FuncSig, args []ast.Expression) ([]ast.Expression, error) {
	params := sig.ParamTypes
	if sig.This && len(params) > 0 {
		params = params[1:]
	}
	last := -1
	for i, a := range args {
		if argsHoldFunctionLiteral([]ast.Expression{a}) {
			last = i
		}
	}
	var out []ast.Expression
	for i, a := range args {
		if i > last {
			break
		}
		if _, isSpread := a.(*ast.SpreadElement); isSpread {
			return args, nil // a spread keeps its place; the caller checks
		}
		if out == nil {
			out = append([]ast.Expression(nil), args...)
		}
		var hint Type
		if i < len(params) {
			hint = params[i]
		}
		v, err := e.emitExprWithObjectHint(a, hint)
		if err != nil {
			return nil, err
		}
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, genElemValIR(v.Ty)))
		e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align 8", genElemValIR(v.Ty), v.Ref, slot))
		name := "__kml_litarg$" + strings.TrimPrefix(slot, "%")
		e.define(name, Symbol{Ptr: slot, Ty: v.Ty})
		out[i] = ast.NewIdentifier(name, a.GetPos())
	}
	if out == nil {
		return args, nil
	}
	return out, nil
}

// argsHoldFunctionLiteral reports whether a call's arguments contain a
// function or class literal.
func argsHoldFunctionLiteral(args []ast.Expression) bool {
	found := false
	for _, a := range args {
		ast.Inspect(a, func(n ast.Node) bool {
			switch n.(type) {
			case *ast.ArrowFunction, *ast.FunctionExpression, *ast.ClassExpression:
				found = true
			}
			return !found
		})
	}
	return found
}

// memberWrittenFlag is the global a library call site reads to learn
// whether the program assigns a property named name: defined in the
// program's unit (its name carries no library module key), so the library
// units stay identical across programs.
func (e *Emitter) memberWrittenFlag(name string) string {
	// A symbol-keyed method's name (`@@iterator`) is escaped: `@` in an ELF
	// symbol name marks a version.
	var sym strings.Builder
	for _, c := range []byte(name) {
		if c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			sym.WriteByte(c)
		} else {
			fmt.Fprintf(&sym, "$%02x", c)
		}
	}
	g := fmt.Sprintf("@\"__kml_mw.%s\"", sym.String())
	if e.memberFlags == nil {
		e.memberFlags = map[string]bool{}
	}
	if !e.memberFlags[name] {
		e.memberFlags[name] = true
		v := 0
		if e.jsMemberWritten(name) {
			v = 1
		}
		e.emitGlobal(fmt.Sprintf("%s ="+irGlobalAS+" global i8 %d", g, v))
	}
	return g
}

// optionalMethodPresence reads an optional method (`m?(): T;`) as a value:
// the implementation's code pointer, or null when the instance's class does
// not implement it — `this.m != null`, `if (this.m)`. ok is false for any
// other member. A method is not otherwise a value yet.
func (e *Emitter) optionalMethodPresence(objVal Value, name string) (Value, bool) {
	info, ok := e.classes[objVal.Ty.ClassName]
	if !ok || !e.isOptionalMethod(objVal.Ty.ClassName, name) {
		return Value{}, false
	}
	ty := TypePtr
	ty.setNullable(true)
	ty.setUndefined(true)
	slot := info.MethodDispatchSlot[name]
	if slot == nil || !slot.Virtual || !info.HasVTable {
		if m := info.Methods[name]; m == nil || m.Body == nil {
			return Value{Ref: "null", Ty: ty}, true
		}
		return Value{Ref: "@" + llvmSafeSymbol(info.MethodImplementor[name]+"_"+name), Ty: ty}, true
	}
	fn := e.emitVirtualTarget(info, objVal.Ref, slot.Index, name)
	return Value{Ref: fn, Ty: ty}, true
}

// classMethodValue reports whether reading name off an instance of t reads
// a method as a value, which emitMember answers with the method's boxed
// function record: a class (no host base) whose layout has no such field,
// declaring or inheriting name as a method that is not optional.
func (e *Emitter) classMethodValue(t Type, name string) bool {
	if !t.IsClass() || t.Nullable() {
		return false
	}
	if _, _, ok := t.FieldIndex(name); ok {
		return false
	}
	info, ok := e.classes[t.ClassName]
	if !ok || info.HostBase != nil || e.isOptionalMethod(t.ClassName, name) {
		return false
	}
	for cls, i := t.ClassName, 0; cls != "" && i < 64; i++ {
		ci, ok := e.classes[cls]
		if !ok {
			return false
		}
		if _, ok := ci.MethodSigs[name]; ok {
			return true
		}
		if e.forwardMethods[forwardMethodKey(cls, name, false)] {
			return true // being registered: its signature comes later
		}
		cls = ci.BaseClass
	}
	return false
}

// classMethodSig is the signature of instance method name of class
// className, its own or an inherited one.
func (e *Emitter) classMethodSig(className, name string) (FuncSig, bool) {
	for cls, i := className, 0; cls != "" && i < 64; i++ {
		ci, ok := e.classes[cls]
		if !ok {
			break
		}
		if sig, ok := ci.MethodSigs[name]; ok {
			return sig, true
		}
		cls = ci.BaseClass
	}
	return FuncSig{}, false
}

// classGetterDecl is the getter reading name off an instance of t runs: the
// class's own or an inherited one, when its layout has no such field.
func (e *Emitter) classGetterDecl(t Type, name string) *ast.FunctionDeclaration {
	if !t.IsClass() || t.Nullable() {
		return nil
	}
	if _, _, ok := t.FieldIndex(name); ok {
		return nil
	}
	getter := accessorMethodName("get", name)
	for cls, i := t.ClassName, 0; cls != "" && i < 64; i++ {
		ci, ok := e.classes[cls]
		if !ok || ci.HostBase != nil {
			return nil
		}
		if m := ci.Methods[getter]; m != nil {
			return m
		}
		cls = ci.BaseClass
	}
	return nil
}

// isOptionalMethod reports whether class declares or inherits name as an
// optional method.
func (e *Emitter) isOptionalMethod(class, name string) bool {
	info, ok := e.classes[class]
	if !ok {
		return false
	}
	for _, c := range append([]string{class}, info.AncestorChain...) {
		if ci, ok := e.classes[c]; ok {
			if m := ci.Methods[name]; m != nil && m.IsOptional {
				return true
			}
		}
	}
	return false
}

// emitClassSetterCall invokes a setter (TDD-00030) whose single argument
// is already an evaluated Value rather than an unevaluated ast.Expression
// — unlike emitClassCall (which evaluates each of its own args itself via
// e.emitExpr), a setter's right-hand side needs its own type-dependent
// handling first (hint-aware coercion for a plain `=`, or a
// read-current-via-getter-then-compute for a compound op — see
// emit_exprs_assign.go's object-field-assignment branch), so there's no
// unevaluated expression left to hand emitClassCall by the time this runs.
// Deliberately duplicates emitClassCall's own direct-vs-vtable dispatch
// shape rather than a larger refactor splitting argument evaluation out of
// that function — kept small since a setter's arity is always exactly one,
// unlike a general method call's.
func (e *Emitter) emitClassSetterCall(objTy Type, thisVal Value, methodName string, argVal Value, pos ast.Pos) (Value, error) {
	info, ok := e.classes[objTy.ClassName]
	if !ok {
		return Value{}, internalAt(pos, "unknown class '%s'", objTy.ClassName)
	}
	sig, ok := info.MethodSigs[methodName]
	if !ok {
		return Value{}, internalAt(pos, "class '%s' has no method '%s'", objTy.ClassName, methodName)
	}
	implementor := info.MethodImplementor[methodName]
	// TDD-00161 Stage 5: a decorated setter routes through its slot (the getter
	// already routes via emitClassCall's decorated-method path).
	if slot, ok := e.decoratedMethodSlots[implementor][methodName]; ok {
		boxedArg, err := e.emitBoxValue(argVal)
		if err != nil {
			return Value{}, err
		}
		argv := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca [1 x i64], align 8", argv))
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", boxedArg.Ref, argv))
		fnBox := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", fnBox, slot))
		if _, err := e.emitDynFnBoxCall(Value{Ref: fnBox, Ty: TypeAny}, thisVal, argv, 1, "setter is not a function", pos); err != nil {
			return Value{}, err
		}
		return Value{Ref: "0", Ty: TypeVoid}, nil
	}

	// An array-typed setter parameter decomposes into two LLVM call
	// operands (ptr, i64 len) at this call site, matching both
	// emitClassMember's own parameter-binding ABI and emitClassCall's
	// identical decomposition (see that function's own doc comment for why
	// this matters — a real, pre-existing bug this same TDD's
	// investigation found and fixed there).
	paramTy := sig.ParamTypes[0]
	var argParts []string
	var fnTypePart string
	if paramTy.IsArray() {
		if !argVal.Ty.IsArray() {
			return Value{}, internalAt(pos, "expression does not yield an array")
		}
		ptrReg := e.freshReg()
		lenReg := e.freshReg()
		lenReg = e.arrayLen(argVal)
		_ = ptrReg
		header := e.boxArrayValue(argVal)
		argParts = []string{irPtr + " " + thisVal.Ref, irPtr + " " + header, "i64 " + lenReg}
		fnTypePart = "(" + irPtr + ", " + irPtr + ", i64)"
	} else if isNullableScalar(paramTy) {
		// Nullable-scalar setter parameter (TDD-00064 Stage 3): its boxed
		// { i1, T } aggregate, with the matching storage type in the fn type.
		agg := e.boxNullableScalarFromValue(argVal, paramTy)
		st := nullableScalarStorageIR(paramTy)
		argParts = []string{irPtr + " " + thisVal.Ref, fmt.Sprintf("%s %s", st, agg)}
		fnTypePart = fmt.Sprintf("("+irPtr+", %s)", st)
	} else {
		v := e.convert(argVal, paramTy)
		if v.Ty.IR != paramTy.IR {
			return Value{}, internalAt(pos, "type mismatch in assignment — a value of one type cannot be used where an incompatible type is expected")
		}
		argParts = []string{irPtr + " " + thisVal.Ref, fmt.Sprintf("%s %s", v.Ty.IR, v.Ref)}
		fnTypePart = fmt.Sprintf("("+irPtr+", %s)", paramTy.IR)
	}
	argsIR := strings.Join(argParts, ", ")

	slot := info.MethodDispatchSlot[methodName]
	if slot == nil || !slot.Virtual {
		llvmName := llvmSafeSymbol(implementor + "_" + methodName)
		e.emitInstr(fmt.Sprintf("call void @%s(%s)", llvmName, argsIR))
		return Value{Ty: TypeVoid}, nil
	}

	fnPtr := e.emitVirtualTarget(info, thisVal.Ref, slot.Index, methodName)

	e.emitInstr(fmt.Sprintf("call void %s %s(%s)", fnTypePart, fnPtr, argsIR))
	return Value{Ty: TypeVoid}, nil
}

// emitStaticMethodCall evaluates `ClassName.staticMethod(args)` (TDD-00009
// Stage 4): always a direct call, never virtual — a bare class-name
// receiver is never polymorphic (there's no "value of static type X
// actually holding a Y instance" scenario for a class *name*), so
// StaticMethodImplementor alone (inherited-then-overridden, same shape
// MethodImplementor uses for instance methods) is always the right target.
func (e *Emitter) emitStaticMethodCall(info ClassInfo, className, methodName string, args []ast.Expression, pos ast.Pos) (Value, error) {
	sig, ok := info.StaticMethodSigs[methodName]
	if ok && e.staticMethodOverridable(className, methodName) {
		return e.emitStaticMethodOverrideCall(info, className, methodName, args, pos)
	}
	if ok && sig.This {
		// Its receiver is the class the call names (`D.m()` passes D).
		recv := ast.NewAsExpression(ast.NewIdentifier(className, pos), ast.AnyAnnotation(""), pos)
		args = append([]ast.Expression{recv}, args...)
	}
	if ok {
		trimmed, err := e.dropExtraArgs(sig, args)
		if err != nil {
			return Value{}, err
		}
		args = trimmed
	}
	if !ok && info.HostBase != nil && info.HostBase.IsPromise() {
		if v, handled, err := e.emitPromiseSubclassStatic(className, methodName, args, pos); handled {
			return v, err
		}
	}
	if !ok && info.HostBase != nil && info.HostBase.IsArray() {
		if v, handled, err := e.emitArrayClassStatic(className, methodName, args, pos); handled {
			return v, err
		}
	}
	if !ok && methodName == "toString" && len(args) == 0 {
		// Function.prototype.toString of the constructor: its source text.
		ref := e.classRefPtr(className)
		e.ensureFnMeta()
		e.declareFn("__kml_classref_tostring", "declare "+irPtr+" @__kml_classref_tostring("+irPtr+")")
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_classref_tostring("+irPtr+" %s)", r, ref))
		return Value{Ref: r, Ty: TypePtr}, nil
	}
	if ft, isField := info.StaticFieldTypes[methodName]; !ok && isField && (ft.IsFunc() || isUnconstrainedDynamic(ft)) {
		// A static field holding a function (a local a static member
		// captures, closureConvertClass): call its value.
		fv, err := e.emitStaticFieldRead(info, className, methodName, pos)
		if err != nil {
			return Value{}, err
		}
		if !ft.IsFunc() {
			return e.emitDynCallValue(fv, args, pos)
		}
		return e.emitClosureCallByPtr(fv.Ref, fv.Ty, args, pos)
	}
	if _, isGetter := info.StaticMethodSigs["__kml_get_"+methodName]; !ok && isGetter {
		// A static getter's value, called (`C.f(x)` with `static get f()`).
		fv, err := e.emitExpr(ast.NewMemberExpression(ast.NewIdentifier(className, pos), methodName, pos))
		if err != nil {
			return Value{}, err
		}
		if fv.Ty.IsFunc() {
			return e.emitClosureCallByPtr(fv.Ref, fv.Ty, args, pos)
		}
		if !fv.Ty.IsDynamic() {
			if fv, err = e.emitBoxValue(fv); err != nil {
				return Value{}, err
			}
		}
		return e.emitDynCallValue(fv, args, pos)
	}
	if !ok && (methodName == "hasOwnProperty" || methodName == "propertyIsEnumerable") && len(args) == 1 && e.compatJS() {
		// Over the class object's own properties, its run-time ones too.
		proto := ast.NewMemberExpression(e.builtinIdent("Object", pos), "prototype", pos)
		fn := ast.NewMemberExpression(ast.NewMemberExpression(proto, methodName, pos), "call", pos)
		return e.emitExpr(ast.NewCallExpression(fn, []ast.Expression{ast.NewIdentifier(className, pos), args[0]}, pos))
	}
	if !ok && (methodName == "hasOwnProperty" || methodName == "propertyIsEnumerable") && len(args) == 1 {
		// Object.prototype's (`C.hasOwnProperty(k)`), over the class's own
		// keys: its static members, and for hasOwnProperty its length,
		// name and prototype; only its static fields are enumerable.
		keys := e.classOwnStaticKeys(className, methodName == "propertyIsEnumerable")
		kv, err := e.emitExpr(ast.NewTemplateLiteral([]string{"", ""}, []ast.Expression{args[0]}, pos))
		if err != nil {
			return Value{}, err
		}
		res := "false"
		for _, k := range keys {
			eq := e.emitStrEq(kv.Ref, e.internString(k))
			acc := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", acc, res, eq))
			res = acc
		}
		return Value{Ref: res, Ty: TypeBool}, nil
	}
	if !ok && e.extendsObject(className) {
		// One of Object's statics (`C.keys(o)`), inherited.
		call := ast.NewCallExpression(ast.NewMemberExpression(e.builtinIdent("Object", pos), methodName, pos), args, pos)
		if v, err := e.emitObjectStatic(methodName, call); err != errNotObjectStatic {
			return v, err
		}
		return e.emitExpr(call)
	}
	if !ok && (e.compatJS() || functionProtoMembers[methodName]) {
		// A class is an object: a method it was given at run time, or
		// Function.prototype's (`C.bind(null)`), called
		// with the class as its receiver (`C.k.call(C, …)`).
		fv, err := e.emitStaticFieldRead(info, className, methodName, pos)
		if err != nil {
			return Value{}, err
		}
		return e.emitClosureCallWithThis(fv, "call", append([]ast.Expression{ast.NewIdentifier(className, pos)}, args...), pos)
	}
	if !ok {
		return Value{}, internalAt(pos, "class '%s' has no static method '%s'", className, methodName)
	}
	implementor := info.StaticMethodImplementor[methodName]
	// Same three gaps found and fixed in emitClassCall (rest params, default
	// values, array-typed parameters) also existed here — a wholly separate
	// function for the static-call-site case, never given the matching
	// fixes. Mirrors emitClassCall's own regularCount/minRequired/rest-
	// packing/array-decomposition shape exactly rather than inventing a
	// third copy of the same logic.
	regularCount := len(sig.ParamTypes)
	if sig.HasRest {
		regularCount--
	}
	// Spread arguments (TDD-00106): split as on every call path.
	args, doneSplit, serr := e.splitSpreadArgs(args, sig.HasRest, regularCount, pos)
	if serr != nil {
		return Value{}, serr
	}
	defer doneSplit()
	minRequired := regularCount
	for minRequired > 0 && ((minRequired-1 < len(sig.Defaults) && sig.Defaults[minRequired-1] != nil) ||
		(minRequired-1 < len(sig.Optional) && sig.Optional[minRequired-1])) {
		minRequired--
	}
	var extraArgs []ast.Expression
	if e.compatJS() && !sig.HasRest && len(args) > len(sig.ParamTypes) {
		extraArgs = args[len(sig.ParamTypes):]
		args = args[:len(sig.ParamTypes):len(sig.ParamTypes)]
	}
	if e.compatJS() {
		minRequired = 0 // untyped JavaScript may omit any argument
	}
	if sig.HasRest {
		if len(args) < minRequired {
			return Value{}, internalAt(pos, "arity: %s.%s expects at least %d argument(s), got %d", className, methodName, minRequired, len(args))
		}
	} else if len(args) < minRequired || len(args) > len(sig.ParamTypes) {
		return Value{}, internalAt(pos, "arity: %s.%s expects %d argument(s), got %d", className, methodName, len(sig.ParamTypes), len(args))
	}

	var argParts []string
	scratch := e.newParamDefaultScratch(sig.ParamNames)
	for i := 0; i < regularCount; i++ {
		paramTy := sig.ParamTypes[i]
		var a ast.Expression
		fromDefault := false
		switch {
		case i < len(args) && !undefinedFillsDefault(args[i], sig, i):
			a = args[i]
		case i < len(sig.Defaults) && sig.Defaults[i] != nil:
			a = sig.Defaults[i]
			fromDefault = true
		case i < len(sig.Optional) && sig.Optional[i] || e.compatJS():
			// ADR-00164: an omitted `param?: T` argument gets T's zero
			// value, the same undefined stand-in ADR-00157/ADR-00158 use.
			// Array-typed params decompose into two LLVM params (ptr, i64
			// len) at the callee side, so their "zero value" is an empty
			// array (null ptr, 0 len), not a single zeroLiteral() operand.
			if paramTy.IsArray() {
				argParts = append(argParts, irPtr+" "+e.omittedArrayArgHeader(paramTy), "i64 0")
			} else if isNullableScalar(paramTy) {
				// An omitted optional `T | undefined` param is a genuinely
				// absent { i1, T } aggregate (present = false) — TDD-00187.
				argParts = append(argParts, nullableScalarStorageIR(paramTy)+" zeroinitializer")
				scratch.bindNullable(i, "zeroinitializer", paramTy)
			} else if paramTy.IsDynamic() {
				// An omitted `any` argument is the boxed undefined.
				undef := fmt.Sprintf("%d", nbUndefined)
				argParts = append(argParts, "i64 "+undef)
				scratch.bind(i, Value{Ref: undef, Ty: paramTy})
			} else {
				argParts = append(argParts, fmt.Sprintf("%s %s", paramTy.IR, paramTy.zeroLiteral()))
				scratch.bind(i, Value{Ref: paramTy.zeroLiteral(), Ty: paramTy})
			}
			continue
		default:
			return Value{}, internalAt(pos, "arity: %s.%s missing argument %d with no default", className, methodName, i+1)
		}
		if !fromDefault {
			// An argument that may be undefined at run time takes the
			// default when it is.
			if v, ok, err := e.emitArgOrDefault(a, paramTy, sig, i, func() { scratch.enter(true) }, func() { scratch.leave(true) }); ok || err != nil {
				if err != nil {
					return Value{}, err
				}
				argParts = append(argParts, fmt.Sprintf("%s %s", v.Ty.IR, v.Ref))
				scratch.bind(i, v) // an any binds as its box
				continue
			}
		}
		if paramTy.IsArray() {
			scratch.enter(fromDefault)
			val, err := e.emitExprWithObjectHint(a, paramTy)
			scratch.leave(fromDefault)
			if err != nil {
				return Value{}, err
			}
			if !val.Ty.IsArray() && paramTy.Nullable() && (val.Ty.IsNull() || val.Ty.IsUndefined()) {
				// `null` for a `T[] | null` parameter: the absent (null) header.
				val = e.emitAbsentArrayValue(val.Ty, paramTy)
			}
			if !val.Ty.IsArray() {
				return Value{}, internalAt(a.GetPos(), "expression does not yield an array")
			}
			header, lenReg := e.packArrayArg(a, val, paramTy)
			argParts = append(argParts, irPtr+" "+header, "i64 "+lenReg)
			scratch.bindArray(i, header, paramTy)
			continue
		}
		// Nullable-scalar parameter (TDD-00064 Stage 3): its boxed aggregate.
		if isNullableScalar(paramTy) {
			scratch.enter(fromDefault)
			agg, err := e.emitNullableScalarBoxedValue(a, paramTy)
			scratch.leave(fromDefault)
			if err != nil {
				return Value{}, err
			}
			argParts = append(argParts, fmt.Sprintf("%s %s", nullableScalarStorageIR(paramTy), agg))
			scratch.bindNullable(i, agg, paramTy)
			continue
		}
		// Every argument is built against its parameter's type, as
		// emitClassCall and the free-function path do (see ADR-00632).
		scratch.enter(fromDefault)
		val, err := e.emitExprWithObjectHint(a, paramTy)
		scratch.leave(fromDefault)
		if err != nil {
			return Value{}, err
		}
		// A dynamic/union parameter (the { i8, i64 } tag+payload box) needs
		// its argument *boxed* (insertvalue), not run through coerce: coerce
		// has no notion of boxing, and because IsInteger() reports true for
		// the box's aggregate IR it would otherwise misfire into an integer
		// `trunc i64 -> { i8, i64 }` (invalid IR, clang-stage failure).
		// Mirrors the free-function call path in emit_call.go, which already
		// handles this — this static-method call site had only the bare
		// coerce, silently breaking every static method with a union/any
		// parameter (e.g. the Test262 harness shim's `assert.sameValue`).
		if paramTy.IsDynamic() {
			if paramTy.UnionMembers != nil {
				val = e.relayoutForUnion(val, paramTy)
			}
			if val, err = e.emitBoxValue(val); err != nil {
				return Value{}, err
			}
		} else if paramTy.IR != "" {
			val = e.convert(val, paramTy)
			if err := argReprMismatch(val, paramTy, a, i); err != nil {
				return Value{}, err
			}
		}
		argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
		scratch.bind(i, val) // an any binds as its box
	}
	// Extra arguments untyped JavaScript passes are evaluated, in
	// order, and dropped.
	for _, x := range extraArgs {
		if _, err := e.emitExpr(x); err != nil {
			return Value{}, err
		}
	}
	if sig.HasRest {
		// An omitted optional parameter before the rest leaves it empty.
		var restArgs []ast.Expression
		if len(args) > regularCount {
			restArgs = args[regularCount:]
		}
		restTy := sig.ParamTypes[len(sig.ParamTypes)-1]
		elemTy := TypeI64
		if restTy.ElemType != nil {
			elemTy = *restTy.ElemType
		}
		if spread, ok := singleSpread(restArgs); ok && sameRestElem(e.typeOfExpr(spread.Arg), elemTy) {
			ptrReg, lenReg, _, err := e.resolveArrayForHOF(spread.Arg, spread.Arg.GetPos())
			if err != nil {
				return Value{}, err
			}
			restHdr := e.newArrayHeader(ptrReg, lenReg)
			argParts = append(argParts, irPtr+" "+restHdr, "i64 "+lenReg)
		} else if len(restArgs) == 0 {
			argParts = append(argParts, irPtr+" "+e.emptyArrayArgHeader(), "i64 0")
		} else if anySpread(restArgs) {
			dataReg, lenReg, err := e.emitRestArgBuffer(restArgs, elemTy)
			if err != nil {
				return Value{}, err
			}
			restHdr := e.newArrayHeader(dataReg, lenReg)
			argParts = append(argParts, irPtr+" "+restHdr, "i64 "+lenReg)
		} else {
			n := int64(len(restArgs))
			dataReg := e.freshReg()
			e.heapMallocInto(dataReg, elemAllocKind(elemTy), fmt.Sprint(n*int64(elemTy.Align())))
			for i, arg := range restArgs {
				val, err := e.emitExprWithObjectHint(arg, elemTy)
				if err != nil {
					return Value{}, err
				}
				val = e.convert(val, elemTy)
				gepReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %d", gepReg, elemTy.IR, dataReg, i))
				e.storeArrayElem(gepReg, elemTy, val)
			}
			restHdr := e.newArrayHeader(dataReg, fmt.Sprintf("%d", n))
			argParts = append(argParts, irPtr+" "+restHdr, fmt.Sprintf("i64 %d", n))
		}
	}
	e.noteArgc(e.methodsMayReadArgs() || sigCountsArgs(sig), args, regularCount)
	argsIR := strings.Join(argParts, ", ")

	llvmName := llvmSafeSymbol(staticMethodName(implementor, methodName))
	restore := e.passStaticReceiver(className)
	if sig.RetType.IR == "void" {
		e.emitInstr(fmt.Sprintf("call void @%s(%s)", llvmName, argsIR))
		restore()
		return Value{Ty: TypeVoid}, nil
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s @%s(%s)", reg, sig.RetType.LLVMRetType(), llvmName, argsIR))
	restore()
	if sig.RetType.IsArray() {
		// Array return ABI is a header pointer (TDD-00213 Stage 3): deref + alias.
		return e.arrayValueFromHeaderReg(reg, sig.RetType), nil
	}
	return Value{Ref: reg, Ty: taskTaggedRet(sig)}, nil
}

// emitSuperCall handles `super(args)` inside a derived class's constructor
// (TDD-00009 Stage 3): calls the base class's constructor as an ordinary
// subroutine call on the same instance, before the derived constructor's
// own field-init code runs. Never virtual — constructors have no dispatch
// concept — and resolved once via the enclosing class's BaseClass link.
// emitDroppedArgs evaluates call arguments no parameter receives, in order;
// a spread's operand is evaluated (and iterated, when it is not an array).
func (e *Emitter) emitDroppedArgs(args []ast.Expression) error {
	for _, a := range args {
		if sp, ok := a.(*ast.SpreadElement); ok {
			a = ast.NewArrayLiteral([]ast.Expression{sp}, sp.GetPos())
		}
		if _, err := e.emitExpr(a); err != nil {
			return err
		}
	}
	return nil
}

func (e *Emitter) emitSuperCall(ex *ast.CallExpression) (Value, error) {
	thisSym, ok := e.lookup("this")
	if !ok {
		return Value{}, internalAt(ex.GetPos(), "super(...) is only valid inside a constructor")
	}
	if thisSym.Ty.IsDynamic() && e.overrideThisStatic != nil {
		thisSym = *e.overrideThisStatic // `this` rebound by an earlier super()
	}
	info, ok := e.classes[thisSym.Ty.ClassName]
	if !ok || info.BaseClass == "" {
		return Value{}, internalAt(ex.GetPos(), "super(...) is only valid inside the constructor of a class with a base class")
	}
	if fn := fnBaseOf(info.BaseClass); fn != "" {
		// A constructor function's: its body run on the instance.
		// The instance itself: `this` is not yet bound while super() runs.
		tr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", tr, thisSym.Ptr))
		tb, err := e.emitBoxValue(Value{Ref: tr, Ty: thisSym.Ty})
		if err != nil {
			return Value{}, err
		}
		e.emitNewTargetFromInstance(tb, ex.GetPos())
		if e.valueBases[fn] {
			if err := e.emitValueBaseSuper(fn, tb, ex.Args, ex.GetPos()); err != nil {
				return Value{}, err
			}
			if e.compatJS() && !e.blockDone && !builtinValueBases[fn] {
				// What a constructor value built is `this`.
				e.emitOverrideThis(thisSym, info)
				if err := e.overrideErr; err != nil {
					e.overrideErr = nil
					return Value{}, err
				}
			}
		} else if _, err := e.emitProtoCtorRun(fn, tb, ex.Args, ex.GetPos()); err != nil {
			return Value{}, err
		}
		return Value{Ref: "0", Ty: TypeUndefined}, nil
	}
	// An error subclass's `super(message?)` (TDD-00155 Stage 6) stores the
	// message into the error-struct prefix; kind/name were already set at
	// construction. Error's synthetic root has no real constructor; a
	// subclass of an Error subclass calls its base's, as any class does.
	if info.IsErrorSubclass && e.isErrorRootKind(info.BaseClass) {
		if e.aggregateRooted(info) {
			// AggregateError's (errors, message, options).
			thisReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", thisReg, thisSym.Ptr))
			if len(ex.Args) == 0 {
				e.emitThrowTypeError("undefined is not iterable (cannot read property Symbol(Symbol.iterator))")
				return Value{Ref: "0", Ty: TypeUndefined}, nil
			}
			if err := e.emitAggregateErrors(thisReg, ex.Args[0]); err != nil {
				return Value{}, err
			}
			rest := *ex
			rest.Args = ex.Args[1:]
			ex = &rest
		}
		if len(ex.Args) > 2 {
			// Extra arguments: evaluated, then ignored.
			kept, err := e.dropArgsPast(ex.Args, 2, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
			trimmed := *ex
			trimmed.Args = kept
			ex = &trimmed
		}
		if len(ex.Args) == 2 {
			if err := e.emitErrorSecondArg(thisSym, info, ex.Args[1]); err != nil {
				return Value{}, err
			}
		}
		if len(ex.Args) >= 1 {
			msg, err := e.emitExpr(ex.Args[0])
			if err != nil {
				return Value{}, err
			}
			msgRef, err := e.emitErrorMessage(msg)
			if err != nil {
				return Value{}, err
			}
			msgStr := Value{Ref: msgRef, Ty: TypePtr}
			thisReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", thisReg, thisSym.Ptr))
			idx, _, okF := info.Ty.FieldIndex("message")
			if !okF {
				return Value{}, internalAt(ex.GetPos(), "error subclass without a message field")
			}
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gep, info.Ty.StructIR(), thisReg, idx))
			e.heapStore(irPtr, msgStr.Ref, gep, 8)
		}
		return Value{Ty: TypeVoid}, nil
	}
	if _, registered := e.classes[info.BaseClass]; !registered && info.HostBase != nil && info.HostBase.IsPromise() {
		// `super(executor)` of a class extending Promise runs it on its
		// body.
		thisReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", thisReg, thisSym.Ptr))
		body := e.freshReg()
		e.emitBodyAddr(body, thisReg)
		if err := e.emitHostPromiseBody(body, *info.HostBase, ex.Args, ex.GetPos()); err != nil {
			return Value{}, err
		}
		return Value{Ty: TypeVoid}, nil
	}
	if _, registered := e.classes[info.BaseClass]; !registered && info.HostBase != nil && info.HostBase.IsArray() {
		// `super(…)` of a class extending Array sets its elements, as
		// `new Array(…)` would.
		thisReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", thisReg, thisSym.Ptr))
		body := e.freshReg()
		e.emitBodyAddr(body, thisReg)
		if err := e.emitHostArrayBody(body, *info.HostBase, ex.Args, ex.GetPos()); err != nil {
			return Value{}, err
		}
		return Value{Ty: TypeVoid}, nil
	}
	if _, registered := e.classes[info.BaseClass]; !registered && info.HostBase != nil && (info.HostBase.IsPrimWrapper() || info.HostBase.IsArrayBuffer()) {
		// `super(…)` of a class extending Number, String or Boolean sets
		// its primitive, of one extending ArrayBuffer its buffer (an
		// implicit constructor's: set by the new expression already, from
		// its arguments).
		if info.ImplicitCtor {
			return Value{Ty: TypeVoid}, nil
		}
		thisReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", thisReg, thisSym.Ptr))
		body := e.freshReg()
		e.emitBodyAddr(body, thisReg)
		set := e.emitHostPrimBody
		if info.HostBase.IsArrayBuffer() {
			set = e.emitHostBufferBody
		}
		if err := set(body, *info.HostBase, ex.Args, ex.GetPos()); err != nil {
			return Value{}, err
		}
		return Value{Ty: TypeVoid}, nil
	}
	if _, registered := e.classes[info.BaseClass]; !registered && info.HostBase != nil && info.HostBase.IsDate() {
		// `super(…)` of a class extending Date sets its time value, as
		// `new Date(…)` would.
		thisReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", thisReg, thisSym.Ptr))
		body := e.freshReg()
		e.emitBodyAddr(body, thisReg)
		if err := e.emitHostDateBody(body, ex.Args, ex.GetPos()); err != nil {
			return Value{}, err
		}
		return Value{Ty: TypeVoid}, nil
	}
	if _, registered := e.classes[info.BaseClass]; !registered && info.HostBase != nil && info.HostBase.IsRegExp() {
		// `super(pattern?, flags?)` of a class extending RegExp compiles
		// its body.
		thisReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", thisReg, thisSym.Ptr))
		body := e.freshReg()
		e.emitBodyAddr(body, thisReg)
		if err := e.emitHostRegExpBody(body, ex.Args, ex.GetPos()); err != nil {
			return Value{}, err
		}
		return Value{Ty: TypeVoid}, nil
	}
	if _, registered := e.classes[info.BaseClass]; !registered && info.HostBase != nil {
		// `super(entries?)` of a class extending Map or Set: the body was
		// made empty at construction; entries seed it.
		if len(ex.Args) > 1 {
			// Extra arguments: evaluated, then ignored.
			kept, err := e.dropArgsPast(ex.Args, 1, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
			trimmed := *ex
			trimmed.Args = kept
			ex = &trimmed
		}
		if len(ex.Args) == 1 && !isUndefinedLiteral(e, ex.Args[0]) {
			thisReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", thisReg, thisSym.Ptr))
			body := e.freshReg()
			e.emitBodyAddr(body, thisReg)
			if err := e.emitHostBodySeed(body, *info.HostBase, ex.Args[0], ex.GetPos()); err != nil {
				return Value{}, err
			}
		}
		return Value{Ty: TypeVoid}, nil
	}
	baseInfo := e.classes[info.BaseClass]
	if baseInfo.Constructor == nil {
		// An implicit base constructor takes no parameters: the arguments
		// are evaluated, in order, and dropped (the strict lane has
		// refused any, as tsc does).
		if err := e.emitDroppedArgs(ex.Args); err != nil {
			return Value{}, err
		}
		return Value{Ty: TypeVoid}, nil
	}
	sig := baseInfo.CtorSig
	params := sig.ParamTypes
	if sig.HasRest {
		params = params[:len(params)-1] // the rest slot takes the remaining arguments
	}
	cargs, doneSplit, serr := e.splitSpreadArgs(ex.Args, sig.HasRest, len(params), ex.GetPos())
	if serr != nil {
		return Value{}, serr
	}
	defer doneSplit()
	var extra []ast.Expression
	if !sig.HasRest && len(cargs) > len(sig.ParamTypes) {
		// Arguments past the parameters are evaluated after them, then
		// dropped.
		cargs, extra = cargs[:len(sig.ParamTypes)], cargs[len(sig.ParamTypes):]
	}
	for i := len(cargs); i < len(params) && !e.compatJS(); i++ {
		// (In JavaScript a missing argument is undefined.)
		if !(i < len(sig.Optional) && sig.Optional[i]) {
			return Value{}, internalAt(ex.GetPos(), "arity: super(...) expects %d argument(s), got %d", len(sig.ParamTypes), len(cargs))
		}
	}

	thisReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", thisReg, thisSym.Ptr))
	argParts := []string{irPtr + " " + thisReg}
	for i, paramTy := range params {
		if i >= len(cargs) {
			// An omitted optional parameter is undefined (its type's zero,
			// or an absent aggregate), as `new` fills one.
			switch {
			case paramTy.IsArray():
				argParts = append(argParts, irPtr+" "+e.omittedArrayArgHeader(paramTy), "i64 0")
			case isNullableScalar(paramTy):
				argParts = append(argParts, nullableScalarStorageIR(paramTy)+" zeroinitializer")
			default:
				argParts = append(argParts, fmt.Sprintf("%s %s", paramTy.IR, paramTy.zeroLiteral()))
			}
			continue
		}
		a := cargs[i]
		if isNullableScalar(paramTy) {
			argStr, err := e.emitNullableScalarArg(a, paramTy)
			if err != nil {
				return Value{}, err
			}
			argParts = append(argParts, argStr)
			continue
		}
		// Built against the parameter's type, as a constructor argument is:
		// an object literal takes the options interface's layout.
		val, err := e.emitExprWithObjectHint(a, paramTy)
		if err != nil {
			return Value{}, err
		}
		if paramTy.IsArray() {
			header, lenReg := e.packArrayArg(a, val, paramTy)
			argParts = append(argParts, irPtr+" "+header, "i64 "+lenReg)
			continue
		}
		val = e.convert(val, paramTy)
		if err := argReprMismatch(val, paramTy, a, i); err != nil {
			return Value{}, err
		}
		argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
	}
	if err := e.emitDroppedArgs(extra); err != nil {
		return Value{}, err
	}
	if sig.HasRest {
		var restArgs []ast.Expression
		if len(cargs) > len(params) {
			restArgs = cargs[len(params):]
		}
		parts, err := e.packRestArgs(restArgs, sig.ParamTypes[len(sig.ParamTypes)-1])
		if err != nil {
			return Value{}, err
		}
		argParts = append(argParts, parts...)
	}
	if info.ImplicitCtor && e.argcOverride == "" {
		// The synthesized pass-through forwards the count its own caller
		// passed (still in @__kml_argc: its arguments are plain reads).
		fwd := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" @__kml_argc, align 8", fwd))
		e.argcOverride = fwd
		defer func() { e.argcOverride = "" }()
	}
	e.noteArgc(e.methodsMayReadArgs() || sigCountsArgs(sig), cargs, len(params))
	if e.ctorMayOverride(info.BaseClass) {
		if !e.compatJS() && !e.foreignCtorName(e.currentCtorClass) {
			// The base's returned instance would become this constructor's
			// `this`: an instance of the base's layout, which the derived
			// fields do not fit.
			return Value{}, internalAt(ex.GetPos(), "super() to a constructor that returns an instance (the checker reports it)")
		}
		e.emitInstr(fmt.Sprintf("call void @%s%s(%s)", info.BaseClass, ctorSymbolSuffix, strings.Join(argParts, ", ")))
		e.emitOverrideThis(thisSym, info)
		if err := e.overrideErr; err != nil {
			e.overrideErr = nil
			return Value{}, err
		}
		return Value{Ty: TypeVoid}, nil
	}
	e.emitInstr(fmt.Sprintf("call void @%s%s(%s)", info.BaseClass, ctorSymbolSuffix, strings.Join(argParts, ", ")))
	return Value{Ty: TypeVoid}, nil
}

// emitOverrideThis binds, after super() to a base whose constructor may
// return another object, `this` to that object, else to the instance
// allocated for the class (boxed either way): the constructor's code after
// super() runs on it, and the `new` site's value is it (overrideThis).
func (e *Emitter) emitOverrideThis(thisSym Symbol, info ClassInfo) {
	tr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", tr, thisSym.Ptr))
	inst, err := e.emitBoxValue(Value{Ref: tr, Ty: thisSym.Ty})
	if err != nil {
		return
	}
	ovAny, ovPtr := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" @__kml_ctor_ret_any, align 8", ovAny))
	e.emitInstr("store i64 0, " + irPtr + " @__kml_ctor_ret_any, align 8")
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" @__kml_ctor_ret, align 8", ovPtr))
	e.emitInstr("store " + irPtr + " null, " + irPtr + " @__kml_ctor_ret, align 8")
	// An instance of the base's layout it returned (its own class's).
	baseInst, err := e.emitBoxValue(Value{Ref: ovPtr, Ty: e.classes[info.BaseClass].Ty})
	if err != nil {
		return
	}
	e.ensureNanBox()
	e.declareFn("__kml_any_is_bigint", "declare i32 @__kml_any_is_bigint(i64)")
	tag, isHeap, big, isBig, notBig, obj, nz, isObj := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_nb_tag(i64 %s)", tag, ovAny))
	e.emitInstr(fmt.Sprintf("%s = icmp uge i8 %s, 6", isHeap, tag))
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_any_is_bigint(i64 %s)", big, ovAny))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", isBig, big))
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notBig, isBig))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", obj, isHeap, notBig))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", nz, ovAny))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", isObj, obj, nz))
	r1, hasPtr, r2 := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", r1, isObj, ovAny, inst.Ref))
	e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", hasPtr, ovPtr))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", r2, hasPtr, baseInst.Ref, r1))
	if e.overrideThis != "" {
		// Another super(): `this` keeps its value when already bound (the
		// ReferenceError that follows may be caught).
		if e.thisInitSlot != "" {
			b, old, v := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", b, e.thisInitSlot))
			e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", old, e.overrideThis))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", v, b, old, r2))
			r2 = v
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", r2, e.overrideThis))
		return
	}
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", r2, slot))
	st := thisSym
	e.overrideThis, e.overrideThisStatic = slot, &st
	e.define("this", Symbol{Ptr: slot, Ty: TypeAny})
	// The private names it gets before its field initializers run.
	if err := e.emitOverrideDefinitions(Value{Ref: r2, Ty: TypeAny}, e.currentCtorClass, ast.Pos{}); err != nil {
		e.overrideErr = err
	}
}

// emitOverrideThisReturn hands a constructor's `this` (emitOverrideThis)
// to its `new` site when it returns.
func (e *Emitter) emitOverrideThisReturn() {
	if e.overrideThis == "" || e.blockDone {
		return
	}
	v := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", v, e.overrideThis))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" @__kml_ctor_ret_any, align 8", v))
}

// emitSuperMethodCall handles `super.methodName(args)` (TDD-00009 Stage 3):
// an explicit call to the base class's own implementation, always direct
// (bypassing dispatch even if the method is Virtual — matching real JS/TS
// semantics for an explicit super call) via emitClassCall's forceDirect
// path. Relies on scope symbol "super" (bound by emitClassMember alongside
// "this", only inside a class with a base) to know both the underlying
// instance pointer and the base's own static type.
func (e *Emitter) emitSuperMethodCall(methodName string, args []ast.Expression, pos ast.Pos) (Value, error) {
	superSym, ok := e.lookup("super")
	if !ok {
		return Value{}, internalAt(pos, "super.%s(...) is only valid inside a method of a class with a base class", methodName)
	}
	e.emitThisBoundCheck() // super.m reads `this` first
	thisReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", thisReg, superSym.Ptr))
	if _, registered := e.classes[superSym.Ty.ClassName]; !registered {
		// The super of a class extending Map or Set is its host: the host's
		// method, on the instance's body (TDD-00247).
		if thisSym, ok := e.lookup("this"); ok {
			if info, ok := e.classes[thisSym.Ty.ClassName]; ok && info.HostBase != nil {
				return e.emitHostBaseCall(Value{Ref: thisReg, Ty: thisSym.Ty}, *info.HostBase, methodName, args, pos)
			}
		}
	}
	return e.emitClassCall(superSym.Ty, Value{Ref: thisReg, Ty: superSym.Ty}, methodName, args, pos, true)
}

// emitInstanceOf implements `x instanceof ClassName` (TDD-00009 Stages 2-3).
// The right-hand side is never evaluated as a general expression — it must
// be a bare identifier naming a registered user-defined class, since this
// compiler has no runtime constructor values to compare against (Error,
// Date, Response, and unregistered names are all rejected here with the same
// error, since none of them carry the class tag this checks).
//
// Five cases, ordered cheapest first — generalized for inheritance via
// AncestorChain and Descendants, both computed once in registerClasses:
//  1. Left is any/unknown: unbox, confirm the runtime tag is "object", then
//     compare the pointed-to instance's own hidden class tag against the
//     OR-chain of every TagID that would make it a T (T itself plus every
//     transitive descendant of T) — a deliberately simple OR-chain rather
//     than a range-encoded tag scheme; fine for realistic hierarchy sizes,
//     and a possible future optimization if that ever stops being true.
//  2. Left statically IsClass with a class C that is T itself or has T
//     somewhere in its AncestorChain: always true, unless the type is
//     nullable, in which case it reduces to a ptr-vs-null check.
//     2b. Left statically IsClass with a class C that has T somewhere in its
//     *Descendants* (e.g. `const s: Shape = new Circle(...); s instanceof
//     Circle`): NOT decidable at compile time — a C-typed value can hold
//     any concrete subtype of C at runtime — so this needs the same real
//     tag check as case 1, just without the unbox/is-object step (already
//     statically known to be an object in this hierarchy).
//  3. Left statically IsClass with a class C unrelated to T (neither an
//     ancestor nor a descendant of the other): constant false — single
//     inheritance, no diamond, so this stays sound.
//  4. Any other static type (number, string, bool, array, plain object,
//     Error, Date, Response, ...): constant false, matching real JS (a
//     non-object or non-matching-constructor value is never `instanceof`
//     anything).
//
// builtinInstanceofTypes maps a built-in type name usable on the right of
// `instanceof` to a predicate over the left side's own already-evaluated
// static Type (ADR-00162). Unlike errorKindIDs/e.classes above, none of
// these carry a runtime class tag to check against — this compiler's
// static typing already answers "is this an Array/Map/Set/Date/RegExp" at
// compile time, and there's no dynamic prototype-chain trickery here for a
// runtime check to ever disagree with — so the result is always a
// compile-time constant, the same reasoning emitInstanceOf's own case 3/4
// (a statically-mismatched user-class comparison) already uses.
//
// Doesn't include `Object` — `instanceof Object` is handled separately in
// emitInstanceOf (ADR-00605) by *negating* the closed set of primitive types
// (number/string/boolean/bigint/symbol/null/undefined) rather than enumerating
// object types: every remaining typed ptr value is a JS object, and an
// undecidable static type (any/union/nullable) keeps a clean rejection.
var builtinInstanceofTypes = map[string]func(Type) bool{
	"Array":                           func(t Type) bool { return t.IsArray() },
	"Map":                             func(t Type) bool { return t.IsMap() && !t.Weak },
	"Set":                             func(t Type) bool { return t.IsSet() && !t.Weak },
	"WeakMap":                         func(t Type) bool { return t.IsMap() && t.Weak },
	"WeakSet":                         func(t Type) bool { return t.IsSet() && t.Weak },
	"Date":                            func(t Type) bool { return t.IsDate() },
	"RegExp":                          func(t Type) bool { return t.IsRegExp() },
	"ReadableStream":                  func(t Type) bool { return t.IsReadableStream() },
	"WritableStream":                  func(t Type) bool { return t.IsWritableStream() },
	"TransformStream":                 func(t Type) bool { return t.IsTransformStream() },
	"ReadableStreamDefaultReader":     func(t Type) bool { return t.IsStreamReader() },
	"WritableStreamDefaultWriter":     func(t Type) bool { return t.IsStreamWriter() },
	"ReadableStreamDefaultController": func(t Type) bool { return t.IsRSController() },
	"WritableStreamDefaultController": func(t Type) bool { return t.IsWSController() },
	"Promise":                         func(t Type) bool { return t.IsPromise() },
	"Buffer":                          func(t Type) bool { return t.IsBuffer() },
}

// typedArrayNames are the TypedArray constructors: `x instanceof Int32Array`
// holds for a TypedArray of that element kind.
var typedArrayNames = map[string]bool{
	"Int8Array": true, "Uint8Array": true, "Uint8ClampedArray": true,
	"Int16Array": true, "Uint16Array": true, "Int32Array": true, "Uint32Array": true,
	"Float32Array": true, "Float64Array": true, "BigInt64Array": true, "BigUint64Array": true,
}

func init() {
	for name := range typedArrayNames {
		builtinInstanceofTypes[name] = func(t Type) bool {
			return t.IsTypedArray() && typedArrayConstructorName(t) == name
		}
	}
}

// builtinInstanceofMatch reports whether a value of static type t is an
// instance of the builtin class name.
func builtinInstanceofMatch(name string, t Type) bool {
	matches, ok := builtinInstanceofTypes[name]
	return ok && matches(t)
}

func (e *Emitter) emitInstanceOf(ex *ast.BinaryExpression) (Value, error) {
	if rid, ok := ex.Right.(*ast.Identifier); ok {
		if lt := e.typeOfExpr(ex.Left); lt.IsClass() && !lt.IsDynamic() && hostWrapperNames[rid.Name] {
			if li, ok := e.classes[lt.ClassName]; ok && li.HostBase != nil && (li.HostBase.IsPrimWrapper() || li.HostBase.IsArrayBuffer()) && !e.isUserClass(rid.Name) {
				// A class extending Number (String, Boolean, ArrayBuffer,
				// SharedArrayBuffer) is one.
				leftVal, err := e.emitExpr(ex.Left)
				if err != nil {
					return Value{}, err
				}
				if hostTagName(*li.HostBase) != rid.Name {
					return Value{Ref: "0", Ty: TypeBool}, nil
				}
				return Value{Ref: e.presentRef(leftVal), Ty: TypeBool}, nil
			}
		}
	}
	// A constructor held in a value (`x instanceof C`, C a parameter):
	// decided at run time.
	if e.instanceofNeedsRuntime(ex.Right) {
		return e.emitDynInstanceOf(ex)
	}
	rightIdent, ok := ex.Right.(*ast.Identifier)
	if !ok {
		return Value{}, internalAt(ex.GetPos(), "right-hand side of instanceof must be a class name")
	}
	// A class with a static [Symbol.hasInstance]: InstanceofOperator calls
	// it, its result made a boolean.
	if info, ok := e.classes[rightIdent.Name]; ok && !e.ordinaryInstanceof {
		if _, has := info.StaticMethodSigs["@@hasInstance"]; has {
			pos := ex.GetPos()
			call := ast.NewCallExpression(ast.NewMemberExpression(rightIdent, "@@hasInstance", pos), []ast.Expression{ex.Left}, pos)
			return e.emitExpr(ast.NewUnaryExpression("!", true, ast.NewUnaryExpression("!", true, call, pos), pos))
		}
	}
	// A caught value (TypeCaught ≈ `unknown`, TDD-00202): resolve instanceof off
	// the record's tag. A built-in Error kind checks the caught Error's kind;
	// `Object` is true for any non-primitive; any other class is not verifiable
	// from the packed record (a thrown user-class instance loses its identity).
	if e.typeOfExpr(ex.Left).IsCaught() {
		lv, err := e.emitExpr(ex.Left)
		if err != nil {
			return Value{}, err
		}
		if kindID, ok := errorKindIDs[rightIdent.Name]; ok {
			return e.emitCaughtInstanceOfError(lv, rightIdent.Name, kindID), nil
		}
		// A user `class X extends Error`: its TagID sits in the caught Error's
		// kind slot (TDD-00202), so compare against it.
		if info, ok := e.classes[rightIdent.Name]; ok && info.IsErrorSubclass {
			return e.emitCaughtInstanceOfClassTag(lv, info.TagID), nil
		}
		if rightIdent.Name == "Object" {
			tag, _ := e.caughtParts(lv)
			nonPrim := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ugt i8 %s, %d", nonPrim, tag, kmlTagUndefined))
			return Value{Ref: nonPrim, Ty: TypeBool}, nil
		}
		return Value{Ref: "0", Ty: TypeBool}, nil
	}
	if kindID, ok := errorKindIDs[rightIdent.Name]; ok {
		return e.emitErrorInstanceOf(ex, rightIdent.Name, kindID)
	}
	// A registered user-defined class always takes precedence over a
	// built-in name below, even one that happens to reuse a built-in's own
	// name (only reachable at all under `-compat=js` — ambient
	// global names are reserved by default) — the more specific, real
	// class registration a user explicitly wrote should never be silently
	// shadowed by this compiler's own fallback built-in handling.
	if _, generic := e.genericClasses[rightIdent.Name]; generic {
		if _, own := e.classes[rightIdent.Name]; !own {
			return e.emitGenericInstanceOf(ex.Left, rightIdent.Name)
		}
	}
	info, isClass := e.classes[rightIdent.Name]
	if !isClass {
		// `x instanceof Object` (ADR-00605): true for any non-primitive. The
		// primitive set is *closed* (number, string, boolean, bigint, symbol,
		// null, undefined), so negating it is drift-safe — every remaining typed
		// ptr value is genuinely a JS object. A value whose static type can't be
		// decided (any/union/nullable) keeps the clean rejection rather than
		// risking a wrong constant.
		if rightIdent.Name == "Object" {
			leftVal, err := e.emitExpr(ex.Left)
			if err != nil {
				return Value{}, err
			}
			t := leftVal.Ty
			if t.IsDynamic() || t.UnionMembers != nil || t.Nullable() {
				// Decided by the box: its prototype chain (a null-prototype
				// object is not one).
				b, err := e.emitBoxValue(leftVal)
				if err != nil {
					return Value{}, err
				}
				e.ensureAnyToPrimitive()
				e.ensureNanBox()
				e.ensureErrorNames()
				e.ensureStrHeaderRuntime()
				// A Proxy's prototype is its target's (no getPrototypeOf trap).
				src := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_proxy_proto_src(i64 %s)", src, b.Ref))
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call zeroext i1 @__kml_instanceof_object(i64 %s)", r, src))
				return Value{Ref: r, Ty: TypeBool}, nil
			}
			// A Date is an object (a `ptr` handle, which isForOfStringTy
			// already excludes; listed for clarity).
			if t.IsDate() {
				return Value{Ref: "1", Ty: TypeBool}, nil
			}
			isPrimitive := (t.IR != irPtr && t.IR != "void") || t.IsBigInt() || t.IsSymbol() ||
				t.IsNull() || t.IsUndefined() || isForOfStringTy(t)
			if isPrimitive {
				return Value{Ref: "0", Ty: TypeBool}, nil
			}
			return Value{Ref: "1", Ty: TypeBool}, nil
		}
		if _, ok := builtinInstanceofTypes[rightIdent.Name]; !ok && e.isHostClassName(rightIdent.Name) {
			// A host class (Blob, Headers, ArrayBuffer, …): its static type
			// decides, or a boxed value's host header.
			leftVal, err := e.emitExpr(ex.Left)
			if err != nil {
				return Value{}, err
			}
			if leftVal.Ty.IsDynamic() {
				return e.emitDynHostInstanceOf(leftVal, rightIdent.Name), nil
			}
			if isHostHandle(leftVal.Ty) && hostClassName(leftVal.Ty) == rightIdent.Name {
				return Value{Ref: e.presentRef(leftVal), Ty: TypeBool}, nil
			}
			return Value{Ref: "0", Ty: TypeBool}, nil
		}
		// A headerless host object (URL) in a union: the union's one object
		// without a header word.
		if lt := e.typeOfExpr(ex.Left); lt.IsDynamic() && len(lt.UnionMembers) > 0 && builtinInstanceofTypes[rightIdent.Name] == nil {
			if m, ok := headerlessHostMember(lt, rightIdent.Name); ok {
				leftVal, err := e.emitExpr(ex.Left)
				if err != nil {
					return Value{}, err
				}
				return e.emitBoxIsHeaderless(leftVal, m), nil
			}
		}
		if matches, ok := builtinInstanceofTypes[rightIdent.Name]; ok {
			// Still a real expression — evaluate for side effects, same as
			// every other branch below, even though the answer never
			// depends on the resulting value itself.
			leftVal, err := e.emitExpr(ex.Left)
			if err != nil {
				return Value{}, err
			}
			if leftVal.Ty.IsDynamic() {
				// Any boxed value, a union's included: its box decides.
				if r, ok := e.emitDynBuiltinInstanceOf(leftVal, rightIdent.Name); ok {
					return r, nil
				}
			}
			if matches(leftVal.Ty) {
				return Value{Ref: e.presentRef(leftVal), Ty: TypeBool}, nil
			}
			if leftVal.Ty.IsClass() {
				// A class extending Map or Set is one (TDD-00247).
				if li, ok := e.classes[leftVal.Ty.ClassName]; ok && li.HostBase != nil && matches(*li.HostBase) {
					return Value{Ref: e.presentRef(leftVal), Ty: TypeBool}, nil
				}
			}
			return Value{Ref: "0", Ty: TypeBool}, nil
		}
		if e.jsProtoCtor[rightIdent.Name] {
			return e.emitProtoCtorInstanceOf(ex.Left, rightIdent.Name)
		}
		if _, bound := e.lookup(rightIdent.Name); !bound && (rightIdent.Name == "Function" || rightIdent.Name == "Symbol") {
			return e.emitFunctionSymbolInstanceOf(ex.Left, rightIdent.Name)
		}
		// Any other value (a plain function, a global's property, an
		// undeclared name's ReferenceError): the right operand read after
		// the left decides, at run time.
		return e.emitDynInstanceOf(ex)
	}

	leftVal, err := e.emitExpr(ex.Left)
	if err != nil {
		return Value{}, err
	}

	// A Map or Set against a class extending one (TDD-00247): the header
	// word before its body is the instance's class tag when it is one.
	if hostBodyAt8(leftVal.Ty) && info.HostBase != nil {
		hp, hdr := e.freshReg(), e.freshReg()
		e.emitPrefixWordAddr(hp, leftVal.Ref)
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", hdr, hp))
		return Value{Ref: e.emitClassIs(hdr, info.TagID), Ty: TypeBool}, nil
	}
	// An array against a class extending Array: the header word before
	// its header is the instance's class tag when it is one's body (a
	// species result). A fresh array never is.
	if leftVal.Ty.IsArray() && !leftVal.Ty.IsBuffer() && info.HostBase != nil && info.HostBase.IsArray() {
		hp, hdr := e.freshReg(), e.freshReg()
		e.emitPrefixWordAddr(hp, e.nullSafeArrayHeader(leftVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", hdr, hp))
		return Value{Ref: e.emitClassIs(hdr, info.TagID), Ty: TypeBool}, nil
	}
	// A caught error (errorObjType) against an error-subclass class
	// (TDD-00155 Stage 6): runtime kind-slot compare — the subclass TagID
	// lives in the error struct's kind position.
	if leftVal.Ty.IsError() && info.IsErrorSubclass {
		kindGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 0", kindGep, errorObjType.StructIR(), leftVal.Ref))
		loadedKind := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", loadedKind, kindGep))
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", r, e.headerIdentity(loadedKind), errorTypeIDStored(info.TagID)))
		return Value{Ref: r, Ty: TypeBool}, nil
	}

	if leftVal.Ty.IsDynamic() {
		if !e.brandOnly && leftVal.Ty.IR == "i64" {
			// A Proxy's prototype is its target's (no getPrototypeOf trap).
			src := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_proxy_proto_src(i64 %s)", src, leftVal.Ref))
			leftVal = Value{Ref: src, Ty: leftVal.Ty}
		}
		tag, payload := e.emitUnboxTagPayload(leftVal)
		isObj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
		isBag := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isBag, tag, kmlTagDynObject))

		ptrReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, ptrReg, payload))

		resultAlloca := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resultAlloca))
		objL := e.freshLabel("instanceof.obj")
		bagChkL := e.freshLabel("instanceof.bagchk")
		bagL := e.freshLabel("instanceof.bag")
		cmpL := e.freshLabel("instanceof.cmp")
		notObjL := e.freshLabel("instanceof.notobj")
		mergeL := e.freshLabel("instanceof.merge")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, bagChkL))

		// A raw class instance (tag kmlTagObject): its instanceof TagID lives at
		// struct field 0.
		e.emitLabel(objL)
		tagGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 0", tagGep, info.Ty.StructIR(), ptrReg))
		loadedTagObj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", loadedTagObj, tagGep))
		e.emitTerminator(fmt.Sprintf("br label %%%s", cmpL))

		// A widened bag (tag kmlTagDynObject) realized from a class instance carries
		// its class TagID at header offset 40 (0 = not from a class → no match).
		e.emitLabel(bagChkL)
		fnChkL := e.freshLabel("instanceof.fnchk")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBag, bagL, fnChkL))
		e.emitLabel(fnChkL)
		if e.brandOnly {
			e.emitTerminator(fmt.Sprintf("br label %%%s", notObjL))
		} else {
			// An array Object.setPrototypeOf gave a chain holding
			// C.prototype.
			isArr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isArr, tag, kmlTagArray))
			arrL, fnChk2L := e.freshLabel("instanceof.arr"), e.freshLabel("instanceof.fnchk2")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isArr, arrL, fnChk2L))
			e.emitLabel(arrL)
			{
				e.ensureShapeRuntime()
				hdr, protoObj, has, onChain := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", hdr, ptrReg))
				e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", protoObj, e.classProtoSlot(rightIdent.Name)))
				e.declareFn("__kml_array_chain_has", "declare i64 @__kml_array_chain_has("+irPtr+", "+irPtr+")")
				e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_array_chain_has("+irPtr+" %s, "+irPtr+" %s)", has, hdr, protoObj))
				e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", onChain, has))
				e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", onChain, resultAlloca))
				e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
			}
			e.emitLabel(fnChk2L)
			// A function whose chain Object.setPrototypeOf gave C.prototype
			// (an instance of a class extending Function).
			isFn := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isFn, tag, kmlTagDynFunc))
			fnL := e.freshLabel("instanceof.fn")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFn, fnL, notObjL))
			e.emitLabel(fnL)
			e.ensureFnMeta()
			props := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_fn_props_dyn("+irPtr+" %s)", props, ptrReg))
			hasBag := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", hasBag, props))
			chainL := e.freshLabel("instanceof.fnchain")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasBag, chainL, notObjL))
			e.emitLabel(chainL)
			protoObj, has, onChain := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", protoObj, e.classProtoSlot(rightIdent.Name)))
			e.declareFn("__kml_dynobj_chain_has", "declare i64 @__kml_dynobj_chain_has("+irPtr+", "+irPtr+")")
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_chain_has("+irPtr+" %s, "+irPtr+" %s)", has, props, protoObj))
			e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", onChain, has))
			e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", onChain, resultAlloca))
			e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		}
		e.emitLabel(bagL)
		e.ensureAnyToPrimitive()
		e.ensureNanBox()
		e.ensureErrorNames()
		e.ensureStrHeaderRuntime()
		loadedTagBag, classTag := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_classtag("+irPtr+" %s)", classTag, ptrReg))
		if e.brandOnly {
			// A private-name brand is the constructor's, never the chain's.
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, 0", loadedTagBag, classTag))
			e.emitTerminator(fmt.Sprintf("br label %%%s", cmpL))
			goto cmp
		}
		{
			// A plain object whose chain holds C.prototype (`D.prototype
			// instanceof C`) is one too.
			protoSlot := e.classProtoSlot(rightIdent.Name)
			protoObj, has, onChain := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", protoObj, protoSlot))
			e.declareFn("__kml_dynobj_chain_has", "declare i64 @__kml_dynobj_chain_has("+irPtr+", "+irPtr+")")
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_chain_has("+irPtr+" %s, "+irPtr+" %s)", has, ptrReg, protoObj))
			e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", onChain, has))
			chainTag := info.TagID
			if info.IsErrorSubclass {
				chainTag = errorTypeIDStored(chainTag)
			}
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", loadedTagBag, onChain, chainTag, classTag))
			e.emitTerminator(fmt.Sprintf("br label %%%s", cmpL))
		}

	cmp:
		e.emitLabel(cmpL)
		loadedTag := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = phi i64 [ %s, %%%s ], [ %s, %%%s ]", loadedTag, loadedTagObj, objL, loadedTagBag, bagL))
		// An error-subclass instance stamps its field-0 TagID with the Error
		// type-id flag (TDD-00222); a hierarchy rooted at Error is uniformly
		// error-subclass, so the target carries the flag too.
		target := info.TagID
		if info.IsErrorSubclass {
			target = errorTypeIDStored(target)
		}
		classMatch := e.emitClassIs(loadedTag, target)
		e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", classMatch, resultAlloca))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(notObjL)
		e.emitInstr(fmt.Sprintf("store i1 0, "+irPtr+" %s, align 1", resultAlloca))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(mergeL)
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", result, resultAlloca))
		return Value{Ref: result, Ty: TypeBool}, nil
	}

	if leftVal.Ty.IsClass() {
		leftInfo := e.classes[leftVal.Ty.ClassName]

		// Case 2: T is C itself or an ancestor of C — every C instance is
		// unconditionally a T, no runtime check needed.
		isAncestorMatch := leftVal.Ty.ClassName == rightIdent.Name
		if !isAncestorMatch {
			for _, anc := range leftInfo.AncestorChain {
				if anc == rightIdent.Name {
					isAncestorMatch = true
					break
				}
			}
		}
		if isAncestorMatch {
			// Every C is a T, but the slot may hold no instance: `this` in a
			// method called without a receiver (`const f = o.m; f()`), or a
			// value that reached a C slot through `any` (a parameter checking
			// its argument). A null pointer is never an instance.
			if leftVal.Ty.IR == irPtr {
				result := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", result, leftVal.Ref))
				return Value{Ref: result, Ty: TypeBool}, nil
			}
			return Value{Ref: "1", Ty: TypeBool}, nil
		}

		// Case 2b: T is a strict descendant of C (e.g. `const s: Shape = new
		// Circle(...); s instanceof Circle`) — NOT decidable at compile
		// time: a C-typed value can hold any concrete subtype of C at
		// runtime, so this needs the same real tag check as the any/unknown
		// case above, just without the unbox/is-object step (already
		// statically known to be an object in this hierarchy). A real bug
		// found via examples/classes/classes.ts: before this case existed,
		// every such check silently and incorrectly returned false.
		isDescendant := false
		for _, d := range leftInfo.Descendants {
			if d == rightIdent.Name {
				isDescendant = true
				break
			}
		}
		if isDescendant {
			targetInfo := e.classes[rightIdent.Name]
			// Error-subclass instances stamp field 0 with the Error type-id flag
			// (TDD-00222); flag the target to match the stored value.
			target := targetInfo.TagID
			if targetInfo.IsErrorSubclass {
				target = errorTypeIDStored(target)
			}

			resultAlloca := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resultAlloca))
			mergeL := ""
			if leftVal.Ty.Nullable() {
				nullL := e.freshLabel("instanceof.null")
				notNullL := e.freshLabel("instanceof.notnull")
				mergeL = e.freshLabel("instanceof.merge")
				isNull := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, null", isNull, leftVal.Ref))
				e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, nullL, notNullL))
				e.emitLabel(nullL)
				e.emitInstr(fmt.Sprintf("store i1 0, "+irPtr+" %s, align 1", resultAlloca))
				e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
				e.emitLabel(notNullL)
			}

			tagGep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 0", tagGep, leftInfo.Ty.StructIR(), leftVal.Ref))
			loadedTag := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", loadedTag, tagGep))
			match := e.emitClassIs(loadedTag, target)

			if !leftVal.Ty.Nullable() {
				return Value{Ref: match, Ty: TypeBool}, nil
			}
			e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", match, resultAlloca))
			e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
			e.emitLabel(mergeL)
			result := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", result, resultAlloca))
			return Value{Ref: result, Ty: TypeBool}, nil
		}

		// Case 3: C and T are unrelated (neither is an ancestor of the
		// other) — constant false, sound under single inheritance (no
		// diamond).
	}

	return Value{Ref: "0", Ty: TypeBool}, nil
}

// emitErrorInstanceOf implements `x instanceof Error` and `x instanceof
// TypeError`/`RangeError`/... (TDD-00013 Option A) — the same shape
// emitInstanceOf uses for user classes, keyed off errorObjType's hidden
// kind tag (field 0) instead of ClassTagField. "Error" itself is the base
// every constructible kind is unconditionally an instance of, so it never
// needs a runtime tag comparison — any value that is *some* Error (whether
// known statically or, for a dynamic/any value, merely confirmed to be some
// object) already qualifies. A specific kind (TypeError, ...) always needs
// the runtime tag comparison, since every kind shares one Type — the tag is
// the only thing distinguishing a TypeError instance from a RangeError one.
// Structurally independent of user-class inheritance (TDD-00009 Stage 3) —
// Error/TypeError/etc. are never registered in e.classes.
//
// No annotationType path ever produces a statically Error-typed, Nullable
// value ("Error" isn't a resolvable type-annotation name — see
// emit_exprs_types.go/types.go's annotationType/ResolveTypeName), so unlike
// emitInstanceOf's class case there is no null-check branch to handle here.
func (e *Emitter) emitErrorInstanceOf(ex *ast.BinaryExpression, kindName string, kindID int64) (Value, error) {
	leftVal, err := e.emitExpr(ex.Left)
	if err != nil {
		return Value{}, err
	}

	if leftVal.Ty.IsDynamic() {
		tag, payload := e.emitUnboxTagPayload(leftVal)
		isObj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))

		if kindName == "Error" {
			// A boxed Error (built-in errorObjType or an error-subclass instance)
			// is an object whose field-0 type-id carries errorTypeIDFlag; a boxed
			// plain object/bag does not, so `plainObj instanceof Error` is now
			// correctly false rather than the old unconditional true (TDD-00222).
			baseAlloca := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", baseAlloca))
			e.emitInstr(fmt.Sprintf("store i1 0, "+irPtr+" %s, align 1", baseAlloca))
			baseObjL := e.freshLabel("errinstanceof.baseobj")
			baseMergeL := e.freshLabel("errinstanceof.basemerge")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, baseObjL, baseMergeL))
			e.emitLabel(baseObjL)
			basePtr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, basePtr, payload))
			baseF0Gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 0", baseF0Gep, errorObjType.StructIR(), basePtr))
			baseF0 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", baseF0, baseF0Gep))
			baseMasked := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", baseMasked, baseF0, errorTypeIDFlag))
			baseIsErr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", baseIsErr, baseMasked))
			e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", baseIsErr, baseAlloca))
			e.emitTerminator(fmt.Sprintf("br label %%%s", baseMergeL))
			e.emitLabel(baseMergeL)
			baseRes := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", baseRes, baseAlloca))
			return e.orBuiltinProtoChain(Value{Ref: baseRes, Ty: TypeBool}, leftVal, kindName), nil
		}

		resultAlloca := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resultAlloca))
		objL := e.freshLabel("errinstanceof.obj")
		notObjL := e.freshLabel("errinstanceof.notobj")
		mergeL := e.freshLabel("errinstanceof.merge")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, notObjL))

		e.emitLabel(objL)
		ptrReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, ptrReg, payload))
		kindGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 0", kindGep, errorObjType.StructIR(), ptrReg))
		loadedKind := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", loadedKind, kindGep))
		kindMatch := e.errorKindMatch(loadedKind, kindName, kindID)
		e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", kindMatch, resultAlloca))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(notObjL)
		e.emitInstr(fmt.Sprintf("store i1 0, "+irPtr+" %s, align 1", resultAlloca))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(mergeL)
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", result, resultAlloca))
		return Value{Ref: result, Ty: TypeBool}, nil
	}

	// An error-subclass instance (TDD-00155 Stage 6): `instanceof Error` is
	// true by construction; a specific builtin kind never matches (the
	// subclass TagID range is disjoint from the builtin kinds).
	present := func() string { return e.presentRef(leftVal) }
	if leftVal.Ty.IsClass() {
		if ci, ok := e.classes[leftVal.Ty.ClassName]; ok && ci.IsErrorSubclass {
			if kindName == "Error" || kindName == ci.ErrorBaseKind {
				return Value{Ref: present(), Ty: TypeBool}, nil
			}
			return Value{Ref: "0", Ty: TypeBool}, nil
		}
	}
	if leftVal.Ty.IsError() {
		if kindName == "Error" {
			return Value{Ref: present(), Ty: TypeBool}, nil
		}
		res := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", res))
		e.emitInstr(fmt.Sprintf("store i1 0, "+irPtr+" %s, align 1", res))
		readL := e.freshLabel("errinstanceof.read")
		doneL := e.freshLabel("errinstanceof.done")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", present(), readL, doneL))
		e.emitLabel(readL)
		kindGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 0", kindGep, errorObjType.StructIR(), leftVal.Ref))
		loadedKind := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", loadedKind, kindGep))
		e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", e.errorKindMatch(loadedKind, kindName, kindID), res))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		out := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", out, res))
		return Value{Ref: out, Ty: TypeBool}, nil
	}

	return Value{Ref: "0", Ty: TypeBool}, nil
}

// errorKindMatch is whether a stored error type id is the builtin kind
// kindName's, or an error subclass's whose class chain reaches that kind
// (`class E extends TypeError`).
func (e *Emitter) errorKindMatch(stored, kindName string, kindID int64) string {
	return e.emitClassIs(stored, errorTypeIDStored(kindID))
}

// isErrorRootKind reports whether a class extending base extends a builtin
// error directly: a builtin whose declaration has Error's layout
// (`@lowerType error`, TDD-00230 P3.2), Error itself or a kind of it. A
// program's own class of that name shadows the builtin.
func (e *Emitter) isErrorRootKind(base string) bool {
	if base == "" {
		return false
	}
	l := declaredLowerType(e.lookupBuiltinType(base))
	return l == "error" || l == "domexception" || l == "aggregateerror"
}

// aggregateRooted reports a class whose error root is AggregateError:
// its constructor takes (errors, message, options).
func (e *Emitter) aggregateRooted(info ClassInfo) bool {
	for i := 0; i < 64; i++ {
		if e.isErrorRootKind(info.BaseClass) {
			return declaredLowerType(e.lookupBuiltinType(info.BaseClass)) == "aggregateerror"
		}
		b, ok := e.classes[info.BaseClass]
		if !ok {
			return false
		}
		info = b
	}
	return false
}

// emitAggregateErrors stores an AggregateError subclass instance's own
// `errors`: the array of what the iterable errs yields.
func (e *Emitter) emitAggregateErrors(obj string, errs ast.Expression) error {
	arr, err := e.emitExprWithObjectHint(ast.NewArrayLiteral([]ast.Expression{&ast.SpreadElement{Arg: errs}}, errs.GetPos()), ArrayOf(TypeAny))
	if err != nil {
		return err
	}
	b, err := e.emitBoxValue(arr)
	if err != nil {
		return err
	}
	e.declareFn("__kml_error_set_hidden", "declare void @__kml_error_set_hidden("+irPtr+", "+irPtr+", i64)")
	e.emitInstr(fmt.Sprintf("call void @__kml_error_set_hidden("+irPtr+" %s, "+irPtr+" %s, i64 %s)", obj, e.internString("errors"), b.Ref))
	return nil
}

func errorBaseKind(isErrorRoot bool, base string) string {
	if !isErrorRoot {
		return ""
	}
	return base
}

// declaredCallReturnType is the annotated return type of a call to a
// top-level function not yet registered (classes register before functions):
// class field inference reads it instead of the unknown-callee default.
func (e *Emitter) declaredCallReturnType(prog *ast.Program, init ast.Expression) (Type, bool) {
	call, ok := init.(*ast.CallExpression)
	if !ok || call.Optional || len(call.TypeArgs) > 0 {
		return Type{}, false
	}
	id, ok := call.Callee.(*ast.Identifier)
	if !ok {
		return Type{}, false
	}
	if _, known := e.funcs[id.Name]; known {
		return Type{}, false
	}
	for _, st := range prog.Body {
		fd, ok := st.(*ast.FunctionDeclaration)
		if !ok || fd.Name != id.Name || fd.Body == nil || len(fd.TypeParams) > 0 || fd.ReturnType == nil || fd.IsAsync {
			continue
		}
		return e.annotationType(fd.ReturnType), true
	}
	return Type{}, false
}

// undefinedFillsDefault reports whether argument i is a literal `undefined`
// passed to a parameter with a default: JS applies the default for an
// explicit `undefined` as for an omitted argument.
func undefinedFillsDefault(arg ast.Expression, sig FuncSig, i int) bool {
	nl, ok := arg.(*ast.NullLiteral)
	return ok && nl.IsUndefined && i < len(sig.Defaults) && sig.Defaults[i] != nil
}

// emitDynIsArray is Array.isArray of a value held in `any`: a boxed static
// array that is no TypedArray, a dynamic array, or an instance of a class
// extending Array.
func (e *Emitter) emitDynIsArray(v Value) Value {
	tag, payload := e.emitUnboxTagPayload(v)
	isArr, isDyn := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isArr, tag, kmlTagArray))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isDyn, tag, kmlTagDynArray))
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resPtr))
	e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", isDyn, resPtr))
	typedL, otherL, mergeL := e.freshLabel("isarray.typed"), e.freshLabel("isarray.other"), e.freshLabel("isarray.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isArr, typedL, otherL))
	// A boxed TypedArray is kmlTagArray too, but no Array: the box's typed
	// byte tells (ADR-01059). Only dereferenced when the tag says the
	// payload is a box.
	e.emitLabel(typedL)
	box := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, box, payload))
	typedGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 2", typedGep, anyArrayBoxTy, box))
	typedB := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, "+irPtr+" %s, align 1", typedB, typedGep))
	plain := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", plain, typedB, anyArrayPlain))
	// An Arguments object (its header's word) is no Array.
	hdrp, hdr := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 0", hdrp, anyArrayBoxTy, box))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", hdr, hdrp))
	notArgs, isArrPlain := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notArgs, e.emitArrayIsArguments(hdr)))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", isArrPlain, plain, notArgs))
	e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", isArrPlain, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	// An object: an instance of a class extending Array, by its parent rows;
	// a dynamic object: Array.prototype.
	e.emitLabel(otherL)
	{
		isBag := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isBag, tag, kmlTagDynObject))
		bagL, restL := e.freshLabel("isarray.bag"), e.freshLabel("isarray.rest")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBag, bagL, restL))
		e.emitLabel(bagL)
		e.declareFn("__kml_dynobj_is_array_exotic", "declare i1 @__kml_dynobj_is_array_exotic("+irPtr+")")
		bag, ex := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, bag, payload))
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynobj_is_array_exotic("+irPtr+" %s)", ex, bag))
		e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", ex, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(restL)
	}
	if e.hasArrayClass() {
		isObj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
		objL := e.freshLabel("isarray.obj")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, mergeL))
		e.emitLabel(objL)
		obj, word := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, obj, payload))
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", word, obj))
		// Each Array host has its own id (its element layout); every one is
		// a child of the any[] one (emitClassParentRows).
		e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", e.emitClassIs(word, kmlHdrMagic|e.hostID(ArrayOf(TypeAny))&kmlHdrIDMask), resPtr))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(mergeL)
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", res, resPtr))
	return Value{Ref: res, Ty: TypeBool}
}

// dynBuiltinInstanceof decides `v instanceof <builtin>` for a value held in
// `any` (v, its box tag and payload): an array by its box tag, a promise by
// its box wrapper's header.
var dynBuiltinInstanceof map[string]func(e *Emitter, v Value, tag, pay string) Value

func init() {
	dynBuiltinInstanceof = map[string]func(e *Emitter, v Value, tag, pay string) Value{
		"Array": func(e *Emitter, v Value, _, _ string) Value { return e.emitDynIsArray(v) },
		"Promise": func(e *Emitter, _ Value, tag, pay string) Value {
			isObj := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
			out := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", out))
			e.emitInstr(fmt.Sprintf("store i1 0, "+irPtr+" %s, align 1", out))
			chkL, doneL := e.freshLabel("inst.prom"), e.freshLabel("inst.prom.done")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, chkL, doneL))
			e.emitLabel(chkL)
			obj := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, obj, pay))
			hdr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", hdr, obj))
			is := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", is, e.headerIdentity(hdr), e.objHeaderWord(promiseBoxType())))
			e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", is, out))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
			e.emitLabel(doneL)
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", r, out))
			return Value{Ref: r, Ty: TypeBool}
		},
	}
}

// emitDynBuiltinInstanceOf answers `v instanceof Array | Promise` for a value
// held in `any` at run time: an array (static or dynamic) by its box tag, a
// promise by its box wrapper's header. ok is false for the other builtins.
func (e *Emitter) emitDynBuiltinInstanceOf(v Value, name string) (Value, bool) {
	if v.Ty.IR == "i64" && name != "Proxy" {
		// A Proxy's prototype is its target's (no getPrototypeOf trap).
		src := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_proxy_proto_src(i64 %s)", src, v.Ref))
		v = Value{Ref: src, Ty: v.Ty}
	}
	tag, pay := e.emitUnboxTagPayload(v)
	if is, ok := dynBuiltinInstanceof[name]; ok {
		return e.orBuiltinProtoChain(is(e, v, tag, pay), v, name), true
	}
	if typedArrayNames[name] || name == "Buffer" {
		// A boxed static array whose box says it is that TypedArray.
		isArr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isArr, tag, kmlTagArray))
		out := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", out))
		e.emitInstr(fmt.Sprintf("store i1 0, "+irPtr+" %s, align 1", out))
		chkL, doneL := e.freshLabel("inst.typed"), e.freshLabel("inst.typed.done")
		notArrL := doneL
		subs := e.sortedClassNames(func(c string, _ ClassInfo) bool { return e.builtinRootBase(c) == name })
		if len(subs) > 0 {
			notArrL = e.freshLabel("inst.typed.cls")
		}
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isArr, chkL, notArrL))
		if len(subs) > 0 {
			// An instance of a class extending it (`class MyU extends
			// Uint8Array`), by its class tag.
			e.emitLabel(notArrL)
			isObj := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
			objL := e.freshLabel("inst.typed.obj")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, doneL))
			e.emitLabel(objL)
			word := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", word, e.emitIntToPtr(pay)))
			wid := e.headerIdentity(word)
			any := "false"
			for _, c := range subs {
				is, or := e.freshReg(), e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", is, wid, e.classes[c].TagID))
				e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", or, any, is))
				any = or
			}
			e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", any, out))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		}
		e.emitLabel(chkL)
		e.ensureDynJSONC()
		box := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, box, pay))
		r32 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_anyarr_typed_is("+irPtr+" %s, "+irPtr+" %s)", r32, box, e.internString(name)))
		is := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", is, r32))
		e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", is, out))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", r, out))
		return Value{Ref: r, Ty: TypeBool}, true
	}
	if e.isHostClassName(name) {
		return e.emitDynHostInstanceOf(v, name), true
	}
	return Value{}, false
}

// headerlessHostMember is union u's member of host class name when it is an
// object without a header word (a URL) and the union's only such member.
func headerlessHostMember(u Type, name string) (Type, bool) {
	var found Type
	n := 0
	for _, m := range u.UnionMembers {
		if m.IsObject() && !m.IsClass() && !hasObjHeader(m) && !m.IsTuple() {
			n++
			found = m
		}
	}
	if n != 1 || hostClassName(found) != name {
		return Type{}, false
	}
	return found, true
}

// emitBoxIsHeaderless is an i1: the boxed value is an object whose first
// word is not a header (the union's headerless host member).
func (e *Emitter) emitBoxIsHeaderless(v Value, m Type) Value {
	tag, pay := e.emitUnboxTagPayload(Value{Ref: v.Ref, Ty: TypeAny})
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", slot))
	e.emitInstr(fmt.Sprintf("store i1 false, "+irPtr+" %s, align 1", slot))
	isObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
	objL, doneL := e.freshLabel("hless.obj"), e.freshLabel("hless.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, doneL))
	e.emitLabel(objL)
	p := e.emitIntToPtr(pay)
	w, mk, isHdr, r := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", w, p))
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", mk, w, kmlHdrMagicMask|hostTypeIDFlag))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isHdr, mk, kmlHdrMagic))
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", r, isHdr))
	e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", r, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", out, slot))
	return Value{Ref: out, Ty: TypeBool}
}

// errorInitialName is the `.name` an instance of a class extending the
// builtin error kind base has until it assigns its own: the kind's name,
// except DOMException's, whose name defaults to "Error".
func errorInitialName(base string) string {
	if base == "DOMException" {
		return "Error"
	}
	return base
}

// emitErrorSecondArg applies an error constructor's second argument to the
// instance: DOMException's name (undefined keeps "Error"), else the ES2022
// options bag's own `cause`.
func (e *Emitter) emitErrorSecondArg(thisSym Symbol, info ClassInfo, arg ast.Expression) error {
	if info.ErrorBaseKind != "DOMException" {
		return e.emitSuperErrorCause(thisSym, info, arg)
	}
	if isUndefinedLiteral(e, arg) {
		return nil
	}
	v, err := e.emitExpr(arg)
	if err != nil {
		return err
	}
	name, err := e.emitErrorMessage(v)
	if err != nil {
		return err
	}
	idx, _, ok := info.Ty.FieldIndex("name")
	if !ok {
		return nil
	}
	thisReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", thisReg, thisSym.Ptr))
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gep, info.Ty.StructIR(), thisReg, idx))
	e.heapStore(irPtr, name, gep, 8)
	return nil
}

// emitSuperErrorCause stores super(message, options)'s options.cause into the
// error's cause slot: absent when options is undefined or has no cause.
func (e *Emitter) emitSuperErrorCause(thisSym Symbol, info ClassInfo, opts ast.Expression) error {
	idx, _, ok := info.Ty.FieldIndex("cause")
	if !ok {
		return nil
	}
	ov, err := e.emitExpr(opts)
	if err != nil {
		return err
	}
	boxed, err := e.emitBoxValue(ov)
	if err != nil {
		return err
	}
	e.ensureNanBox()
	tag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_nb_tag(i64 %s)", tag, boxed.Ref))
	isObj := e.freshReg()
	// Any object is an options bag (tags from kmlTagObject up are objects).
	e.emitInstr(fmt.Sprintf("%s = icmp uge i8 %s, %d", isObj, tag, kmlTagObject))
	getL, doneL := e.freshLabel("errcause.get"), e.freshLabel("errcause.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, getL, doneL))
	e.emitLabel(getL)
	cause, err := e.emitDynAnyMemberGetNamed(boxed, e.internString("cause"), "cause", opts.GetPos())
	if err != nil {
		return err
	}
	ctag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_nb_tag(i64 %s)", ctag, cause.Ref))
	present := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i8 %s, %d", present, ctag, kmlTagUndefined))
	storeL := e.freshLabel("errcause.store")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", present, storeL, doneL))
	e.emitLabel(storeL)
	thisReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", thisReg, thisSym.Ptr))
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gep, info.Ty.StructIR(), thisReg, idx))
	e.heapStore("i64", cause.Ref, gep, 8)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	return nil
}

// dealiasNew is x with a `new K(…)` through a constant bound to a class
// (constClassAlias) written as `new C(…)`.
func (e *Emitter) dealiasNew(x ast.Expression) ast.Expression {
	ne, ok := x.(*ast.NewExpression)
	if !ok {
		return x
	}
	target, ok := e.constClassAlias(ne)
	if !ok || target == ne.ClassName {
		return x // a generic class, which e.classes does not hold, is no alias
	}
	alias := *ne
	alias.ClassName = target
	return &alias
}

// constClassAlias is the class a `new K(…)` constructs when K is a constant
// bound to a class (`const K = C`, through further such constants).
func (e *Emitter) constClassAlias(ex *ast.NewExpression) (string, bool) {
	if _, isClass := e.classes[ex.ClassName]; isClass || ex.Qualified {
		return "", false
	}
	c := e.front()
	if c == nil {
		return "", false
	}
	return e.classBehind(c.Binding().NewTarget(ex))
}

// identClassAlias is the class the identifier id names through constants
// bound to it (`K` after `const K = C`); false for the class itself.
func (e *Emitter) identClassAlias(id *ast.Identifier) (string, bool) {
	if _, isClass := e.classes[id.Name]; isClass {
		return "", false
	}
	c := e.front()
	if c == nil {
		return "", false
	}
	sym, _ := c.Binding().Resolve(id)
	if sym == nil || sym.Flags&binder.Class != 0 {
		return "", false
	}
	return e.classBehind(sym)
}

// classBehind follows constants bound to an identifier (`const K = C`) to
// the class they name.
func (e *Emitter) classBehind(sym *binder.Symbol) (string, bool) {
	c := e.front()
	for i := 0; sym != nil && i < 8; i++ {
		if sym.Flags&binder.Class != 0 {
			_, generic := e.genericClasses[sym.Name]
			_, known := e.classes[sym.Name]
			return sym.Name, known || generic
		}
		if len(sym.Declarations) != 1 {
			return "", false
		}
		vd, ok := sym.Declarations[0].Node.(*ast.VarDeclaration)
		if !ok || vd.Kind != "const" || vd.TypeAnnot != nil {
			return "", false
		}
		id, ok := vd.Init.(*ast.Identifier)
		if !ok {
			return "", false
		}
		sym, _ = c.Binding().Resolve(id)
	}
	return "", false
}

// errorSystemSlot is the index in fields of the system-error slot named
// name (errorObjType's fields beyond message and name), or -1.
func errorSystemSlot(fields []Field, name string) int {
	switch name {
	case "message", "name", "stack", errorExtraField, ClassTagField:
		return -1
	}
	if _, _, ok := errorObjType.FieldIndex(name); !ok {
		return -1
	}
	for i, f := range fields {
		if f.Name == name {
			return i
		}
	}
	return -1
}

// classField is field name of class type t by the class's registered
// layout: its index and type. A class type carried on a value can be a
// snapshot taken before the layout was final (without its vtable field), so
// a field reached by name always goes through here (TDD-00238).
func (e *Emitter) classField(t Type, name string) (int, Type, Type, bool) {
	t = e.canonicalizeClassTy(t)
	if info, ok := e.classes[t.ClassName]; ok && t.IsClass() {
		t = info.Ty
	}
	i, fty, ok := t.FieldIndex(name)
	if !ok && strings.HasPrefix(name, "#") {
		// A private name another class in its module shares is renamed
		// apart (`#reason__kml_ps3`).
		for j, f := range t.Fields {
			if strings.HasPrefix(f.Name, name+ast.PrivateScopeSep) {
				return j, f.Ty, t, true
			}
		}
	}
	return i, fty, t, ok
}

// emitVirtualTarget is the code pointer a virtual call of method name on
// thisRef (static class info) calls through slot: the object's vtable entry
// when the object is info's class or a subclass of it, else info's own
// implementation direct (null when no class in the chain has a body). A value can carry a class type it is not an instance of: Node's
// types make a Duplex a Writable, and casts from any say anything. A
// builtin module's methods are all virtual (ADR-01326), so the check is
// what keeps such a receiver on the implementation it was typed with.
func (e *Emitter) emitVirtualTarget(info ClassInfo, thisRef string, slot int, name string) string {
	direct := "null" // no class in the chain implements it
	if m := info.Methods[name]; m != nil && m.Body != nil {
		direct = "@" + llvmSafeSymbol(info.MethodImplementor[name]+"_"+name)
	}
	want := e.objHeaderWord(info.Ty)
	out := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", out))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", direct, out))
	hp, hdr := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, "+irPtr+" %s, i32 0", hp, thisRef))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", hdr, hp))
	hdr = e.headerIdentity(hdr)
	exact := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", exact, hdr, want))
	vtL, chkL, doneL := e.freshLabel("vcall.vt"), e.freshLabel("vcall.chk"), e.freshLabel("vcall.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", exact, vtL, chkL))
	e.emitLabel(chkL)
	sub := e.emitClassIs(hdr, want)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", sub, vtL, doneL))
	e.emitLabel(vtL)
	vtGep, vtPtr, slotGep, fp := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", vtGep, info.Ty.StructIR(), thisRef, info.Ty.VTableIndex()))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", vtPtr, vtGep))
	e.emitInstr(fmt.Sprintf("%s = getelementptr "+irPtr+", "+irPtr+" %s, i32 %d", slotGep, vtPtr, slot))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", fp, slotGep))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", fp, out))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", r, out))
	return r
}

// packRestArgs passes a rest parameter's arguments as its two LLVM
// arguments (header, length): a single spread forwards its array, none is the
// empty array, anything else a fresh heap array of the converted elements.
func (e *Emitter) packRestArgs(restArgs []ast.Expression, restTy Type) ([]string, error) {
	var out []string
	elemTy := TypeI64
	if restTy.ElemType != nil {
		elemTy = *restTy.ElemType
	}
	if spread, ok := singleSpread(restArgs); ok && sameRestElem(e.typeOfExpr(spread.Arg), elemTy) {
		// obj.m(...arr): forward the array's own (ptr, len) into the rest
		// slot, the same fast path the free-function call uses (TDD-00106).
		ptrReg, lenReg, _, err := e.resolveArrayForHOF(spread.Arg, spread.Arg.GetPos())
		if err != nil {
			return nil, err
		}
		restHdr := e.newArrayHeader(ptrReg, lenReg)
		out = append(out, irPtr+" "+restHdr, "i64 "+lenReg)
	} else if len(restArgs) == 0 {
		out = append(out, irPtr+" "+e.emptyArrayArgHeader(), "i64 0")
	} else if anySpread(restArgs) {
		dataReg, lenReg, err := e.emitRestArgBuffer(restArgs, elemTy)
		if err != nil {
			return nil, err
		}
		restHdr := e.newArrayHeader(dataReg, lenReg)
		out = append(out, irPtr+" "+restHdr, "i64 "+lenReg)
	} else {
		n := int64(len(restArgs))
		dataReg := e.freshReg()
		e.heapMallocInto(dataReg, elemAllocKind(elemTy), fmt.Sprint(n*int64(elemTy.Align())))
		for i, arg := range restArgs {
			val, err := e.emitExprWithObjectHint(arg, elemTy)
			if err != nil {
				return nil, err
			}
			val = e.convert(val, elemTy)
			gepReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %d", gepReg, elemTy.IR, dataReg, i))
			e.storeArrayElem(gepReg, elemTy, val)
		}
		restHdr := e.newArrayHeader(dataReg, fmt.Sprintf("%d", n))
		out = append(out, irPtr+" "+restHdr, fmt.Sprintf("i64 %d", n))
	}
	return out, nil
}

// sameRestElem reports whether an array of type at already holds elements
// of a rest parameter's element type, so a spread of it forwards as is.
func sameRestElem(at, elemTy Type) bool {
	if !at.IsArray() || at.ElemType == nil {
		return false
	}
	src := *at.ElemType
	return src.IR == elemTy.IR && src.IsArray() == elemTy.IsArray() && src.IsObject() == elemTy.IsObject() &&
		src.IsDynamic() == elemTy.IsDynamic()
}

// newTypeArgs is the type arguments of a generic class's `new`: the explicit
// ones (with defaults), else those inferred from its arguments
// (`new WeakRef(o)` is a WeakRef<Box>).
func (e *Emitter) newTypeArgs(decl *ast.ClassDeclaration, ne *ast.NewExpression) ([]ast.TypeNode, bool) {
	if targs, ok := classTypeArgs(decl, ne.TypeArgs); ok || len(ne.TypeArgs) > 0 {
		return targs, ok
	}
	if annots := e.inferredClassTypeArgs(ne, decl); annots != nil {
		return classTypeArgs(decl, annots)
	}
	return nil, false
}

// originalNew is the program's own `new` expression a key (class@line:col)
// names, indexed on first use.
func (e *Emitter) originalNew(key string) *ast.NewExpression {
	if e.newByKey == nil {
		e.newByKey = map[string]*ast.NewExpression{}
		if e.prog != nil {
			ast.Inspect(e.prog, func(n ast.Node) bool {
				if ne, ok := n.(*ast.NewExpression); ok {
					k := fmt.Sprintf("%s@%d:%d", ne.ClassName, ne.GetPos().Line, ne.GetPos().Col)
					if _, dup := e.newByKey[k]; !dup {
						e.newByKey[k] = ne
					}
				}
				return true
			})
		}
	}
	return e.newByKey[key]
}

// inferredClassTypeArgs is the type arguments of a generic class's `new`
// written without them, as annotations: each type parameter a constructor
// parameter is annotated with outright takes its argument's type, as the
// checker gives it. nil when one is left unanswered.
func (e *Emitter) inferredClassTypeArgs(ex *ast.NewExpression, decl *ast.ClassDeclaration) []ast.TypeNode {
	c := e.front()
	if c == nil {
		return nil
	}
	// The checker's instance type: its arguments, contextual ones included
	// (`const s: Stack<number> = new Stack()`). A copy of the node (a pass
	// rewriting it) keeps its position, and the answer the original got.
	key := fmt.Sprintf("%s@%d:%d", ex.ClassName, ex.GetPos().Line, ex.GetPos().Col)
	if out, ok := e.newClassArgs[key]; ok {
		return out
	}
	// A copy of the node outside the checked tree (a function body a pass
	// cloned) has no contextual type there: ask about the original.
	if c.ParentOf(ex) == nil {
		if orig := e.originalNew(key); orig != nil {
			ex = orig
		}
	}
	if t := c.TypeOf(ex); t != nil && !c.Unanswered(t) && t.Kind == checker.Instance && len(t.TypeArgs) == len(decl.TypeParams) {
		// An argument inferred `any` (`new Opt(x as any)`) where the
		// context expects an instance of the class (`(): Opt<number>`): the
		// context's, so the instance has the layout its reader expects.
		ctx := c.ContextualType(ex)
		if ctx != nil && (c.Unanswered(ctx) || ctx.Kind != checker.Instance || ctx.Symbol != t.Symbol || len(ctx.TypeArgs) != len(t.TypeArgs)) {
			ctx = nil
		}
		out := make([]ast.TypeNode, len(t.TypeArgs))
		perInstance := false
		for i, a := range t.TypeArgs {
			if ctx != nil && a != nil && a.Flags&checker.Any != 0 && ctx.TypeArgs[i] != nil && !c.Unanswered(ctx.TypeArgs[i]) {
				a = ctx.TypeArgs[i]
				if c.IsGeneric(a) {
					// Spelled with the enclosing generic's own parameters
					// (`Opt<[T, string]>`): as this instantiation binds them.
					rc := e.repr()
					rc.tuples = true // an annotation's
					if rt, ok := rc.of(a); ok {
						if n := typeNodeOf(rt); n != nil && typeNodeDepth(n) <= 4 {
							// Bounded: a method building a deeper instance of
							// its own class (`zip` of `Opt<[T, string]>`)
							// would otherwise instantiate it without end.
							out[i], perInstance = n, true
							continue
						}
					}
				}
			}
			if a == nil || c.Unanswered(a) || c.IsGeneric(a) {
				// Unanswered, or spelled with the enclosing generic's own
				// parameters (`new Opt(x as [T, ...O])` in a method): the
				// constructor's arguments below, typed under this
				// instantiation as the method's own annotations are.
				out = nil
				break
			}
			out[i] = checkerTypeAnnotation(a)
		}
		if out != nil && perInstance {
			return out
		}
		if out != nil {
			if e.newClassArgs == nil {
				e.newClassArgs = map[string][]ast.TypeNode{}
			}
			e.newClassArgs[key] = out
			return out
		}
	}
	if decl.Constructor == nil {
		return nil
	}
	found := map[string]ast.TypeNode{}
	for i, p := range decl.Constructor.Params {
		if i >= len(ex.Args) || p.Type == nil || p.Rest {
			continue
		}
		for _, tp := range decl.TypeParams {
			if p.Type != nil && ast.RefName(ast.NonNullish(p.Type)) == tp && found[tp] == nil {
				if as, ok := ex.Args[i].(*ast.AsExpression); ok && as.TypeAnnot != nil {
					// An asserted argument (`x as [T, ...O]`): the type it
					// is asserted to, spelled as written.
					found[tp] = as.TypeAnnot
				} else if t := c.TypeOf(ex.Args[i]); t != nil && !c.Unanswered(t) {
					found[tp] = checkerTypeAnnotation(t)
				}
			}
		}
	}
	out := make([]ast.TypeNode, len(decl.TypeParams))
	for i, tp := range decl.TypeParams {
		if out[i] = found[tp]; out[i] == nil {
			return nil
		}
	}
	return out
}

// checkerTypeAnnotation names checker type t as an annotation codegen
// resolves: a class or interface by its name, a primitive, an array of
// one; anything else as any.
func checkerTypeAnnotation(t *checker.Type) ast.TypeNode {
	switch {
	case t.Flags&(checker.Number|checker.NumberLiteral) != 0 && !t.AnyArith:
		return nameAnnotation("number")
	case t.Flags&(checker.String|checker.StringLiteral) != 0:
		return nameAnnotation("string")
	case t.Flags&(checker.Boolean|checker.BooleanLiteral) != 0:
		return nameAnnotation("boolean")
	case t.Flags&checker.Object != 0 && t.Kind == checker.Array:
		el := checkerTypeAnnotation(t.Elem)
		return arrayAnnotation(el)
	case t.Flags&checker.Object != 0 && t.Flags&checker.Union == 0 && t.Kind == checker.Tuple && t.Opt == 0 && len(t.Elems) > 0:
		// A tuple (`T` of `new Opt([2, "b"] as [number, string])`): the
		// tuple an annotation of it lays out.
		es := make([]ast.TypeNode, len(t.Elems))
		for i, el := range t.Elems {
			es[i] = checkerTypeAnnotation(el)
		}
		return &ast.TupleType{Elements: es}
	case t.Flags&checker.Object != 0 && (t.Kind == checker.Instance || t.Kind == checker.Interface) && t.Symbol != nil && len(t.TypeArgs) == 0:
		return nameAnnotation(t.Symbol.Name)
	}
	return ast.AnyAnnotation("ts")
}

// staticMethodName is the function a class's static method compiles to.
// Its infix differs from a static field's global (className_static_name),
// so a static field may redeclare a static method's name (`static g() {}`
// then `static g = this.g()`): the field's initializer replaces the method.
func staticMethodName(className, method string) string {
	return className + "_smethod_" + method
}

// forwardMethodKey keys a method's provisional result type in
// e.forwardMethodRet.
func forwardMethodKey(className, method string, static bool) string {
	if static {
		return className + "\x00s\x00" + method
	}
	return className + "\x00" + method
}

// inferForwardMethodResults fills e.forwardMethodRet with the inferred
// result of each of cd's unannotated plain methods, re-inferring until none
// changes (at most a few rounds), and each annotated one's declared result,
// so a call to a later-declared sibling infers its result.
func (e *Emitter) inferForwardMethodResults(cd *ast.ClassDeclaration, thisTy Type) {
	if e.forwardMethodRet == nil {
		e.forwardMethodRet = map[string]Type{}
	}
	if e.forwardMethods == nil {
		e.forwardMethods = map[string]bool{}
	}
	for _, m := range cd.Methods {
		if m.AccessorKind == "" && m.Body != nil {
			e.forwardMethods[forwardMethodKey(cd.Name, m.Name, m.IsStatic)] = true
		}
	}
	var ms []*ast.FunctionDeclaration
	for _, m := range cd.Methods {
		if m.ReturnType != nil && !m.IsGenerator && m.AccessorKind == "" && len(m.TypeParams) == 0 {
			// An annotated sibling's result is its annotation.
			e.forwardMethodRet[forwardMethodKey(cd.Name, m.Name, m.IsStatic)] = e.annotationType(m.ReturnType)
			continue
		}
		if m.ReturnType == nil && m.Body != nil && !m.IsGenerator && m.AccessorKind == "" {
			ms = append(ms, m)
		}
	}
	for round := 0; round < 4; round++ {
		changed := false
		for _, m := range ms {
			sig := e.buildParamSig(m.Params, m)
			e.pushScope()
			mThis := thisTy
			if m.IsStatic {
				mThis = TypeAny // a static member's `this` is the class value, a box
			} else if declared, ok := e.declaredMethodThis(m.Body, thisTy); ok {
				mThis = declared // `m(this: Other)`, as emission binds it
			}
			e.define("this", Symbol{Ty: mThis})
			if cd.BaseClass != "" {
				if baseTy, ok := e.interfaces[cd.BaseClass]; ok {
					e.define("super", Symbol{Ty: baseTy})
				}
			}
			inferred, ok := e.funcBodyReturnType(m.Body, sig.ParamNames, e.bodyParamTypes(m.Params, sig.ParamTypes))
			e.popScope()
			if !ok {
				continue
			}
			if m.IsAsync && !m.IsGenerator && !inferred.IsPromise() {
				inferred = PromiseOf(inferred)
			}
			k := forwardMethodKey(cd.Name, m.Name, m.IsStatic)
			if prev, had := e.forwardMethodRet[k]; !had || !reflect.DeepEqual(prev, inferred) {
				e.forwardMethodRet[k] = inferred
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

// dropForwardMethodResults removes className's provisional results once its
// methods are registered.
func (e *Emitter) dropForwardMethodResults(className string) {
	prefix := className + "\x00"
	for k := range e.forwardMethodRet {
		if strings.HasPrefix(k, prefix) {
			delete(e.forwardMethodRet, k)
		}
	}
	for k := range e.forwardMethods {
		if strings.HasPrefix(k, prefix) {
			delete(e.forwardMethods, k)
		}
	}
}

// argReprMismatch rejects argument i when no conversion reached its
// parameter's representation: a string, object or function where a number
// or boolean is expected, which only the strict lane leaves unconverted (it
// is tsc's "not assignable" there).
func argReprMismatch(val Value, paramTy Type, a ast.Expression, i int) error {
	if paramTy.IR == "" || val.Ty.IR == paramTy.IR {
		return nil
	}
	return internalAt(a.GetPos(), "argument %d representation %s for %s", i+1, val.Ty.IR, paramTy.IR)
}

// declaredMethodThis is the class a method's explicit `this: C` parameter
// names, when it is another class than the method's own.
func (e *Emitter) declaredMethodThis(body *ast.BlockStatement, own Type) (Type, bool) {
	if e.prog == nil || body == nil {
		return Type{}, false
	}
	for n, ta := range e.prog.ThisParams {
		fd, ok := n.(*ast.FunctionDeclaration)
		if !ok || fd.Body != body || ta == nil || annotIs(ta, "void") {
			continue
		}
		t := e.annotationType(ta)
		if !t.IsClass() || t.ClassName == own.ClassName || t.Nullable() {
			return Type{}, false
		}
		return t, true
	}
	return Type{}, false
}

// methodThisAny reports a method whose explicit `this` parameter is any.
func (e *Emitter) methodThisAny(body *ast.BlockStatement) bool {
	if e.prog == nil || body == nil {
		return false
	}
	for n, ta := range e.prog.ThisParams {
		if fd, ok := n.(*ast.FunctionDeclaration); ok && fd.Body == body {
			return annotIs(ta, "any")
		}
	}
	return false
}

// isGlobalObjectCtor reports a `new Object(…)`/`Object(…)` of the global
// Object, not a program class of that name.
func (e *Emitter) isGlobalObjectCtor(name string, qualified bool) bool {
	if name != "Object" || qualified {
		return false
	}
	if _, user := e.classes[name]; user {
		return false
	}
	_, bound := e.lookup(name)
	return !bound
}

// ensureObjectWrapAny defines (once) Object(v) of a boxed primitive v, its
// wrapper object, for the runtime's call of the Object constructor held as
// a value (dynobj.c): Object(v)'s own lowering over the boxed argument.
func (e *Emitter) ensureObjectWrapAny() string {
	const name = "__kml_object_wrap_any"
	if e.fnDecls[name] {
		return name
	}
	e.fnDecls[name] = true
	restore := e.beginDetachedFunc()
	defer restore()
	e.pushScope()
	defer e.popScope()
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitInstr(fmt.Sprintf("store i64 %%v, "+irPtr+" %s, align 8", slot))
	e.define("__kml_wrap_v", Symbol{Ptr: slot, Ty: TypeAny})
	v, err := e.emitObjectCtorDynamic(ast.NewIdentifier("__kml_wrap_v", ast.Pos{}))
	if err != nil {
		panic("Object(v) of a primitive: " + err.Error())
	}
	e.emitTerminator(fmt.Sprintf("ret i64 %s", v.Ref))
	e.writeFunc("i64 @"+name+"(i64 %v)", e.allocas.String()+e.body.String())
	return name
}

// emitObjectCtorDynamic is Object(v) of an `any`, decided at run time: a
// new empty object for null or undefined, v itself for an object, and a
// TypeError for a primitive, whose wrapper object does not exist yet.
func (e *Emitter) emitObjectCtorDynamic(arg ast.Expression) (Value, error) {
	v, err := e.emitExpr(arg)
	if err != nil {
		return Value{}, err
	}
	v = e.convert(v, TypeAny)
	res := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", res))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", v.Ref, res))
	tag, _ := e.emitUnboxTagPayload(v)
	nullish := e.anyIsNullish(v)
	emptyL, checkL, primL, doneL := e.freshLabel("objctor.empty"), e.freshLabel("objctor.check"), e.freshLabel("objctor.prim"), e.freshLabel("objctor.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", nullish, emptyL, checkL))
	e.emitLabel(emptyL)
	o, err := e.emitDynObjLiteral(&ast.ObjectLiteral{})
	if err != nil {
		return Value{}, err
	}
	o = e.convert(o, TypeAny)
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", o.Ref, res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(checkL)
	{
		// A bigint or a symbol (an object-tagged box): its wrapper.
		_, pay := e.emitUnboxTagPayload(v)
		objL, notObjL := e.emitTagCheck(tag, kmlTagObject, "objctor.cell")
		e.emitLabel(objL)
		_, isBig := e.emitBoxedBigIntProbe(pay)
		bigL, notBigL := e.freshLabel("objctor.bigint"), e.freshLabel("objctor.notbig")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBig, bigL, notBigL))
		e.emitLabel(bigL)
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", e.emitBuildWrapper(v.Ref, "BigInt").Ref, res))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(notBigL)
		_, isSym := e.emitBoxedSymbolProbe(pay)
		symL := e.freshLabel("objctor.symbol")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isSym, symL, doneL))
		e.emitLabel(symL)
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", e.emitBuildWrapper(v.Ref, "Symbol").Ref, res))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(notObjL)
	}
	prim := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ule i8 %s, %d", prim, tag, kmlTagBoolean))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", prim, primL, doneL))
	e.emitLabel(primL)
	// A primitive: its wrapper object.
	for _, w := range []struct {
		tags  []int
		class string
	}{{[]int{kmlTagInt, kmlTagFloat}, "Number"}, {[]int{kmlTagString}, "String"}, {[]int{kmlTagBoolean}, "Boolean"}} {
		hit := ""
		for _, t := range w.tags {
			is := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", is, tag, t))
			if hit != "" {
				or := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", or, hit, is))
				is = or
			}
			hit = is
		}
		wrapL, nextL := e.freshLabel("objctor.wrap"), e.freshLabel("objctor.next")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hit, wrapL, nextL))
		e.emitLabel(wrapL)
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", e.emitBuildWrapper(v.Ref, w.class).Ref, res))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(nextL)
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", out, res))
	return Value{Ref: out, Ty: TypeAny}, nil
}

// emitObjectCtor is `new Object(v)` and `Object(v)`: a new empty dynamic
// object for no argument, null or undefined, and an object argument itself.
// A primitive's wrapper object does not exist yet.
func (e *Emitter) emitObjectCtor(args []ast.Expression, pos ast.Pos) (Value, error) {
	empty := func() (Value, error) { return e.emitDynObjLiteral(&ast.ObjectLiteral{}) }
	if len(args) == 0 {
		return empty()
	}
	for _, a := range args[1:] {
		if _, err := e.emitExpr(a); err != nil {
			return Value{}, err
		}
	}
	at := e.typeOfExpr(args[0])
	switch {
	case at.IsNull() || at.IsUndefined():
		if _, err := e.emitExpr(args[0]); err != nil {
			return Value{}, err
		}
		return empty()
	case isUnconstrainedDynamic(at):
		return e.emitObjectCtorDynamic(args[0])
	case at.IsBigInt() || at.IsSymbol():
		// Its wrapper object (`[BigInt: 2n]`, `[Symbol: Symbol(s)]`).
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		word, err := e.emitBoxValue(v)
		if err != nil {
			return Value{}, err
		}
		class := "BigInt"
		if at.IsSymbol() {
			class = "Symbol"
		}
		e.wrapperScan = 2
		return e.emitBuildWrapper(word.Ref, class), nil
	case at.IsDynamic() || isNumberTy(at) || isStringTy(at) || at.IR == "i1" || isNullableScalar(at) || at.Nullable():
		// A primitive, or a value that may be one: decided on its box.
		return e.emitObjectCtorDynamic(args[0])
	}
	v, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	return e.convert(v, TypeAny), nil
}

// emitFunctionSymbolInstanceOf is `x instanceof Function` (every function
// and class: its static type, or a box's function tags) and `x instanceof
// Symbol` (only a Symbol wrapper object, which no value is here).
func (e *Emitter) emitFunctionSymbolInstanceOf(left ast.Expression, ctor string) (Value, error) {
	v, err := e.emitExpr(left)
	if err != nil {
		return Value{}, err
	}
	if ctor == "Symbol" {
		return Value{Ref: "0", Ty: TypeBool}, nil
	}
	if !v.Ty.IsDynamic() {
		if v.Ty.IsFunc() {
			return Value{Ref: "1", Ty: TypeBool}, nil
		}
		return Value{Ref: "0", Ty: TypeBool}, nil
	}
	e.ensureNanBox()
	tag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_nb_tag(i64 %s)", tag, v.Ref))
	isRef, isDyn, r := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isRef, tag, kmlTagFuncRef))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isDyn, tag, kmlTagDynFunc))
	// A function's chain holds Function.prototype unless
	// Object.setPrototypeOf rerouted it.
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", slot))
	e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", isRef, slot))
	fnL, doneL := e.freshLabel("iofn.fn"), e.freshLabel("iofn.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isDyn, fnL, doneL))
	e.emitLabel(fnL)
	e.ensureFnMeta()
	_, pay := e.emitUnboxTagPayload(v)
	in := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_fn_is_function_inst("+irPtr+" %s, "+irPtr+" %s)", in, e.emitIntToPtr(pay), e.fnProtoRec("")))
	inB := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", inB, in))
	e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", inB, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", r, slot))
	return Value{Ref: r, Ty: TypeBool}, nil
}

// dropExtraArgs is a JavaScript call's arguments past a method's declared
// parameters, which it ignores: they are evaluated, after the declared ones
// and before the call, then dropped. The declared ones are evaluated first
// into temporaries, except function literals, which have no side effects and
// keep their parameter's contextual type. Strict TypeScript rejects the call.
func (e *Emitter) dropExtraArgs(sig FuncSig, args []ast.Expression) ([]ast.Expression, error) {
	if !e.compatJS() || sig.HasRest || len(args) <= len(sig.ParamTypes) {
		return args, nil
	}
	for _, a := range args {
		if _, ok := a.(*ast.SpreadElement); ok {
			return args, nil
		}
	}
	n := len(sig.ParamTypes)
	kept := make([]ast.Expression, n)
	for i, a := range args {
		if i < n {
			switch a.(type) {
			case *ast.ArrowFunction, *ast.FunctionExpression:
				kept[i] = a
				continue
			}
		}
		v, err := e.emitExpr(a)
		if err != nil {
			return nil, err
		}
		if i < n {
			kept[i] = e.bindValue(v, a.GetPos())
		}
	}
	return kept, nil
}

// methodSig is a class method's signature as registerClasses builds it:
// its parameters and its declared or inferred return, the class's
// provisional type its `this`.
func (e *Emitter) methodSig(cd *ast.ClassDeclaration, m *ast.FunctionDeclaration, provisionalTy Type) FuncSig {
	sig := e.buildParamSig(m.Params, m)
	sig.IsAsync = m.IsAsync
	sig.This = takesStaticThis(m)
	if m.ReturnType != nil {
		sig.RetType = e.annotationType(m.ReturnType)
		sig.RetThis = annotIs(m.ReturnType, "this")
	} else if m.Body == nil {
		// Abstract method with no return-type annotation: nothing
		// to infer from (no body) — defaults to void, same as any
		// other unannotated-return function.
		sig.RetType = TypeVoid
	} else {
		sig.RetType = TypeVoid
		// Best-effort inference, same as registerFunctions — but the
		// method's own "this" also needs to be visible in the temp
		// scope funcBodyReturnType pushes for its parameters,
		// since the inferred return expression may read this.field.
		e.pushScope()
		mThis := provisionalTy
		if m.IsStatic {
			mThis = TypeAny // a static member's `this` is the class value, a box
		} else if declared, ok := e.declaredMethodThis(m.Body, provisionalTy); ok {
			mThis = declared // `m(this: Other)`, as emission binds it
		}
		e.define("this", Symbol{Ty: mThis})
		// Bind "super" too (typed as the base class), so a method whose
		// return expression is `super.method()` can resolve the base
		// method's type during inference — mirrors emitClassMember's own
		// super binding (ADR-00906).
		if cd.BaseClass != "" {
			if baseTy, ok := e.interfaces[cd.BaseClass]; ok {
				e.define("super", Symbol{Ty: baseTy})
			}
		}
		if inferred, ok := e.funcBodyReturnType(m.Body, sig.ParamNames, e.bodyParamTypes(m.Params, sig.ParamTypes)); ok {
			sig.RetType = inferred
		}
		e.popScope()
		// An unannotated async method returns the promise of its
		// result, as an annotated `Promise<T>` one does.
		if m.IsAsync && !m.IsGenerator && !sig.RetType.IsPromise() {
			sig.RetType = PromiseOf(sig.RetType)
		}
	}
	if e.jsOverrideWidened(cd, m) {
		// Its overrides disagree on their signatures (a number from one,
		// a string from another): the family is called through any.
		for i := range sig.ParamTypes {
			if !(sig.This && i == 0) {
				sig.ParamTypes[i] = TypeAny
			}
		}
		sig.RetType = TypeAny
		if m.IsAsync && !m.IsGenerator {
			sig.RetType = PromiseOf(TypeAny)
		}
	}
	return sig
}

// jsOverrideWidened reports, in the js lane, that the method m of cd
// belongs to a family of overrides (a method and those of the same name in
// its ancestors and descendants) whose signatures lower differently.
func (e *Emitter) jsOverrideWidened(cd *ast.ClassDeclaration, m *ast.FunctionDeclaration) bool {
	if !e.compatJS() || m.IsStatic || m.IsGenerator || e.prog == nil {
		return false
	}
	if e.jsOverrideWiden == nil {
		e.jsOverrideWiden = e.jsOverrideFamilies()
	}
	return len(e.jsOverrideWiden) > 0 && e.jsOverrideWiden[jsMethodRoot(e.jsClassDecls(), cd, m.Name, m.AccessorKind)+"."+m.AccessorKind+m.Name]
}

// generatorOverridden reports that the instance method name of class
// className is a generator a subclass overrides, or is overridden by a
// generator in a subclass: its generator construction is not the static
// class's to pick.
func (e *Emitter) generatorOverridden(className, name string) bool {
	if e.prog == nil || className == "" {
		return false
	}
	key := className + "." + name
	if v, ok := e.jsGenOverridden[key]; ok {
		return v
	}
	if e.jsGenOverridden == nil {
		e.jsGenOverridden = map[string]bool{}
	}
	v := e.generatorOverriddenUncached(className, name)
	e.jsGenOverridden[key] = v
	return v
}

func (e *Emitter) generatorOverriddenUncached(className, name string) bool {
	classes := e.jsClassDecls()
	cd := classes[className]
	if cd == nil {
		return false
	}
	gen := false
	for c, seen := cd, map[string]bool{}; c != nil && !seen[c.Name]; c = classes[c.BaseClass] {
		seen[c.Name] = true
		if m := plainMethod(c, name, ""); m != nil {
			gen = m.IsGenerator
			break
		}
	}
	for _, sub := range classes {
		if sub == cd {
			continue
		}
		derives := false
		for c, seen := classes[sub.BaseClass], map[string]bool{}; c != nil && !seen[c.Name]; c = classes[c.BaseClass] {
			seen[c.Name] = true
			if c == cd {
				derives = true
				break
			}
		}
		if !derives {
			continue
		}
		if m := plainMethod(sub, name, ""); m != nil && (gen || m.IsGenerator) {
			return true
		}
	}
	return false
}

// jsClassDecls is the program's class declarations by name.
func (e *Emitter) jsClassDecls() map[string]*ast.ClassDeclaration {
	if e.jsClassDeclMap != nil {
		return e.jsClassDeclMap
	}
	out := map[string]*ast.ClassDeclaration{}
	defer func() { e.jsClassDeclMap = out }()
	ast.Inspect(e.prog, func(n ast.Node) bool {
		if cd, ok := n.(*ast.ClassDeclaration); ok {
			if _, seen := out[cd.Name]; !seen {
				out[cd.Name] = cd
			}
		}
		return true
	})
	return out
}

// plainMethod is cd's own instance method (kind "") or accessor ("get",
// "set") name, if any.
func plainMethod(cd *ast.ClassDeclaration, name, kind string) *ast.FunctionDeclaration {
	for _, m := range cd.Methods {
		if m.Name == name && !m.IsStatic && m.AccessorKind == kind && !m.IsOverloadSig {
			return m
		}
	}
	return nil
}

// jsMethodRoot is the topmost class of cd's chain declaring the method (or
// accessor of kind) name.
func jsMethodRoot(classes map[string]*ast.ClassDeclaration, cd *ast.ClassDeclaration, name, kind string) string {
	root := cd.Name
	seen := map[string]bool{}
	for c := cd; c != nil && !seen[c.Name]; c = classes[c.BaseClass] {
		seen[c.Name] = true
		if plainMethod(c, name, kind) != nil {
			root = c.Name
		}
		if c.BaseClass == "" {
			break
		}
	}
	return root
}

// jsOverrideFamilies finds the families whose methods' checker signatures
// lower to different parameter or result representations.
func (e *Emitter) jsOverrideFamilies() map[string]bool {
	out := map[string]bool{}
	o := e.reprOracle()
	if o == nil {
		return out
	}
	classes := e.jsClassDecls()
	lowered := func(m *ast.FunctionDeclaration) (Type, bool) {
		if st := o.c.FunctionSignature(m); st != nil {
			if t, ok := o.r.of(st); ok && t.IsFunc() {
				return t, true
			}
		}
		if len(m.Params) > 0 || m.Body == nil {
			return Type{}, false
		}
		// No parameters: its body's result, as the checker types it.
		r, ok, answered := e.checkerBodyReturn(m.Body)
		if !answered {
			return Type{}, false
		}
		if !ok {
			r = TypeVoid
		}
		return FuncType(nil, r), true
	}
	sameRepr := func(a, b Type) bool {
		if a.FuncRetType == nil || b.FuncRetType == nil || len(a.FuncParams) != len(b.FuncParams) {
			return a.FuncRetType == nil && b.FuncRetType == nil
		}
		if reprKey(*a.FuncRetType) != reprKey(*b.FuncRetType) {
			return false
		}
		for i := range a.FuncParams {
			if reprKey(a.FuncParams[i]) != reprKey(b.FuncParams[i]) {
				return false
			}
		}
		return true
	}
	for _, cd := range classes {
		for _, m := range cd.Methods {
			if m.IsStatic || m.IsGenerator || m.IsOverloadSig || cd.BaseClass == "" {
				continue
			}
			seen := map[string]bool{cd.Name: true}
			for b := classes[cd.BaseClass]; b != nil && !seen[b.Name]; b = classes[b.BaseClass] {
				seen[b.Name] = true
				if am := plainMethod(b, m.Name, m.AccessorKind); am != nil {
					mt, ok1 := lowered(m)
					at, ok2 := lowered(am)
					if ok1 && ok2 && !sameRepr(mt, at) {
						out[jsMethodRoot(classes, cd, m.Name, m.AccessorKind)+"."+m.AccessorKind+m.Name] = true
					}
					break
				}
				if b.BaseClass == "" {
					break
				}
			}
		}
	}
	return out
}

// ctorAssigns reports whether cd's constructor assigns this.name before
// anything can read it: a parameter property of that name, or a
// `this.name = …` statement at its top level.
func ctorAssigns(cd *ast.ClassDeclaration, name string) bool {
	ctor := cd.Constructor
	if ctor == nil || ctor.Body == nil {
		return false
	}
	for _, p := range ctor.Params {
		if p.IsPropertyParam() && p.Name == name {
			return true
		}
	}
	for _, st := range ctor.Body.Body {
		es, ok := st.(*ast.ExpressionStatement)
		if !ok {
			continue
		}
		as, ok := es.Expr.(*ast.AssignmentExpression)
		if !ok || as.Op != "=" {
			continue
		}
		if m, ok := as.Left.(*ast.MemberExpression); ok && m.Property == name {
			if _, this := m.Object.(*ast.ThisExpression); this {
				return true
			}
		}
	}
	return false
}

// markUnassignedField records that class's field may be read before
// anything assigns it.
func (e *Emitter) markUnassignedField(class, field string) {
	if e.unassignedFields == nil {
		e.unassignedFields = map[string]bool{}
	}
	e.unassignedFields[class+"."+field] = true
}

// unassignedField reports whether m reads a class field that may still be
// unassigned (markUnassignedField), in its class or a base class: tsc
// types the read T, but it may be undefined, as an unchecked index read.
func (e *Emitter) unassignedField(m *ast.MemberExpression) bool {
	if len(e.unassignedFields) == 0 || m.Optional {
		return false
	}
	rt := e.typeOfExpr(m.Object)
	if !rt.IsClass() || rt.IsDynamic() {
		return false
	}
	for cls, n := rt.ClassName, 0; cls != "" && n < 64; n++ {
		if e.unassignedFields[cls+"."+m.Property] {
			return true
		}
		info, ok := e.classes[cls]
		if !ok {
			break
		}
		cls = info.BaseClass
	}
	return false
}

// foundAtTop reports a name lookup resolves in the program's own frame
// (or a module global), not in a function being emitted.
func (e *Emitter) foundAtTop(name string) bool {
	for i := len(e.scopes) - 1; i >= 0; i-- {
		if _, ok := e.scopes[i].syms[name]; ok {
			return e.scopes[i].top
		}
	}
	_, ok := e.moduleGlobals[name]
	return ok
}

// genericFamilyID is the id every instantiation of generic class name
// records as its parent, so `x instanceof Box` holds for each `Box<T>`.
func (e *Emitter) genericFamilyID(name string) int64 {
	if e.genericFamilies == nil {
		e.genericFamilies = map[string]int64{}
	}
	id, ok := e.genericFamilies[name]
	if !ok {
		id = e.classTypeID(name)
		e.genericFamilies[name] = id
	}
	return id
}

// emitGenericInstanceOf is `left instanceof G` for a generic class G: an
// instance of any of its instantiations, or of a class extending one.
func (e *Emitter) emitGenericInstanceOf(left ast.Expression, name string) (Value, error) {
	fam := e.genericFamilyID(name)
	leftVal, err := e.emitExpr(left)
	if err != nil {
		return Value{}, err
	}
	switch {
	case leftVal.Ty.IsClass():
		li := e.classes[leftVal.Ty.ClassName]
		of := li.GenericOf == name
		for _, anc := range li.AncestorChain {
			if e.classes[anc].GenericOf == name {
				of = true
			}
		}
		if of {
			if leftVal.Ty.IR == irPtr {
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", r, leftVal.Ref))
				return Value{Ref: r, Ty: TypeBool}, nil
			}
			return Value{Ref: "1", Ty: TypeBool}, nil
		}
		if leftVal.Ty.IR != irPtr {
			return Value{Ref: "0", Ty: TypeBool}, nil
		}
		// A subclass slot may hold an instance of a descendant extending
		// an instantiation: its header id decides.
		b, err := e.emitBoxValue(leftVal)
		if err != nil {
			return Value{}, err
		}
		leftVal = b
	case !leftVal.Ty.IsDynamic():
		return Value{Ref: "0", Ty: TypeBool}, nil
	}
	e.ensureAnyToPrimitive()
	e.ensureNanBox()
	e.ensureErrorNames()
	e.ensureStrHeaderRuntime()
	tag, payload := e.emitUnboxTagPayload(leftVal)
	ptrReg := e.emitIntToPtr(payload)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", slot))
	e.emitInstr(fmt.Sprintf("store i1 0, "+irPtr+" %s, align 1", slot))
	objL, bagChkL, bagL, doneL := e.freshLabel("ginst.obj"), e.freshLabel("ginst.bagchk"), e.freshLabel("ginst.bag"), e.freshLabel("ginst.done")
	isObj, isBag := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, bagChkL))
	e.emitLabel(objL)
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", hdr, ptrReg))
	e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", e.emitClassIs(hdr, fam), slot))
	e.emitTerminator("br label %" + doneL)
	e.emitLabel(bagChkL)
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isBag, tag, kmlTagDynObject))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBag, bagL, doneL))
	e.emitLabel(bagL)
	ct := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_classtag("+irPtr+" %s)", ct, ptrReg))
	e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", e.emitClassIs(ct, fam), slot))
	e.emitTerminator("br label %" + doneL)
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", r, slot))
	return Value{Ref: r, Ty: TypeBool}, nil
}

// typeNodeDepth is how deeply tuples, arrays and type arguments nest in n.
func typeNodeDepth(n ast.TypeNode) int {
	d := 0
	deeper := func(ns ...ast.TypeNode) {
		for _, m := range ns {
			if k := typeNodeDepth(m) + 1; k > d {
				d = k
			}
		}
	}
	switch x := n.(type) {
	case *ast.TupleType:
		deeper(x.Elements...)
	case *ast.ArrayType:
		deeper(x.ElementType)
	case *ast.TypeReference:
		deeper(x.TypeArgs...)
	case *ast.UnionType:
		deeper(x.Types...)
	}
	return d
}

// presentRef is whether v holds a value: a reference that may hold none
// (`o?.d`, `d?: Date`) is a null pointer there, and is no instance; a
// value of any other representation always holds one.
func (e *Emitter) presentRef(v Value) string {
	if StructFieldIR(v.Ty) != irPtr || v.Ref == "null" {
		if v.Ref == "null" {
			return "0"
		}
		return "1"
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", r, v.Ref))
	return r
}

// hostWrapperNames are the builtins a class instance's host body makes it
// an instance of without a class of its own.
var hostWrapperNames = map[string]bool{"Number": true, "String": true, "Boolean": true, "ArrayBuffer": true, "SharedArrayBuffer": true}

// someClassLacks reports a concrete class of the program whose chain leaves
// method name without a body (an abstract or optional method no class
// implements): its vtable slot is empty.
func (e *Emitter) someClassLacks(name string) bool {
	for _, info := range e.classes {
		if m := info.Methods[name]; m != nil && m.Body == nil {
			return true
		}
	}
	return false
}

// jsFieldTakesOtherKind reports, under -compat=js, an unannotated field of
// a number, string or boolean that some assignment in the program
// (`<obj>.name = v`) gives a value of another or unknown kind: the
// property holds each in turn, so its slot is a boxed any (the field
// counterpart of the local-binding widening).
func (e *Emitter) jsFieldTakesOtherKind(prog *ast.Program, name string, fty Type) bool {
	if !e.compatJS() || prog == nil {
		return false
	}
	k := scalarTypeKind(fty)
	if k == "" {
		return false
	}
	found := false
	for _, st := range prog.Body {
		ast.Inspect(st, func(n ast.Node) bool {
			if found {
				return false
			}
			as, ok := n.(*ast.AssignmentExpression)
			if !ok || as.Op != "=" {
				return true
			}
			if m, ok := as.Left.(*ast.MemberExpression); ok && m.Property == name {
				rt := e.typeOfExpr(as.Right)
				if rk := scalarTypeKind(rt); rk != k && (rk != "" || isUnconstrainedDynamic(rt) || rt.IsNull() || rt.IsUndefined()) {
					found = true
				}
			}
			return true
		})
	}
	return found
}

// initInstanceWords writes a fresh instance's words before its constructor
// runs: its header (the TagID, an error subclass's flagged), every number
// and any field's undefined, and its vtable.
func (e *Emitter) initInstanceWords(dataReg, className string, info ClassInfo) {
	tagGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 0", tagGep, info.Ty.StructIR(), dataReg))
	// An error-subclass instance carries its TagID in field 0 marked with the
	// Error type-id flag, so a boxed instance is recognised as an Error at a
	// general render/JSON/member/instanceof site (TDD-00222). A plain class
	// stores the bare TagID.
	storedTag := info.TagID
	if info.IsErrorSubclass {
		storedTag = errorTypeIDStored(storedTag)
	}
	e.heapStore("i64", fmt.Sprint(storedTag), tagGep, 8)
	e.emitPresenceAll(dataReg, info.Ty, info.DeclareFields)
	// A number field is undefined until a constructor or initializer
	// assigns it: the double slot's sentinel, not calloc's 0 (TDD-00241).
	// Error's own slots (errno, port, …) keep 0 as their absent state.
	for i, f := range info.Ty.Fields {
		if _, _, errSlot := errorObjType.FieldIndex(f.Name); (errSlot || strings.HasPrefix(f.Name, "__kml_err_")) && e.isErrorValueTy(info.Ty) {
			continue
		}
		if isF64Slot(f.Ty) && f.Name != ClassTagField {
			g := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", g, info.Ty.StructIR(), dataReg, i))
			e.heapStore("i64", fmt.Sprint(undefF64), g, 8)
		} else if isUnconstrainedDynamic(f.Ty) && f.Ty.IR == "i64" && !isHiddenField(f.Name) {
			// An any field is undefined until assigned: the box's own
			// undefined word, which === compares equal, not calloc's 0.
			g := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", g, info.Ty.StructIR(), dataReg, i))
			e.heapStore("i64", fmt.Sprint(nbUndefined), g, 8)
		}
	}

	if info.Ty.HasVTable {
		vtGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", vtGep, info.Ty.StructIR(), dataReg, info.Ty.VTableIndex()))
		e.heapStore(irPtr, "@"+className+"_vtable", vtGep, 8)
	}
}

// emitTakePendingProto gives the object at dataReg the prototype a
// Reflect.construct newTarget left pending (shapesrc/shape.c), if any.
func (e *Emitter) emitTakePendingProto(dataReg string) {
	if !e.protoUses().newTarget {
		return
	}
	e.ensureShapeRuntime()
	e.declareFn("__kml_shape_take_pending", "declare void @__kml_shape_take_pending("+irPtr+")")
	f, set := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, "+irPtr+" @__kml_pending_proto_set, align 4", f))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", set, f))
	takeL, doneL := e.freshLabel("new.pendproto"), e.freshLabel("new.ownproto")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", set, takeL, doneL))
	e.emitLabel(takeL)
	e.emitInstr(fmt.Sprintf("call void @__kml_shape_take_pending("+irPtr+" %s)", dataReg))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
}

// orBuiltinProtoChain is r, or v a plain object whose chain holds builtin
// name's prototype (`F.prototype = Date.prototype; new F()`,
// `Object.create(Map.prototype)`), as OrdinaryHasInstance walks it.
func (e *Emitter) orBuiltinProtoChain(r, v Value, name string) Value {
	if _, hasProto := hostProtoClasses[name]; !hasProto && !isErrorKindName(name) {
		return r
	}
	tag, pay := e.emitUnboxTagPayload(v)
	out := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", out))
	e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", r.Ref, out))
	isBag, chkL, doneL := e.freshReg(), e.freshLabel("inst.chain"), e.freshLabel("inst.chain.done")
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isBag, tag, kmlTagDynObject))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBag, chkL, doneL))
	e.emitLabel(chkL)
	// A prototype nothing built yet is on no object's chain: this test must
	// not build it (and every member it reaches into the program).
	ppay := e.emitBuiltinProtoPeek(name)
	walkL := e.freshLabel("inst.chain.walk")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(ppay), doneL, walkL))
	e.emitLabel(walkL)
	e.declareFn("__kml_dynobj_chain_has", "declare i64 @__kml_dynobj_chain_has("+irPtr+", "+irPtr+")")
	has, hb := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_chain_has("+irPtr+" %s, "+irPtr+" %s)", has, e.emitIntToPtr(pay), ppay))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", hb, has))
	e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", hb, out))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", res, out))
	return Value{Ref: res, Ty: TypeBool}
}
