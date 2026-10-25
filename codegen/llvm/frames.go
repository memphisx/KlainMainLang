// frames.go — the shadow call stack an Error's `stack` reads (TDD-00188,
// TDD-00230 P3.5): each user function's prologue links a frame record
// { previous, descriptor, position } into the thread's top, each return
// restores the previous top, and each call site stores its position, so an
// error made anywhere captures the `.ts` frames Node prints. framesrc/frames.c
// formats them.
package llvm

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/lib"
)

//go:embed framesrc/frames.c
var framesSource string

// FramesSource is the shadow call stack's C source.
func FramesSource() string { return layoutHeader() + framesSource }

// frameIR is a frame record: { previous, descriptor, position, function,
// arguments }. The last two are a sloppy function's own boxed value and its
// arguments object (0 otherwise, 1 for the module's frame), stored only in a
// program reading `f.caller`/`f.arguments` (callerprops.go).
const frameIR = "{ ptr, ptr, i64, i64, i64 }"

// ensureFrameCut declares __kml_frame_cut(base): a resumed fiber's base cut
// from its creator's frames (null for none).
func (e *Emitter) ensureFrameCut() {
	e.declareFn("__kml_frame_cut", "declare void @__kml_frame_cut(ptr)")
}

func (e *Emitter) ensureFrames() {
	if e.usedFrames {
		return
	}
	e.usedFrames = true
	e.emitGlobal("@__kml_frame_top = external thread_local global ptr")
	e.emitGlobal("@__kml_frame_base = external thread_local global ptr")
	e.emitGlobal("declare void @__kml_error_capture(ptr)")
	e.emitGlobal("declare ptr @__kml_error_stack(ptr)")
}

// fileOfFunc is the source file of the function node fn, or "" for one of a
// builtin module (whose frames Node shows as its own internals) or unknown.
func (e *Emitter) fileOfFunc(fn ast.Node) string {
	if e.funcFiles == nil {
		e.funcFiles = map[ast.Node]string{}
		if e.prog != nil {
			for st, path := range e.prog.StmtFiles {
				ast.Inspect(st, func(n ast.Node) bool {
					switch n.(type) {
					case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassDeclaration:
						if _, seen := e.funcFiles[n]; !seen {
							e.funcFiles[n] = path
						}
					}
					return true
				})
			}
		}
	}
	path := e.funcFiles[fn]
	if path == "" || strings.HasPrefix(path, lib.ModuleRoot) {
		return ""
	}
	return path
}

// programHasFrames reports whether any of the program's statements is of a
// user file, whose functions keep frame records.
func (e *Emitter) programHasFrames() bool {
	if e.prog == nil {
		return false
	}
	for _, path := range e.prog.StmtFiles {
		if path != "" && !strings.HasPrefix(path, lib.ModuleRoot) {
			return true
		}
	}
	return false
}

// frameDesc is the constant descriptor of a frame named name in file: the
// name Node prints ("" prints the location alone) and the file's URL.
func (e *Emitter) frameDesc(name, file string) string {
	key := [2]string{name, file}
	if g, ok := e.frameDescs[key]; ok {
		return g
	}
	if e.frameDescs == nil {
		e.frameDescs = map[[2]string]string{}
	}
	url := file
	if abs, err := filepath.Abs(file); err == nil {
		url = "file://" + filepath.ToSlash(abs)
		if !strings.HasPrefix(filepath.ToSlash(abs), "/") {
			url = "file:///" + filepath.ToSlash(abs) // a Windows drive path
		}
	}
	// Named by the file's place in the program, not its URL: a unit naming
	// the descriptor keeps the text of a function at the same place in a
	// file of another path (the conformance runner's harness functions), so
	// its cached object serves both. The URL stays in the program's unit.
	g := contentSymbol("@__kml_fdesc.", fmt.Sprintf("%s\x00%d", name, e.fileIndex(file)))
	e.emitGlobal(fmt.Sprintf("%s = private unnamed_addr constant { ptr, ptr } { ptr %s, ptr %s }", g, e.internString(name), e.internString(url)))
	e.frameDescs[key] = g
	return g
}

// fileIndex is file's place among the program's files, in the order their
// statements come.
func (e *Emitter) fileIndex(file string) int {
	if e.fileIndexes == nil {
		e.fileIndexes = map[string]int{}
		if e.prog != nil {
			for _, st := range e.prog.Body {
				if p, ok := e.prog.StmtFiles[st]; ok {
					if _, seen := e.fileIndexes[p]; !seen {
						e.fileIndexes[p] = len(e.fileIndexes)
					}
				}
			}
		}
	}
	if i, ok := e.fileIndexes[file]; ok {
		return i
	}
	i := len(e.fileIndexes)
	e.fileIndexes[file] = i
	return i
}

// packPos is a frame position: line << 32 | column.
func packPos(p ast.Pos) int64 { return int64(p.Line)<<32 | int64(p.Col) }

// enterFrame emits the prologue of a function of source node fn, named as
// Node names its frame: its record links into the thread's top. A builtin
// module's function has none.
func (e *Emitter) enterFrame(fn ast.Node, name string) {
	e.enterFrameIn(e.fileOfFunc(fn), name, fn.GetPos())
}

// frameSlot is the address of the thread-local __kml_frame_<name> (top or
// base). In a program using klain:sync a goroutine may move to another
// thread at any safepoint or channel operation, and the optimizer keeps a
// thread-local's address across calls, so there each access asks the
// runtime for the current thread's slot.
func (e *Emitter) frameSlot(name string) string {
	if !e.usesSyncProgram {
		return "@__kml_frame_" + name
	}
	fn := "__kml_frame_" + name + "_slot"
	e.declareFn(fn, fmt.Sprintf("declare ptr @%s()", fn))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @%s()", r, fn))
	return r
}

// enterFrameIn is enterFrame for a frame of file at pos.
func (e *Emitter) enterFrameIn(file, name string, pos ast.Pos) {
	if file == "" {
		return
	}
	e.ensureFrames()
	desc := e.frameDesc(name, file)
	fr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", fr, frameIR))
	old := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", old, e.frameSlot("top")))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", old, fr))
	d := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr inbounds %s, ptr %s, i32 0, i32 1", d, frameIR, fr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", desc, d))
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr inbounds %s, ptr %s, i32 0, i32 2", p, frameIR, fr))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", packPos(pos), p))
	if e.usesCallerProps() {
		e.storeFrameActivation(fr, "0", "0")
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", fr, e.frameSlot("top")))
	e.frame, e.frameOld, e.frameFile = fr, old, file
}

// setFrameFile re-points the running frame's descriptor at file: a
// top-level statement of another module, run from main.
func (e *Emitter) setFrameFile(file string) {
	if e.frame == "" || file == "" || strings.HasPrefix(file, lib.ModuleRoot) {
		return
	}
	d := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr inbounds %s, ptr %s, i32 0, i32 1", d, frameIR, e.frame))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.frameDesc(e.moduleFrameName(file), file), d))
}

// leaveFrame is the epilogue every return emits: the previous top back.
func (e *Emitter) leaveFrame() {
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.frameOld, e.frameSlot("top")))
}

// frameAt is the call-site hook: the running frame's position becomes pos,
// the call or construction about to run.
func (e *Emitter) frameAt(pos ast.Pos) {
	if e.frame == "" || e.blockDone || pos.Line == 0 {
		return
	}
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr inbounds %s, ptr %s, i32 0, i32 2", p, frameIR, e.frame))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", packPos(pos), p))
}

// enterFiberBase emits a fiber body's base frame (KML_FIBER_ENTRY in
// kml_heap.h): a record with no descriptor whose previous frame is the
// resumer's top, cut when the fiber is first resumed.
func (e *Emitter) enterFiberBase() {
	e.ensureFrames()
	b := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", b, frameIR))
	old := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", old, e.frameSlot("top")))
	e.emitInstr(fmt.Sprintf("store %s zeroinitializer, ptr %s, align 8", frameIR, b))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", old, b))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", b, e.frameSlot("top")))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", b, e.frameSlot("base")))
}

// callSite is the call-site hook around a call or construction at pos: the
// running frame's position becomes pos, and once a call nested in its
// arguments has run, the enclosing call's again, so the frame names the
// call in progress when its callee runs. The returned func ends the call.
func (e *Emitter) callSite(pos ast.Pos) func() {
	if e.frame == "" {
		return func() {}
	}
	e.frameAt(pos)
	e.sitePos = append(e.sitePos, pos)
	ctx := e.funcBuilder
	return func() {
		if e.funcBuilder != ctx {
			return
		}
		ctx.sitePos = ctx.sitePos[:len(ctx.sitePos)-1]
		if n := len(ctx.sitePos); n > 0 {
			e.frameAt(ctx.sitePos[n-1])
		}
	}
}

// moduleFrameName is the name of a module's top-level frame: none for an ES
// module, and CommonJS's module wrapper's, called on module.exports, for a
// file Node runs as CommonJS.
func (e *Emitter) moduleFrameName(file string) string {
	if e.prog != nil && e.prog.ESMFiles[file] {
		return ""
	}
	return "Object.<anonymous>"
}

// accessSite is the position hook around a property read that may throw
// (its receiver may be null, undefined or of any type): the frame is at the
// property, as V8 places the TypeError, and then back at the enclosing call.
func (e *Emitter) accessSite(m *ast.MemberExpression) func() {
	if e.frame == "" {
		return func() {}
	}
	if rt := e.inferExprType(m.Object); !rt.IsDynamic() && !rt.Nullable() && !rt.IsUndefined() {
		return func() {}
	}
	e.frameAt(m.GetPos())
	ctx := e.funcBuilder
	return func() {
		if e.funcBuilder != ctx {
			return
		}
		if n := len(ctx.sitePos); n > 0 {
			e.frameAt(ctx.sitePos[n-1])
		}
	}
}

// callAs is a method call's receiver naming: the receiver's type and the
// property the callee is called as.
type callAs struct{ recv, prop string }

// pushCallAs links a marker record naming as below the callee's frame, for
// the call about to be emitted; the returned func unlinks it. A marker is a
// descriptor with no file (framesrc/frames.c).
func (e *Emitter) pushCallAs(as callAs) func() {
	if as.recv == "" {
		return func() {}
	}
	e.ensureFrames()
	key := "\x00as:" + as.recv + "\x00" + as.prop
	g, ok := e.frameDescs[[2]string{key, ""}]
	if !ok {
		if e.frameDescs == nil {
			e.frameDescs = map[[2]string]string{}
		}
		g = contentSymbol("@__kml_fdesc.", key)
		e.emitGlobal(fmt.Sprintf("%s = private unnamed_addr constant { ptr, ptr, ptr } { ptr %s, ptr null, ptr %s }", g, e.internString(as.recv), e.internString(as.prop)))
		e.frameDescs[[2]string{key, ""}] = g
	}
	m := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", m, frameIR))
	old := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", old, e.frameSlot("top")))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", old, m))
	d := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr inbounds %s, ptr %s, i32 0, i32 1", d, frameIR, m))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", g, d))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", m, e.frameSlot("top")))
	return func() {
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", old, e.frameSlot("top")))
	}
}

// memberCallAs is the receiver naming of a call of callee: V8 names the
// frame of a function called as an object's method by the object's type
// (`Timeout.tick [as _onTimeout]`).
func (e *Emitter) memberCallAs(callee ast.Expression) callAs {
	for {
		switch c := callee.(type) {
		case *ast.NonNullExpression:
			callee = c.Arg
			continue
		case *ast.MemberExpression:
			if rt := e.inferExprType(c.Object); rt.IsClass() {
				return callAs{ast.Unmangle(rt.ClassName), c.Property}
			} else if rt.IsObject() {
				return callAs{"Object", c.Property}
			}
		}
		return callAs{}
	}
}

// callSitePos is V8's position of a call: a named callee's (a member call's
// property), else the call's parenthesis.
func callSitePos(ex *ast.CallExpression) ast.Pos {
	switch ex.Callee.(type) {
	case *ast.Identifier, *ast.MemberExpression:
		return ex.Callee.GetPos()
	}
	return ex.GetPos()
}

// isStackTraceLimit reports whether m is V8's Error.stackTraceLimit.
func (e *Emitter) isStackTraceLimit(m *ast.MemberExpression) bool {
	id, ok := m.Object.(*ast.Identifier)
	return ok && id.Name == "Error" && m.Property == "stackTraceLimit" && !e.isShadowedByLocal("Error")
}
