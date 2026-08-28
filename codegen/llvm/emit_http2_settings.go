// emit_http2_settings.go — TDD-00139 Stage 4: `http2.constants` and the
// settings helpers (getDefaultSettings / getPackedSettings /
// getUnpackedSettings).
//
// `http2.constants` is a compile-time namespace: a member read resolves to a
// literal (the HTTP2_HEADER_* strings, the NGHTTP2_* codes) with no runtime
// object. A binding (`const c = http2.constants`) carries the IsH2Constants
// flag so reads through the alias resolve identically.
//
// The settings helpers follow nghttp2's wire format: 6-byte entries,
// identifier (2, big-endian) + value (4, big-endian), packed in identifier
// order 1..6, 8 — the order Node's own packing emits.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// h2Constant is one http2.constants member: exactly one of str/num is live.
type h2Constant struct {
	str   string
	num   float64
	isStr bool
}

func h2s(s string) h2Constant  { return h2Constant{str: s, isStr: true} }
func h2n(n float64) h2Constant { return h2Constant{num: n} }

var h2Constants = map[string]h2Constant{
	// Pseudo- and common header names.
	"HTTP2_HEADER_STATUS": h2s(":status"), "HTTP2_HEADER_METHOD": h2s(":method"),
	"HTTP2_HEADER_AUTHORITY": h2s(":authority"), "HTTP2_HEADER_SCHEME": h2s(":scheme"),
	"HTTP2_HEADER_PATH": h2s(":path"), "HTTP2_HEADER_PROTOCOL": h2s(":protocol"),
	"HTTP2_HEADER_CONTENT_TYPE":   h2s("content-type"),
	"HTTP2_HEADER_CONTENT_LENGTH": h2s("content-length"),
	"HTTP2_HEADER_ACCEPT":         h2s("accept"),
	"HTTP2_HEADER_ACCEPT_ENCODING": h2s("accept-encoding"),
	"HTTP2_HEADER_USER_AGENT":     h2s("user-agent"),
	"HTTP2_HEADER_COOKIE":         h2s("cookie"),
	"HTTP2_HEADER_SET_COOKIE":     h2s("set-cookie"),
	"HTTP2_HEADER_HOST":           h2s("host"),
	"HTTP2_HEADER_LOCATION":       h2s("location"),
	"HTTP2_HEADER_DATE":           h2s("date"),
	// Common method names.
	"HTTP2_METHOD_GET": h2s("GET"), "HTTP2_METHOD_POST": h2s("POST"),
	"HTTP2_METHOD_PUT": h2s("PUT"), "HTTP2_METHOD_DELETE": h2s("DELETE"),
	"HTTP2_METHOD_HEAD": h2s("HEAD"), "HTTP2_METHOD_CONNECT": h2s("CONNECT"),
	// nghttp2 error codes (RFC 7540 §7).
	"NGHTTP2_NO_ERROR": h2n(0), "NGHTTP2_PROTOCOL_ERROR": h2n(1),
	"NGHTTP2_INTERNAL_ERROR": h2n(2), "NGHTTP2_FLOW_CONTROL_ERROR": h2n(3),
	"NGHTTP2_SETTINGS_TIMEOUT": h2n(4), "NGHTTP2_STREAM_CLOSED": h2n(5),
	"NGHTTP2_FRAME_SIZE_ERROR": h2n(6), "NGHTTP2_REFUSED_STREAM": h2n(7),
	"NGHTTP2_CANCEL": h2n(8), "NGHTTP2_COMPRESSION_ERROR": h2n(9),
	"NGHTTP2_CONNECT_ERROR": h2n(10), "NGHTTP2_ENHANCE_YOUR_CALM": h2n(11),
	"NGHTTP2_INADEQUATE_SECURITY": h2n(12), "NGHTTP2_HTTP_1_1_REQUIRED": h2n(13),
	// nghttp2 library error codes the corpus asserts against.
	"NGHTTP2_ERR_STREAM_ID_NOT_AVAILABLE": h2n(-509),
	"NGHTTP2_ERR_INVALID_ARGUMENT":        h2n(-501),
	"NGHTTP2_ERR_STREAM_CLOSED":           h2n(-510),
	// Default-settings values, exposed as constants too.
	"DEFAULT_SETTINGS_HEADER_TABLE_SIZE":   h2n(4096),
	"DEFAULT_SETTINGS_ENABLE_PUSH":         h2n(1),
	"DEFAULT_SETTINGS_INITIAL_WINDOW_SIZE": h2n(65535),
	"DEFAULT_SETTINGS_MAX_FRAME_SIZE":      h2n(16384),
	"MAX_MAX_FRAME_SIZE":                   h2n(16777215),
	"MIN_MAX_FRAME_SIZE":                   h2n(16384),
	"MAX_INITIAL_WINDOW_SIZE":              h2n(2147483647),
}

// isH2ConstantsExpr reports whether expr statically denotes http2.constants —
// the direct member read or a binding carrying the flag.
func (e *Emitter) isH2ConstantsExpr(expr ast.Expression) bool {
	if m, ok := expr.(*ast.MemberExpression); ok && m.Property == "constants" {
		if id, ok := m.Object.(*ast.Identifier); ok && id.Name == "http2__kml_builtin" {
			return true
		}
	}
	return e.inferExprType(expr).IsH2Constants
}

// emitH2Constant resolves one constants member to its literal value.
func (e *Emitter) emitH2Constant(name string, pos ast.Pos) (Value, error) {
	c, ok := h2Constants[name]
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: http2.constants has no member '%s' (the common header/method names, RFC 7540 error codes, and default-settings values are covered)", pos.Line, pos.Col, name)
	}
	if c.isStr {
		return Value{Ref: e.internString(c.str), Ty: TypePtr}, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fadd double 0.0, %s", r, llvmDoubleLit(c.num)))
	return Value{Ref: r, Ty: TypeF64}, nil
}

// h2SettingsOrder is Node's packing order: identifier order 1..6 then 8.
var h2SettingsOrder = []struct {
	name   string
	id     int
	isBool bool
	def    float64
}{
	{"headerTableSize", 1, false, 4096},
	{"enablePush", 2, true, 1},
	{"maxConcurrentStreams", 3, false, 4294967295},
	{"initialWindowSize", 4, false, 65535},
	{"maxFrameSize", 5, false, 16384},
	{"maxHeaderListSize", 6, false, 65535},
	{"enableConnectProtocol", 8, true, 0},
}

// h2SettingsObjectType is the record getDefaultSettings/getUnpackedSettings
// return and getPackedSettings reads.
func h2SettingsObjectType() Type {
	fields := make([]Field, 0, len(h2SettingsOrder))
	for _, s := range h2SettingsOrder {
		ty := TypeF64
		if s.isBool {
			ty = TypeBool
		}
		fields = append(fields, Field{Name: s.name, Ty: ty})
	}
	return ObjectType(fields)
}

// emitH2GetDefaultSettings builds the defaults record.
func (e *Emitter) emitH2GetDefaultSettings(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: http2.getDefaultSettings takes no arguments", pos.Line, pos.Col)
	}
	ty := h2SettingsObjectType()
	e.ensureCalloc()
	obj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", obj, ty.StructSize()))
	for _, s := range h2SettingsOrder {
		idx, _, _ := ty.FieldIndex(s.name)
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, ty.StructIR(), obj, idx))
		if s.isBool {
			b := "false"
			if s.def != 0 {
				b = "true"
			}
			e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", b, g))
		} else {
			e.emitInstr(fmt.Sprintf("store double %s, ptr %s, align 8", llvmDoubleLit(s.def), g))
		}
	}
	return Value{Ref: obj, Ty: ty}, nil
}

// emitH2GetPackedSettings packs a settings object into the 6-byte-entry wire
// Buffer, in Node's identifier order, including only the fields the argument
// actually provides (a literal's own keys, or every known field of a typed
// object value such as getDefaultSettings()'s result).
func (e *Emitter) emitH2GetPackedSettings(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: http2.getPackedSettings takes one settings object", pos.Line, pos.Col)
	}
	// Collect (id, value-as-u32-register) pairs in packing order.
	type entry struct {
		id  int
		reg string // i64 register holding the 32-bit value
	}
	var entries []entry
	toU32 := func(v Value) string {
		if v.Ty.IR == "i1" {
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", r, v.Ref))
			return r
		}
		return e.coerce(v, TypeI64).Ref
	}
	if lit, ok := args[0].(*ast.ObjectLiteral); ok {
		byKey := map[string]ast.Expression{}
		for _, prop := range lit.Properties {
			byKey[prop.Key] = prop.Value
		}
		for _, s := range h2SettingsOrder {
			expr, ok := byKey[s.name]
			if !ok {
				continue
			}
			delete(byKey, s.name)
			v, err := e.emitExpr(expr)
			if err != nil {
				return Value{}, err
			}
			entries = append(entries, entry{s.id, toU32(v)})
		}
		for k := range byKey {
			return Value{}, fmt.Errorf("%d:%d: http2.getPackedSettings: unknown setting '%s'", pos.Line, pos.Col, k)
		}
	} else {
		objVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		if !objVal.Ty.IsObject {
			return Value{}, fmt.Errorf("%d:%d: http2.getPackedSettings takes a settings object", pos.Line, pos.Col)
		}
		for _, s := range h2SettingsOrder {
			idx, fty, ok := objVal.Ty.FieldIndex(s.name)
			if !ok {
				continue
			}
			g := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, objVal.Ty.StructIR(), objVal.Ref, idx))
			v := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", v, fty.IR, g, fty.Align()))
			entries = append(entries, entry{s.id, toU32(Value{Ref: v, Ty: fty})})
		}
	}

	n := len(entries)
	e.ensureMalloc()
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", buf, n*6))
	storeByte := func(off int, val string) {
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", g, buf, off))
		t := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i8", t, val))
		e.emitInstr(fmt.Sprintf("store i8 %s, ptr %s, align 1", t, g))
	}
	for i, en := range entries {
		base := i * 6
		storeByte(base, fmt.Sprintf("%d", (en.id>>8)&0xff))
		storeByte(base+1, fmt.Sprintf("%d", en.id&0xff))
		for b := 0; b < 4; b++ {
			sh := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = lshr i64 %s, %d", sh, en.reg, (3-b)*8))
			storeByte(base+2+b, sh)
		}
	}
	return e.bufferAggregate(buf, fmt.Sprintf("%d", n*6)), nil
}

// emitH2GetUnpackedSettings parses a packed-settings Buffer back into the
// settings record (unknown identifiers skipped, later entries win — nghttp2's
// own semantics). Fields not present keep their zero values (not the
// defaults — matching Node, which only sets what the buffer carries, except
// Node also validates length % 6, which this does too).
func (e *Emitter) emitH2GetUnpackedSettings(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: http2.getUnpackedSettings takes one Buffer", pos.Line, pos.Col)
	}
	bv, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !bv.Ty.IsArray && !bv.Ty.IsBuffer {
		return Value{}, fmt.Errorf("%d:%d: http2.getUnpackedSettings takes a Buffer", pos.Line, pos.Col)
	}
	dataPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", dataPtr, bv.Ref))
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, bv.Ref))

	ty := h2SettingsObjectType()
	e.ensureCalloc()
	obj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", obj, ty.StructSize()))

	// for (i = 0; i+6 <= len; i += 6) { id = b[i]<<8|b[i+1]; v = 4B BE; switch }
	iA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", iA))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", iA))
	loopL := e.freshLabel("h2ups.loop")
	bodyL := e.freshLabel("h2ups.body")
	doneL := e.freshLabel("h2ups.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))
	e.emitLabel(loopL)
	iv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", iv, iA))
	end := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 6", end, iv))
	fits := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sle i64 %s, %s", fits, end, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", fits, bodyL, doneL))
	e.emitLabel(bodyL)
	loadByte := func(off int) string {
		g := e.freshReg()
		idx := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", idx, iv, off))
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", g, dataPtr, idx))
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", b, g))
		z := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i8 %s to i64", z, b))
		return z
	}
	idHi := loadByte(0)
	idLo := loadByte(1)
	idSh := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = shl i64 %s, 8", idSh, idHi))
	idReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i64 %s, %s", idReg, idSh, idLo))
	valReg := "0"
	for b := 0; b < 4; b++ {
		by := loadByte(2 + b)
		sh := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = shl i64 %s, %d", sh, by, (3-b)*8))
		nv := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i64 %s, %s", nv, valReg, sh))
		valReg = nv
	}
	contL := e.freshLabel("h2ups.cont")
	for _, s := range h2SettingsOrder {
		isID := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isID, idReg, s.id))
		setL := e.freshLabel("h2ups.set")
		nextL := e.freshLabel("h2ups.next")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isID, setL, nextL))
		e.emitLabel(setL)
		idx, fty, _ := ty.FieldIndex(s.name)
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, ty.StructIR(), obj, idx))
		if fty.IR == "i1" {
			b := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", b, valReg))
			e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", b, g))
		} else {
			d := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = uitofp i64 %s to double", d, valReg))
			e.emitInstr(fmt.Sprintf("store double %s, ptr %s, align 8", d, g))
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
		e.emitLabel(nextL)
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
	e.emitLabel(contL)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", end, iA))
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))
	e.emitLabel(doneL)
	return Value{Ref: obj, Ty: ty}, nil
}
