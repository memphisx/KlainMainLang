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
// value over a fresh header — the same shape .subarray returns.
func (e *Emitter) bufferAggregate(ptrRef, lenRef string) Value {
	r1 := e.newArrayHeader(ptrRef, lenRef)
	return Value{Ref: r1, Ty: BufferType()}
}

// bufferEncodingNames maps each encoding name a literal may spell, lowercased,
// to its codec.
var bufferEncodingNames = map[string]string{
	"utf8": "utf8", "utf-8": "utf8",
	"hex":       "hex",
	"base64":    "base64",
	"base64url": "base64url",
	"latin1":    "latin1", "binary": "latin1", "ascii": "latin1",
	"utf16le": "utf16le", "utf-16le": "utf16le", "ucs2": "utf16le", "ucs-2": "utf16le",
}

// bufferEncodingArg resolves an optional encoding argument. The codec choice
// is a compile-time dispatch, so the argument must be a string literal;
// names are normalized (utf-8 → utf8, binary/ascii → latin1, ucs2/utf-16le →
// utf16le).
func bufferEncodingArg(args []ast.Expression, idx int, pos ast.Pos) (string, error) {
	if len(args) <= idx {
		return "utf8", nil
	}
	lit, ok := stringLiteralThrough(args[idx])
	if !ok {
		return "", fmt.Errorf("%d:%d: a Buffer encoding must be a string literal (the codec is chosen at compile time)", pos.Line, pos.Col)
	}
	if enc, ok := bufferEncodingNames[strings.ToLower(lit.Value)]; ok {
		return enc, nil
	}
	return "", fmt.Errorf("%d:%d: unknown Buffer encoding '%s' (utf8/hex/base64/base64url/latin1/binary/ascii)", pos.Line, pos.Col, lit.Value)
}

// bufferEncoding resolves an optional encoding argument: a string literal
// picks its codec at compile time (enc), any other expression names one at
// run time (encRef, the evaluated string).
func (e *Emitter) bufferEncoding(args []ast.Expression, idx int, pos ast.Pos) (enc, encRef string, err error) {
	if len(args) > idx {
		if _, ok := stringLiteralThrough(args[idx]); !ok {
			v, err := e.emitExpr(args[idx])
			if err != nil {
				return "", "", err
			}
			if isStringTy(v.Ty) && !v.Ty.IsDynamic() {
				return "", v.Ref, nil // null (undefined) is utf8
			}
			s, err := e.emitArgToString(v)
			if err != nil {
				return "", "", err
			}
			if v.Ty.IsDynamic() && v.Ty.IR == "i64" {
				// An undefined encoding is utf8.
				isU, r := e.freshReg(), e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isU, v.Ref, nbUndefined))
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr null, ptr %s", r, isU, s.Ref))
				return "", r, nil
			}
			return "", s.Ref, nil
		}
	}
	enc, err = bufferEncodingArg(args, idx, pos)
	if err != nil && len(args) > idx {
		// A name Node rejects: its ERR_UNKNOWN_ENCODING, at run time.
		lit, _ := stringLiteralThrough(args[idx])
		return "", e.internString(lit.Value), nil
	}
	return enc, "", err
}

// emitBufferDecodeStringDyn is emitBufferDecodeString for an encoding named
// at run time; an unknown one throws Node's ERR_UNKNOWN_ENCODING.
func (e *Emitter) emitBufferDecodeStringDyn(strRef, encRef string) (ptrRef, lenRef string) {
	e.ensureBufferCodecs()
	outSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", outSlot))
	l := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_buf_decode_enc(ptr %s, ptr %s, ptr %s)", l, strRef, encRef, outSlot))
	bad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", bad, l))
	e.emitUnknownEncodingGuard(bad, encRef)
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", buf, outSlot))
	return buf, l
}

// emitBufferEncodeStringDyn is emitBufferEncodeString for an encoding named
// at run time; an unknown one throws Node's ERR_UNKNOWN_ENCODING.
func (e *Emitter) emitBufferEncodeStringDyn(ptrRef, lenRef, encRef string) Value {
	e.ensureBufferCodecs()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_buf_encode_enc(ptr %s, i64 %s, ptr %s)", r, ptrRef, lenRef, encRef))
	bad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", bad, r))
	e.emitUnknownEncodingGuard(bad, encRef)
	return Value{Ref: r, Ty: TypePtr}
}

// emitUnknownEncodingGuard throws `TypeError [ERR_UNKNOWN_ENCODING]: Unknown
// encoding: <enc>` where bad holds.
func (e *Emitter) emitUnknownEncodingGuard(bad, encRef string) {
	throwL := e.freshLabel("buf.badenc")
	okL := e.freshLabel("buf.enc")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, throwL, okL))
	e.emitLabel(throwL)
	msg, _ := e.emitStringConcat(Value{Ref: e.internString("Unknown encoding: "), Ty: TypePtr}, Value{Ref: encRef, Ty: TypePtr})
	errObj := e.buildErrorObjWithCode(errorKindIDs["TypeError"], msg.Ref, e.internString("TypeError"), e.internString("ERR_UNKNOWN_ENCODING"), "0.0", "null")
	e.excThrow(errObj)
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
}

// emitBufferDecodeString lowers (string, encoding) → (dataPtr, len)
// registers holding a fresh byte buffer.
func (e *Emitter) emitBufferDecodeString(strRef, enc string) (ptrRef, lenRef string) {
	switch enc {
	case "utf8":
		// The string's own length (its header): an embedded NUL is a byte
		// like any other.
		e.ensureStrHeaderRuntime()
		e.ensureMalloc()
		e.ensureMemcpy()
		l := e.freshReg()
		buf := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", l, strRef))
		e.heapMallocInto(buf, l)
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", buf, strRef, l))
		e.emitInstr(fmt.Sprintf("call void @__kml_wtf8_to_utf8(ptr %s, i64 %s)", buf, l))
		return buf, l
	default:
		e.ensureBufferCodecs()
		fn := map[string]string{
			"hex":       "@__kml_buf_hex_dec",
			"base64":    "@__kml_buf_b64_dec",
			"base64url": "@__kml_buf_b64_dec",
			"latin1":    "@__kml_buf_latin1_bytes",
			"utf16le":   "@__kml_buf_utf16le_bytes",
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
		// The WHATWG decoder: invalid sequences become U+FFFD (embedded NULs
		// truncate — the standard string-boundary caveat).
		e.ensureBufferCodecs()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_buf_utf8_str(ptr %s, i64 %s)", r, ptrRef, lenRef))
		return Value{Ref: r, Ty: TypePtr}
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
	case "utf16le":
		e.ensureBufferCodecs()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_buf_utf16le_str(ptr %s, i64 %s)", r, ptrRef, lenRef))
		return Value{Ref: r, Ty: TypePtr}
	case "ascii":
		// Decoding drops each byte's high bit (encoding is latin1's).
		e.ensureBufferCodecs()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_buf_ascii_str(ptr %s, i64 %s)", r, ptrRef, lenRef))
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
	c := &bufferStaticCall{method: method, args: args, pos: pos}
	if f, ok := bufferStatics[method]; ok {
		return f(e, c)
	}
	return Value{}, fmt.Errorf("%d:%d: unknown Buffer method '%s' (from/alloc/allocUnsafe/allocUnsafeSlow/concat/compare/isBuffer/byteLength/isEncoding)", pos.Line, pos.Col, method)
}

type bufferStaticCall struct {
	method string
	args   []ast.Expression
	pos    ast.Pos
}

// bufferStatics are the Buffer constructor's static methods, by name.
var bufferStatics map[string]func(e *Emitter, c *bufferStaticCall) (Value, error)

func init() {
	bufferStatics = map[string]func(e *Emitter, c *bufferStaticCall) (Value, error){
		"from":            (*Emitter).bufferStaticFrom,
		"alloc":           (*Emitter).bufferStaticAlloc,
		"allocUnsafe":     (*Emitter).bufferStaticAlloc,
		"allocUnsafeSlow": (*Emitter).bufferStaticAlloc,
		"concat":          (*Emitter).bufferStaticConcat,
		"compare":         (*Emitter).bufferStaticCompare,
		"isBuffer":        (*Emitter).bufferStaticIsBuffer,
		"byteLength":      (*Emitter).bufferStaticByteLength,
		"isEncoding":      (*Emitter).bufferStaticIsEncoding,
	}
}

// bufferStaticFrom is from().
func (e *Emitter) bufferStaticFrom(c *bufferStaticCall) (Value, error) {
	if len(c.args) == 3 && e.inferExprType(c.args[0]).IsArrayBuffer() {
		return e.emitBufferFromArrayBufferRange(c.args, c.pos)
	}
	if len(c.args) == 3 && e.inferExprType(c.args[0]).IsDynamic() {
		// Known only at runtime: an ArrayBuffer takes (byteOffset,
		// length); anything else reads the second argument as its own
		// (an encoding) and ignores the third.
		var t []ast.Expression
		for _, a := range c.args {
			x, err := e.receiverTemp(a)
			if err != nil {
				return Value{}, err
			}
			t = append(t, x)
		}
		box, err := e.emitExpr(t[0])
		if err != nil {
			return Value{}, err
		}
		isAB := e.emitDynHostInstanceOf(box, "ArrayBuffer")
		abL, otherL, doneL := e.freshLabel("buffrom.ab"), e.freshLabel("buffrom.other"), e.freshLabel("buffrom.done")
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isAB.Ref, abL, otherL))
		var ty Type
		for i, arm := range [][]ast.Expression{{ast.NewAsExpression(t[0], &ast.TypeAnnotation{Name: "ArrayBuffer", Source: "ts"}, c.pos), t[1], t[2]}, {t[0], t[1]}} {
			e.emitLabel([]string{abL, otherL}[i])
			v, err := e.emitBufferStaticCall("from", arm, c.pos)
			if err != nil {
				return Value{}, err
			}
			ty = v.Ty
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", v.Ref, slot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		}
		e.emitLabel(doneL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
		return Value{Ref: r, Ty: ty}, nil
	}
	if len(c.args) < 1 || len(c.args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: Buffer.from takes (string, encoding?) or (array|TypedArray|ArrayBuffer|Buffer)", c.pos.Line, c.pos.Col)
	}
	// Inline array literal: evaluate each element directly into a fresh
	// buffer (the same direct-evaluation trick TypedArray literals use).
	if lit, ok := c.args[0].(*ast.ArrayLiteral); ok {
		n := int64(len(lit.Elements))
		e.ensureMalloc()
		data := e.freshReg()
		e.heapMallocInto(data, fmt.Sprint(n))
		for i, elem := range lit.Elements {
			if _, isSpread := elem.(*ast.SpreadElement); isSpread {
				return Value{}, fmt.Errorf("%d:%d: spread elements in Buffer.from's array literal are not supported", elem.GetPos().Line, elem.GetPos().Col)
			}
			v, err := e.emitExpr(elem)
			if err != nil {
				return Value{}, err
			}
			v = e.convert(v, TypeU8)
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", gep, data, i))
			e.heapStore("i8", v.Ref, gep, 1)
		}
		return e.bufferAggregate(data, fmt.Sprintf("%d", n)), nil
	}
	argTy := e.inferExprType(c.args[0])
	switch {
	case argTy.IsDynamic():
		v, err := e.emitExpr(c.args[0])
		if err != nil {
			return Value{}, err
		}
		return e.emitBufferFromDynamic(v, c.args, c.pos)
	case argTy.IsArrayBuffer() && len(c.args) == 2:
		return e.emitBufferFromArrayBufferRange(c.args, c.pos)
	case argTy.IsArrayBuffer():
		// A view of the ArrayBuffer's bytes, as Node's (TDD-00243).
		v, err := e.emitExpr(c.args[0])
		if err != nil {
			return Value{}, err
		}
		size, data := e.emitBufSizeData(v.Ref)
		e.emitRegisterView(data, v, "0", argTy.IsSharedArrayBuffer())
		return e.bufferAggregate(data, size), nil
	case argTy.IsArray():
		if argTy.BigIntElem {
			return Value{}, fmt.Errorf("%d:%d: Buffer.from cannot copy-construct from a BigInt64Array/BigUint64Array", c.pos.Line, c.pos.Col)
		}
		ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(c.args[0], c.pos)
		if err != nil {
			return Value{}, err
		}
		e.ensureMalloc()
		data := e.freshReg()
		e.heapMallocInto(data, lenReg)
		if err := e.emitBufferCopyConvertLoop(ptrReg, lenReg, elemTy, data); err != nil {
			return Value{}, err
		}
		return e.bufferAggregate(data, lenReg), nil
	default:
		v, err := e.emitExpr(c.args[0])
		if err != nil {
			return Value{}, err
		}
		if !isStringTy(v.Ty) || v.Ty.IsArrayBuffer() {
			return Value{}, fmt.Errorf("%d:%d: Buffer.from takes a string, array, TypedArray, ArrayBuffer, or Buffer", c.pos.Line, c.pos.Col)
		}
		enc, encRef, err := e.bufferEncoding(c.args, 1, c.pos)
		if err != nil {
			return Value{}, err
		}
		if encRef != "" {
			data, l := e.emitBufferDecodeStringDyn(v.Ref, encRef)
			return e.bufferAggregate(data, l), nil
		}
		data, l := e.emitBufferDecodeString(v.Ref, enc)
		return e.bufferAggregate(data, l), nil
	}
}

// bufferStaticAlloc is alloc/allocUnsafe/allocUnsafeSlow().
func (e *Emitter) bufferStaticAlloc(c *bufferStaticCall) (Value, error) {
	if c.method == "allocUnsafeSlow" {
		c.method = "allocUnsafe" // no pool to bypass
	}
	if len(c.args) < 1 || (c.method == "allocUnsafe" && len(c.args) != 1) || len(c.args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: Buffer.%s takes (size%s)", c.pos.Line, c.pos.Col, c.method, map[string]string{"alloc": ", fill?, encoding?", "allocUnsafe": ""}[c.method])
	}
	nVal, err := e.emitExpr(c.args[0])
	if err != nil {
		return Value{}, err
	}
	nRef := e.convert(nVal, TypeI64).Ref
	data := e.freshReg()
	if c.method == "allocUnsafe" {
		e.ensureMalloc()
		e.heapMallocInto(data, nRef)
	} else {
		e.ensureCalloc()
		e.heapCallocInto(data, nRef, "1")
		viaFill := len(c.args) == 3
		if len(c.args) == 2 {
			fillTy := e.inferExprType(c.args[1])
			viaFill = !isNumberTy(fillTy) && !isStringTy(fillTy)
		}
		if viaFill {
			// _fill(buf, fill, 0, length, encoding), as Node's alloc does.
			boxes := make([]string, 0, 4)
			for _, a := range c.args[1:] {
				v, err := e.emitExpr(a)
				if err != nil {
					return Value{}, err
				}
				b, err := e.emitBoxValue(v)
				if err != nil {
					return Value{}, err
				}
				boxes = append(boxes, b.Ref)
			}
			fill := boxes[0]
			enc := fmt.Sprintf("%d", nbUndefined)
			if len(boxes) == 2 {
				enc = boxes[1]
			}
			lenD := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", lenD, nRef))
			e.emitBufferFillBoxes(data, nRef, []string{fill, e.emitNbEncodeDouble("0.0"), e.emitNbEncodeDouble(lenD), enc})
		} else if len(c.args) == 2 {
			fv, err := e.emitExpr(c.args[1])
			if err != nil {
				return Value{}, err
			}
			switch {
			case isNumberTy(fv.Ty):
				b := e.convert(fv, TypeU8)
				wide := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = zext i8 %s to i32", wide, b.Ref))
				e.ensureMemset()
				e.emitInstr(fmt.Sprintf("call ptr @memset(ptr %s, i32 %s, i64 %s)", data, wide, nRef))
			case isStringTy(fv.Ty):
				// String fill (ADR-00576): repeat the fill string's UTF-8 bytes
				// across the whole buffer, matching Node's Buffer.alloc(n, str).
				fv = e.convert(fv, TypePtr)
				e.ensureStrHeaderRuntime()
				needleLen := e.emitStrLenHeader(fv.Ref)
				e.emitBufferRepeatFill(data, fv.Ref, needleLen, "0", nRef)
			default:
				return Value{}, fmt.Errorf("%d:%d: Buffer.alloc's fill must be a number or a string", c.pos.Line, c.pos.Col)
			}
		}
	}
	return e.bufferAggregate(data, nRef), nil
}

// bufferStaticConcat is concat().
func (e *Emitter) bufferStaticConcat(c *bufferStaticCall) (Value, error) {
	if len(c.args) < 1 || len(c.args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: Buffer.concat takes (list, totalLength?)", c.pos.Line, c.pos.Col)
	}
	lit, ok := c.args[0].(*ast.ArrayLiteral)
	if ok {
		// A spread element makes the list's length dynamic: the list path.
		for _, elem := range lit.Elements {
			if _, spread := elem.(*ast.SpreadElement); spread {
				ok = false
				break
			}
		}
	}
	if !ok {
		return e.emitBufferConcatList(c.args, c.pos)
	}
	type part struct{ ptrRef, lenRef string }
	parts := make([]part, 0, len(lit.Elements))
	for i, elem := range lit.Elements {
		pTy := e.inferExprType(elem)
		if pTy.IsDynamic() {
			// A box (an any-held binding): a Uint8Array (a Buffer is one)
			// is read as itself, anything else throws Node's TypeError.
			v, err := e.emitExpr(elem)
			if err != nil {
				return Value{}, err
			}
			u8Kind, _ := arrayElemKind(TypeU8)
			fits := e.emitBoxIsArrayOf(v, []int{anyArrayTyped, anyArrayBuffer}, []int{u8Kind})
			throwL, okL := e.freshLabel("bufcat.throw"), e.freshLabel("bufcat.ok")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", fits, okL, throwL))
			e.emitLabel(throwL)
			e.ensureDynJSONC()
			suffix := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_received(i64 %s)", suffix, v.Ref))
			msg, err := e.emitStringConcat(Value{Ref: e.internString(fmt.Sprintf("The \"list[%d]\" argument must be an instance of Buffer or Uint8Array.", i)), Ty: TypePtr}, Value{Ref: suffix, Ty: TypePtr})
			if err != nil {
				return Value{}, err
			}
			e.emitThrowCoded("TypeError", "ERR_INVALID_ARG_TYPE", msg.Ref)
			e.emitLabel(okL)
			buf := e.convert(v, BufferType())
			data, n := e.arrayParts(buf)
			parts = append(parts, part{ptrRef: data, lenRef: n})
			continue
		}
		if !pTy.IsTypedArray() || pTy.BigIntElem {
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
	if len(c.args) == 2 {
		tv, err := e.emitExpr(c.args[1])
		if err != nil {
			return Value{}, err
		}
		total = e.convert(tv, TypeI64).Ref
	}
	e.ensureCalloc()
	e.ensureMemcpy()
	data := e.freshReg()
	e.heapCallocInto(data, total, "1")
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
}

// bufferStaticCompare is compare().
func (e *Emitter) bufferStaticCompare(c *bufferStaticCall) (Value, error) {
	if len(c.args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: Buffer.compare takes (a, b)", c.pos.Line, c.pos.Col)
	}
	aPtr, aLen, _, err := e.resolveArrayForHOF(c.args[0], c.pos)
	if err != nil {
		return Value{}, err
	}
	bPtr, bLen, _, err := e.resolveArrayForHOF(c.args[1], c.pos)
	if err != nil {
		return Value{}, err
	}
	return e.emitBufferCompareCore(aPtr, aLen, bPtr, bLen), nil
}

// bufferStaticIsBuffer is isBuffer().
func (e *Emitter) bufferStaticIsBuffer(c *bufferStaticCall) (Value, error) {
	if len(c.args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Buffer.isBuffer takes 1 argument", c.pos.Line, c.pos.Col)
	}
	// A compile-time constant from the inferred type (the argument is
	// still evaluated for its side effects), except for an `any`, whose
	// box says.
	v, err := e.emitExpr(c.args[0])
	if err != nil {
		return Value{}, err
	}
	if isSelfDescribingBox(v.Ty) {
		return Value{Ref: e.emitBoxIsTypedArray(v, anyArrayBuffer), Ty: TypeBool}, nil
	}
	if v.Ty.IsBuffer() {
		return Value{Ref: "true", Ty: TypeBool}, nil
	}
	return Value{Ref: "false", Ty: TypeBool}, nil
}

// bufferStaticByteLength is byteLength().
func (e *Emitter) bufferStaticByteLength(c *bufferStaticCall) (Value, error) {
	if len(c.args) < 1 || len(c.args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: Buffer.byteLength takes (string, encoding?)", c.pos.Line, c.pos.Col)
	}
	v, err := e.emitExpr(c.args[0])
	if err != nil {
		return Value{}, err
	}
	if v.Ty.IsTypedArray() || v.Ty.IsArrayBuffer() {
		return Value{}, fmt.Errorf("%d:%d: Buffer.byteLength here takes a string (use .byteLength on a TypedArray/ArrayBuffer value)", c.pos.Line, c.pos.Col)
	}
	if v.Ty.IsDynamic() {
		// An untyped string: its text.
		if sv, ok := e.emitAnyIntoString(v, TypePtr); ok {
			v = sv
		}
	}
	// Node's per-encoding formula (__kml_buf_byte_length): hex is the
	// length >>> 1 however valid its digits, an unknown name is utf8.
	encRef := "null"
	if len(c.args) > 1 {
		lit, isLit := stringLiteralThrough(c.args[1])
		switch {
		case isLit:
			encRef = e.internString(lit.Value)
		default:
			_, r, err := e.bufferEncoding(c.args, 1, c.pos)
			if err != nil {
				return Value{}, err
			}
			encRef = r
		}
	}
	e.ensureBufferCodecs()
	l := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_buf_byte_length(ptr %s, ptr %s)", l, v.Ref, encRef))
	return Value{Ref: l, Ty: TypeI64}, nil
}

// bufferStaticIsEncoding is isEncoding().
func (e *Emitter) bufferStaticIsEncoding(c *bufferStaticCall) (Value, error) {
	// Buffer.isEncoding(x): a non-empty string naming an encoding Node
	// knows, case-insensitively; anything else is false.
	if len(c.args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Buffer.isEncoding takes 1 argument", c.pos.Line, c.pos.Col)
	}
	v, err := e.emitExpr(c.args[0])
	if err != nil {
		return Value{}, err
	}
	var str string
	switch {
	case v.Ty.IsDynamic():
		tag, pay := e.emitUnboxTagPayload(v)
		isStr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isStr, tag, kmlTagString))
		p := e.emitIntToPtr(pay)
		sel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr null", sel, isStr, p))
		str = sel
	case isStringTy(v.Ty):
		str = v.Ref
	default:
		return Value{Ref: "false", Ty: TypeBool}, nil
	}
	e.ensureBufferCodecs()
	r, b := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_buf_is_encoding(ptr %s)", r, str))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", b, r))
	return Value{Ref: b, Ty: TypeBool}, nil
}

// emitBufferCopyConvertLoop copies lenReg elements from a source array
// (any numeric elemTy) into dstData as u8, with the standard wrap coercion.
func (e *Emitter) emitBufferCopyConvertLoop(srcPtr, lenReg string, srcElemTy Type, dstData string) error {
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
	c := e.convert(Value{Ref: sVal, Ty: srcElemTy}, TypeU8)
	dGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", dGep, dstData, i))
	e.heapStore("i8", c.Ref, dGep, 1)
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

// bufferAccessorShapes are the element types a read*/write* name carries
// between its prefix and its LE/BE suffix, each with Node's two spellings
// of "UInt".
var bufferAccessorShapes = map[string]bufferAccessorKind{
	"UInt8": {width: 1}, "Uint8": {width: 1},
	"Int8":   {width: 1, signed: true},
	"UInt16": {width: 2}, "Uint16": {width: 2},
	"Int16":  {width: 2, signed: true},
	"UInt32": {width: 4}, "Uint32": {width: 4},
	"Int32":     {width: 4, signed: true},
	"Float":     {width: 4, float: true, signed: true},
	"Double":    {width: 8, float: true, signed: true},
	"BigInt64":  {width: 8, bigint: true, signed: true},
	"BigUInt64": {width: 8, bigint: true}, "BigUint64": {width: 8, bigint: true},
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
	shape, ok := bufferAccessorShapes[rest]
	if !ok {
		return k, false
	}
	k.width, k.signed, k.float, k.bigint = shape.width, shape.signed, shape.float, shape.bigint
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
		if ov.Ty.Float {
			// boundsError: a fractional offset is out of range as "an
			// integer", before any bounds are looked at.
			f := e.convert(ov, TypeF64)
			fl, frac := e.freshReg(), e.freshReg()
			e.ensureMathFuncs()
			e.emitInstr(fmt.Sprintf("%s = call double @floor(double %s)", fl, f.Ref))
			e.emitInstr(fmt.Sprintf("%s = fcmp une double %s, %s", frac, fl, f.Ref))
			fracL, intL := e.freshLabel("buf.acc.frac"), e.freshLabel("buf.acc.int")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", frac, fracL, intL))
			e.emitLabel(fracL)
			recv, err := e.emitValueToString(f)
			if err != nil {
				return Value{}, err
			}
			msg, err := e.emitStringConcat(Value{Ref: e.internString(`The value of "offset" is out of range. It must be an integer. Received `), Ty: TypePtr}, recv)
			if err != nil {
				return Value{}, err
			}
			e.emitThrowCoded("RangeError", "ERR_OUT_OF_RANGE", msg.Ref)
			e.emitLabel(intL)
		}
		offRef = e.convert(ov, TypeI64).Ref
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
	// Node's boundsError: a buffer shorter than the access is
	// ERR_BUFFER_OUT_OF_BOUNDS; otherwise the offset is out of
	// [0, length - width], ERR_OUT_OF_RANGE naming both.
	maxOff, short := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %d", maxOff, lenReg, k.width))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", short, maxOff))
	shortL, rangeL := e.freshLabel("buf.acc.short"), e.freshLabel("buf.acc.range")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", short, shortL, rangeL))
	e.emitLabel(shortL)
	e.emitThrowCoded("RangeError", "ERR_BUFFER_OUT_OF_BOUNDS", e.internString("Attempt to access memory outside buffer bounds"))
	e.emitLabel(rangeL)
	maxStr, err := e.emitValueToString(Value{Ref: maxOff, Ty: TypeI64})
	if err != nil {
		return Value{}, err
	}
	msg, err := e.emitStringConcat(Value{Ref: e.internString(`The value of "offset" is out of range. It must be >= 0 and <= `), Ty: TypePtr}, maxStr)
	if err != nil {
		return Value{}, err
	}
	if msg, err = e.emitStringConcat(msg, Value{Ref: e.internString(". Received "), Ty: TypePtr}); err != nil {
		return Value{}, err
	}
	e.ensureReceivedInt()
	recv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_received_int(i64 %s)", recv, offRef))
	if msg, err = e.emitStringConcat(msg, Value{Ref: recv, Ty: TypePtr}); err != nil {
		return Value{}, err
	}
	e.emitThrowCoded("RangeError", "ERR_OUT_OF_RANGE", msg.Ref)
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
		val := e.emitMaybeSwap(raw, k.width, littleRef)
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
		f := e.convert(rawVal, TypeF64)
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
		if !rawVal.Ty.IsBigInt() {
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
		iv := e.convert(rawVal, TypeI64)
		if k.width == 8 {
			narrow = iv.Ref
		} else {
			narrow = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i%d", narrow, iv.Ref, bits))
		}
	}
	stored := e.emitMaybeSwap(narrow, k.width, littleRef)
	e.heapStore(fmt.Sprintf("i%d", bits), stored, elemPtr, 1)
	ret := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", ret, offRef, k.width))
	return Value{Ref: ret, Ty: TypeI64}, nil
}

// isBufferMethodName reports whether name is one of the instance methods
// the IsBuffer flag adds (vs the shared array/TypedArray machinery).
func isBufferMethodName(name string) bool {
	if _, ok := bufferInstanceMethods[name]; ok {
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
	needleVal = e.convert(needleVal, TypePtr)
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

// emitBufferFill is buf.fill(value, offset?, end?, encoding?), Node's
// argument reading and validation (dynjson.c's __kml_buf_fill_any) over the
// boxed arguments; it returns the receiver.
func (e *Emitter) emitBufferFill(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 4 {
		return Value{}, fmt.Errorf("%d:%d: Buffer.fill takes (value, offset?, end?, encoding?)", pos.Line, pos.Col)
	}
	recvTy := e.inferExprType(mem.Object)
	hdr, ptrReg, lenReg, elemTy, err := e.resolveArrayHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	e.emitArrayGuardFor(mem.Object, arrOpReorder, nil)
	var boxes []string
	for _, a := range args {
		v, err := e.emitExpr(a)
		if err != nil {
			return Value{}, err
		}
		b, err := e.emitBoxValue(v)
		if err != nil {
			return Value{}, err
		}
		boxes = append(boxes, b.Ref)
	}
	e.emitBufferFillBoxes(ptrReg, lenReg, boxes)
	r := e.receiverArrayHeader(hdr, ptrReg, lenReg)
	if !recvTy.IsArray() {
		recvTy = ArrayOf(elemTy)
	}
	return Value{Ref: r, Ty: recvTy}, nil
}

// emitBufferFillBoxes fills the len bytes at data as buf.fill does with the
// boxed arguments boxes (the value first), throwing Node's error for a bad
// argument.
func (e *Emitter) emitBufferFillBoxes(data, length string, boxes []string) {
	e.ensureBufFill()
	argv := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca [4 x i64], align 8", argv))
	for i, b := range boxes {
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr [4 x i64], ptr %s, i64 0, i64 %d", slot, argv, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", b, slot))
	}
	msgSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", msgSlot))
	code := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_buf_fill_any(ptr %s, i64 %s, i64 %d, ptr %s, ptr %s)", code, data, length, len(boxes), argv, msgSlot))
	okL := e.freshLabel("buffill.ok")
	errs := []struct{ kind, code string }{{"TypeError", "ERR_INVALID_ARG_TYPE"}, {"RangeError", "ERR_OUT_OF_RANGE"}, {"TypeError", "ERR_UNKNOWN_ENCODING"}, {"TypeError", "ERR_INVALID_ARG_VALUE"}}
	labels := make([]string, len(errs))
	cases := ""
	for i := range errs {
		labels[i] = e.freshLabel("buffill.err")
		cases += fmt.Sprintf(" i64 %d, label %%%s", i+1, labels[i])
	}
	e.emitTerminator(fmt.Sprintf("switch i64 %s, label %%%s [%s ]", code, okL, cases))
	for i, x := range errs {
		e.emitLabel(labels[i])
		msg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", msg, msgSlot))
		e.emitThrowCoded(x.kind, x.code, msg)
	}
	e.emitLabel(okL)
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
	e.heapStore("i8", byteVal, dstGep, 1)
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
	c := &bufferInstanceCall{mem: mem, method: method, args: args, pos: pos}
	if f, ok := bufferInstanceMethods[method]; ok {
		return f(e, c)
	}
	return Value{}, fmt.Errorf("%d:%d: unknown Buffer method '%s'", pos.Line, pos.Col, method)
}

type bufferInstanceCall struct {
	mem    *ast.MemberExpression
	method string
	args   []ast.Expression
	pos    ast.Pos
}

// bufferInstanceMethods are a Buffer's own instance methods other than its typed accessors, by name.
var bufferInstanceMethods map[string]func(e *Emitter, c *bufferInstanceCall) (Value, error)

func init() {
	bufferInstanceMethods = map[string]func(e *Emitter, c *bufferInstanceCall) (Value, error){
		"toString": (*Emitter).bufferInstanceToString,
		"write":    (*Emitter).bufferInstanceWrite,
		"copy":     (*Emitter).bufferInstanceCopy,
		"equals":   (*Emitter).bufferInstanceEquals,
		"compare":  (*Emitter).bufferInstanceEquals,
	}
}

// bufferInstanceToString is toString().
func (e *Emitter) bufferInstanceToString(c *bufferInstanceCall) (Value, error) {
	if len(c.args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: Buffer.toString takes (encoding?, start?, end?)", c.pos.Line, c.pos.Col)
	}
	enc, encRef, err := e.bufferEncoding(c.args, 0, c.pos)
	if err != nil {
		return Value{}, err
	}
	if len(c.args) > 0 {
		if lit, ok := stringLiteralThrough(c.args[0]); ok && strings.EqualFold(lit.Value, "ascii") {
			enc = "ascii"
		}
	}
	ptrReg, lenReg, _, err := e.resolveArrayForHOF(c.mem.Object, c.pos)
	if err != nil {
		return Value{}, err
	}
	startN := "0"
	if len(c.args) >= 2 {
		sv, err := e.emitExpr(c.args[1])
		if err != nil {
			return Value{}, err
		}
		startN = e.emitNormalizeSliceIdx(e.convert(sv, TypeI64).Ref, lenReg)
	}
	endN := lenReg
	if len(c.args) == 3 {
		ev, err := e.emitExpr(c.args[2])
		if err != nil {
			return Value{}, err
		}
		endN = e.emitNormalizeSliceIdx(e.convert(ev, TypeI64).Ref, lenReg)
	}
	rawLen := e.freshReg()
	isNeg := e.freshReg()
	n := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", rawLen, endN, startN))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNeg, rawLen))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", n, isNeg, rawLen))
	base := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", base, ptrReg, startN))
	if encRef != "" {
		return e.emitBufferEncodeStringDyn(base, n, encRef), nil
	}
	return e.emitBufferEncodeString(base, n, enc), nil
}

// bufferInstanceWrite is write().
func (e *Emitter) bufferInstanceWrite(c *bufferInstanceCall) (Value, error) {
	// write(string, offset?, encoding?) — the 4-arg (string, offset,
	// length, encoding) form is not supported (disclosed caveat).
	if len(c.args) < 1 || len(c.args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: Buffer.write takes (string, offset?, encoding?)", c.pos.Line, c.pos.Col)
	}
	sv, err := e.emitExpr(c.args[0])
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(sv.Ty) {
		return Value{}, fmt.Errorf("%d:%d: Buffer.write's first argument must be a string", c.pos.Line, c.pos.Col)
	}
	offRef := "0"
	encIdx := 1
	if len(c.args) >= 2 {
		if _, isStr := c.args[1].(*ast.StringLiteral); isStr {
			encIdx = 1
		} else {
			ov, err := e.emitExpr(c.args[1])
			if err != nil {
				return Value{}, err
			}
			offRef = e.convert(ov, TypeI64).Ref
			encIdx = 2
		}
	}
	enc, encRef, err := e.bufferEncoding(c.args, encIdx, c.pos)
	if err != nil {
		return Value{}, err
	}
	var srcPtr, srcLen string
	if encRef != "" {
		srcPtr, srcLen = e.emitBufferDecodeStringDyn(sv.Ref, encRef)
	} else {
		srcPtr, srcLen = e.emitBufferDecodeString(sv.Ref, enc)
	}
	dstPtr, dstLen, _, err := e.resolveArrayForHOF(c.mem.Object, c.pos)
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
}

// bufferInstanceCopy is copy().
func (e *Emitter) bufferInstanceCopy(c *bufferInstanceCall) (Value, error) {
	if len(c.args) < 1 || len(c.args) > 4 {
		return Value{}, fmt.Errorf("%d:%d: Buffer.copy takes (target, targetStart?, sourceStart?, sourceEnd?)", c.pos.Line, c.pos.Col)
	}
	srcPtr, srcLen, _, err := e.resolveArrayForHOF(c.mem.Object, c.pos)
	if err != nil {
		return Value{}, err
	}
	tgtTy := e.inferExprType(c.args[0])
	if !tgtTy.IsTypedArray() || tgtTy.ElemType == nil || tgtTy.ElemType.Align() != 1 {
		return Value{}, fmt.Errorf("%d:%d: Buffer.copy's target must be a Buffer or byte TypedArray", c.pos.Line, c.pos.Col)
	}
	tgtPtr, tgtLen, _, err := e.resolveArrayForHOF(c.args[0], c.pos)
	if err != nil {
		return Value{}, err
	}
	read := func(idx int, def string) (string, error) {
		if len(c.args) <= idx {
			return def, nil
		}
		v, err := e.emitExpr(c.args[idx])
		if err != nil {
			return "", err
		}
		return e.convert(v, TypeI64).Ref, nil
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
}

// bufferInstanceEquals is equals/compare().
func (e *Emitter) bufferInstanceEquals(c *bufferInstanceCall) (Value, error) {
	if len(c.args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Buffer.%s takes 1 argument", c.pos.Line, c.pos.Col, c.method)
	}
	aPtr, aLen, _, err := e.resolveArrayForHOF(c.mem.Object, c.pos)
	if err != nil {
		return Value{}, err
	}
	bPtr, bLen, _, err := e.resolveArrayForHOF(c.args[0], c.pos)
	if err != nil {
		return Value{}, err
	}
	cmp := e.emitBufferCompareCore(aPtr, aLen, bPtr, bLen)
	if c.method == "compare" {
		return cmp, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", r, cmp.Ref))
	return Value{Ref: r, Ty: TypeBool}, nil
}

// emitBufferConcatList is Buffer.concat(list, totalLength?) over a runtime
// array of Buffers or byte TypedArrays (an inline literal is joined in IR
// instead).
func (e *Emitter) emitBufferConcatList(args []ast.Expression, pos ast.Pos) (Value, error) {
	lt := e.inferExprType(args[0])
	if !lt.IsArray() || lt.ElemType == nil || !lt.ElemType.IsTypedArray() || lt.ElemType.BigIntElem || lt.ElemType.ElemType == nil || lt.ElemType.ElemType.Align() != 1 {
		return Value{}, fmt.Errorf("%d:%d: Buffer.concat's list must be an array of Buffers or byte TypedArrays", pos.Line, pos.Col)
	}
	ptrReg, lenReg, _, err := e.resolveArrayForHOF(args[0], pos)
	if err != nil {
		return Value{}, err
	}
	total := "-1"
	if len(args) == 2 {
		tv, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		total = e.convert(tv, TypeI64).Ref
	}
	e.ensureBufferCodecs()
	outLen := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", outLen))
	data := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_buf_concat(ptr %s, i64 %s, i64 %s, ptr %s)", data, ptrReg, lenReg, total, outLen))
	n := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", n, outLen))
	return e.bufferAggregate(data, n), nil
}

// stringLiteralThrough is expr's string literal, seen through a type
// assertion (`'UCS-2' as BufferEncoding`), which changes no value.
func stringLiteralThrough(expr ast.Expression) (*ast.StringLiteral, bool) {
	for {
		switch x := expr.(type) {
		case *ast.StringLiteral:
			return x, true
		case *ast.AsExpression:
			expr = x.Expr
		default:
			return nil, false
		}
	}
}

// emitBufferFromDynamic is Buffer.from(v) for a boxed v, dispatched at run
// time as Node does: a string decodes (with the encoding argument), an
// ArrayBuffer's bytes are copied, and anything array-like has each element
// converted to a byte. null/undefined throw.
func (e *Emitter) emitBufferFromDynamic(v Value, args []ast.Expression, pos ast.Pos) (Value, error) {
	tag, payload := e.emitUnboxTagPayload(v)
	dataSlot, lenSlot := e.freshReg(), e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", dataSlot))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenSlot))
	doneL := e.freshLabel("buffrom.done")
	put := func(data, n string) {
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", data, dataSlot))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", n, lenSlot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	// A string.
	strL, notStrL := e.emitTagCheck(tag, kmlTagString, "buffrom.str")
	e.emitLabel(strL)
	s := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", s, payload))
	enc, encRef, err := e.bufferEncoding(args, 1, pos)
	if err != nil {
		return Value{}, err
	}
	var data, n string
	if encRef != "" {
		data, n = e.emitBufferDecodeStringDyn(s, encRef)
	} else {
		data, n = e.emitBufferDecodeString(s, enc)
	}
	put(data, n)
	e.emitLabel(notStrL)
	// An ArrayBuffer (a host box, emit_hostbox.go).
	isAB := e.emitDynHostInstanceOf(v, "ArrayBuffer")
	isSAB := e.emitDynHostInstanceOf(v, "SharedArrayBuffer")
	either := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", either, isAB.Ref, isSAB.Ref))
	abL, notABL := e.freshLabel("buffrom.ab"), e.freshLabel("buffrom.notab")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", either, abL, notABL))
	e.emitLabel(abL)
	h := e.emitUnboxHost(v, ArrayBufferType())
	size, bytes := e.emitBufSizeData(h.Ref)
	e.emitRegisterViewDyn(bytes, h, "0", isSAB.Ref)
	put(bytes, size)
	e.emitLabel(notABL)
	// Anything array-like: each element to a byte.
	e.ensureDynJSONC()
	lp := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lp))
	f64s := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_any_arraylike_f64(i64 %s, ptr %s)", f64s, v.Ref, lp))
	cnt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cnt, lp))
	bad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", bad, cnt))
	badL, okL := e.freshLabel("buffrom.bad"), e.freshLabel("buffrom.arr")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
	e.emitLabel(badL)
	e.emitThrowTypeError("The first argument must be of type string or an instance of Buffer, ArrayBuffer, or Array or an Array-like Object.")
	e.emitLabel(okL)
	e.ensureMalloc()
	out := e.freshReg()
	e.heapMallocInto(out, cnt)
	if err := e.emitBufferCopyConvertLoop(f64s, cnt, TypeF64, out); err != nil {
		return Value{}, err
	}
	put(out, cnt)
	e.emitLabel(doneL)
	d, l := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", d, dataSlot))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", l, lenSlot))
	return e.bufferAggregate(d, l), nil
}

// emitBufferFromArrayBufferRange implements Buffer.from(arrayBuffer,
// byteOffset, length?): the bytes from byteOffset, length of them (the rest
// by default), as a copy (the same caveat as the one-argument form). An
// offset past the end, or a length beyond it, is Node's RangeError
// ERR_BUFFER_OUT_OF_BOUNDS.
func (e *Emitter) emitBufferFromArrayBufferRange(args []ast.Expression, pos ast.Pos) (Value, error) {
	v, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	size, data := e.emitBufSizeData(v.Ref)
	ov, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	off := e.convert(ov, TypeI64).Ref
	e.ensureExceptionHelpers()
	badOff := e.freshReg()
	neg := e.freshReg()
	past := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, off))
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", past, off, size))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", badOff, neg, past))
	offBadL, offOkL := e.freshLabel("buffrom.offbad"), e.freshLabel("buffrom.offok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", badOff, offBadL, offOkL))
	e.emitLabel(offBadL)
	e.emitThrowCoded("RangeError", "ERR_BUFFER_OUT_OF_BOUNDS", e.internString("\"offset\" is outside of buffer bounds"))
	e.emitLabel(offOkL)
	rest := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", rest, size, off))
	n := rest
	if len(args) > 2 {
		lv, err := e.emitExpr(args[2])
		if err != nil {
			return Value{}, err
		}
		l := e.convert(lv, TypeI64).Ref
		badLen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", badLen, l, rest))
		lenBadL, lenOkL := e.freshLabel("buffrom.lenbad"), e.freshLabel("buffrom.lenok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", badLen, lenBadL, lenOkL))
		e.emitLabel(lenBadL)
		e.emitThrowCoded("RangeError", "ERR_BUFFER_OUT_OF_BOUNDS", e.internString("\"length\" is outside of buffer bounds"))
		e.emitLabel(lenOkL)
		neg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, l))
		clamped := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", clamped, neg, l))
		n = clamped
	}
	src := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", src, data, off))
	// A view of that range, as Node's (TDD-00243).
	e.emitRegisterView(src, v, off, v.Ty.IsSharedArrayBuffer())
	return e.bufferAggregate(src, n), nil
}

// bufferIntrinsicNames are the Buffer methods emitBufferInstanceCall
// implements, the keys of their `Buffer.prototype.*` intrinsics.
func bufferIntrinsicNames() []string {
	names := []string{"toString", "write", "copy", "equals", "compare"}
	for _, op := range []string{"read", "write"} {
		for _, w := range []string{"UInt8", "Uint8", "Int8", "UInt16", "Uint16", "Int16", "UInt32", "Uint32", "Int32", "Float", "Double", "BigInt64", "BigUInt64", "BigUint64"} {
			for _, end := range []string{"", "LE", "BE"} {
				if n := op + w + end; isBufferMethodName(n) {
					names = append(names, n)
				}
			}
		}
	}
	return names
}

// ensureBswap declares the llvm.bswap intrinsic of a width-byte integer.
func (e *Emitter) ensureBswap(width int) {
	switch width {
	case 2:
		if !e.usedBswap16 {
			e.usedBswap16 = true
			e.emitGlobal("declare i16 @llvm.bswap.i16(i16)")
		}
	case 4:
		if !e.usedBswap32 {
			e.usedBswap32 = true
			e.emitGlobal("declare i32 @llvm.bswap.i32(i32)")
		}
	case 8:
		if !e.usedBswap64 {
			e.usedBswap64 = true
			e.emitGlobal("declare i64 @llvm.bswap.i64(i64)")
		}
	}
}

// emitMaybeSwap emits the conditional byte swap of an iN register raw:
// littleRef is a runtime i1 little-endian flag. The host is little-endian,
// so the value is swapped exactly when littleRef is false.
func (e *Emitter) emitMaybeSwap(raw string, width int, littleRef string) string {
	if width == 1 {
		return raw
	}
	e.ensureBswap(width)
	bits := width * 8
	swapped := e.freshReg()
	sel := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i%d @llvm.bswap.i%d(i%d %s)", swapped, bits, bits, bits, raw))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i%d %s, i%d %s", sel, littleRef, bits, raw, bits, swapped))
	return sel
}
