// emit_fs.go — fs.readFileSync/writeFileSync/appendFileSync/existsSync/
// unlinkSync: synchronous file I/O, recognized as a pseudo-namespace
// (matching process.*/Math.*/JSON.* — not a real importable module).
//
// All synchronous by design: there's no event loop in this compiler, so
// there's no non-blocking variant to offer. Text-only, like every string
// here — reading a file with embedded null bytes truncates at the first
// one (see runtime.go's ensureFsReadFile doc). A failed read/write/append/
// delete throws a catchable Error (built from strerror(errno)), matching
// how fetch's network failures are surfaced (see emit_fetch.go).
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

func (e *Emitter) emitFsReadFileSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: fs.readFileSync takes exactly 1 argument (path)", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

	e.ensureFsReadFile()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fs_read_file(ptr %s)", r, pathVal.Ref))
	return Value{Ref: r, Ty: TypePtr}, nil
}

// emitFsReadFileSyncBytes implements fs.readFileSyncBytes(path): Uint8Array
// (ADR-00094) — the null-byte-safe sibling of readFileSync, going through
// __kml_fs_read_file_raw directly instead of the string-returning
// __kml_fs_read_file wrapper. __kml_fs_read_file_raw's {ptr, i64} return is
// already the exact SSA aggregate shape a first-class TypedArray value
// uses (see emit_arraybuffer.go's .subarray()), so no repacking is needed.
func (e *Emitter) emitFsReadFileSyncBytes(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: fs.readFileSyncBytes takes exactly 1 argument (path)", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

	e.ensureFsReadFileRaw()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { ptr, i64 } @__kml_fs_read_file_raw(ptr %s)", r, pathVal.Ref))
	return Value{Ref: r, Ty: TypedArrayType("uint8")}, nil
}

func (e *Emitter) emitFsWriteFileSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	return e.emitFsWriteLikeCall(args, pos, "fs.writeFileSync", "@__kml_fs_write_file")
}

func (e *Emitter) emitFsAppendFileSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	return e.emitFsWriteLikeCall(args, pos, "fs.appendFileSync", "@__kml_fs_append_file")
}

// emitFsWriteLikeCall backs both writeFileSync and appendFileSync. A plain
// string `data` argument keeps using the original strlen-based runtime
// functions, behavior-unchanged. An ArrayBuffer or TypedArray `data`
// argument (ADR-00094) routes to the explicit-length runtime siblings
// instead, so a buffer with an embedded null byte writes out whole rather
// than truncating at the first one — dispatch is on data's inferred type,
// the same pattern emitFetch already uses for its optional init fields
// (emit_fetch.go).
func (e *Emitter) emitFsWriteLikeCall(args []ast.Expression, pos ast.Pos, name, runtimeFn string) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: %s takes exactly 2 arguments (path, data)", pos.Line, pos.Col, name)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

	isWrite := runtimeFn == "@__kml_fs_write_file"
	dataTy := e.inferExprType(args[1])

	switch {
	case dataTy.IsArrayBuffer:
		bufVal, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		lenVal, err := e.emitArrayBufferByteLength(bufVal)
		if err != nil {
			return Value{}, err
		}
		dataSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", dataSlot, bufVal.Ref))
		dataReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataReg, dataSlot))

		bytesFn := "@__kml_fs_write_file_bytes"
		if isWrite {
			e.ensureFsWriteFileBytes()
		} else {
			bytesFn = "@__kml_fs_append_file_bytes"
			e.ensureFsAppendFileBytes()
		}
		e.emitInstr(fmt.Sprintf("call void %s(ptr %s, ptr %s, i64 %s)", bytesFn, pathVal.Ref, dataReg, lenVal.Ref))
		return Value{Ty: TypeVoid}, nil

	case dataTy.IsTypedArray:
		ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(args[1], pos)
		if err != nil {
			return Value{}, err
		}
		byteLenVal, err := e.emitTypedArrayByteLength(lenReg, elemTy)
		if err != nil {
			return Value{}, err
		}

		bytesFn := "@__kml_fs_write_file_bytes"
		if isWrite {
			e.ensureFsWriteFileBytes()
		} else {
			bytesFn = "@__kml_fs_append_file_bytes"
			e.ensureFsAppendFileBytes()
		}
		e.emitInstr(fmt.Sprintf("call void %s(ptr %s, ptr %s, i64 %s)", bytesFn, pathVal.Ref, ptrReg, byteLenVal.Ref))
		return Value{Ty: TypeVoid}, nil

	default:
		dataVal, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		dataVal = e.coerce(dataVal, TypePtr)

		if isWrite {
			e.ensureFsWriteFile()
		} else {
			e.ensureFsAppendFile()
		}
		e.emitInstr(fmt.Sprintf("call void %s(ptr %s, ptr %s)", runtimeFn, pathVal.Ref, dataVal.Ref))
		return Value{Ty: TypeVoid}, nil
	}
}

func (e *Emitter) emitFsExistsSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: fs.existsSync takes exactly 1 argument (path)", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

	e.ensureFsExists()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_fs_exists(ptr %s)", r, pathVal.Ref))
	return Value{Ref: r, Ty: TypeBool}, nil
}

func (e *Emitter) emitFsUnlinkSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: fs.unlinkSync takes exactly 1 argument (path)", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

	e.ensureFsUnlink()
	e.emitInstr(fmt.Sprintf("call void @__kml_fs_unlink(ptr %s)", pathVal.Ref))
	return Value{Ty: TypeVoid}, nil
}

func (e *Emitter) emitFsMkdirSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: fs.mkdirSync takes (path[, { recursive: true }])", pos.Line, pos.Col)
	}
	// `{ recursive: true }` (ADR-00487): creates every missing prefix and
	// tolerates already-exists. Only the literal `recursive: true` form —
	// anything else in the options object is a clean rejection.
	recursive := false
	if len(args) == 2 {
		ol, ok := args[1].(*ast.ObjectLiteral)
		if !ok || len(ol.Properties) != 1 || ol.Properties[0].Key != "recursive" {
			return Value{}, fmt.Errorf("%d:%d: fs.mkdirSync options support only `{ recursive: true }`", pos.Line, pos.Col)
		}
		if b, ok := ol.Properties[0].Value.(*ast.BooleanLiteral); !ok || !b.Value {
			return Value{}, fmt.Errorf("%d:%d: fs.mkdirSync options support only `{ recursive: true }`", pos.Line, pos.Col)
		}
		recursive = true
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

	if recursive {
		e.ensureFsMkdirP()
		e.emitInstr(fmt.Sprintf("call void @__kml_fs_mkdir_p(ptr %s)", pathVal.Ref))
		return Value{Ty: TypeVoid}, nil
	}
	e.ensureFsMkdir()
	e.emitInstr(fmt.Sprintf("call void @__kml_fs_mkdir(ptr %s)", pathVal.Ref))
	return Value{Ty: TypeVoid}, nil
}

func (e *Emitter) emitFsRmdirSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: fs.rmdirSync takes (path[, { recursive: true }])", pos.Line, pos.Col)
	}
	// `{ recursive: true }` removes the directory tree — Node deprecated the
	// option on rmdirSync in favor of rmSync but still honors it. Only the
	// literal `recursive: true` form is accepted (like mkdirSync's).
	recursive := false
	if len(args) == 2 {
		ol, ok := args[1].(*ast.ObjectLiteral)
		if !ok || len(ol.Properties) != 1 || ol.Properties[0].Key != "recursive" {
			return Value{}, fmt.Errorf("%d:%d: fs.rmdirSync options support only `{ recursive: true }`", pos.Line, pos.Col)
		}
		bl, ok := ol.Properties[0].Value.(*ast.BooleanLiteral)
		if !ok || !bl.Value {
			return Value{}, fmt.Errorf("%d:%d: fs.rmdirSync options support only `{ recursive: true }`", pos.Line, pos.Col)
		}
		recursive = true
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

	if recursive {
		// Reuse rmSync's recursive tree-removal; force=false so a missing path
		// still throws, matching rmdirSync's own posture.
		e.ensureFsRm()
		e.emitInstr(fmt.Sprintf("call void @__kml_fs_rm(ptr %s, i1 true, i1 false)", pathVal.Ref))
		return Value{Ty: TypeVoid}, nil
	}
	e.ensureFsRmdir()
	e.emitInstr(fmt.Sprintf("call void @__kml_fs_rmdir(ptr %s)", pathVal.Ref))
	return Value{Ty: TypeVoid}, nil
}

func (e *Emitter) emitFsRenameSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: fs.renameSync takes exactly 2 arguments (oldPath, newPath)", pos.Line, pos.Col)
	}
	oldVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	oldVal = e.coerce(oldVal, TypePtr)
	newVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	newVal = e.coerce(newVal, TypePtr)

	e.ensureFsRename()
	e.emitInstr(fmt.Sprintf("call void @__kml_fs_rename(ptr %s, ptr %s)", oldVal.Ref, newVal.Ref))
	return Value{Ty: TypeVoid}, nil
}

// emitFsCopyFileSync implements fs.copyFileSync(src, dest): reads src fully
// then writes it to dest — no new C-level I/O code needed, since both halves
// of "copy a file" already exist as their own fs.* runtime helpers. Goes
// through the binary-safe pair (__kml_fs_read_file_raw, which returns the
// buffer plus its true byte count, and __kml_fs_write_file_bytes, which
// writes that exact length — ADR-00094) rather than the strlen-based text
// helpers, so a src file with an embedded null byte copies back at its real
// size instead of truncated at the first null.
func (e *Emitter) emitFsCopyFileSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: fs.copyFileSync takes exactly 2 arguments (src, dest)", pos.Line, pos.Col)
	}
	srcVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	srcVal = e.coerce(srcVal, TypePtr)
	destVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	destVal = e.coerce(destVal, TypePtr)

	e.ensureFsReadFileRaw()
	e.ensureFsWriteFileBytes()
	rawReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { ptr, i64 } @__kml_fs_read_file_raw(ptr %s)", rawReg, srcVal.Ref))
	bufReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 0", bufReg, rawReg))
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 1", lenReg, rawReg))
	e.emitInstr(fmt.Sprintf("call void @__kml_fs_write_file_bytes(ptr %s, ptr %s, i64 %s)", destVal.Ref, bufReg, lenReg))
	return Value{Ty: TypeVoid}, nil
}

// emitFsReaddirSync implements fs.readdirSync(path): lists a directory's
// entries (excluding "." and "..") as a string[], in whatever order the OS's
// own readdir() returns them (unspecified/filesystem-dependent — matching
// real Node's own readdirSync, which makes no ordering guarantee either).
func (e *Emitter) emitFsReaddirSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: fs.readdirSync takes exactly 1 argument (path)", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

	e.ensureFsReaddir()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_fs_readdir(ptr %s)", r, pathVal.Ref))
	return Value{Ref: r, Ty: ArrayOf(TypePtr)}, nil
}

// emitFsStatSync implements fs.statSync(path) (ADR-00495): __kml_fs_stat
// throws on failure (ENOENT etc. via the shared fs error), otherwise the
// {mode, size, mtimeMs} triple fills a fresh Stats object.
func (e *Emitter) emitFsStatSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: fs.statSync takes exactly 1 argument (path)", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)
	e.ensureFsStat()
	e.ensureMalloc()
	trip := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s @__kml_fs_stat(ptr %s)", trip, statResultIR, pathVal.Ref))
	return e.buildStatsObject(trip), nil
}

// emitStatsKindCall implements stats.isFile()/isDirectory(): mask the hidden
// st_mode word with S_IFMT (0xF000) and compare against S_IFREG (0x8000) /
// S_IFDIR (0x4000) — identical values on every POSIX target here.
func (e *Emitter) emitStatsKindCall(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: stats.%s() takes no arguments", pos.Line, pos.Col, method)
	}
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	idx, fieldTy, _ := objVal.Ty.FieldIndex("mode")
	mode := e.loadFieldValue(objVal, idx, fieldTy)
	masked := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, 61440", masked, mode.Ref))
	want := "32768"
	switch method {
	case "isDirectory":
		want = "16384"
	case "isSymbolicLink":
		want = "40960"
	}
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", res, masked, want))
	return Value{Ref: res, Ty: TypeBool}, nil
}

// emitFsLstatSync — statSync's non-following twin (ADR-00497).
func (e *Emitter) emitFsLstatSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: fs.lstatSync takes exactly 1 argument (path)", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)
	e.ensureFsLstat()
	e.ensureMalloc()
	trip := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s @__kml_fs_lstat(ptr %s)", trip, statResultIR, pathVal.Ref))
	return e.buildStatsObject(trip), nil
}

// buildStatsObject fills a fresh Stats heap object from the runtime's 14-i64
// stat result (ADR-00565) — shared by statSync, lstatSync, and fstatSync. The
// result's element order matches statFieldOrder / StatsType exactly.
func (e *Emitter) buildStatsObject(trip string) Value {
	ty := StatsType()
	e.ensureMalloc()
	obj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", obj, ty.StructSize()))
	for i, name := range statFieldOrder {
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue %s %s, %d", v, statResultIR, trip, i))
		idx, _, _ := ty.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, ty.StructIR(), obj, idx))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v, gep))
	}
	return Value{Ref: obj, Ty: ty}
}

// emitFsPathOp handles the one-shot path-based ops (ADR-00497): a string
// argument in, void or a string out, throwing the shared fs error on
// failure. chmod/truncate/access take an optional numeric second argument.
func (e *Emitter) emitFsPathOp(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	argN := map[string][2]int{
		"realpathSync": {1, 1}, "mkdtempSync": {1, 1}, "readlinkSync": {1, 1},
		"symlinkSync": {2, 2}, "chmodSync": {2, 2}, "truncateSync": {1, 2}, "accessSync": {1, 2},
	}[method]
	if len(args) < argN[0] || len(args) > argN[1] {
		return Value{}, fmt.Errorf("%d:%d: fs.%s: wrong argument count", pos.Line, pos.Col, method)
	}
	e.ensureFsPathOps()
	p0, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	p0 = e.coerce(p0, TypePtr)
	switch method {
	case "realpathSync", "mkdtempSync", "readlinkSync":
		// The runtime returns a raw C string — wrap it into a
		// length-prefixed header string so .length/concat/indexOf work.
		fn := map[string]string{"realpathSync": "realpath", "mkdtempSync": "mkdtemp", "readlinkSync": "readlink"}[method]
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fs_%s(ptr %s)", raw, fn, p0.Ref))
		e.ensureStrHeaderRuntime()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", r, raw))
		return Value{Ref: r, Ty: TypePtr}, nil
	case "symlinkSync":
		p1, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		p1 = e.coerce(p1, TypePtr)
		e.emitInstr(fmt.Sprintf("call void @__kml_fs_symlink(ptr %s, ptr %s)", p0.Ref, p1.Ref))
		return Value{Ty: TypeVoid}, nil
	case "chmodSync", "truncateSync", "accessSync":
		numRef := "0"
		if len(args) == 2 {
			nv, err := e.emitExpr(args[1])
			if err != nil {
				return Value{}, err
			}
			numRef = e.coerce(nv, TypeI64).Ref
		}
		fn := map[string]string{"chmodSync": "chmod", "truncateSync": "truncate", "accessSync": "access"}[method]
		e.emitInstr(fmt.Sprintf("call void @__kml_fs_%s(ptr %s, i64 %s)", fn, p0.Ref, numRef))
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: fs.%s not handled", pos.Line, pos.Col, method)
}

// emitFsRmSync implements fs.rmSync(path[, {recursive, force}]) (ADR-00497).
// Options must be a literal, same as mkdirSync's {recursive: true}.
func (e *Emitter) emitFsRmSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: fs.rmSync takes (path[, options])", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)
	recursive, force := "false", "false"
	if len(args) == 2 {
		ol, ok := args[1].(*ast.ObjectLiteral)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: fs.rmSync options must be an object literal", pos.Line, pos.Col)
		}
		for _, prop := range ol.Properties {
			bl, isBool := prop.Value.(*ast.BooleanLiteral)
			if !isBool {
				return Value{}, fmt.Errorf("%d:%d: fs.rmSync option '%s' must be a boolean literal", pos.Line, pos.Col, prop.Key)
			}
			val := "false"
			if bl.Value {
				val = "true"
			}
			switch prop.Key {
			case "recursive":
				recursive = val
			case "force":
				force = val
			default:
				return Value{}, fmt.Errorf("%d:%d: unknown fs.rmSync option '%s'", pos.Line, pos.Col, prop.Key)
			}
		}
	}
	e.ensureFsRm()
	e.emitInstr(fmt.Sprintf("call void @__kml_fs_rm(ptr %s, i1 %s, i1 %s)", pathVal.Ref, recursive, force))
	return Value{Ty: TypeVoid}, nil
}

// emitFsOpenSync implements fs.openSync(path[, flags[, mode]]) (ADR-00498):
// flags must be a compile-time string literal (mapped to the host's O_*
// bits via openFlagBits) or a numeric expression; mode defaults to 0o666.
func (e *Emitter) emitFsOpenSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: fs.openSync takes (path[, flags[, mode]])", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)
	flagsRef := "0"
	if len(args) >= 2 {
		if lit, ok := args[1].(*ast.StringLiteral); ok {
			bits, known := openFlagBits(lit.Value)
			if !known {
				return Value{}, fmt.Errorf("%d:%d: fs.openSync: unsupported flags '%s' (r, r+, w, w+, a, a+, wx, ax)", pos.Line, pos.Col, lit.Value)
			}
			flagsRef = fmt.Sprintf("%d", bits)
		} else {
			fv, err := e.emitExpr(args[1])
			if err != nil {
				return Value{}, err
			}
			flagsRef = e.coerce(fv, TypeI64).Ref
		}
	}
	modeRef := "438"
	if len(args) == 3 {
		mv, err := e.emitExpr(args[2])
		if err != nil {
			return Value{}, err
		}
		modeRef = e.coerce(mv, TypeI64).Ref
	}
	e.ensureFsFdOps()
	fd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_fs_open(ptr %s, i64 %s, i64 %s)", fd, pathVal.Ref, flagsRef, modeRef))
	return Value{Ref: fd, Ty: TypeI64}, nil
}

// emitFsCloseSync implements fs.closeSync(fd).
func (e *Emitter) emitFsCloseSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: fs.closeSync takes exactly 1 argument (fd)", pos.Line, pos.Col)
	}
	fv, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	fd := e.coerce(fv, TypeI64)
	e.ensureFsFdOps()
	f32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", f32, fd.Ref))
	e.emitInstr(fmt.Sprintf("call i32 @close(i32 %s)", f32))
	return Value{Ty: TypeVoid}, nil
}

// emitFsWriteSync implements fs.writeSync(fd, string[, position]) — string
// data only (Node's Buffer form needs the offset/length variant readSync
// has; a string is this compiler's canonical chunk). Returns bytes written.
func (e *Emitter) emitFsWriteSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: fs.writeSync takes (fd, string[, position])", pos.Line, pos.Col)
	}
	fv, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	fd := e.coerce(fv, TypeI64)
	dv, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	dv = e.coerce(dv, TypePtr)
	posRef := "-1"
	if len(args) == 3 {
		pv, err := e.emitExpr(args[2])
		if err != nil {
			return Value{}, err
		}
		posRef = e.coerce(pv, TypeI64).Ref
	}
	e.ensureFsFdOps()
	e.ensureStrHeaderRuntime()
	ln := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", ln, dv.Ref))
	n := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_fs_fdrw(i64 %s, ptr %s, i64 %s, i64 %s, i1 true)", n, fd.Ref, dv.Ref, ln, posRef))
	return Value{Ref: n, Ty: TypeI64}, nil
}

// emitFsReadSync implements fs.readSync(fd, buffer[, offset, length,
// position]) — buffer is a Uint8Array/Buffer; returns bytes read.
func (e *Emitter) emitFsReadSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 2 || len(args) > 5 {
		return Value{}, fmt.Errorf("%d:%d: fs.readSync takes (fd, buffer[, offset, length, position])", pos.Line, pos.Col)
	}
	fv, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	fd := e.coerce(fv, TypeI64)
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(args[1], pos)
	if err != nil {
		return Value{}, err
	}
	if elemTy.Align() != 1 {
		return Value{}, fmt.Errorf("%d:%d: fs.readSync's buffer must be a Uint8Array/Buffer", pos.Line, pos.Col)
	}
	offRef := "0"
	if len(args) >= 3 {
		ov, err := e.emitExpr(args[2])
		if err != nil {
			return Value{}, err
		}
		offRef = e.coerce(ov, TypeI64).Ref
	}
	lnRef := ""
	if len(args) >= 4 {
		lv, err := e.emitExpr(args[3])
		if err != nil {
			return Value{}, err
		}
		lnRef = e.coerce(lv, TypeI64).Ref
	} else {
		lnRef = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", lnRef, lenReg, offRef))
	}
	posRef := "-1"
	if len(args) == 5 {
		pv, err := e.emitExpr(args[4])
		if err != nil {
			return Value{}, err
		}
		posRef = e.coerce(pv, TypeI64).Ref
	}
	e.ensureFsFdOps()
	dst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", dst, ptrReg, offRef))
	n := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_fs_fdrw(i64 %s, ptr %s, i64 %s, i64 %s, i1 false)", n, fd.Ref, dst, lnRef, posRef))
	return Value{Ref: n, Ty: TypeI64}, nil
}

// emitFsFstatSync implements fs.fstatSync(fd) — statSync over an open fd.
func (e *Emitter) emitFsFstatSync(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: fs.fstatSync takes exactly 1 argument (fd)", pos.Line, pos.Col)
	}
	fv, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	fd := e.coerce(fv, TypeI64)
	e.ensureFsFdOps()
	e.ensureMalloc()
	trip := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s @__kml_fs_fstat(i64 %s)", trip, statResultIR, fd.Ref))
	return e.buildStatsObject(trip), nil
}
