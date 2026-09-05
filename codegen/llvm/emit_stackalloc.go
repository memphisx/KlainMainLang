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
		e.emitInstr(fmt.Sprintf("store %s zeroinitializer, ptr %s, align 8", structIR, dataReg))
		return dataReg
	}
	e.ensureCalloc()
	dataReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", dataReg, ty.StructSize()))
	return dataReg
}

// closureAllocs returns the allocation instructions' registers for a closure
// literal's 16-byte {fn,env} header and (if caps > 0) its env struct —
// entry-block allocas when lit is the currently stack-planned initializer
// (TDD-00134 Stage 1, consuming the marker), plain mallocs otherwise. The
// shared capture cells are unaffected either way: they stay heap, shared by
// pointer with the enclosing scope.
func (e *Emitter) closureAllocs(lit ast.Expression, envIR string, envSize int64) (hdr, env string) {
	if e.pendingStackAllocLit != nil && e.pendingStackAllocLit == lit {
		e.pendingStackAllocLit = nil
		if e.stackAllocatedLits == nil {
			e.stackAllocatedLits = map[ast.Expression]bool{}
		}
		e.stackAllocatedLits[lit] = true
		hdr = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca {ptr, ptr}, align 8", hdr))
		if envSize > 0 {
			env = e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", env, envIR))
		}
		return hdr, env
	}
	e.ensureMalloc()
	hdr = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdr))
	if envSize > 0 {
		env = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", env, envSize))
	}
	return hdr, env
}

// planStackAllocs records, per candidate declaration, whether its flow
// provably keeps the value block-local — the -optimize-memory analog of
// planAutoFrees, writing to its own plan.
func (e *Emitter) planStackAllocs(prog *ast.Program) error {
	if !e.optimizeMemory {
		return nil
	}
	p := &escPlanner{
		auto:  true,
		stack: true,
		plan:  map[*ast.VarDeclaration]bool{},
	}
	if err := p.walkStmts(prog.Body, false); err != nil {
		return err
	}
	e.stackAllocPlan = p.plan
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
	// A Node-stream class wires `this` into a runtime handle at construction;
	// a decorated method's replacement slot may hold a wrapper that retains
	// its receiver. Both are outside what the audit below can see.
	if info.HasNodeReadable || info.HasNodeWritable {
		return false
	}
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

// collectFinRegNames returns the identifiers that provably denote a
// FinalizationRegistry everywhere they appear: bound exactly once, by a
// `new FinalizationRegistry(...)` declaration, and never rebound by any
// other binder (another declaration, a parameter, a catch/for binder, a
// destructuring element, an assignment). The scan is deliberately
// over-broad on the disqualifying side — via reflection, ANY struct field
// that names a binding (`Name`/`VarName`/`Param`/`Local`/`Alias`) counts —
// since over-disqualifying only loses an optimization, never soundness.
func collectFinRegNames(prog *ast.Program) map[string]bool {
	candidates := map[string]int{}
	other := map[string]bool{}

	binderFields := map[string]bool{"Name": true, "VarName": true, "Param": true, "Local": true, "Alias": true}

	addrOf := func(v reflect.Value) interface{} {
		if v.CanAddr() {
			if a := v.Addr(); a.CanInterface() {
				return a.Interface()
			}
		}
		return nil
	}

	var visit func(v reflect.Value)
	visit = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Ptr, reflect.Interface:
			if !v.IsNil() {
				visit(v.Elem())
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i))
			}
		case reflect.Struct:
			// An Identifier is a *reference*, not a binder — its Name field
			// must not disqualify (every use of the registry would).
			if _, isRef := addrOf(v).(*ast.Identifier); isRef {
				return
			}
			if vd, ok := addrOf(v).(*ast.VarDeclaration); ok {
				if ne, isNew := vd.Init.(*ast.NewExpression); isNew && ne.ClassName == "FinalizationRegistry" {
					candidates[vd.Name]++
				} else {
					other[vd.Name] = true
				}
				visit(reflect.ValueOf(vd.Init))
				return
			}
			for i := 0; i < v.NumField(); i++ {
				f := v.Field(i)
				name := v.Type().Field(i).Name
				if f.Kind() == reflect.String && binderFields[name] && f.String() != "" {
					other[f.String()] = true
					continue
				}
				if f.CanInterface() {
					visit(f)
				}
			}
		}
	}
	for _, s := range prog.Body {
		visit(reflect.ValueOf(s))
	}

	out := map[string]bool{}
	for n, cnt := range candidates {
		if cnt == 1 && !other[n] {
			out[n] = true
		}
	}
	return out
}
