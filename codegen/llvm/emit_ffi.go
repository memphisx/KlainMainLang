package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// emit_ffi.go — node:ffi Stage A (TDD-00164): dlopen/DynamicLibrary/dlsym/
// dlclose/suffix/types, typed calls through statically-resolved signature
// objects. A signature is a compile-time constant, so a bound function lowers
// to a plain indirect C-ABI `call` through the dlsym'd pointer — no libffi,
// no trampoline. The raw-memory helpers are Stage B; registerCallback (the
// closure→C-pointer trampoline) is Stage C.

// ffiCanonicalType normalizes a node:ffi type name (including every alias
// spelling) to its canonical form, or errors on an unknown name.
func ffiCanonicalType(name string, pos ast.Pos) (string, error) {
	switch name {
	case "void", "char", "int8", "uint8", "int16", "uint16", "int32", "uint32",
		"int64", "uint64", "float32", "float64", "pointer", "string", "buffer",
		"arraybuffer", "function":
		return name, nil
	case "i8":
		return "int8", nil
	case "u8", "bool":
		return "uint8", nil
	case "i16":
		return "int16", nil
	case "u16":
		return "uint16", nil
	case "i32":
		return "int32", nil
	case "u32":
		return "uint32", nil
	case "i64":
		return "int64", nil
	case "u64":
		return "uint64", nil
	case "f32", "float":
		return "float32", nil
	case "f64", "double":
		return "float64", nil
	case "ptr":
		return "pointer", nil
	case "str":
		return "string", nil
	}
	return "", fmt.Errorf("%d:%d: unknown FFI type name '%s'", pos.Line, pos.Col, name)
}

// ffiTypesConstants maps the ffi.types.* constant names to their literal string
// value in Node — the exact strings `ffi.types` holds (verified against a real
// `--experimental-ffi` build): `FLOAT` is `"float"` and `DOUBLE` is `"double"`
// (their canonical-name aliases, not `"float32"`/`"float64"`), and `BOOL` is
// `"bool"`. These are what `console.log(ffi.types.X)` must print; used in a
// signature they are run back through ffiCanonicalType (ffiTypeNameExpr), which
// maps `float`→`float32`, `double`→`float64`, `bool`→`uint8`.
var ffiTypesConstants = map[string]string{
	"VOID": "void", "POINTER": "pointer", "BUFFER": "buffer",
	"ARRAY_BUFFER": "arraybuffer", "FUNCTION": "function", "BOOL": "bool",
	"CHAR": "char", "STRING": "string", "FLOAT": "float", "DOUBLE": "double",
	"INT_8": "int8", "UINT_8": "uint8", "INT_16": "int16", "UINT_16": "uint16",
	"INT_32": "int32", "UINT_32": "uint32", "INT_64": "int64",
	"UINT_64": "uint64", "FLOAT_32": "float32", "FLOAT_64": "float64",
}

// ffiTypeNameExpr statically resolves one type-name position of a signature:
// a string literal ('int32') or an ffi.types.X constant.
func ffiTypeNameExpr(expr ast.Expression) (string, error) {
	pos := expr.GetPos()
	if lit, ok := expr.(*ast.StringLiteral); ok {
		return ffiCanonicalType(lit.Value, pos)
	}
	if mem, ok := expr.(*ast.MemberExpression); ok {
		if inner, ok := mem.Object.(*ast.MemberExpression); ok && inner.Property == "types" {
			if id, ok := inner.Object.(*ast.Identifier); ok && id.Name == "ffi__kml_builtin" {
				if c, present := ffiTypesConstants[mem.Property]; present {
					// The constant's value is Node's public spelling (e.g.
					// "double"/"float"/"bool"); canonicalize it for signature use.
					return ffiCanonicalType(c, pos)
				}
				return "", fmt.Errorf("%d:%d: unknown ffi.types constant '%s'", pos.Line, pos.Col, mem.Property)
			}
		}
	}
	return "", fmt.Errorf("%d:%d: an FFI type must be a string literal (or ffi.types constant) — the signature is resolved at compile time", pos.Line, pos.Col)
}

// ffiParseSignature statically resolves a `{ arguments: [...], return: '...' }`
// signature object. nil means the default `void ()` signature.
func ffiParseSignature(expr ast.Expression) (*FFISignature, error) {
	sig := &FFISignature{Ret: "void"}
	if expr == nil {
		return sig, nil
	}
	lit, ok := expr.(*ast.ObjectLiteral)
	if !ok {
		pos := expr.GetPos()
		return nil, fmt.Errorf("%d:%d: an FFI signature must be an object literal ({ arguments: [...], return: '...' }) — it is resolved at compile time", pos.Line, pos.Col)
	}
	for _, prop := range lit.Properties {
		pos := lit.GetPos()
		if prop.KeyExpr != nil {
			return nil, fmt.Errorf("%d:%d: an FFI signature cannot use computed property keys", pos.Line, pos.Col)
		}
		switch prop.Key {
		case "arguments":
			arr, ok := prop.Value.(*ast.ArrayLiteral)
			if !ok {
				return nil, fmt.Errorf("%d:%d: an FFI signature's 'arguments' must be an array literal of type names", pos.Line, pos.Col)
			}
			for _, el := range arr.Elements {
				c, err := ffiTypeNameExpr(el)
				if err != nil {
					return nil, err
				}
				if c == "void" {
					return nil, fmt.Errorf("%d:%d: 'void' is not a valid FFI argument type", pos.Line, pos.Col)
				}
				sig.Args = append(sig.Args, c)
			}
		case "return":
			c, err := ffiTypeNameExpr(prop.Value)
			if err != nil {
				return nil, err
			}
			switch c {
			case "buffer", "arraybuffer":
				return nil, fmt.Errorf("%d:%d: '%s' is not a valid FFI return type (use 'pointer' and the memory helpers)", pos.Line, pos.Col, c)
			}
			sig.Ret = c
		default:
			return nil, fmt.Errorf("%d:%d: unknown FFI signature key '%s' (expected 'arguments' and/or 'return')", pos.Line, pos.Col, prop.Key)
		}
	}
	return sig, nil
}

// ffiLLVMType returns the LLVM IR type string for a canonical FFI type name.
func ffiLLVMType(canonical string) string {
	switch canonical {
	case "void":
		return "void"
	case "char", "int8", "uint8":
		return "i8"
	case "int16", "uint16":
		return "i16"
	case "int32", "uint32":
		return "i32"
	case "int64", "uint64":
		return "i64"
	case "float32":
		return "float"
	case "float64":
		return "double"
	}
	return "ptr" // pointer, string, buffer, arraybuffer, function
}

// ffiReturnKmlType is the KML-visible type of a call returning `canonical`:
// 64-bit ints and pointers surface as bigint (matching node:ffi), narrower
// ints as number (i64), floats as number (f64), string as string|null.
func ffiReturnKmlType(canonical string) Type {
	switch canonical {
	case "void":
		return TypeVoid
	case "char", "int8", "uint8", "int16", "uint16", "int32", "uint32":
		return TypeI64
	case "int64", "uint64", "pointer", "function", "string", "buffer", "arraybuffer":
		// `string` is pointer-like: a call returning it yields the raw pointer
		// bigint, exactly like `pointer` (Node does not marshal the char* to a
		// JS string; read it with ffi.toString). Callers pass only primitive
		// accessor types here, so this affects only bound-function call returns.
		return BigIntType()
	case "float32", "float64":
		return TypeF64
	}
	return TypeVoid
}

// ffiSuffix is the host's shared-library filename suffix (ffi.suffix).
func (e *Emitter) ffiSuffix() string {
	switch e.opts.Target.OS() {
	case "darwin":
		return "dylib"
	case "windows":
		return "dll"
	}
	return "so"
}

// ffiRejectWindows was the guard for the not-yet-shimmed host; node:ffi now has
// a LoadLibrary/GetProcAddress-backed dlopen shim on Windows (FFIWinDlShimSource,
// wired through ensureFFIDl), so it is a no-op. Retained as the single point to
// re-gate from should a specific FFI surface prove Windows-incompatible.
func ffiRejectWindows(pos ast.Pos) error {
	_ = pos
	return nil
}

// emitFFIThrowOnNull throws a KML Error carrying dlerror()'s message (or a
// fallback) when ptrReg is null, then continues straight-line code.
func (e *Emitter) emitFFIThrowOnNull(ptrReg, fallback string) {
	e.ensureExceptionHelpers()
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, ptrReg))
	throwL := e.freshLabel("ffi.err")
	contL := e.freshLabel("ffi.cont")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, throwL, contL))
	e.emitLabel(throwL)
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @dlerror()", raw))
	rawNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", rawNull, raw))
	msg, _ := e.emitStrBranch(rawNull,
		func() (string, error) { return e.internString(fallback), nil },
		func() (string, error) {
			m := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", m, raw))
			return m, nil
		})
	errReg := e.buildErrorObj(0, msg, e.internString("Error"))
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errReg))
	e.emitTerminator("unreachable")
	e.emitLabel(contL)
}

// emitFFIOpenLibrary opens a library for `new DynamicLibrary(path)` /
// `ffi.dlopen(path)`: a string path, or null/undefined for the process image.
// Any other static type is Node's TypeError; a failed open is
// ERR_FFI_CALL_FAILED ("dlopen failed: …").
func (e *Emitter) emitFFIOpenLibrary(pathExpr ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureFFIRuntime()
	pathRef := "null"
	if pathExpr != nil {
		pv, err := e.emitExpr(pathExpr)
		if err != nil {
			return Value{}, err
		}
		switch {
		case pv.Ty.IsNull:
		case isStringTy(pv.Ty) && !pv.Ty.IsDynamic:
			pathRef = pv.Ref
		default:
			e.emitFFIThrowWhen("true", "TypeError", "ERR_INVALID_ARG_TYPE", e.internString("Library path must be a string or null"))
		}
	}
	out := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", out))
	st := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_ffi_open(ptr %s, ptr %s)", st, pathRef, out))
	e.emitFFIStatusCheck(st, nil)
	lib := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", lib, out))
	return Value{Ref: lib, Ty: FFILibraryType()}, nil
}

// ffiParsedDef is one statically-parsed definition: the signature, or the
// runtime error Node would throw for it (rtErr, "%s" = the function name).
type ffiParsedDef struct {
	name  string
	sig   *FFISignature
	rtErr string
	code  string // the error's code/kind: ERR_INVALID_ARG_VALUE (TypeError) or ERR_INVALID_ARG_TYPE (TypeError)
}

// ffiParseSignatureRT parses a signature the way Node's ParseFunctionSignature
// does, turning every invalid-but-static shape into the runtime TypeError Node
// throws (Go error only for a shape that has no AOT lowering: a signature,
// argument list or type name computed at run time). Only `return` and
// `arguments` are read; other keys are ignored, as in Node.
func ffiParseSignatureRT(expr ast.Expression) (sig *FFISignature, rtErr string, err error) {
	sig = &FFISignature{Ret: "void"}
	expr = ffiStripTS(expr)
	lit, ok := expr.(*ast.ObjectLiteral)
	if !ok {
		switch expr.(type) {
		case *ast.ArrayLiteral, *ast.StringLiteral, *ast.NumberLiteral, *ast.BooleanLiteral, *ast.NullLiteral, *ast.TemplateLiteral:
			return sig, "\x00Function signature must be an object", nil
		}
		pos := expr.GetPos()
		return nil, "", fmt.Errorf("%d:%d: an FFI signature must be an object literal ({ arguments: [...], return: '...' }) — it is resolved at compile time", pos.Line, pos.Col)
	}
	var retExpr, argsExpr ast.Expression
	for _, prop := range lit.Properties {
		if prop.KeyExpr != nil {
			pos := lit.GetPos()
			return nil, "", fmt.Errorf("%d:%d: an FFI signature cannot use computed property keys", pos.Line, pos.Col)
		}
		switch prop.Key {
		case "return":
			retExpr = prop.Value
		case "arguments":
			argsExpr = prop.Value
		}
	}
	typeName := func(el ast.Expression) (name string, isStr bool, err error) {
		switch t := el.(type) {
		case *ast.StringLiteral:
			return t.Value, true, nil
		case *ast.NumberLiteral, *ast.BooleanLiteral, *ast.NullLiteral, *ast.ArrayLiteral, *ast.ObjectLiteral:
			return "", false, nil
		case *ast.MemberExpression:
			if inner, ok := t.Object.(*ast.MemberExpression); ok && inner.Property == "types" {
				if id, ok := inner.Object.(*ast.Identifier); ok && id.Name == "ffi__kml_builtin" {
					if c, present := ffiTypesConstants[t.Property]; present {
						return c, true, nil
					}
					return "", false, nil // undefined — not a string
				}
			}
		}
		pos := el.GetPos()
		return "", false, fmt.Errorf("%d:%d: an FFI type must be a string literal (or ffi.types constant) — the signature is resolved at compile time", pos.Line, pos.Col)
	}
	if retExpr != nil {
		n, isStr, err := typeName(retExpr)
		if err != nil {
			return nil, "", err
		}
		if !isStr {
			return sig, "Return value type of function %s must be a string", nil
		}
		c, cerr := ffiCanonicalType(n, retExpr.GetPos())
		if cerr != nil {
			return sig, "\x00Unsupported FFI type: " + n, nil
		}
		sig.Ret = c
	}
	if argsExpr != nil {
		arr, ok := argsExpr.(*ast.ArrayLiteral)
		if !ok {
			switch argsExpr.(type) {
			case *ast.StringLiteral, *ast.NumberLiteral, *ast.BooleanLiteral, *ast.NullLiteral, *ast.ObjectLiteral, *ast.TemplateLiteral:
				return sig, "Arguments list of function %s must be an array", nil
			}
			pos := argsExpr.GetPos()
			return nil, "", fmt.Errorf("%d:%d: an FFI signature's 'arguments' must be an array literal of type names", pos.Line, pos.Col)
		}
		for i, el := range arr.Elements {
			n, isStr, err := typeName(el)
			if err != nil {
				return nil, "", err
			}
			if !isStr {
				return sig, fmt.Sprintf("Argument %d of function %%s must be a string", i), nil
			}
			c, cerr := ffiCanonicalType(n, el.GetPos())
			if cerr != nil {
				return sig, "\x00Unsupported FFI type: " + n, nil
			}
			if c == "void" {
				return sig, fmt.Sprintf("Argument %d of function %%s must not be 'void'; use an empty array for no-argument functions", i), nil
			}
			sig.Args = append(sig.Args, c)
		}
	}
	return sig, "", nil
}

// ffiStripTS looks through the TS-only wrappers type stripping removes
// (`x as T`, `x!`), so `{ … } as any` parses as the literal it is.
func ffiStripTS(expr ast.Expression) ast.Expression {
	for {
		switch w := expr.(type) {
		case *ast.AsExpression:
			expr = w.Expr
		case *ast.NonNullExpression:
			expr = w.Arg
		default:
			return expr
		}
	}
}

// ffiRTErrMessage builds the runtime message of a parse error for function
// `nameRef` (a KML string register). A leading NUL marks a message with no
// name slot (and, for "Function signature must be an object", a type error).
func (e *Emitter) ffiRTErrMessage(rtErr, nameRef string) (string, error) {
	if strings.HasPrefix(rtErr, "\x00") {
		return e.internString(rtErr[1:]), nil
	}
	i := strings.Index(rtErr, "%s")
	pre := Value{Ref: e.internString(rtErr[:i]), Ty: TypePtr}
	mid, err := e.emitStringConcat(pre, Value{Ref: nameRef, Ty: TypePtr})
	if err != nil {
		return "", err
	}
	out, err := e.emitStringConcat(mid, Value{Ref: e.internString(rtErr[i+2:]), Ty: TypePtr})
	if err != nil {
		return "", err
	}
	return out.Ref, nil
}

// ffiRTErrCode is the error code of a parse error (ERR_INVALID_ARG_TYPE for a
// non-object signature, ERR_INVALID_ARG_VALUE for the rest).
func ffiRTErrCode(rtErr string) string {
	if rtErr == "\x00Function signature must be an object" {
		return "ERR_INVALID_ARG_TYPE"
	}
	return "ERR_INVALID_ARG_VALUE"
}

// ffiDefinitionsType statically derives the `functions` object type of a
// dlopen/getFunctions definitions literal: one bound function per key, in the
// literal's order, with a null prototype.
func ffiDefinitionsType(defs *ast.ObjectLiteral) (Type, error) {
	var fields []Field
	for _, prop := range defs.Properties {
		if prop.KeyExpr != nil {
			pos := defs.GetPos()
			return Type{}, fmt.Errorf("%d:%d: FFI definitions cannot use computed property keys", pos.Line, pos.Col)
		}
		sig, _, err := ffiParseSignatureRT(prop.Value)
		if err != nil {
			return Type{}, err
		}
		fields = append(fields, Field{Name: prop.Key, Ty: FFIFunctionType(sig)})
	}
	ty := ObjectType(fields)
	ty.IsNullProtoObject = true
	return ty, nil
}

// emitFFINameArg validates a function/symbol name argument as Node does
// (a string without NUL bytes; `what` is "Function" or "Symbol") and returns
// its pointer and byte length.
func (e *Emitter) emitFFINameArg(nameExpr ast.Expression, what string) (ptr, length string, err error) {
	nv, err := e.emitExpr(nameExpr)
	if err != nil {
		return "", "", err
	}
	if !isStringTy(nv.Ty) || nv.Ty.IsDynamic {
		e.emitFFIThrowWhen("true", "TypeError", "ERR_INVALID_ARG_TYPE", e.internString(what+" name must be a string"))
		return e.internString(""), "0", nil
	}
	return e.emitFFINameChecked(nv.Ref, what), e.ffiLastLen, nil
}

// emitFFINameChecked rejects a name holding a NUL byte (its length header
// disagreeing with strlen) and records the length in e.ffiLastLen.
func (e *Emitter) emitFFINameChecked(nameRef, what string) string {
	l := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", l, nameRef))
	sl := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", sl, nameRef))
	nul := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %s", nul, l, sl))
	e.emitFFIThrowWhen(nul, "TypeError", "ERR_INVALID_ARG_VALUE", e.internString(what+" name must not contain null bytes"))
	e.ffiLastLen = l
	return nameRef
}

// emitFFIPrepare runs PrepareFunction for one name/signature and returns the
// registry entry and its freshness. A static parse error throws first, in
// Node's order (after the name checks, before any lookup).
func (e *Emitter) emitFFIPrepare(libPtr, nameRef, nameLen string, sig *FFISignature, rtErr string, onFail func()) (fnReg, freshReg string, err error) {
	if rtErr != "" {
		msg, err := e.ffiRTErrMessage(rtErr, nameRef)
		if err != nil {
			return "", "", err
		}
		if onFail != nil {
			throwL := e.freshLabel("ffi.sig.throw")
			contL := e.freshLabel("ffi.sig.ok")
			e.emitTerminator(fmt.Sprintf("br i1 true, label %%%s, label %%%s", throwL, contL))
			e.emitLabel(throwL)
			onFail()
			e.emitThrowCoded("TypeError", ffiRTErrCode(rtErr), msg)
			e.emitLabel(contL)
		} else {
			e.emitFFIThrowWhen("true", "TypeError", ffiRTErrCode(rtErr), msg)
		}
	}
	out := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", out))
	fresh := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", fresh))
	st := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_ffi_prepare(ptr %s, ptr %s, i64 %s, ptr %s, ptr %s, ptr %s)",
		st, libPtr, nameRef, nameLen, e.internString(e.ffiSigKey(sig)), out, fresh))
	e.emitFFIStatusCheck(st, onFail)
	fnReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fnReg, out))
	freshReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", freshReg, fresh))
	return fnReg, freshReg, nil
}

// emitFFIGetFunctionsDefs implements getFunctions(definitions) (and dlopen's
// definitions): every definition is prepared first — one failure caches
// nothing — then committed and turned into its function object, and the
// result is a null-prototype object in the literal's key order.
func (e *Emitter) emitFFIGetFunctionsDefs(libPtr string, defsExpr ast.Expression, onFail func()) (Value, error) {
	defsExpr = ffiStripTS(defsExpr)
	defs, ok := defsExpr.(*ast.ObjectLiteral)
	if !ok {
		switch defsExpr.(type) {
		case *ast.ArrayLiteral, *ast.StringLiteral, *ast.NumberLiteral, *ast.BooleanLiteral, *ast.NullLiteral, *ast.TemplateLiteral:
			if onFail != nil {
				throwL := e.freshLabel("ffi.defs.throw")
				contL := e.freshLabel("ffi.defs.ok")
				e.emitTerminator(fmt.Sprintf("br i1 true, label %%%s, label %%%s", throwL, contL))
				e.emitLabel(throwL)
				onFail()
				e.emitThrowCoded("TypeError", "ERR_INVALID_ARG_TYPE", e.internString("Functions signatures must be an object"))
				e.emitLabel(contL)
			} else {
				e.emitFFIThrowWhen("true", "TypeError", "ERR_INVALID_ARG_TYPE", e.internString("Functions signatures must be an object"))
			}
			ty := ObjectType(nil)
			ty.IsNullProtoObject = true
			return Value{Ref: "null", Ty: ty}, nil
		}
		pos := defsExpr.GetPos()
		return Value{}, fmt.Errorf("%d:%d: FFI definitions must be an object literal — signatures are resolved at compile time", pos.Line, pos.Col)
	}
	funcsTy, err := ffiDefinitionsType(defs)
	if err != nil {
		return Value{}, err
	}
	type prepared struct{ fn, fresh string }
	var preps []prepared
	for _, prop := range defs.Properties {
		sig, rtErr, err := ffiParseSignatureRT(prop.Value)
		if err != nil {
			return Value{}, err
		}
		if strings.HasPrefix(rtErr, "\x00Function signature must be an object") {
			rtErr = "\x00Signature of function " + prop.Key + " must be an object"
		}
		nameRef := e.internString(prop.Key)
		nameLen := fmt.Sprintf("%d", len(prop.Key))
		if strings.ContainsRune(prop.Key, 0) {
			e.emitFFIThrowWhen("true", "TypeError", "ERR_INVALID_ARG_VALUE", e.internString("Function name must not contain null bytes"))
		}
		fn, fresh, err := e.emitFFIPrepare(libPtr, nameRef, nameLen, sig, rtErr, onFail)
		if err != nil {
			return Value{}, err
		}
		preps = append(preps, prepared{fn, fresh})
	}
	obj := e.freshReg()
	size := funcsTy.StructSize()
	if size == 0 {
		size = 8
	}
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", obj, size))
	for i, f := range funcsTy.Fields {
		rec := e.emitFFIRecordFor(libPtr, preps[i].fn, preps[i].fresh, f.Ty.FFISig)
		e.storeSQLiteField(funcsTy, obj, f.Name, "ptr", rec)
	}
	return Value{Ref: obj, Ty: funcsTy}, nil
}

// ffiDlopenResultType derives ffi.dlopen(...)'s `{ lib, functions }` type —
// shared by codegen and inferExprType so both agree.
func ffiDlopenResultType(args []ast.Expression) (Type, error) {
	funcsTy := ObjectType(nil)
	funcsTy.IsNullProtoObject = true
	if len(args) >= 2 {
		if defs, ok := ffiStripTS(args[1]).(*ast.ObjectLiteral); ok {
			var err error
			funcsTy, err = ffiDefinitionsType(defs)
			if err != nil {
				return Type{}, err
			}
		}
	}
	return ObjectType([]Field{
		{Name: "lib", Ty: FFILibraryType()},
		{Name: "functions", Ty: funcsTy},
	}), nil
}

// emitFFIModuleCall dispatches ffi.dlopen / ffi.dlclose / ffi.dlsym and the
// raw-memory helpers.
func (e *Emitter) emitFFIModuleCall(prop string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if err := ffiRejectWindows(pos); err != nil {
		return Value{}, err
	}
	switch prop {
	case "dlopen":
		if len(args) < 1 || len(args) > 2 {
			return Value{}, fmt.Errorf("%d:%d: ffi.dlopen takes (path, definitions?)", pos.Line, pos.Col)
		}
		resTy, err := ffiDlopenResultType(args)
		if err != nil {
			return Value{}, err
		}
		lib, err := e.emitFFIOpenLibrary(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		var funcs Value
		if len(args) == 2 {
			// A failing definition closes the library before the error
			// propagates, as Node's dlopen does.
			closeLib := func() { e.emitInstr(fmt.Sprintf("call void @__kml_ffi_close(ptr %s)", lib.Ref)) }
			funcs, err = e.emitFFIGetFunctionsDefs(lib.Ref, args[1], closeLib)
			if err != nil {
				return Value{}, err
			}
		} else {
			// No definitions: a frozen empty null-prototype object.
			emptyObj := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 8)", emptyObj))
			e.emitStaticIntegrity(emptyObj, staticIntegrityFrozen)
			ty := ObjectType(nil)
			ty.IsNullProtoObject = true
			funcs = Value{Ref: emptyObj, Ty: ty}
		}
		obj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", obj, resTy.StructSize()))
		e.storeSQLiteField(resTy, obj, "lib", "ptr", lib.Ref)
		e.storeSQLiteField(resTy, obj, "functions", "ptr", funcs.Ref)
		return Value{Ref: obj, Ty: resTy}, nil

	case "dlclose":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: ffi.dlclose takes exactly 1 argument (a DynamicLibrary)", pos.Line, pos.Col)
		}
		return e.emitFFILibraryMethod(args[0], "close", nil, pos)

	case "dlsym":
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: ffi.dlsym takes exactly 2 arguments (library, symbol)", pos.Line, pos.Col)
		}
		return e.emitFFILibraryMethod(args[0], "getSymbol", args[1:], pos)

	case "toString":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: ffi.toString takes exactly 1 argument (pointer)", pos.Line, pos.Col)
		}
		e.ensureFFIDl()
		_, p, err := e.emitFFIPointerArg(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		return e.emitFFIMarshalReturn(p, "string")

	case "toBuffer", "toArrayBuffer":
		return e.emitFFIToBuffer(prop, args, pos)

	case "exportString":
		return e.emitFFIExportString(args, pos)

	case "exportBuffer", "exportArrayBuffer", "exportArrayBufferView":
		return e.emitFFIExportBytes(prop, args, pos)

	case "getRawPointer":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: ffi.getRawPointer takes exactly 1 argument (Buffer/ArrayBuffer/view)", pos.Line, pos.Col)
		}
		p, _, err := e.emitFFIByteSource(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		return e.ffiPtrToBigInt(p), nil
	}
	if canonical, ok := ffiAccessorTypes[strings.TrimPrefix(prop, "get")]; ok && strings.HasPrefix(prop, "get") {
		return e.emitFFIGetPrimitive(canonical, args, pos)
	}
	if canonical, ok := ffiAccessorTypes[strings.TrimPrefix(prop, "set")]; ok && strings.HasPrefix(prop, "set") {
		return e.emitFFISetPrimitive(canonical, args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: ffi.%s is not supported yet", pos.Line, pos.Col, prop)
}

// ffiAccessorTypes maps the get*/set* primitive-accessor name suffix to the
// canonical FFI type it reads/writes.
var ffiAccessorTypes = map[string]string{
	"Int8": "int8", "Uint8": "uint8", "Int16": "int16", "Uint16": "uint16",
	"Int32": "int32", "Uint32": "uint32", "Int64": "int64", "Uint64": "uint64",
	"Float32": "float32", "Float64": "float64",
}

// emitFFII64Arg lowers a length/offset argument to an i64 register — a plain
// number, or a bigint (lengths are frequently written as literals like 64n).
func (e *Emitter) emitFFII64Arg(expr ast.Expression) (string, error) {
	v, err := e.emitExpr(expr)
	if err != nil {
		return "", err
	}
	if v.Ty.IsBigInt {
		e.ensureBigInt()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_bigint_to_i64(ptr %s)", r, v.Ref))
		return r, nil
	}
	return e.coerce(v, TypeI64).Ref, nil
}

// emitFFIPtrPlusOffset lowers (pointerExpr, optional offsetExpr) to a raw
// address register: the pointer bigint plus a byte offset.
func (e *Emitter) emitFFIPtrPlusOffset(ptrExpr, offExpr ast.Expression, pos ast.Pos) (string, error) {
	_, base, err := e.emitFFIPointerArg(ptrExpr, pos)
	if err != nil {
		return "", err
	}
	if offExpr == nil {
		return base, nil
	}
	off, err := e.emitFFII64Arg(offExpr)
	if err != nil {
		return "", err
	}
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", p, base, off))
	return p, nil
}

// emitFFIGetPrimitive implements ffi.getInt8..getFloat64(pointer[, offset]).
func (e *Emitter) emitFFIGetPrimitive(canonical string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: an FFI primitive getter takes (pointer, offset?)", pos.Line, pos.Col)
	}
	var offExpr ast.Expression
	if len(args) == 2 {
		offExpr = args[1]
	}
	p, err := e.emitFFIPtrPlusOffset(args[0], offExpr, pos)
	if err != nil {
		return Value{}, err
	}
	irTy := ffiLLVMType(canonical)
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 1", raw, irTy, p))
	return e.emitFFIMarshalReturn(raw, canonical)
}

// emitFFISetPrimitive implements ffi.setInt8..setFloat64(pointer, offset, value).
func (e *Emitter) emitFFISetPrimitive(canonical string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 3 {
		return Value{}, fmt.Errorf("%d:%d: an FFI primitive setter takes (pointer, offset, value)", pos.Line, pos.Col)
	}
	p, err := e.emitFFIPtrPlusOffset(args[0], args[1], pos)
	if err != nil {
		return Value{}, err
	}
	irTy, ref, err := e.emitFFIMarshalArg(args[2], canonical, pos)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 1", irTy, ref, p))
	return Value{Ty: TypeVoid}, nil
}

// emitFFIToBuffer implements ffi.toBuffer/toArrayBuffer(pointer, length[, copy]).
// copy defaults to true (a fresh malloc'd copy); copy=false wraps the native
// memory zero-copy — the caller keeps it valid, exactly node:ffi's contract.
func (e *Emitter) emitFFIToBuffer(prop string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: ffi.%s takes (pointer, length, copy?)", pos.Line, pos.Col, prop)
	}
	src, err := e.emitFFIPtrPlusOffset(args[0], nil, pos)
	if err != nil {
		return Value{}, err
	}
	lenRef, err := e.emitFFII64Arg(args[1])
	if err != nil {
		return Value{}, err
	}

	e.ensureMalloc()
	e.ensureMemcpy()
	dataSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", dataSlot))

	copyCond := "true" // omitted → copy
	if len(args) == 3 {
		cv, err := e.emitExpr(args[2])
		if err != nil {
			return Value{}, err
		}
		copyCond = e.coerce(cv, TypeBool).Ref
	}
	copyL := e.freshLabel("ffi.copy")
	rawL := e.freshLabel("ffi.nocopy")
	doneL := e.freshLabel("ffi.wrapped")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", copyCond, copyL, rawL))
	e.emitLabel(copyL)
	m := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", m, lenRef))
	cp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @memcpy(ptr %s, ptr %s, i64 %s)", cp, m, src, lenRef))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", m, dataSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(rawL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", src, dataSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	data := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", data, dataSlot))

	if prop == "toBuffer" {
		return e.bufferAggregate(data, lenRef), nil
	}
	// toArrayBuffer: build the hidden { i64 byteLength, ptr data } header.
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdr))
	lenGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 0", lenGep, hdr))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenRef, lenGep))
	dataGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", dataGep, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", data, dataGep))
	return Value{Ref: hdr, Ty: ArrayBufferType()}, nil
}

// emitFFIByteSource lowers a Buffer/TypedArray/DataView/ArrayBuffer value to
// its (data pointer, byte length) registers.
func (e *Emitter) emitFFIByteSource(argExpr ast.Expression, pos ast.Pos) (ptrReg, byteLenReg string, err error) {
	ty := e.inferExprType(argExpr)
	switch {
	case ty.IsArrayBuffer:
		v, err := e.emitExpr(argExpr)
		if err != nil {
			return "", "", err
		}
		lenGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 0", lenGep, v.Ref))
		l := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", l, lenGep))
		dataGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", dataGep, v.Ref))
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", p, dataGep))
		return p, l, nil

	case ty.IsDataView:
		v, err := e.emitExpr(argExpr)
		if err != nil {
			return "", "", err
		}
		// { ptr data, i64 byteLength, i64 byteOffset, ptr bufHdr }
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", p, v.Ref))
		lenGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, i64, i64, ptr }, ptr %s, i32 0, i32 1", lenGep, v.Ref))
		l := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", l, lenGep))
		return p, l, nil

	case ty.IsArray || ty.IsBuffer || ty.IsTypedArray:
		p, lenReg, elemTy, err := e.resolveArrayForHOF(argExpr, pos)
		if err != nil {
			return "", "", err
		}
		elemSize := int64(elemTy.Align())
		if elemSize <= 1 {
			return p, lenReg, nil
		}
		bl := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bl, lenReg, elemSize))
		return p, bl, nil
	}
	return "", "", fmt.Errorf("%d:%d: expected a Buffer, TypedArray, DataView, or ArrayBuffer", pos.Line, pos.Col)
}

// emitFFIThrowMsgOnCond throws a KML Error with a fixed name/message when
// condReg is true.
func (e *Emitter) emitFFIThrowMsgOnCond(condReg, name, msg string) {
	e.ensureExceptionHelpers()
	throwL := e.freshLabel("ffi.err")
	contL := e.freshLabel("ffi.cont")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", condReg, throwL, contL))
	e.emitLabel(throwL)
	errReg := e.buildErrorObj(0, e.internString(msg), e.internString(name))
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errReg))
	e.emitTerminator("unreachable")
	e.emitLabel(contL)
}

// emitFFIExportString implements ffi.exportString(string, pointer, length[,
// encoding]): copy the UTF-8 bytes into native memory, NUL-terminated,
// truncating to the capacity.
func (e *Emitter) emitFFIExportString(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 3 || len(args) > 4 {
		return Value{}, fmt.Errorf("%d:%d: ffi.exportString takes (string, pointer, length, encoding?)", pos.Line, pos.Col)
	}
	e.ensureFFIRuntime()
	// Node's order (lib/ffi.js): validateString(str), validateString(encoding),
	// validateInteger(len, 0), then Buffer.from(str, encoding) — an unknown
	// encoding throws there — then the capacity check against the encoded
	// bytes plus a terminator (2 bytes for utf16le/ucs2).
	sv, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(sv.Ty) || sv.Ty.IsDynamic {
		// Node's message ends with the inspected value: "… Received type number (5)".
		boxed, berr := e.emitBoxValue(sv)
		if berr != nil {
			return Value{}, berr
		}
		shown, serr := e.emitDynamicInspect(boxed)
		if serr != nil {
			return Value{}, serr
		}
		msg, cerr := e.emitStringConcat(Value{Ref: e.internString(fmt.Sprintf("The \"string\" argument must be of type string. Received type %s (", typeofString(sv.Ty))), Ty: TypePtr}, shown)
		if cerr != nil {
			return Value{}, cerr
		}
		msg, cerr = e.emitStringConcat(msg, Value{Ref: e.internString(")"), Ty: TypePtr})
		if cerr != nil {
			return Value{}, cerr
		}
		e.emitFFIThrowWhen("true", "TypeError", "ERR_INVALID_ARG_TYPE", msg.Ref)
		return Value{Ty: TypeVoid}, nil
	}
	dst, err := e.emitFFIPtrPlusOffset(args[1], nil, pos)
	if err != nil {
		return Value{}, err
	}
	enc := "utf8"
	encName := "utf8"
	if len(args) == 4 {
		lit, ok := ffiStripTS(args[3]).(*ast.StringLiteral)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: ffi.exportString's encoding must be a string literal (the codec is chosen at compile time)", pos.Line, pos.Col)
		}
		encName = lit.Value
		if c, err := bufferEncodingArg([]ast.Expression{lit}, 0, pos); err == nil {
			enc = c
		} else {
			enc = ""
		}
	}
	lv, err := e.emitExpr(args[2])
	if err != nil {
		return Value{}, err
	}
	if lv.Ty.IsBigInt || !isNumberTy(lv.Ty) || lv.Ty.IsDynamic {
		msg := fmt.Sprintf("The \"len\" argument must be of type number. Received type %s", typeofString(lv.Ty))
		if lv.Ty.IsBigInt {
			s, serr := e.emitBigIntToString(lv, true)
			if serr != nil {
				return Value{}, serr
			}
			full, cerr := e.emitStringConcat(Value{Ref: e.internString(msg + " ("), Ty: TypePtr}, s)
			if cerr != nil {
				return Value{}, cerr
			}
			full, cerr = e.emitStringConcat(full, Value{Ref: e.internString(")"), Ty: TypePtr})
			if cerr != nil {
				return Value{}, cerr
			}
			e.emitFFIThrowWhen("true", "TypeError", "ERR_INVALID_ARG_TYPE", full.Ref)
		} else {
			e.emitFFIThrowWhen("true", "TypeError", "ERR_INVALID_ARG_TYPE", e.internString(msg))
		}
		return Value{Ty: TypeVoid}, nil
	}
	d := e.coerce(lv, TypeF64).Ref
	// validateInteger(len, 'len', 0): an integer in [0, MAX_SAFE_INTEGER].
	e.ensureSprintf()
	notInt := e.freshReg()
	tr := e.freshReg()
	e.ensureMathFuncs()
	e.emitInstr(fmt.Sprintf("%s = call double @trunc(double %s)", tr, d))
	e.emitInstr(fmt.Sprintf("%s = fcmp une double %s, %s", notInt, tr, d))
	intMsg, err := e.ffiOutOfRangeMsg("an integer", d)
	if err != nil {
		return Value{}, err
	}
	e.emitFFIThrowWhen(notInt, "RangeError", "ERR_OUT_OF_RANGE", intMsg)
	outRange := e.freshReg()
	lo := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fcmp olt double %s, 0.0", lo, d))
	hi := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fcmp ogt double %s, 9007199254740991.0", hi, d))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", outRange, lo, hi))
	rangeMsg, err := e.ffiOutOfRangeMsg(">= 0 && <= 9007199254740991", d)
	if err != nil {
		return Value{}, err
	}
	e.emitFFIThrowWhen(outRange, "RangeError", "ERR_OUT_OF_RANGE", rangeMsg)
	if enc == "" {
		e.emitFFIThrowWhen("true", "TypeError", "ERR_UNKNOWN_ENCODING", e.internString("Unknown encoding: "+encName))
		return Value{Ty: TypeVoid}, nil
	}
	capRef := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fptosi double %s to i64", capRef, d))
	src, n := e.emitBufferDecodeString(sv.Ref, enc)
	term := 1
	if enc == "utf16le" {
		term = 2
	}
	needed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", needed, n, term))
	small := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", small, capRef, needed))
	// "The value of "len" is out of range. It must be >= <needed>. Received <len>"
	neededD := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", neededD, needed))
	neededS, err := e.emitValueToString(Value{Ref: neededD, Ty: TypeF64})
	if err != nil {
		return Value{}, err
	}
	head, err := e.emitStringConcat(Value{Ref: e.internString(">= "), Ty: TypePtr}, neededS)
	if err != nil {
		return Value{}, err
	}
	smallMsg, err := e.ffiOutOfRangeMsgRef(head.Ref, d)
	if err != nil {
		return Value{}, err
	}
	e.emitFFIThrowWhen(small, "RangeError", "ERR_OUT_OF_RANGE", smallMsg)
	e.ensureMemcpy()
	cp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @memcpy(ptr %s, ptr %s, i64 %s)", cp, dst, src, n))
	for i := 0; i < term; i++ {
		off := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", off, n, i))
		end := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", end, dst, off))
		e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", end))
	}
	return Value{Ty: TypeVoid}, nil
}

// ffiOutOfRangeMsg builds Node's ERR_OUT_OF_RANGE text for `len`:
// `The value of "len" is out of range. It must be <range>. Received <d>`.
func (e *Emitter) ffiOutOfRangeMsg(rangeText, d string) (string, error) {
	return e.ffiOutOfRangeMsgRef(e.internString(rangeText), d)
}

func (e *Emitter) ffiOutOfRangeMsgRef(rangeRef, d string) (string, error) {
	pre, err := e.emitStringConcat(Value{Ref: e.internString(`The value of "len" is out of range. It must be `), Ty: TypePtr}, Value{Ref: rangeRef, Ty: TypePtr})
	if err != nil {
		return "", err
	}
	mid, err := e.emitStringConcat(pre, Value{Ref: e.internString(". Received "), Ty: TypePtr})
	if err != nil {
		return "", err
	}
	ds, err := e.emitValueToString(Value{Ref: d, Ty: TypeF64})
	if err != nil {
		return "", err
	}
	out, err := e.emitStringConcat(mid, ds)
	if err != nil {
		return "", err
	}
	return out.Ref, nil
}

// emitFFIExportBytes implements ffi.exportBuffer/exportArrayBuffer/
// exportArrayBufferView(src, pointer, length): copy the source's bytes into
// native memory; a capacity smaller than the source throws, as in Node.
func (e *Emitter) emitFFIExportBytes(prop string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 3 {
		return Value{}, fmt.Errorf("%d:%d: ffi.%s takes (source, pointer, length)", pos.Line, pos.Col, prop)
	}
	src, srcLen, err := e.emitFFIByteSource(args[0], pos)
	if err != nil {
		return Value{}, err
	}
	dst, err := e.emitFFIPtrPlusOffset(args[1], nil, pos)
	if err != nil {
		return Value{}, err
	}
	// Node validates the length as a number (`validateInteger`), throwing
	// ERR_INVALID_ARG_TYPE on a bigint — match that (a bigint length is a static
	// type error here, the AOT equivalent of Node's runtime throw).
	if e.inferExprType(args[2]).IsBigInt {
		return Value{}, fmt.Errorf("%d:%d: ffi export: the length argument must be a number, not a bigint", pos.Line, pos.Col)
	}
	capRef, err := e.emitFFII64Arg(args[2])
	if err != nil {
		return Value{}, err
	}
	small := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", small, capRef, srcLen))
	e.emitFFIThrowMsgOnCond(small, "RangeError", fmt.Sprintf("ffi.%s: length is smaller than the source's byteLength", prop))
	e.ensureMemcpy()
	cp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @memcpy(ptr %s, ptr %s, i64 %s)", cp, dst, src, srcLen))
	return Value{Ty: TypeVoid}, nil
}

// emitFFILibraryMethod dispatches methods on a DynamicLibrary value.
func (e *Emitter) emitFFILibraryMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if err := ffiRejectWindows(pos); err != nil {
		return Value{}, err
	}
	e.ensureFFIRuntime()
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	if !objVal.Ty.IsFFILibrary {
		return Value{}, fmt.Errorf("%d:%d: '%s' requires a DynamicLibrary receiver", pos.Line, pos.Col, method)
	}
	lib := objVal.Ref

	switch method {
	case "getFunction":
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: getFunction takes exactly 2 arguments (name, signature)", pos.Line, pos.Col)
		}
		sig, rtErr, err := ffiParseSignatureRT(args[1])
		if err != nil {
			return Value{}, err
		}
		// Node's order: the name's type, the signature's type, the name's
		// NUL bytes, then the signature's contents.
		nv, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		if !isStringTy(nv.Ty) || nv.Ty.IsDynamic {
			e.emitFFIThrowWhen("true", "TypeError", "ERR_INVALID_ARG_TYPE", e.internString("Function name must be a string"))
			return e.ffiZeroFn(sig), nil
		}
		if ffiRTErrCode(rtErr) == "ERR_INVALID_ARG_TYPE" && rtErr != "" {
			e.emitFFIThrowWhen("true", "TypeError", "ERR_INVALID_ARG_TYPE", e.internString("Function signature must be an object"))
			return e.ffiZeroFn(sig), nil
		}
		name := e.emitFFINameChecked(nv.Ref, "Function")
		fn, fresh, err := e.emitFFIPrepare(lib, name, e.ffiLastLen, sig, rtErr, nil)
		if err != nil {
			return Value{}, err
		}
		rec := e.emitFFIRecordFor(lib, fn, fresh, sig)
		return Value{Ref: rec, Ty: FFIFunctionType(sig)}, nil

	case "getFunctions":
		// With no arguments, Node returns the accumulator of everything
		// previously resolved — same as the `functions` property.
		if len(args) == 0 {
			return e.emitFFIAccumulator(lib, true), nil
		}
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: getFunctions takes a definitions object (or no argument for the previously-resolved set)", pos.Line, pos.Col)
		}
		return e.emitFFIGetFunctionsDefs(lib, args[0], nil)

	case "getSymbol":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: getSymbol takes exactly 1 argument (name)", pos.Line, pos.Col)
		}
		name, nameLen, err := e.emitFFINameArg(args[0], "Symbol")
		if err != nil {
			return Value{}, err
		}
		out := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", out))
		st := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_ffi_get_symbol(ptr %s, ptr %s, i64 %s, ptr %s)", st, lib, name, nameLen, out))
		e.emitFFIStatusCheck(st, nil)
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", p, out))
		return e.ffiPtrToBigInt(p), nil

	case "getSymbols":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: getSymbols takes no arguments (it returns the previously-resolved symbol addresses)", pos.Line, pos.Col)
		}
		return e.emitFFIAccumulator(lib, false), nil

	case "close":
		// Idempotent (the registry ignores a second close).
		e.emitInstr(fmt.Sprintf("call void @__kml_ffi_close(ptr %s)", lib))
		return Value{Ty: TypeVoid}, nil

	case "registerCallback":
		return e.emitFFIRegisterCallback(args, pos)

	case "unregisterCallback":
		return e.emitFFIUnregisterCallback(args, pos)

	case "refCallback", "unrefCallback":
		// Registered callbacks are always strongly referenced here (the
		// registration table is scanned global data in every memory mode), so
		// ref/unref reduce to validated no-ops; an unref'd-then-collected
		// callback can therefore never hit Node's zero-return path via these.
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: %s takes exactly 1 argument (the callback pointer)", pos.Line, pos.Col, method)
		}
		if _, _, err := e.emitFFIPointerArg(args[0], pos); err != nil {
			return Value{}, err
		}
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: unknown DynamicLibrary method '%s'", pos.Line, pos.Col, method)
}

// ffiZeroFn is a placeholder bound-function value after an unconditional
// throw.
func (e *Emitter) ffiZeroFn(sig *FFISignature) Value {
	return Value{Ref: "null", Ty: FFIFunctionType(sig)}
}

// emitFFILibraryProperty lowers `library.functions` / `library.symbols`: a
// fresh null-prototype object of every previously resolved function/address.
func (e *Emitter) emitFFILibraryProperty(objExpr ast.Expression, prop string, pos ast.Pos) (Value, error) {
	e.ensureFFIRuntime()
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	return e.emitFFIAccumulator(objVal.Ref, prop == "functions"), nil
}

// emitNewDynamicLibrary implements `new DynamicLibrary(path)` (TDD-00164).
func (e *Emitter) emitNewDynamicLibrary(ex *ast.NewExpression) (Value, error) {
	pos := ex.GetPos()
	if err := ffiRejectWindows(pos); err != nil {
		return Value{}, err
	}
	if len(ex.Args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: new DynamicLibrary takes exactly 1 argument (path)", pos.Line, pos.Col)
	}
	return e.emitFFIOpenLibrary(ex.Args[0], pos)
}

// ffiPtrToBigInt wraps a raw pointer register into a bigint address value.
func (e *Emitter) ffiPtrToBigInt(ptrReg string) Value {
	e.ensureBigInt()
	addr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", addr, ptrReg))
	b := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_bigint_from_u64(i64 %s)", b, addr))
	return Value{Ref: b, Ty: BigIntType()}
}

// emitFFIMarshalArg lowers one call argument to the declared C type.
func (e *Emitter) emitFFIMarshalArg(argExpr ast.Expression, canonical string, pos ast.Pos) (irTy, ref string, err error) {
	irTy = ffiLLVMType(canonical)
	switch canonical {
	case "char", "int8", "uint8", "int16", "uint16", "int32", "uint32":
		v, err := e.emitExpr(argExpr)
		if err != nil {
			return "", "", err
		}
		wide := e.coerce(v, TypeI64)
		if irTy == "i64" {
			return irTy, wide.Ref, nil
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to %s", r, wide.Ref, irTy))
		return irTy, r, nil

	case "int64", "uint64":
		v, err := e.emitExpr(argExpr)
		if err != nil {
			return "", "", err
		}
		if v.Ty.IsBigInt {
			e.ensureBigInt()
			r := e.freshReg()
			fn := "__kml_bigint_to_i64"
			if canonical == "uint64" {
				fn = "__kml_bigint_to_u64"
			}
			e.emitInstr(fmt.Sprintf("%s = call i64 @%s(ptr %s)", r, fn, v.Ref))
			return irTy, r, nil
		}
		return irTy, e.coerce(v, TypeI64).Ref, nil

	case "float32":
		v, err := e.emitExpr(argExpr)
		if err != nil {
			return "", "", err
		}
		d := e.coerce(v, TypeF64)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fptrunc double %s to float", r, d.Ref))
		return irTy, r, nil

	case "float64":
		v, err := e.emitExpr(argExpr)
		if err != nil {
			return "", "", err
		}
		return irTy, e.coerce(v, TypeF64).Ref, nil

	case "string":
		v, err := e.emitExpr(argExpr)
		if err != nil {
			return "", "", err
		}
		if v.Ty.IsNull {
			return irTy, "null", nil
		}
		return irTy, e.coerce(v, TypePtr).Ref, nil

	case "pointer", "buffer", "arraybuffer", "function":
		return e.emitFFIPointerArg(argExpr, pos)
	}
	return "", "", fmt.Errorf("%d:%d: unsupported FFI argument type '%s'", pos.Line, pos.Col, canonical)
}

// emitFFIPointerArg lowers a pointer-family argument: null/undefined → null,
// bigint → the raw address, Buffer/TypedArray/ArrayBuffer/DataView → the
// backing data pointer, anything else pointer-shaped passes as-is.
func (e *Emitter) emitFFIPointerArg(argExpr ast.Expression, pos ast.Pos) (irTy, ref string, err error) {
	ty := e.inferExprType(argExpr)
	switch {
	case ty.IsNull:
		if _, err := e.emitExpr(argExpr); err != nil { // keep side-effect order
			return "", "", err
		}
		return "ptr", "null", nil

	case ty.IsBigInt:
		v, err := e.emitExpr(argExpr)
		if err != nil {
			return "", "", err
		}
		e.ensureBigInt()
		addr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_bigint_to_u64(ptr %s)", addr, v.Ref))
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", p, addr))
		return "ptr", p, nil

	case ty.IsArrayBuffer:
		v, err := e.emitExpr(argExpr)
		if err != nil {
			return "", "", err
		}
		// ArrayBuffer is a ptr to {i64 byteLength, ptr data} — load the data ptr.
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr {i64, ptr}, ptr %s, i32 0, i32 1", gep, v.Ref))
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", p, gep))
		return "ptr", p, nil

	case ty.IsArray || ty.IsBuffer || ty.IsTypedArray:
		ptrReg, _, _, err := e.resolveArrayForHOF(argExpr, pos)
		if err != nil {
			return "", "", err
		}
		return "ptr", ptrReg, nil
	}
	v, err := e.emitExpr(argExpr)
	if err != nil {
		return "", "", err
	}
	if v.Ty.IsNull {
		return "ptr", "null", nil
	}
	return "ptr", e.coerce(v, TypePtr).Ref, nil
}

// emitFFIFunctionCall lowers a direct call through a bound native function:
// the arguments are evaluated first (JS order), then the record's
// closed-library, argument-count and per-argument checks run and the C
// function is called (emitFFICallThroughRecord).
func (e *Emitter) emitFFIFunctionCall(ex *ast.CallExpression) (Value, error) {
	pos := ex.GetPos()
	if err := ffiRejectWindows(pos); err != nil {
		return Value{}, err
	}
	calleeVal, err := e.emitExpr(ex.Callee)
	if err != nil {
		return Value{}, err
	}
	sig := calleeVal.Ty.FFISig
	if sig == nil {
		return Value{}, fmt.Errorf("%d:%d: FFI function value has no compile-time signature", pos.Line, pos.Col)
	}
	e.ensureFFIRuntime()
	args := make([]Value, len(ex.Args))
	for i, a := range ex.Args {
		v, err := e.emitExpr(a)
		if err != nil {
			return Value{}, err
		}
		args[i] = v
	}
	return e.emitFFICallThroughRecord(calleeVal.Ref, sig, args)
}

// emitFFIMarshalReturn widens/wraps a raw C return register to its KML value.
func (e *Emitter) emitFFIMarshalReturn(raw, canonical string) (Value, error) {
	switch canonical {
	case "char", "int8", "int16", "int32":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sext %s %s to i64", r, ffiLLVMType(canonical), raw))
		return Value{Ref: r, Ty: TypeI64}, nil
	case "uint8", "uint16", "uint32":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext %s %s to i64", r, ffiLLVMType(canonical), raw))
		return Value{Ref: r, Ty: TypeI64}, nil
	case "int64":
		e.ensureBigInt()
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_bigint_from_i64(i64 %s)", b, raw))
		return Value{Ref: b, Ty: BigIntType()}, nil
	case "uint64":
		e.ensureBigInt()
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_bigint_from_u64(i64 %s)", b, raw))
		return Value{Ref: b, Ty: BigIntType()}, nil
	case "float32":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fpext float %s to double", r, raw))
		return Value{Ref: r, Ty: TypeF64}, nil
	case "float64":
		return Value{Ref: raw, Ty: TypeF64}, nil
	case "pointer", "function":
		return e.ffiPtrToBigInt(raw), nil
	case "string":
		// NUL-terminated C string → KML string; a null pointer surfaces as null.
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, raw))
		res, err := e.emitStrBranch(isNull,
			func() (string, error) { return "null", nil },
			func() (string, error) {
				s := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", s, raw))
				return s, nil
			})
		if err != nil {
			return Value{}, err
		}
		nt := TypePtr
		nt.Nullable = true
		return Value{Ref: res, Ty: nt}, nil
	}
	return Value{Ty: TypeVoid}, nil
}
