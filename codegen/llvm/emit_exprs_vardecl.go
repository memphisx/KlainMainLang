package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// registerModuleGlobals promotes each top-level `const`/`let`/`var` of a simple
// scalar/string type to an LLVM module global (TDD-00093), so a named `function`
// declaration — emitted with its own fresh scope, unlike an arrow/closure that
// captures — can read it. Run before function bodies are emitted (they resolve
// the name through e.moduleGlobals). The global is zero-initialized; the actual
// initializer runs in `main()` at the declaration's position (emitVarDecl stores
// into the same global). Only reliably-simple types are promoted (annotated, or a
// literal initializer) — an array/object/Map/complex value stays a `main()` local
// (the pre-existing behavior), never miscompiled.
func (e *Emitter) registerModuleGlobals(prog *ast.Program) {
	promote := func(v *ast.VarDeclaration) {
		if _, exists := e.moduleGlobals[v.Name]; exists {
			return
		}
		ty, ok := e.reliableGlobalType(v)
		if !ok {
			return
		}
		// A promoted generic class instance (`const b = new Box<number>()`) needs
		// its monomorphized ClassInfo (methods, dispatch) registered now, before
		// Pass 2 emits the function bodies that will call `b.get()` — otherwise
		// the method dispatch finds no registered class. genericClassInstanceType
		// (via inferExprType above) only computed the *shape*; instantiateGeneric-
		// Class does the real, memoized registration (a no-op if the top-level
		// `new` already forced it). Field access rides the type's own Fields and
		// needs no registration, but the method side does.
		if ne, isNew := v.Init.(*ast.NewExpression); isNew {
			if genDecl, gen := e.genericClasses[ne.ClassName]; gen && len(ne.TypeArgs) == len(genDecl.TypeParams) {
				subs := e.buildTypeArgSubs(genDecl.TypeParams, ne.TypeArgs)
				_, _ = e.instantiateGenericClass(genDecl, subs)
			}
		}
		safe := llvmSafeSymbol(v.Name)
		if ty.IsArray {
			// An array binding is two slots (data ptr + length) — two globals,
			// matching the Ptr/LenPtr Named-Symbol shape emitArrayVarDecl uses.
			dataG := "@__kml_global_" + safe + "_data"
			lenG := "@__kml_global_" + safe + "_len"
			e.emitGlobal(fmt.Sprintf("%s = internal global ptr null, align 8", dataG))
			e.emitGlobal(fmt.Sprintf("%s = internal global i64 0, align 8", lenG))
			e.moduleGlobals[v.Name] = Symbol{Ptr: dataG, LenPtr: lenG, Ty: ty, IsConst: v.Kind == "const"}
		} else {
			gname := "@__kml_global_" + safe
			e.emitGlobal(fmt.Sprintf("%s = internal global %s %s, align %d", gname, ty.IR, ty.zeroLiteral(), ty.Align()))
			e.moduleGlobals[v.Name] = Symbol{Ptr: gname, Ty: ty, IsConst: v.Kind == "const"}
		}
		e.promotedGlobalDecls[v] = true
	}
	for _, stmt := range prog.Body {
		switch s := stmt.(type) {
		case *ast.VarDeclaration:
			promote(s)
		case *ast.VarDeclarationList:
			for _, d := range s.Decls {
				promote(d)
			}
		}
	}
}

// reliableGlobalType returns a top-level declaration's type only when it can be
// determined unambiguously *and* occupies a promotable shape — a simple
// scalar/string slot, or an array (Ptr/LenPtr) — from an explicit annotation
// (resolveType is authoritative) or a literal initializer whose type is fixed.
// This deliberately matches what emitVarDecl computes for the same cases, so the
// pre-declared global's IR type can never disagree with the store emitVarDecl
// later emits. Anything else (object/`Map`/`Set`/`bigint`/un-annotated call)
// returns ok=false and stays a local.
func (e *Emitter) reliableGlobalType(v *ast.VarDeclaration) (Type, bool) {
	// Map/Set: emitMapVarDecl/emitSetVarDecl derive the type from the initializer
	// (not any annotation), so compute it the same way to match their store. Both
	// are a single ptr slot. A `new Set([…])` whose element type comes from the
	// initializer array is skipped — replicating that inference risks an IR-type
	// mismatch; a `new Set<T>()`/`new Set()` is unambiguous.
	switch init := v.Init.(type) {
	case *ast.NewMapExpression:
		// A `new Map([…])` whose K/V come from the initializer entries is
		// skipped for the same reason the Set case below is: replicating that
		// inference here risks an IR-type mismatch with emitMapVarDecl's store,
		// and it must run in source order anyway so the entries source is in
		// scope. A `new Map<K,V>()`/`new Map()` is unambiguous.
		if init.Init != nil {
			return Type{}, false
		}
		keyTy := TypePtr
		valTy := TypeI64
		if init.KeyType != nil {
			keyTy = e.resolveType(init.KeyType)
		}
		if init.ValType != nil {
			valTy = e.resolveType(init.ValType)
		}
		return MapType(keyTy, valTy), true
	case *ast.NewSetExpression:
		if init.Init != nil {
			return Type{}, false
		}
		elemTy := TypePtr
		if init.ElemType != nil {
			elemTy = e.resolveType(init.ElemType)
		}
		return SetType(elemTy), true
	case *ast.NewWeakMapExpression:
		keyTy, valTy := TypePtr, TypeI64
		if init.KeyType != nil {
			keyTy = e.resolveType(init.KeyType)
		}
		if init.ValType != nil {
			valTy = e.resolveType(init.ValType)
		}
		return WeakMapType(keyTy, valTy), true
	case *ast.NewWeakSetExpression:
		elemTy := TypePtr
		if init.ElemType != nil {
			elemTy = e.resolveType(init.ElemType)
		}
		return WeakSetType(elemTy), true
	case *ast.NewWeakRefExpression:
		// The referent init runs in source order in main(); the global is a
		// single ptr slot, so its IR can't disagree with storePtrHandleVarDecl.
		referentTy := TypePtr
		if init.ElemType != nil {
			referentTy = e.resolveType(init.ElemType)
		} else if init.Init != nil {
			referentTy = e.inferExprType(init.Init)
		}
		return WeakRefType(referentTy), true
	case *ast.NewTypedArrayExpression:
		// A TypedArray is a 2-slot `{ptr,i64}` IsArray value — promoted via the
		// same two-global (data + length) path as a plain array; emitArrayVarDecl
		// already both constructs a `new Uint8Array(...)` and honors the promoted
		// globals. Type matches emitVarDecl's own `TypedArrayType(init.ElemKind)`.
		return TypedArrayType(init.ElemKind), true
	}
	if v.TypeAnnot != nil {
		ty := e.resolveType(v.TypeAnnot)
		if ty.IsArray && ty.ElemType != nil {
			return ty, true
		}
		// A fixed-shape object (or tuple) is a single ptr slot, stored the same way
		// as a string; its field layout rides on the Symbol's Ty, not the global's
		// IR. A dynamic (`any`) object is excluded — it isn't a fixed slot.
		if ty.IsObject && !ty.IsDynamicObject {
			return ty, true
		}
		if isSimpleGlobalType(ty) {
			return ty, true
		}
		return Type{}, false
	}
	// A `new`-expression that yields a single ptr-sized handle (a class instance,
	// or a builtin like Blob/Date/URL/RegExp/EventEmitter/…). Its type comes from
	// the canonical inferExprType, and because every such handle's global IR is
	// uniformly `ptr`, the pre-declared global can't disagree with emitVarDecl's
	// store. Generic class instances and Promise are excluded (see
	// promotableNewExpr), and the single-ptr gate there excludes 2-slot
	// TypedArray/array shapes.
	if ty, ok := e.promotableNewExpr(v.Init); ok {
		return ty, true
	}
	// Un-annotated: promote only when the type is fully determinable in the
	// pre-pass context (see promotableInitInPrePass), then compute it the *same
	// way* emitVarDecl does — using the same lookup/resolveFuncRef/inferX helpers
	// in the same source-ordered context — so the pre-declared global's IR type
	// can never disagree with the store emitVarDecl later emits.
	if !e.promotableInitInPrePass(v.Init) {
		return Type{}, false
	}
	switch init := v.Init.(type) {
	case *ast.NumberLiteral:
		if strings.ContainsRune(init.Value, '.') {
			return TypeF64, true
		}
		return TypeI64, true
	case *ast.StringLiteral, *ast.TemplateLiteral:
		return TypePtr, true // a string is a plain ptr slot
	case *ast.BooleanLiteral:
		return TypeBool, true
	case *ast.Identifier:
		// Guaranteed an earlier module global; its type is exactly what
		// emitVarDecl's Identifier case reads via the same lookup.
		return e.moduleGlobals[init.Name].Ty, true
	case *ast.CallExpression:
		// A named non-generic non-async function returning a composite — the call
		// shape emitVarDecl sets ty = sig.RetType for.
		if id, ok := init.Callee.(*ast.Identifier); ok {
			if _, sig, found := e.resolveFuncRef(id.Name); found {
				return sig.RetType, true
			}
		}
		return Type{}, false
	case *ast.ArrayLiteral:
		ty := e.inferArrayType(init)
		if ty.IsArray && ty.ElemType != nil {
			return ty, true
		}
	case *ast.ObjectLiteral:
		ty := e.inferObjectType(init)
		if ty.IsObject && !ty.IsDynamicObject {
			return ty, true
		}
	}
	return Type{}, false
}

// promotableInitInPrePass reports whether an un-annotated initializer's type is
// fully determinable in registerModuleGlobals' context. That pass runs before
// main()'s body, so only things resolvable then may appear: literals, an *earlier
// module global* (registered in source order, so visible via lookup exactly as in
// emitVarDecl), and a named non-generic non-async function's composite return
// type. An identifier bound to a runtime local (a destructuring target, a block
// local), a spread, a computed key, a generic/async call, or a scalar-returning
// call (which hits emitVarDecl's i64 default) would let the pre-pass type diverge
// from emitVarDecl's — so it is not promoted. This is the invariant the whole
// feature rests on ("promote iff the type cannot drift"), not a per-symptom gate.
func (e *Emitter) promotableInitInPrePass(expr ast.Expression) bool {
	// A promotable `new`-expression handle (class instance / Blob / Date / …) is
	// fully determinable here: classes are registered (Pass 0.5) before this
	// pre-pass (Pass 1.7), and a builtin handle's type is a constant.
	if _, ok := e.promotableNewExpr(expr); ok {
		return true
	}
	switch ex := expr.(type) {
	case *ast.NumberLiteral:
		return !ex.IsBigInt
	case *ast.StringLiteral, *ast.BooleanLiteral, *ast.NullLiteral, *ast.TemplateLiteral:
		return true
	case *ast.Identifier:
		_, ok := e.moduleGlobals[ex.Name]
		return ok
	case *ast.CallExpression:
		id, ok := ex.Callee.(*ast.Identifier)
		if !ok {
			return false
		}
		if _, isGeneric := e.genericFuncs[id.Name]; isGeneric {
			return false
		}
		_, sig, found := e.resolveFuncRef(id.Name)
		if !found || sig.MaySuspend {
			return false
		}
		rt := sig.RetType
		return rt.IsArray || rt.IsObject || rt.IsMap || rt.IsSet || rt.IsDate || rt.IsFunc || isStringTy(rt)
	case *ast.ArrayLiteral:
		for _, el := range ex.Elements {
			if _, isSpread := el.(*ast.SpreadElement); isSpread {
				return false
			}
			if !e.promotableInitInPrePass(el) {
				return false
			}
		}
		return true
	case *ast.ObjectLiteral:
		for _, prop := range ex.Properties {
			// A computed key, or a spread (Key=="" with a SpreadElement value),
			// depends on scope.
			if prop.KeyExpr != nil || prop.Key == "" {
				return false
			}
			if !e.promotableInitInPrePass(prop.Value) {
				return false
			}
		}
		return true
	}
	return false
}

// promotableNewExpr returns the type of a top-level binding initialized by a
// `new`-expression that is promotable to a module global (TDD-00093): a class
// instance (concrete or a fully-type-argumented generic, `new Box<number>()`),
// or a single-ptr-slot builtin handle (Blob/Date/URL/RegExp/EventEmitter/
// Headers/…). It returns ok=false for anything not a single fixed slot (arrays
// and TypedArrays are 2-slot `{ptr,i64}`), for a dynamic/`any` object, and — as
// an exclusion — for `new Promise` (task-promise semantics). inferExprType is the
// canonical type source (it computes a generic instantiation's shape purely);
// registerModuleGlobals forces the generic class's real registration so a named
// function's method dispatch finds it.
func (e *Emitter) promotableNewExpr(init ast.Expression) (Type, bool) {
	switch ne := init.(type) {
	case *ast.NewExpression:
		if ne.ClassName == "Promise" {
			return Type{}, false
		}
		_, concrete := e.classes[ne.ClassName]
		genDecl, generic := e.genericClasses[ne.ClassName]
		if !concrete && !generic {
			return Type{}, false
		}
		// A generic class instance (`new Box<number>()`) promotes too: its shape
		// comes from genericClassInstanceType via inferExprType below — a *pure*
		// lookup (no IR emission / no e.classes registration as a side effect),
		// safe in this pre-pass. Only a full type-argument list is resolvable.
		if generic && len(ne.TypeArgs) != len(genDecl.TypeParams) {
			return Type{}, false
		}
	default:
		if !isHandleNewExpr(init) {
			return Type{}, false
		}
	}
	ty := e.inferExprType(init)
	// A single fixed slot only: exclude a 2-slot `{ptr,i64}` array/TypedArray
	// aggregate and a dynamic/`any` object. A ptr handle (Blob/URL/class/…) or a
	// scalar-backed handle (a `Date` is an i64 epoch) both store into one global
	// via the shape-appropriate path emitVarDecl already dispatches on.
	if ty.IsDynamicObject || ty.IsArray || ty.IR == "" || ty.IR == "void" {
		return Type{}, false
	}
	return ty, true
}

// isHandleNewExpr reports whether init is one of the builtin `new`-handle
// constructors this promotion covers — the plain value/data handles verified
// (targeted probes + a full E2E-suite pass) to construct and dispatch correctly
// as a module global. Class instances (`new C()`) are handled separately in
// promotableNewExpr and ride the ordinary class object-var path.
//
// Deliberately excluded: the connection handles — `Worker`/`WebSocket`/
// `EventSource` (their construction opens a thread/socket, ill-suited to a
// module-global-at-startup), `BroadcastChannel`/`MessageChannel`, and
// `XMLHttpRequest`. They stay `main()` locals (the prior behavior).
//
// (Two families used to be excluded and are now in: the event handles
// `AbortController`/`Event`/`CustomEvent`/`EventTarget` failed only because
// `inferExprType` lacked their `new`-expression cases and mistyped the global;
// and the streams + `EventEmitter` use dedicated var-decl emitters that ignored
// the module global — `storePtrHandleVarDecl` makes those promotion-aware. Both
// fixes are at the source, so these promote correctly.)
func isHandleNewExpr(init ast.Expression) bool {
	switch init.(type) {
	case *ast.NewBlobExpression, *ast.NewDateExpression,
		*ast.NewErrorExpression, *ast.NewURLExpression,
		*ast.NewURLSearchParamsExpression, *ast.NewRegExpExpression,
		*ast.NewHeadersExpression, *ast.NewArrayBufferExpression,
		*ast.NewTextEncoderExpression, *ast.NewTextDecoderExpression,
		*ast.NewDataViewExpression, *ast.NewRequestExpression,
		*ast.NewURLPatternExpression, *ast.NewAbortControllerExpression,
		*ast.NewEventTargetExpression, *ast.NewEventExpression,
		*ast.NewCustomEventExpression,
		*ast.NewReadableStreamExpression, *ast.NewWritableStreamExpression,
		*ast.NewTransformStreamExpression, *ast.NewCompressionStreamExpression,
		*ast.NewNodeStreamExpression, *ast.NewEventEmitterExpression:
		return true
	}
	return false
}

// moduleGlobalPtrOrLocal returns the storage pointer for a single-ptr binding v
// (object/`Map`/`Set`): the pre-registered module global (TDD-00093) for a
// promoted decl — already in e.moduleGlobals and zero-initialized — or a fresh
// local ptr alloca with the Symbol defined, the prior behavior.
func (e *Emitter) moduleGlobalPtrOrLocal(v *ast.VarDeclaration, ty Type) string {
	if e.promotedGlobalDecls[v] {
		return e.moduleGlobals[v.Name].Ptr
	}
	ptrName := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrName))
	e.define(v.Name, Symbol{Ptr: ptrName, Ty: ty, IsConst: v.Kind == "const"})
	return ptrName
}

// isSimpleGlobalType reports whether a type occupies a single scalar/string slot —
// the only shapes registerModuleGlobals promotes, since they reach emitVarDecl's
// plain alloca path (never the array/object/Map/nullable-scalar sub-emitters).
func isSimpleGlobalType(ty Type) bool {
	if ty.IsArray || ty.IsObject || ty.IsDynamicObject || ty.IsMap || ty.IsSet || ty.IsDynamic || ty.IsFunc || ty.IsBigInt || isNullableScalar(ty) {
		return false
	}
	switch ty.IR {
	case "i1", "i8", "i16", "i32", "i64", "float", "double":
		return true
	}
	return isStringTy(ty)
}

// emitVarSlotDefault pre-initializes a scalar or dynamic `var` slot in the
// entry block to a deterministic default (an any-typed var to the `undefined`
// box `{ i8 5, i64 0 }`, a typed scalar to its zero literal). Only scalar and
// dynamic types reach this — array/object/map/set var declarations are handled
// by their own sub-emitters before the scalar path. The store is a compile-time
// constant, so it is valid to place in the entry (alloca) block. See TDD-00070.
func (e *Emitter) emitVarSlotDefault(ptrName string, ty Type) {
	if ty.IsDynamic {
		e.emitAlloca(fmt.Sprintf("store %s { i8 %d, i64 0 }, ptr %s, align %d", ty.IR, kmlTagUndefined, ptrName, ty.Align()))
		return
	}
	e.emitAlloca(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, ty.zeroLiteral(), ptrName, ty.Align()))
}

// emitVarDecl handles variable declarations (scalar, array, and object).
// storePtrHandleVarDecl stores a just-built single-ptr handle value (a
// stream/CompressionStream/… whose var-decl is emitted by a dedicated
// emitNewX rather than the generic object path) into v's storage: the
// pre-registered module global when v is promoted (TDD-00093/ADR-00342), else a
// fresh local ptr alloca with the Symbol defined. Without the promoted branch a
// promoted binding's global stayed null and a named function reading it saw an
// empty handle (a promoted `ReadableStream` silently dropped its chunks).
func (e *Emitter) storePtrHandleVarDecl(v *ast.VarDeclaration, val Value) {
	var ptrName string
	if e.promotedGlobalDecls[v] {
		ptrName = e.moduleGlobals[v.Name].Ptr
	} else {
		ptrName = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrName))
		e.define(v.Name, Symbol{Ptr: ptrName, Ty: val.Ty, IsConst: v.Kind == "const"})
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, ptrName))
}

func (e *Emitter) emitVarDecl(v *ast.VarDeclaration) error {
	if init, ok := v.Init.(*ast.NewMapExpression); ok {
		return e.emitMapVarDecl(v, init)
	}
	if init, ok := v.Init.(*ast.NewSetExpression); ok {
		return e.emitSetVarDecl(v, init)
	}
	if init, ok := v.Init.(*ast.NewEventEmitterExpression); ok {
		return e.emitEventEmitterVarDecl(v, init)
	}
	if init, ok := v.Init.(*ast.NewWeakMapExpression); ok {
		val, err := e.emitNewWeakMapValue(init)
		if err != nil {
			return err
		}
		e.storePtrHandleVarDecl(v, val)
		return nil
	}
	if init, ok := v.Init.(*ast.NewWeakSetExpression); ok {
		val, err := e.emitNewWeakSetValue(init)
		if err != nil {
			return err
		}
		e.storePtrHandleVarDecl(v, val)
		return nil
	}
	if init, ok := v.Init.(*ast.NewWeakRefExpression); ok {
		val, err := e.emitNewWeakRefValue(init)
		if err != nil {
			return err
		}
		e.storePtrHandleVarDecl(v, val)
		return nil
	}
	if init, ok := v.Init.(*ast.NewNodeStreamExpression); ok {
		val, err := e.emitNewNodeStream(init)
		if err != nil {
			return err
		}
		e.storePtrHandleVarDecl(v, val)
		return nil
	}
	if init, ok := v.Init.(*ast.NewCompressionStreamExpression); ok {
		val, err := e.emitNewCompressionStream(init)
		if err != nil {
			return err
		}
		e.storePtrHandleVarDecl(v, val)
		return nil
	}
	if init, ok := v.Init.(*ast.NewTransformStreamExpression); ok {
		val, err := e.emitNewTransformStream(init)
		if err != nil {
			return err
		}
		e.storePtrHandleVarDecl(v, val)
		return nil
	}
	if init, ok := v.Init.(*ast.NewWritableStreamExpression); ok {
		val, err := e.emitNewWritableStream(init)
		if err != nil {
			return err
		}
		e.storePtrHandleVarDecl(v, val)
		return nil
	}
	if init, ok := v.Init.(*ast.NewReadableStreamExpression); ok {
		val, err := e.emitNewReadableStream(init)
		if err != nil {
			return err
		}
		e.storePtrHandleVarDecl(v, val)
		return nil
	}

	ty := e.resolveType(v.TypeAnnot)

	// Infer type from init when no annotation.
	if !ty.IsArray && !ty.IsObject && v.TypeAnnot == nil {
		switch init := v.Init.(type) {
		case *ast.NullLiteral:
			if init.IsUndefined {
				ty = TypeUndefined
			} else {
				ty = TypeNull
			}
		case *ast.BooleanLiteral:
			// Without this case an unannotated `let b = true` fell through to
			// the switch's blind TypeI64 default, so the boolean stored as i64
			// and printed as `1`/`0` instead of `true`/`false` (a comparison- or
			// `!`-valued initializer already infers bool via the BinaryExpression
			// /UnaryExpression cases — only a bare boolean literal was missed).
			ty = TypeBool
		case *ast.StringLiteral:
			ty = TypePtr
		case *ast.TemplateLiteral:
			ty = TypePtr
		case *ast.Identifier:
			if sym, ok := e.lookup(init.Name); ok {
				ty = sym.Ty
			} else if _, sig, found := e.resolveFuncRef(init.Name); found {
				// A named function taken by value (`const g = f`) is a closure
				// value — a ptr — so the slot must be sized as one.
				ty = funcTypeFromSig(sig)
			} else {
				switch init.Name {
				case "NaN", "Infinity":
					ty = TypeF64
				}
			}
		case *ast.IndexExpression:
			ty = e.inferExprType(init)
		case *ast.BinaryExpression:
			ty = e.inferExprType(init)
		case *ast.UnaryExpression:
			// `!x` is bool, unary `-`/`+` preserve the operand's numeric type —
			// without this an unannotated `let n = !cond` fell through to the
			// TypeI64 default and printed `0`/`1` instead of `false`/`true`.
			ty = e.inferExprType(init)
		case *ast.SequenceExpression:
			// The comma operator's value is its last operand's — inferExprType
			// handles that; without this case the switch's default left `ty` at
			// its number default, so a sequence whose last operand was a
			// non-number (e.g. `const s = (log(), "x")`) allocated an i64 slot
			// and then stored a pointer into it (invalid IR).
			ty = e.inferExprType(init)
		case *ast.ArrayLiteral:
			ty = e.inferArrayType(init)
		case *ast.ObjectLiteral:
			ty = e.inferObjectType(init)
		case *ast.NewErrorExpression:
			ty = errorObjType
		case *ast.NewDateExpression:
			ty = TypeDate
		case *ast.NewURLExpression:
			ty = URLType()
		case *ast.NewURLSearchParamsExpression:
			ty = URLSearchParamsType()
		case *ast.NewURLPatternExpression:
			ty = URLPatternType()
		case *ast.NewArrayBufferExpression:
			ty = ArrayBufferType()
			if init.Shared {
				ty = SharedArrayBufferType()
			}
		case *ast.NewBroadcastChannelExpression:
			ty = BroadcastChannelType(init.Name)
		case *ast.NewMessageChannelExpression:
			if init.TypeArg != nil {
				ty = MessageChannelType(e.resolveType(init.TypeArg))
			} else {
				ty = MessageChannelType(TypeI64)
			}
		case *ast.NewDataViewExpression:
			ty = DataViewType()
		case *ast.NewBlobExpression:
			ty = BlobType()
			if gen := e.blobShadowedByClass(init); gen != nil {
				ty = e.inferExprType(gen)
			}
		case *ast.NewTypedArrayExpression:
			ty = TypedArrayType(init.ElemKind)
		case *ast.NewTextEncoderExpression:
			ty = TextEncoderType()
		case *ast.NewTextDecoderExpression:
			ty = TextDecoderType()
		case *ast.NewRegExpExpression:
			ty = RegExpType()
		case *ast.NewEventSourceExpression:
			ty = EventSourceType()
		case *ast.NewEventTargetExpression:
			ty = EventTargetType()
		case *ast.NewAbortControllerExpression:
			ty = AbortControllerType()
		case *ast.NewEventExpression:
			ty = EventType()
		case *ast.NewCustomEventExpression:
			detailTy := TypePtr
			if init.Detail != nil {
				detailTy = e.inferExprType(init.Detail)
			}
			ty = CustomEventType(detailTy)
		case *ast.NewWebSocketExpression:
			ty = WebSocketClientType()
		case *ast.NewWorkerExpression:
			ty = WorkerType(init.ResolvedPath)
		case *ast.NewHeadersExpression:
			ty = HeadersType()
		case *ast.NewRequestExpression:
			ty = FetchRequestType()
		case *ast.NewXMLHttpRequestExpression:
			ty = XMLHttpRequestType()
		case *ast.AwaitExpression:
			ty = e.inferExprType(init)
		case *ast.ArrowFunction:
			ty = e.inferExprType(init)
		case *ast.FunctionExpression:
			ty = e.inferExprType(init)
		case *ast.MemberExpression:
			ty = e.inferExprType(init)
		case *ast.TaggedTemplateExpression:
			ty = e.inferExprType(init)
		case *ast.CallExpression:
			// Generator construction (TDD-00061/ADR-00172): `gen(args)`'s
			// own type is the constructed instance's GenTy — checked before
			// the callee-name switch below, which has no case for it
			// (GenTy deliberately isn't IsObject/IsArray/IsFunc/etc., same
			// reasoning IsEventEmitter's own type doesn't trigger any of
			// this switch's other type-preserving branches either, so
			// without this check `ty` would silently stay this whole
			// switch's blind TypeI64 default — confirmed directly: without
			// this case, `const g = gen();` allocated an i64-sized slot for
			// what emitExpr(v.Init) actually produces as a ptr-typed
			// generator instance, a hard clang-stage type mismatch, not
			// just a wrong result).
			if id, ok := init.Callee.(*ast.Identifier); ok {
				if info, found := e.lookupGenerator(id.Name); found {
					ty = info.GenTy
				}
			}
			if callee, ok := init.Callee.(*ast.Identifier); ok {
				switch callee.Name {
				case "fetch":
					ty = PromiseOf(ResponseType())
				case "btoa", "atob", "encodeURIComponent", "decodeURIComponent", "encodeURI", "decodeURI":
					ty = TypePtr
				case "String":
					ty = TypePtr
				case "Number", "parseInt", "parseFloat":
					// Number/parseInt/parseFloat can produce NaN, so their
					// results are doubles unless Number's input is already
					// integral — must match inferExprType's own cases (which
					// this pre-inference switch would otherwise shadow with
					// its blind i64 default, truncating a NaN to 0).
					ty = e.inferExprType(init)
				case "Boolean":
					ty = TypeBool
				case "structuredClone":
					if len(init.Args) == 1 {
						ty = e.inferExprType(init.Args[0])
					}
				case "Symbol":
					ty = SymbolType()
				case "BigInt":
					ty = BigIntType()
				default:
					// A plain `ptr`-shaped return (a string — see isStringTy)
					// was missing from this condition entirely alongside
					// array/object/func/date/map/set: since ty otherwise stays
					// this switch's TypeI64 default, a string-returning
					// function assigned to an unannotated const/let allocated
					// an i64-sized slot for what emitExpr(v.Init) then
					// actually produces as a ptr value — a real, pre-existing,
					// unrelated bug (confirmed with a plain, non-generic
					// function too) found while wiring TDD-00010 V1's
					// generic-function call support, whose most natural usage
					// (`const y = identity("hi")`) hit this exact gap.
					if _, sig, found := e.resolveFuncRef(callee.Name); found && (sig.RetType.IsArray || sig.RetType.IsObject || sig.RetType.IsFunc || sig.RetType.IsDate || sig.RetType.IsMap || sig.RetType.IsSet || sig.RetType.IsDynamic || isStringTy(sig.RetType)) {
						ty = sig.RetType
					} else if sym, found := e.lookup(callee.Name); found && sym.Ty.IsFunc && sym.Ty.FuncRetType != nil {
						// Calling a closure-typed variable (e.g. a const-bound
						// arrow function) rather than a named declaration —
						// same fallback as inferExprType's CallExpression case.
						retTy := *sym.Ty.FuncRetType
						if retTy.IsArray || retTy.IsObject || retTy.IsFunc || retTy.IsDate || retTy.IsMap || retTy.IsSet || isStringTy(retTy) {
							ty = retTy
						}
					} else if genDecl, found := e.genericFuncs[callee.Name]; found {
						// Generic function (TDD-00010 V1): infer purely, same
						// reasoning as the NewExpression branch below —
						// emitVarDecl's pre-inference switch must not trigger
						// real emission as a side effect.
						if retTy, ok := e.genericCallReturnType(genDecl, init.Args); ok {
							ty = retTy
						}
					}
				}
			}
			// Built-in methods that return arrays with the same element type.
			if mem, ok := init.Callee.(*ast.MemberExpression); ok {
				switch mem.Property {
				case "splice":
					// Any mutable receiver shape (variable, object/class
					// field, nested-array element) — the same coverage
					// emitSplice itself accepts via resolveArrayMutLoc.
					if recvTy := e.inferExprType(mem.Object); recvTy.IsArray {
						ty = recvTy
					}
				case "pop", "shift":
					if recvTy := e.inferExprType(mem.Object); recvTy.IsArray && recvTy.ElemType != nil {
						ty = *recvTy.ElemType
					}
				default:
					inferred := e.inferExprType(init)
					if inferred.IR != TypeI64.IR || inferred.IsArray || inferred.IsObject {
						ty = inferred
					}
				}
			}
			// A may-suspend async fn returns a task promise (TDD-00083 Stage 2):
			// a ptr with PromiseTask so a later `await` on this variable takes the
			// task path. Applied last so the string/ptr default above (which drops
			// PromiseTask) can't overwrite it.
			if id, ok := init.Callee.(*ast.Identifier); ok {
				if _, sig, found := e.resolveFuncRef(id.Name); found && sig.MaySuspend && sig.RetType.IsPromise {
					ty = e.inferExprType(init)
				}
			}
		case *ast.NewArrayExpression:
			if init.ElemType != nil {
				ty = ArrayOf(e.resolveType(init.ElemType))
			}
		case *ast.NewExpression:
			if init.ClassName == "Promise" {
				// new Promise<T>(executor) → task Promise<T> (default number) — TDD-00087.
				valTy := TypeI64
				if len(init.TypeArgs) == 1 {
					valTy = e.resolveType(init.TypeArgs[0])
				}
				pt := PromiseOf(valTy)
				pt.PromiseTask = true
				ty = pt
			} else if info, ok := e.classes[init.ClassName]; ok {
				ty = info.Ty
			} else if genDecl, ok := e.genericClasses[init.ClassName]; ok && len(init.TypeArgs) == len(genDecl.TypeParams) {
				// Pure lookup only (see genericClassInstanceType's doc
				// comment) — the real, memoized instantiation still happens
				// exactly once, from emitExpr(v.Init) below via
				// emitNewExpression.
				subs := e.buildTypeArgSubs(genDecl.TypeParams, init.TypeArgs)
				if instTy, err := e.genericClassInstanceType(genDecl, subs); err == nil {
					ty = instTy
				}
			}
		}
	}
	if _, ok := v.Init.(*ast.NewArrayExpression); ok && !ty.IsArray {
		return fmt.Errorf("%d:%d: new Array() requires a type annotation or a type parameter, e.g. new Array<number>(n)", v.GetPos().Line, v.GetPos().Col)
	}

	if containsDynamicElement(ty) {
		return fmt.Errorf("%d:%d: any/unknown is not yet supported as an array element or object field type", v.GetPos().Line, v.GetPos().Col)
	}
	if err := validateCompositeType(ty, v.GetPos().Line, v.GetPos().Col); err != nil {
		return err
	}
	if ty.UnionMembers != nil && !ty.Nullable && v.Init == nil {
		return fmt.Errorf("%d:%d: a union type without null/undefined as a member requires an initializer", v.GetPos().Line, v.GetPos().Col)
	}
	if ty.IsArray {
		return e.emitArrayVarDecl(v, ty)
	}
	if ty.IsObject || ty.IsDynamicObject {
		return e.emitObjectVarDecl(v, ty)
	}

	// With no explicit type, a numeric-literal initializer refines the i64
	// default: a `123n` BigInt literal → bigint, a `3.14` float literal → f64.
	if v.TypeAnnot == nil {
		if nl, ok := v.Init.(*ast.NumberLiteral); ok {
			switch {
			case nl.IsBigInt:
				ty = BigIntType()
			case strings.ContainsRune(nl.Value, '.'):
				ty = TypeF64
			}
		}
	}

	// A nullable non-pointer scalar (`number | null`, `boolean | null`, ...)
	// gets a presence-flagged { i1, T } slot rather than a bare scalar — see
	// emit_nullable_scalar.go / TDD-00064.
	if isNullableScalar(ty) {
		return e.emitNullableScalarVarDecl(v, ty)
	}

	// Module-global promotion (TDD-00093): at module scope, a top-level
	// const/let/var of a simple scalar/string type is backed by a pre-registered
	// LLVM global (registerModuleGlobals) so a named `function` can read it. Store
	// into that global rather than a fresh local alloca; it's already zero-init'd
	// (the global's own initializer) and already in e.moduleGlobals (so lookup
	// resolves it), so neither the default-init nor a local define is needed.
	var ptrName string
	if e.promotedGlobalDecls[v] {
		ptrName = e.moduleGlobals[v.Name].Ptr
	}
	if ptrName == "" {
		ptrName = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", ptrName, ty.IR, ty.Align()))
		if v.Kind == "var" || v.Init == nil {
			// Pre-initialize the slot in the entry block to a deterministic default
			// (`undefined` for an any-typed slot, zero for a typed scalar) whenever a
			// read could reach it before a store:
			//   - a `var` is function-scoped (promoteVarToFuncScope), so its slot can
			//     be read on a path where its own initializer never ran (e.g.
			//     `if (c) { var r = 1 } use(r)` with c false); and
			//   - a `let`/`const` with no initializer (`let x: number;`) can be read
			//     on a path the definite-assignment analysis soundly misses (a var
			//     assigned only inside a maybe-skipped loop, ADR-00214). Without this
			//     that read would be uninitialized memory rather than a defined value.
			// A read textually *before* the declaration is still a clean
			// undefined-variable/TDZ error, since the binding isn't visible until its
			// declaration point. See TDD-00070/TDD-00071.
			e.emitVarSlotDefault(ptrName, ty)
		}
		e.define(v.Name, Symbol{Ptr: ptrName, Ty: ty, IsConst: v.Kind == "const"})
	}

	if v.Init != nil {
		// JSON.parse / Response.json() (optionally awaited) need the target type
		// for correct deserialization — shared with the object/array var-decl
		// emitters via emitDeclJSONProjection (emit_objects.go).
		if val, ok, err := e.emitDeclJSONProjection(v.Init, ty); ok {
			if err != nil {
				return err
			}
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, val.Ref, ptrName, ty.Align()))
			return nil
		}
		// Mark this binding as mid-initialization so a closure emitted inside its
		// own initializer that captures it (`const s = f(() => use(s))`) seeds its
		// capture cell with a default rather than by loading the still-unwritten
		// slot — see promoteCaptureToCell. Restored (not just deleted) so a nested
		// same-named binding in an outer initializer is unaffected.
		wasInitializing := e.varsBeingInitialized[v.Name]
		e.varsBeingInitialized[v.Name] = true
		val, err := e.emitExprWithObjectHint(v.Init, ty)
		e.varsBeingInitialized[v.Name] = wasInitializing
		if err != nil {
			return err
		}
		if ty.IsDynamic {
			if ty.UnionMembers != nil && !unionAllowsAssignmentFrom(ty, val.Ty) {
				return fmt.Errorf("%d:%d: value's type is not a member of the declared union type", v.GetPos().Line, v.GetPos().Col)
			}
			val, err = e.emitBoxValue(val)
			if err != nil {
				return err
			}
		} else {
			val = e.coerce(val, ty)
		}
		// Re-resolve the variable's current storage location rather than
		// trusting ptrName (captured above, before the initializer ran):
		// if evaluating the initializer itself created a closure that
		// captures this same variable — e.g. the self-cancelling-timer
		// idiom `const id = setInterval(() => { ...; clearInterval(id) },
		// ms)` — ADR-00001's capture-time promotion (boxing) already moved
		// v.Name from ptrName to a new shared heap cell via
		// updateSymbolInPlace. Storing into the now-stale ptrName in that
		// case would silently write the real value nowhere anyone (least
		// of all the closure itself) still reads from.
		finalPtr := ptrName
		if sym, ok := e.lookup(v.Name); ok {
			finalPtr = sym.Ptr
		}
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, val.Ref, finalPtr, ty.Align()))
	} else if ty.IsDynamic {
		// No initializer: any/unknown default to undefined (matching JS `let x: any;`
		// -> x === undefined), rather than leaving the tag byte as uninitialized
		// garbage, which would drive real runtime branching in print/typeof/equality.
		undef, err := e.emitBoxValue(Value{Ty: TypeUndefined})
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, undef.Ref, ptrName, ty.Align()))
	}
	return nil
}
