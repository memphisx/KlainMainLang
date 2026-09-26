package llvm

import (
	_ "embed"
	"fmt"
	"runtime"
	"strings"
)

// emit_ffi_runtime.go — node:ffi's runtime library registry (TDD-00229 Stage
// C). A DynamicLibrary is a KmlFfiLib (ffisrc/ffi_registry.c) whose `symbols`
// and `functions` tables are ordered exactly as Node's std::unordered_map
// (the real STL container on POSIX, an MSVC port on Windows), and a bound
// function is a function object: an extended tag-12 record whose code is a
// per-signature dynamic-ABI thunk, whose env is the registry entry, and
// which carries its own name and an own `pointer` property. Failures come
// back from the C side as status codes and are thrown here as Node's errors.

//go:embed ffisrc/ffi_registry.c
var ffiRegistrySource string

//go:embed ffisrc/ffi_umap_stl.cc
var ffiUmapSTLSource string

//go:embed ffisrc/ffi_umap_msvc.c
var ffiUmapMSVCSource string

// FFIRegistrySources returns the C/C++ members behind the registry for the
// current target: the registry itself plus the order container.
func (e *Emitter) FFIRegistrySources() []CSource {
	out := []CSource{{"ffireg", ffiRegistrySource, nil, nil, ""}}
	if e.opts.Target.OS() == "windows" {
		return append(out, CSource{"ffiumap", ffiUmapMSVCSource, nil, nil, ""})
	}
	cxx := "-lstdc++"
	if e.opts.Target.OS() == "darwin" || runtime.GOOS == "darwin" && e.opts.Target.OS() == "" {
		cxx = "-lc++"
	}
	return append(out, CSource{"ffiumap", ffiUmapSTLSource, nil, []string{cxx}, "cc"})
}

// UsesFFIRegistry reports whether the registry members must be linked.
func (e *Emitter) UsesFFIRegistry() bool { return e.usedFFIRegistry }

// KmlFfiLib / KmlFfiFn field offsets (ffi_registry.c).
const (
	ffiLibOffClosed = 16
	ffiFnOffCfn     = 0
	ffiFnOffRec     = 16
	ffiFnOffLib     = 24
	ffiFnOffName    = 32
	// An extended tag-12 record (a bound native function) sets this bit in its
	// arity word: { fnptr, env, arity|ext, ptr name, ptr props } (fnmeta.c).
	ffiRecExtFlag = int64(1) << 62
)

// Registry status codes (ffi_registry.c).
const (
	ffiStatusClosed  = 1
	ffiStatusDlsym   = 2
	ffiStatusSigDiff = 3
	ffiStatusDlopen  = 4
)

func (e *Emitter) ensureFFIRuntime() {
	if e.usedFFIRegistry {
		return
	}
	e.usedFFIRegistry = true
	e.ensureFFIDl()
	e.ensureBigInt()
	e.ensureDynObj()
	e.ensureDynJSONC()
	e.ensureFnMeta()
	e.ensureExceptionHelpers()
	e.ensureStrHeaderRuntime()
	e.ensureStrlen()
	e.ensureMalloc()
	for _, d := range []string{
		"declare i64 @__kml_ffi_open(ptr, ptr)",
		"declare ptr @__kml_ffi_errmsg()",
		"declare i64 @__kml_ffi_get_symbol(ptr, ptr, i64, ptr)",
		"declare i64 @__kml_ffi_prepare(ptr, ptr, i64, ptr, ptr, ptr)",
		"declare void @__kml_ffi_commit(ptr, ptr)",
		"declare void @__kml_ffi_close(ptr)",
		"declare i64 @__kml_ffi_count(ptr, i64)",
		"declare void @__kml_ffi_list(ptr, i64, ptr, ptr)",
		"declare i64 @__kml_ffi_v_int(i64, i64, ptr)",
		"declare i64 @__kml_ffi_v_i64(i64, i64, ptr)",
		"declare i64 @__kml_ffi_v_f64(i64, ptr)",
		"declare i64 @__kml_ffi_v_ptr(i64, ptr)",
	} {
		e.emitGlobal(d)
	}
}

// ffiCharSigned reports whether the target's C `char` is signed — Node maps
// the `char` type to the platform char (verified: `int8` on macOS/arm64,
// `uint8` on Linux/arm64). Signed everywhere except the AAPCS Linux/Android
// arm targets.
func (e *Emitter) ffiCharSigned() bool {
	if e.opts.Target.OS() == "linux" || e.opts.Target.OS() == "android" {
		switch e.opts.Target.Arch() {
		case "arm64", "arm":
			return false
		}
	}
	return true
}

// ffiIntKind maps an 8..32-bit canonical type to __kml_ffi_v_int's kind
// (0 int8, 1 uint8, 2 int16, 3 uint16, 4 int32, 5 uint32).
func (e *Emitter) ffiIntKind(c string) (int, bool) {
	if c == "char" {
		if e.ffiCharSigned() {
			c = "int8"
		} else {
			c = "uint8"
		}
	}
	switch c {
	case "int8":
		return 0, true
	case "uint8":
		return 1, true
	case "int16":
		return 2, true
	case "uint16":
		return 3, true
	case "int32":
		return 4, true
	case "uint32":
		return 5, true
	}
	return 0, false
}

// ffiIsPointerLike reports the pointer family (one libffi type in Node).
func ffiIsPointerLike(c string) bool {
	switch c {
	case "pointer", "string", "buffer", "arraybuffer", "function":
		return true
	}
	return false
}

// ffiArgMessage is the "must be …" tail Node throws for an invalid argument.
func (e *Emitter) ffiArgMessage(c string) string {
	if k, ok := e.ffiIntKind(c); ok {
		return []string{"an int8", "a uint8", "an int16", "a uint16", "an int32", "a uint32"}[k]
	}
	switch c {
	case "int64":
		return "an int64"
	case "uint64":
		return "a uint64"
	case "float32":
		return "a float"
	case "float64":
		return "a double"
	}
	return "a buffer, an ArrayBuffer, a string, or a bigint"
}

// ffiSigKey is a signature's identity as Node compares it (libffi types, so
// aliases and the whole pointer family coincide; `char` is the platform
// char's type).
func (e *Emitter) ffiSigKey(sig *FFISignature) string {
	t := func(c string) string {
		if k, ok := e.ffiIntKind(c); ok {
			return []string{"i8", "u8", "i16", "u16", "i32", "u32"}[k]
		}
		switch {
		case ffiIsPointerLike(c):
			return "p"
		case c == "float32":
			return "f"
		case c == "float64":
			return "d"
		case c == "int64":
			return "i64"
		case c == "uint64":
			return "u64"
		}
		return "v"
	}
	args := make([]string, len(sig.Args))
	for i, a := range sig.Args {
		args[i] = t(a)
	}
	return t(sig.Ret) + "(" + strings.Join(args, ",") + ")"
}

// emitThrowCoded throws a Node-style coded error (`kind` is "Error" or
// "TypeError") with a runtime message. Ends the current block.
func (e *Emitter) emitThrowCoded(kind, code, msgPtr string) {
	e.ensureExceptionHelpers()
	errReg := e.buildErrorObj(errorKindIDs[kind], msgPtr, e.internString(kind))
	codeGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 3", codeGep, errorObjType.StructIR(), errReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString(code), codeGep))
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errReg))
	e.emitTerminator("unreachable")
}

// emitFFIThrowWhen throws when cond (an i1 register, or "true") holds and
// continues in a fresh block otherwise — so an unconditional throw still
// leaves a live continuation for the code after it.
func (e *Emitter) emitFFIThrowWhen(cond, kind, code, msgPtr string) {
	throwL := e.freshLabel("ffi.throw")
	contL := e.freshLabel("ffi.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond, throwL, contL))
	e.emitLabel(throwL)
	e.emitThrowCoded(kind, code, msgPtr)
	e.emitLabel(contL)
}

// emitFFIThrowClosedIf throws ERR_FFI_LIBRARY_CLOSED when lib is closed.
func (e *Emitter) emitFFIThrowClosedIf(libPtr string) {
	cp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", cp, libPtr, ffiLibOffClosed))
	closed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", closed, cp))
	isClosed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isClosed, closed))
	e.emitFFIThrowWhen(isClosed, "Error", "ERR_FFI_LIBRARY_CLOSED", e.internString("Library is closed"))
}

// emitFFIStatusCheck turns a registry status into Node's error; onFail runs
// first on every failing path (dlopen closes the half-built library).
func (e *Emitter) emitFFIStatusCheck(st string, onFail func()) {
	okL := e.freshLabel("ffi.st.ok")
	closedL := e.freshLabel("ffi.st.closed")
	callL := e.freshLabel("ffi.st.call")
	sigL := e.freshLabel("ffi.st.sig")
	e.emitTerminator(fmt.Sprintf("switch i64 %s, label %%%s [ i64 %d, label %%%s i64 %d, label %%%s i64 %d, label %%%s i64 %d, label %%%s ]",
		st, okL, ffiStatusClosed, closedL, ffiStatusDlsym, callL, ffiStatusSigDiff, sigL, ffiStatusDlopen, callL))
	e.emitLabel(closedL)
	if onFail != nil {
		onFail()
	}
	e.emitThrowCoded("Error", "ERR_FFI_LIBRARY_CLOSED", e.internString("Library is closed"))
	e.emitLabel(callL)
	if onFail != nil {
		onFail()
	}
	m := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ffi_errmsg()", m))
	e.emitThrowCoded("Error", "ERR_FFI_CALL_FAILED", m)
	e.emitLabel(sigL)
	if onFail != nil {
		onFail()
	}
	m2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ffi_errmsg()", m2))
	e.emitThrowCoded("TypeError", "ERR_INVALID_ARG_VALUE", m2)
	e.emitLabel(okL)
}

// emitFFIMakeRecord builds the function object for a freshly prepared entry
// (fnReg: KmlFfiFn*), stores it back into the entry, and returns it.
func (e *Emitter) emitFFIMakeRecord(fnReg string, sig *FFISignature) string {
	thunk := e.ensureFFIDynThunk(sig)
	rec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 40)", rec))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", thunk, rec))
	store := func(off int, ty, val string) {
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", p, rec, off))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", ty, val, p))
	}
	store(8, "ptr", fnReg)
	store(16, "i64", fmt.Sprintf("%d", ffiRecExtFlag|int64(len(sig.Args))))
	np := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", np, fnReg, ffiFnOffName))
	name := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", name, np))
	store(24, "ptr", name)
	// The one own property: `pointer`, the symbol address as a bigint.
	props := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_new()", props))
	cfn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cfn, fnReg))
	pb := e.emitBoxBigInt(e.ffiPtrToBigInt(cfn))
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set(ptr %s, ptr %s, i64 %s)", props, e.internString("pointer"), pb.Ref))
	store(32, "ptr", props)
	rp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", rp, fnReg, ffiFnOffRec))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", rec, rp))
	return rec
}

// emitFFIRecordFor returns the function object for a prepared entry: a fresh
// entry is committed to the tables and gets a new record; an existing one
// hands back its cached record (Node's per-name callable identity).
func (e *Emitter) emitFFIRecordFor(libPtr, fnReg, freshReg string, sig *FFISignature) string {
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	isFresh := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isFresh, freshReg))
	newL := e.freshLabel("ffi.fn.new")
	oldL := e.freshLabel("ffi.fn.cached")
	doneL := e.freshLabel("ffi.fn.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFresh, newL, oldL))
	e.emitLabel(newL)
	e.emitInstr(fmt.Sprintf("call void @__kml_ffi_commit(ptr %s, ptr %s)", libPtr, fnReg))
	rec := e.emitFFIMakeRecord(fnReg, sig)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", rec, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(oldL)
	rp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", rp, fnReg, ffiFnOffRec))
	old := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", old, rp))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", old, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
	return r
}

// emitFFIAccumulator builds `library.symbols` / `library.functions` (and the
// no-argument getSymbols()/getFunctions()): a fresh null-prototype object
// whose keys enumerate in the registry's container order.
func (e *Emitter) emitFFIAccumulator(libPtr string, functions bool) Value {
	fl := "0"
	if functions {
		fl = "1"
	}
	n := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_ffi_count(ptr %s, i64 %s)", n, libPtr, fl))
	isClosed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isClosed, n))
	e.emitFFIThrowWhen(isClosed, "Error", "ERR_FFI_LIBRARY_CLOSED", e.internString("Library is closed"))
	bytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", bytes, n))
	alloc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 8", alloc, bytes))
	names := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", names, alloc))
	vals := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", vals, alloc))
	e.emitInstr(fmt.Sprintf("call void @__kml_ffi_list(ptr %s, i64 %s, ptr %s, ptr %s)", libPtr, fl, names, vals))
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_new()", bag))
	e.emitDynSetProtoChecked(bag, "null")

	iSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", iSlot))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", iSlot))
	condL := e.freshLabel("ffi.acc.cond")
	bodyL := e.freshLabel("ffi.acc.body")
	doneL := e.freshLabel("ffi.acc.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, iSlot))
	more := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, i, n))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, doneL))
	e.emitLabel(bodyL)
	kp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", kp, names, i))
	key := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", key, kp))
	vp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", vp, vals, i))
	val := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", val, vp))
	var boxed string
	if functions {
		rp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", rp, val, ffiFnOffRec))
		rec := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", rec, rp))
		boxed = e.emitNbTagPtr(rec, kmlTagDynFunc)
	} else {
		boxed = e.emitBoxBigInt(e.ffiPtrToBigInt(val)).Ref
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set(ptr %s, ptr %s, i64 %s)", bag, key, boxed))
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, iSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
	v := e.emitDynObjBox(bag)
	v.Ty = ffiAccumulatorType(functions)
	return v
}

// ffiAccumulatorType is the static type of `library.symbols`/getSymbols()
// and `library.functions`/getFunctions(): a dynamic object, which for the
// symbols is the typings' `{ [symbol: string]: bigint }` view — a read is a
// bigint (undefined for a name never resolved).
func ffiAccumulatorType(functions bool) Type {
	t := TypeAny
	if !functions {
		pt := BigIntType()
		pt.Nullable, pt.IsUndefined = true, true
		t.DynPropTy = &pt
	}
	return t
}

// emitFFIValidateArg validates and converts one already-evaluated argument
// for C type `canon`, throwing Node's "Argument i must be …" on failure.
// Pointer-family arguments whose static type is a byte container, a string,
// or null take the direct path; every other value is judged from its box by
// the same C validators the dynamic-call thunk uses.
func (e *Emitter) emitFFIValidateArg(v Value, canon string, idx int) (irTy, ref string, err error) {
	irTy = ffiLLVMType(canon)
	msg := e.internString(fmt.Sprintf("Argument %d must be %s", idx, e.ffiArgMessage(canon)))
	if ffiIsPointerLike(canon) {
		switch {
		case v.Ty.IsNull:
			return "ptr", "null", nil
		case isStringTy(v.Ty) && !v.Ty.IsDynamic:
			return "ptr", v.Ref, nil
		case v.Ty.IsArrayBuffer:
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr {i64, ptr}, ptr %s, i32 0, i32 1", gep, v.Ref))
			p := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", p, gep))
			return "ptr", p, nil
		case v.Ty.IsDataView:
			// {ptr data(base+offset), …}: field 0 is the view's first byte.
			p := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", p, v.Ref))
			return "ptr", p, nil
		case v.Ty.IsArray || v.Ty.IsBuffer || v.Ty.IsTypedArray:
			p := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", p, v.Ref))
			return "ptr", p, nil
		}
	}
	boxed, berr := e.emitBoxValue(v)
	if berr != nil {
		return "", "", berr
	}
	switch {
	case ffiIsPointerLike(canon):
		out := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", out))
		st := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_ffi_v_ptr(i64 %s, ptr %s)", st, boxed.Ref, out))
		isRange := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 2", isRange, st))
		e.emitFFIThrowWhen(isRange, "TypeError", "ERR_INVALID_ARG_VALUE", e.internString(fmt.Sprintf("Argument %d must be a non-negative pointer bigint", idx)))
		bad := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", bad, st))
		e.emitFFIThrowWhen(bad, "TypeError", "ERR_INVALID_ARG_VALUE", msg)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, out))
		return "ptr", r, nil
	case canon == "int64" || canon == "uint64":
		out := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", out))
		unsig := 0
		if canon == "uint64" {
			unsig = 1
		}
		st := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_ffi_v_i64(i64 %s, i64 %d, ptr %s)", st, boxed.Ref, unsig, out))
		bad := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", bad, st))
		e.emitFFIThrowWhen(bad, "TypeError", "ERR_INVALID_ARG_VALUE", msg)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, out))
		return "i64", r, nil
	case canon == "float32" || canon == "float64":
		out := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca double, align 8", out))
		st := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_ffi_v_f64(i64 %s, ptr %s)", st, boxed.Ref, out))
		bad := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", bad, st))
		e.emitFFIThrowWhen(bad, "TypeError", "ERR_INVALID_ARG_VALUE", msg)
		d := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load double, ptr %s, align 8", d, out))
		if canon == "float32" {
			f := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = fptrunc double %s to float", f, d))
			return "float", f, nil
		}
		return "double", d, nil
	}
	kind, ok := e.ffiIntKind(canon)
	if !ok {
		return "", "", fmt.Errorf("unsupported FFI argument type '%s'", canon)
	}
	out := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", out))
	st := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_ffi_v_int(i64 %s, i64 %d, ptr %s)", st, boxed.Ref, kind, out))
	bad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", bad, st))
	e.emitFFIThrowWhen(bad, "TypeError", "ERR_INVALID_ARG_VALUE", msg)
	w := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", w, out))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to %s", r, w, irTy))
	return irTy, r, nil
}

// emitFFIReturnValue widens a raw C return to its JS value (Node: 8..32-bit
// integers and floats are numbers, 64-bit integers and the pointer family are
// bigints, void is undefined).
func (e *Emitter) emitFFIReturnValue(raw, canon string) Value {
	switch {
	case canon == "void":
		return Value{Ty: TypeVoid}
	case ffiIsPointerLike(canon):
		return e.ffiPtrToBigInt(raw)
	}
	if k, ok := e.ffiIntKind(canon); ok {
		ext := "sext"
		if k%2 == 1 {
			ext = "zext"
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = %s %s %s to i64", r, ext, ffiLLVMType(canon), raw))
		return Value{Ref: r, Ty: TypeI64}
	}
	v, _ := e.emitFFIMarshalReturn(raw, canon)
	return v
}

// emitFFICallThroughRecord performs the call once the record and the evaluated
// arguments are known: the closed-library check, the argument-count check
// and per-argument validation (all in Node's order), then the C call.
func (e *Emitter) emitFFICallThroughRecord(rec string, sig *FFISignature, args []Value) (Value, error) {
	ep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", ep, rec))
	fn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fn, ep))
	lp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", lp, fn, ffiFnOffLib))
	lib := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", lib, lp))
	e.emitFFIThrowClosedIf(lib)
	if len(args) != len(sig.Args) {
		e.emitFFIThrowWhen("true", "TypeError", "ERR_INVALID_ARG_VALUE",
			e.internString(fmt.Sprintf("Invalid argument count: expected %d, got %d", len(sig.Args), len(args))))
		return e.ffiZeroReturn(sig.Ret), nil
	}
	var callArgs []string
	for i, a := range args {
		irTy, ref, err := e.emitFFIValidateArg(a, sig.Args[i], i)
		if err != nil {
			return Value{}, err
		}
		callArgs = append(callArgs, irTy+" "+ref)
	}
	cfn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cfn, fn))
	retIR := ffiLLVMType(sig.Ret)
	if retIR == "void" {
		e.emitInstr(fmt.Sprintf("call void %s(%s)", cfn, strings.Join(callArgs, ", ")))
		return Value{Ty: TypeVoid}, nil
	}
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s %s(%s)", raw, retIR, cfn, strings.Join(callArgs, ", ")))
	return e.emitFFIReturnValue(raw, sig.Ret), nil
}

// ffiZeroReturn is a placeholder result after an unconditional throw.
func (e *Emitter) ffiZeroReturn(canon string) Value {
	ty := ffiReturnKmlType(canon)
	if ty.IR == "" || ty.IR == "void" {
		return Value{Ty: TypeVoid}
	}
	return Value{Ref: ty.zeroLiteral(), Ty: ty}
}

// ensureFFIDynThunk emits (once per signature identity) the dynamic-ABI entry
// point of bound native functions: `i64 (ptr fn, i64 this, i64 argc, ptr
// argv)` over NaN-boxed words — what a call through `any` (lib.functions.x(…),
// a boxed function) reaches. Same checks and conversions as the direct path.
func (e *Emitter) ensureFFIDynThunk(sig *FFISignature) string {
	key := e.ffiSigKey(sig)
	if e.ffiDynThunks == nil {
		e.ffiDynThunks = map[string]string{}
	}
	if name, ok := e.ffiDynThunks[key]; ok {
		return name
	}
	name := fmt.Sprintf("@__kml_ffi_dyn_%d", len(e.ffiDynThunks))
	e.ffiDynThunks[key] = name

	restoreBuf := e.beginThunkEmit()
	savedRet := e.currentRetType
	restore := func() {
		restoreBuf()
		e.currentRetType = savedRet
	}
	e.currentRetType = TypeAny
	// Node's order: closed library, argument count, then each argument.
	lp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %%env, i64 %d", lp, ffiFnOffLib))
	lib := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", lib, lp))
	e.emitFFIThrowClosedIf(lib)
	badCount := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %%argc, %d", badCount, len(sig.Args)))
	cntL := e.freshLabel("ffi.dyn.count")
	okL := e.freshLabel("ffi.dyn.countok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", badCount, cntL, okL))
	e.emitLabel(cntL)
	// "Invalid argument count: expected N, got M" with the runtime M.
	e.ensureSprintf()
	buf := e.emitStringScratch(96)
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %%argc)", buf,
		e.internString(fmt.Sprintf("Invalid argument count: expected %d, got %%lld", len(sig.Args)))))
	e.emitStringFinalizeLen(buf)
	e.emitThrowCoded("TypeError", "ERR_INVALID_ARG_VALUE", buf)
	e.emitLabel(okL)
	var callArgs []string
	for i, canon := range sig.Args {
		ap := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %%argv, i64 %d", ap, i))
		aw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", aw, ap))
		irTy, ref, err := e.emitFFIValidateArg(Value{Ref: aw, Ty: TypeAny}, canon, i)
		if err != nil {
			restore()
			return name
		}
		callArgs = append(callArgs, irTy+" "+ref)
	}
	cfn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %%env, align 8", cfn))
	retIR := ffiLLVMType(sig.Ret)
	if retIR == "void" {
		e.emitInstr(fmt.Sprintf("call void %s(%s)", cfn, strings.Join(callArgs, ", ")))
		e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
	} else {
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call %s %s(%s)", raw, retIR, cfn, strings.Join(callArgs, ", ")))
		b, _ := e.emitBoxValue(e.emitFFIReturnValue(raw, sig.Ret))
		e.emitTerminator(fmt.Sprintf("ret i64 %s", b.Ref))
	}
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine i64 %s(ptr %%env, i64 %%this, i64 %%argc, ptr %%argv) {\nentry:\n%s}\n", name, body))
	return name
}
