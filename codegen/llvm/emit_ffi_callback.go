package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// emit_ffi_callback.go — node:ffi Stage C (TDD-00164): registerCallback, the
// closure→C-function-pointer trampoline. A C function pointer is one word and
// carries no env, so a KML closure ({fn, env}) cannot be handed over directly.
// The design is the TDD's option (a): for each (FFI signature × closure KML
// ABI) pair, a family of 16 statically-emitted C-ABI wrapper functions is
// generated, each hard-wired to one slot of a registration table holding the
// live closure's {fn, env}. registerCallback claims a free slot and returns
// that wrapper's address as a bigint; unregisterCallback clears the slot, and
// a cleared wrapper returns a zero value if C still calls it (Node's
// collected-callback semantics). No runtime code generation, no W^X concerns;
// the trade is a cap of 16 concurrently-live callbacks per signature shape.

const ffiCbSlots = 16

// ffiCbLetter classifies a closure parameter/return type for the wrapper ABI:
// 'i' i64 number, 'd' double, 'b' bigint (ptr), 's' string (ptr), 'v' void.
func ffiCbLetter(t Type) byte {
	switch {
	case t.IsBigInt:
		return 'b'
	case t.IR == "double":
		return 'd'
	case t.IR == "i64" || t.IR == "i32" || t.IR == "i16" || t.IR == "i8" || t.IR == "i1":
		return 'i'
	case t.IR == "ptr":
		return 's'
	}
	return 0
}

// ffiCbKmlIR maps an ABI letter to the KML-side LLVM type.
func ffiCbKmlIR(letter byte) string {
	switch letter {
	case 'i':
		return "i64"
	case 'd':
		return "double"
	case 'b', 's':
		return "ptr"
	}
	return "void"
}

// ffiCbValidate checks a closure's declared parameter/return types against the
// FFI signature and returns the ABI letter string (params then return).
func ffiCbValidate(sig *FFISignature, params []Type, ret *Type, pos ast.Pos) (string, error) {
	if len(params) != len(sig.Args) {
		return "", fmt.Errorf("%d:%d: registerCallback: the callback takes %d parameter(s) but the signature declares %d", pos.Line, pos.Col, len(params), len(sig.Args))
	}
	letters := make([]byte, 0, len(params)+1)
	for i, c := range sig.Args {
		l := ffiCbLetter(params[i])
		switch c {
		case "char", "int8", "uint8", "int16", "uint16", "int32", "uint32", "float32", "float64":
			if l != 'i' && l != 'd' {
				return "", fmt.Errorf("%d:%d: registerCallback: parameter %d must be a number for FFI type '%s'", pos.Line, pos.Col, i+1, c)
			}
		case "int64", "uint64", "pointer", "function", "buffer", "arraybuffer":
			if l != 'b' {
				return "", fmt.Errorf("%d:%d: registerCallback: parameter %d must be a bigint for FFI type '%s' (64-bit values and pointers marshal as bigint)", pos.Line, pos.Col, i+1, c)
			}
		case "string":
			if l != 's' {
				return "", fmt.Errorf("%d:%d: registerCallback: parameter %d must be a string for FFI type 'string'", pos.Line, pos.Col, i+1)
			}
		}
		letters = append(letters, l)
	}
	rl := byte('v')
	if ret != nil && ret.IR != "" && !ret.IsNull && ret.IR != "void" {
		rl = ffiCbLetter(*ret)
	}
	switch sig.Ret {
	case "void":
		// any return is called and dropped
	case "char", "int8", "uint8", "int16", "uint16", "int32", "uint32", "float32", "float64":
		if rl != 'i' && rl != 'd' {
			return "", fmt.Errorf("%d:%d: registerCallback: the callback must return a number for FFI return type '%s'", pos.Line, pos.Col, sig.Ret)
		}
	case "int64", "uint64":
		if rl != 'b' && rl != 'i' {
			return "", fmt.Errorf("%d:%d: registerCallback: the callback must return a bigint (or number) for FFI return type '%s'", pos.Line, pos.Col, sig.Ret)
		}
	case "pointer", "function":
		if rl != 'b' {
			return "", fmt.Errorf("%d:%d: registerCallback: the callback must return a bigint for FFI return type '%s'", pos.Line, pos.Col, sig.Ret)
		}
	case "string":
		if rl != 's' {
			return "", fmt.Errorf("%d:%d: registerCallback: the callback must return a string for FFI return type 'string'", pos.Line, pos.Col)
		}
	}
	return string(letters) + string(rl), nil
}

// ffiCbKey names one wrapper family: FFI signature + closure ABI letters.
func ffiCbKey(sig *FFISignature, abi string) string {
	parts := append(append([]string{}, sig.Args...), "", sig.Ret, abi)
	return strings.Join(parts, "_")
}

// emitFFICbFamily emits (once per key) the registration table, the 16 wrapper
// functions, the wrapper-address table, and the register helper.
func (e *Emitter) emitFFICbFamily(key string, sig *FFISignature, abi string) {
	if e.ffiCbEmitted == nil {
		e.ffiCbEmitted = map[string]bool{}
	}
	if e.ffiCbEmitted[key] {
		return
	}
	e.ffiCbEmitted[key] = true
	e.ffiCbKeys = append(e.ffiCbKeys, key)

	needBigint := strings.ContainsRune(abi, 'b')
	if needBigint {
		e.ensureBigInt()
	}
	e.ensureFFIDl() // __kml_str_from_cstr for 's' params; harmless otherwise

	tab := "@__kml_ffi_cbtab_" + key
	addrs := "@__kml_ffi_cbaddrs_" + key
	e.emitGlobal(fmt.Sprintf("%s = internal global [%d x { ptr, ptr }] zeroinitializer", tab, ffiCbSlots))

	addrRefs := make([]string, ffiCbSlots)
	for i := 0; i < ffiCbSlots; i++ {
		name := fmt.Sprintf("@__kml_ffi_cbw_%s_%d", key, i)
		addrRefs[i] = "ptr " + name
		e.emitGlobal(e.ffiCbWrapperDef(name, tab, i, sig, abi))
	}
	e.emitGlobal(fmt.Sprintf("%s = internal constant [%d x ptr] [%s]", addrs, ffiCbSlots, strings.Join(addrRefs, ", ")))

	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_ffi_cbreg_%s(ptr %%fn, ptr %%env) {
entry:
  br label %%loop
loop:
  %%i = phi i64 [ 0, %%entry ], [ %%in, %%next ]
  %%done = icmp sge i64 %%i, %d
  br i1 %%done, label %%full, label %%chk
chk:
  %%fslot = getelementptr [%d x { ptr, ptr }], ptr %s, i64 0, i64 %%i, i32 0
  %%cur = load ptr, ptr %%fslot, align 8
  %%free = icmp eq ptr %%cur, null
  br i1 %%free, label %%take, label %%next
next:
  %%in = add i64 %%i, 1
  br label %%loop
take:
  store ptr %%fn, ptr %%fslot, align 8
  %%eslot = getelementptr [%d x { ptr, ptr }], ptr %s, i64 0, i64 %%i, i32 1
  store ptr %%env, ptr %%eslot, align 8
  %%aslot = getelementptr [%d x ptr], ptr %s, i64 0, i64 %%i
  %%a = load ptr, ptr %%aslot, align 8
  ret ptr %%a
full:
  ret ptr null
}`, key, ffiCbSlots, ffiCbSlots, tab, ffiCbSlots, tab, ffiCbSlots, addrs))
}

// ffiCbWrapperDef builds one wrapper function's define text: exact C-ABI
// prototype in, closure call out, slot-driven.
func (e *Emitter) ffiCbWrapperDef(name, tab string, slot int, sig *FFISignature, abi string) string {
	var b strings.Builder
	reg := 0
	r := func() string { reg++; return fmt.Sprintf("%%r%d", reg) }

	cRet := ffiLLVMType(sig.Ret)
	params := make([]string, len(sig.Args))
	for i, c := range sig.Args {
		params[i] = fmt.Sprintf("%s %%c%d", ffiLLVMType(c), i)
	}
	fmt.Fprintf(&b, "\ndefine %s %s(%s) {\nentry:\n", cRet, name, strings.Join(params, ", "))
	fp := r()
	fmt.Fprintf(&b, "  %%fslot = getelementptr [%d x { ptr, ptr }], ptr %s, i64 0, i64 %d, i32 0\n", ffiCbSlots, tab, slot)
	fmt.Fprintf(&b, "  %s = load ptr, ptr %%fslot, align 8\n", fp)
	live := r()
	fmt.Fprintf(&b, "  %s = icmp ne ptr %s, null\n", live, fp)
	fmt.Fprintf(&b, "  br i1 %s, label %%live, label %%dead\nlive:\n", live)
	env := r()
	fmt.Fprintf(&b, "  %%eslot = getelementptr [%d x { ptr, ptr }], ptr %s, i64 0, i64 %d, i32 1\n", ffiCbSlots, tab, slot)
	fmt.Fprintf(&b, "  %s = load ptr, ptr %%eslot, align 8\n", env)

	// Marshal each C argument to its KML value per the ABI letter.
	kmlArgs := []string{"ptr " + env}
	for i, c := range sig.Args {
		letter := abi[i]
		cReg := fmt.Sprintf("%%c%d", i)
		var v string
		switch c {
		case "char", "int8", "int16", "int32":
			v = r()
			fmt.Fprintf(&b, "  %s = sext %s %s to i64\n", v, ffiLLVMType(c), cReg)
		case "uint8", "uint16", "uint32":
			v = r()
			fmt.Fprintf(&b, "  %s = zext %s %s to i64\n", v, ffiLLVMType(c), cReg)
		case "float32":
			v = r()
			fmt.Fprintf(&b, "  %s = fpext float %s to double\n", v, cReg)
		case "float64":
			v = cReg
		case "int64":
			v = r()
			fmt.Fprintf(&b, "  %s = call ptr @__kml_bigint_from_i64(i64 %s)\n", v, cReg)
		case "uint64":
			v = r()
			fmt.Fprintf(&b, "  %s = call ptr @__kml_bigint_from_u64(i64 %s)\n", v, cReg)
		case "pointer", "function", "buffer", "arraybuffer":
			ai := r()
			fmt.Fprintf(&b, "  %s = ptrtoint ptr %s to i64\n", ai, cReg)
			v = r()
			fmt.Fprintf(&b, "  %s = call ptr @__kml_bigint_from_u64(i64 %s)\n", v, ai)
		case "string":
			// NULL-safe: pass null through untouched.
			isN := r()
			fmt.Fprintf(&b, "  %s = icmp eq ptr %s, null\n", isN, cReg)
			fmt.Fprintf(&b, "  br i1 %s, label %%snull%d, label %%sconv%d\nsconv%d:\n", isN, i, i, i)
			sv := r()
			fmt.Fprintf(&b, "  %s = call ptr @__kml_str_from_cstr(ptr %s)\n", sv, cReg)
			fmt.Fprintf(&b, "  br label %%sdone%d\nsnull%d:\n  br label %%sdone%d\nsdone%d:\n", i, i, i, i)
			v = r()
			fmt.Fprintf(&b, "  %s = phi ptr [ %s, %%sconv%d ], [ null, %%snull%d ]\n", v, sv, i, i)
		}
		// Numeric letter conversion: the C value normalized above is i64 for
		// ints, double for floats — flip if the closure declared the other.
		if letter == 'd' && (c == "char" || strings.HasPrefix(c, "int") || strings.HasPrefix(c, "uint")) && c != "int64" && c != "uint64" {
			d := r()
			fmt.Fprintf(&b, "  %s = sitofp i64 %s to double\n", d, v)
			v = d
		}
		if letter == 'i' && (c == "float32" || c == "float64") {
			iv := r()
			fmt.Fprintf(&b, "  %s = fptosi double %s to i64\n", iv, v)
			v = iv
		}
		kmlArgs = append(kmlArgs, ffiCbKmlIR(letter)+" "+v)
	}

	// Call the closure and marshal its return to the C ABI.
	retLetter := abi[len(abi)-1]
	kmlRet := ffiCbKmlIR(retLetter)
	var res string
	if kmlRet == "void" {
		fmt.Fprintf(&b, "  call void %s(%s)\n", fp, strings.Join(kmlArgs, ", "))
	} else {
		res = r()
		fmt.Fprintf(&b, "  %s = call %s %s(%s)\n", res, kmlRet, fp, strings.Join(kmlArgs, ", "))
	}
	switch sig.Ret {
	case "void":
		fmt.Fprintf(&b, "  ret void\ndead:\n  ret void\n}")
	case "char", "int8", "uint8", "int16", "uint16", "int32", "uint32":
		v := res
		if retLetter == 'd' {
			v = r()
			fmt.Fprintf(&b, "  %s = fptosi double %s to i64\n", v, res)
		}
		out := r()
		fmt.Fprintf(&b, "  %s = trunc i64 %s to %s\n", out, v, cRet)
		fmt.Fprintf(&b, "  ret %s %s\ndead:\n  ret %s 0\n}", cRet, out, cRet)
	case "int64", "uint64":
		v := res
		if retLetter == 'b' {
			v = r()
			fn := "__kml_bigint_to_i64"
			if sig.Ret == "uint64" {
				fn = "__kml_bigint_to_u64"
			}
			fmt.Fprintf(&b, "  %s = call i64 @%s(ptr %s)\n", v, fn, res)
		}
		fmt.Fprintf(&b, "  ret i64 %s\ndead:\n  ret i64 0\n}", v)
	case "float32":
		v := res
		if retLetter == 'i' {
			v = r()
			fmt.Fprintf(&b, "  %s = sitofp i64 %s to double\n", v, res)
		}
		out := r()
		fmt.Fprintf(&b, "  %s = fptrunc double %s to float\n", out, v)
		fmt.Fprintf(&b, "  ret float %s\ndead:\n  ret float 0.000000e+00\n}", out)
	case "float64":
		v := res
		if retLetter == 'i' {
			v = r()
			fmt.Fprintf(&b, "  %s = sitofp i64 %s to double\n", v, res)
		}
		fmt.Fprintf(&b, "  ret double %s\ndead:\n  ret double 0.000000e+00\n}", v)
	case "pointer", "function":
		ai := r()
		fmt.Fprintf(&b, "  %s = call i64 @__kml_bigint_to_u64(ptr %s)\n", ai, res)
		out := r()
		fmt.Fprintf(&b, "  %s = inttoptr i64 %s to ptr\n", out, ai)
		fmt.Fprintf(&b, "  ret ptr %s\ndead:\n  ret ptr null\n}", out)
	case "string":
		fmt.Fprintf(&b, "  ret ptr %s\ndead:\n  ret ptr null\n}", res)
	}
	return b.String()
}

// emitFFIRegisterCallback lowers library.registerCallback([sig,] callback).
func (e *Emitter) emitFFIRegisterCallback(args []ast.Expression, pos ast.Pos) (Value, error) {
	var sigExpr, cbExpr ast.Expression
	switch len(args) {
	case 1:
		cbExpr = args[0] // default signature: void ()
	case 2:
		sigExpr, cbExpr = args[0], args[1]
	default:
		return Value{}, fmt.Errorf("%d:%d: registerCallback takes (signature?, callback)", pos.Line, pos.Col)
	}
	sig, err := ffiParseSignature(sigExpr)
	if err != nil {
		return Value{}, err
	}
	cb, err := e.resolveCallback(cbExpr)
	if err != nil {
		return Value{}, fmt.Errorf("%d:%d: registerCallback: %v", pos.Line, pos.Col, err)
	}
	var params []Type
	var ret *Type
	var hdr string
	if cb.kind == cbClosure {
		params, ret = cb.ty.FuncParams, cb.ty.FuncRetType
		hdr = cb.hdrPtr
	} else {
		params = cb.sig.ParamTypes
		rt := cb.sig.RetType
		ret = &rt
		hdr = e.emitNamedFuncValue(cb.name, cb.sig).Ref
	}
	abi, err := ffiCbValidate(sig, params, ret, pos)
	if err != nil {
		return Value{}, err
	}
	key := ffiCbKey(sig, abi)
	e.emitFFICbFamily(key, sig, abi)

	fpSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", fpSlot, hdr))
	fp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, fpSlot))
	epSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", epSlot, hdr))
	ep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ep, epSlot))
	w := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ffi_cbreg_%s(ptr %s, ptr %s)", w, key, fp, ep))
	full := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", full, w))
	e.emitFFIThrowMsgOnCond(full, "Error", fmt.Sprintf("registerCallback: all %d callback slots for this signature are in use (unregister one first)", ffiCbSlots))
	return e.ffiPtrToBigInt(w), nil
}

// emitFFIUnregisterCallback lowers library.unregisterCallback(pointer).
func (e *Emitter) emitFFIUnregisterCallback(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: unregisterCallback takes exactly 1 argument (the callback pointer)", pos.Line, pos.Col)
	}
	_, p, err := e.emitFFIPointerArg(args[0], pos)
	if err != nil {
		return Value{}, err
	}
	// The definition is emitted once at the end of EmitProgram (finalize),
	// when every wrapper family is known; an LLVM forward reference from this
	// call site to that later define is fine, and a separate `declare` would
	// collide with it.
	e.ffiCbUnregUsed = true
	e.emitInstr(fmt.Sprintf("call void @__kml_ffi_cb_unregister(ptr %s)", p))
	return Value{Ty: TypeVoid}, nil
}

// emitFFICbFinalize defines @__kml_ffi_cb_unregister over every wrapper family
// the program emitted — called at the end of EmitProgram, when the full set of
// signatures is known. Defined whenever it was declared (or any family
// exists), with an empty scan if no callbacks were ever registered.
func (e *Emitter) emitFFICbFinalize() {
	if !e.ffiCbUnregUsed {
		return // families may exist, but unregister is never called
	}
	var b strings.Builder
	b.WriteString("\ndefine void @__kml_ffi_cb_unregister(ptr %a) {\nentry:\n  br label %k0\n")
	for ki, key := range e.ffiCbKeys {
		next := fmt.Sprintf("%%k%d", ki+1)
		fmt.Fprintf(&b, "k%d:\n  br label %%k%d.loop\nk%d.loop:\n", ki, ki, ki)
		fmt.Fprintf(&b, "  %%k%d.i = phi i64 [ 0, %%k%d ], [ %%k%d.in, %%k%d.next ]\n", ki, ki, ki, ki)
		fmt.Fprintf(&b, "  %%k%d.done = icmp sge i64 %%k%d.i, %d\n", ki, ki, ffiCbSlots)
		fmt.Fprintf(&b, "  br i1 %%k%d.done, label %s, label %%k%d.chk\n", ki, next, ki)
		fmt.Fprintf(&b, "k%d.chk:\n  %%k%d.aslot = getelementptr [%d x ptr], ptr @__kml_ffi_cbaddrs_%s, i64 0, i64 %%k%d.i\n", ki, ki, ffiCbSlots, key, ki)
		fmt.Fprintf(&b, "  %%k%d.addr = load ptr, ptr %%k%d.aslot, align 8\n", ki, ki)
		fmt.Fprintf(&b, "  %%k%d.hit = icmp eq ptr %%k%d.addr, %%a\n", ki, ki)
		fmt.Fprintf(&b, "  br i1 %%k%d.hit, label %%k%d.clear, label %%k%d.next\n", ki, ki, ki)
		fmt.Fprintf(&b, "k%d.next:\n  %%k%d.in = add i64 %%k%d.i, 1\n  br label %%k%d.loop\n", ki, ki, ki, ki)
		fmt.Fprintf(&b, "k%d.clear:\n  %%k%d.fslot = getelementptr [%d x { ptr, ptr }], ptr @__kml_ffi_cbtab_%s, i64 0, i64 %%k%d.i, i32 0\n", ki, ki, ffiCbSlots, key, ki)
		fmt.Fprintf(&b, "  store ptr null, ptr %%k%d.fslot, align 8\n", ki)
		fmt.Fprintf(&b, "  %%k%d.eslot = getelementptr [%d x { ptr, ptr }], ptr @__kml_ffi_cbtab_%s, i64 0, i64 %%k%d.i, i32 1\n", ki, ffiCbSlots, key, ki)
		fmt.Fprintf(&b, "  store ptr null, ptr %%k%d.eslot, align 8\n  ret void\n", ki)
	}
	fmt.Fprintf(&b, "k%d:\n  ret void\n}", len(e.ffiCbKeys))
	e.emitGlobal(b.String())
}
