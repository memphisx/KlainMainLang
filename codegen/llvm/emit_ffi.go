package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"runtime"
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

// ffiTypesConstants maps the ffi.types.* constant names to canonical type
// names (they are plain string constants in Node).
var ffiTypesConstants = map[string]string{
	"VOID": "void", "POINTER": "pointer", "BUFFER": "buffer",
	"ARRAY_BUFFER": "arraybuffer", "FUNCTION": "function", "BOOL": "uint8",
	"CHAR": "char", "STRING": "string", "FLOAT": "float32", "DOUBLE": "float64",
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
					return c, nil
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
	case "int64", "uint64", "pointer", "function":
		return BigIntType()
	case "float32", "float64":
		return TypeF64
	case "string":
		nt := TypePtr
		nt.Nullable = true
		return nt
	}
	return TypeVoid
}

// ffiSuffix is the host's shared-library filename suffix (ffi.suffix).
func ffiSuffix() string {
	switch runtime.GOOS {
	case "darwin":
		return "dylib"
	case "windows":
		return "dll"
	}
	return "so"
}

// ffiRejectWindows returns the clean rejection for the not-yet-shimmed host.
func ffiRejectWindows(pos ast.Pos) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("%d:%d: node:ffi is not supported on Windows yet (dlopen has no LoadLibrary shim)", pos.Line, pos.Col)
	}
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

// emitFFIDlopenHandle emits the dlopen call for a path expression (a string,
// or null/undefined for the current process image) and throws on failure.
func (e *Emitter) emitFFIDlopenHandle(pathExpr ast.Expression, pos ast.Pos) (handleReg, pathRef string, err error) {
	e.ensureFFIDl()
	pathRef = "null"
	if pathExpr != nil {
		pv, err := e.emitExpr(pathExpr)
		if err != nil {
			return "", "", err
		}
		if !pv.Ty.IsNull {
			pathRef = e.coerce(pv, TypePtr).Ref
		}
	}
	handleReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @dlopen(ptr %s, i32 %d)", handleReg, pathRef, ffiRTLDFlags()))
	e.emitFFIThrowOnNull(handleReg, "dlopen failed")
	return handleReg, pathRef, nil
}

// emitFFIBuildLibrary wraps a dlopen handle + path into a DynamicLibrary object.
func (e *Emitter) emitFFIBuildLibrary(handleReg, pathRef string) Value {
	e.ensureMalloc()
	libTy := FFILibraryType()
	obj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", obj, libTy.StructSize()))
	e.storeSQLiteField(libTy, obj, "__kml_handle", "ptr", handleReg)
	e.storeSQLiteField(libTy, obj, "path", "ptr", pathRef)
	return Value{Ref: obj, Ty: libTy}
}

// ffiDefinitionsType statically derives the `functions` object type from a
// dlopen/getFunctions definitions object literal.
func ffiDefinitionsType(defs *ast.ObjectLiteral) (Type, error) {
	var fields []Field
	for _, prop := range defs.Properties {
		if prop.KeyExpr != nil {
			pos := defs.GetPos()
			return Type{}, fmt.Errorf("%d:%d: FFI definitions cannot use computed property keys", pos.Line, pos.Col)
		}
		sig, err := ffiParseSignature(prop.Value)
		if err != nil {
			return Type{}, err
		}
		fields = append(fields, Field{Name: prop.Key, Ty: FFIFunctionType(sig)})
	}
	return ObjectType(fields), nil
}

// emitFFIResolveFunctions dlsym-resolves every definition against handleReg
// and returns the populated `functions` object.
func (e *Emitter) emitFFIResolveFunctions(handleReg string, defs *ast.ObjectLiteral) (Value, error) {
	funcsTy, err := ffiDefinitionsType(defs)
	if err != nil {
		return Value{}, err
	}
	e.ensureMalloc()
	obj := e.freshReg()
	size := funcsTy.StructSize()
	if size == 0 {
		size = 8 // an empty definitions map still yields a real (empty) object
	}
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", obj, size))
	for _, f := range funcsTy.Fields {
		sym := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @dlsym(ptr %s, ptr %s)", sym, handleReg, e.internString(f.Name)))
		e.emitFFIThrowOnNull(sym, fmt.Sprintf("undefined symbol: %s", f.Name))
		e.storeSQLiteField(funcsTy, obj, f.Name, "ptr", sym)
	}
	return Value{Ref: obj, Ty: funcsTy}, nil
}

// ffiDlopenResultType derives ffi.dlopen(...)'s `{ lib, functions }` type from
// the call's arguments — shared by codegen and inferExprType so both agree.
func ffiDlopenResultType(args []ast.Expression) (Type, error) {
	funcsTy := ObjectType(nil)
	if len(args) >= 2 {
		defs, ok := args[1].(*ast.ObjectLiteral)
		if !ok {
			pos := args[1].GetPos()
			return Type{}, fmt.Errorf("%d:%d: ffi.dlopen definitions must be an object literal — signatures are resolved at compile time", pos.Line, pos.Col)
		}
		var err error
		funcsTy, err = ffiDefinitionsType(defs)
		if err != nil {
			return Type{}, err
		}
	}
	libTy := FFILibraryType()
	// The dlopen definitions seed the library's accumulator, so a later
	// `lib.functions` includes them, as in Node.
	for _, f := range funcsTy.Fields {
		libTy.FFILibReg.AddFunc(f.Name, f.Ty.FFISig)
	}
	return ObjectType([]Field{
		{Name: "lib", Ty: libTy},
		{Name: "functions", Ty: funcsTy},
	}), nil
}

// emitFFIModuleCall dispatches ffi.dlopen / ffi.dlclose / ffi.dlsym.
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
		handle, pathRef, err := e.emitFFIDlopenHandle(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		lib := e.emitFFIBuildLibrary(handle, pathRef)
		var funcs Value
		if len(args) == 2 {
			funcs, err = e.emitFFIResolveFunctions(handle, args[1].(*ast.ObjectLiteral))
			if err != nil {
				return Value{}, err
			}
		} else {
			e.ensureMalloc()
			emptyObj := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 8)", emptyObj))
			funcs = Value{Ref: emptyObj, Ty: ObjectType(nil)}
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
	if len(args) == 4 {
		lit, ok := args[3].(*ast.StringLiteral)
		if !ok || (lit.Value != "utf8" && lit.Value != "utf-8") {
			return Value{}, fmt.Errorf("%d:%d: ffi.exportString supports only the 'utf8' encoding (this compiler's strings are UTF-8-native)", pos.Line, pos.Col)
		}
	}
	sv, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	s := e.coerce(sv, TypePtr).Ref
	dst, err := e.emitFFIPtrPlusOffset(args[1], nil, pos)
	if err != nil {
		return Value{}, err
	}
	capRef, err := e.emitFFII64Arg(args[2])
	if err != nil {
		return Value{}, err
	}

	e.ensureStrlen()
	e.ensureMemcpy()
	n := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", n, s))
	// w = min(n, max(cap-1, 0)); write w bytes + NUL (NUL only when cap > 0).
	capm1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", capm1, capRef))
	neg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, capm1))
	room := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", room, neg, capm1))
	fits := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %s, %s", fits, n, room))
	w := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", w, fits, n, room))
	cp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @memcpy(ptr %s, ptr %s, i64 %s)", cp, dst, s, w))
	hasRoom := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 0", hasRoom, capRef))
	nulL := e.freshLabel("ffi.nul")
	doneL := e.freshLabel("ffi.exported")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasRoom, nulL, doneL))
	e.emitLabel(nulL)
	end := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", end, dst, w))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", end))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	return Value{Ty: TypeVoid}, nil
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
	e.ensureFFIDl()
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	if !objVal.Ty.IsFFILibrary {
		return Value{}, fmt.Errorf("%d:%d: '%s' requires a DynamicLibrary receiver", pos.Line, pos.Col, method)
	}
	handleIdx, _, _ := objVal.Ty.FieldIndex("__kml_handle")
	handle := e.loadFieldValue(objVal, handleIdx, TypePtr).Ref

	reg := objVal.Ty.FFILibReg

	switch method {
	case "getFunction":
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: getFunction takes exactly 2 arguments (name, signature)", pos.Line, pos.Col)
		}
		sig, err := ffiParseSignature(args[1])
		if err != nil {
			return Value{}, err
		}
		if lit, ok := args[0].(*ast.StringLiteral); ok && reg != nil {
			reg.AddFunc(lit.Value, sig)
		}
		nameVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		nameRef := e.coerce(nameVal, TypePtr).Ref
		sym := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @dlsym(ptr %s, ptr %s)", sym, handle, nameRef))
		e.emitFFIThrowOnNull(sym, "undefined symbol")
		return Value{Ref: sym, Ty: FFIFunctionType(sig)}, nil

	case "getFunctions":
		// With no arguments, Node returns the accumulator of everything
		// previously resolved — same as the `functions` property.
		if len(args) == 0 {
			return e.emitFFILibAccumulator(objVal, handle, true, pos)
		}
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: getFunctions takes a definitions object literal (or no argument for the previously-resolved set)", pos.Line, pos.Col)
		}
		defs, ok := args[0].(*ast.ObjectLiteral)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: getFunctions definitions must be an object literal — signatures are resolved at compile time", pos.Line, pos.Col)
		}
		if reg != nil {
			funcsTy, err := ffiDefinitionsType(defs)
			if err != nil {
				return Value{}, err
			}
			for _, f := range funcsTy.Fields {
				reg.AddFunc(f.Name, f.Ty.FFISig)
			}
		}
		return e.emitFFIResolveFunctions(handle, defs)

	case "getSymbol":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: getSymbol takes exactly 1 argument (name)", pos.Line, pos.Col)
		}
		if lit, ok := args[0].(*ast.StringLiteral); ok && reg != nil {
			reg.AddSym(lit.Value)
		}
		nameVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		nameRef := e.coerce(nameVal, TypePtr).Ref
		sym := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @dlsym(ptr %s, ptr %s)", sym, handle, nameRef))
		e.emitFFIThrowOnNull(sym, "undefined symbol")
		return e.ffiPtrToBigInt(sym), nil

	case "getSymbols":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: getSymbols takes no arguments (it returns the previously-resolved symbol addresses)", pos.Line, pos.Col)
		}
		return e.emitFFILibAccumulator(objVal, handle, false, pos)

	case "close":
		// Idempotent: dlclose only a live handle, then null it out.
		live := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", live, handle))
		closeL := e.freshLabel("ffi.close")
		doneL := e.freshLabel("ffi.closed")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", live, closeL, doneL))
		e.emitLabel(closeL)
		rc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @dlclose(ptr %s)", rc, handle))
		e.storeSQLiteField(objVal.Ty, objVal.Ref, "__kml_handle", "ptr", "null")
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
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

// ffiLibAccumulatorType derives the object type of `library.functions` /
// `library.symbols` (and their no-arg getter forms) from the accumulator.
func ffiLibAccumulatorType(reg *FFILibReg, wantFuncs bool) Type {
	var fields []Field
	if reg != nil {
		for _, f := range reg.Entries {
			if wantFuncs {
				if f.Sig != nil {
					fields = append(fields, Field{Name: f.Name, Ty: FFIFunctionType(f.Sig)})
				}
				continue
			}
			// Node's `symbols` holds every previously-resolved address —
			// getSymbol names and resolved functions alike.
			fields = append(fields, Field{Name: f.Name, Ty: BigIntType()})
		}
	}
	return ObjectType(fields)
}

// emitFFILibAccumulator materializes the accumulator object: every recorded
// name re-dlsym'd against the live handle (same address as the original
// resolution), typed as callable functions or bigint addresses.
func (e *Emitter) emitFFILibAccumulator(objVal Value, handle string, wantFuncs bool, pos ast.Pos) (Value, error) {
	ty := ffiLibAccumulatorType(objVal.Ty.FFILibReg, wantFuncs)
	e.ensureMalloc()
	obj := e.freshReg()
	size := ty.StructSize()
	if size == 0 {
		size = 8
	}
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", obj, size))
	for _, f := range ty.Fields {
		sym := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @dlsym(ptr %s, ptr %s)", sym, handle, e.internString(f.Name)))
		e.emitFFIThrowOnNull(sym, fmt.Sprintf("undefined symbol: %s", f.Name))
		val := sym
		if !wantFuncs {
			val = e.ffiPtrToBigInt(sym).Ref
		}
		e.storeSQLiteField(ty, obj, f.Name, "ptr", val)
	}
	return Value{Ref: obj, Ty: ty}, nil
}

// emitFFILibraryProperty lowers `library.functions` / `library.symbols`.
func (e *Emitter) emitFFILibraryProperty(objExpr ast.Expression, prop string, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	handleIdx, _, _ := objVal.Ty.FieldIndex("__kml_handle")
	handle := e.loadFieldValue(objVal, handleIdx, TypePtr).Ref
	e.ensureFFIDl()
	return e.emitFFILibAccumulator(objVal, handle, prop == "functions", pos)
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
	handle, pathRef, err := e.emitFFIDlopenHandle(ex.Args[0], pos)
	if err != nil {
		return Value{}, err
	}
	return e.emitFFIBuildLibrary(handle, pathRef), nil
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

// emitFFIFunctionCall lowers a call through an IsFFIFunction value: marshal
// each argument to the declared C type, emit the indirect C-ABI call, and
// marshal the return.
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
	if len(ex.Args) != len(sig.Args) {
		return Value{}, fmt.Errorf("%d:%d: FFI call expects %d argument(s), got %d", pos.Line, pos.Col, len(sig.Args), len(ex.Args))
	}
	callArgs := ""
	for i, argExpr := range ex.Args {
		irTy, ref, err := e.emitFFIMarshalArg(argExpr, sig.Args[i], pos)
		if err != nil {
			return Value{}, err
		}
		if i > 0 {
			callArgs += ", "
		}
		callArgs += irTy + " " + ref
	}
	retIR := ffiLLVMType(sig.Ret)
	if retIR == "void" {
		e.emitInstr(fmt.Sprintf("call void %s(%s)", calleeVal.Ref, callArgs))
		return Value{Ty: TypeVoid}, nil
	}
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s %s(%s)", raw, retIR, calleeVal.Ref, callArgs))
	return e.emitFFIMarshalReturn(raw, sig.Ret)
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
