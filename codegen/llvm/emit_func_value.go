// emit_func_value.go — first-class values for named top-level (and nested)
// functions: `const g = f`, `apply(f, ...)`, `return f`, etc.
//
// A named function's LLVM signature has no leading environment pointer
// (`define i64 @f(i64 %x)`), but every function *value* in this compiler is
// invoked through the closure ABI, which passes the environment first
// (`call i64 (ptr, i64) %fp(ptr %env, i64 %x)`). So a named function can't be
// pointed at by a closure header directly. Instead, referencing one by value
// materializes a `{ trampoline, null }` closure header whose trampoline drops
// the (unused) env pointer and forwards every remaining argument verbatim to
// the real function — after which the ordinary closure-call path (emitClosureCall)
// works unchanged.
//
// The closure and named-function ABIs agree operand-for-operand except for that
// leading env pointer (an array/rest parameter expands to `(ptr, i64)` on both
// sides, a nullable scalar to `{ i1, T }` on both, etc.), so the trampoline is a
// pure forwarder: receive-all, forward-all-but-env, return.
package llvm

import (
	"fmt"
	"strings"
)

// funcTypeFromSig is the closure/function Type a named function is seen as when
// used by value — its parameter/return types plus its rest-parameter flag, so a
// call site (emitClosureCallByPtr) packs a trailing rest argument correctly.
func funcTypeFromSig(sig FuncSig) Type {
	ft := FuncType(sig.ParamTypes, sig.RetType)
	ft.FuncHasRest = sig.HasRest
	return ft
}

// fnValueTrampolineName returns the trampoline symbol for a named function.
func fnValueTrampolineName(mangled string) string {
	return "@__fnval_" + llvmSafeSymbol(mangled)
}

// paramABITypes returns the LLVM operand type(s) one parameter occupies under
// both the closure and named-function calling conventions — an array (a plain
// array parameter or a rest parameter, whose declared type is the collected
// array) expands to a `(ptr, i64)` pair; every other type is one operand,
// nullable scalars included (their `{ i1, T }` storage shape).
func paramABITypes(pty Type) []string {
	if pty.IsArray {
		return []string{"ptr", "i64"}
	}
	return []string{storageIR(pty)}
}

// ensureFuncValueTrampoline emits (once, memoized) the env-dropping trampoline
// for a named function and returns its symbol.
func (e *Emitter) ensureFuncValueTrampoline(mangled string, sig FuncSig) string {
	sym := fnValueTrampolineName(mangled)
	if e.fnValueTrampolines[mangled] {
		return sym
	}
	e.fnValueTrampolines[mangled] = true

	// Build the parameter declaration list (env first) and the forward-argument
	// list (the same operands, minus env) in lockstep.
	decls := []string{"ptr %env"}
	var fwd []string
	n := 0
	for _, pty := range sig.ParamTypes {
		for _, ir := range paramABITypes(pty) {
			name := fmt.Sprintf("%%a%d", n)
			decls = append(decls, fmt.Sprintf("%s %s", ir, name))
			fwd = append(fwd, fmt.Sprintf("%s %s", ir, name))
			n++
		}
	}

	ret := sig.RetType.LLVMRetType()
	call := fmt.Sprintf("call %s @%s(%s)", ret, llvmSafeSymbol(mangled), strings.Join(fwd, ", "))

	var b strings.Builder
	b.WriteString(fmt.Sprintf("\ndefine %s %s(%s) {\nentry:\n", ret, sym, strings.Join(decls, ", ")))
	if ret == "void" {
		b.WriteString("  " + call + "\n  ret void\n}\n")
	} else {
		b.WriteString(fmt.Sprintf("  %%r = %s\n  ret %s %%r\n}\n", call, ret))
	}
	e.functions.WriteString(b.String())
	return sym
}

// emitNamedFuncValue materializes a `{ trampoline, null }` closure header for a
// named function referenced by value, returning it as a FuncType Value that the
// ordinary closure-call path can invoke.
func (e *Emitter) emitNamedFuncValue(mangled string, sig FuncSig) Value {
	tramp := e.ensureFuncValueTrampoline(mangled, sig)
	e.ensureMalloc()
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdr))
	fpSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", fpSlot, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", tramp, fpSlot))
	epSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", epSlot, hdr))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", epSlot))
	return Value{Ref: hdr, Ty: funcTypeFromSig(sig)}
}
