// emit_console_format.go — console.log's printf-style specifiers: the
// conversion of one argument for %s/%d/%i/%f/%j/%o/%O (util.format is
// lib/node/util.ts).
package llvm

import (
	"KlainMainLang/ast"
)

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
