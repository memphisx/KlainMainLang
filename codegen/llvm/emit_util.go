// emit_util.go — codegen for Node's `util`: util.inspect and util.format.
// Both reuse existing machinery — the object/value inspector (emit_inspect.go,
// already driving console.log) and JSON.stringify (emit_call_json.go) — so
// this module is pure surface, no runtime helpers of its own.
//
// util.promisify is deliberately not implemented: the codegen's callback APIs
// (zlib.gzip, child_process.exec) are name-dispatched call expressions, not
// first-class function values that could be handed to promisify(fn); wrapping
// them would need per-builtin special-casing. Omitted from virtualModuleMembers
// so an import of it fails cleanly. See docs/adr/ADR-00325.md.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitUtilModuleCall dispatches util.inspect / util.format.
func (e *Emitter) emitUtilModuleCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch method {
	case "inspect":
		return e.emitUtilInspect(args, pos)
	case "format":
		return e.emitUtilFormat(args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: util.%s is not supported", pos.Line, pos.Col, method)
}

// emitUtilInspect implements util.inspect(value): the same field-formatting the
// inspector produces inside an object (strings single-quoted, `ClassName { … }`,
// arrays `[ … ]`, bigints `10n`), applied at the top level. Any options object
// is accepted but ignored in V1.
func (e *Emitter) emitUtilInspect(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: util.inspect takes (value, options?)", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	return e.emitInspectField(val, 0)
}

// emitUtilFormat implements util.format(format, ...args): printf-style
// substitution over a string-literal format. %s → String(arg), %d → Number(arg)
// (as-is, no truncation), %i → integer (truncated), %f → float, %c → consumes
// its arg and emits nothing (CSS directive), %j → JSON.stringify(arg),
// %o/%O → util.inspect(arg), %% → literal
// %. Excess args are appended space-separated; excess specifiers are left
// literal (matching Node). A non-literal format string is rejected in V1.
func (e *Emitter) emitUtilFormat(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) == 0 {
		return Value{Ref: e.internString(""), Ty: TypePtr}, nil
	}
	lit, ok := args[0].(*ast.StringLiteral)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: util.format requires a string-literal format string in V1", pos.Line, pos.Col)
	}

	// Accumulate the result as a growing string Value, starting empty.
	acc := Value{Ref: e.internString(""), Ty: TypePtr}
	appendStr := func(s Value) error {
		r, err := e.emitStringConcat(acc, s)
		if err != nil {
			return err
		}
		acc = r
		return nil
	}
	appendLit := func(s string) error { return appendStr(Value{Ref: e.internString(s), Ty: TypePtr}) }

	f := lit.Value
	argIdx := 1 // args[0] is the format string
	var pending []byte
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		s := string(pending)
		pending = pending[:0]
		return appendLit(s)
	}

	for i := 0; i < len(f); i++ {
		if f[i] != '%' || i+1 >= len(f) {
			pending = append(pending, f[i])
			continue
		}
		verb := f[i+1]
		if verb == '%' {
			pending = append(pending, '%')
			i++
			continue
		}
		if verb != 's' && verb != 'd' && verb != 'i' && verb != 'f' && verb != 'j' && verb != 'o' && verb != 'O' && verb != 'c' {
			// Not a recognized specifier — emit the '%' literally.
			pending = append(pending, '%')
			continue
		}
		if argIdx >= len(args) {
			// No arg to consume — Node leaves the specifier literal.
			pending = append(pending, '%', verb)
			i++
			continue
		}
		if verb == 'c' {
			// Node's %c is a CSS-styling directive: it consumes its argument
			// and emits nothing. Evaluate for side effects, then discard.
			if _, err := e.emitExpr(args[argIdx]); err != nil {
				return Value{}, err
			}
			argIdx++
			i++
			continue
		}
		if err := flush(); err != nil {
			return Value{}, err
		}
		conv, err := e.emitUtilFormatArg(verb, args[argIdx], pos)
		if err != nil {
			return Value{}, err
		}
		if err := appendStr(conv); err != nil {
			return Value{}, err
		}
		argIdx++
		i++
	}
	if err := flush(); err != nil {
		return Value{}, err
	}

	// Excess args: append space-separated, bare-formatted (String()-style).
	for ; argIdx < len(args); argIdx++ {
		if err := appendLit(" "); err != nil {
			return Value{}, err
		}
		v, err := e.emitExpr(args[argIdx])
		if err != nil {
			return Value{}, err
		}
		s, err := e.emitValueToString(v)
		if err != nil {
			return Value{}, err
		}
		if err := appendStr(s); err != nil {
			return Value{}, err
		}
	}
	return acc, nil
}

// emitUtilFormatArg converts one argument for a format specifier.
func (e *Emitter) emitUtilFormatArg(verb byte, arg ast.Expression, pos ast.Pos) (Value, error) {
	switch verb {
	case 'j':
		return e.emitJSONStringifyValueExpr(arg)
	case 'o', 'O':
		v, err := e.emitExpr(arg)
		if err != nil {
			return Value{}, err
		}
		return e.emitInspectField(v, 0)
	case 'i':
		// %i truncates toward integer.
		v, err := e.emitExpr(arg)
		if err != nil {
			return Value{}, err
		}
		iv := e.coerce(v, TypeI64)
		return e.emitValueToString(iv)
	case 'd':
		// %d formats the number as-is — it does NOT truncate a float
		// (Node: format('%d', 3.7) === '3.7'). Only %i truncates.
		v, err := e.emitExpr(arg)
		if err != nil {
			return Value{}, err
		}
		fv := e.coerce(v, TypeF64)
		return e.emitValueToString(fv)
	case 'f':
		v, err := e.emitExpr(arg)
		if err != nil {
			return Value{}, err
		}
		fv := e.coerce(v, TypeF64)
		return e.emitValueToString(fv)
	default: // 's'
		v, err := e.emitExpr(arg)
		if err != nil {
			return Value{}, err
		}
		return e.emitValueToString(v)
	}
}

// emitJSONStringifyValueExpr evaluates arg and JSON-stringifies the value
// (util.format %j), reusing the value-based stringifier.
func (e *Emitter) emitJSONStringifyValueExpr(arg ast.Expression) (Value, error) {
	v, err := e.emitExpr(arg)
	if err != nil {
		return Value{}, err
	}
	return e.emitJSONStringifyValue(v, jsonIndent{})
}
