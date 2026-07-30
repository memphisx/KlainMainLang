// emit_path.go — Node's `path` module: join, resolve, dirname, basename,
// extname, parse, format, isAbsolute, sep, delimiter. POSIX-only (this
// compiler doesn't cross-compile — see docs/status/PATH.md).
package llvm

import (
	"fmt"

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
func (e *Emitter) emitPathJoin(args []ast.Expression, pos ast.Pos) (Value, error) {
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
	isAbs := e.emitPathStartsWithSlash(vals[0])

	raw := vals[0]
	sep := Value{Ref: e.internString("/"), Ty: TypePtr}
	for i := 1; i < len(vals); i++ {
		var err error
		raw, err = e.emitStringConcat(raw, sep)
		if err != nil {
			return Value{}, err
		}
		raw, err = e.emitStringConcat(raw, vals[i])
		if err != nil {
			return Value{}, err
		}
	}

	e.ensurePathNormalize()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_normalize(ptr %s, i1 %s)", r, raw.Ref, isAbs))
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
func (e *Emitter) emitPathResolve(args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureProcessCwd()
	accPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", accPtr))
	cwdReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_process_cwd()", cwdReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cwdReg, accPtr))

	sep := Value{Ref: e.internString("/"), Ty: TypePtr}
	for _, a := range args {
		segVal, err := e.emitExpr(a)
		if err != nil {
			return Value{}, err
		}
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

func (e *Emitter) emitPathDirname(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: path.dirname takes exactly 1 argument", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)
	e.ensurePathDirname()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_dirname(ptr %s)", r, pathVal.Ref))
	return Value{Ref: r, Ty: TypePtr}, nil
}

func (e *Emitter) emitPathBasename(args []ast.Expression, pos ast.Pos) (Value, error) {
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
	e.ensurePathBasename()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_basename(ptr %s, ptr %s)", r, pathVal.Ref, extRef))
	return Value{Ref: r, Ty: TypePtr}, nil
}

func (e *Emitter) emitPathExtname(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: path.extname takes exactly 1 argument", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)
	e.ensurePathExtname()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_extname(ptr %s)", r, pathVal.Ref))
	return Value{Ref: r, Ty: TypePtr}, nil
}

func (e *Emitter) emitPathIsAbsolute(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: path.isAbsolute takes exactly 1 argument", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)
	isSlash := e.emitPathStartsWithSlash(pathVal)
	return Value{Ref: isSlash, Ty: TypeBool}, nil
}

// emitPathParse implements path.parse(p): {root, dir, base, ext, name}.
// name is computed by reusing __kml_path_basename's own ext-stripping
// argument, passing extname(p) as the ext to strip — equivalent to "base
// minus its extension" without a separate substring routine.
func (e *Emitter) emitPathParse(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: path.parse takes exactly 1 argument", pos.Line, pos.Col)
	}
	pathVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	pathVal = e.coerce(pathVal, TypePtr)

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

// emitPathFormat implements path.format(pathObject): the inverse of
// path.parse, following real Node's own algorithm — base wins over
// name+ext when base is non-empty; dir falls back to root when dir is
// empty; the result is just base when dir is also empty; dir and base are
// joined directly (no separator) when dir equals root (e.g. "/" + "foo" is
// "/foo", not "//foo"), otherwise joined with '/'.
func (e *Emitter) emitPathFormat(args []ast.Expression, pos ast.Pos) (Value, error) {
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
	readField := func(name string) (Value, error) {
		idx, fieldTy, ok := objTy.FieldIndex(name)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: path.format's argument has no field '%s' (expected the shape returned by path.parse)", pos.Line, pos.Col, name)
		}
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, objTy.StructIR(), objVal.Ref, idx))
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, fieldTy.IR, gep, fieldTy.Align()))
		return Value{Ref: result, Ty: fieldTy}, nil
	}
	rootV, err := readField("root")
	if err != nil {
		return Value{}, err
	}
	dirV, err := readField("dir")
	if err != nil {
		return Value{}, err
	}
	baseV, err := readField("base")
	if err != nil {
		return Value{}, err
	}
	extV, err := readField("ext")
	if err != nil {
		return Value{}, err
	}
	nameV, err := readField("name")
	if err != nil {
		return Value{}, err
	}

	e.ensureStrlen()
	e.ensureStrcmp()

	baseLenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", baseLenReg, baseV.Ref))
	baseNonEmpty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", baseNonEmpty, baseLenReg))
	baseFinalRef, err := e.emitStrBranch(baseNonEmpty,
		func() (string, error) { return baseV.Ref, nil },
		func() (string, error) {
			combined, err := e.emitStringConcat(nameV, extV)
			if err != nil {
				return "", err
			}
			return combined.Ref, nil
		},
	)
	if err != nil {
		return Value{}, err
	}

	dirLenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", dirLenReg, dirV.Ref))
	dirNonEmpty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", dirNonEmpty, dirLenReg))
	dirFinalRef, err := e.emitStrBranch(dirNonEmpty,
		func() (string, error) { return dirV.Ref, nil },
		func() (string, error) { return rootV.Ref, nil },
	)
	if err != nil {
		return Value{}, err
	}

	dirFinalLenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", dirFinalLenReg, dirFinalRef))
	dirFinalEmpty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", dirFinalEmpty, dirFinalLenReg))

	noDirL := e.freshLabel("pathformat.nodir")
	hasDirL := e.freshLabel("pathformat.hasdir")
	mergeL := e.freshLabel("pathformat.merge")
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resPtr))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", dirFinalEmpty, noDirL, hasDirL))

	e.emitLabel(noDirL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", baseFinalRef, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(hasDirL)
	sameAsRootReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", sameAsRootReg, dirFinalRef, rootV.Ref))
	sameAsRoot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", sameAsRoot, sameAsRootReg))
	rootJoinL := e.freshLabel("pathformat.rootjoin")
	sepJoinL := e.freshLabel("pathformat.sepjoin")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", sameAsRoot, rootJoinL, sepJoinL))

	e.emitLabel(rootJoinL)
	joined1, err := e.emitStringConcat(Value{Ref: dirFinalRef, Ty: TypePtr}, Value{Ref: baseFinalRef, Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", joined1.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(sepJoinL)
	withSep, err := e.emitStringConcat(Value{Ref: dirFinalRef, Ty: TypePtr}, Value{Ref: e.internString("/"), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	joined2, err := e.emitStringConcat(withSep, Value{Ref: baseFinalRef, Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", joined2.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", result, resPtr))
	return Value{Ref: result, Ty: TypePtr}, nil
}
