// emit_buffer.go — Node's Buffer (TDD-00103). A Buffer IS a Uint8Array
// (TypedArrayType("uint8") + IsBuffer, the IsURLSearchParams-on-IsMap
// pattern), so indexing/.length/.slice/.subarray/numeric .fill/.indexOf/
// .includes/HOFs/for-of/.set/.byteLength all come free from the array
// machinery. This file adds only what the flag enables: the Buffer.*
// statics (call syntax dispatched like the Atomics namespace — general
// expressions, no vardecl restriction) and the instance methods
// (.toString/.write/.copy/.equals/.compare and the fixed-width
// read*/write* accessors, built on the same bswap/bitcast core DataView
// uses). String codecs (hex/base64/latin1) live in buffersrc/ via the
// __kml_buf_* ABI; utf8 is strlen/memcpy (strings are UTF-8 already).
package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

func (e *Emitter) ensureMemcmp() {
	if e.usedMemcmp {
		return
	}
	e.usedMemcmp = true
	e.emitGlobal("declare i32 @memcmp(ptr noundef, ptr noundef, i64 noundef)")
}

// bufferAggregate wraps (dataPtr, len) registers into a Buffer-typed
// {ptr, i64} value — the same aggregate shape .subarray returns.
func (e *Emitter) bufferAggregate(ptrRef, lenRef string) Value {
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, ptrRef))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenRef))
	return Value{Ref: r1, Ty: BufferType()}
}

// bufferEncodingArg resolves an optional encoding argument. The codec choice
// is a compile-time dispatch, so the argument must be a string literal;
// names are normalized (utf-8 → utf8, binary/ascii → latin1, ucs2 aliases
// rejected with the utf16 message).
func bufferEncodingArg(args []ast.Expression, idx int, pos ast.Pos) (string, error) {
	if len(args) <= idx {
		return "utf8", nil
	}
	lit, ok := args[idx].(*ast.StringLiteral)
	if !ok {
		return "", fmt.Errorf("%d:%d: a Buffer encoding must be a string literal (the codec is chosen at compile time)", pos.Line, pos.Col)
	}
	switch strings.ToLower(lit.Value) {
	case "utf8", "utf-8":
		return "utf8", nil
	case "hex":
		return "hex", nil
	case "base64":
		return "base64", nil
	case "base64url":
		return "base64url", nil
	case "latin1", "binary", "ascii":
		return "latin1", nil
	case "utf16le", "utf-16le", "ucs2", "ucs-2":
		return "", fmt.Errorf("%d:%d: the '%s' encoding is not supported (this compiler's strings are UTF-8-native)", pos.Line, pos.Col, lit.Value)
	}
	return "", fmt.Errorf("%d:%d: unknown Buffer encoding '%s' (utf8/hex/base64/base64url/latin1/binary/ascii)", pos.Line, pos.Col, lit.Value)
}

// emitBufferDecodeString lowers (string, encoding) → (dataPtr, len)
// registers holding a fresh byte buffer.
func (e *Emitter) emitBufferDecodeString(strRef, enc string) (ptrRef, lenRef string) {
	switch enc {
	case "utf8":
		e.ensureStrlen()
		e.ensureMalloc()
		e.ensureMemcpy()
		l := e.freshReg()
		buf := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", l, strRef))
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", buf, l))
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", buf, strRef, l))
		return buf, l
	default:
		e.ensureBufferCodecs()
		fn := map[string]string{
			"hex":       "@__kml_buf_hex_dec",
			"base64":    "@__kml_buf_b64_dec",
			"base64url": "@__kml_buf_b64_dec",
			"latin1":    "@__kml_buf_latin1_bytes",
		}[enc]
		outSlot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", outSlot))
		l := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 %s(ptr %s, ptr %s)", l, fn, strRef, outSlot))
		buf := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", buf, outSlot))
		return buf, l
	}
}

// emitBufferEncodeString lowers (dataPtr, len, encoding) → a string value.
func (e *Emitter) emitBufferEncodeString(ptrRef, lenRef, enc string) Value {
	switch enc {
	case "utf8":
		// Copy + NUL-terminate (embedded NULs truncate — the standard
		// string-boundary caveat).
		e.ensureMemcpy()
		nul := e.freshReg()
		buf := e.emitStringAlloc(lenRef) // TDD-00120: length-prefixed string
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", buf, ptrRef, lenRef))
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", nul, buf, lenRef))
		e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", nul))
		return Value{Ref: buf, Ty: TypePtr}
	case "hex":
		e.ensureBufferCodecs()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_buf_hex_enc(ptr %s, i64 %s)", r, ptrRef, lenRef))
		return Value{Ref: r, Ty: TypePtr}
	case "base64", "base64url":
		e.ensureBufferCodecs()
		urlsafe := "0"
		if enc == "base64url" {
			urlsafe = "1"
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_buf_b64_enc(ptr %s, i64 %s, i32 %s)", r, ptrRef, lenRef, urlsafe))
		return Value{Ref: r, Ty: TypePtr}
	default: // latin1
		e.ensureBufferCodecs()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_buf_latin1_str(ptr %s, i64 %s)", r, ptrRef, lenRef))
		return Value{Ref: r, Ty: TypePtr}
	}
}

// emitBufferStaticCall dispatches Buffer.<method>(...).
func (e *Emitter) emitBufferStaticCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch method {
	case "from":
		if len(args) < 1 || len(args) > 2 {
			return Value{}, fmt.Errorf("%d:%d: Buffer.from takes (string, encoding?) or (array|TypedArray|ArrayBuffer|Buffer)", pos.Line, pos.Col)
		}
		// Inline array literal: evaluate each element directly into a fresh
		// buffer (the same direct-evaluation trick TypedArray literals use).
		if lit, ok := args[0].(*ast.ArrayLiteral); ok {
			n := int64(len(lit.Elements))
			e.ensureMalloc()
			data := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", data, n))
			for i, elem := range lit.Elements {
				if _, isSpread := elem.(*ast.SpreadElement); isSpread {
					return Value{}, fmt.Errorf("%d:%d: spread elements in Buffer.from's array literal are not supported", elem.GetPos().Line, elem.GetPos().Col)
				}
				v, err := e.emitExpr(elem)
				if err != nil {
					return Value{}, err
				}
				v = e.coerce(v, TypeU8)
				gep := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", gep, data, i))
				e.emitInstr(fmt.Sprintf("store i8 %s, ptr %s, align 1", v.Ref, gep))
			}
			return e.bufferAggregate(data, fmt.Sprintf("%d", n)), nil
		}
		argTy := e.inferExprType(args[0])
		switch {
		case argTy.IsArrayBuffer:
			// Node views the ArrayBuffer; this copies (disclosed caveat) —
			// the out-of-scope .buffer machinery would be needed to alias.
			v, err := e.emitExpr(args[0])
			if err != nil {
				return Value{}, err
			}
			size, data := e.emitBlobSizeData(v.Ref)
			c := e.emitBlobCopyData(size, data)
			return e.bufferAggregate(c, size), nil
		case argTy.IsArray:
			if argTy.BigIntElem {
				return Value{}, fmt.Errorf("%d:%d: Buffer.from cannot copy-construct from a BigInt64Array/BigUint64Array", pos.Line, pos.Col)
			}
			ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(args[0], pos)
			if err != nil {
				return Value{}, err
			}
			e.ensureMalloc()
			data := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", data, lenReg))
			if err := e.emitBufferCopyCoerceLoop(ptrReg, lenReg, elemTy, data); err != nil {
				return Value{}, err
			}
			return e.bufferAggregate(data, lenReg), nil
		default:
			v, err := e.emitExpr(args[0])
			if err != nil {
				return Value{}, err
			}
			if !isStringTy(v.Ty) || v.Ty.IsArrayBuffer || v.Ty.IsBlob || v.Ty.IsDataView {
				return Value{}, fmt.Errorf("%d:%d: Buffer.from takes a string, array, TypedArray, ArrayBuffer, or Buffer", pos.Line, pos.Col)
			}
			enc, err := bufferEncodingArg(args, 1, pos)
			if err != nil {
				return Value{}, err
			}
			data, l := e.emitBufferDecodeString(v.Ref, enc)
			return e.bufferAggregate(data, l), nil
		}

	case "alloc", "allocUnsafe":
		if len(args) < 1 || (method == "allocUnsafe" && len(args) != 1) || len(args) > 2 {
			return Value{}, fmt.Errorf("%d:%d: Buffer.%s takes (size%s)", pos.Line, pos.Col, method, map[string]string{"alloc": ", fill?", "allocUnsafe": ""}[method])
		}
		nVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		nRef := e.coerce(nVal, TypeI64).Ref
		data := e.freshReg()
		if method == "allocUnsafe" {
			e.ensureMalloc()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", data, nRef))
		} else {
			e.ensureCalloc()
			e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 %s, i64 1)", data, nRef))
			if len(args) == 2 {
				fv, err := e.emitExpr(args[1])
				if err != nil {
					return Value{}, err
				}
				switch {
				case isNumberTy(fv.Ty):
					b := e.coerce(fv, TypeU8)
					wide := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = zext i8 %s to i32", wide, b.Ref))
					e.ensureMemset()
					e.emitInstr(fmt.Sprintf("call ptr @memset(ptr %s, i32 %s, i64 %s)", data, wide, nRef))
				case isStringTy(fv.Ty):
					// String fill (ADR-00576): repeat the fill string's UTF-8 bytes
					// across the whole buffer, matching Node's Buffer.alloc(n, str).
					fv = e.coerce(fv, TypePtr)
					e.ensureStrHeaderRuntime()
					needleLen := e.emitStrLenHeader(fv.Ref)
					e.emitBufferRepeatFill(data, fv.Ref, needleLen, "0", nRef)
				default:
					return Value{}, fmt.Errorf("%d:%d: Buffer.alloc's fill must be a number or a string", pos.Line, pos.Col)
				}
			}
		}
		return e.bufferAggregate(data, nRef), nil

	case "concat":
		if len(args) < 1 || len(args) > 2 {
			return Value{}, fmt.Errorf("%d:%d: Buffer.concat takes (list, totalLength?)", pos.Line, pos.Col)
		}
		lit, ok := args[0].(*ast.ArrayLiteral)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: Buffer.concat's list must be an inline array literal of Buffers/TypedArrays", pos.Line, pos.Col)
		}
		type part struct{ ptrRef, lenRef string }
		parts := make([]part, 0, len(lit.Elements))
		for _, elem := range lit.Elements {
			pTy := e.inferExprType(elem)
			if !pTy.IsTypedArray || pTy.BigIntElem {
				return Value{}, fmt.Errorf("%d:%d: Buffer.concat's list elements must be Buffers or byte TypedArrays", elem.GetPos().Line, elem.GetPos().Col)
			}
			ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(elem, elem.GetPos())
			if err != nil {
				return Value{}, err
			}
			byteLen := lenReg
			if elemTy.Align() != 1 {
				bl := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bl, lenReg, elemTy.Align()))
				byteLen = bl
			}
			parts = append(parts, part{ptrRef: ptrReg, lenRef: byteLen})
		}
		total := "0"
		for _, p := range parts {
			next := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", next, total, p.lenRef))
			total = next
		}
		if len(args) == 2 {
			tv, err := e.emitExpr(args[1])
			if err != nil {
				return Value{}, err
			}
			total = e.coerce(tv, TypeI64).Ref
		}
		e.ensureCalloc()
		e.ensureMemcpy()
		data := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 %s, i64 1)", data, total))
		offset := "0"
		for _, p := range parts {
			// Clamp each copy to the remaining room (an explicit shorter
			// totalLength truncates, per Node).
			room := e.freshReg()
			over := e.freshReg()
			cnt := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", room, total, offset))
			e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", over, p.lenRef, room))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", cnt, over, room, p.lenRef))
			isNeg := e.freshReg()
			cnt2 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNeg, cnt))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", cnt2, isNeg, cnt))
			dst := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", dst, data, offset))
			e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", dst, p.ptrRef, cnt2))
			next := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", next, offset, cnt2))
			offset = next
		}
		return e.bufferAggregate(data, total), nil

	case "compare":
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: Buffer.compare takes (a, b)", pos.Line, pos.Col)
		}
		aPtr, aLen, _, err := e.resolveArrayForHOF(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		bPtr, bLen, _, err := e.resolveArrayForHOF(args[1], pos)
		if err != nil {
			return Value{}, err
		}
		return e.emitBufferCompareCore(aPtr, aLen, bPtr, bLen), nil

	case "isBuffer":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: Buffer.isBuffer takes 1 argument", pos.Line, pos.Col)
		}
		// A compile-time constant from the inferred type; the argument is
		// still evaluated for its side effects.
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		if v.Ty.IsBuffer {
			return Value{Ref: "true", Ty: TypeBool}, nil
		}
		return Value{Ref: "false", Ty: TypeBool}, nil

	case "byteLength":
		if len(args) < 1 || len(args) > 2 {
			return Value{}, fmt.Errorf("%d:%d: Buffer.byteLength takes (string, encoding?)", pos.Line, pos.Col)
		}
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		if v.Ty.IsTypedArray || v.Ty.IsArrayBuffer {
			return Value{}, fmt.Errorf("%d:%d: Buffer.byteLength here takes a string (use .byteLength on a TypedArray/ArrayBuffer value)", pos.Line, pos.Col)
		}
		enc, err := bufferEncodingArg(args, 1, pos)
		if err != nil {
			return Value{}, err
		}
		if enc == "utf8" {
			e.ensureStrlen()
			l := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", l, v.Ref))
			return Value{Ref: l, Ty: TypeI64}, nil
		}
		// Non-utf8: decode and take the length — exact for every codec
		// (Node's fast formulas are just optimizations).
		_, l := e.emitBufferDecodeString(v.Ref, enc)
		return Value{Ref: l, Ty: TypeI64}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: unknown Buffer method '%s' (from/alloc/allocUnsafe/concat/compare/isBuffer/byteLength)", pos.Line, pos.Col, method)
}

// emitBufferCopyCoerceLoop copies lenReg elements from a source array
// (any numeric elemTy) into dstData as u8, with the standard wrap coercion.
func (e *Emitter) emitBufferCopyCoerceLoop(srcPtr, lenReg string, srcElemTy Type, dstData string) error {
	idx := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idx))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idx))
	condL := e.freshLabel("buf.copy.cond")
	bodyL := e.freshLabel("buf.copy.body")
	doneL := e.freshLabel("buf.copy.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, idx))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, i, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))
	e.emitLabel(bodyL)
	sGep := e.freshReg()
	sVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", sGep, srcElemTy.IR, srcPtr, i))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", sVal, srcElemTy.IR, sGep, srcElemTy.Align()))
	c := e.coerce(Value{Ref: sVal, Ty: srcElemTy}, TypeU8)
	dGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", dGep, dstData, i))
	e.emitInstr(fmt.Sprintf("store i8 %s, ptr %s, align 1", c.Ref, dGep))
	nx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", nx, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nx, idx))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
	return nil
}

// emitBufferCompareCore emits the Node comparison: memcmp over the common
// prefix, then the shorter buffer sorts first. Returns -1/0/1 as i64.
func (e *Emitter) emitBufferCompareCore(aPtr, aLen, bPtr, bLen string) Value {
	e.ensureMemcmp()
	aSmall := e.freshReg()
	minLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", aSmall, aLen, bLen))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", minLen, aSmall, aLen, bLen))
	mc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @memcmp(ptr %s, ptr %s, i64 %s)", mc, aPtr, bPtr, minLen))
	mneg := e.freshReg()
	mpos := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i32 %s, 0", mneg, mc))
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i32 %s, 0", mpos, mc))
	// prefix differs → sign of memcmp; else compare lengths.
	lneg := e.freshReg()
	lpos := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", lneg, aLen, bLen))
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", lpos, aLen, bLen))
	byLen := e.freshReg()
	s1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 -1, i64 0", s1, lneg))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 1, i64 %s", byLen, lpos, s1))
	s2 := e.freshReg()
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 1, i64 %s", s2, mpos, byLen))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 -1, i64 %s", res, mneg, s2))
	return Value{Ref: res, Ty: TypeI64}
}

// bufferAccessorKind parses a read*/write* accessor name into its shape.
type bufferAccessorKind struct {
	write  bool
	width  int
	signed bool
	float  bool
	bigint bool
	little bool
}

func bufferAccessorKindFor(name string) (bufferAccessorKind, bool) {
	var k bufferAccessorKind
	rest := ""
	switch {
	case strings.HasPrefix(name, "read"):
		rest = name[4:]
	case strings.HasPrefix(name, "write"):
		k.write = true
		rest = name[5:]
	default:
		return k, false
	}
	switch {
	case strings.HasSuffix(rest, "LE"):
		k.little = true
		rest = rest[:len(rest)-2]
	case strings.HasSuffix(rest, "BE"):
		rest = rest[:len(rest)-2]
	}
	switch rest {
	case "UInt8", "Uint8":
		k.width = 1
	case "Int8":
		k.width, k.signed = 1, true
	case "UInt16", "Uint16":
		k.width = 2
	case "Int16":
		k.width, k.signed = 2, true
	case "UInt32", "Uint32":
		k.width = 4
	case "Int32":
		k.width, k.signed = 4, true
	case "Float":
		k.width, k.float, k.signed = 4, true, true
	case "Double":
		k.width, k.float, k.signed = 8, true, true
	case "BigInt64":
		k.width, k.bigint, k.signed = 8, true, true
	case "BigUInt64", "BigUint64":
		k.width, k.bigint = 8, true
	default:
		return k, false
	}
	// The 1-byte forms have no LE/BE suffix in Node; wider non-suffixed
	// names (e.g. readUInt16) don't exist — but the suffix stripping above
	// already handled every real name, and a width>1 name without a parsed
	// suffix defaults to big-endian false→little=false, which only arises
	// for the 8-bit forms anyway.
	return k, true
}

// emitBufferAccessor implements buf.read*/write*(...). Bounds are checked
// against .length with a catchable RangeError, like Node's ERR_OUT_OF_RANGE.
func (e *Emitter) emitBufferAccessor(mem *ast.MemberExpression, k bufferAccessorKind, args []ast.Expression, pos ast.Pos) (Value, error) {
	ptrReg, lenReg, _, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	// Node: read*(offset?) / write*(value, offset?), offset defaults 0.
	offIdx := 0
	if k.write {
		offIdx = 1
		if len(args) < 1 {
			return Value{}, fmt.Errorf("%d:%d: a Buffer write accessor takes (value, offset?)", pos.Line, pos.Col)
		}
	}
	offRef := "0"
	if len(args) > offIdx {
		ov, err := e.emitExpr(args[offIdx])
		if err != nil {
			return Value{}, err
		}
		offRef = e.coerce(ov, TypeI64).Ref
	}
	// Bounds: 0 <= offset && offset + width <= length.
	end := e.freshReg()
	neg := e.freshReg()
	big := e.freshReg()
	bad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", end, offRef, k.width))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, offRef))
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", big, end, lenReg))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", bad, neg, big))
	badL := e.freshLabel("buf.acc.bad")
	okL := e.freshLabel("buf.acc.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
	e.emitLabel(badL)
	e.emitInternalThrow(e.internString("RangeError: The offset is outside the bounds of the Buffer"))
	e.emitLabel(okL)
	elemPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", elemPtr, ptrReg, offRef))

	bits := k.width * 8
	littleRef := "false"
	if k.little {
		littleRef = "true"
	}
	if !k.write {
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i%d, ptr %s, align 1", raw, bits, elemPtr))
		val := e.emitDataViewMaybeSwap(raw, k.width, littleRef)
		switch {
		case k.float:
			if k.width == 4 {
				f32 := e.freshReg()
				f64 := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = bitcast i32 %s to float", f32, val))
				e.emitInstr(fmt.Sprintf("%s = fpext float %s to double", f64, f32))
				return Value{Ref: f64, Ty: TypeF64}, nil
			}
			f64 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", f64, val))
			return Value{Ref: f64, Ty: TypeF64}, nil
		case k.bigint:
			e.ensureBigInt()
			wrap := "@__kml_bigint_from_i64"
			if !k.signed {
				wrap = "@__kml_bigint_from_u64"
			}
			h := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr %s(i64 %s)", h, wrap, val))
			return Value{Ref: h, Ty: BigIntType()}, nil
		case k.width == 8:
			return Value{Ref: val, Ty: TypeI64}, nil
		default:
			wide := e.freshReg()
			ext := "zext"
			if k.signed {
				ext = "sext"
			}
			e.emitInstr(fmt.Sprintf("%s = %s i%d %s to i64", wide, ext, bits, val))
			return Value{Ref: wide, Ty: TypeI64}, nil
		}
	}

	// write: coerce the value, swap for the byte order, store; Node returns
	// offset + width.
	rawVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	var narrow string
	switch {
	case k.float:
		f := e.coerce(rawVal, TypeF64)
		if k.width == 4 {
			f32 := e.freshReg()
			narrow = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = fptrunc double %s to float", f32, f.Ref))
			e.emitInstr(fmt.Sprintf("%s = bitcast float %s to i32", narrow, f32))
		} else {
			narrow = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", narrow, f.Ref))
		}
	case k.bigint:
		if !rawVal.Ty.IsBigInt {
			return Value{}, fmt.Errorf("%d:%d: a Buffer BigInt accessor takes a bigint value (e.g. 1n)", pos.Line, pos.Col)
		}
		e.ensureBigInt()
		unwrap := "@__kml_bigint_to_i64"
		if !k.signed {
			unwrap = "@__kml_bigint_to_u64"
		}
		narrow = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 %s(ptr %s)", narrow, unwrap, rawVal.Ref))
	default:
		iv := e.coerce(rawVal, TypeI64)
		if k.width == 8 {
			narrow = iv.Ref
		} else {
			narrow = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i%d", narrow, iv.Ref, bits))
		}
	}
	stored := e.emitDataViewMaybeSwap(narrow, k.width, littleRef)
	e.emitInstr(fmt.Sprintf("store i%d %s, ptr %s, align 1", bits, stored, elemPtr))
	ret := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", ret, offRef, k.width))
	return Value{Ref: ret, Ty: TypeI64}, nil
}

// isBufferMethodName reports whether name is one of the instance methods
// the IsBuffer flag adds (vs the shared array/TypedArray machinery).
func isBufferMethodName(name string) bool {
	switch name {
	case "toString", "write", "copy", "equals", "compare":
		return true
	}
	_, ok := bufferAccessorKindFor(name)
	return ok
}

// emitBufferStringSearch implements the string-argument forms of
// buf.indexOf/includes/lastIndexOf (TDD-00103 follow-up): the needle string's
// UTF-8 bytes are searched over the buffer's raw bytes. `kind` is "indexOf",
// "includes", or "lastIndexOf". Only the single-argument form is supported (no
// byteOffset/encoding). An empty needle matches Node: indexOf → 0,
// lastIndexOf → buffer length, includes → true.
func (e *Emitter) emitBufferStringSearch(mem *ast.MemberExpression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Buffer.%s(string) takes exactly 1 argument (byteOffset/encoding not supported)", pos.Line, pos.Col, method)
	}
	bufPtr, bufLen, _, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	needleVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	needleVal = e.coerce(needleVal, TypePtr)
	e.ensureStrHeaderRuntime() // memmem + __kml_str_len
	needleLen := e.emitStrLenHeader(needleVal.Ref)

	if method == "lastIndexOf" {
		e.ensureMemcmp()
		resAlloca := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resAlloca))
		e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", resAlloca))
		// Empty needle → buffer length (Node).
		isEmpty := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isEmpty, needleLen))
		emptyL := e.freshLabel("buflast.empty")
		scanL := e.freshLabel("buflast.scan")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isEmpty, emptyL, scanL))
		e.emitLabel(emptyL)
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", bufLen, resAlloca))
		doneL := e.freshLabel("buflast.done")
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(scanL)
		// start = bufLen - needleLen; if < 0 the loop never runs.
		start := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", start, bufLen, needleLen))
		idxAlloca := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", start, idxAlloca))
		condL := e.freshLabel("buflast.cond")
		bodyL := e.freshLabel("buflast.body")
		matchL := e.freshLabel("buflast.match")
		nextL := e.freshLabel("buflast.next")
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(condL)
		i := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, idxAlloca))
		neg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, i))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", neg, doneL, bodyL))
		e.emitLabel(bodyL)
		hayAt := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", hayAt, bufPtr, i))
		cmp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @memcmp(ptr %s, ptr %s, i64 %s)", cmp, hayAt, needleVal.Ref, needleLen))
		iseq := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", iseq, cmp))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", iseq, matchL, nextL))
		e.emitLabel(matchL)
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", i, resAlloca))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(nextL)
		iDec := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", iDec, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", iDec, idxAlloca))
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(doneL)
		res := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", res, resAlloca))
		return e.countToNumber(Value{Ref: res, Ty: TypeI64}), nil
	}

	// Forward search (indexOf/includes) via memmem.
	found := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @memmem(ptr %s, i64 %s, ptr %s, i64 %s)", found, bufPtr, bufLen, needleVal.Ref, needleLen))
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, found))
	foundInt := e.freshReg()
	bufInt := e.freshReg()
	diff := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", foundInt, found))
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", bufInt, bufPtr))
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", diff, foundInt, bufInt))
	notFoundIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 -1, i64 %s", notFoundIdx, isNull, diff))
	// An empty needle is index 0 (Node) regardless of what memmem returns for a
	// zero length (macOS returns NULL, glibc returns the haystack).
	isEmptyN := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isEmptyN, needleLen))
	idx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", idx, isEmptyN, notFoundIdx))
	if method == "includes" {
		ge0 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", ge0, idx))
		return Value{Ref: ge0, Ty: TypeBool}, nil
	}
	return e.countToNumber(Value{Ref: idx, Ty: TypeI64}), nil
}

// emitBufferStringFill implements buf.fill(string[, offset[, end]]): the range
// [offset, end) is filled by repeating the string's UTF-8 bytes, aligned to the
// fill offset (Node: `Buffer.alloc(5).fill("ab", 1)` → `00 61 62 61 62`). An
// empty string is a no-op. (ADR-00559.)
func (e *Emitter) emitBufferStringFill(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: Buffer.fill(string) takes 1–3 arguments (value, offset?, end?; encoding not supported)", pos.Line, pos.Col)
	}
	bufPtr, bufLen, _, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	needleVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	needleVal = e.coerce(needleVal, TypePtr)
	e.ensureStrHeaderRuntime()
	needleLen := e.emitStrLenHeader(needleVal.Ref)

	startN := "0"
	if len(args) >= 2 {
		sr, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		startN = e.emitNormalizeSliceIdx(e.coerce(sr, TypeI64).Ref, bufLen)
	}
	endN := bufLen
	if len(args) >= 3 {
		er, err := e.emitExpr(args[2])
		if err != nil {
			return Value{}, err
		}
		endN = e.emitNormalizeSliceIdx(e.coerce(er, TypeI64).Ref, bufLen)
	}

	e.emitBufferRepeatFill(bufPtr, needleVal.Ref, needleLen, startN, endN)
	// Return the same buffer aggregate.
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, bufPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, bufLen))
	return Value{Ref: r1, Ty: e.inferExprType(mem.Object)}, nil
}

// emitBufferRepeatFill fills bufPtr[startN, endN) by repeating needlePtr's
// needleLen bytes, aligned to startN (`Buffer.alloc(5).fill("ab", 1)` →
// `00 61 62 61 62`). An empty needle (needleLen == 0) is a no-op. Shared by
// buf.fill(string) and Buffer.alloc(size, stringFill) (ADR-00559/ADR-00576).
func (e *Emitter) emitBufferRepeatFill(bufPtr, needlePtr, needleLen, startN, endN string) {
	nonEmpty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 0", nonEmpty, needleLen))
	fillL := e.freshLabel("buffill.start")
	doneL := e.freshLabel("buffill.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", nonEmpty, fillL, doneL))

	e.emitLabel(fillL)
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", startN, idxAlloca))
	condL := e.freshLabel("buffill.cond")
	bodyL := e.freshLabel("buffill.body")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, idxAlloca))
	atEnd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, %s", atEnd, i, endN))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", atEnd, doneL, bodyL))
	e.emitLabel(bodyL)
	rel := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", rel, i, startN))
	cyc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = urem i64 %s, %s", cyc, rel, needleLen))
	srcGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", srcGep, needlePtr, cyc))
	byteVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", byteVal, srcGep))
	dstGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", dstGep, bufPtr, i))
	e.emitInstr(fmt.Sprintf("store i8 %s, ptr %s, align 1", byteVal, dstGep))
	iNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", iNext, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", iNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
}

// emitBufferInstanceCall dispatches buffer.<method>(...) for the names the
// IsBuffer flag adds beyond the shared TypedArray/array machinery.
func (e *Emitter) emitBufferInstanceCall(mem *ast.MemberExpression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if k, ok := bufferAccessorKindFor(method); ok {
		return e.emitBufferAccessor(mem, k, args, pos)
	}
	switch method {
	case "toString":
		if len(args) > 3 {
			return Value{}, fmt.Errorf("%d:%d: Buffer.toString takes (encoding?, start?, end?)", pos.Line, pos.Col)
		}
		enc, err := bufferEncodingArg(args, 0, pos)
		if err != nil {
			return Value{}, err
		}
		ptrReg, lenReg, _, err := e.resolveArrayForHOF(mem.Object, pos)
		if err != nil {
			return Value{}, err
		}
		startN := "0"
		if len(args) >= 2 {
			sv, err := e.emitExpr(args[1])
			if err != nil {
				return Value{}, err
			}
			startN = e.emitNormalizeSliceIdx(e.coerce(sv, TypeI64).Ref, lenReg)
		}
		endN := lenReg
		if len(args) == 3 {
			ev, err := e.emitExpr(args[2])
			if err != nil {
				return Value{}, err
			}
			endN = e.emitNormalizeSliceIdx(e.coerce(ev, TypeI64).Ref, lenReg)
		}
		rawLen := e.freshReg()
		isNeg := e.freshReg()
		n := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", rawLen, endN, startN))
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNeg, rawLen))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", n, isNeg, rawLen))
		base := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", base, ptrReg, startN))
		return e.emitBufferEncodeString(base, n, enc), nil

	case "write":
		// write(string, offset?, encoding?) — the 4-arg (string, offset,
		// length, encoding) form is not supported (disclosed caveat).
		if len(args) < 1 || len(args) > 3 {
			return Value{}, fmt.Errorf("%d:%d: Buffer.write takes (string, offset?, encoding?)", pos.Line, pos.Col)
		}
		sv, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		if !isStringTy(sv.Ty) {
			return Value{}, fmt.Errorf("%d:%d: Buffer.write's first argument must be a string", pos.Line, pos.Col)
		}
		offRef := "0"
		encIdx := 1
		if len(args) >= 2 {
			if _, isStr := args[1].(*ast.StringLiteral); isStr {
				encIdx = 1
			} else {
				ov, err := e.emitExpr(args[1])
				if err != nil {
					return Value{}, err
				}
				offRef = e.coerce(ov, TypeI64).Ref
				encIdx = 2
			}
		}
		enc, err := bufferEncodingArg(args, encIdx, pos)
		if err != nil {
			return Value{}, err
		}
		srcPtr, srcLen := e.emitBufferDecodeString(sv.Ref, enc)
		dstPtr, dstLen, _, err := e.resolveArrayForHOF(mem.Object, pos)
		if err != nil {
			return Value{}, err
		}
		// written = min(srcLen, max(dstLen - offset, 0))
		room := e.freshReg()
		roomNeg := e.freshReg()
		room2 := e.freshReg()
		over := e.freshReg()
		cnt := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", room, dstLen, offRef))
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", roomNeg, room))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", room2, roomNeg, room))
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", over, srcLen, room2))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", cnt, over, room2, srcLen))
		e.ensureMemcpy()
		dst := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", dst, dstPtr, offRef))
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", dst, srcPtr, cnt))
		return Value{Ref: cnt, Ty: TypeI64}, nil

	case "copy":
		if len(args) < 1 || len(args) > 4 {
			return Value{}, fmt.Errorf("%d:%d: Buffer.copy takes (target, targetStart?, sourceStart?, sourceEnd?)", pos.Line, pos.Col)
		}
		srcPtr, srcLen, _, err := e.resolveArrayForHOF(mem.Object, pos)
		if err != nil {
			return Value{}, err
		}
		tgtTy := e.inferExprType(args[0])
		if !tgtTy.IsTypedArray || tgtTy.ElemType == nil || tgtTy.ElemType.Align() != 1 {
			return Value{}, fmt.Errorf("%d:%d: Buffer.copy's target must be a Buffer or byte TypedArray", pos.Line, pos.Col)
		}
		tgtPtr, tgtLen, _, err := e.resolveArrayForHOF(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		read := func(idx int, def string) (string, error) {
			if len(args) <= idx {
				return def, nil
			}
			v, err := e.emitExpr(args[idx])
			if err != nil {
				return "", err
			}
			return e.coerce(v, TypeI64).Ref, nil
		}
		tStart, err := read(1, "0")
		if err != nil {
			return Value{}, err
		}
		sStart, err := read(2, "0")
		if err != nil {
			return Value{}, err
		}
		sEnd, err := read(3, srcLen)
		if err != nil {
			return Value{}, err
		}
		// count = min(sEnd - sStart, tgtLen - tStart), clamped at 0.
		c1 := e.freshReg()
		c2 := e.freshReg()
		small := e.freshReg()
		cnt := e.freshReg()
		neg := e.freshReg()
		cnt2 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", c1, sEnd, sStart))
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", c2, tgtLen, tStart))
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", small, c1, c2))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", cnt, small, c1, c2))
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, cnt))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", cnt2, neg, cnt))
		e.ensureMemcpy()
		s := e.freshReg()
		d := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", s, srcPtr, sStart))
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", d, tgtPtr, tStart))
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", d, s, cnt2))
		return Value{Ref: cnt2, Ty: TypeI64}, nil

	case "equals", "compare":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: Buffer.%s takes 1 argument", pos.Line, pos.Col, method)
		}
		aPtr, aLen, _, err := e.resolveArrayForHOF(mem.Object, pos)
		if err != nil {
			return Value{}, err
		}
		bPtr, bLen, _, err := e.resolveArrayForHOF(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		cmp := e.emitBufferCompareCore(aPtr, aLen, bPtr, bLen)
		if method == "compare" {
			return cmp, nil
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", r, cmp.Ref))
		return Value{Ref: r, Ty: TypeBool}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: unknown Buffer method '%s'", pos.Line, pos.Col, method)
}
