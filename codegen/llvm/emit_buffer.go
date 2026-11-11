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
	"strconv"
	"strings"

	"KlainMainLang/ast"
)

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

// bufferEncodingLiteral resolves an optional encoding argument that is
// absent (utf8) or a string literal naming a codec; names are normalized
// (utf-8 → utf8, binary/ascii → latin1, ucs2/utf-16le → utf16le). ok is
// false for any other argument.
func bufferEncodingLiteral(args []ast.Expression, idx int) (enc string, ok bool) {
	if len(args) <= idx {
		return "utf8", true
	}
	lit, isLit := stringLiteralThrough(args[idx])
	if !isLit {
		return "", false
	}
	enc, ok = bufferEncodingNames[strings.ToLower(lit.Value)]
	return enc, ok
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
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" null, "+irPtr+" %s", r, isU, s.Ref))
				return "", r, nil
			}
			return "", s.Ref, nil
		}
	}
	enc, ok := bufferEncodingLiteral(args, idx)
	if !ok {
		// A name Node rejects: its ERR_UNKNOWN_ENCODING, at run time.
		lit, _ := stringLiteralThrough(args[idx])
		return "", e.internString(lit.Value), nil
	}
	return enc, "", nil
}

// emitBufferDecodeStringDyn is emitBufferDecodeString for an encoding named
// at run time; an unknown one throws Node's ERR_UNKNOWN_ENCODING.
func (e *Emitter) emitBufferDecodeStringDyn(strRef, encRef string) (ptrRef, lenRef string) {
	outSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", outSlot))
	l := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_buf_decode_enc("+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s)", l, strRef, encRef, outSlot))
	bad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", bad, l))
	e.emitUnknownEncodingGuard(bad, encRef)
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", buf, outSlot))
	return buf, l
}

// emitBufferEncodeStringDyn is emitBufferEncodeString for an encoding named
// at run time; an unknown one throws Node's ERR_UNKNOWN_ENCODING.
func (e *Emitter) emitBufferEncodeStringDyn(ptrRef, lenRef, encRef string) Value {
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_buf_encode_enc("+irPtr+" %s, i64 %s, "+irPtr+" %s)", r, ptrRef, lenRef, encRef))
	bad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, null", bad, r))
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
		l := e.freshReg()
		buf := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len("+irPtr+" %s)", l, strRef))
		e.heapMallocInto(buf, allocBytes, l)
		e.emitInstr(fmt.Sprintf("call "+irPtr+" @memcpy("+irPtr+" %s, "+irPtr+" %s, i64 %s)", buf, strRef, l))
		e.emitInstr(fmt.Sprintf("call void @__kml_wtf8_to_utf8("+irPtr+" %s, i64 %s)", buf, l))
		return buf, l
	default:
		fn := map[string]string{
			"hex":       "@__kml_buf_hex_dec",
			"base64":    "@__kml_buf_b64_dec",
			"base64url": "@__kml_buf_b64_dec",
			"latin1":    "@__kml_buf_latin1_bytes",
			"utf16le":   "@__kml_buf_utf16le_bytes",
		}[enc]
		outSlot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", outSlot))
		l := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 %s("+irPtr+" %s, "+irPtr+" %s)", l, fn, strRef, outSlot))
		buf := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", buf, outSlot))
		return buf, l
	}
}

// emitBufferEncodeString lowers (dataPtr, len, encoding) → a string value.
func (e *Emitter) emitBufferEncodeString(ptrRef, lenRef, enc string) Value {
	switch enc {
	case "utf8":
		// The WHATWG decoder: invalid sequences become U+FFFD (embedded NULs
		// truncate — the standard string-boundary caveat).
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_buf_utf8_str("+irPtr+" %s, i64 %s)", r, ptrRef, lenRef))
		return Value{Ref: r, Ty: TypePtr}
	case "hex":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_buf_hex_enc("+irPtr+" %s, i64 %s)", r, ptrRef, lenRef))
		return Value{Ref: r, Ty: TypePtr}
	case "base64", "base64url":
		urlsafe := "0"
		if enc == "base64url" {
			urlsafe = "1"
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_buf_b64_enc("+irPtr+" %s, i64 %s, i32 %s)", r, ptrRef, lenRef, urlsafe))
		return Value{Ref: r, Ty: TypePtr}
	case "utf16le":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_buf_utf16le_str("+irPtr+" %s, i64 %s)", r, ptrRef, lenRef))
		return Value{Ref: r, Ty: TypePtr}
	case "ascii":
		// Decoding drops each byte's high bit (encoding is latin1's).
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_buf_ascii_str("+irPtr+" %s, i64 %s)", r, ptrRef, lenRef))
		return Value{Ref: r, Ty: TypePtr}
	default: // latin1
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_buf_latin1_str("+irPtr+" %s, i64 %s)", r, ptrRef, lenRef))
		return Value{Ref: r, Ty: TypePtr}
	}
}

// emitBufferStaticCall dispatches Buffer.<method>(...).
func (e *Emitter) emitBufferStaticCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if a, ok := bufferStaticArity[method]; ok {
		call := ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier("Buffer", pos), method, pos), args, pos)
		fitted, v, thrown, err := e.applyBufferArity(a, args, call, pos)
		if err != nil || thrown {
			return v, err
		}
		args = fitted
	}
	c := &bufferStaticCall{method: method, args: args, pos: pos}
	if f, ok := bufferStatics[method]; ok {
		return f(e, c)
	}
	return Value{}, internalAt(pos, "Buffer.%s", method)
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
	if len(c.args) == 3 && e.typeOfExpr(c.args[0]).IsArrayBuffer() {
		return e.emitBufferFromArrayBufferRange(c.args, c.pos)
	}
	if len(c.args) == 3 && e.typeOfExpr(c.args[0]).IsDynamic() {
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
		e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isAB.Ref, abL, otherL))
		var ty Type
		for i, arm := range [][]ast.Expression{{ast.NewAsExpression(t[0], nameAnnotation("ArrayBuffer"), c.pos), t[1], t[2]}, {t[0], t[1]}} {
			e.emitLabel([]string{abL, otherL}[i])
			v, err := e.emitBufferStaticCall("from", arm, c.pos)
			if err != nil {
				return Value{}, err
			}
			ty = v.Ty
			e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", v.Ref, slot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		}
		e.emitLabel(doneL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", r, slot))
		return Value{Ref: r, Ty: ty}, nil
	}
	if len(c.args) > 2 {
		// A third argument only an ArrayBuffer reads (its length).
		args, err := e.dropArgsPast(c.args, 2, c.pos)
		if err != nil {
			return Value{}, err
		}
		c.args = args
	}
	if len(c.args) < 1 {
		return Value{}, internalAt(c.pos, "Buffer.from arity")
	}
	// Inline array literal: evaluate each element directly into a fresh
	// buffer (the same direct-evaluation trick TypedArray literals use).
	if lit, ok := c.args[0].(*ast.ArrayLiteral); ok && !hasSpreadArg(lit.Elements) {
		n := int64(len(lit.Elements))
		data := e.freshReg()
		e.heapMallocInto(data, allocBytes, fmt.Sprint(n))
		for i, elem := range lit.Elements {
			v, err := e.emitExpr(elem)
			if err != nil {
				return Value{}, err
			}
			v = e.convert(v, TypeU8)
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %d", gep, data, i))
			e.heapStore("i8", v.Ref, gep, 1)
		}
		return e.bufferAggregate(data, fmt.Sprintf("%d", n)), nil
	}
	argTy := e.typeOfExpr(c.args[0])
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
			// Copying a bigint into a byte is ToNumber of a BigInt.
			if _, err := e.emitExpr(c.args[0]); err != nil {
				return Value{}, err
			}
			e.emitThrowTypeError("Cannot mix BigInt and other types, use explicit conversions")
			e.emitLabel(e.freshLabel("buffrom.dead"))
			return e.bufferAggregate("null", "0"), nil
		}
		ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(c.args[0], c.pos)
		if err != nil {
			return Value{}, err
		}
		data := e.freshReg()
		e.heapMallocInto(data, allocBytes, lenReg)
		if err := e.emitBufferCopyConvertLoop(ptrReg, lenReg, elemTy, data); err != nil {
			return Value{}, err
		}
		return e.bufferAggregate(data, lenReg), nil
	default:
		v, err := e.emitExpr(c.args[0])
		if err != nil {
			return Value{}, err
		}
		if !isPlainStringTy(v.Ty) || v.Ty.IsArrayBuffer() {
			// Any other value (an array-like object, a number): the
			// dynamic path's reading, or its TypeError.
			box, err := e.emitBoxValue(v)
			if err != nil {
				return Value{}, err
			}
			return e.emitBufferFromDynamic(box, c.args, c.pos)
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
		return Value{}, internalAt(c.pos, "arity: Buffer.%s takes (size%s)", c.method, map[string]string{"alloc": ", fill?, encoding?", "allocUnsafe": ""}[c.method])
	}
	size := c.args[0]
	if check, ok := e.globalLinks["__kml_buffer_size"]; ok && !bufferSizeLiteral(size) {
		// Node's size check: a number from 0 to 2^53-1.
		size = ast.NewCallExpression(ast.NewIdentifier(check, c.pos), []ast.Expression{size}, c.pos)
		e.lowered(size)
	}
	nVal, err := e.emitExpr(size)
	if err != nil {
		return Value{}, err
	}
	nRef := e.convert(nVal, TypeI64).Ref
	data := e.freshReg()
	if c.method == "allocUnsafe" {
		e.heapMallocInto(data, allocBytes, nRef)
	} else {
		e.heapCallocInto(data, allocBytes, nRef, "1")
		viaFill := len(c.args) == 3
		if len(c.args) == 2 {
			fillTy := e.typeOfExpr(c.args[1])
			viaFill = !isNumberTy(fillTy) && !isPlainStringTy(fillTy) || fillTy.Nullable() || fillTy.IsDynamic() || nullishLiteralType(fillTy)
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
				e.emitInstr(fmt.Sprintf("call "+irPtr+" @memset("+irPtr+" %s, i32 %s, i64 %s)", data, wide, nRef))
			case isPlainStringTy(fv.Ty):
				// String fill (ADR-00576): repeat the fill string's UTF-8 bytes
				// across the whole buffer, matching Node's Buffer.alloc(n, str).
				fv = e.convert(fv, TypePtr)
				e.ensureStrHeaderRuntime()
				needleLen := e.emitStrLenHeader(fv.Ref)
				e.emitBufferRepeatFill(data, fv.Ref, needleLen, "0", nRef)
			default:
				return Value{}, internalAt(c.pos, "Buffer.alloc fill of type %s (routed through _fill above)", fv.Ty.IR)
			}
		}
	}
	return e.bufferAggregate(data, nRef), nil
}

// bufferStaticConcat is concat().
func (e *Emitter) bufferStaticConcat(c *bufferStaticCall) (Value, error) {
	if len(c.args) < 1 || len(c.args) > 2 {
		return Value{}, internalAt(c.pos, "arity: Buffer.concat takes (list, totalLength?)")
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
		pTy := e.typeOfExpr(elem)
		byteView := pTy.IsTypedArray() && !pTy.BigIntElem && pTy.ElemType != nil && pTy.ElemType.Align() == 1
		if pTy.IsDynamic() || !byteView {
			// A box (an any-held binding): a Uint8Array (a Buffer is one)
			// is read as itself, anything else throws Node's TypeError.
			v, err := e.emitExpr(elem)
			if err != nil {
				return Value{}, err
			}
			if v, err = e.emitBoxValue(v); err != nil {
				return Value{}, err
			}
			u8Kind, _ := arrayElemKind(TypeU8)
			fits := e.emitBoxIsArrayOf(v, []int{anyArrayTyped, anyArrayBuffer}, []int{u8Kind})
			throwL, okL := e.freshLabel("bufcat.throw"), e.freshLabel("bufcat.ok")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", fits, okL, throwL))
			e.emitLabel(throwL)
			e.ensureDynJSONC()
			suffix := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_received(i64 %s)", suffix, v.Ref))
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
	data := e.freshReg()
	e.heapCallocInto(data, allocBytes, total, "1")
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
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %s", dst, data, offset))
		e.emitInstr(fmt.Sprintf("call "+irPtr+" @memcpy("+irPtr+" %s, "+irPtr+" %s, i64 %s)", dst, p.ptrRef, cnt2))
		next := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", next, offset, cnt2))
		offset = next
	}
	return e.bufferAggregate(data, total), nil
}

// bufferStaticCompare is compare().
func (e *Emitter) bufferStaticCompare(c *bufferStaticCall) (Value, error) {
	if len(c.args) != 2 {
		return Value{}, internalAt(c.pos, "arity: Buffer.compare takes (a, b)")
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
		return Value{}, internalAt(c.pos, "arity: Buffer.isBuffer takes 1 argument")
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
		return Value{}, internalAt(c.pos, "arity: Buffer.byteLength takes (string, encoding?)")
	}
	v, err := e.emitExpr(c.args[0])
	if err != nil {
		return Value{}, err
	}
	if v.Ty.IsTypedArray() || v.Ty.IsArrayBuffer() || v.Ty.IsBuffer() || !v.Ty.IsDynamic() && !isPlainStringTy(v.Ty) && hasHandleFlag(v.Ty) {
		// A view's or an ArrayBuffer's: its byteLength.
		bl, err := e.emitExpr(ast.NewMemberExpression(e.bindValue(v, c.pos), "byteLength", c.pos))
		if err != nil {
			return Value{}, err
		}
		return e.convert(bl, TypeI64), nil
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
	l := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_buf_byte_length("+irPtr+" %s, "+irPtr+" %s)", l, v.Ref, encRef))
	return Value{Ref: l, Ty: TypeI64}, nil
}

// bufferStaticIsEncoding is isEncoding().
func (e *Emitter) bufferStaticIsEncoding(c *bufferStaticCall) (Value, error) {
	// Buffer.isEncoding(x): a non-empty string naming an encoding Node
	// knows, case-insensitively; anything else is false.
	if len(c.args) != 1 {
		return Value{}, internalAt(c.pos, "arity: Buffer.isEncoding takes 1 argument")
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
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" null", sel, isStr, p))
		str = sel
	case isStringTy(v.Ty):
		str = v.Ref
	default:
		return Value{Ref: "false", Ty: TypeBool}, nil
	}
	r, b := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_buf_is_encoding("+irPtr+" %s)", r, str))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", b, r))
	return Value{Ref: b, Ty: TypeBool}, nil
}

// emitBufferCopyConvertLoop copies lenReg elements from a source array
// (any numeric elemTy) into dstData as u8, with the standard wrap coercion.
func (e *Emitter) emitBufferCopyConvertLoop(srcPtr, lenReg string, srcElemTy Type, dstData string) error {
	idx := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idx))
	e.emitInstr(fmt.Sprintf("store i64 0, "+irPtr+" %s, align 8", idx))
	condL := e.freshLabel("buf.copy.cond")
	bodyL := e.freshLabel("buf.copy.body")
	doneL := e.freshLabel("buf.copy.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", i, idx))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, i, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))
	e.emitLabel(bodyL)
	sGep := e.freshReg()
	sVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %s", sGep, srcElemTy.IR, srcPtr, i))
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", sVal, srcElemTy.IR, sGep, srcElemTy.Align()))
	c := e.convert(Value{Ref: sVal, Ty: srcElemTy}, TypeU8)
	dGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %s", dGep, dstData, i))
	e.heapStore("i8", c.Ref, dGep, 1)
	nx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", nx, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", nx, idx))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
	return nil
}

// emitBufferCompareCore emits the Node comparison: memcmp over the common
// prefix, then the shorter buffer sorts first. Returns -1/0/1 as i64.
func (e *Emitter) emitBufferCompareCore(aPtr, aLen, bPtr, bLen string) Value {
	aSmall := e.freshReg()
	minLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", aSmall, aLen, bLen))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", minLen, aSmall, aLen, bLen))
	mc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @memcmp("+irPtr+" %s, "+irPtr+" %s, i64 %s)", mc, aPtr, bPtr, minLen))
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
			return Value{}, internalAt(pos, "a Buffer write accessor takes (value, offset?)")
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
	e.ensureStrHeaderRuntime()
	recv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_received_int(i64 %s)", recv, offRef))
	if msg, err = e.emitStringConcat(msg, Value{Ref: recv, Ty: TypePtr}); err != nil {
		return Value{}, err
	}
	e.emitThrowCoded("RangeError", "ERR_OUT_OF_RANGE", msg.Ref)
	e.emitLabel(okL)
	elemPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %s", elemPtr, ptrReg, offRef))

	bits := k.width * 8
	littleRef := "false"
	if k.little {
		littleRef = "true"
	}
	if !k.write {
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i%d, "+irPtr+" %s, align 1", raw, bits, elemPtr))
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
			e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" %s(i64 %s)", h, wrap, val))
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
			// Writing a non-bigint is ToBigInt's TypeError (Node mixes it
			// into its range check: "Cannot mix BigInt and other types").
			e.emitThrowTypeError("Cannot mix BigInt and other types, use explicit conversions")
			e.emitLabel(e.freshLabel("bufbig.dead"))
			rawVal = Value{Ref: "null", Ty: rawVal.Ty}
			rawVal.Ty = BigIntType()
		}
		e.ensureBigInt()
		// Outside the 64-bit range: Node's ERR_OUT_OF_RANGE, before the
		// write.
		signed := 0
		if k.signed {
			signed = 1
		}
		cmsg, out := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_bigint_range_msg("+irPtr+" %s, i32 %d)", cmsg, rawVal.Ref, signed))
		e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", out, cmsg))
		badL, okL := e.freshLabel("bufbig.range"), e.freshLabel("bufbig.ok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", out, badL, okL))
		e.emitLabel(badL)
		msg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_str_from_cstr("+irPtr+" %s)", msg, cmsg))
		e.emitThrowCoded("RangeError", "ERR_OUT_OF_RANGE", msg)
		e.emitLabel(okL)
		unwrap := "@__kml_bigint_to_i64"
		if !k.signed {
			unwrap = "@__kml_bigint_to_u64"
		}
		narrow = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 %s("+irPtr+" %s)", narrow, unwrap, rawVal.Ref))
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

// emitBufferStringSearchAt is buf.indexOf/includes/lastIndexOf(string,
// byteOffset, encoding?) as Node's bidirectionalIndexOf: a string second
// argument is the encoding (the offset then the default), the needle is
// encoded as given, and __kml_buf_index_of searches from the offset.
func (e *Emitter) emitBufferStringSearchAt(mem *ast.MemberExpression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	bufPtr, bufLen, _, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	needleVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	needleVal = e.convert(needleVal, TypePtr)
	offset := "0x7FF8000000000000" // NaN: the default
	encIdx := 2
	if isPlainStringTy(e.typeOfExpr(args[1])) {
		encIdx = 1 // (byteOffset, the encoding)
	} else {
		ov, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		offset = e.convert(ov, TypeF64).Ref
	}
	enc, encRef, err := e.bufferEncoding(args, encIdx, pos)
	if err != nil {
		return Value{}, err
	}
	var np, nl string
	if encRef != "" {
		np, nl = e.emitBufferDecodeStringDyn(needleVal.Ref, encRef)
	} else {
		np, nl = e.emitBufferDecodeString(needleVal.Ref, enc)
	}
	dir := 1
	if method == "lastIndexOf" {
		dir = 0
	}
	e.declareFn("__kml_buf_index_of", "declare i64 @__kml_buf_index_of("+irPtr+", i64, "+irPtr+", i64, double, i32)")
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_buf_index_of("+irPtr+" %s, i64 %s, "+irPtr+" %s, i64 %s, double %s, i32 %d)", r, bufPtr, bufLen, np, nl, offset, dir))
	if method == "includes" {
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, -1", b, r))
		return Value{Ref: b, Ty: TypeBool}, nil
	}
	return Value{Ref: r, Ty: TypeI64}, nil
}

// emitBufferStringSearch implements the string-argument forms of
// buf.indexOf/includes/lastIndexOf (TDD-00103 follow-up): the needle string's
// UTF-8 bytes are searched over the buffer's raw bytes. `kind` is "indexOf",
// "includes", or "lastIndexOf". Only the single-argument form is supported (no
// byteOffset/encoding). An empty needle matches Node: indexOf → 0,
// lastIndexOf → buffer length, includes → true.
func (e *Emitter) emitBufferStringSearch(mem *ast.MemberExpression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return e.emitBufferStringSearchAt(mem, method, args, pos)
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
		resAlloca := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resAlloca))
		e.emitInstr(fmt.Sprintf("store i64 -1, "+irPtr+" %s, align 8", resAlloca))
		// Empty needle → buffer length (Node).
		isEmpty := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isEmpty, needleLen))
		emptyL := e.freshLabel("buflast.empty")
		scanL := e.freshLabel("buflast.scan")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isEmpty, emptyL, scanL))
		e.emitLabel(emptyL)
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", bufLen, resAlloca))
		doneL := e.freshLabel("buflast.done")
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(scanL)
		// start = bufLen - needleLen; if < 0 the loop never runs.
		start := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", start, bufLen, needleLen))
		idxAlloca := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", start, idxAlloca))
		condL := e.freshLabel("buflast.cond")
		bodyL := e.freshLabel("buflast.body")
		matchL := e.freshLabel("buflast.match")
		nextL := e.freshLabel("buflast.next")
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(condL)
		i := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", i, idxAlloca))
		neg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, i))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", neg, doneL, bodyL))
		e.emitLabel(bodyL)
		hayAt := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %s", hayAt, bufPtr, i))
		cmp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @memcmp("+irPtr+" %s, "+irPtr+" %s, i64 %s)", cmp, hayAt, needleVal.Ref, needleLen))
		iseq := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", iseq, cmp))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", iseq, matchL, nextL))
		e.emitLabel(matchL)
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", i, resAlloca))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(nextL)
		iDec := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", iDec, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", iDec, idxAlloca))
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(doneL)
		res := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", res, resAlloca))
		return e.countToNumber(Value{Ref: res, Ty: TypeI64}), nil
	}

	// Forward search (indexOf/includes) via memmem.
	found := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @memmem("+irPtr+" %s, i64 %s, "+irPtr+" %s, i64 %s)", found, bufPtr, bufLen, needleVal.Ref, needleLen))
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, null", isNull, found))
	foundInt := e.freshReg()
	bufInt := e.freshReg()
	diff := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint "+irPtr+" %s to i64", foundInt, found))
	e.emitInstr(fmt.Sprintf("%s = ptrtoint "+irPtr+" %s to i64", bufInt, bufPtr))
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
	// `buf.fill()` fills with 0, as Node's does; arguments past encoding are
	// evaluated and ignored.
	if len(args) == 0 {
		args = []ast.Expression{ast.NewNumberLiteral("0", pos)}
	}
	if len(args) > 4 {
		var err error
		if args, err = e.dropArgsPast(args, 4, pos); err != nil {
			return Value{}, err
		}
	}
	recvTy := e.typeOfExpr(mem.Object)
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
	e.ensureDynJSONC()
	argv := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca [4 x i64], align 8", argv))
	for i, b := range boxes {
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr [4 x i64], "+irPtr+" %s, i64 0, i64 %d", slot, argv, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", b, slot))
	}
	msgSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", msgSlot))
	code := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_buf_fill_any("+irPtr+" %s, i64 %s, i64 %d, "+irPtr+" %s, "+irPtr+" %s)", code, data, length, len(boxes), argv, msgSlot))
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
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", msg, msgSlot))
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
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", startN, idxAlloca))
	condL := e.freshLabel("buffill.cond")
	bodyL := e.freshLabel("buffill.body")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", i, idxAlloca))
	atEnd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, %s", atEnd, i, endN))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", atEnd, doneL, bodyL))
	e.emitLabel(bodyL)
	rel := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", rel, i, startN))
	cyc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = urem i64 %s, %s", cyc, rel, needleLen))
	srcGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %s", srcGep, needlePtr, cyc))
	byteVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, "+irPtr+" %s, align 1", byteVal, srcGep))
	dstGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %s", dstGep, bufPtr, i))
	e.heapStore("i8", byteVal, dstGep, 1)
	iNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", iNext, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", iNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
}

// emitBufferInstanceCall dispatches buffer.<method>(...) for the names the
// IsBuffer flag adds beyond the shared TypedArray/array machinery.
func (e *Emitter) emitBufferInstanceCall(mem *ast.MemberExpression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if k, ok := bufferAccessorKindFor(method); ok {
		return e.emitBufferAccessor(mem, k, args, pos)
	}
	if a, ok := bufferInstanceArity[method]; ok {
		fitted, v, thrown, err := e.applyBufferArity(a, args, ast.NewCallExpression(mem, args, pos), pos)
		if err != nil || thrown {
			return v, err
		}
		args = fitted
	}
	c := &bufferInstanceCall{mem: mem, method: method, args: args, pos: pos}
	if f, ok := bufferInstanceMethods[method]; ok {
		return f(e, c)
	}
	return Value{}, internalAt(pos, "buffer.%s", method)
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
		return Value{}, internalAt(c.pos, "arity: Buffer.toString takes (encoding?, start?, end?)")
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
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %s", base, ptrReg, startN))
	if encRef != "" {
		return e.emitBufferEncodeStringDyn(base, n, encRef), nil
	}
	return e.emitBufferEncodeString(base, n, enc), nil
}

// bufferInstanceWrite is write().
func (e *Emitter) bufferInstanceWrite(c *bufferInstanceCall) (Value, error) {
	// write(string, offset?, length?, encoding?): an encoding may stand in
	// any position after the string.
	if len(c.args) < 1 || len(c.args) > 4 {
		return Value{}, internalAt(c.pos, "buffer.write arity %d", len(c.args))
	}
	sv, err := e.emitExpr(c.args[0])
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(sv.Ty) || sv.Ty.IsDynamic() {
		// Node's check of its first argument, after the others.
		for _, a := range c.args[1:] {
			if _, err := e.emitExpr(a); err != nil {
				return Value{}, err
			}
		}
		e.emitThrowCoded("TypeError", "ERR_INVALID_ARG_TYPE", e.internString("argument must be a string"))
		e.emitLabel(e.freshLabel("bufwrite.dead"))
		return Value{Ref: "0", Ty: TypeI64}, nil
	}
	isEnc := func(i int) bool {
		if i >= len(c.args) {
			return false
		}
		_, lit := c.args[i].(*ast.StringLiteral)
		return lit || isStringTy(e.typeOfExpr(c.args[i])) && !e.typeOfExpr(c.args[i]).IsDynamic()
	}
	offRef, limRef := "0", ""
	encIdx := 1
	if len(c.args) >= 2 && !isEnc(1) {
		ov, err := e.emitExpr(c.args[1])
		if err != nil {
			return Value{}, err
		}
		offRef = e.convert(ov, TypeI64).Ref
		encIdx = 2
		if len(c.args) >= 3 && !isEnc(2) {
			lv, err := e.emitExpr(c.args[2])
			if err != nil {
				return Value{}, err
			}
			limRef = e.convert(lv, TypeI64).Ref
			encIdx = 3
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
	if limRef != "" {
		// At most length bytes.
		lo, capped := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", lo, limRef, cnt))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", capped, lo, limRef, cnt))
		cnt = capped
	}
	dst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %s", dst, dstPtr, offRef))
	e.emitInstr(fmt.Sprintf("call "+irPtr+" @memcpy("+irPtr+" %s, "+irPtr+" %s, i64 %s)", dst, srcPtr, cnt))
	return Value{Ref: cnt, Ty: TypeI64}, nil
}

// bufferInstanceCopy is copy().
func (e *Emitter) bufferInstanceCopy(c *bufferInstanceCall) (Value, error) {
	if len(c.args) < 1 || len(c.args) > 4 {
		return Value{}, internalAt(c.pos, "arity: Buffer.copy takes (target, targetStart?, sourceStart?, sourceEnd?)")
	}
	srcPtr, srcLen, _, err := e.resolveArrayForHOF(c.mem.Object, c.pos)
	if err != nil {
		return Value{}, err
	}
	tgtTy := e.typeOfExpr(c.args[0])
	if !(tgtTy.IsTypedArray() || tgtTy.IsBuffer()) || tgtTy.IsDynamic() || tgtTy.ElemType == nil {
		// Not a TypedArray: Node's ERR_INVALID_ARG_TYPE, once evaluated.
		tv, err := e.emitExpr(c.args[0])
		if err != nil {
			return Value{}, err
		}
		box, err := e.emitBoxValue(tv)
		if err != nil {
			return Value{}, err
		}
		e.ensureDynJSONC()
		suffix := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_received(i64 %s)", suffix, box.Ref))
		msg, err := e.emitStringConcat(Value{Ref: e.internString(`The "target" argument must be an instance of Buffer or Uint8Array.`), Ty: TypePtr}, Value{Ref: suffix, Ty: TypePtr})
		if err != nil {
			return Value{}, err
		}
		e.emitThrowCoded("TypeError", "ERR_INVALID_ARG_TYPE", msg.Ref)
		e.emitLabel(e.freshLabel("bufcopy.dead"))
		return Value{Ref: "0", Ty: TypeI64}, nil
	}
	tgtPtr, tgtLen, _, err := e.resolveArrayForHOF(c.args[0], c.pos)
	if err != nil {
		return Value{}, err
	}
	if w := tgtTy.ElemType.Align(); w > 1 {
		// A wider view's bytes: its length in bytes.
		bl := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bl, tgtLen, w))
		tgtLen = bl
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
	s := e.freshReg()
	d := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %s", s, srcPtr, sStart))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %s", d, tgtPtr, tStart))
	e.emitInstr(fmt.Sprintf("call "+irPtr+" @memcpy("+irPtr+" %s, "+irPtr+" %s, i64 %s)", d, s, cnt2))
	return Value{Ref: cnt2, Ty: TypeI64}, nil
}

// bufferInstanceEquals is equals/compare().
func (e *Emitter) bufferInstanceEquals(c *bufferInstanceCall) (Value, error) {
	args := c.args
	if c.method == "equals" && len(args) > 1 {
		kept, err := e.dropArgsPast(args, 1, c.pos)
		if err != nil {
			return Value{}, err
		}
		args = kept
	}
	if len(args) < 1 {
		return Value{}, internalAt(c.pos, "Buffer.%s arity", c.method)
	}
	if len(args) > 5 {
		kept, err := e.dropArgsPast(args, 5, c.pos)
		if err != nil {
			return Value{}, err
		}
		args = kept
	}
	aPtr, aLen, _, err := e.resolveArrayForHOF(c.mem.Object, c.pos)
	if err != nil {
		return Value{}, err
	}
	bPtr, bLen, _, err := e.resolveArrayForHOF(args[0], c.pos)
	if err != nil {
		return Value{}, err
	}
	if len(args) > 1 {
		// compare(target, targetStart, targetEnd, sourceStart, sourceEnd):
		// each in [0, its buffer's length] (validateOffset), then the
		// ranges compared.
		names := []string{"targetStart", "targetEnd", "sourceStart", "sourceEnd"}
		// (a start is bounded by kMaxLength alone, 2^53 - 1)
		bounds := []string{"9007199254740991", bLen, "9007199254740991", aLen}
		defs := []string{"0", bLen, "0", aLen}
		vals := make([]string, 4)
		for i := range names {
			vals[i] = defs[i]
			if len(args) <= i+1 {
				continue
			}
			if nl, ok := args[i+1].(*ast.NullLiteral); ok && nl.IsUndefined {
				continue
			}
			v, err := e.emitExpr(args[i+1])
			if err != nil {
				return Value{}, err
			}
			iv := e.convert(v, TypeI64).Ref
			if err := e.emitBufferOffsetCheck(names[i], iv, bounds[i]); err != nil {
				return Value{}, err
			}
			vals[i] = iv
		}
		slice := func(ptr, start, end string) (string, string) {
			p, n, neg, n0 := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %s", p, ptr, start))
			e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", n, end, start))
			e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, n))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", n0, neg, n))
			return p, n0
		}
		bPtr, bLen = slice(bPtr, vals[0], vals[1])
		aPtr, aLen = slice(aPtr, vals[2], vals[3])
	}
	cmp := e.emitBufferCompareCore(aPtr, aLen, bPtr, bLen)
	if c.method == "compare" {
		return cmp, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", r, cmp.Ref))
	return Value{Ref: r, Ty: TypeBool}, nil
}

// emitBufferOffsetCheck throws Node's ERR_OUT_OF_RANGE (validateOffset's
// wording) unless the offset v (named name) is in [0, max].
func (e *Emitter) emitBufferOffsetCheck(name, v, max string) error {
	lo, hi, bad := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", lo, v))
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", hi, v, max))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", bad, lo, hi))
	badL, okL := e.freshLabel("bufoff.bad"), e.freshLabel("bufoff.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
	e.emitLabel(badL)
	maxStr, err := e.emitValueToString(Value{Ref: max, Ty: TypeI64})
	if err != nil {
		return err
	}
	msg, err := e.emitStringConcat(Value{Ref: e.internString(`The value of "` + name + `" is out of range. It must be >= 0 && <= `), Ty: TypePtr}, maxStr)
	if err != nil {
		return err
	}
	if msg, err = e.emitStringConcat(msg, Value{Ref: e.internString(". Received "), Ty: TypePtr}); err != nil {
		return err
	}
	e.ensureStrHeaderRuntime()
	recv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_received_int(i64 %s)", recv, v))
	if msg, err = e.emitStringConcat(msg, Value{Ref: recv, Ty: TypePtr}); err != nil {
		return err
	}
	e.emitThrowCoded("RangeError", "ERR_OUT_OF_RANGE", msg.Ref)
	e.emitLabel(okL)
	return nil
}

// emitBufferConcatList is Buffer.concat(list, totalLength?) over a runtime
// array of Buffers or byte TypedArrays (an inline literal is joined in IR
// instead).
func (e *Emitter) emitBufferConcatList(args []ast.Expression, pos ast.Pos) (Value, error) {
	lt := e.typeOfExpr(args[0])
	var ptrReg, lenReg string
	if !lt.IsArray() || lt.ElemType == nil || !lt.ElemType.IsTypedArray() || lt.ElemType.BigIntElem || lt.ElemType.ElemType == nil || lt.ElemType.ElemType.Align() != 1 {
		// Any other list: checked at run time, as Node does.
		var err error
		if ptrReg, lenReg, err = e.emitBufferConcatCheckedList(args[0]); err != nil {
			return Value{}, err
		}
	} else {
		var err error
		if ptrReg, lenReg, _, err = e.resolveArrayForHOF(args[0], pos); err != nil {
			return Value{}, err
		}
	}
	total := "-1"
	if len(args) == 2 {
		tv, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		total = e.convert(tv, TypeI64).Ref
	}
	outLen := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", outLen))
	data := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_buf_concat("+irPtr+" %s, i64 %s, i64 %s, "+irPtr+" %s)", data, ptrReg, lenReg, total, outLen))
	n := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", n, outLen))
	return e.bufferAggregate(data, n), nil
}

// emitBufferConcatCheckedList is the (slots, count) __kml_buf_concat
// takes for a Buffer.concat list of no static Buffer[] type: an array
// whose every element is a Uint8Array (a Buffer is one), each header in a
// fresh slot; anything else throws Node's ERR_INVALID_ARG_TYPE.
func (e *Emitter) emitBufferConcatCheckedList(list ast.Expression) (string, string, error) {
	lv, err := e.emitExpr(list)
	if err != nil {
		return "", "", err
	}
	box, err := e.emitBoxValue(lv)
	if err != nil {
		return "", "", err
	}
	e.ensureDynJSONC()
	pos := list.GetPos()
	isArr, err := e.emitExpr(ast.NewCallExpression(ast.NewMemberExpression(e.builtinIdent("Array", pos), "isArray", pos), []ast.Expression{e.bindValue(box, pos)}, pos))
	if err != nil {
		return "", "", err
	}
	arrL, badL := e.freshLabel("bufcat.list"), e.freshLabel("bufcat.notlist")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.toBool(isArr).Ref, arrL, badL))
	e.emitLabel(badL)
	throwArgType := func(prefix Value, v Value) error {
		suffix := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_received(i64 %s)", suffix, v.Ref))
		msg, err := e.emitStringConcat(prefix, Value{Ref: suffix, Ty: TypePtr})
		if err != nil {
			return err
		}
		e.emitThrowCoded("TypeError", "ERR_INVALID_ARG_TYPE", msg.Ref)
		return nil
	}
	if err := throwArgType(Value{Ref: e.internString(`The "list" argument must be an instance of Array.`), Ty: TypePtr}, box); err != nil {
		return "", "", err
	}
	e.emitLabel(arrL)
	elems, n := e.emitAnyArrayElems(box, nil)
	slots := e.freshReg()
	bytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", bytes, n))
	e.heapMallocInto(slots, allocBytes, bytes)
	iSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", iSlot))
	e.emitInstr(fmt.Sprintf("store i64 0, "+irPtr+" %s, align 8", iSlot))
	headL, bodyL, doneL := e.freshLabel("bufcat.head"), e.freshLabel("bufcat.body"), e.freshLabel("bufcat.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", headL))
	e.emitLabel(headL)
	i, more := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", i, iSlot))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, i, n))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, doneL))
	e.emitLabel(bodyL)
	gep, el := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, "+irPtr+" %s, i64 %s", gep, elems, i))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", el, gep))
	ev := Value{Ref: el, Ty: TypeAny}
	u8Kind, _ := arrayElemKind(TypeU8)
	fits := e.emitBoxIsArrayOf(ev, []int{anyArrayTyped, anyArrayBuffer}, []int{u8Kind})
	okL, throwL := e.freshLabel("bufcat.ok"), e.freshLabel("bufcat.throw")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", fits, okL, throwL))
	e.emitLabel(throwL)
	idx, err := e.emitValueToString(Value{Ref: i, Ty: TypeI64})
	if err != nil {
		return "", "", err
	}
	head, err := e.emitStringConcat(Value{Ref: e.internString(`The "list[`), Ty: TypePtr}, idx)
	if err != nil {
		return "", "", err
	}
	head, err = e.emitStringConcat(head, Value{Ref: e.internString(`]" argument must be an instance of Buffer or Uint8Array.`), Ty: TypePtr})
	if err != nil {
		return "", "", err
	}
	if err := throwArgType(head, ev); err != nil {
		return "", "", err
	}
	e.emitLabel(okL)
	buf := e.convert(ev, BufferType())
	sgep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr "+irPtr+", "+irPtr+" %s, i64 %s", sgep, slots, i))
	e.heapStore(irPtr, e.arrayReturnHeader(buf), sgep, 8)
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", next, iSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", headL))
	e.emitLabel(doneL)
	return slots, n, nil
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
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", dataSlot))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenSlot))
	doneL := e.freshLabel("buffrom.done")
	put := func(data, n string) {
		e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", data, dataSlot))
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", n, lenSlot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	// A string.
	strL, notStrL := e.emitTagCheck(tag, kmlTagString, "buffrom.str")
	e.emitLabel(strL)
	s := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, s, payload))
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
	// Not an object, or one with no length (Node's fromObject): its
	// TypeError.
	isObj, err := e.emitIsObjectValue(v)
	if err != nil {
		return Value{}, err
	}
	objL, primL := e.freshLabel("buffrom.obj"), e.freshLabel("buffrom.prim")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, primL))
	e.emitLabel(primL)
	e.emitThrowBufferFromArgType(v)
	e.emitLabel(objL)
	length, err := e.emitDynAnyMemberGetNamed(v, e.internString("length"), "length", pos)
	if err != nil {
		return Value{}, err
	}
	ltag, _ := e.emitUnboxTagPayload(length)
	noLenL, lenL := e.emitTagCheck(ltag, kmlTagUndefined, "buffrom.nolen")
	e.emitLabel(noLenL)
	e.emitThrowBufferFromArgType(v)
	e.emitLabel(lenL)
	// Anything array-like: each element to a byte.
	e.ensureDynJSONC()
	lp := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lp))
	f64s := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_any_arraylike_f64(i64 %s, "+irPtr+" %s)", f64s, v.Ref, lp))
	cnt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", cnt, lp))
	bad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", bad, cnt))
	badL, okL := e.freshLabel("buffrom.bad"), e.freshLabel("buffrom.arr")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
	e.emitLabel(badL)
	e.emitThrowBufferFromArgType(v)
	e.emitLabel(okL)
	out := e.freshReg()
	e.heapMallocInto(out, allocBytes, cnt)
	if err := e.emitBufferCopyConvertLoop(f64s, cnt, TypeF64, out); err != nil {
		return Value{}, err
	}
	put(out, cnt)
	e.emitLabel(doneL)
	d, l := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", d, dataSlot))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", l, lenSlot))
	return e.bufferAggregate(d, l), nil
}

// emitIsObjectValue is an i1: whether the box v holds an object or a
// function (`typeof v === "object" && v !== null || typeof v === "function"`).
func (e *Emitter) emitIsObjectValue(v Value) (string, error) {
	pos := ast.Pos{}
	id := e.bindValue(v, pos)
	ty := func() ast.Expression { return ast.NewUnaryExpression("typeof", true, id, pos) }
	str := func(s string) ast.Expression { return ast.NewStringLiteral(s, pos) }
	test := ast.NewBinaryExpression("||",
		ast.NewBinaryExpression("&&", ast.NewBinaryExpression("===", ty(), str("object"), pos), ast.NewBinaryExpression("!==", id, ast.NewNullLiteral(false, pos), pos), pos),
		ast.NewBinaryExpression("===", ty(), str("function"), pos), pos)
	r, err := e.emitExpr(test)
	if err != nil {
		return "", err
	}
	return e.toBool(r).Ref, nil
}

// emitThrowBufferFromArgType throws Node's ERR_INVALID_ARG_TYPE for a
// Buffer.from argument it cannot read, the value v described.
func (e *Emitter) emitThrowBufferFromArgType(v Value) {
	e.ensureDynJSONC()
	suffix := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_received(i64 %s)", suffix, v.Ref))
	msg, err := e.emitStringConcat(Value{Ref: e.internString("The first argument must be of type string or an instance of Buffer, ArrayBuffer, or Array or an Array-like Object."), Ty: TypePtr}, Value{Ref: suffix, Ty: TypePtr})
	if err != nil {
		e.emitThrowTypeError("The first argument must be of type string or an instance of Buffer, ArrayBuffer, or Array or an Array-like Object.")
		return
	}
	e.emitThrowCoded("TypeError", "ERR_INVALID_ARG_TYPE", msg.Ref)
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
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %s", src, data, off))
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
		}
	case 4:
		if !e.usedBswap32 {
			e.usedBswap32 = true
		}
	case 8:
		if !e.usedBswap64 {
			e.usedBswap64 = true
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

// bufferCtorCall is the global Buffer called or constructed (`Buffer(n)`,
// `new Buffer(s)`): lib/node/kml_buffer_ctor.ts's deprecated form. False
// when the program declares its own Buffer or the helper is not linked.
func (e *Emitter) bufferCtorCall(args []ast.Expression, pos ast.Pos) (ast.Expression, bool) {
	m, ok := e.globalLinks["__kml_Buffer_ctor"]
	if !ok {
		return nil, false
	}
	if _, user := e.classes["Buffer"]; user {
		return nil, false
	}
	for _, a := range args {
		if _, spread := a.(*ast.SpreadElement); spread {
			return nil, false
		}
	}
	list := ast.NewArrayLiteral(args, pos)
	e.lowered(list)
	return ast.NewCallExpression(ast.NewIdentifier(m, pos), []ast.Expression{list}, pos), true
}

// bufferCallee reports whether a new's target is Buffer itself, written
// plainly or through `as` (`new (Buffer as any)(…)`).
func bufferCallee(className string, callee ast.Expression) bool {
	if callee == nil {
		return className == "Buffer"
	}
	for {
		switch c := callee.(type) {
		case *ast.AsExpression:
			callee = c.Expr
			continue
		case *ast.Identifier:
			return c.Name == "Buffer"
		}
		return false
	}
}

// bufferSizeLiteral reports a Buffer.alloc size written as a literal the
// size check passes: a number from 0 to 2^53-1.
func bufferSizeLiteral(x ast.Expression) bool {
	n, ok := x.(*ast.NumberLiteral)
	if !ok {
		return false
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(n.Value, "_", ""), 64)
	return err == nil && v >= 0 && v <= 9007199254740991
}
