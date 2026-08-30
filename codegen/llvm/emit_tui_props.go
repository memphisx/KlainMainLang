package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emit_tui_props.go — compile-time parsing of a klain:tui builder's style-prop
// object literal into Yoga/paint setter calls (TDD-00150 Stage 1).
//
// Enum/colour/border/boolean props must be literals (resolved to their integer
// form at compile time); numeric props (sizes, padding, margin, gap) may be
// arbitrary expressions, evaluated to doubles with a NaN "unset" sentinel.

// tuiFgColors maps a colour name to its ANSI foreground SGR number.
var tuiFgColors = map[string]int{
	"black": 30, "red": 31, "green": 32, "yellow": 33, "blue": 34,
	"magenta": 35, "cyan": 36, "white": 37, "default": -1,
	"gray": 90, "grey": 90, "brightblack": 90,
	"brightred": 91, "brightgreen": 92, "brightyellow": 93, "brightblue": 94,
	"brightmagenta": 95, "brightcyan": 96, "brightwhite": 97,
}

// tuiBgColors maps a colour name to its ANSI background SGR number.
var tuiBgColors = map[string]int{
	"black": 40, "red": 41, "green": 42, "yellow": 43, "blue": 44,
	"magenta": 45, "cyan": 46, "white": 47, "default": -1,
	"gray": 100, "grey": 100, "brightblack": 100,
	"brightred": 101, "brightgreen": 102, "brightyellow": 103, "brightblue": 104,
	"brightmagenta": 105, "brightcyan": 106, "brightwhite": 107,
}

var tuiJustify = map[string]int{
	"flex-start": 1, "center": 2, "flex-end": 3,
	"space-between": 4, "space-around": 5, "space-evenly": 6,
}
var tuiAlign = map[string]int{
	"auto": 0, "flex-start": 1, "center": 2, "flex-end": 3, "stretch": 4, "baseline": 5,
}
var tuiBorder = map[string]int{"none": 0, "single": 1, "round": 2, "double": 3}

// knownTuiProps is the full recognized prop set — an unknown key is a hard
// error so a typo (`colr:`) fails at compile time instead of silently no-op'ing.
var knownTuiProps = map[string]bool{
	"width": true, "height": true, "minWidth": true, "minHeight": true,
	"flexDirection": true, "flexGrow": true, "flexShrink": true, "flexBasis": true,
	"justifyContent": true, "alignItems": true, "alignSelf": true, "flexWrap": true,
	"padding": true, "paddingX": true, "paddingY": true,
	"paddingTop": true, "paddingRight": true, "paddingBottom": true, "paddingLeft": true,
	"margin": true, "marginX": true, "marginY": true,
	"marginTop": true, "marginRight": true, "marginBottom": true, "marginLeft": true,
	"gap": true, "color": true, "backgroundColor": true, "borderColor": true,
	"border": true, "bold": true, "dim": true, "underline": true, "inverse": true,
	// consumed by the specific builder, not applyTuiProps, but valid keys:
	"wrap": true, "label": true, "selected": true,
}

// tuiObjLit narrows a props argument to a static object literal.
func tuiObjLit(expr ast.Expression, pos ast.Pos) (*ast.ObjectLiteral, map[string]ast.Expression, error) {
	obj, ok := expr.(*ast.ObjectLiteral)
	if !ok {
		return nil, nil, fmt.Errorf("%d:%d: klain:tui props must be an object literal", pos.Line, pos.Col)
	}
	m := make(map[string]ast.Expression, len(obj.Properties))
	for _, p := range obj.Properties {
		if p.KeyExpr != nil || (p.Key == "" && p.Value != nil) {
			return nil, nil, fmt.Errorf("%d:%d: klain:tui props do not support computed keys or spreads", pos.Line, pos.Col)
		}
		if !knownTuiProps[p.Key] {
			return nil, nil, fmt.Errorf("%d:%d: klain:tui: unknown style prop '%s'", pos.Line, pos.Col, p.Key)
		}
		m[p.Key] = p.Value
	}
	return obj, m, nil
}

// tuiBoolProp reads an optional boolean-literal prop.
func (e *Emitter) tuiBoolProp(expr ast.Expression, key string) (bool, bool, error) {
	_, m, err := tuiObjLit(expr, expr.GetPos())
	if err != nil {
		return false, false, err
	}
	v, ok := m[key]
	if !ok {
		return false, false, nil
	}
	b, ok := v.(*ast.BooleanLiteral)
	if !ok {
		return false, false, fmt.Errorf("%d:%d: klain:tui '%s' must be a boolean literal", expr.GetPos().Line, expr.GetPos().Col, key)
	}
	return b.Value, true, nil
}

// tuiNumProp reads an optional numeric prop (an arbitrary expression).
func (e *Emitter) tuiNumProp(expr ast.Expression, key string) (Value, bool, error) {
	_, m, err := tuiObjLit(expr, expr.GetPos())
	if err != nil {
		return Value{}, false, err
	}
	v, ok := m[key]
	if !ok {
		return Value{}, false, nil
	}
	val, err := e.emitExpr(v)
	if err != nil {
		return Value{}, false, err
	}
	return val, true, nil
}

// tuiStrProp reads an optional string prop (an arbitrary expression), returning
// it coerced to a ptr.
func (e *Emitter) tuiStrProp(expr ast.Expression, key string) (Value, bool, error) {
	_, m, err := tuiObjLit(expr, expr.GetPos())
	if err != nil {
		return Value{}, false, err
	}
	v, ok := m[key]
	if !ok {
		return Value{}, false, nil
	}
	val, err := e.emitExpr(v)
	if err != nil {
		return Value{}, false, err
	}
	return e.coerce(val, TypePtr), true, nil
}

// tuiEnumProp resolves a string-literal enum prop to its integer code.
func tuiEnumProp(m map[string]ast.Expression, key string, table map[string]int, pos ast.Pos) (int, bool, error) {
	v, ok := m[key]
	if !ok {
		return 0, false, nil
	}
	s, ok := v.(*ast.StringLiteral)
	if !ok {
		return 0, false, fmt.Errorf("%d:%d: klain:tui '%s' must be a string literal", pos.Line, pos.Col, key)
	}
	code, ok := table[s.Value]
	if !ok {
		return 0, false, fmt.Errorf("%d:%d: klain:tui '%s': unrecognized value '%s'", pos.Line, pos.Col, key, s.Value)
	}
	return code, true, nil
}

// tuiDbl evaluates a numeric prop expr to a double operand (register/immediate).
func (e *Emitter) tuiDbl(expr ast.Expression) (string, error) {
	v, err := e.emitExpr(expr)
	if err != nil {
		return "", err
	}
	return e.coerce(v, TypeF64).Ref, nil
}

// edgeOperands resolves the four edges (top,right,bottom,left) for a
// padding/margin group with general/axis/specific precedence. Returns the four
// double operands (nanIR for unset) and whether any edge was set.
func (e *Emitter) edgeOperands(m map[string]ast.Expression, prefix string) (top, right, bot, left string, any bool, err error) {
	top, right, bot, left = nanIR, nanIR, nanIR, nanIR
	set := func(dst *string, expr ast.Expression) error {
		op, err := e.tuiDbl(expr)
		if err != nil {
			return err
		}
		*dst = op
		any = true
		return nil
	}
	if v, ok := m[prefix]; ok { // all edges
		op, err := e.tuiDbl(v)
		if err != nil {
			return "", "", "", "", false, err
		}
		top, right, bot, left, any = op, op, op, op, true
	}
	if v, ok := m[prefix+"X"]; ok {
		op, err := e.tuiDbl(v)
		if err != nil {
			return "", "", "", "", false, err
		}
		left, right, any = op, op, true
	}
	if v, ok := m[prefix+"Y"]; ok {
		op, err := e.tuiDbl(v)
		if err != nil {
			return "", "", "", "", false, err
		}
		top, bot, any = op, op, true
	}
	if v, ok := m[prefix+"Top"]; ok {
		if err := set(&top, v); err != nil {
			return "", "", "", "", false, err
		}
	}
	if v, ok := m[prefix+"Right"]; ok {
		if err := set(&right, v); err != nil {
			return "", "", "", "", false, err
		}
	}
	if v, ok := m[prefix+"Bottom"]; ok {
		if err := set(&bot, v); err != nil {
			return "", "", "", "", false, err
		}
	}
	if v, ok := m[prefix+"Left"]; ok {
		if err := set(&left, v); err != nil {
			return "", "", "", "", false, err
		}
	}
	return top, right, bot, left, any, nil
}

// applyTuiProps evaluates a style-prop object literal and emits the setter calls.
func (e *Emitter) applyTuiProps(node string, propsExpr ast.Expression, pos ast.Pos) error {
	if propsExpr == nil {
		return nil
	}
	_, m, err := tuiObjLit(propsExpr, pos)
	if err != nil {
		return err
	}

	// size / min-size
	if _, hw := m["width"]; hw || m["height"] != nil {
		w, h := nanIR, nanIR
		if v, ok := m["width"]; ok {
			if w, err = e.tuiDbl(v); err != nil {
				return err
			}
		}
		if v, ok := m["height"]; ok {
			if h, err = e.tuiDbl(v); err != nil {
				return err
			}
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_tui_set_size(ptr %s, double %s, double %s)", node, w, h))
	}
	if _, a := m["minWidth"]; a || m["minHeight"] != nil {
		w, h := nanIR, nanIR
		if v, ok := m["minWidth"]; ok {
			if w, err = e.tuiDbl(v); err != nil {
				return err
			}
		}
		if v, ok := m["minHeight"]; ok {
			if h, err = e.tuiDbl(v); err != nil {
				return err
			}
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_tui_set_min(ptr %s, double %s, double %s)", node, w, h))
	}

	// flex (direction/grow/shrink/basis) — emitted together so C can apply the
	// direction only when present (dir=-1 leaves Yoga's default).
	_, hasDir := m["flexDirection"]
	_, hasGrow := m["flexGrow"]
	_, hasShrink := m["flexShrink"]
	_, hasBasis := m["flexBasis"]
	if hasDir || hasGrow || hasShrink || hasBasis {
		dir := -1
		if hasDir {
			d, _, derr := tuiEnumProp(m, "flexDirection", map[string]int{"row": 0, "column": 1}, pos)
			if derr != nil {
				return derr
			}
			dir = d
		}
		grow, shrink, basis := nanIR, nanIR, nanIR
		if v, ok := m["flexGrow"]; ok {
			if grow, err = e.tuiDbl(v); err != nil {
				return err
			}
		}
		if v, ok := m["flexShrink"]; ok {
			if shrink, err = e.tuiDbl(v); err != nil {
				return err
			}
		}
		if v, ok := m["flexBasis"]; ok {
			if basis, err = e.tuiDbl(v); err != nil {
				return err
			}
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_tui_set_flex(ptr %s, i32 %d, double %s, double %s, double %s)", node, dir, grow, shrink, basis))
	}

	if code, ok, jerr := tuiEnumProp(m, "justifyContent", tuiJustify, pos); jerr != nil {
		return jerr
	} else if ok {
		e.emitInstr(fmt.Sprintf("call void @__kml_tui_set_justify(ptr %s, i32 %d)", node, code))
	}
	if code, ok, aerr := tuiEnumProp(m, "alignItems", tuiAlign, pos); aerr != nil {
		return aerr
	} else if ok {
		e.emitInstr(fmt.Sprintf("call void @__kml_tui_set_align(ptr %s, i32 %d)", node, code))
	}
	if code, ok, aerr := tuiEnumProp(m, "alignSelf", tuiAlign, pos); aerr != nil {
		return aerr
	} else if ok {
		e.emitInstr(fmt.Sprintf("call void @__kml_tui_set_self(ptr %s, i32 %d)", node, code))
	}
	if _, ok := m["flexWrap"]; ok {
		w, _, werr := tuiEnumProp(m, "flexWrap", map[string]int{"nowrap": 0, "wrap": 1}, pos)
		if werr != nil {
			return werr
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_tui_set_wrap(ptr %s, i32 %d)", node, w))
	}

	// padding / margin edge groups
	if t, r, b, l, any, perr := e.edgeOperands(m, "padding"); perr != nil {
		return perr
	} else if any {
		e.emitInstr(fmt.Sprintf("call void @__kml_tui_set_padding(ptr %s, double %s, double %s, double %s, double %s)", node, t, r, b, l))
	}
	if t, r, b, l, any, merr := e.edgeOperands(m, "margin"); merr != nil {
		return merr
	} else if any {
		e.emitInstr(fmt.Sprintf("call void @__kml_tui_set_margin(ptr %s, double %s, double %s, double %s, double %s)", node, t, r, b, l))
	}
	if v, ok := m["gap"]; ok {
		op, gerr := e.tuiDbl(v)
		if gerr != nil {
			return gerr
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_tui_set_gap(ptr %s, double %s)", node, op))
	}

	// colours
	_, hasFg := m["color"]
	_, hasBg := m["backgroundColor"]
	if hasFg || hasBg {
		fg, bg := -1, -1
		if code, ok, cerr := tuiEnumProp(m, "color", tuiFgColors, pos); cerr != nil {
			return cerr
		} else if ok {
			fg = code
		}
		if code, ok, cerr := tuiEnumProp(m, "backgroundColor", tuiBgColors, pos); cerr != nil {
			return cerr
		} else if ok {
			bg = code
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_tui_set_colors(ptr %s, i32 %d, i32 %d)", node, fg, bg))
	}

	// border (style string or boolean) + border colour
	if v, ok := m["border"]; ok {
		style := 0
		switch b := v.(type) {
		case *ast.BooleanLiteral:
			if b.Value {
				style = 1
			}
		case *ast.StringLiteral:
			s, sok := tuiBorder[b.Value]
			if !sok {
				return fmt.Errorf("%d:%d: klain:tui 'border': unrecognized style '%s'", pos.Line, pos.Col, b.Value)
			}
			style = s
		default:
			return fmt.Errorf("%d:%d: klain:tui 'border' must be a string or boolean literal", pos.Line, pos.Col)
		}
		bc := -1
		if code, ok, cerr := tuiEnumProp(m, "borderColor", tuiFgColors, pos); cerr != nil {
			return cerr
		} else if ok {
			bc = code
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_tui_set_border(ptr %s, i32 %d, i32 %d)", node, style, bc))
	}

	// text attributes (bold/dim/underline/inverse) collapsed to one bitmask
	attr := 0
	for key, bit := range map[string]int{"bold": 1, "dim": 2, "underline": 4, "inverse": 8} {
		if v, ok := m[key]; ok {
			b, bok := v.(*ast.BooleanLiteral)
			if !bok {
				return fmt.Errorf("%d:%d: klain:tui '%s' must be a boolean literal", pos.Line, pos.Col, key)
			}
			if b.Value {
				attr |= bit
			}
		}
	}
	if attr != 0 {
		e.emitInstr(fmt.Sprintf("call void @__kml_tui_set_attr(ptr %s, i32 %d)", node, attr))
	}
	return nil
}
