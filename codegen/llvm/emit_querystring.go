// emit_querystring.go — Node's `querystring` core module: legacy
// "a=b&c=d" parse/stringify. Largely superseded in real Node by
// URLSearchParams (see docs/status/URL.md), which is exactly why this is
// cheap here too — parse and stringify both wrap machinery URLSearchParams
// and http.listen's req.query already share: __kml_http_parse_query
// (percent-decoding parse, runtime_http.go) and emitMapStrToQueryString
// (percent-encoding join, emit_url.go).
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// emitQuerystringParse implements querystring.parse(str): an ordinary
// Map<string,string>, the same shape req.query and URLSearchParams's own
// internal map already use. Unlike `new URLSearchParams(str)`, a leading
// '?' is NOT stripped — matching real Node's querystring.parse, which
// treats it as plain text at the start of the first key.
func (e *Emitter) emitQuerystringParse(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: querystring.parse takes exactly 1 argument", pos.Line, pos.Col)
	}
	e.ensureMapStrHelpers()
	e.ensureHTTPParseQuery()

	strVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	strVal = e.coerce(strVal, TypePtr)

	mapPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", mapPtr))
	e.emitInstr(fmt.Sprintf("call void @__kml_http_parse_query(ptr %s, ptr %s)", strVal.Ref, mapPtr))

	return Value{Ref: mapPtr, Ty: MapType(TypePtr, TypePtr)}, nil
}

// emitQuerystringStringify implements querystring.stringify(obj): the
// reverse of parse, over a Map<string,string> — shares
// emitMapStrToQueryString with URLSearchParams.toString() (emit_url.go).
func (e *Emitter) emitQuerystringStringify(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: querystring.stringify takes exactly 1 argument", pos.Line, pos.Col)
	}
	if objTy := e.inferExprType(args[0]); !objTy.IsMap {
		return Value{}, fmt.Errorf("%d:%d: querystring.stringify's argument must be a Map<string,string>", pos.Line, pos.Col)
	}
	_, mapPtr, err := e.resolveMapOrSetForCall(args[0], pos)
	if err != nil {
		return Value{}, err
	}
	return e.emitMapStrToQueryString(mapPtr)
}
