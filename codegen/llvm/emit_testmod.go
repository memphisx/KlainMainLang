// emit_test.go — Node-style testing helpers as a first-class builtin module
// (TDD-00122): `import { mustCall, skip } from 'test'`. mustCall wraps a callback
// in a counting closure and registers an at-exit expectation (runtime_test.go);
// mustNotCall fails the moment it's invoked; skip exits 0; expectsError reuses
// the assert.throws try-frame; env probes are constant booleans (emit_exprs_member.go).
package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// testHostBool returns the LLVM i1 literal for a compile-time host-OS probe.
func testHostBool(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// emitTestModuleCall dispatches test.<method>(...).
func (e *Emitter) emitTestModuleCall(property string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch property {
	case "skip":
		return e.emitTestSkip(args, pos)
	case "mustCall":
		return e.emitTestMustCall(property, args, pos, false)
	case "mustCallAtLeast":
		return e.emitTestMustCall(property, args, pos, true)
	case "mustSucceed":
		// V1: same counting semantics as mustCall; the "err is falsy" check is
		// deferred with the nullable-arg ABI (TDD-00122 open question).
		return e.emitTestMustCall(property, args, pos, false)
	case "mustNotCall":
		return e.emitTestMustNotCall(args, pos)
	case "expectsError":
		return e.emitAssertThrows(args, pos)
	case "expectWarning":
		// V1 leniency: run the thunk if given a function; warning capture isn't
		// modeled, so this asserts nothing beyond "it ran".
		if len(args) >= 1 && e.inferExprType(args[0]).IsFunc {
			fnVal, err := e.emitExpr(args[0])
			if err != nil {
				return Value{}, err
			}
			if _, err := e.emitClosureCallByPtr(fnVal.Ref, fnVal.Ty, nil, pos); err != nil {
				return Value{}, err
			}
		}
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: unsupported test.%s", pos.Line, pos.Col, property)
}

// emitTestSkip prints a skip notice and exits 0 — a skipped test is not a failure.
func (e *Emitter) emitTestSkip(args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensurePrintf()
	e.ensureExit()
	if !e.testSkipFmtEmitted {
		e.testSkipFmtEmitted = true
		e.emitGlobal(llvmCStrConst("@.kml_test_skip", "test skipped: %s"))
	}
	msgPtr := e.internString("")
	if len(args) >= 1 {
		var err error
		msgPtr, err = e.emitAssertMessage(args[0])
		if err != nil {
			return Value{}, err
		}
	}
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ...) @printf(ptr @.kml_test_skip, ptr %s)", msgPtr))
	e.emitInstr("call void @exit(i32 0)")
	e.emitTerminator("unreachable")
	return Value{Ty: TypeVoid}, nil
}

// emitTestMustCall wraps a callback so each invocation bumps a counter, and
// registers an at-exit expectation. atLeast=false ⇒ exactly `exact` (default 1
// or the 2nd argument); atLeast=true ⇒ `>= min`.
func (e *Emitter) emitTestMustCall(property string, args []ast.Expression, pos ast.Pos, atLeast bool) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: test.%s takes 1-2 arguments", pos.Line, pos.Col, property)
	}
	fnTy := e.inferExprType(args[0])
	if !fnTy.IsFunc {
		return Value{}, fmt.Errorf("%d:%d: test.%s's first argument must be a function", pos.Line, pos.Col, property)
	}
	// V1 ABI bound: 0-2 scalar/pointer params, no rest, no array/nullable params.
	if fnTy.FuncHasRest || len(fnTy.FuncParams) > 2 {
		return Value{}, fmt.Errorf("%d:%d: test.%s supports callbacks of 0-2 simple parameters (no rest / >2 args) in this version", pos.Line, pos.Col, property)
	}
	for _, p := range fnTy.FuncParams {
		if p.IsArray || isNullableScalar(p) {
			return Value{}, fmt.Errorf("%d:%d: test.%s does not yet support array or nullable-scalar callback parameters", pos.Line, pos.Col, property)
		}
	}

	fnVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}

	// The expected count (i64): the 2nd arg if given, else 1.
	countRef := "1"
	if len(args) == 2 {
		cv, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		countRef = e.coerce(cv, TypeI64).Ref
	}

	e.ensureTestRuntime()
	e.ensureMalloc()

	// Counter slot (i64, starts at 0).
	cnt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 8)", cnt))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", cnt))

	// Wrapper env: { origClosure, counterPtr }.
	env := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", env))
	envS0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", envS0, env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", fnVal.Ref, envS0))
	envS1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", envS1, env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cnt, envS1))

	// Wrapper closure header: { countingTrampoline, env }.
	tramp := e.ensureMustCallTrampoline(fnVal.Ty)
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdr))
	hS0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", hS0, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", tramp, hS0))
	hS1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", hS1, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", env, hS1))

	// Register the expectation. exact ⇒ min=max=count; atLeast ⇒ min=count,max=-1.
	msgPtr := e.internString(property)
	maxRef := countRef
	if atLeast {
		maxRef = "-1"
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_test_register(ptr %s, i64 %s, i64 %s, ptr %s)", cnt, countRef, maxRef, msgPtr))

	return Value{Ref: hdr, Ty: fnVal.Ty}, nil
}

// ensureMustCallTrampoline emits (once per callback signature) the counting
// forwarder: load {origClosure, counterPtr} from env, bump the counter, then
// invoke the original closure with the forwarded arguments and return its result.
func (e *Emitter) ensureMustCallTrampoline(ty Type) string {
	// Signature key from the operand IR types.
	var parts []string
	for _, p := range ty.FuncParams {
		parts = append(parts, paramABITypes(p)...)
	}
	retIR := "void"
	if ty.FuncRetType != nil {
		retIR = ty.FuncRetType.LLVMRetType()
	}
	key := strings.Join(parts, "_") + "__" + retIR
	key = strings.NewReplacer("{", "s", "}", "e", ",", "c", " ", "").Replace(key)
	sym := "@__kml_mc_" + key
	if e.testTrampolines[key] {
		return sym
	}
	e.testTrampolines[key] = true

	decls := []string{"ptr %env"}
	var fwd []string
	n := 0
	for _, p := range ty.FuncParams {
		for _, ir := range paramABITypes(p) {
			name := fmt.Sprintf("%%a%d", n)
			decls = append(decls, fmt.Sprintf("%s %s", ir, name))
			fwd = append(fwd, fmt.Sprintf("%s %s", ir, name))
			n++
		}
	}
	innerArgs := append([]string{"ptr %oep"}, fwd...)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("\ndefine %s %s(%s) {\nentry:\n", retIR, sym, strings.Join(decls, ", ")))
	b.WriteString("  %oc_p = getelementptr {ptr, ptr}, ptr %env, i32 0, i32 0\n")
	b.WriteString("  %oc = load ptr, ptr %oc_p, align 8\n")
	b.WriteString("  %ct_p = getelementptr {ptr, ptr}, ptr %env, i32 0, i32 1\n")
	b.WriteString("  %ct = load ptr, ptr %ct_p, align 8\n")
	b.WriteString("  %cv = load i64, ptr %ct, align 8\n")
	b.WriteString("  %cv1 = add i64 %cv, 1\n")
	b.WriteString("  store i64 %cv1, ptr %ct, align 8\n")
	b.WriteString("  %ofp_p = getelementptr {ptr, ptr}, ptr %oc, i32 0, i32 0\n")
	b.WriteString("  %ofp = load ptr, ptr %ofp_p, align 8\n")
	b.WriteString("  %oep_p = getelementptr {ptr, ptr}, ptr %oc, i32 0, i32 1\n")
	b.WriteString("  %oep = load ptr, ptr %oep_p, align 8\n")
	if retIR == "void" {
		b.WriteString(fmt.Sprintf("  call void %%ofp(%s)\n  ret void\n}\n", strings.Join(innerArgs, ", ")))
	} else {
		b.WriteString(fmt.Sprintf("  %%r = call %s %%ofp(%s)\n  ret %s %%r\n}\n", retIR, strings.Join(innerArgs, ", "), retIR))
	}
	e.functions.WriteString(b.String())
	return sym
}

// emitTestMustNotCall returns a zero-arg wrapper that fails the process the
// moment it is invoked. V1: the wrapper is `() => void`; using it where a
// non-zero-arity callback is expected is a type mismatch (documented limitation).
func (e *Emitter) emitTestMustNotCall(args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensurePrintf()
	e.ensureExit()
	e.ensureMalloc()
	if !e.testMustNotCallEmitted {
		e.testMustNotCallEmitted = true
		e.emitGlobal(llvmCStrConst("@.kml_test_mustnot", "test: mustNotCall callback was invoked"))
		e.emitGlobal(`
define void @__kml_test_mustnotcall(ptr %env) {
entry:
  call i32 (ptr, ...) @printf(ptr @.kml_test_mustnot)
  call void @exit(i32 1)
  unreachable
}`)
	}
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdr))
	hS0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", hS0, hdr))
	e.emitInstr(fmt.Sprintf("store ptr @__kml_test_mustnotcall, ptr %s, align 8", hS0))
	hS1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", hS1, hdr))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", hS1))
	return Value{Ref: hdr, Ty: FuncType(nil, TypeVoid)}, nil
}
