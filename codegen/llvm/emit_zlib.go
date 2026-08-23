// emit_zlib.go — Node's `zlib` core module: the one-shot compress/decompress
// family (gzip/gunzip, deflate/inflate, deflateRaw/inflateRaw, unzip) in both
// the *Sync and the Node-style (err, result) callback forms.
//
// Every variant is the same call: normalize the input to a (ptr, byteLen)
// pair, then drive @__kml_zlib_oneshot (runtime_streams_zlib.go) with a
// per-codec mode/windowBits pair, wrapping its { ptr, i64 } return as a Buffer
// (a uint8 TypedArray value — the same first-class shape fs.readFileSyncBytes
// returns). The heavy lifting (libz linkage, the z_stream ABI, the growable
// output loop) is shared with CompressionStream via that runtime helper, so
// this file is pure surface binding.
//
// Input types match Node: a Buffer/Uint8Array (or any TypedArray), an
// ArrayBuffer, a DataView, or a string (encoded as its UTF-8 bytes). Output is
// always a Buffer.
//
// The callback form fires synchronously here rather than deferring to the next
// event-loop tick (a documented caveat, docs/status/NODE-CORE-MODULES.md).
package llvm

import (
	"fmt"
	"strconv"

	"KlainMainLang/ast"
)

// zlibConstIntLiteral reads a compile-time integer from a number literal or a
// unary-minus-number (e.g. -1), the only forms a static { level } accepts.
func zlibConstIntLiteral(expr ast.Expression) (int, bool) {
	if u, ok := expr.(*ast.UnaryExpression); ok && u.Op == "-" && u.Prefix {
		if n, ok := zlibConstIntLiteral(u.Arg); ok {
			return -n, true
		}
		return 0, false
	}
	lit, ok := expr.(*ast.NumberLiteral)
	if !ok || lit.IsBigInt {
		return 0, false
	}
	n, err := strconv.Atoi(lit.Value)
	if err != nil {
		return 0, false
	}
	return n, true
}

// zlib windowBits conventions (shared with emitNewCompressionStream): 31 gzip,
// 15 zlib, -15 raw for compression; 47 (auto-detect gzip+zlib) for the
// decoders, matching Node's gunzip/unzip both accepting either wrapper.
type zlibCodec struct {
	mode  int // 0 = deflate/compress, 1 = inflate/decompress
	wbits int
}

var zlibCodecs = map[string]zlibCodec{
	"gzip":       {0, 31},
	"gunzip":     {1, 47},
	"deflate":    {0, 15},
	"inflate":    {1, 15},
	"deflateRaw": {0, -15},
	"inflateRaw": {1, -15},
	"unzip":      {1, 47},
}

// emitZlibModuleCall dispatches a `zlib.<method>(...)` call. `method` is the
// property name with any trailing "Sync" already carrying its own meaning: a
// *Sync name returns the Buffer directly, a bare name takes a trailing
// (err, result) callback.
func (e *Emitter) emitZlibModuleCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	sync := false
	base := method
	if n := len(method); n > 4 && method[n-4:] == "Sync" {
		sync = true
		base = method[:n-4]
	}
	codec, ok := zlibCodecs[base]
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: zlib.%s is not supported", pos.Line, pos.Col, method)
	}

	if sync {
		if len(args) < 1 || len(args) > 2 {
			return Value{}, fmt.Errorf("%d:%d: zlib.%s takes 1 argument (buffer)%s", pos.Line, pos.Col, method, zlibOptsHint(codec))
		}
		level, err := e.zlibLevelArg(args, 1, codec, pos)
		if err != nil {
			return Value{}, err
		}
		resReg, err := e.emitZlibOneshotCall(args[0], codec, level, pos)
		if err != nil {
			return Value{}, err
		}
		// A -1 length field signals a zlib error (corrupt/truncated input).
		e.emitZlibThrowOnError(resReg, base, pos)
		return Value{Ref: resReg, Ty: TypedArrayType("uint8")}, nil
	}

	// Callback form: zlib.<base>(buffer[, opts], callback).
	if len(args) < 2 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: zlib.%s takes a buffer%s and a callback", pos.Line, pos.Col, method, zlibOptsHint(codec))
	}
	level, err := e.zlibLevelArg(args[:len(args)-1], 1, codec, pos)
	if err != nil {
		return Value{}, err
	}
	cbArg := args[len(args)-1]
	cb, err := e.resolveCallbackWithHints(cbArg, []Type{errorObjType, TypedArrayType("uint8")})
	if err != nil {
		return Value{}, err
	}
	resReg, err := e.emitZlibOneshotCall(args[0], codec, level, pos)
	if err != nil {
		return Value{}, err
	}

	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 1", lenReg, resReg))
	isErr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, -1", isErr, lenReg))
	errL := e.freshLabel("zlib.cb.err")
	okL := e.freshLabel("zlib.cb.ok")
	doneL := e.freshLabel("zlib.cb.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isErr, errL, okL))

	e.emitLabel(okL)
	nullErr := Value{Ref: "null", Ty: errorObjType}
	if _, err := e.emitCBCall(cb, []Value{nullErr, {Ref: resReg, Ty: TypedArrayType("uint8")}}); err != nil {
		return Value{}, err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(errL)
	errObj := e.buildErrorObj(0, e.internString(base+" failed: invalid input"), e.internString("Error"))
	empty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } { ptr null, i64 0 }, ptr null, 0", empty))
	if _, err := e.emitCBCall(cb, []Value{{Ref: errObj, Ty: errorObjType}, {Ref: empty, Ty: TypedArrayType("uint8")}}); err != nil {
		return Value{}, err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	return Value{Ty: TypeVoid}, nil
}

// zlibOptsHint tailors the arity error message: only the compressors accept an
// optional { level } options object.
func zlibOptsHint(c zlibCodec) string {
	if c.mode == 0 {
		return " (with an optional { level } options object)"
	}
	return ""
}

// zlibLevelArg reads an optional { level } options object at args[idx] for the
// compressors, returning the compile-time level (default 6, matching zlib's
// own default). Decompressors ignore it. A runtime-variable level, or any
// other option key, is a clean rejection — this compiler's args are static.
func (e *Emitter) zlibLevelArg(args []ast.Expression, idx int, codec zlibCodec, pos ast.Pos) (int, error) {
	if idx >= len(args) {
		return 6, nil
	}
	if codec.mode != 0 {
		return 0, fmt.Errorf("%d:%d: this zlib decompressor takes no options object", pos.Line, pos.Col)
	}
	obj, ok := args[idx].(*ast.ObjectLiteral)
	if !ok {
		return 0, fmt.Errorf("%d:%d: zlib options must be an object literal like { level: 9 }", pos.Line, pos.Col)
	}
	level := 6
	for _, prop := range obj.Properties {
		if prop.Key != "level" {
			return 0, fmt.Errorf("%d:%d: only the { level } zlib option is supported", pos.Line, pos.Col)
		}
		n, ok := zlibConstIntLiteral(prop.Value)
		if !ok {
			return 0, fmt.Errorf("%d:%d: zlib { level } must be a compile-time integer constant (0-9, or -1 for the default)", pos.Line, pos.Col)
		}
		level = n
		if level < -1 || level > 9 {
			return 0, fmt.Errorf("%d:%d: zlib { level } must be between -1 and 9", pos.Line, pos.Col)
		}
	}
	return level, nil
}

// emitZlibOneshotCall normalizes the input argument to a (ptr, byteLen) pair
// and calls @__kml_zlib_oneshot, returning the register holding its { ptr, i64 }
// result aggregate.
func (e *Emitter) emitZlibOneshotCall(arg ast.Expression, codec zlibCodec, level int, pos ast.Pos) (string, error) {
	e.ensureZlibOneshot()
	ptrRef, lenRef, err := e.zlibResolveInput(arg, pos)
	if err != nil {
		return "", err
	}
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { ptr, i64 } @__kml_zlib_oneshot(ptr %s, i64 %s, i64 %d, i64 %d, i64 %d)",
		res, ptrRef, lenRef, codec.mode, codec.wbits, level))
	return res, nil
}

// zlibResolveInput coerces a zlib input argument (Buffer/TypedArray,
// ArrayBuffer, DataView, or string) to a raw (dataPtr, byteLen) pair.
func (e *Emitter) zlibResolveInput(arg ast.Expression, pos ast.Pos) (string, string, error) {
	ty := e.inferExprType(arg)
	switch {
	case ty.IsArrayBuffer:
		bufVal, err := e.emitExpr(arg)
		if err != nil {
			return "", "", err
		}
		lenVal, err := e.emitArrayBufferByteLength(bufVal)
		if err != nil {
			return "", "", err
		}
		dataSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", dataSlot, bufVal.Ref))
		dataReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataReg, dataSlot))
		return dataReg, lenVal.Ref, nil

	case ty.IsDataView:
		dvVal, err := e.emitExpr(arg)
		if err != nil {
			return "", "", err
		}
		dataReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataReg, dvVal.Ref)) // field 0: data
		lenSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", lenSlot, dataViewStructIR, dvVal.Ref))
		lenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, lenSlot))
		return dataReg, lenReg, nil

	case ty.IsTypedArray:
		ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(arg, pos)
		if err != nil {
			return "", "", err
		}
		byteLen, err := e.emitTypedArrayByteLength(lenReg, elemTy)
		if err != nil {
			return "", "", err
		}
		return ptrReg, byteLen.Ref, nil

	default:
		// String input: encode as its UTF-8 bytes (the stored form already is
		// UTF-8), length via strlen.
		strVal, err := e.emitExpr(arg)
		if err != nil {
			return "", "", err
		}
		strVal = e.coerce(strVal, TypePtr)
		e.ensureStrlen()
		lenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", lenReg, strVal.Ref))
		return strVal.Ref, lenReg, nil
	}
}

// emitZlibThrowOnError branches on the oneshot result's length == -1 sentinel,
// throwing an Error for the *Sync forms on a corrupt/truncated input.
func (e *Emitter) emitZlibThrowOnError(resReg, base string, pos ast.Pos) {
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 1", lenReg, resReg))
	isErr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, -1", isErr, lenReg))
	throwL := e.freshLabel("zlib.throw")
	okL := e.freshLabel("zlib.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isErr, throwL, okL))
	e.emitLabel(throwL)
	e.emitInternalThrow(e.internString(base + " failed: invalid input"))
	e.emitLabel(okL)
}
