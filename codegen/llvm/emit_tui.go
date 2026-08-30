package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emit_tui.go — TDD-00150 Stage 1: the `klain:tui` builder + render codegen.
//
// Every builder (Box/Text/List/Spinner/Progress/TextInput) lowers to a
// __kml_tui_node(kind) allocation followed by style/attribute setter calls, and
// returns the opaque node handle (a ptr). Children and text are inserted
// imperatively, so the whole retained tree is built in C (tui.go) with no walked
// KML object tree and no closure->C trampoline — the state->view->update loop
// lives in userland TS over klain:tty. render(root) runs Yoga layout + the diff
// painter and frees the tree.
//
// Style props are read at *compile time* from the object-literal argument at the
// call site: enum/colour/border/boolean props must be literals (mapped to their
// integer form here); numeric props (sizes, padding, progress) may be arbitrary
// expressions, evaluated and passed as doubles with a NaN sentinel meaning
// "unset". This mirrors how the rest of the compiler treats fixed-shape option
// objects and keeps the node model free of runtime object reflection.

// nanIR is the LLVM double literal for a quiet NaN — the "unset" sentinel the
// tui.c setters test with isnan(), so an omitted dimension leaves Yoga's own
// default (auto) rather than forcing a value.
const nanIR = "0x7FF8000000000000"

// ensureTuiDecls declares every tui runtime extern exactly once.
func (e *Emitter) ensureTuiDecls() {
	if e.usedTui {
		return
	}
	e.usedTui = true
	for _, d := range []string{
		"declare ptr @__kml_tui_node(i32)",
		"declare void @__kml_tui_set_size(ptr, double, double)",
		"declare void @__kml_tui_set_min(ptr, double, double)",
		"declare void @__kml_tui_set_flex(ptr, i32, double, double, double)",
		"declare void @__kml_tui_set_justify(ptr, i32)",
		"declare void @__kml_tui_set_align(ptr, i32)",
		"declare void @__kml_tui_set_self(ptr, i32)",
		"declare void @__kml_tui_set_wrap(ptr, i32)",
		"declare void @__kml_tui_set_padding(ptr, double, double, double, double)",
		"declare void @__kml_tui_set_margin(ptr, double, double, double, double)",
		"declare void @__kml_tui_set_gap(ptr, double)",
		"declare void @__kml_tui_set_colors(ptr, i32, i32)",
		"declare void @__kml_tui_set_attr(ptr, i32)",
		"declare void @__kml_tui_set_border(ptr, i32, i32)",
		"declare void @__kml_tui_set_text(ptr, ptr, i32)",
		"declare void @__kml_tui_add_item(ptr, ptr)",
		"declare void @__kml_tui_set_selected(ptr, i32)",
		"declare void @__kml_tui_set_progress(ptr, double)",
		"declare void @__kml_tui_set_spinner(ptr, i32)",
		"declare void @__kml_tui_insert(ptr, ptr)",
		"declare void @__kml_tui_render(ptr)",
		"declare void @__kml_tui_enter()",
		"declare void @__kml_tui_leave()",
	} {
		e.emitGlobal(d)
	}
}

// component kind codes — must match tui.go's K_* enum.
const (
	tuiBox = iota
	tuiText
	tuiList
	tuiSpinner
	tuiProgress
	tuiInput
)

func (e *Emitter) emitTuiModuleCall(member string, args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureTuiDecls()
	switch member {
	case "Box":
		return e.emitTuiBox(args, pos)
	case "Text":
		return e.emitTuiLeafText(tuiText, "Text", args, pos)
	case "TextInput":
		return e.emitTuiLeafText(tuiInput, "TextInput", args, pos)
	case "List":
		return e.emitTuiList(args, pos)
	case "Spinner":
		return e.emitTuiSpinner(args, pos)
	case "Progress":
		return e.emitTuiProgress(args, pos)
	case "render":
		return e.emitTuiRender(args, pos)
	case "enter":
		return e.emitTuiVoidCall("__kml_tui_enter", "enter", args, pos)
	case "leave":
		return e.emitTuiVoidCall("__kml_tui_leave", "leave", args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: klain:tui has no member '%s'", pos.Line, pos.Col, member)
}

// emitTuiNode allocates a node of the given kind and returns its handle reg.
func (e *Emitter) emitTuiNode(kind int) string {
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_tui_node(i32 %d)", r, kind))
	return r
}

// emitTuiPtrArrayLoop resolves a runtime array of ptr-typed elements (strings
// for List items, node handles for Box children) and emits a loop calling
// fn(node, elem) for each — the non-literal fallback so `items.map(...)` and any
// dynamically-built array work, not just inline array literals.
func (e *Emitter) emitTuiPtrArrayLoop(node string, arrExpr ast.Expression, fn string, pos ast.Pos) error {
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(arrExpr, pos)
	if err != nil {
		return err
	}
	if elemTy.IR != "ptr" {
		return fmt.Errorf("%d:%d: klain:tui expects an array of strings/nodes here", pos.Line, pos.Col)
	}
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))
	condL := e.freshLabel("tui.cond")
	bodyL := e.freshLabel("tui.body")
	doneL := e.freshLabel("tui.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))
	e.emitLabel(bodyL)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.IR, ptrReg, idxVal))
	elem := e.loadArrayElem(gep, elemTy)
	e.emitInstr(fmt.Sprintf("call void @%s(ptr %s, ptr %s)", fn, node, elem.Ref))
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
	return nil
}

func (e *Emitter) emitTuiVoidCall(fn, name string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: klain:tui %s() takes no arguments", pos.Line, pos.Col, name)
	}
	e.emitInstr(fmt.Sprintf("call void @%s()", fn))
	return Value{Ty: TypeVoid}, nil
}

// emitTuiRender lays out and paints a root node.
func (e *Emitter) emitTuiRender(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: klain:tui render(root) takes exactly 1 argument", pos.Line, pos.Col)
	}
	root, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	root = e.coerce(root, TypePtr)
	e.emitInstr(fmt.Sprintf("call void @__kml_tui_render(ptr %s)", root.Ref))
	return Value{Ty: TypeVoid}, nil
}

// emitTuiBox builds a flex container: Box(props?, children?).
func (e *Emitter) emitTuiBox(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: klain:tui Box(props?, children?) takes at most 2 arguments", pos.Line, pos.Col)
	}
	node := e.emitTuiNode(tuiBox)
	if len(args) >= 1 {
		if err := e.applyTuiProps(node, args[0], pos); err != nil {
			return Value{}, err
		}
	}
	if len(args) == 2 {
		if arr, ok := args[1].(*ast.ArrayLiteral); ok {
			for _, el := range arr.Elements {
				child, err := e.emitExpr(el)
				if err != nil {
					return Value{}, err
				}
				child = e.coerce(child, TypePtr)
				e.emitInstr(fmt.Sprintf("call void @__kml_tui_insert(ptr %s, ptr %s)", node, child.Ref))
			}
		} else if err := e.emitTuiPtrArrayLoop(node, args[1], "__kml_tui_insert", pos); err != nil {
			return Value{}, err
		}
	}
	return Value{Ref: node, Ty: TypePtr}, nil
}

// emitTuiLeafText builds a Text or TextInput node from a string first argument.
func (e *Emitter) emitTuiLeafText(kind int, name string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: klain:tui %s(text, props?) takes 1 or 2 arguments", pos.Line, pos.Col, name)
	}
	node := e.emitTuiNode(kind)
	sv, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	sv = e.coerce(sv, TypePtr)
	// Text wraps by default; TextInput is a single clipped line.
	wrap := 0
	if kind == tuiText {
		wrap = 1
	}
	if len(args) == 2 {
		if w, ok, err := e.tuiBoolProp(args[1], "wrap"); err != nil {
			return Value{}, err
		} else if ok {
			if w {
				wrap = 1
			} else {
				wrap = 0
			}
		}
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_tui_set_text(ptr %s, ptr %s, i32 %d)", node, sv.Ref, wrap))
	if len(args) == 2 {
		if err := e.applyTuiProps(node, args[1], pos); err != nil {
			return Value{}, err
		}
	}
	return Value{Ref: node, Ty: TypePtr}, nil
}

// emitTuiList builds a List(items, props?) from an array literal of strings.
func (e *Emitter) emitTuiList(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: klain:tui List(items, props?) takes 1 or 2 arguments", pos.Line, pos.Col)
	}
	node := e.emitTuiNode(tuiList)
	if arr, ok := args[0].(*ast.ArrayLiteral); ok {
		for _, el := range arr.Elements {
			sv, err := e.emitExpr(el)
			if err != nil {
				return Value{}, err
			}
			sv = e.coerce(sv, TypePtr)
			e.emitInstr(fmt.Sprintf("call void @__kml_tui_add_item(ptr %s, ptr %s)", node, sv.Ref))
		}
	} else if err := e.emitTuiPtrArrayLoop(node, args[0], "__kml_tui_add_item", pos); err != nil {
		return Value{}, err
	}
	if len(args) == 2 {
		if sel, ok, err := e.tuiNumProp(args[1], "selected"); err != nil {
			return Value{}, err
		} else if ok {
			si := e.coerce(sel, TypeI64)
			t := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", t, si.Ref))
			e.emitInstr(fmt.Sprintf("call void @__kml_tui_set_selected(ptr %s, i32 %s)", node, t))
		}
		if err := e.applyTuiProps(node, args[1], pos); err != nil {
			return Value{}, err
		}
	}
	return Value{Ref: node, Ty: TypePtr}, nil
}

// emitTuiSpinner builds Spinner(frame, props?); props.label is an optional caption.
func (e *Emitter) emitTuiSpinner(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: klain:tui Spinner(frame, props?) takes 1 or 2 arguments", pos.Line, pos.Col)
	}
	node := e.emitTuiNode(tuiSpinner)
	fv, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	fi := e.coerce(fv, TypeI64)
	t := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", t, fi.Ref))
	e.emitInstr(fmt.Sprintf("call void @__kml_tui_set_spinner(ptr %s, i32 %s)", node, t))
	if len(args) == 2 {
		if lbl, ok, err := e.tuiStrProp(args[1], "label"); err != nil {
			return Value{}, err
		} else if ok {
			e.emitInstr(fmt.Sprintf("call void @__kml_tui_set_text(ptr %s, ptr %s, i32 0)", node, lbl.Ref))
		}
		if err := e.applyTuiProps(node, args[1], pos); err != nil {
			return Value{}, err
		}
	}
	return Value{Ref: node, Ty: TypePtr}, nil
}

// emitTuiProgress builds Progress(value, props?); value is 0..1.
func (e *Emitter) emitTuiProgress(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: klain:tui Progress(value, props?) takes 1 or 2 arguments", pos.Line, pos.Col)
	}
	node := e.emitTuiNode(tuiProgress)
	vv, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	vv = e.coerce(vv, TypeF64)
	e.emitInstr(fmt.Sprintf("call void @__kml_tui_set_progress(ptr %s, double %s)", node, vv.Ref))
	if len(args) == 2 {
		if err := e.applyTuiProps(node, args[1], pos); err != nil {
			return Value{}, err
		}
	}
	return Value{Ref: node, Ty: TypePtr}, nil
}
