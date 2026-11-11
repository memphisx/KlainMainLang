// emit_stackalloc.go — TDD-00134 Stage 1 (`-optimize-memory`): escape
// analysis → stack allocation, plus the FinalizationRegistry name collection
// that powers TDD-00163 Stage 5's register-target exemption in the free
// planner.
//
// Stack allocation reuses the TDD-00173 escape checker verbatim: the flow
// condition proving "safe to free at block exit" (no alias outlives the
// block) is exactly the condition proving "safe to place on the stack". A
// planned declaration's object literal is emitted into an entry-block
// `alloca` (zero-stored at the literal site, so a loop iteration reusing the
// slot re-zeroes it — matching calloc's absent-optional-field guarantee)
// instead of `calloc`; a planned closure literal's 16-byte {fn,env} header
// and env struct become allocas the same way (ADR-00704 — the shared
// capture cells stay heap regardless, since another capturer may escape).
// Candidacy (Stage 1 complete): implicit let/const bindings whose
// initializer is a plain fixed-shape object literal, a non-async closure
// literal, a tuple-annotated array literal, or a `new` of a class whose
// constructor/methods provably never leak `this` (classStackEligible below,
// ADR-00705). Explicit @free/@owned declarations keep their heap+free
// meaning; Map/Set (runtime-created) and plain arrays' growable data
// buffers stay heap — Stage 2 territory, see the TDD.
//
// Mode interactions: under `-mm=auto` a stack-planned declaration is skipped
// by maybeRegisterAutoFree (there is no heap allocation to free); under
// `-mm=gc` a stack value is simply never handed to the collector (Boehm
// scans the stack anyway). A registered FinalizationRegistry target never
// stack-allocates: the stack planner runs without the register-target
// exemption, so `reg.register(x, …)` keeps x on the heap, where its
// (auto-)free is the registry's death signal.
package llvm

import (
	"KlainMainLang/memplan"
	"fmt"
	"reflect"

	"KlainMainLang/ast"
)

// structAlloc allocates a fixed-shape struct value (object literal, tuple
// literal, class instance): an entry-block alloca + at-site zero store when
// lit is the currently stack-planned initializer (consuming the marker), a
// zeroing calloc otherwise. The at-site zero store is what preserves
// calloc's "unstored slots read back 0" guarantee across loop-iteration
// slot reuse.
func (e *Emitter) structAlloc(lit ast.Expression, ty Type) (dataReg string) {
	structIR := ty.StructIR()
	if e.pendingStackAllocLit != nil && e.pendingStackAllocLit == lit {
		e.pendingStackAllocLit = nil
		if e.stackAllocatedLits == nil {
			e.stackAllocatedLits = map[ast.Expression]bool{}
		}
		e.stackAllocatedLits[lit] = true
		dataReg = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", dataReg, structIR))
		e.emitInstr(fmt.Sprintf("store %s zeroinitializer, "+irPtr+" %s, align 8", structIR, dataReg))
		e.emitStoreObjHeader(dataReg, ty)
		return dataReg
	}
	return e.emitObjAlloc(ty)
}

// emitObjAlloc is the one heap allocation of an object layout (TDD-00230
// P3.3a): zeroed storage for ty with its header word stored.
func (e *Emitter) emitObjAlloc(ty Type) string {
	if hostPrefixedObject(ty) {
		dataReg := e.freshReg()
		e.emitObjAllocInto(dataReg, ty)
		return dataReg
	}
	dataReg := e.freshReg()
	e.emitObjBlock(dataReg, ty, true)
	e.emitStoreObjHeader(dataReg, ty)
	return dataReg
}

// closureAllocs returns the allocation instructions' registers for a closure
// literal's 16-byte {fn,env} header of code fn and (if caps > 0) its env struct —
// entry-block allocas when lit is the currently stack-planned initializer
// (TDD-00134 Stage 1, consuming the marker), plain mallocs otherwise. The
// shared capture cells are unaffected either way: they stay heap, shared by
// pointer with the enclosing scope.
func (e *Emitter) closureAllocs(lit ast.Expression, envIR string, envSize int64, fn string) (hdr, env string) {
	if e.pendingStackAllocLit != nil && e.pendingStackAllocLit == lit {
		e.pendingStackAllocLit = nil
		if e.stackAllocatedLits == nil {
			e.stackAllocatedLits = map[ast.Expression]bool{}
		}
		e.stackAllocatedLits[lit] = true
		// The prefix word too: a function value's name and length read it.
		cell := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca { i64, "+irPtr+", "+irPtr+" }, align 8", cell))
		e.emitInstr(fmt.Sprintf("store i64 %d, "+irPtr+" %s, align 8", e.fnWord(fn), cell))
		hdr = e.freshReg()
		e.emitBodyAddr(hdr, cell)
		if envSize > 0 {
			env = e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", env, envIR))
		}
		return hdr, env
	}
	hdr = e.freshReg()
	e.emitClosureAlloc(hdr, "16", fn)
	if envSize > 0 {
		env = e.freshReg()
		e.heapMallocInto(env, allocClosure, fmt.Sprint(envSize))
	}
	return hdr, env
}

// planStackAllocs records, per candidate declaration, whether its flow
// provably keeps the value block-local — the -optimize-memory analog of
// the free plan, writing to its own plan.
func (e *Emitter) planStackAllocs(prog *ast.Program) error {
	if !e.opts.OptimizeMemory {
		return nil
	}
	plan, err := memplan.Stack(prog)
	if err != nil {
		return fmt.Errorf("internal: %v", err)
	}
	e.stackAllocPlan = plan
	return nil
}

// classStackEligible reports whether instances of className may be
// stack-allocated when their binding's flow is non-escaping: the class (and
// its effective method table, which already includes the base chain and
// spliced field initializers) must provably never leak `this`. Memoized —
// the verdict is per-class, not per-site.
func (e *Emitter) classStackEligible(className string, info ClassInfo) bool {
	if v, ok := e.classStackAudit[className]; ok {
		return v
	}
	ok := e.classStackAuditRun(className, info)
	if e.classStackAudit == nil {
		e.classStackAudit = map[string]bool{}
	}
	e.classStackAudit[className] = ok
	return ok
}

func (e *Emitter) classStackAuditRun(className string, info ClassInfo) bool {
	// A decorated method's replacement slot may hold a wrapper that retains
	// its receiver, which the audit below cannot see.
	if len(e.decoratedMethodSlots[className]) > 0 {
		return false
	}
	methodNames := map[string]bool{}
	for name := range info.Methods {
		methodNames[name] = true
	}
	if info.Constructor != nil && thisEscapesFn(info.Constructor, methodNames) {
		return false
	}
	// A subclass constructor runs its ancestors' constructors via super(...):
	// their bodies see the same `this`, so they are audited too (against the
	// leaf's effective method table — a this.m() in a base ctor dispatches to
	// the override that table names).
	for _, anc := range info.AncestorChain {
		if ai, ok := e.classes[anc]; ok && ai.Constructor != nil && thisEscapesFn(ai.Constructor, methodNames) {
			return false
		}
	}
	for _, m := range info.Methods {
		if m == nil || m.IsStatic {
			continue
		}
		if thisEscapesFn(m, methodNames) {
			return false
		}
	}
	return true
}

// thisEscapesFn scans one constructor/method body for any use of `this`
// beyond field access (`this.f` read/write, `this[i]`) and method calls on
// this (`this.m(...)` — m's own body is audited separately via the effective
// method table). Everything else — `this` returned, passed, stored, captured
// by a nested closure (arrows capture lexical this into a heap env), used as
// a bare value, or a method *extracted* as a value (`this.m` uncalled) —
// counts as an escape. Any decorator on the declaration also fails it. The
// walk is reflection-based like collectFinRegNames: a construct it does not
// positively recognize that reaches a ThisExpression fails, never passes.
func thisEscapesFn(fd *ast.FunctionDeclaration, methodNames map[string]bool) bool {
	if len(fd.Decorators) > 0 {
		return true
	}
	if fd.Body == nil {
		return false
	}
	escaped := false

	isThis := func(x ast.Expression) bool {
		_, ok := x.(*ast.ThisExpression)
		return ok
	}

	var visit func(v reflect.Value)
	visitAny := func(x interface{}) { visit(reflect.ValueOf(x)) }
	visit = func(v reflect.Value) {
		if escaped {
			return
		}
		switch v.Kind() {
		case reflect.Ptr, reflect.Interface:
			if v.IsNil() {
				return
			}
			// Recognized safe shapes for `this`, handled before the generic
			// struct descent so their ThisExpression is never "reached".
			switch n := v.Interface().(type) {
			case *ast.ThisExpression:
				escaped = true
				return
			case *ast.ArrowFunction, *ast.FunctionExpression:
				// A nested closure CAPTURES lexical `this` into a heap env —
				// even a bare `this.f` read inside it is an escape, so the
				// member-safe shapes below must not apply here.
				if containsThis(v.Interface()) {
					escaped = true
				}
				return
			case *ast.CallExpression:
				if mem, ok := n.Callee.(*ast.MemberExpression); ok && isThis(mem.Object) {
					for _, a := range n.Args {
						visitAny(a)
					}
					return
				}
			case *ast.MemberExpression:
				if isThis(n.Object) {
					if methodNames[n.Property] {
						escaped = true // method extracted as a value
					}
					return
				}
			case *ast.IndexExpression:
				if isThis(n.Object) {
					visitAny(n.Index)
					return
				}
			}
			visit(v.Elem())
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i))
			}
		case reflect.Struct:
			if dec := v.FieldByName("Decorators"); dec.IsValid() && dec.Kind() == reflect.Slice && dec.Len() > 0 {
				escaped = true
				return
			}
			for i := 0; i < v.NumField(); i++ {
				if f := v.Field(i); f.CanInterface() {
					visit(f)
				}
			}
		}
	}
	visitAny(fd.Body)
	return escaped
}

// containsThis reports whether any ThisExpression appears anywhere in the
// subtree — the blunt scan thisEscapesFn applies inside nested closures.
func containsThis(root interface{}) bool {
	found := false
	var visit func(v reflect.Value)
	visit = func(v reflect.Value) {
		if found {
			return
		}
		switch v.Kind() {
		case reflect.Ptr, reflect.Interface:
			if v.IsNil() {
				return
			}
			if _, ok := v.Interface().(*ast.ThisExpression); ok {
				found = true
				return
			}
			visit(v.Elem())
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i))
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if f := v.Field(i); f.CanInterface() {
					visit(f)
				}
			}
		}
	}
	visit(reflect.ValueOf(root))
	return found
}

// emitObjAllocInto is emitObjAlloc into a register the caller already named.
func (e *Emitter) emitObjAllocInto(reg string, ty Type) {
	if hostPrefixedObject(ty) {
		e.emitHostPrefixedAlloc(reg, ty, "calloc(i64 1, ")
		return
	}
	e.emitObjBlock(reg, ty, true)
	e.emitStoreObjHeader(reg, ty)
}

// emitHostPrefixedAlloc allocates a prefixed host object (hostPrefixedObject)
// with call, its header word at the base, and names its body reg.
func (e *Emitter) emitHostPrefixedAlloc(reg string, ty Type, call string) {
	base := e.freshReg()
	e.heapAllocCall(base, allocHost, fmt.Sprintf("%si64 %d)", call, ty.StructSize()+8))
	e.heapStore("i64", fmt.Sprint(e.hostHeaderWord(ty)), base, 8)
	e.emitBodyAddr(reg, base)
}

// emitObjMallocInto is emitObjAllocInto without the zeroing, for a caller
// that stores every field itself.
func (e *Emitter) emitObjMallocInto(reg string, ty Type) {
	if hostPrefixedObject(ty) {
		e.emitHostPrefixedAlloc(reg, ty, "malloc(")
		return
	}
	e.emitObjBlock(reg, ty, false)
	e.emitStoreObjHeader(reg, ty)
}
