// emit_classes.go — TDD-00009 Stages 1-3: class registration (fields, ctor
// and method signatures, inheritance/override analysis), constructor/method
// emission, `this`/`super`, `new ClassName(args)`, method-call dispatch
// (static or vtable-indirect), Stage 1a's class-based for...of iterator
// protocol, and `instanceof` (including through inheritance).
package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
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
	CtorSig     FuncSig // RetType always TypeVoid; zero value if Constructor == nil

	// MethodSigs/Methods hold the class's full *effective* method table —
	// own declarations plus everything inherited (overridden entries use
	// the overriding declaration/signature) — so every existing lookup
	// site written before Stage 3 (emit_call.go, emit_exprs_types.go,
	// emit_stmts.go's for...of) keeps working with no changes: a method
	// inherited-but-not-overridden is found here exactly like an own one.
	Methods    map[string]*ast.FunctionDeclaration
	MethodSigs map[string]FuncSig
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

	// HasVTable/VTableSize are uniform across every class sharing a
	// RootClass (see registerClasses' Pass 2) — true/nonzero only when at
	// least one method anywhere in the tree is overridden by a descendant.
	HasVTable  bool
	VTableSize int

	// HasEventEmitter/EventEmitterPayload (TDD-00023) are set for a class
	// that directly `extends EventEmitter<T>`, and propagate to every
	// descendant the same way BaseClass-derived fields/HasVTable already do
	// — see registerClasses' Pass 1. EventEmitterPayload is the T in
	// EventEmitter<T>, valid only when HasEventEmitter.
	HasEventEmitter     bool
	EventEmitterPayload Type

	// --- TDD-00009 Stage 4 ---

	// IsAbstract marks an `abstract class` — cannot be directly
	// instantiated (emitNewExpression), and is exempt from the
	// "every method must have a real implementation" completeness check
	// every other (concrete) class is held to.
	IsAbstract bool
	// Implements is the class's `implements A, B, ...` clause — purely a
	// compile-time self-check (registerClasses' Pass 1), never affecting
	// codegen/dispatch: does this class already structurally satisfy each
	// named interface's fields and method signatures.
	Implements []string

	// FieldOrigin names which class actually declared an (instance) field
	// — needed by checkMemberVisibility to find the field's own visibility
	// annotation, since InheritedFields/OwnFields/FlatFields don't
	// otherwise track per-field provenance.
	FieldOrigin map[string]string
	// OwnFieldVisibility/OwnMethodVisibility are this class's own-declared
	// members' visibility ("private"/"protected"/"" for public) — an
	// inherited (not overridden) member's effective visibility is found by
	// looking it up on its origin/implementor class instead.
	OwnFieldVisibility  map[string]string
	OwnMethodVisibility map[string]string

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
	StaticFieldTypes         map[string]Type
	OwnStaticFieldTypes      map[string]Type
	StaticFieldOwner         map[string]string
	OwnStaticFieldVisibility map[string]string

	// StaticMethodSigs/StaticMethodImplementor mirror MethodSigs/
	// MethodImplementor's inherit-then-override shape, but a static method
	// call is a bare class-name receiver — never polymorphic, so there is
	// no vtable/MethodDispatchSlot concept for statics at all, always a
	// direct call to StaticMethodImplementor.
	StaticMethodSigs          map[string]FuncSig
	StaticMethodImplementor   map[string]string
	OwnStaticMethodVisibility map[string]string
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
func (e *Emitter) canonicalizeClassTy(ty Type) Type {
	if ty.IsClass {
		if info, ok := e.classes[ty.ClassName]; ok {
			// Nullable is a property of the field's own annotation (`Node |
			// null`), not of the class itself — info.Ty is the bare class
			// registry entry and is never Nullable, so swapping it in
			// wholesale would silently discard a caller's `| null`
			// annotation (found via instanceof's null-check reduction for a
			// nullable class-typed field, TDD-00009 Stage 2).
			canon := info.Ty
			canon.Nullable = ty.Nullable
			return canon
		}
	}
	return ty
}

// buildParamSig resolves a parameter list into a FuncSig's parameter half
// (types/names/defaults/rest), the same per-parameter rules
// registerFunctions uses for top-level functions: an explicit annotation is
// resolved via resolveType; a rest parameter with no annotation defaults to
// number[]; anything else unannotated defaults to (inferred) TypeI64.
func (e *Emitter) buildParamSig(params []ast.Param) FuncSig {
	var sig FuncSig
	for _, p := range params {
		var pty Type
		if p.Type != nil {
			pty = e.resolveType(p.Type)
		} else if p.Rest {
			pty = ArrayOf(TypeI64)
		} else {
			pty = TypeI64
			pty.Inferred = true
		}
		sig.ParamTypes = append(sig.ParamTypes, pty)
		sig.ParamNames = append(sig.ParamNames, p.Name)
		sig.Defaults = append(sig.Defaults, p.Default)
	}
	if len(params) > 0 && params[len(params)-1].Rest {
		sig.HasRest = true
	}
	return sig
}

// sigCompatible reports whether an overriding method's signature is
// call-compatible with the signature it overrides — same parameter count
// and per-parameter IR shape, same return IR shape. An indirect vtable call
// site only ever knows the *introducing* declaration's signature, so every
// override sharing that slot must agree on the actual LLVM call shape, or
// the indirect call itself would be unsound.
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

// checkMemberVisibility enforces TDD-00009 Stage 4's private/protected
// rules — compile-time only, matching real TypeScript's own erasure (zero
// runtime check ever emitted). vis == "" (public, the common case) is a
// no-op fast path. originClass is whichever class actually declared the
// member (FieldOrigin / MethodImplementor / StaticFieldOwner /
// StaticMethodImplementor, depending on the kind of access).
//
// The enclosing class is read from scope symbol "__kml_enclosing_class"
// (bound unconditionally by emitClassMember/emitClassStaticInit, both
// static and instance contexts alike — "this" alone doesn't work here
// since a static method has no receiver). Top-level code (no enclosing
// class at all) fails both private and protected checks. This one check
// also correctly rejects `super.privateMethod()` with no special-casing:
// the enclosing class there is the *subclass*, which a private check on
// the *base*'s member is right to refuse.
func (e *Emitter) checkMemberVisibility(originClass, vis, kind, name string, pos ast.Pos) error {
	if vis == "" {
		return nil
	}
	var enclosing string
	if sym, ok := e.lookup("__kml_enclosing_class"); ok {
		enclosing = sym.Ty.ClassName
	}
	if enclosing == originClass {
		return nil
	}
	if vis == "protected" && enclosing != "" {
		for _, anc := range e.classes[enclosing].AncestorChain {
			if anc == originClass {
				return nil
			}
		}
	}
	return fmt.Errorf("%d:%d: %s '%s' is %s and not accessible here (declared on class '%s')", pos.Line, pos.Col, kind, name, vis, originClass)
}

// checkFieldVisibility looks up an instance field's declaring class/
// visibility (via FieldOrigin/OwnFieldVisibility) and delegates to
// checkMemberVisibility. A no-op if className isn't a registered class or
// fieldName isn't a tracked field (both cases handled/reported elsewhere).
func (e *Emitter) checkFieldVisibility(className, fieldName string, pos ast.Pos) error {
	info, ok := e.classes[className]
	if !ok {
		return nil
	}
	origin, ok := info.FieldOrigin[fieldName]
	if !ok {
		return nil
	}
	vis := e.classes[origin].OwnFieldVisibility[fieldName]
	return e.checkMemberVisibility(origin, vis, "field", fieldName, pos)
}

// emitStaticFieldRead evaluates `ClassName.staticField` (TDD-00009 Stage
// 4): a plain load off whichever class's global actually owns the storage
// (StaticFieldOwner — an inherited, non-redeclared static field shares its
// base's global rather than getting its own).
func (e *Emitter) emitStaticFieldRead(info ClassInfo, className, fieldName string, pos ast.Pos) (Value, error) {
	fieldTy, ok := info.StaticFieldTypes[fieldName]
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: class '%s' has no static field '%s'", pos.Line, pos.Col, className, fieldName)
	}
	owner := info.StaticFieldOwner[fieldName]
	vis := e.classes[owner].OwnStaticFieldVisibility[fieldName]
	if err := e.checkMemberVisibility(owner, vis, "static field", fieldName, pos); err != nil {
		return Value{}, err
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr @%s_static_%s, align %d", reg, fieldTy.IR, owner, fieldName, fieldTy.Align()))
	return Value{Ref: reg, Ty: fieldTy}, nil
}

// emitStaticFieldAssign evaluates `ClassName.staticField = val` (or a
// compound op) — TDD-00009 Stage 4. Same shape as the instance field-write
// path in emit_exprs_assign.go, minus the GEP: a static field's storage is
// a plain global, addressed directly by name.
func (e *Emitter) emitStaticFieldAssign(info ClassInfo, className, fieldName, op string, rhsExpr ast.Expression, pos ast.Pos) (Value, error) {
	fieldTy, ok := info.StaticFieldTypes[fieldName]
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: class '%s' has no static field '%s'", pos.Line, pos.Col, className, fieldName)
	}
	owner := info.StaticFieldOwner[fieldName]
	vis := e.classes[owner].OwnStaticFieldVisibility[fieldName]
	if err := e.checkMemberVisibility(owner, vis, "static field", fieldName, pos); err != nil {
		return Value{}, err
	}
	globalPtr := fmt.Sprintf("@%s_static_%s", owner, fieldName)

	if isLogicalAssignOp(op) {
		return e.emitLogicalCompoundAssign(op, globalPtr, fieldTy, rhsExpr)
	}

	var rhs Value
	var err error
	if op == "=" {
		rhs, err = e.emitExpr(rhsExpr)
		if err != nil {
			return Value{}, err
		}
	} else {
		curReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", curReg, fieldTy.IR, globalPtr, fieldTy.Align()))
		cur := Value{Ref: curReg, Ty: fieldTy}
		rhsVal, err := e.emitExpr(rhsExpr)
		if err != nil {
			return Value{}, err
		}
		if err := dateCompoundAssignGuard(op, fieldTy.IsDate, rhsVal.Ty.IsDate); err != nil {
			return Value{}, fmt.Errorf("%d:%d: %s", pos.Line, pos.Col, err)
		}
		rhsVal = e.coerce(rhsVal, fieldTy)
		rhs, err = e.emitArith(strings.TrimSuffix(op, "="), cur, rhsVal, fieldTy)
		if err != nil {
			return Value{}, err
		}
	}
	rhs = e.coerce(rhs, fieldTy)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, rhs.Ref, globalPtr, fieldTy.Align()))
	return rhs, nil
}

// hasTopLevelSuperCall reports whether a constructor body contains a
// `super(...)` call as one of its own top-level statements — a shallow,
// non-nested presence check (consistent with this project's existing
// "cheapest useful check" pattern, e.g. Stage 1's flat "constructor
// required if fields present" rule), not full control-flow analysis.
func hasTopLevelSuperCall(body *ast.BlockStatement) bool {
	for _, s := range body.Body {
		es, ok := s.(*ast.ExpressionStatement)
		if !ok {
			continue
		}
		call, ok := es.Expr.(*ast.CallExpression)
		if !ok {
			continue
		}
		if _, ok := call.Callee.(*ast.SuperExpression); ok {
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
//	Pass 2: for each maximal inheritance tree, decide HasVTable/VTableSize
//	  uniformly (true only if some MethodSlot anywhere in the tree ended up
//	  Virtual) and assign slot indices to every Virtual slot.
//	Pass 3: finalize each class's real Ty (field layout now depends on
//	  Pass 2's HasVTable decision) and publish it into e.interfaces.
func (e *Emitter) registerClasses(prog *ast.Program) error {
	classDeclByName := make(map[string]*ast.ClassDeclaration)
	for _, stmt := range prog.Body {
		if cd, ok := stmt.(*ast.ClassDeclaration); ok {
			// A generic class's field/param/return types reference an
			// unresolvable bare type-parameter name — it's kept entirely out
			// of this registration pipeline (no placeholder, no topo-order
			// entry) and instantiated on demand at each `new
			// ClassName<T>(...)` construction site instead (TDD-00010 V1,
			// see emit_generics.go). Deliberately out of V1 scope: a generic
			// class can't be an `extends` base or target here.
			if len(cd.TypeParams) > 0 {
				if cd.BaseClass != "" {
					return fmt.Errorf("%d:%d: generic class '%s' cannot use 'extends' — not yet supported (see docs/tdd/TDD-00010.md)", cd.GetPos().Line, cd.GetPos().Col, cd.Name)
				}
				if cd.IsAbstract {
					return fmt.Errorf("%d:%d: generic class '%s' cannot be abstract — not yet supported (see docs/tdd/TDD-00010.md)", cd.GetPos().Line, cd.GetPos().Col, cd.Name)
				}
				if len(cd.Implements) > 0 {
					return fmt.Errorf("%d:%d: generic class '%s' cannot use 'implements' — not yet supported (see docs/tdd/TDD-00010.md)", cd.GetPos().Line, cd.GetPos().Col, cd.Name)
				}
				if len(cd.StaticBlocks) > 0 {
					return fmt.Errorf("%d:%d: generic class '%s' cannot have a static {} block — not yet supported (see docs/tdd/TDD-00010.md)", cd.GetPos().Line, cd.GetPos().Col, cd.Name)
				}
				for _, f := range cd.Fields {
					if f.Static {
						return fmt.Errorf("%d:%d: generic class '%s' cannot have a static field ('%s') — not yet supported (see docs/tdd/TDD-00010.md)", cd.GetPos().Line, cd.GetPos().Col, cd.Name, f.Name)
					}
				}
				for _, m := range cd.Methods {
					if m.IsStatic {
						return fmt.Errorf("%d:%d: generic class '%s' cannot have a static method ('%s') — not yet supported (see docs/tdd/TDD-00010.md)", cd.GetPos().Line, cd.GetPos().Col, cd.Name, m.Name)
					}
				}
				e.genericClasses[cd.Name] = cd
				continue
			}
			classDeclByName[cd.Name] = cd
			// Placeholder: correct IR/IsObject/IsClass/ClassName, no fields
			// yet — see canonicalizeClassTy's doc comment for why this must
			// exist before any field/param/return type is resolved.
			e.interfaces[cd.Name] = ClassType(cd.Name, nil, nil, false, false)
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
		if cd.BaseClass == "EventEmitter" {
			// A synthetic root (TDD-00023): EventEmitter is never itself a
			// registered class (no vtable slot, no TagID, not
			// instanceof-checkable — same as Error/Date/Map), so there is
			// nothing in classDeclByName to recurse into.
			if len(cd.BaseTypeArgs) != 1 {
				return fmt.Errorf("%d:%d: class '%s' extends EventEmitter but supplies %d type argument(s), expected exactly 1 (EventEmitter<T>)", cd.GetPos().Line, cd.GetPos().Col, name, len(cd.BaseTypeArgs))
			}
		} else if len(cd.BaseTypeArgs) > 0 {
			return fmt.Errorf("%d:%d: class '%s' extends '%s' with type arguments, but only EventEmitter<T> currently supports generic extends", cd.GetPos().Line, cd.GetPos().Col, name, cd.BaseClass)
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
	var nextTagID int64
	for _, name := range topoOrder {
		cd := classDeclByName[name]

		var baseInfo ClassInfo
		// A class directly `extends EventEmitter<T>` (TDD-00023) has
		// haveBase=false — EventEmitter is a synthetic root, never a real
		// registered class (see Pass 0 above), so it contributes no fields/
		// constructor to flatten/forward, and this class flows through
		// exactly the same "root class" rules below as one with no base at
		// all.
		isEEDirect := cd.BaseClass == "EventEmitter"
		haveBase := cd.BaseClass != "" && !isEEDirect
		if haveBase {
			baseInfo = e.classes[cd.BaseClass]
		}
		hasEventEmitter := isEEDirect || (haveBase && baseInfo.HasEventEmitter)
		var eePayload Type
		switch {
		case isEEDirect:
			eePayload = e.resolveEventEmitterPayloadType(cd.BaseTypeArgs[0])
		case haveBase && baseInfo.HasEventEmitter:
			eePayload = baseInfo.EventEmitterPayload
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
		ownFieldVisibility := make(map[string]string)
		staticFieldTypes := make(map[string]Type, len(baseInfo.StaticFieldTypes))
		for k, v := range baseInfo.StaticFieldTypes {
			staticFieldTypes[k] = v
		}
		staticFieldOwner := make(map[string]string, len(baseInfo.StaticFieldOwner))
		for k, v := range baseInfo.StaticFieldOwner {
			staticFieldOwner[k] = v
		}
		ownStaticFieldTypes := make(map[string]Type)
		ownStaticFieldVisibility := make(map[string]string)

		var ownFields []Field
		for _, f := range cd.Fields {
			if f.Name == ClassTagField {
				return fmt.Errorf("%d:%d: class '%s' cannot declare a field named '%s' — reserved for the compiler's internal runtime type tag", cd.GetPos().Line, cd.GetPos().Col, cd.Name, ClassTagField)
			}
			if f.Name == ClassVTableField {
				return fmt.Errorf("%d:%d: class '%s' cannot declare a field named '%s' — reserved for the compiler's internal runtime vtable pointer", cd.GetPos().Line, cd.GetPos().Col, cd.Name, ClassVTableField)
			}
			if f.Name == ClassEventEmitterField {
				return fmt.Errorf("%d:%d: class '%s' cannot declare a field named '%s' — reserved for the compiler's internal EventEmitter listener map", cd.GetPos().Line, cd.GetPos().Col, cd.Name, ClassEventEmitterField)
			}
			if f.Static {
				fty := e.resolveType(f.Type)
				staticFieldTypes[f.Name] = fty
				staticFieldOwner[f.Name] = cd.Name
				ownStaticFieldTypes[f.Name] = fty
				ownStaticFieldVisibility[f.Name] = f.Visibility
				continue
			}
			if seen[f.Name] {
				return fmt.Errorf("%d:%d: class '%s' redeclares inherited field '%s'", cd.GetPos().Line, cd.GetPos().Col, cd.Name, f.Name)
			}
			seen[f.Name] = true
			fty := e.resolveType(f.Type)
			ownFields = append(ownFields, Field{Name: f.Name, Ty: fty})
			fieldOrigin[f.Name] = cd.Name
			ownFieldVisibility[f.Name] = f.Visibility
		}
		flatFields := append(append([]Field{}, inheritedFields...), ownFields...)

		// Provisional Ty for this class's own unannotated-return-type
		// inference during this pass only — HasVTable is always false here
		// (irrelevant to field name/type lookup by name, only affects a
		// hidden field's presence); Pass 3 rebuilds the real Ty once
		// HasVTable is known and publishes it into e.interfaces/info.Ty.
		// hasEventEmitter, unlike HasVTable, is already fully known by this
		// point (computed above, not deferred to a later pass), so it's
		// threaded through here too.
		provisionalTy := ClassType(cd.Name, inheritedFields, ownFields, false, hasEventEmitter)
		e.interfaces[cd.Name] = provisionalTy

		ancestorChain := append([]string{}, baseInfo.AncestorChain...)
		rootClass := cd.Name
		if haveBase {
			ancestorChain = append(ancestorChain, cd.BaseClass)
			rootClass = baseInfo.RootClass
		}

		info := ClassInfo{
			BaseClass:                 cd.BaseClass,
			AncestorChain:             ancestorChain,
			RootClass:                 rootClass,
			InheritedFields:           inheritedFields,
			OwnFields:                 ownFields,
			FlatFields:                flatFields,
			Methods:                   make(map[string]*ast.FunctionDeclaration),
			MethodSigs:                make(map[string]FuncSig),
			MethodImplementor:         make(map[string]string),
			MethodDispatchSlot:        make(map[string]*MethodSlot),
			TagID:                     nextTagID,
			IsAbstract:                cd.IsAbstract,
			Implements:                cd.Implements,
			FieldOrigin:               fieldOrigin,
			OwnFieldVisibility:        ownFieldVisibility,
			OwnMethodVisibility:       make(map[string]string),
			StaticFieldTypes:          staticFieldTypes,
			OwnStaticFieldTypes:       ownStaticFieldTypes,
			StaticFieldOwner:          staticFieldOwner,
			OwnStaticFieldVisibility:  ownStaticFieldVisibility,
			StaticMethodSigs:          make(map[string]FuncSig),
			StaticMethodImplementor:   make(map[string]string),
			OwnStaticMethodVisibility: make(map[string]string),
			HasEventEmitter:           hasEventEmitter,
			EventEmitterPayload:       eePayload,
		}
		nextTagID++

		if haveBase {
			for mname, sig := range baseInfo.MethodSigs {
				info.MethodSigs[mname] = sig
				info.MethodImplementor[mname] = baseInfo.MethodImplementor[mname]
				info.MethodDispatchSlot[mname] = baseInfo.MethodDispatchSlot[mname]
				info.Methods[mname] = baseInfo.Methods[mname]
			}
			info.MethodOrder = append(info.MethodOrder, baseInfo.MethodOrder...)
			for mname, sig := range baseInfo.StaticMethodSigs {
				info.StaticMethodSigs[mname] = sig
				info.StaticMethodImplementor[mname] = baseInfo.StaticMethodImplementor[mname]
			}
		}

		ownDeclared := make(map[string]bool, len(cd.Methods))
		ownStaticDeclared := make(map[string]bool, len(cd.Methods))
		for _, m := range cd.Methods {
			// Reserved-method-name collision (TDD-00023): a class in an
			// EventEmitter-rooted tree cannot declare any of EventEmitter's
			// own method names — those are hand-written codegen dispatched
			// by name (emit_call.go), never real AST-driven class methods,
			// so there is no vtable slot for them to occupy and no
			// "override an EventEmitter method" interaction to support.
			if !m.IsStatic && hasEventEmitter && isEventEmitterMethodName(m.Name) {
				return fmt.Errorf("%d:%d: class '%s' cannot declare method '%s' — reserved by EventEmitter<T>", m.GetPos().Line, m.GetPos().Col, cd.Name, m.Name)
			}
			sig := e.buildParamSig(m.Params)
			if m.ReturnType != nil {
				sig.RetType = e.resolveType(m.ReturnType)
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
				if inferred, ok := e.inferUnannotatedReturnType(m.Body, sig.ParamNames, sig.ParamTypes); ok {
					sig.RetType = inferred
				}
				e.popScope()
			}

			if m.IsStatic {
				if ownStaticDeclared[m.Name] {
					return fmt.Errorf("%d:%d: class '%s' declares more than one static method named '%s'", m.GetPos().Line, m.GetPos().Col, cd.Name, m.Name)
				}
				ownStaticDeclared[m.Name] = true
				info.StaticMethodSigs[m.Name] = sig
				info.StaticMethodImplementor[m.Name] = cd.Name
				info.OwnStaticMethodVisibility[m.Name] = m.Visibility
				continue
			}

			if ownDeclared[m.Name] {
				return fmt.Errorf("%d:%d: class '%s' declares more than one method named '%s'", m.GetPos().Line, m.GetPos().Col, cd.Name, m.Name)
			}
			ownDeclared[m.Name] = true

			if existingSig, overriding := info.MethodSigs[m.Name]; overriding {
				if !sigCompatible(existingSig, sig) {
					return fmt.Errorf("%d:%d: method '%s' on class '%s' overrides an inherited method with an incompatible signature", m.GetPos().Line, m.GetPos().Col, m.Name, cd.Name)
				}
				slot := info.MethodDispatchSlot[m.Name]
				if slot == nil {
					slot = &MethodSlot{Name: m.Name}
					info.MethodDispatchSlot[m.Name] = slot
				}
				slot.Virtual = true
			} else {
				info.MethodDispatchSlot[m.Name] = &MethodSlot{Name: m.Name}
				info.MethodOrder = append(info.MethodOrder, m.Name)
			}
			info.MethodImplementor[m.Name] = cd.Name
			info.MethodSigs[m.Name] = sig
			info.Methods[m.Name] = m
			info.OwnMethodVisibility[m.Name] = m.Visibility
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
				if decl.Body == nil {
					return fmt.Errorf("%d:%d: class '%s' does not implement abstract method '%s' inherited from '%s'", cd.GetPos().Line, cd.GetPos().Col, cd.Name, mname, info.MethodImplementor[mname])
				}
			}
		}

		// `implements` conformance check (TDD-00009 Stage 4) — purely a
		// compile-time self-check, never affecting codegen/dispatch: does
		// this class's already-built effective field/method tables satisfy
		// each named interface's shape. Fails fast on the first mismatch.
		for _, ifaceName := range cd.Implements {
			ifaceTy, ok := e.interfaces[ifaceName]
			if !ok {
				return fmt.Errorf("%d:%d: class '%s' implements unknown type '%s'", cd.GetPos().Line, cd.GetPos().Col, cd.Name, ifaceName)
			}
			for _, ifield := range ifaceTy.Fields {
				found := false
				for _, cf := range flatFields {
					if cf.Name == ifield.Name && cf.Ty.IR == ifield.Ty.IR {
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("%d:%d: class '%s' does not satisfy interface '%s': missing or incompatible field '%s'", cd.GetPos().Line, cd.GetPos().Col, cd.Name, ifaceName, ifield.Name)
				}
			}
			for mname, isig := range e.interfaceMethodSigs[ifaceName] {
				csig, ok := info.MethodSigs[mname]
				if !ok || !sigCompatible(isig, csig) {
					return fmt.Errorf("%d:%d: class '%s' does not satisfy interface '%s': missing or incompatible method '%s'", cd.GetPos().Line, cd.GetPos().Col, cd.Name, ifaceName, mname)
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
			callsSuper := hasTopLevelSuperCall(cd.Constructor.Body)
			if baseCtor != nil && !callsSuper {
				return fmt.Errorf("%d:%d: constructor of class '%s' must call super(...) (base class '%s' has a constructor)", cd.Constructor.GetPos().Line, cd.Constructor.GetPos().Col, cd.Name, cd.BaseClass)
			}
			if baseCtor == nil && callsSuper {
				if haveBase {
					return fmt.Errorf("%d:%d: constructor of class '%s' calls super(...) but base class '%s' has no constructor", cd.Constructor.GetPos().Line, cd.Constructor.GetPos().Col, cd.Name, cd.BaseClass)
				}
				if isEEDirect {
					return fmt.Errorf("%d:%d: constructor of class '%s' calls super(...) but EventEmitter has no constructor to call", cd.Constructor.GetPos().Line, cd.Constructor.GetPos().Col, cd.Name)
				}
				return fmt.Errorf("%d:%d: constructor of class '%s' calls super(...) but the class has no base class", cd.Constructor.GetPos().Line, cd.Constructor.GetPos().Col, cd.Name)
			}
			info.Constructor = cd.Constructor
			sig := e.buildParamSig(cd.Constructor.Params)
			sig.RetType = TypeVoid
			info.CtorSig = sig

		case len(ownFields) == 0 && baseCtor != nil && !baseCtorSig.HasRest:
			// Implicit pass-through constructor: `constructor(...args) {
			// super(...args) }`, forwarding every base parameter 1:1 — only
			// possible (and only needed) when this class adds no fields of
			// its own and the base's own constructor has no rest parameter
			// (a rest-forwarding super(...spread) call isn't representable
			// without a general spread-call mechanism this compiler doesn't
			// have — a base with a rest-parameter constructor simply
			// requires an explicit derived constructor instead).
			params := make([]ast.Param, len(baseCtorSig.ParamNames))
			superArgs := make([]ast.Expression, len(baseCtorSig.ParamNames))
			for i, pname := range baseCtorSig.ParamNames {
				params[i] = ast.Param{Name: pname}
				superArgs[i] = ast.NewIdentifier(pname, cd.GetPos())
			}
			superCall := ast.NewCallExpression(ast.NewSuperExpression(cd.GetPos()), superArgs, cd.GetPos())
			body := ast.NewBlockStatement([]ast.Statement{ast.NewExpressionStatement(superCall, cd.GetPos())}, cd.GetPos())
			info.Constructor = &ast.FunctionDeclaration{Name: "constructor", Params: params, Body: body}
			info.CtorSig = baseCtorSig

		case len(ownFields) > 0:
			// A class with fields but no constructor would leave every
			// instance's fields as uninitialized (garbage) malloc'd memory
			// — this compiler has no field-initializer syntax (Stage 0
			// scope) to fall back on; requiring a constructor here is the
			// same "no silently uninitialized state" philosophy Stage 1
			// already established, just now scoped to *own* fields (an
			// inherited field is the base constructor's responsibility via
			// super(...), not this class's).
			return fmt.Errorf("%d:%d: class '%s' has fields but no constructor to initialize them", cd.GetPos().Line, cd.GetPos().Col, cd.Name)
		}

		e.classes[cd.Name] = info
	}
	// Continue the TagID sequence for any later on-demand generic-class
	// instantiation (TDD-00010 V1, emit_generics.go) — always past every
	// real class's own TagID, so a generic instantiation's runtime identity
	// tag can never collide with an unrelated real class's.
	e.nextClassTagID = nextTagID

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

	// Pass 2: per inheritance tree (grouped by RootClass), decide
	// HasVTable/VTableSize uniformly and assign vtable slot indices to
	// every Virtual MethodSlot — deferred until every class's own override
	// analysis (Pass 1) is done, since a deep override can retroactively
	// mark an ancestor's slot Virtual after that ancestor was itself
	// already processed.
	type rootAgg struct {
		slots []*MethodSlot
		seen  map[*MethodSlot]bool
	}
	roots := make(map[string]*rootAgg)
	for _, name := range topoOrder {
		info := e.classes[name]
		agg := roots[info.RootClass]
		if agg == nil {
			agg = &rootAgg{seen: make(map[*MethodSlot]bool)}
			roots[info.RootClass] = agg
		}
		for _, mname := range info.MethodOrder {
			slot := info.MethodDispatchSlot[mname]
			if slot != nil && !agg.seen[slot] {
				agg.seen[slot] = true
				agg.slots = append(agg.slots, slot)
			}
		}
	}
	for rootName, agg := range roots {
		size := 0
		for _, slot := range agg.slots {
			if slot.Virtual {
				slot.Index = size
				size++
			}
		}
		if size == 0 {
			continue
		}
		for _, name := range topoOrder {
			info := e.classes[name]
			if info.RootClass != rootName {
				continue
			}
			info.HasVTable = true
			info.VTableSize = size
			e.classes[name] = info
		}
	}

	// Pass 3: finalize each class's real Ty now that HasVTable is known,
	// and publish it into e.interfaces so every other type-resolution call
	// site (unchanged since before Stage 3) sees the final shape.
	for _, name := range topoOrder {
		info := e.classes[name]
		info.Ty = ClassType(name, info.InheritedFields, info.OwnFields, info.HasVTable, info.HasEventEmitter)
		e.classes[name] = info
		e.interfaces[name] = info.Ty
	}

	return nil
}

// emitClassDecl emits one class's constructor (if declared or synthesized —
// see registerClasses' constructor rules) and every own-declared method as
// ordinary LLVM functions named "@ClassName_constructor" /
// "@ClassName_methodName". A method inherited-but-not-overridden gets no
// function of its own here — calls to it resolve (via emitClassCall) to its
// nearest ancestor's own function instead.
func (e *Emitter) emitClassDecl(cd *ast.ClassDeclaration) error {
	info := e.classes[cd.Name]
	if info.Constructor != nil {
		llvmName := cd.Name + "_constructor"
		if err := e.emitClassMember(llvmName, info.Ty, info.Constructor.Params, info.CtorSig, info.Constructor.Body, TypeVoid, info.Constructor.GetPos(), false); err != nil {
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
		if m.IsStatic {
			sig := info.StaticMethodSigs[m.Name]
			llvmName := cd.Name + "_static_" + m.Name
			if err := e.emitClassMember(llvmName, info.Ty, m.Params, sig, m.Body, sig.RetType, m.GetPos(), true); err != nil {
				return err
			}
			continue
		}
		sig := info.MethodSigs[m.Name]
		llvmName := cd.Name + "_" + m.Name
		if err := e.emitClassMember(llvmName, info.Ty, m.Params, sig, m.Body, sig.RetType, m.GetPos(), false); err != nil {
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
	info := e.classes[className]
	for name, ty := range info.OwnStaticFieldTypes {
		e.emitGlobal(fmt.Sprintf("@%s_static_%s = global %s zeroinitializer, align %d", className, name, ty.IR, ty.Align()))
	}
}

// emitClassStaticInit emits `@ClassName_staticinit()`, running every
// static {} block this class declares, concatenated in declaration order
// (TDD-00009 Stage 4) — called once from EmitProgram's Pass 3, before any
// top-level statement runs. A static context: no "this"/"super" (there is
// no receiver), but "__kml_enclosing_class" is still bound so a private
// static field/method access from inside the block is checked correctly.
func (e *Emitter) emitClassStaticInit(cd *ast.ClassDeclaration) error {
	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedLabelCtr := e.labelCtr
	savedScopes := e.scopes
	savedRetType := e.currentRetType

	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.labelCtr = 0
	e.scopes = nil
	e.blockDone = false
	e.currentRetType = TypeVoid
	e.pushScope()
	e.define("__kml_enclosing_class", Symbol{Ty: e.classes[cd.Name].Ty})

	for _, block := range cd.StaticBlocks {
		for _, stmt := range block.Body {
			if err := e.emitStmt(stmt); err != nil {
				return err
			}
		}
	}
	e.emitTerminator("ret void")

	llvmName := cd.Name + "_staticinit"
	e.functions.WriteString(fmt.Sprintf("\ndefine void @%s() {\nentry:\n", llvmName))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")

	e.allocas = savedAllocas
	e.body = savedBody
	e.regCtr = savedRegCtr
	e.labelCtr = savedLabelCtr
	e.scopes = savedScopes
	e.currentRetType = savedRetType
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
// any HasVTable tree, and for an abstract class (TDD-00009 Stage 4): its
// own vtable, if built, would need a slot pointing at its own never-emitted
// abstract-method stub, but since `new AbstractClass()` is always rejected
// (emitNewExpression), no instance ever exists to reference it — simplest
// correct fix is not emitting it at all, rather than null-filling a slot
// nothing will ever read.
func (e *Emitter) emitClassVTable(className string) {
	info := e.classes[className]
	if !info.HasVTable || info.IsAbstract {
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
		slots[slot.Index] = fmt.Sprintf("ptr @%s_%s", info.MethodImplementor[mname], mname)
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
// top-level function already depends on (same reasoning docs/adr/ADR-00061.md
// gives for resolveObjectPtr's ObjectLiteral case duplicating
// emitObjectLiteral).
func (e *Emitter) emitClassMember(llvmName string, classTy Type, params []ast.Param, sig FuncSig, body *ast.BlockStatement, retType Type, pos ast.Pos, isStatic bool) error {
	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedLabelCtr := e.labelCtr
	savedScopes := e.scopes
	savedRetType := e.currentRetType

	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.labelCtr = 0
	e.scopes = nil
	e.blockDone = false
	e.currentRetType = retType
	e.pushScope()

	// __kml_enclosing_class (TDD-00009 Stage 4) is a Ptr-less, lookup-only
	// scope symbol (same established idiom this function's own return-type
	// inference already uses for a bare "this" — see registerClasses)
	// recording which class this code is lexically inside, for
	// checkMemberVisibility's private/protected checks. Bound
	// unconditionally, unlike "this"/"super", since a static method has no
	// receiver at all but still needs a lexical-class identity.
	e.define("__kml_enclosing_class", Symbol{Ty: classTy})

	var llvmParams []string
	if isStatic {
		// No implicit receiver at all — a static member belongs to the
		// class itself, not an instance.
	} else {
		// Implicit receiver: LLVM parameter slot 0, never part of the AST's
		// own parameter list.
		llvmParams = append(llvmParams, "ptr %p_this")
		thisPtr := "%v_this"
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", thisPtr))
		e.emitInstr(fmt.Sprintf("store ptr %%p_this, ptr %s, align 8", thisPtr))
		e.define("this", Symbol{Ptr: thisPtr, Ty: classTy})
		if classInfo, ok := e.classes[classTy.ClassName]; ok && classInfo.BaseClass != "" {
			baseTy := e.classes[classInfo.BaseClass].Ty
			e.define("super", Symbol{Ptr: thisPtr, Ty: baseTy})
		}
	}

	for i, p := range params {
		pty := sig.ParamTypes[i]
		if pty.IsDynamic || containsDynamicElement(pty) {
			return fmt.Errorf("%d:%d: any/unknown is not yet supported as a method parameter type", pos.Line, pos.Col)
		}
		if pty.IsArray {
			llvmParams = append(llvmParams,
				fmt.Sprintf("ptr %%p_%s_ptr", p.Name),
				fmt.Sprintf("i64 %%p_%s_len", p.Name),
			)
			ptrAlloca := "%v_" + p.Name + "_ptr"
			lenAlloca := "%v_" + p.Name + "_len"
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrAlloca))
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenAlloca))
			e.emitInstr(fmt.Sprintf("store ptr %%p_%s_ptr, ptr %s, align 8", p.Name, ptrAlloca))
			e.emitInstr(fmt.Sprintf("store i64 %%p_%s_len, ptr %s, align 8", p.Name, lenAlloca))
			// Destructured array parameter — see emit_func.go's
			// emitFunctionDeclAs for the identical reasoning (this is the
			// same param-binding shape, just for a class method/constructor
			// instead of a top-level function).
			if p.ArrayPattern != nil {
				dataPtrReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataPtrReg, ptrAlloca))
				if err := e.unpackArrayPatternInto(dataPtrReg, *pty.ElemType, p.ArrayPattern); err != nil {
					return err
				}
				continue
			}
			e.define(p.Name, Symbol{Ptr: ptrAlloca, LenPtr: lenAlloca, Ty: pty})
		} else {
			llvmParams = append(llvmParams, fmt.Sprintf("%s %%p_%s", pty.IR, p.Name))
			ptrName := "%v_" + p.Name
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", ptrName, pty.IR, pty.Align()))
			e.emitInstr(fmt.Sprintf("store %s %%p_%s, ptr %s, align %d", pty.IR, p.Name, ptrName, pty.Align()))
			if p.ObjectPattern != nil {
				objPtrReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", objPtrReg, ptrName))
				if err := e.unpackObjectPatternInto(objPtrReg, pty, p.ObjectPattern, pos); err != nil {
					return err
				}
				continue
			}
			e.define(p.Name, Symbol{Ptr: ptrName, Ty: pty})
		}
	}

	for _, stmt := range body.Body {
		if err := e.emitStmt(stmt); err != nil {
			return err
		}
	}

	if retType.IR == "void" {
		e.emitTerminator("ret void")
	} else {
		e.emitTerminator("unreachable")
	}
	e.functions.WriteString(fmt.Sprintf("\ndefine %s @%s(%s) {\nentry:\n",
		retType.LLVMRetType(), llvmName, strings.Join(llvmParams, ", ")))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")

	e.allocas = savedAllocas
	e.body = savedBody
	e.regCtr = savedRegCtr
	e.labelCtr = savedLabelCtr
	e.scopes = savedScopes
	e.currentRetType = savedRetType
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
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", reg, sym.Ptr))
	return Value{Ref: reg, Ty: sym.Ty}, nil
}

// emitNewExpression evaluates `new ClassName(args)`: malloc an instance
// sized to the class's field layout, initialize the hidden tag (and, for a
// HasVTable class — TDD-00009 Stage 3 — the hidden vtable pointer), call
// its constructor (if any) with the fresh pointer as the implicit receiver,
// and return the pointer.
func (e *Emitter) emitNewExpression(ex *ast.NewExpression) (Value, error) {
	// className is the actual LLVM-symbol-bearing/e.classes-registry name —
	// ex.ClassName itself for a plain class, but a mangled per-instantiation
	// name for a generic one (TDD-00010 V1); user-facing error messages
	// below still reference ex.ClassName, the name the source actually
	// wrote.
	className := ex.ClassName
	info, ok := e.classes[ex.ClassName]
	if !ok {
		// A generic class (TDD-00010 V1) is never itself entered into
		// e.classes — only its concrete instantiations are, keyed by their
		// mangled name — so a construction site against the bare generic
		// name always misses the lookup above on its first-ever use.
		if genDecl, isGeneric := e.genericClasses[ex.ClassName]; isGeneric {
			if len(ex.TypeArgs) != 1 {
				return Value{}, fmt.Errorf("%d:%d: generic class '%s' requires exactly one explicit type argument (e.g. new %s<number>(...)) — inference isn't supported for class construction (see docs/tdd/TDD-00010.md)", ex.GetPos().Line, ex.GetPos().Col, ex.ClassName, ex.ClassName)
			}
			concrete := e.resolveType(ex.TypeArgs[0])
			mangled, err := e.instantiateGenericClass(genDecl, concrete)
			if err != nil {
				return Value{}, err
			}
			className = mangled
			info = e.classes[mangled]
		} else {
			return Value{}, fmt.Errorf("%d:%d: unknown class '%s'", ex.GetPos().Line, ex.GetPos().Col, ex.ClassName)
		}
	}
	if info.IsAbstract {
		return Value{}, fmt.Errorf("%d:%d: cannot create an instance of abstract class '%s'", ex.GetPos().Line, ex.GetPos().Col, ex.ClassName)
	}
	if info.Constructor != nil && info.Constructor.Visibility != "" {
		if err := e.checkMemberVisibility(className, info.Constructor.Visibility, "constructor", ex.ClassName, ex.GetPos()); err != nil {
			return Value{}, err
		}
	}

	e.ensureMalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, info.Ty.StructSize()))

	tagGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", tagGep, info.Ty.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", info.TagID, tagGep))

	if info.Ty.HasVTable {
		vtGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", vtGep, info.Ty.StructIR(), dataReg))
		e.emitInstr(fmt.Sprintf("store ptr @%s_vtable, ptr %s, align 8", className, vtGep))
	}

	if info.Ty.HasEventEmitter {
		e.ensureMapStrHelpers()
		listenersPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", listenersPtr))
		eeGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", eeGep, info.Ty.StructIR(), dataReg, classEventEmitterFieldIndex(info.Ty)))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", listenersPtr, eeGep))
	}

	if info.Constructor != nil {
		if len(ex.Args) != len(info.CtorSig.ParamTypes) {
			return Value{}, fmt.Errorf("%d:%d: %s constructor expects %d argument(s), got %d",
				ex.GetPos().Line, ex.GetPos().Col, ex.ClassName, len(info.CtorSig.ParamTypes), len(ex.Args))
		}
		argParts := []string{"ptr " + dataReg}
		for i, a := range ex.Args {
			val, err := e.emitExpr(a)
			if err != nil {
				return Value{}, err
			}
			val = e.coerce(val, info.CtorSig.ParamTypes[i])
			argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
		}
		e.emitInstr(fmt.Sprintf("call void @%s_constructor(%s)", className, strings.Join(argParts, ", ")))
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
func (e *Emitter) emitClassCall(objTy Type, thisVal Value, methodName string, args []ast.Expression, pos ast.Pos, forceDirect bool) (Value, error) {
	info, ok := e.classes[objTy.ClassName]
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: unknown class '%s'", pos.Line, pos.Col, objTy.ClassName)
	}
	sig, ok := info.MethodSigs[methodName]
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: class '%s' has no method '%s'", pos.Line, pos.Col, objTy.ClassName, methodName)
	}
	implementor := info.MethodImplementor[methodName]
	if vis := e.classes[implementor].OwnMethodVisibility[methodName]; vis != "" {
		if err := e.checkMemberVisibility(implementor, vis, "method", methodName, pos); err != nil {
			return Value{}, err
		}
	}
	if len(args) != len(sig.ParamTypes) {
		return Value{}, fmt.Errorf("%d:%d: %s.%s expects %d argument(s), got %d",
			pos.Line, pos.Col, objTy.ClassName, methodName, len(sig.ParamTypes), len(args))
	}

	argParts := []string{"ptr " + thisVal.Ref}
	for i, a := range args {
		val, err := e.emitExpr(a)
		if err != nil {
			return Value{}, err
		}
		val = e.coerce(val, sig.ParamTypes[i])
		argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
	}
	argsIR := strings.Join(argParts, ", ")

	slot := info.MethodDispatchSlot[methodName]
	if forceDirect || slot == nil || !slot.Virtual {
		llvmName := info.MethodImplementor[methodName] + "_" + methodName
		if sig.RetType.IR == "void" {
			e.emitInstr(fmt.Sprintf("call void @%s(%s)", llvmName, argsIR))
			return Value{Ty: TypeVoid}, nil
		}
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call %s @%s(%s)", reg, sig.RetType.LLVMRetType(), llvmName, argsIR))
		return Value{Ref: reg, Ty: sig.RetType}, nil
	}

	vtGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", vtGep, info.Ty.StructIR(), thisVal.Ref))
	vtPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", vtPtr, vtGep))
	slotGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr [%d x ptr], ptr %s, i32 0, i32 %d", slotGep, info.VTableSize, vtPtr, slot.Index))
	fnPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fnPtr, slotGep))

	paramTyStrs := []string{"ptr"}
	for _, p := range sig.ParamTypes {
		paramTyStrs = append(paramTyStrs, p.IR)
	}
	fnTypePart := "(" + strings.Join(paramTyStrs, ", ") + ")"
	if sig.RetType.IR == "void" {
		e.emitInstr(fmt.Sprintf("call void %s %s(%s)", fnTypePart, fnPtr, argsIR))
		return Value{Ty: TypeVoid}, nil
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s %s %s(%s)", reg, sig.RetType.LLVMRetType(), fnTypePart, fnPtr, argsIR))
	return Value{Ref: reg, Ty: sig.RetType}, nil
}

// emitClassMethodCall emits `objExpr.methodName(args)` — objExpr is
// evaluated generically via emitExpr (so this works on any expression, not
// just a bare identifier), then dispatch is delegated to emitClassCall.
func (e *Emitter) emitClassMethodCall(objTy Type, objExpr ast.Expression, methodName string, args []ast.Expression, pos ast.Pos) (Value, error) {
	thisVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	return e.emitClassCall(objTy, thisVal, methodName, args, pos, false)
}

// emitStaticMethodCall evaluates `ClassName.staticMethod(args)` (TDD-00009
// Stage 4): always a direct call, never virtual — a bare class-name
// receiver is never polymorphic (there's no "value of static type X
// actually holding a Y instance" scenario for a class *name*), so
// StaticMethodImplementor alone (inherited-then-overridden, same shape
// MethodImplementor uses for instance methods) is always the right target.
func (e *Emitter) emitStaticMethodCall(info ClassInfo, className, methodName string, args []ast.Expression, pos ast.Pos) (Value, error) {
	sig, ok := info.StaticMethodSigs[methodName]
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: class '%s' has no static method '%s'", pos.Line, pos.Col, className, methodName)
	}
	implementor := info.StaticMethodImplementor[methodName]
	if vis := e.classes[implementor].OwnStaticMethodVisibility[methodName]; vis != "" {
		if err := e.checkMemberVisibility(implementor, vis, "static method", methodName, pos); err != nil {
			return Value{}, err
		}
	}
	if len(args) != len(sig.ParamTypes) {
		return Value{}, fmt.Errorf("%d:%d: %s.%s expects %d argument(s), got %d",
			pos.Line, pos.Col, className, methodName, len(sig.ParamTypes), len(args))
	}

	var argParts []string
	for i, a := range args {
		val, err := e.emitExpr(a)
		if err != nil {
			return Value{}, err
		}
		val = e.coerce(val, sig.ParamTypes[i])
		argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
	}
	argsIR := strings.Join(argParts, ", ")

	llvmName := implementor + "_static_" + methodName
	if sig.RetType.IR == "void" {
		e.emitInstr(fmt.Sprintf("call void @%s(%s)", llvmName, argsIR))
		return Value{Ty: TypeVoid}, nil
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s @%s(%s)", reg, sig.RetType.LLVMRetType(), llvmName, argsIR))
	return Value{Ref: reg, Ty: sig.RetType}, nil
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
	baseInfo := e.classes[info.BaseClass]
	if baseInfo.Constructor == nil {
		return Value{}, fmt.Errorf("%d:%d: base class '%s' has no constructor to call via super(...)", ex.GetPos().Line, ex.GetPos().Col, info.BaseClass)
	}
	if len(ex.Args) != len(baseInfo.CtorSig.ParamTypes) {
		return Value{}, fmt.Errorf("%d:%d: super(...) expects %d argument(s), got %d",
			ex.GetPos().Line, ex.GetPos().Col, len(baseInfo.CtorSig.ParamTypes), len(ex.Args))
	}

	thisReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", thisReg, thisSym.Ptr))
	argParts := []string{"ptr " + thisReg}
	for i, a := range ex.Args {
		val, err := e.emitExpr(a)
		if err != nil {
			return Value{}, err
		}
		val = e.coerce(val, baseInfo.CtorSig.ParamTypes[i])
		argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
	}
	e.emitInstr(fmt.Sprintf("call void @%s_constructor(%s)", info.BaseClass, strings.Join(argParts, ", ")))
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
	return e.emitClassCall(superSym.Ty, Value{Ref: thisReg, Ty: superSym.Ty}, methodName, args, pos, true)
}

// emitForOfClassIterator implements Stage 1a's for...of extension: a class
// declaring a zero-arg `next(): T | null` method iterates by calling next()
// repeatedly until it returns the sentinel (null for a ptr-shaped T, 0
// otherwise — the same "zero/null doubles as absent" convention .find()
// already uses, including its same pre-existing ambiguity for numeric T,
// not a new limitation here). This is a genuinely different loop shape from
// the array/Map/Set path in emitForOf (no known length, one call per
// iteration instead of an index into a pre-materialized array), so it's a
// fully independent loop body — it just reuses the cond/body/inc/end labels
// and the break/continue/pendingLabel bookkeeping the caller already set up.
// recvVal is the already-evaluated receiver (evaluated once by the caller,
// not per iteration, since s.Iterable may be an arbitrary expression). The
// next() call itself goes through emitClassCall (TDD-00009 Stage 3) so an
// overridden next() dispatches correctly through the vtable when needed —
// the one bug the pre-Stage-3 direct-call version would otherwise have had.
func (e *Emitter) emitForOfClassIterator(s *ast.ForOfStatement, objTy Type, nextSig FuncSig, recvVal Value, condL, bodyL, incL, endL string) error {
	elemTy := nextSig.RetType
	elemTy.Nullable = false

	resultAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", resultAlloca, elemTy.IR, elemTy.Align()))

	varPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", varPtr, elemTy.IR, elemTy.Align()))
	e.define(s.VarName, Symbol{Ptr: varPtr, Ty: elemTy})

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	nextVal, err := e.emitClassCall(objTy, recvVal, "next", nil, s.GetPos(), false)
	if err != nil {
		return err
	}
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, nextVal.Ref, resultAlloca, elemTy.Align()))
	doneReg := e.freshReg()
	zero := "null"
	if elemTy.IR != "ptr" {
		zero = "0"
	}
	e.emitInstr(fmt.Sprintf("%s = icmp eq %s %s, %s", doneReg, elemTy.IR, nextVal.Ref, zero))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", doneReg, endL, bodyL))

	e.emitLabel(bodyL)
	loaded := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loaded, elemTy.IR, resultAlloca, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, loaded, varPtr, elemTy.Align()))
	if err := e.emitStmt(s.Body); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
	return nil
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
func (e *Emitter) emitInstanceOf(ex *ast.BinaryExpression) (Value, error) {
	rightIdent, ok := ex.Right.(*ast.Identifier)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: right-hand side of instanceof must be a class name", ex.GetPos().Line, ex.GetPos().Col)
	}
	if kindID, ok := errorKindIDs[rightIdent.Name]; ok {
		return e.emitErrorInstanceOf(ex, rightIdent.Name, kindID)
	}
	info, ok := e.classes[rightIdent.Name]
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: instanceof is only supported against user-defined classes; '%s' is not a registered class", ex.GetPos().Line, ex.GetPos().Col, rightIdent.Name)
	}

	leftVal, err := e.emitExpr(ex.Left)
	if err != nil {
		return Value{}, err
	}

	if leftVal.Ty.IsDynamic {
		tag, payload := e.emitUnboxTagPayload(leftVal)
		isObj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))

		resultAlloca := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resultAlloca))
		objL := e.freshLabel("instanceof.obj")
		notObjL := e.freshLabel("instanceof.notobj")
		mergeL := e.freshLabel("instanceof.merge")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, notObjL))

		e.emitLabel(objL)
		ptrReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", ptrReg, payload))
		tagGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", tagGep, info.Ty.StructIR(), ptrReg))
		loadedTag := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", loadedTag, tagGep))

		tagIDs := []int64{info.TagID}
		for _, d := range info.Descendants {
			tagIDs = append(tagIDs, e.classes[d].TagID)
		}
		classMatch := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", classMatch, loadedTag, tagIDs[0]))
		for _, id := range tagIDs[1:] {
			next := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", next, loadedTag, id))
			combined := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", combined, classMatch, next))
			classMatch = combined
		}
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

	if leftVal.Ty.IsClass {
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
			if leftVal.Ty.Nullable {
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
			tagIDs := []int64{targetInfo.TagID}
			for _, d := range targetInfo.Descendants {
				tagIDs = append(tagIDs, e.classes[d].TagID)
			}

			resultAlloca := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resultAlloca))
			mergeL := ""
			if leftVal.Ty.Nullable {
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
			match := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", match, loadedTag, tagIDs[0]))
			for _, id := range tagIDs[1:] {
				next := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", next, loadedTag, id))
				combined := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", combined, match, next))
				match = combined
			}

			if !leftVal.Ty.Nullable {
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

	if leftVal.Ty.IsDynamic {
		tag, payload := e.emitUnboxTagPayload(leftVal)
		isObj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))

		if kindName == "Error" {
			return Value{Ref: isObj, Ty: TypeBool}, nil
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
		kindMatch := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", kindMatch, loadedKind, kindID))
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

	if leftVal.Ty.IsError {
		if kindName == "Error" {
			return Value{Ref: "1", Ty: TypeBool}, nil
		}
		kindGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", kindGep, errorObjType.StructIR(), leftVal.Ref))
		loadedKind := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", loadedKind, kindGep))
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", result, loadedKind, kindID))
		return Value{Ref: result, Ty: TypeBool}, nil
	}

	return Value{Ref: "0", Ty: TypeBool}, nil
}
