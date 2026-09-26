// emit_path.go — Node's `path` module: join, resolve, dirname, basename,
// extname, parse, format, isAbsolute, sep, delimiter. Two flavours
// (TDD-00178): the posix algorithms live here and in runtime_path.go as
// hand-written IR (format shares the sidecar's _format); the win32
// algorithms are the C sidecar behind path_win32.go. A bare `path.X` is the host's flavour, `path.posix.X` and
// `path.win32.X` name one explicitly — pathFlavorOf() resolves the object.
package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// emitPathStartsWithSlash returns an i1 register ref for whether v's first
// byte is '/'. Safe even for an empty string: every string this compiler
// produces is a malloc'd, null-terminated buffer, so byte 0 always exists
// and an empty string's byte 0 (the terminator) simply compares unequal to
// '/' — no length check needed first.
func (e *Emitter) emitPathStartsWithSlash(v Value) string {
	b := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", b, v.Ref))
	isSlash := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, 47", isSlash, b))
	return isSlash
}

// emitPathJoin implements path.join(...segments): concatenates every
// argument with '/' between, then normalizes the result (collapses "."
// segments, resolves ".." against what's already been seen, collapses
// repeated/empty segments as a side effect of splitting on '/'). Whether
// the result is absolute is decided by the *first* argument alone (not the
// concatenated raw string), so an empty first segment can't accidentally
// manufacture a leading '/' that wasn't really there.
func (e *Emitter) emitPathJoin(f pathFlavor, args []ast.Expression, pos ast.Pos) (Value, error) {
	if f == pathWin32 {
		return e.emitPathWin32Variadic("join", args, pos)
	}
	if len(args) == 0 {
		return Value{Ref: e.internString("."), Ty: TypePtr}, nil
	}
	vals := make([]Value, len(args))
	for i, a := range args {
		v, err := e.emitExpr(a)
		if err != nil {
			return Value{}, err
		}
		vals[i] = e.coerce(v, TypePtr)
	}

	// Node's path.join skips empty segments, joins the rest with a single '/',
	// then normalizes — and its normalize preserves a trailing slash. Build the
	// segment-pointer array, hand it to __kml_path_join_segs (the empty-skipping
	// concatenation), then route through the trailing-preserving posix normalize
	// sidecar (the same one path.normalize uses), rather than the IR normalize
	// that strips the trailing slash (which resolve still wants). This makes
	// join('foo','bar/') → 'foo/bar/' and join('a/','') → 'a/' (ADR-00993).
	e.ensurePathJoinSegs()
	e.ensurePathWin32() // __kml_path_posix_normalize
	arr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, i64 %d, align 8", arr, len(vals)))
	for i, v := range vals {
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", slot, arr, i))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", v.Ref, slot))
	}
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_join_segs(ptr %s, i64 %d)", raw, arr, len(vals)))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_posix_normalize(ptr %s)", r, raw))
	return Value{Ref: r, Ty: TypePtr}, nil
}

// emitPathResolve implements path.resolve(...segments): starting from
// process.cwd(), processes arguments left to right — each argument that
// starts with '/' *resets* the accumulator to just that argument (discarding
// everything before it, including cwd), anything else is appended with '/'.
// This left-to-right "reset on absolute" formulation is exactly equivalent
// to real Node's right-to-left "stop at the first absolute segment found"
// algorithm (both end up keeping only the last absolute segment seen, plus
// everything after it), but unrolls at compile time instead of needing a
// runtime loop over a dynamic argument array — the call site's argument
// count is already known to the compiler. The accumulated raw path is
// always absolute by construction, so is_absolute is unconditionally true
// for the final normalize call.
func (e *Emitter) emitPathResolve(f pathFlavor, args []ast.Expression, pos ast.Pos) (Value, error) {
	if f == pathWin32 {
		return e.emitPathWin32Variadic("resolve", args, pos)
	}
	segs := make([]Value, 0, len(args))
	for _, a := range args {
		segVal, err := e.emitExpr(a)
		if err != nil {
			return Value{}, err
		}
		segs = append(segs, segVal)
	}
	return e.emitPathResolveValues(f, segs, pos)
}

// emitPathResolveValues is emitPathResolve over already-evaluated segments
// (POSIX flavor only; the win32 flavor takes the sidecar's variadic path).
func (e *Emitter) emitPathResolveValues(f pathFlavor, segs []Value, pos ast.Pos) (Value, error) {
	if f == pathWin32 {
		return Value{}, fmt.Errorf("%d:%d: internal: emitPathResolveValues is POSIX-only", pos.Line, pos.Col)
	}
	e.ensureProcessCwd()
	accPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", accPtr))
	cwdReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_process_cwd()", cwdReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cwdReg, accPtr))

	sep := Value{Ref: e.internString("/"), Ty: TypePtr}
	for _, segVal := range segs {
		segVal = e.coerce(segVal, TypePtr)
		isAbs := e.emitPathStartsWithSlash(segVal)

		resetL := e.freshLabel("pathresolve.reset")
		appendL := e.freshLabel("pathresolve.append")
		mergeL := e.freshLabel("pathresolve.merge")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isAbs, resetL, appendL))

		e.emitLabel(resetL)
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", segVal.Ref, accPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(appendL)
		curAcc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curAcc, accPtr))
		withSep, err := e.emitStringConcat(Value{Ref: curAcc, Ty: TypePtr}, sep)
		if err != nil {
			return Value{}, err
		}
		newAcc, err := e.emitStringConcat(withSep, segVal)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newAcc.Ref, accPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(mergeL)
	}

	rawReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", rawReg, accPtr))
	e.ensurePathNormalize()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_normalize(ptr %s, i1 true)", r, rawReg))
	return Value{Ref: r, Ty: TypePtr}, nil
}

func (e *Emitter) emitPathDirname(f pathFlavor, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: path.dirname takes exactly 1 argument", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)
	if f == pathWin32 {
		e.ensurePathWin32()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_win32_dirname(ptr %s)", r, pathVal.Ref))
		return Value{Ref: r, Ty: TypePtr}, nil
	}
	e.ensurePathDirname()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_dirname(ptr %s)", r, pathVal.Ref))
	return Value{Ref: r, Ty: TypePtr}, nil
}

func (e *Emitter) emitPathBasename(f pathFlavor, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: path.basename takes 1 or 2 arguments (path, ext?)", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)
	extRef := "null"
	if len(args) == 2 {
		extVal, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		extVal = e.coerce(extVal, TypePtr)
		extRef = extVal.Ref
	}
	if f == pathWin32 {
		e.ensurePathWin32()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_win32_basename(ptr %s, ptr %s)", r, pathVal.Ref, extRef))
		return Value{Ref: r, Ty: TypePtr}, nil
	}
	e.ensurePathBasename()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_basename(ptr %s, ptr %s)", r, pathVal.Ref, extRef))
	return Value{Ref: r, Ty: TypePtr}, nil
}

func (e *Emitter) emitPathExtname(f pathFlavor, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: path.extname takes exactly 1 argument", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)
	if f == pathWin32 {
		e.ensurePathWin32()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_win32_extname(ptr %s)", r, pathVal.Ref))
		return Value{Ref: r, Ty: TypePtr}, nil
	}
	e.ensurePathExtname()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_extname(ptr %s)", r, pathVal.Ref))
	return Value{Ref: r, Ty: TypePtr}, nil
}

func (e *Emitter) emitPathIsAbsolute(f pathFlavor, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: path.isAbsolute takes exactly 1 argument", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)
	if f == pathWin32 {
		e.ensurePathWin32()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_path_win32_is_absolute(ptr %s)", r, pathVal.Ref))
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", b, r))
		return Value{Ref: b, Ty: TypeBool}, nil
	}
	isSlash := e.emitPathStartsWithSlash(pathVal)
	return Value{Ref: isSlash, Ty: TypeBool}, nil
}

// emitPathParse implements path.parse(p): {root, dir, base, ext, name}.
// name is computed by reusing __kml_path_basename's own ext-stripping
// argument, passing extname(p) as the ext to strip — equivalent to "base
// minus its extension" without a separate substring routine.
func (e *Emitter) emitPathParse(f pathFlavor, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: path.parse takes exactly 1 argument", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

	if f == pathWin32 {
		return e.emitPathWin32Parse(pathVal)
	}
	e.ensurePathDirname()
	e.ensurePathBasename()
	e.ensurePathExtname()
	e.ensureMalloc()

	dirReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_dirname(ptr %s)", dirReg, pathVal.Ref))
	baseReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_basename(ptr %s, ptr null)", baseReg, pathVal.Ref))
	extReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_extname(ptr %s)", extReg, pathVal.Ref))
	nameReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_basename(ptr %s, ptr %s)", nameReg, pathVal.Ref, extReg))

	isAbs := e.emitPathStartsWithSlash(pathVal)
	rootReg, err := e.emitStrBranch(isAbs,
		func() (string, error) { return e.internString("/"), nil },
		func() (string, error) { return e.internString(""), nil },
	)
	if err != nil {
		return Value{}, err
	}

	ty := PathParsedType()
	structIR := ty.StructIR()
	objReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", objReg, ty.StructSize()))
	storeField := func(name, ref string) {
		idx, fieldTy, _ := ty.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, objReg, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, ref, gep, fieldTy.Align()))
	}
	storeField("root", rootReg)
	storeField("dir", dirReg)
	storeField("base", baseReg)
	storeField("ext", extReg)
	storeField("name", nameReg)
	return Value{Ref: objReg, Ty: ty}, nil
}

// emitPathFormat implements path.format(pathObject) through the sidecar's
// copy of Node's _format: base wins over name+ext (ext gains a leading dot),
// dir falls back to root, and dir and base join with the separator unless
// dir is root. An absent or undefined field is NULL, read as empty.
func (e *Emitter) emitPathFormat(f pathFlavor, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: path.format takes exactly 1 argument", pos.Line, pos.Col)
	}
	objTy := e.inferExprType(args[0])
	if !objTy.IsObject {
		return Value{}, fmt.Errorf("%d:%d: path.format expects an object with {root, dir, base, ext, name} fields", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	fields := make([]string, 0, 5)
	for _, name := range []string{"root", "dir", "base", "ext", "name"} {
		idx, fieldTy, ok := objTy.FieldIndex(name)
		if !ok {
			fields = append(fields, "ptr null")
			continue
		}
		if fieldTy.IR != "ptr" {
			return Value{}, fmt.Errorf("%d:%d: path.format's '%s' field must be a string", pos.Line, pos.Col, name)
		}
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, objTy.StructIR(), objVal.Ref, idx))
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", v, gep))
		fields = append(fields, "ptr "+v)
	}
	fn := "__kml_path_posix_format"
	if f == pathWin32 {
		fn = "__kml_path_win32_format"
	}
	e.ensurePathWin32()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @%s(%s)", r, fn, strings.Join(fields, ", ")))
	return Value{Ref: r, Ty: TypePtr}, nil
}
