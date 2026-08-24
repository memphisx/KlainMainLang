// emit_fs_stream.go — codegen for fs.createReadStream / fs.createWriteStream
// (TDD-00108). createReadStream returns a Node Readable pre-filled with the
// file's string chunks (the Readable.from machinery, sourced from chunked
// fread); createWriteStream returns a Node Writable whose write/close sinks
// fwrite/fclose a persistent FILE*. Chunks are strings (this fs is text-first,
// like readFileSync), so a read→write pipe round-trips. See runtime_fs_stream.go.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

const fsStreamDefaultHWM = 65536

// emitFsCreateReadStream implements fs.createReadStream(path[, options]).
func (e *Emitter) emitFsCreateReadStream(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: fs.createReadStream takes (path[, options])", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

	hwm := fsStreamDefaultHWM
	if len(args) == 2 {
		hwm, err = e.fsStreamHWMOption(args[1], pos)
		if err != nil {
			return Value{}, err
		}
	}

	e.ensureNodeStreamRuntime()
	e.ensureFsReadStream()
	chunkTy := TypePtr // string chunks

	// Build a WHATWG rstream, fill it from the file, close it — the exact shape
	// emitReadableStreamFrom uses, but sourced from fread instead of an array.
	fulfillFn := e.emitStreamFulfillThunk(chunkTy)
	rs := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_rs_alloc(double 1.0, ptr %s)", rs, fulfillFn))
	e.emitInstr(fmt.Sprintf("call void @__kml_fs_read_stream(ptr %s, ptr %s, i64 %d)", pathVal.Ref, rs, hwm))
	closed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_rs_close(ptr %s)", closed, rs))

	// Wrap the readable in a Node Readable (.on('data')/.pipe()/for-await).
	return e.wrapWebReadable(Value{Ref: rs, Ty: ReadableStreamType(chunkTy)})
}

// emitFsCreateWriteStream implements fs.createWriteStream(path[, options]).
func (e *Emitter) emitFsCreateWriteStream(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: fs.createWriteStream takes (path[, options])", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

	append := 0
	if len(args) == 2 {
		append, err = e.fsStreamFlagsOption(args[1], pos)
		if err != nil {
			return Value{}, err
		}
	}

	e.ensureNodeStreamRuntime()
	e.ensureFsWriteStream()
	chunkTy := TypePtr // string chunks

	fp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fs_open_write(ptr %s, i64 %d)", fp, pathVal.Ref, append))

	ws := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ws_alloc(double 1.0)", ws))
	// The FILE* is the closure env of both sinks — no separate handle needed.
	e.storeWStreamField(ws, 9, e.buildBuiltinClosure("@__kml_fs_stream_write", fp))
	e.storeWStreamField(ws, 10, e.buildBuiltinClosure("@__kml_fs_stream_close", fp))
	e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", e.buildBuiltinClosure("@__kml_ws_started", ws)))

	ns := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ns_alloc(ptr null, ptr %s, ptr null, ptr null)", ns, ws))
	e.emitInstr(fmt.Sprintf("call void @__kml_ns_arm_writable(ptr %s)", ns))
	return Value{Ref: ns, Ty: NodeWritableType(chunkTy)}, nil
}

// fsStreamHWMOption reads a `{ highWaterMark: N }` option (a compile-time integer
// chunk size). An empty object is allowed; any other key or a runtime value is a
// clean error — the same static-args posture zlib's `{ level }` uses.
func (e *Emitter) fsStreamHWMOption(arg ast.Expression, pos ast.Pos) (int, error) {
	obj, ok := arg.(*ast.ObjectLiteral)
	if !ok {
		return 0, fmt.Errorf("%d:%d: createReadStream options must be an object literal like { highWaterMark: 8192 }", pos.Line, pos.Col)
	}
	hwm := fsStreamDefaultHWM
	for _, prop := range obj.Properties {
		if prop.Key != "highWaterMark" {
			return 0, fmt.Errorf("%d:%d: only the { highWaterMark } createReadStream option is supported (not '%s')", pos.Line, pos.Col, prop.Key)
		}
		n, ok := zlibConstIntLiteral(prop.Value)
		if !ok || n <= 0 {
			return 0, fmt.Errorf("%d:%d: createReadStream { highWaterMark } must be a positive compile-time integer", pos.Line, pos.Col)
		}
		hwm = n
	}
	return hwm, nil
}

// fsStreamFlagsOption reads a `{ flags: "w"|"a" }` option, returning 1 for append.
func (e *Emitter) fsStreamFlagsOption(arg ast.Expression, pos ast.Pos) (int, error) {
	obj, ok := arg.(*ast.ObjectLiteral)
	if !ok {
		return 0, fmt.Errorf("%d:%d: createWriteStream options must be an object literal like { flags: \"a\" }", pos.Line, pos.Col)
	}
	append := 0
	for _, prop := range obj.Properties {
		if prop.Key != "flags" {
			return 0, fmt.Errorf("%d:%d: only the { flags } createWriteStream option is supported (not '%s')", pos.Line, pos.Col, prop.Key)
		}
		lit, ok := prop.Value.(*ast.StringLiteral)
		if !ok {
			return 0, fmt.Errorf("%d:%d: createWriteStream { flags } must be a string literal (\"w\" or \"a\")", pos.Line, pos.Col)
		}
		switch lit.Value {
		case "w":
			append = 0
		case "a":
			append = 1
		default:
			return 0, fmt.Errorf("%d:%d: createWriteStream { flags } supports \"w\" (truncate) and \"a\" (append)", pos.Line, pos.Col)
		}
	}
	return append, nil
}
