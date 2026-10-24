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
// resolveType's existing named-type lookup resolves a class name in any
// type annotation with no changes of its own.
type ClassInfo struct {
	Ty          Type // ClassType(name, inherited, own, hasVTable) — the instance's storage type
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
	HostAnnot *ast.TypeAnnotation
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
	// ReadonlyFields is the set of `readonly` instance-field names (own +
	// inherited) — TDD-00154. A write is rejected unless it is inside the
	// constructor of the field's declaring class (FieldOrigin).
	ReadonlyFields map[string]bool
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
// drill into further* (emitMember, emitOptionalMember, inferExprType's
// field-read case) re-resolves a class-typed field's type through this
// helper before handing it back, so the caller always sees the final,
// fully-resolved field list — without changing Field/Type's storage shape
// or touching any of the many other call sites that read a field's type
// for a non-chaining purpose (coercion, alignment, GEP storage width),
// where the snapshot's IR-level shape (always "ptr" for any class) was
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
// resolved via resolveType; a rest parameter with no annotation defaults to
// number[]; a destructured one the type its pattern gives it (fd's, when
// the list is a declaration's); anything else unannotated defaults to
// (inferred) TypeI64.
func (e *Emitter) buildParamSig(params []ast.Param, fd *ast.FunctionDeclaration) FuncSig {
	var sig FuncSig
	for i, p := range params {
		var pty Type
		if p.Type != nil {
			pty = e.resolveType(p.Type)
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
		own, theirs := e.resolveType(p.Type), e.resolveType(bp.Type)
		if own.IR == theirs.IR && own.IsArray() == theirs.IsArray() && own.IsDynamic() == theirs.IsDynamic() {
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

// checkReadonlyWrite enforces the TS `readonly` field modifier (TDD-00154): a
// write to a readonly field is allowed only inside the constructor of the class
// that declares it (which also covers field initializers, spliced into the
// constructor). Any other write — a regular method, an outside `obj.x = v`, or a
// subclass constructor writing an inherited readonly field — is a clean error.
func (e *Emitter) checkReadonlyWrite(className, fieldName string, pos ast.Pos) error {
	info, ok := e.classes[className]
	if !ok || !info.ReadonlyFields[fieldName] {
		return nil
	}
	origin := info.FieldOrigin[fieldName]
	if e.currentCtorClass == origin {
		return nil
	}
	return fmt.Errorf("%d:%d: cannot assign to '%s' because it is a read-only property (declared on class '%s') — a readonly field can only be set in that class's constructor", pos.Line, pos.Col, fieldName, inspectClassName(origin))
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
		return Value{}, fmt.Errorf("%d:%d: class '%s' has no static field '%s'", pos.Line, pos.Col, className, fieldName)
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", reg, storageIR(fieldTy), e.staticFieldReadPtr(className, fieldName), fieldTy.Align()))
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
		e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", own, e.staticOwnPtr(chain[i], fieldName)))
		sel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sel, own, e.staticSlotPtr(chain[i], fieldName), ptr))
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
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", StructFieldIR(v.Ty), v.Ref, slot))
			// A nullable scalar's slot holds its { i1, payload } pair, read
			// back as one (null stays null in the setter).
			e.define(tmp, Symbol{Ptr: slot, Ty: v.Ty})
			if _, err := e.emitStaticMethodCall(info, className, accessorMethodName("set", fieldName), []ast.Expression{ast.NewIdentifier(tmp, pos)}, pos); err != nil {
				return Value{}, err
			}
			return v, nil
		}
		return Value{}, fmt.Errorf("%d:%d: class '%s' has no static field '%s'", pos.Line, pos.Col, className, fieldName)
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
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", curReg, storageIR(fieldTy), globalPtr, fieldTy.Align()))
		cur := Value{Ref: curReg, Ty: fieldTy}
		rhsVal, err := e.emitExpr(rhsExpr)
		if err != nil {
			return Value{}, err
		}
		if err := dateCompoundAssignGuard(op, fieldTy.IsDate(), rhsVal.Ty.IsDate()); err != nil {
			return Value{}, fmt.Errorf("%d:%d: %s", pos.Line, pos.Col, err)
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
			rt := e.inferExprType(as.Right)
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
					return fmt.Errorf("%d:%d: generic class '%s' cannot use 'extends' — not yet supported", cd.GetPos().Line, cd.GetPos().Col, cd.Name)
				}
				if cd.IsAbstract {
					return fmt.Errorf("%d:%d: generic class '%s' cannot be abstract — not yet supported", cd.GetPos().Line, cd.GetPos().Col, cd.Name)
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
			return fmt.Errorf("%d:%d: class '%s' has a cyclic 'extends' chain", cd.GetPos().Line, cd.GetPos().Col, name)
		case 2:
			return nil
		}
		visitState[name] = 1
		if e.isErrorRootKind(cd.BaseClass) {
			// A synthetic root (TDD-00155 Stage 6): Error (or a builtin kind
			// of it) is never a registered class — nothing to recurse into;
			// its fields are grafted in Pass 1.
			if len(cd.BaseTypeArgs) > 0 {
				return fmt.Errorf("%d:%d: class '%s' extends %s with type arguments, but %s is not generic", cd.GetPos().Line, cd.GetPos().Col, name, cd.BaseClass, cd.BaseClass)
			}
		} else if _, user := classDeclByName[cd.BaseClass]; !user && cd.BaseClass != "" && e.hostBaseOf(cd) != nil {
			// A builtin host base (TDD-00247): never a registered class;
			// its body is grafted in Pass 1.
		} else if _, user := classDeclByName[cd.BaseClass]; !user && cd.BaseClass != "" && e.isHostLayoutBase(cd.BaseClass) {
			return fmt.Errorf("%d:%d: class '%s' extends the builtin '%s': extending a builtin other than Error is not supported yet", cd.GetPos().Line, cd.GetPos().Col, ast.Unmangle(name), cd.BaseClass)
		} else if len(cd.BaseTypeArgs) > 0 && !e.typeOnlyGenerics[cd.BaseClass] {
			return fmt.Errorf("%d:%d: class '%s' extends '%s' with type arguments, but a generic class base is not supported yet", cd.GetPos().Line, cd.GetPos().Col, name, cd.BaseClass)
		} else if cd.BaseClass != "" {
			if _, ok := classDeclByName[cd.BaseClass]; !ok {
				return fmt.Errorf("%d:%d: class '%s' extends unknown class '%s'", cd.GetPos().Line, cd.GetPos().Col, name, cd.BaseClass)
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
		var hostAnnot *ast.TypeAnnotation
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
		// TDD-00154: readonly field names (own + inherited). A write is checked
		// against FieldOrigin so only the *declaring* class's constructor may set
		// it, matching TypeScript.
		readonlyFields := make(map[string]bool, len(baseInfo.ReadonlyFields))
		for k, v := range baseInfo.ReadonlyFields {
			readonlyFields[k] = v
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
			if f.Name == ClassTagField {
				return fmt.Errorf("%d:%d: class '%s' cannot declare a field named '%s' — reserved for the compiler's internal runtime type tag", cd.GetPos().Line, cd.GetPos().Col, cd.Name, ClassTagField)
			}
			if f.Name == ClassVTableField {
				return fmt.Errorf("%d:%d: class '%s' cannot declare a field named '%s' — reserved for the compiler's internal runtime vtable pointer", cd.GetPos().Line, cd.GetPos().Col, cd.Name, ClassVTableField)
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
					fty = e.resolveType(f.Type)
					if f.Initializer == nil {
						// `static x: number;` is undefined until assigned
						// (no initialization check covers a static field).
						fty = undefinedableElem(fty)
					}
				} else if f.Initializer != nil {
					fty = e.inferExprType(f.Initializer)
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
				} else if e.redeclaresSameStorage(inheritedFields, f) {
					continue
				} else {
					return fmt.Errorf("%d:%d: class '%s' redeclares inherited field '%s' with a different type", cd.GetPos().Line, cd.GetPos().Col, cd.Name, f.Name)
				}
			}
			seen[f.Name] = true
			// An unannotated field (`x = expr`, TDD-00063 Stage 1) takes its
			// type from its initializer, the same compile-time inference a
			// `let x = expr` uses; an annotated field keeps its declared type.
			var fty Type
			if f.Type != nil {
				fty = e.resolveType(f.Type)
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
				fty = e.inferExprType(f.Initializer)
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
				if (fty.IsNull() || fty.IsUndefined()) && !e.opts.NoAny && e.classFieldNullEvolves(cd, f.Name) {
					fty = TypeAny
				}
			}
			// A `name?: T` field is optional: an own property from
			// construction (every declared field is), until `delete` removes
			// it (TDD-00256's presence word, set whole by emitNew).
			ownFields = append(ownFields, Field{Name: f.Name, Ty: fty, Optional: f.Optional && !f.Static})
			fieldOrigin[f.Name] = cd.Name
			if f.Declare && !f.Static {
				declareFields[f.Name] = true
			}
			if f.Readonly {
				readonlyFields[f.Name] = true
			}
		}
		// JS-compat field inference (TDD-00022 sub-problem 2, `-compat=js`
		// only): a class with a constructor and no declared instance fields
		// collects them from the constructor's `this.NAME = expr` assignments
		// — the vanilla-JS class shape. See emit_classes_jsinfer.go.
		if e.compatJS() && len(ownFields) == 0 && cd.Constructor != nil {
			inferred, err := e.jsInferConstructorFields(cd, seen)
			if err != nil {
				return err
			}
			for _, f := range inferred {
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
			BaseClass:               cd.BaseClass,
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
			ReadonlyFields:          readonlyFields,
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
					return fmt.Errorf("%d:%d: a static generator method in a class declared inside a function, or reading `arguments`, is not yet supported ('%s' on class '%s')", m.GetPos().Line, m.GetPos().Col, m.Name, cd.Name)
				}
				if m.Body == nil {
					return fmt.Errorf("%d:%d: an abstract generator method ('%s' on class '%s') is not supported", m.GetPos().Line, m.GetPos().Col, m.Name, cd.Name)
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
						return fmt.Errorf("%d:%d: getter '%s' on class '%s' must take no parameters", m.GetPos().Line, m.GetPos().Col, m.Name, cd.Name)
					}
					if sig.RetType.IR == "void" && e.compatJS() {
						// JavaScript's getter falling off its end: undefined.
						sig.RetType = TypeAny
					} else if sig.RetType.IR == "void" {
						return fmt.Errorf("%d:%d: getter '%s' on class '%s' must return a value", m.GetPos().Line, m.GetPos().Col, m.Name, cd.Name)
					}
				} else { // "set"
					if nParams != 1 {
						return fmt.Errorf("%d:%d: setter '%s' on class '%s' must take exactly one parameter", m.GetPos().Line, m.GetPos().Col, m.Name, cd.Name)
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
						return fmt.Errorf("%d:%d: class '%s' cannot declare static accessor '%s' — a static field with that name already exists", m.GetPos().Line, m.GetPos().Col, cd.Name, m.Name)
					}
					if ownStaticDeclared[mangled] {
						return fmt.Errorf("%d:%d: class '%s' declares more than one static %s accessor for '%s'", m.GetPos().Line, m.GetPos().Col, cd.Name, m.AccessorKind, m.Name)
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
					return fmt.Errorf("%d:%d: class '%s' cannot declare accessor '%s' — a field with that name already exists", m.GetPos().Line, m.GetPos().Col, cd.Name, m.Name)
				}
				if _, ok := info.MethodSigs[m.Name]; ok {
					return fmt.Errorf("%d:%d: class '%s' cannot declare accessor '%s' — a method with that name already exists", m.GetPos().Line, m.GetPos().Col, cd.Name, m.Name)
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
						if setter := accessorDecl(cd, "set", m.Name, false); setter != nil && len(setter.Params) == 1 && setter.Params[0].Type == nil {
							if m.AccessorKind == "set" {
								sig.ParamTypes[0] = getterRet
							} else {
								otherSig.ParamTypes[0] = getterRet
								info.MethodSigs[accessorMethodName(otherKind, m.Name)] = otherSig
							}
							setterParam = getterRet
						}
					}
					if getterRet.IR != setterParam.IR {
						return fmt.Errorf("%d:%d: getter/setter '%s' on class '%s' disagree on type", m.GetPos().Line, m.GetPos().Col, m.Name, cd.Name)
					}
				}

				if ownDeclared[mangled] {
					return fmt.Errorf("%d:%d: class '%s' declares more than one %s accessor for '%s'", m.GetPos().Line, m.GetPos().Col, cd.Name, m.AccessorKind, m.Name)
				}
				ownDeclared[mangled] = true

				if existingSig, overriding := info.MethodSigs[mangled]; overriding {
					if !sigCompatible(existingSig, sig) {
						return fmt.Errorf("%d:%d: accessor '%s' on class '%s' overrides an inherited accessor with an incompatible signature", m.GetPos().Line, m.GetPos().Col, m.Name, cd.Name)
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
					return fmt.Errorf("%d:%d: class '%s' declares more than one method named '%s'", m.GetPos().Line, m.GetPos().Col, cd.Name, m.Name)
				}
				ownDeclared[m.Name] = true
				info.MethodImplementor[m.Name] = cd.Name
				info.MethodSigs[m.Name] = sig
				info.Methods[m.Name] = m
				continue
			}

			if m.IsStatic {
				if ownStaticDeclared[m.Name] {
					return fmt.Errorf("%d:%d: class '%s' declares more than one static method named '%s'", m.GetPos().Line, m.GetPos().Col, cd.Name, m.Name)
				}
				ownStaticDeclared[m.Name] = true
				info.StaticMethodSigs[m.Name] = sig
				info.StaticMethodImplementor[m.Name] = cd.Name
				continue
			}

			if ownDeclared[m.Name] {
				return fmt.Errorf("%d:%d: class '%s' declares more than one method named '%s'", m.GetPos().Line, m.GetPos().Col, cd.Name, m.Name)
			}
			ownDeclared[m.Name] = true
			// Symmetric to the accessor branch's own field/method collision
			// check above: a plain method can't reuse a name already
			// claimed by an accessor (own-earlier-in-this-class, or
			// inherited — both already live in info.MethodSigs by now).
			if _, ok := info.MethodSigs[accessorMethodName("get", m.Name)]; ok {
				return fmt.Errorf("%d:%d: class '%s' cannot declare method '%s' — a getter with that name already exists", m.GetPos().Line, m.GetPos().Col, cd.Name, m.Name)
			}
			if _, ok := info.MethodSigs[accessorMethodName("set", m.Name)]; ok {
				return fmt.Errorf("%d:%d: class '%s' cannot declare method '%s' — a setter with that name already exists", m.GetPos().Line, m.GetPos().Col, cd.Name, m.Name)
			}

			if existingSig, overriding := info.MethodSigs[m.Name]; overriding {
				if !sigCompatible(existingSig, sig) {
					needed, supported := funcAdapterPlan(funcTypeFromSig(sig), funcTypeFromSig(existingSig))
					if !needed || !supported || m.IsAsync || existingSig.This || sig.This {
						return fmt.Errorf("%d:%d: method '%s' on class '%s' overrides an inherited method with an incompatible signature", m.GetPos().Line, m.GetPos().Col, m.Name, cd.Name)
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
		if !cd.IsAbstract {
			for mname, decl := range info.Methods {
				if decl.Body == nil && !decl.IsOptional {
					return fmt.Errorf("%d:%d: class '%s' does not implement abstract method '%s' inherited from '%s'", cd.GetPos().Line, cd.GetPos().Col, cd.Name, mname, info.MethodImplementor[mname])
				}
			}
		}

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
			callsSuper := hasTopLevelSuperCall(cd.Constructor.Body)
			if baseCtor != nil && !callsSuper && containsSuperCall(cd.Constructor.Body) {
				return fmt.Errorf("%d:%d: constructor of class '%s' must call super(...) (base class '%s' has a constructor)", cd.Constructor.GetPos().Line, cd.Constructor.GetPos().Col, cd.Name, cd.BaseClass)
			}
			// A derived class must call super() (TS2377) even when its base
			// has no constructor of its own: that call is then a no-op
			// (emitSuperCall).
			if baseCtor == nil && callsSuper && !isErrorRoot && !haveBase && hostBase == nil {
				return fmt.Errorf("%d:%d: constructor of class '%s' calls super(...) but the class has no base class", cd.Constructor.GetPos().Line, cd.Constructor.GetPos().Col, cd.Name)
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
			if inits := classFieldInitStmts(cd); len(inits) > 0 {
				at := topLevelSuperCallIndex(cd.Constructor.Body) + 1 // -1 → 0 when there is no super()
				stmts := cd.Constructor.Body.Body
				spliced := make([]ast.Statement, 0, len(stmts)+len(inits))
				spliced = append(spliced, stmts[:at]...)
				spliced = append(spliced, inits...)
				spliced = append(spliced, stmts[at:]...)
				cd.Constructor.Body.Body = spliced
			}

		case (len(ownFields) > 0 || len(classFieldInitStmts(cd)) > 0 || (e.standardDecorators() && classHasStandardDecorators(cd))) && (baseCtor == nil || !baseCtorSig.HasRest):
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
					{Name: "pattern", Type: &ast.TypeAnnotation{Name: "any"}, Optional: true},
					{Name: "flags", Type: &ast.TypeAnnotation{Name: "string"}, Optional: true},
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
				second := ast.Param{Name: "options", Type: &ast.TypeAnnotation{Name: "any"}, Optional: true}
				if cd.BaseClass == "DOMException" {
					second = ast.Param{Name: "name", Type: &ast.TypeAnnotation{Name: "string"}, Optional: true}
				}
				params = []ast.Param{{Name: "message", Type: &ast.TypeAnnotation{Name: "string"}, Optional: true}, second}
				superCall := ast.NewCallExpression(ast.NewSuperExpression(cd.GetPos()), []ast.Expression{ast.NewIdentifier("message", cd.GetPos()), ast.NewIdentifier(second.Name, cd.GetPos())}, cd.GetPos())
				stmts = append(stmts, ast.NewExpressionStatement(superCall, cd.GetPos()))
			} else if baseCtor != nil {
				params = make([]ast.Param, len(baseCtorSig.ParamNames))
				superArgs := make([]ast.Expression, len(baseCtorSig.ParamNames))
				for i, pname := range baseCtorSig.ParamNames {
					params[i] = ast.Param{Name: pname}
					superArgs[i] = ast.NewIdentifier(pname, cd.GetPos())
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

		case len(ownFields) > 0:
			// Only reachable now when the class adds own fields *and* the base
			// has a rest-parameter constructor: a pass-through
			// `constructor(...args) { super(...args) }` isn't representable
			// without a general spread-call mechanism this compiler lacks, so an
			// explicit derived constructor is required (same limitation the
			// no-own-fields pass-through case below carries).
			return fmt.Errorf("%d:%d: class '%s' adds fields but its base class has a rest-parameter constructor — write an explicit constructor that calls super(...)", cd.GetPos().Line, cd.GetPos().Col, cd.Name)
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
		return fmt.Errorf("%d:%d: a decorator on a generic class is not yet supported", cd.GetPos().Line, cd.GetPos().Col)
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
		return fmt.Errorf("%d:%d: a decorator on this member is not yet supported — accessor (get/set), static, and generator method decorators, and decorators on a generic class's methods, are the unsupported cases; a plain instance method decorator is supported", m.GetPos().Line, m.GetPos().Col)
	}
	if len(cd.AutoAccessors) > 0 && !e.standardDecorators() {
		return fmt.Errorf("%d:%d: an `accessor` auto-field decorator is a standard (TC39) decorator — compile with -decorators=standard", cd.GetPos().Line, cd.GetPos().Col)
	}
	if info.Constructor != nil {
		llvmName := cd.Name + ctorSymbolSuffix
		e.currentCtorClass = cd.Name
		e.memberFrame.node, e.memberFrame.name = cd, "new "+ast.Unmangle(cd.Name)
		err := e.emitClassMember(llvmName, info.Ty, info.Constructor.Params, info.CtorSig, info.Constructor.Body, TypeVoid, info.Constructor.GetPos(), false, false)
		e.currentCtorClass = ""
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
			if _, ok := e.emitAdapterFunc("@"+llvmName, funcTypeFromSig(own), funcTypeFromSig(sig), callee); !ok {
				return fmt.Errorf("%d:%d: method '%s' on class '%s' overrides an inherited method with an incompatible signature", m.GetPos().Line, m.GetPos().Col, m.Name, cd.Name)
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
		slots[i] = "ptr null"
	}
	for _, mname := range info.MethodOrder {
		slot := info.MethodDispatchSlot[mname]
		if slot == nil || !slot.Virtual {
			continue
		}
		if m := info.Methods[mname]; m != nil && m.Body == nil {
			continue // an optional or abstract method no class in the chain implements
		}
		slots[slot.Index] = fmt.Sprintf("ptr @%s", llvmSafeSymbol(info.MethodImplementor[mname]+"_"+mname))
	}
	e.emitGlobal(fmt.Sprintf("@%s_vtable = global [%d x ptr] [%s]", className, info.VTableSize, strings.Join(slots, ", ")))
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
		e.setForwardClosures(body.Body)
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
		llvmParams = append(llvmParams, "ptr %p_this")
		thisPtr := "%v_this"
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", thisPtr))
		e.emitInstr(fmt.Sprintf("store ptr %%p_this, ptr %s, align 8", thisPtr))
		if declared, ok := e.declaredMethodThis(body, classTy); ok {
			// `m(this: Other)`: the receiver a call passes (`m.call(o)`) is
			// read as the declared class.
			classTy = declared
		}
		e.define("this", Symbol{Ptr: thisPtr, Ty: classTy})
		if body != nil && arrowsCaptureThis(body) {
			// An arrow reads `this`: box the receiver at entry (which
			// dominates the whole body), as a captured parameter is, not at
			// the first capture, which may sit in one branch.
			e.boxHoistedCapture("this", classTy, "%p_this", true, true)
		}
		if classInfo, ok := e.classes[classTy.ClassName]; ok && classInfo.BaseClass != "" {
			baseTy := e.classes[classInfo.BaseClass].Ty
			e.define("super", Symbol{Ptr: thisPtr, Ty: baseTy})
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
		// a constrained-union param. Only a nested dynamic shape stays rejected.
		if containsDynamicElement(pty) {
			return fmt.Errorf("%d:%d: any/unknown is not yet supported nested inside an array or object method parameter type", pos.Line, pos.Col)
		}
		if err := validateCompositeType(pty, pos.Line, pos.Col); err != nil {
			return err
		}
		if pty.IsArray() {
			llvmParams = append(llvmParams,
				fmt.Sprintf("ptr %%p_%s_ptr", p.Name),
				fmt.Sprintf("i64 %%p_%s_len", p.Name),
			)
			// Object-reference array param (TDD-00127): see bindArrayParam. Same
			// shape as a top-level function, for a class method/constructor.
			if p.ArrayPattern != nil {
				e.bindRawPatternParam(unpackLater, p, pty, "")
				if err := e.patternStep(unpackLater, func() error {
					dataPtrReg := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %%p_%s_ptr, align 8", dataPtrReg, p.Name))
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
			e.emitInstr(fmt.Sprintf("store %s %%p_%s, ptr %s, align %d", pty.IR, p.Name, ptrName, pty.Align()))
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
					e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", objPtrReg, patternParamIR(pty), ptrName))
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
		if err := e.emitStandardConstructorTail(e.currentCtorClass); err != nil {
			return err
		}
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

// emitThisExpression evaluates `this` — valid only inside a method or
// constructor body, where emitClassMember has already bound scope symbol
// "this" to the receiver. Same load-from-alloca shape any other
// object-typed identifier read uses.
func (e *Emitter) emitThisExpression(pos ast.Pos) (Value, error) {
	sym, ok := e.lookup("this")
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: 'this' is only valid inside a method or constructor body", pos.Line, pos.Col)
	}
	reg := e.freshReg()
	// A dynamic `this` (a prototype-class constructor/method under
	// `-compat=js`, TDD-00155 Stage 4) is a boxed { i8, i64 } slot, not a
	// class-instance pointer.
	if sym.Ty.IsDynamic() {
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", reg, sym.Ptr))
		return Value{Ref: reg, Ty: sym.Ty}, nil
	}
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", reg, sym.Ptr))
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
			return e.emitExpr(ast.NewMemberExpression(ast.NewThisExpression(pos), "constructor", pos))
		}
	case ast.NewTargetFunction:
		if fn := e.newTargetFunc(); fn != "" && ex.FnID != 0 && ex.FnID == e.newTargetDecl.NewTargetID {
			if _, ok := e.lookup("this"); !ok {
				break
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
	if o := e.reprOracle(); o != nil && strings.HasPrefix(sym.Ptr, "@") {
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
func (e *Emitter) emitNewExpression(ex *ast.NewExpression) (Value, error) {
	defer e.callSite(ex.GetPos())()
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
	if name := protoCtorCallee(ex); ex.ClassName == "" && e.jsProtoCtor[name] {
		// `new (F as any)(…)`: a constructor function.
		nx := *ex
		nx.ClassName, nx.Callee = name, nil
		return e.emitProtoCtorNew(&nx)
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
				return Value{}, fmt.Errorf("%d:%d: generic class '%s' requires exactly %d explicit type argument(s) (e.g. new %s<%s>(...)) — inference isn't supported for class construction", ex.GetPos().Line, ex.GetPos().Col, ast.Unmangle(ex.ClassName), len(genDecl.TypeParams), ast.Unmangle(ex.ClassName), strings.Join(genDecl.TypeParams, ", "))
			}
			subs := e.buildTypeArgSubs(genDecl.TypeParams, typeArgs)
			if err := e.checkTypeParamConstraints(genDecl.TypeParams, genDecl.TypeParamConstraints, subs, "class", genDecl.Name, ex.GetPos()); err != nil {
				return Value{}, err
			}
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
			e.emitTerminator("unreachable")
			return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil
		} else {
			return Value{}, fmt.Errorf("%d:%d: unknown class '%s'", ex.GetPos().Line, ex.GetPos().Col, ex.ClassName)
		}
	}
	if info.IsAbstract && !e.dynConstructing {
		return Value{}, fmt.Errorf("%d:%d: cannot create an instance of abstract class '%s'", ex.GetPos().Line, ex.GetPos().Col, ex.ClassName)
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
	dataReg := e.structAlloc(ex, info.Ty)

	tagGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", tagGep, info.Ty.StructIR(), dataReg))
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
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, info.Ty.StructIR(), dataReg, i))
			e.heapStore("i64", fmt.Sprint(undefF64), g, 8)
		}
	}

	if info.Ty.HasVTable {
		vtGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", vtGep, info.Ty.StructIR(), dataReg, info.Ty.VTableIndex()))
		e.heapStore("ptr", "@"+className+"_vtable", vtGep, 8)
	}
	if err := e.storeClassCaptures(ex, className, dataReg, info.Ty); err != nil {
		return Value{}, err
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
		if (info.HostBase.IsDate() || info.HostBase.IsArray()) && (info.Constructor == nil || info.ImplicitCtor) {
			// No constructor of its own: the arguments are Date's (Array's).
			// An implicit one (field initializers) then runs without them.
			var err error
			if info.HostBase.IsArray() {
				err = e.emitHostArrayBody(body, *info.HostBase, ex.Args, ex.GetPos())
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
				return Value{}, fmt.Errorf("%d:%d: %s takes at most one argument", ex.GetPos().Line, ex.GetPos().Col, ex.ClassName)
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
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, info.Ty.StructIR(), dataReg, idx))
				e.heapStore("ptr", ref, gep, 8)
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
			e.ensureFrames()
			e.emitInstr(fmt.Sprintf("call void @__kml_error_capture(ptr %s)", dataReg))
		}
		if info.Constructor == nil {
			if len(ex.Args) > 2 {
				return Value{}, fmt.Errorf("%d:%d: %s takes a message and one more argument", ex.GetPos().Line, ex.GetPos().Col, ex.ClassName)
			}
			if len(ex.Args) == 2 {
				slot := e.freshReg()
				e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dataReg, slot))
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
		if err := e.checkSpreadArgs(ex.Args, sig.HasRest, regularCount, ex.GetPos()); err != nil {
			return Value{}, err
		}
		minRequired := regularCount
		for minRequired > 0 && ((minRequired-1 < len(sig.Defaults) && sig.Defaults[minRequired-1] != nil) ||
			(minRequired-1 < len(sig.Optional) && sig.Optional[minRequired-1])) {
			minRequired--
		}
		var extraArgs []ast.Expression
		if e.compatJS() && !sig.HasRest && len(ex.Args) > len(sig.ParamTypes) {
			extraArgs = ex.Args[len(sig.ParamTypes):]
			trimmed := *ex // a copy: the AST node keeps every argument
			trimmed.Args = ex.Args[:len(sig.ParamTypes):len(sig.ParamTypes)]
			ex = &trimmed
		}
		if e.compatJS() {
			minRequired = 0 // untyped JavaScript may omit any argument
		}
		if len(ex.Args) < minRequired || !sig.HasRest && len(ex.Args) > len(sig.ParamTypes) {
			return Value{}, fmt.Errorf("%d:%d: %s constructor expects %d argument(s), got %d",
				ex.GetPos().Line, ex.GetPos().Col, ex.ClassName, len(sig.ParamTypes), len(ex.Args))
		}
		argParts := []string{"ptr " + dataReg}
		scratch := e.newParamDefaultScratch(sig.ParamNames)
		for i := 0; i < regularCount; i++ {
			paramTy := sig.ParamTypes[i]
			var a ast.Expression
			fromDefault := false
			switch {
			case i < len(ex.Args) && !undefinedFillsDefault(ex.Args[i], sig, i):
				a = ex.Args[i]
			case i < len(sig.Defaults) && sig.Defaults[i] != nil:
				a = sig.Defaults[i]
				fromDefault = true
			case i < len(sig.Optional) && sig.Optional[i] || e.compatJS():
				// ADR-00164: an omitted `param?: T` gets T's zero value.
				if paramTy.IsArray() {
					argParts = append(argParts, "ptr "+e.omittedArrayArgHeader(paramTy), "i64 0")
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
				return Value{}, fmt.Errorf("%d:%d: %s constructor missing argument %d with no default",
					ex.GetPos().Line, ex.GetPos().Col, ex.ClassName, i+1)
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
					val = Value{Ref: "null", Ty: paramTy}
				}
				if !val.Ty.IsArray() {
					return Value{}, fmt.Errorf("%d:%d: expression does not yield an array", a.GetPos().Line, a.GetPos().Col)
				}
				header, lenReg := e.packArrayArg(a, val, paramTy)
				argParts = append(argParts, "ptr "+header, "i64 "+lenReg)
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
				return Value{}, fmt.Errorf("%d:%d: constructor parameter %s of '%s' has no type annotation (defaults to number) but was called with a non-numeric argument here — add an explicit type annotation", a.GetPos().Line, a.GetPos().Col, paramName, ex.ClassName)
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
			if len(ex.Args) > regularCount {
				restArgs = ex.Args[regularCount:]
			}
			parts, err := e.packRestArgs(restArgs, sig.ParamTypes[len(sig.ParamTypes)-1])
			if err != nil {
				return Value{}, err
			}
			argParts = append(argParts, parts...)
		}
		e.noteArgc(e.methodsMayReadArgs() || countsArgs(sig.HasRest, sig.ParamNames), ex.Args, regularCount)
		e.emitInstr(fmt.Sprintf("call void @%s%s(%s)", className, ctorSymbolSuffix, strings.Join(argParts, ", ")))
	} else if len(ex.Args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: class '%s' has no constructor but was called with %d argument(s)",
			ex.GetPos().Line, ex.GetPos().Col, ex.ClassName, len(ex.Args))
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
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, info.Ty.StructIR(), thisVal.Ref, idx))
	fv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", fv, StructFieldIR(fty), gep))
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
	s.e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", val.Ty.IR, val.Ref, slot, val.Ty.Align()))
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
	s.e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	s.e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", header, slot))
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
	s.e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", nullableScalarStorageIR(pty), agg, slot, storageAlign(pty)))
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
		return Value{}, fmt.Errorf("%d:%d: unknown class '%s'", pos.Line, pos.Col, objTy.ClassName)
	}
	sig, ok := info.MethodSigs[methodName]
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
	// Spread argument (TDD-00106): same rule as the free-function/closure paths
	// — a spread may only fill the rest slot (after the fixed args), never a
	// fixed parameter or a rest-less method.
	if err := e.checkSpreadArgs(args, sig.HasRest, regularCount, pos); err != nil {
		return Value{}, err
	}
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
			return Value{}, fmt.Errorf("%d:%d: %s.%s expects at least %d argument(s), got %d",
				pos.Line, pos.Col, objTy.ClassName, methodName, minRequired, len(args))
		}
	} else if len(args) < minRequired || len(args) > len(sig.ParamTypes) {
		return Value{}, fmt.Errorf("%d:%d: %s.%s expects %d argument(s), got %d",
			pos.Line, pos.Col, objTy.ClassName, methodName, len(sig.ParamTypes), len(args))
	}

	argParts := []string{"ptr " + thisVal.Ref}
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
				argParts = append(argParts, "ptr "+e.omittedArrayArgHeader(paramTy), "i64 0")
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
			return Value{}, fmt.Errorf("%d:%d: %s.%s missing argument %d with no default",
				pos.Line, pos.Col, objTy.ClassName, methodName, i+1)
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
				val = Value{Ref: "null", Ty: paramTy}
			}
			if !val.Ty.IsArray() {
				return Value{}, fmt.Errorf("%d:%d: expression does not yield an array", a.GetPos().Line, a.GetPos().Col)
			}
			header, lenReg := e.packArrayArg(a, val, paramTy)
			argParts = append(argParts, "ptr "+header, "i64 "+lenReg)
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
			if paramTy.UnionMembers != nil && !unionAllowsAssignmentFrom(paramTy, val.Ty) {
				return Value{}, fmt.Errorf("%d:%d: argument's type is not a member of parameter %d's declared union type", a.GetPos().Line, a.GetPos().Col, i+1)
			}
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
	e.noteArgc(e.methodsMayReadArgs() || countsArgs(sig.HasRest, sig.ParamNames), args, regularCount)
	argsIR := strings.Join(argParts, ", ")

	slot := info.MethodDispatchSlot[methodName]
	if forceDirect || slot == nil || !slot.Virtual {
		if m := info.Methods[methodName]; m != nil && m.IsOptional && m.Body == nil {
			// An optional method no class in the chain implements: the
			// member is undefined, and calling it is a TypeError.
			e.emitThrowTypeError(methodName + " is not a function")
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

	paramTyStrs := []string{"ptr"}
	for _, p := range sig.ParamTypes {
		if p.IsArray() {
			paramTyStrs = append(paramTyStrs, "ptr", "i64")
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
	e.emitInstr(fmt.Sprintf("store i1 true, ptr %s, align 1", ownSlot))
	hdrL, testL := e.freshLabel("clsfld.hdr"), e.freshLabel("clsfld.test")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(thisVal.Ref), testL, hdrL))
	e.emitLabel(hdrL)
	h := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", h, thisVal.Ref))
	h = e.headerIdentity(h)
	acc := "false"
	for _, t := range tags {
		c, o := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", c, h, t))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", o, acc, c))
		acc = o
	}
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", acc, ownSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", testL))
	e.emitLabel(testL)
	own := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", own, ownSlot))
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
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", valIR, ov.Ref, slot))
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
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", valIR, cv.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", r, valIR, slot))
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
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", valIR, ov.Ref, slot))
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
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", valIR, cv.Ref, slot))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	if slot == "" {
		return Value{Ty: resTy}, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", r, valIR, slot))
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
	e.emitInstr(fmt.Sprintf("store i1 true, ptr %s, align 1", ownSlot))
	hdrL, testL := e.freshLabel("clsfor.hdr"), e.freshLabel("clsfor.test")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(thisVal.Ref), testL, hdrL))
	e.emitLabel(hdrL)
	h := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", h, thisVal.Ref))
	h = e.headerIdentity(h)
	acc := "false"
	for _, t := range tags {
		c, o := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", c, h, t))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", o, acc, c))
		acc = o
	}
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", acc, ownSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", testL))
	e.emitLabel(testL)
	own := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", own, ownSlot))
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
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", valIR, sv.Ref, slot))
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
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", valIR, cv.Ref, slot))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	if slot == "" {
		return Value{Ty: retTy}, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", r, valIR, slot))
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
		e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", fv, flag))
		set := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i8 %s, 0", set, fv))
		checkL := e.freshLabel("ownmeth.check")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", set, checkL, classL))
		e.emitLabel(checkL)
	}
	has := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_shape_expando_has(ptr %s, ptr %s)", has, thisVal.Ref, e.internString(methodName)))
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
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", valIR, cv.Ref, slot))
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
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", valIR, sv.Ref, slot))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	if slot == "" {
		return Value{Ty: TypeVoid}, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", r, valIR, slot))
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
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", genElemValIR(v.Ty), v.Ref, slot))
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
		e.emitGlobal(fmt.Sprintf("%s = global i8 %d", g, v))
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
		return Value{}, fmt.Errorf("%d:%d: unknown class '%s'", pos.Line, pos.Col, objTy.ClassName)
	}
	sig, ok := info.MethodSigs[methodName]
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: class '%s' has no method '%s'", pos.Line, pos.Col, objTy.ClassName, methodName)
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
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxedArg.Ref, argv))
		fnBox := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", fnBox, slot))
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
			return Value{}, fmt.Errorf("%d:%d: expression does not yield an array", pos.Line, pos.Col)
		}
		ptrReg := e.freshReg()
		lenReg := e.freshReg()
		lenReg = e.arrayLen(argVal)
		_ = ptrReg
		header := e.boxArrayValue(argVal)
		argParts = []string{"ptr " + thisVal.Ref, "ptr " + header, "i64 " + lenReg}
		fnTypePart = "(ptr, ptr, i64)"
	} else if isNullableScalar(paramTy) {
		// Nullable-scalar setter parameter (TDD-00064 Stage 3): its boxed
		// { i1, T } aggregate, with the matching storage type in the fn type.
		agg := e.boxNullableScalarFromValue(argVal, paramTy)
		st := nullableScalarStorageIR(paramTy)
		argParts = []string{"ptr " + thisVal.Ref, fmt.Sprintf("%s %s", st, agg)}
		fnTypePart = fmt.Sprintf("(ptr, %s)", st)
	} else {
		v := e.convert(argVal, paramTy)
		if v.Ty.IR != paramTy.IR {
			return Value{}, fmt.Errorf("%d:%d: type mismatch in assignment — a value of one type cannot be used where an incompatible type is expected", pos.Line, pos.Col)
		}
		argParts = []string{"ptr " + thisVal.Ref, fmt.Sprintf("%s %s", v.Ty.IR, v.Ref)}
		fnTypePart = fmt.Sprintf("(ptr, %s)", paramTy.IR)
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
		e.declareFn("__kml_classref_tostring", "declare ptr @__kml_classref_tostring(ptr)")
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_classref_tostring(ptr %s)", r, ref))
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
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: class '%s' has no static method '%s'", pos.Line, pos.Col, className, methodName)
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
	// Spread argument (TDD-00106): same rule as every other call path.
	if err := e.checkSpreadArgs(args, sig.HasRest, regularCount, pos); err != nil {
		return Value{}, err
	}
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
			return Value{}, fmt.Errorf("%d:%d: %s.%s expects at least %d argument(s), got %d",
				pos.Line, pos.Col, className, methodName, minRequired, len(args))
		}
	} else if len(args) < minRequired || len(args) > len(sig.ParamTypes) {
		return Value{}, fmt.Errorf("%d:%d: %s.%s expects %d argument(s), got %d",
			pos.Line, pos.Col, className, methodName, len(sig.ParamTypes), len(args))
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
				argParts = append(argParts, "ptr "+e.omittedArrayArgHeader(paramTy), "i64 0")
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
			return Value{}, fmt.Errorf("%d:%d: %s.%s missing argument %d with no default",
				pos.Line, pos.Col, className, methodName, i+1)
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
				val = Value{Ref: "null", Ty: paramTy}
			}
			if !val.Ty.IsArray() {
				return Value{}, fmt.Errorf("%d:%d: expression does not yield an array", a.GetPos().Line, a.GetPos().Col)
			}
			header, lenReg := e.packArrayArg(a, val, paramTy)
			argParts = append(argParts, "ptr "+header, "i64 "+lenReg)
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
			if paramTy.UnionMembers != nil && !unionAllowsAssignmentFrom(paramTy, val.Ty) {
				return Value{}, fmt.Errorf("%d:%d: argument's type is not a member of parameter %d's declared union type", a.GetPos().Line, a.GetPos().Col, i+1)
			}
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
		if spread, ok := singleSpread(restArgs); ok && sameRestElem(e.inferExprType(spread.Arg), elemTy) {
			ptrReg, lenReg, _, err := e.resolveArrayForHOF(spread.Arg, spread.Arg.GetPos())
			if err != nil {
				return Value{}, err
			}
			restHdr := e.newArrayHeader(ptrReg, lenReg)
			argParts = append(argParts, "ptr "+restHdr, "i64 "+lenReg)
		} else if len(restArgs) == 0 {
			argParts = append(argParts, "ptr "+e.emptyArrayArgHeader(), "i64 0")
		} else if anySpread(restArgs) {
			dataReg, lenReg, err := e.emitRestArgBuffer(restArgs, elemTy)
			if err != nil {
				return Value{}, err
			}
			restHdr := e.newArrayHeader(dataReg, lenReg)
			argParts = append(argParts, "ptr "+restHdr, "i64 "+lenReg)
		} else {
			n := int64(len(restArgs))
			e.ensureMalloc()
			dataReg := e.freshReg()
			e.heapMallocInto(dataReg, fmt.Sprint(n*int64(elemTy.Align())))
			for i, arg := range restArgs {
				val, err := e.emitExprWithObjectHint(arg, elemTy)
				if err != nil {
					return Value{}, err
				}
				val = e.convert(val, elemTy)
				gepReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gepReg, elemTy.IR, dataReg, i))
				e.storeArrayElem(gepReg, elemTy, val)
			}
			restHdr := e.newArrayHeader(dataReg, fmt.Sprintf("%d", n))
			argParts = append(argParts, "ptr "+restHdr, fmt.Sprintf("i64 %d", n))
		}
	}
	e.noteArgc(e.methodsMayReadArgs() || countsArgs(sig.HasRest, sig.ParamNames), args, regularCount)
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
func (e *Emitter) emitSuperCall(ex *ast.CallExpression) (Value, error) {
	thisSym, ok := e.lookup("this")
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: super(...) is only valid inside a constructor", ex.GetPos().Line, ex.GetPos().Col)
	}
	info, ok := e.classes[thisSym.Ty.ClassName]
	if !ok || info.BaseClass == "" {
		return Value{}, fmt.Errorf("%d:%d: super(...) is only valid inside the constructor of a class with a base class", ex.GetPos().Line, ex.GetPos().Col)
	}
	// An error subclass's `super(message?)` (TDD-00155 Stage 6) stores the
	// message into the error-struct prefix; kind/name were already set at
	// construction. Error's synthetic root has no real constructor; a
	// subclass of an Error subclass calls its base's, as any class does.
	if info.IsErrorSubclass && e.isErrorRootKind(info.BaseClass) {
		if len(ex.Args) > 2 {
			return Value{}, fmt.Errorf("%d:%d: super(...) on %s takes a message and one more argument", ex.GetPos().Line, ex.GetPos().Col, info.ErrorBaseKind)
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
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", thisReg, thisSym.Ptr))
			idx, _, okF := info.Ty.FieldIndex("message")
			if !okF {
				return Value{}, fmt.Errorf("%d:%d: internal: error subclass without a message field", ex.GetPos().Line, ex.GetPos().Col)
			}
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, info.Ty.StructIR(), thisReg, idx))
			e.heapStore("ptr", msgStr.Ref, gep, 8)
		}
		return Value{Ty: TypeVoid}, nil
	}
	if _, registered := e.classes[info.BaseClass]; !registered && info.HostBase != nil && info.HostBase.IsPromise() {
		// `super(executor)` of a class extending Promise runs it on its
		// body.
		thisReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", thisReg, thisSym.Ptr))
		body := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", body, thisReg))
		if err := e.emitHostPromiseBody(body, *info.HostBase, ex.Args, ex.GetPos()); err != nil {
			return Value{}, err
		}
		return Value{Ty: TypeVoid}, nil
	}
	if _, registered := e.classes[info.BaseClass]; !registered && info.HostBase != nil && info.HostBase.IsArray() {
		// `super(…)` of a class extending Array sets its elements, as
		// `new Array(…)` would.
		thisReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", thisReg, thisSym.Ptr))
		body := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", body, thisReg))
		if err := e.emitHostArrayBody(body, *info.HostBase, ex.Args, ex.GetPos()); err != nil {
			return Value{}, err
		}
		return Value{Ty: TypeVoid}, nil
	}
	if _, registered := e.classes[info.BaseClass]; !registered && info.HostBase != nil && info.HostBase.IsDate() {
		// `super(…)` of a class extending Date sets its time value, as
		// `new Date(…)` would.
		thisReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", thisReg, thisSym.Ptr))
		body := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", body, thisReg))
		if err := e.emitHostDateBody(body, ex.Args, ex.GetPos()); err != nil {
			return Value{}, err
		}
		return Value{Ty: TypeVoid}, nil
	}
	if _, registered := e.classes[info.BaseClass]; !registered && info.HostBase != nil && info.HostBase.IsRegExp() {
		// `super(pattern?, flags?)` of a class extending RegExp compiles
		// its body.
		thisReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", thisReg, thisSym.Ptr))
		body := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", body, thisReg))
		if err := e.emitHostRegExpBody(body, ex.Args, ex.GetPos()); err != nil {
			return Value{}, err
		}
		return Value{Ty: TypeVoid}, nil
	}
	if _, registered := e.classes[info.BaseClass]; !registered && info.HostBase != nil {
		// `super(entries?)` of a class extending Map or Set: the body was
		// made empty at construction; entries seed it.
		if len(ex.Args) > 1 {
			return Value{}, fmt.Errorf("%d:%d: super(...) on %s takes at most one argument", ex.GetPos().Line, ex.GetPos().Col, info.BaseClass)
		}
		if len(ex.Args) == 1 && !isUndefinedLiteral(e, ex.Args[0]) {
			thisReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", thisReg, thisSym.Ptr))
			body := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", body, thisReg))
			if err := e.emitHostBodySeed(body, *info.HostBase, ex.Args[0], ex.GetPos()); err != nil {
				return Value{}, err
			}
		}
		return Value{Ty: TypeVoid}, nil
	}
	baseInfo := e.classes[info.BaseClass]
	if baseInfo.Constructor == nil {
		if len(ex.Args) > 0 {
			return Value{}, fmt.Errorf("%d:%d: base class '%s' has no constructor to pass super(...) arguments to", ex.GetPos().Line, ex.GetPos().Col, info.BaseClass)
		}
		return Value{Ty: TypeVoid}, nil
	}
	sig := baseInfo.CtorSig
	params := sig.ParamTypes
	if sig.HasRest {
		params = params[:len(params)-1] // the rest slot takes the remaining arguments
	}
	if err := e.checkSpreadArgs(ex.Args, sig.HasRest, len(params), ex.GetPos()); err != nil {
		return Value{}, err
	}
	if !sig.HasRest && len(ex.Args) > len(sig.ParamTypes) {
		return Value{}, fmt.Errorf("%d:%d: super(...) expects %d argument(s), got %d",
			ex.GetPos().Line, ex.GetPos().Col, len(sig.ParamTypes), len(ex.Args))
	}
	for i := len(ex.Args); i < len(params); i++ {
		if !(i < len(sig.Optional) && sig.Optional[i]) {
			return Value{}, fmt.Errorf("%d:%d: super(...) expects %d argument(s), got %d",
				ex.GetPos().Line, ex.GetPos().Col, len(sig.ParamTypes), len(ex.Args))
		}
	}

	thisReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", thisReg, thisSym.Ptr))
	argParts := []string{"ptr " + thisReg}
	for i, paramTy := range params {
		if i >= len(ex.Args) {
			// An omitted optional parameter is undefined (its type's zero,
			// or an absent aggregate), as `new` fills one.
			switch {
			case paramTy.IsArray():
				argParts = append(argParts, "ptr "+e.omittedArrayArgHeader(paramTy), "i64 0")
			case isNullableScalar(paramTy):
				argParts = append(argParts, nullableScalarStorageIR(paramTy)+" zeroinitializer")
			default:
				argParts = append(argParts, fmt.Sprintf("%s %s", paramTy.IR, paramTy.zeroLiteral()))
			}
			continue
		}
		a := ex.Args[i]
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
			argParts = append(argParts, "ptr "+header, "i64 "+lenReg)
			continue
		}
		val = e.convert(val, paramTy)
		if err := argReprMismatch(val, paramTy, a, i); err != nil {
			return Value{}, err
		}
		argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
	}
	if sig.HasRest {
		var restArgs []ast.Expression
		if len(ex.Args) > len(params) {
			restArgs = ex.Args[len(params):]
		}
		parts, err := e.packRestArgs(restArgs, sig.ParamTypes[len(sig.ParamTypes)-1])
		if err != nil {
			return Value{}, err
		}
		argParts = append(argParts, parts...)
	}
	e.noteArgc(e.methodsMayReadArgs() || countsArgs(sig.HasRest, sig.ParamNames), ex.Args, len(params))
	e.emitInstr(fmt.Sprintf("call void @%s%s(%s)", info.BaseClass, ctorSymbolSuffix, strings.Join(argParts, ", ")))
	return Value{Ty: TypeVoid}, nil
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
		return Value{}, fmt.Errorf("%d:%d: super.%s(...) is only valid inside a method of a class with a base class", pos.Line, pos.Col, methodName)
	}
	thisReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", thisReg, superSym.Ptr))
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
	"Array":           func(t Type) bool { return t.IsArray() },
	"Map":             func(t Type) bool { return t.IsMap() },
	"Set":             func(t Type) bool { return t.IsSet() },
	"Date":            func(t Type) bool { return t.IsDate() },
	"RegExp":          func(t Type) bool { return t.IsRegExp() },
	"ReadableStream":  func(t Type) bool { return t.IsReadableStream() },
	"WritableStream":  func(t Type) bool { return t.IsWritableStream() },
	"TransformStream": func(t Type) bool { return t.IsTransformStream() },
	"Promise":         func(t Type) bool { return t.IsPromise() },
	"Buffer":          func(t Type) bool { return t.IsBuffer() },
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
	// A constructor held in a value (`x instanceof C`, C a parameter):
	// decided at run time.
	if e.instanceofNeedsRuntime(ex.Right) {
		return e.emitDynInstanceOf(ex)
	}
	rightIdent, ok := ex.Right.(*ast.Identifier)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: right-hand side of instanceof must be a class name", ex.GetPos().Line, ex.GetPos().Col)
	}
	// A caught value (TypeCaught ≈ `unknown`, TDD-00202): resolve instanceof off
	// the record's tag. A built-in Error kind checks the caught Error's kind;
	// `Object` is true for any non-primitive; any other class is not verifiable
	// from the packed record (a thrown user-class instance loses its identity).
	if e.inferExprType(ex.Left).IsCaught() {
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
				e.ensureDynObj()
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call zeroext i1 @__kml_instanceof_object(i64 %s)", r, b.Ref))
				return Value{Ref: r, Ty: TypeBool}, nil
			}
			// A Date is an object (a `ptr` handle, which isForOfStringTy
			// already excludes; listed for clarity).
			if t.IsDate() {
				return Value{Ref: "1", Ty: TypeBool}, nil
			}
			isPrimitive := (t.IR != "ptr" && t.IR != "void") || t.IsBigInt() || t.IsSymbol() ||
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
				return Value{Ref: "1", Ty: TypeBool}, nil
			}
			return Value{Ref: "0", Ty: TypeBool}, nil
		}
		// A headerless host object (URL) in a union: the union's one object
		// without a header word.
		if lt := e.inferExprType(ex.Left); lt.IsDynamic() && len(lt.UnionMembers) > 0 && builtinInstanceofTypes[rightIdent.Name] == nil {
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
				return Value{Ref: "1", Ty: TypeBool}, nil
			}
			if leftVal.Ty.IsClass() {
				// A class extending Map or Set is one (TDD-00247).
				if li, ok := e.classes[leftVal.Ty.ClassName]; ok && li.HostBase != nil && matches(*li.HostBase) {
					return Value{Ref: "1", Ty: TypeBool}, nil
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
		return Value{}, fmt.Errorf("%d:%d: instanceof is only supported against user-defined classes; '%s' is not a registered class", ex.GetPos().Line, ex.GetPos().Col, rightIdent.Name)
	}

	leftVal, err := e.emitExpr(ex.Left)
	if err != nil {
		return Value{}, err
	}

	// A Map or Set against a class extending one (TDD-00247): the header
	// word before its body is the instance's class tag when it is one.
	if hostBodyAt8(leftVal.Ty) && info.HostBase != nil {
		hp, hdr := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 -8", hp, leftVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", hdr, hp))
		return Value{Ref: e.emitClassIs(hdr, info.TagID), Ty: TypeBool}, nil
	}
	// An array against a class extending Array: the header word before
	// its header is the instance's class tag when it is one's body (a
	// species result). A fresh array never is.
	if leftVal.Ty.IsArray() && !leftVal.Ty.IsBuffer() && info.HostBase != nil && info.HostBase.IsArray() {
		hp, hdr := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 -8", hp, e.nullSafeArrayHeader(leftVal.Ref)))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", hdr, hp))
		return Value{Ref: e.emitClassIs(hdr, info.TagID), Ty: TypeBool}, nil
	}
	// A caught error (errorObjType) against an error-subclass class
	// (TDD-00155 Stage 6): runtime kind-slot compare — the subclass TagID
	// lives in the error struct's kind position.
	if leftVal.Ty.IsError() && info.IsErrorSubclass {
		kindGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", kindGep, errorObjType.StructIR(), leftVal.Ref))
		loadedKind := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", loadedKind, kindGep))
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", r, e.headerIdentity(loadedKind), errorTypeIDStored(info.TagID)))
		return Value{Ref: r, Ty: TypeBool}, nil
	}

	if leftVal.Ty.IsDynamic() {
		tag, payload := e.emitUnboxTagPayload(leftVal)
		isObj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
		isBag := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isBag, tag, kmlTagDynObject))

		ptrReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", ptrReg, payload))

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
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", tagGep, info.Ty.StructIR(), ptrReg))
		loadedTagObj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", loadedTagObj, tagGep))
		e.emitTerminator(fmt.Sprintf("br label %%%s", cmpL))

		// A widened bag (tag kmlTagDynObject) realized from a class instance carries
		// its class TagID at header offset 40 (0 = not from a class → no match).
		e.emitLabel(bagChkL)
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBag, bagL, notObjL))
		e.emitLabel(bagL)
		e.ensureDynObj()
		loadedTagBag, classTag := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_classtag(ptr %s)", classTag, ptrReg))
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
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", protoObj, protoSlot))
			e.declareFn("__kml_dynobj_chain_has", "declare i64 @__kml_dynobj_chain_has(ptr, ptr)")
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_chain_has(ptr %s, ptr %s)", has, ptrReg, protoObj))
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
		e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", classMatch, resultAlloca))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(notObjL)
		e.emitInstr(fmt.Sprintf("store i1 0, ptr %s, align 1", resultAlloca))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(mergeL)
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", result, resultAlloca))
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
			if leftVal.Ty.IR == "ptr" {
				result := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", result, leftVal.Ref))
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
				e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, leftVal.Ref))
				e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, nullL, notNullL))
				e.emitLabel(nullL)
				e.emitInstr(fmt.Sprintf("store i1 0, ptr %s, align 1", resultAlloca))
				e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
				e.emitLabel(notNullL)
			}

			tagGep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", tagGep, leftInfo.Ty.StructIR(), leftVal.Ref))
			loadedTag := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", loadedTag, tagGep))
			match := e.emitClassIs(loadedTag, target)

			if !leftVal.Ty.Nullable() {
				return Value{Ref: match, Ty: TypeBool}, nil
			}
			e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", match, resultAlloca))
			e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
			e.emitLabel(mergeL)
			result := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", result, resultAlloca))
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
// No resolveType path ever produces a statically Error-typed, Nullable
// value ("Error" isn't a resolvable type-annotation name — see
// emit_exprs_types.go/types.go's resolveType/ResolveTypeName), so unlike
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
			e.emitInstr(fmt.Sprintf("store i1 0, ptr %s, align 1", baseAlloca))
			baseObjL := e.freshLabel("errinstanceof.baseobj")
			baseMergeL := e.freshLabel("errinstanceof.basemerge")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, baseObjL, baseMergeL))
			e.emitLabel(baseObjL)
			basePtr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", basePtr, payload))
			baseF0Gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", baseF0Gep, errorObjType.StructIR(), basePtr))
			baseF0 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", baseF0, baseF0Gep))
			baseMasked := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", baseMasked, baseF0, errorTypeIDFlag))
			baseIsErr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", baseIsErr, baseMasked))
			e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", baseIsErr, baseAlloca))
			e.emitTerminator(fmt.Sprintf("br label %%%s", baseMergeL))
			e.emitLabel(baseMergeL)
			baseRes := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", baseRes, baseAlloca))
			return Value{Ref: baseRes, Ty: TypeBool}, nil
		}

		resultAlloca := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resultAlloca))
		objL := e.freshLabel("errinstanceof.obj")
		notObjL := e.freshLabel("errinstanceof.notobj")
		mergeL := e.freshLabel("errinstanceof.merge")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, notObjL))

		e.emitLabel(objL)
		ptrReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", ptrReg, payload))
		kindGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", kindGep, errorObjType.StructIR(), ptrReg))
		loadedKind := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", loadedKind, kindGep))
		kindMatch := e.errorKindMatch(loadedKind, kindName, kindID)
		e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", kindMatch, resultAlloca))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(notObjL)
		e.emitInstr(fmt.Sprintf("store i1 0, ptr %s, align 1", resultAlloca))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(mergeL)
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", result, resultAlloca))
		return Value{Ref: result, Ty: TypeBool}, nil
	}

	// An error-subclass instance (TDD-00155 Stage 6): `instanceof Error` is
	// true by construction; a specific builtin kind never matches (the
	// subclass TagID range is disjoint from the builtin kinds).
	if leftVal.Ty.IsClass() {
		if ci, ok := e.classes[leftVal.Ty.ClassName]; ok && ci.IsErrorSubclass {
			if kindName == "Error" || kindName == ci.ErrorBaseKind {
				return Value{Ref: "1", Ty: TypeBool}, nil
			}
			return Value{Ref: "0", Ty: TypeBool}, nil
		}
	}
	if leftVal.Ty.IsError() {
		if kindName == "Error" {
			return Value{Ref: "1", Ty: TypeBool}, nil
		}
		kindGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", kindGep, errorObjType.StructIR(), leftVal.Ref))
		loadedKind := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", loadedKind, kindGep))
		return Value{Ref: e.errorKindMatch(loadedKind, kindName, kindID), Ty: TypeBool}, nil
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
	return declaredLowerType(e.lookupBuiltinType(base)) == "error"
}

func errorBaseKind(isErrorRoot bool, base string) string {
	if !isErrorRoot {
		return ""
	}
	return base
}

// inheritedFieldIs reports whether the inherited field name is stored as ty.
func inheritedFieldIs(fields []Field, name string, ty Type) bool {
	for _, f := range fields {
		if f.Name == name {
			if f.Ty.IR == "ptr" && f.Ty.Nullable() && !ty.Nullable() {
				// A narrowing of an optional reference (`code: string` over
				// Error's `code?: string`): the same storage.
				ty.setNullable(f.Ty.Nullable())
				ty.setUndefined(f.Ty.IsUndefined())
			}
			return reflect.DeepEqual(f.Ty, ty)
		}
	}
	return false
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
		return e.resolveType(fd.ReturnType), true
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
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", isDyn, resPtr))
	typedL, otherL, mergeL := e.freshLabel("isarray.typed"), e.freshLabel("isarray.other"), e.freshLabel("isarray.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isArr, typedL, otherL))
	// A boxed TypedArray is kmlTagArray too, but no Array: the box's typed
	// byte tells (ADR-01059). Only dereferenced when the tag says the
	// payload is a box.
	e.emitLabel(typedL)
	box := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", box, payload))
	typedGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", typedGep, anyArrayBoxTy, box))
	typedB := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", typedB, typedGep))
	plain := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", plain, typedB, anyArrayPlain))
	// An Arguments object (its header's word) is no Array.
	hdrp, hdr := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", hdrp, anyArrayBoxTy, box))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hdr, hdrp))
	notArgs, isArrPlain := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notArgs, e.emitArrayIsArguments(hdr)))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", isArrPlain, plain, notArgs))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", isArrPlain, resPtr))
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
		e.declareFn("__kml_dynobj_is_array_exotic", "declare i1 @__kml_dynobj_is_array_exotic(ptr)")
		bag, ex := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", bag, payload))
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynobj_is_array_exotic(ptr %s)", ex, bag))
		e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", ex, resPtr))
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
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", obj, payload))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", word, obj))
		// Each Array host has its own id (its element layout); every one is
		// a child of the any[] one (emitClassParentRows).
		e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", e.emitClassIs(word, kmlHdrMagic|e.hostID(ArrayOf(TypeAny))&kmlHdrIDMask), resPtr))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(mergeL)
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", res, resPtr))
	return Value{Ref: res, Ty: TypeBool}
}

// emitDynBuiltinInstanceOf answers `v instanceof Array | Promise` for a value
// held in `any` at run time: an array (static or dynamic) by its box tag, a
// promise by its box wrapper's header. ok is false for the other builtins.
func (e *Emitter) emitDynBuiltinInstanceOf(v Value, name string) (Value, bool) {
	tag, pay := e.emitUnboxTagPayload(v)
	switch name {
	case "Array":
		return e.emitDynIsArray(v), true
	case "Promise":
		isObj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
		out := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", out))
		e.emitInstr(fmt.Sprintf("store i1 0, ptr %s, align 1", out))
		chkL, doneL := e.freshLabel("inst.prom"), e.freshLabel("inst.prom.done")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, chkL, doneL))
		e.emitLabel(chkL)
		obj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", obj, pay))
		hdr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", hdr, obj))
		is := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", is, e.headerIdentity(hdr), e.objHeaderWord(promiseBoxType())))
		e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", is, out))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", r, out))
		return Value{Ref: r, Ty: TypeBool}, true
	}
	if typedArrayNames[name] || name == "Buffer" {
		// A boxed static array whose box says it is that TypedArray.
		isArr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isArr, tag, kmlTagArray))
		out := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", out))
		e.emitInstr(fmt.Sprintf("store i1 0, ptr %s, align 1", out))
		chkL, doneL := e.freshLabel("inst.typed"), e.freshLabel("inst.typed.done")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isArr, chkL, doneL))
		e.emitLabel(chkL)
		e.ensureDynJSONC()
		box := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", box, pay))
		r32 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_anyarr_typed_is(ptr %s, ptr %s)", r32, box, e.internString(name)))
		is := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", is, r32))
		e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", is, out))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", r, out))
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
	e.emitInstr(fmt.Sprintf("store i1 false, ptr %s, align 1", slot))
	isObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
	objL, doneL := e.freshLabel("hless.obj"), e.freshLabel("hless.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, doneL))
	e.emitLabel(objL)
	p := e.emitIntToPtr(pay)
	w, mk, isHdr, r := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", w, p))
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", mk, w, kmlHdrMagicMask|hostTypeIDFlag))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isHdr, mk, kmlHdrMagic))
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", r, isHdr))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", r, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", out, slot))
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
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", thisReg, thisSym.Ptr))
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, info.Ty.StructIR(), thisReg, idx))
	e.heapStore("ptr", name, gep, 8)
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
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
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
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", thisReg, thisSym.Ptr))
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, info.Ty.StructIR(), thisReg, idx))
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

// redeclaresSameStorage reports a field redeclared over an inherited one of
// the same storage: by its annotation, or (`code = 'E_X'`) by its
// initializer's type as TypeScript widens it.
func (e *Emitter) redeclaresSameStorage(inherited []Field, f ast.AnnotField) bool {
	if f.Optional {
		return false
	}
	// A base field nothing assigned yet holds T as `T | undefined`
	// (markUnassignedField): the same storage as T's.
	is := func(t Type) bool {
		return inheritedFieldIs(inherited, f.Name, t) || inheritedFieldIs(inherited, f.Name, optionalFieldType(t))
	}
	if f.Type != nil {
		return is(e.resolveType(f.Type))
	}
	if f.Initializer != nil {
		it := e.inferExprType(f.Initializer)
		it.setKind(kindStrLiteral, false)
		return is(it)
	}
	return false
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
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", out))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", direct, out))
	hp, hdr := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i32 0", hp, thisRef))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", hdr, hp))
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
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", vtGep, info.Ty.StructIR(), thisRef, info.Ty.VTableIndex()))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", vtPtr, vtGep))
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i32 %d", slotGep, vtPtr, slot))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, slotGep))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", fp, out))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, out))
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
	if spread, ok := singleSpread(restArgs); ok && sameRestElem(e.inferExprType(spread.Arg), elemTy) {
		// obj.m(...arr): forward the array's own (ptr, len) into the rest
		// slot, the same fast path the free-function call uses (TDD-00106).
		ptrReg, lenReg, _, err := e.resolveArrayForHOF(spread.Arg, spread.Arg.GetPos())
		if err != nil {
			return nil, err
		}
		restHdr := e.newArrayHeader(ptrReg, lenReg)
		out = append(out, "ptr "+restHdr, "i64 "+lenReg)
	} else if len(restArgs) == 0 {
		out = append(out, "ptr "+e.emptyArrayArgHeader(), "i64 0")
	} else if anySpread(restArgs) {
		dataReg, lenReg, err := e.emitRestArgBuffer(restArgs, elemTy)
		if err != nil {
			return nil, err
		}
		restHdr := e.newArrayHeader(dataReg, lenReg)
		out = append(out, "ptr "+restHdr, "i64 "+lenReg)
	} else {
		n := int64(len(restArgs))
		e.ensureMalloc()
		dataReg := e.freshReg()
		e.heapMallocInto(dataReg, fmt.Sprint(n*int64(elemTy.Align())))
		for i, arg := range restArgs {
			val, err := e.emitExprWithObjectHint(arg, elemTy)
			if err != nil {
				return nil, err
			}
			val = e.convert(val, elemTy)
			gepReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gepReg, elemTy.IR, dataReg, i))
			e.storeArrayElem(gepReg, elemTy, val)
		}
		restHdr := e.newArrayHeader(dataReg, fmt.Sprintf("%d", n))
		out = append(out, "ptr "+restHdr, fmt.Sprintf("i64 %d", n))
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
func (e *Emitter) newTypeArgs(decl *ast.ClassDeclaration, ne *ast.NewExpression) ([]*ast.TypeAnnotation, bool) {
	if targs, ok := classTypeArgs(decl, ne.TypeArgs); ok || len(ne.TypeArgs) > 0 {
		return targs, ok
	}
	if annots := e.inferredClassTypeArgs(ne, decl); annots != nil {
		return classTypeArgs(decl, annots)
	}
	return nil, false
}

// inferredClassTypeArgs is the type arguments of a generic class's `new`
// written without them, as annotations: each type parameter a constructor
// parameter is annotated with outright takes its argument's type, as the
// checker gives it. nil when one is left unanswered.
func (e *Emitter) inferredClassTypeArgs(ex *ast.NewExpression, decl *ast.ClassDeclaration) []*ast.TypeAnnotation {
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
	if t := c.TypeOf(ex); t != nil && !c.Unanswered(t) && t.Kind == checker.Instance && len(t.TypeArgs) == len(decl.TypeParams) {
		out := make([]*ast.TypeAnnotation, len(t.TypeArgs))
		for i, a := range t.TypeArgs {
			if a == nil || c.Unanswered(a) {
				out = nil
				break
			}
			out[i] = checkerTypeAnnotation(a)
		}
		if out != nil {
			if e.newClassArgs == nil {
				e.newClassArgs = map[string][]*ast.TypeAnnotation{}
			}
			e.newClassArgs[key] = out
			return out
		}
	}
	if decl.Constructor == nil {
		return nil
	}
	found := map[string]*ast.TypeAnnotation{}
	for i, p := range decl.Constructor.Params {
		if i >= len(ex.Args) || p.Type == nil || p.Rest {
			continue
		}
		for _, tp := range decl.TypeParams {
			if p.Type.Name == tp && found[tp] == nil {
				if t := c.TypeOf(ex.Args[i]); t != nil && !c.Unanswered(t) {
					found[tp] = checkerTypeAnnotation(t)
				}
			}
		}
	}
	out := make([]*ast.TypeAnnotation, len(decl.TypeParams))
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
func checkerTypeAnnotation(t *checker.Type) *ast.TypeAnnotation {
	switch {
	case t.Flags&(checker.Number|checker.NumberLiteral) != 0 && !t.AnyArith:
		return &ast.TypeAnnotation{Name: "number"}
	case t.Flags&(checker.String|checker.StringLiteral) != 0:
		return &ast.TypeAnnotation{Name: "string"}
	case t.Flags&(checker.Boolean|checker.BooleanLiteral) != 0:
		return &ast.TypeAnnotation{Name: "boolean"}
	case t.Flags&checker.Object != 0 && t.Kind == checker.Array:
		el := checkerTypeAnnotation(t.Elem)
		return &ast.TypeAnnotation{Name: "Array", ElemType: el}
	case t.Flags&checker.Object != 0 && (t.Kind == checker.Instance || t.Kind == checker.Interface) && t.Symbol != nil && len(t.TypeArgs) == 0:
		return &ast.TypeAnnotation{Name: t.Symbol.Name}
	}
	return &ast.TypeAnnotation{Name: "any"}
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
			e.forwardMethodRet[forwardMethodKey(cd.Name, m.Name, m.IsStatic)] = e.resolveType(m.ReturnType)
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
			e.define("this", Symbol{Ty: thisTy})
			if cd.BaseClass != "" {
				if baseTy, ok := e.interfaces[cd.BaseClass]; ok {
					e.define("super", Symbol{Ty: baseTy})
				}
			}
			inferred, ok := e.inferUnannotatedReturnType(m.Body, sig.ParamNames, e.bodyParamTypes(m.Params, sig.ParamTypes))
			e.popScope()
			if !ok {
				continue
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
	return fmt.Errorf("%d:%d: type mismatch in argument %d — a value of one type cannot be used where an incompatible type is expected", a.GetPos().Line, a.GetPos().Col, i+1)
}

// declaredMethodThis is the class a method's explicit `this: C` parameter
// names, when it is another class than the method's own.
func (e *Emitter) declaredMethodThis(body *ast.BlockStatement, own Type) (Type, bool) {
	if e.prog == nil || body == nil {
		return Type{}, false
	}
	for n, ta := range e.prog.ThisParams {
		fd, ok := n.(*ast.FunctionDeclaration)
		if !ok || fd.Body != body || ta == nil || ta.Name == "void" {
			continue
		}
		t := e.resolveType(ta)
		if !t.IsClass() || t.ClassName == own.ClassName || t.Nullable() {
			return Type{}, false
		}
		return t, true
	}
	return Type{}, false
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
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v.Ref, res))
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
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", o.Ref, res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(checkL)
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
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", e.emitBuildWrapper(v.Ref, w.class).Ref, res))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(nextL)
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, res))
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
	at := e.inferExprType(args[0])
	switch {
	case at.IsNull() || at.IsUndefined():
		if _, err := e.emitExpr(args[0]); err != nil {
			return Value{}, err
		}
		return empty()
	case isUnconstrainedDynamic(at):
		return e.emitObjectCtorDynamic(args[0])
	case at.IsBigInt() || at.IsSymbol():
		return Value{}, fmt.Errorf("%d:%d: Object() of a bigint or a symbol is not supported: their wrapper objects do not exist yet", pos.Line, pos.Col)
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
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", r, isRef, isDyn))
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
		sig.RetType = e.resolveType(m.ReturnType)
		sig.RetThis = m.ReturnType.IsThis
	} else if m.Body == nil {
		// Abstract method with no return-type annotation: nothing
		// to infer from (no body) — defaults to void, same as any
		// other unannotated-return function.
		sig.RetType = TypeVoid
	} else {
		sig.RetType = TypeVoid
		// Best-effort inference, same as registerFunctions — but the
		// method's own "this" also needs to be visible in the temp
		// scope inferUnannotatedReturnType pushes for its parameters,
		// since the inferred return expression may read this.field.
		e.pushScope()
		e.define("this", Symbol{Ty: provisionalTy})
		// Bind "super" too (typed as the base class), so a method whose
		// return expression is `super.method()` can resolve the base
		// method's type during inference — mirrors emitClassMember's own
		// super binding (ADR-00906).
		if cd.BaseClass != "" {
			if baseTy, ok := e.interfaces[cd.BaseClass]; ok {
				e.define("super", Symbol{Ty: baseTy})
			}
		}
		if inferred, ok := e.inferUnannotatedReturnType(m.Body, sig.ParamNames, e.bodyParamTypes(m.Params, sig.ParamTypes)); ok {
			sig.RetType = inferred
		}
		e.popScope()
		// An unannotated async method returns the promise of its
		// result, as an annotated `Promise<T>` one does.
		if m.IsAsync && !m.IsGenerator && !sig.RetType.IsPromise() {
			sig.RetType = PromiseOf(sig.RetType)
		}
	}
	return sig
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
	rt := e.inferExprType(m.Object)
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
