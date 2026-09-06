package llvm

import (
	_ "embed"
	"fmt"
	"runtime"

	"KlainMainLang/ast"
)

// path_win32.go — Node's `path.win32` flavour (TDD-00178). The algorithms are
// a C port of lib/path.js's win32 object (pathsrc/path_win32.c), compiled
// alongside the program (the json_parse.c pattern) whenever a win32-flavoured
// path call is emitted: every bare `path.X` on a Windows host, or an explicit
// `path.win32.X` on any host. The posix flavour stays in runtime_path.go.

//go:embed pathsrc/path_win32.c
var pathWin32Source string

// PathWin32Source returns the C source implementing the __kml_path_win32_*
// ABI; main.go writes it next to the .ll and compiles it when UsesPathWin32()
// is set (libc only, no library to locate).
func PathWin32Source() string { return pathWin32Source }

// UsesPathWin32 reports whether any win32-flavoured path call reached codegen.
func (e *Emitter) UsesPathWin32() bool { return e.usesPathWin32 }

// pathFlavor names which of Node's two path flavours a call site resolved to.
type pathFlavor int

const (
	pathPosix pathFlavor = iota
	pathWin32
)

// hostPathFlavor is what a bare `path.X` means on this host: Node's `path` is
// `path.win32` on Windows and `path.posix` everywhere else. A compile-time
// switch, like nodePlatformName() — this compiler builds for the host only.
func hostPathFlavor() pathFlavor {
	if runtime.GOOS == "windows" {
		return pathWin32
	}
	return pathPosix
}

// pathFlavorSep / pathFlavorDelimiter are path.sep / path.delimiter per flavour.
func pathFlavorSep(f pathFlavor) string {
	if f == pathWin32 {
		return "\\"
	}
	return "/"
}

func pathFlavorDelimiter(f pathFlavor) string {
	if f == pathWin32 {
		return ";"
	}
	return ":"
}

// ensurePathWin32 declares the sidecar's ABI once and marks the program as
// needing path_win32.c compiled in.
func (e *Emitter) ensurePathWin32() {
	e.usesPathWin32 = true
	if e.declaredPathWin32 {
		return
	}
	e.declaredPathWin32 = true
	e.emitGlobal("declare ptr @__kml_path_win32_join(i64, ptr)")
	e.emitGlobal("declare ptr @__kml_path_win32_resolve(i64, ptr, ptr, i32)")
	e.emitGlobal("declare ptr @__kml_path_win32_dirname(ptr)")
	e.emitGlobal("declare ptr @__kml_path_win32_basename(ptr, ptr)")
	e.emitGlobal("declare ptr @__kml_path_win32_extname(ptr)")
	e.emitGlobal("declare i32 @__kml_path_win32_is_absolute(ptr)")
	e.emitGlobal("declare void @__kml_path_win32_parse(ptr, ptr, ptr, ptr, ptr, ptr)")
	e.emitGlobal("declare ptr @__kml_path_win32_format(ptr, ptr, ptr, ptr, ptr)")
	// url.pathToFileURL / url.fileURLToPath, Windows halves (ADR-00722).
	e.emitGlobal("declare ptr @__kml_path_win32_to_file_url(ptr, ptr, ptr)")
	e.emitGlobal("declare ptr @__kml_path_win32_from_file_url(ptr, ptr, ptr)")
	e.emitGlobal("declare ptr @__kml_path_win32_split_file_host(ptr, ptr)")
	// normalize / relative / toNamespacedPath, both flavours (ADR-00723): the
	// posix trio lives in the same sidecar since it shares normalizeString.
	e.emitGlobal("declare ptr @__kml_path_win32_normalize(ptr)")
	e.emitGlobal("declare ptr @__kml_path_win32_relative(ptr, ptr, ptr, i32)")
	e.emitGlobal("declare ptr @__kml_path_win32_to_namespaced_path(ptr, ptr, i32)")
	e.emitGlobal("declare ptr @__kml_path_posix_normalize(ptr)")
	e.emitGlobal("declare ptr @__kml_path_posix_resolve(i64, ptr, ptr)")
	e.emitGlobal("declare ptr @__kml_path_posix_relative(ptr, ptr, ptr)")
}

// hostIsWindowsI32 is Node's `isWindows` as the sidecar's i32 argument.
func hostIsWindowsI32() int {
	if runtime.GOOS == "windows" {
		return 1
	}
	return 0
}

// emitPathNormalize implements path.normalize(p) for either flavour via the
// sidecar (the posix one keeps a trailing `/`, unlike the IR join).
func (e *Emitter) emitPathNormalize(f pathFlavor, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: path.normalize takes exactly 1 argument", pos.Line, pos.Col)
	}
	v, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	v = e.coerce(v, TypePtr)
	e.ensurePathWin32()
	fn := "@__kml_path_posix_normalize"
	if f == pathWin32 {
		fn = "@__kml_path_win32_normalize"
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr %s(ptr %s)", r, fn, v.Ref))
	return Value{Ref: r, Ty: TypePtr}, nil
}

// emitPathRelative implements path.relative(from, to): both flavours resolve
// their arguments against process.cwd() first, as Node does.
func (e *Emitter) emitPathRelative(f pathFlavor, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: path.relative takes exactly 2 arguments (from, to)", pos.Line, pos.Col)
	}
	from, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	from = e.coerce(from, TypePtr)
	to, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	to = e.coerce(to, TypePtr)
	e.ensurePathWin32()
	e.ensureProcessCwd()
	cwd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_process_cwd()", cwd))
	r := e.freshReg()
	if f == pathWin32 {
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_win32_relative(ptr %s, ptr %s, ptr %s, i32 %d)", r, from.Ref, to.Ref, cwd, hostIsWindowsI32()))
	} else {
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_posix_relative(ptr %s, ptr %s, ptr %s)", r, from.Ref, to.Ref, cwd))
	}
	return Value{Ref: r, Ty: TypePtr}, nil
}

// emitPathToNamespacedPath implements path.toNamespacedPath(p): the identity
// for posix; for win32 a resolved drive or UNC path gains the `\\?\` prefix.
func (e *Emitter) emitPathToNamespacedPath(f pathFlavor, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: path.toNamespacedPath takes exactly 1 argument", pos.Line, pos.Col)
	}
	v, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	v = e.coerce(v, TypePtr)
	if f == pathPosix {
		return v, nil
	}
	e.ensurePathWin32()
	e.ensureProcessCwd()
	cwd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_process_cwd()", cwd))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_win32_to_namespaced_path(ptr %s, ptr %s, i32 %d)", r, v.Ref, cwd, hostIsWindowsI32()))
	return Value{Ref: r, Ty: TypePtr}, nil
}

// pathFlavorOf resolves the object of a `path` member expression to a flavour:
// the bare virtual-module marker is the host's flavour; `path.posix` and
// `path.win32` name one explicitly. ok is false for anything else.
func pathFlavorOf(obj ast.Expression) (pathFlavor, bool) {
	if id, ok := obj.(*ast.Identifier); ok && id.Name == "path__kml_builtin" {
		return hostPathFlavor(), true
	}
	if mem, ok := obj.(*ast.MemberExpression); ok {
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "path__kml_builtin" {
			switch mem.Property {
			case "posix":
				return pathPosix, true
			case "win32":
				return pathWin32, true
			}
		}
	}
	return pathPosix, false
}

// emitPathWin32Variadic emits path.win32.join / path.win32.resolve: the
// argument count is known at the call site, so the segments go into an
// entry-block `[N x ptr]` alloca and the sidecar walks (n, ptr). resolve also
// receives process.cwd() and Node's `isWindows` for its one host branch.
func (e *Emitter) emitPathWin32Variadic(which string, args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensurePathWin32()
	n := len(args)
	arrTy := fmt.Sprintf("[%d x ptr]", n)
	arr := e.freshReg()
	if n == 0 {
		// A zero-length array alloca is legal but pointless; pass null.
		arr = "null"
	} else {
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", arr, arrTy))
	}
	for i, a := range args {
		v, err := e.emitExpr(a)
		if err != nil {
			return Value{}, err
		}
		v = e.coerce(v, TypePtr)
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 0, i64 %d", slot, arrTy, arr, i))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", v.Ref, slot))
	}
	r := e.freshReg()
	if which == "join" {
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_win32_join(i64 %d, ptr %s)", r, n, arr))
		return Value{Ref: r, Ty: TypePtr}, nil
	}
	e.ensureProcessCwd()
	cwd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_process_cwd()", cwd))
	hostWin := 0
	if runtime.GOOS == "windows" {
		hostWin = 1
	}
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_win32_resolve(i64 %d, ptr %s, ptr %s, i32 %d)", r, n, arr, cwd, hostWin))
	return Value{Ref: r, Ty: TypePtr}, nil
}

// emitPathWin32Parse emits path.win32.parse(p): the sidecar fills five
// out-pointers, which are copied into a fresh PathParsedType object.
func (e *Emitter) emitPathWin32Parse(pathVal Value) (Value, error) {
	e.ensurePathWin32()
	e.ensureMalloc()
	names := []string{"root", "dir", "base", "ext", "name"}
	outs := make([]string, len(names))
	for i := range names {
		outs[i] = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", outs[i]))
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_path_win32_parse(ptr %s, ptr %s, ptr %s, ptr %s, ptr %s, ptr %s)",
		pathVal.Ref, outs[0], outs[1], outs[2], outs[3], outs[4]))
	ty := PathParsedType()
	structIR := ty.StructIR()
	objReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", objReg, ty.StructSize()))
	for i, name := range names {
		val := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", val, outs[i]))
		idx, fieldTy, _ := ty.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, objReg, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, val, gep, fieldTy.Align()))
	}
	return Value{Ref: objReg, Ty: ty}, nil
}
