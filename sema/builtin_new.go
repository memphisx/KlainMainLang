package sema

import (
	"fmt"

	"KlainMainLang/ast"
)

// A builder turns a generic `new X<T…>(args)` whose X resolves to the global
// builtin into the node kind codegen lowers that builtin through. The parser
// knows no builtin names; this table is the one place they are recognised,
// and it is deleted when phase 3 of TDD-00230 describes the builtins as
// declarations.
type builder func(e *ast.NewExpression) (ast.Expression, error)

var builtinConstructors = map[string]builder{
	"Array":               buildArray,
	"Map":                 buildMap,
	"Set":                 buildSet,
	"WeakMap":             buildWeakMap,
	"WeakSet":             buildWeakSet,
	"WeakRef":             buildWeakRef,
	"EventEmitter":        buildEventEmitter,
	"ReadableStream":      buildReadableStream,
	"WritableStream":      buildWritableStream,
	"TransformStream":     buildTransformStream,
	"Agent":               optionsOnly(func(o ast.Expression, p ast.Pos) ast.Expression { return ast.NewNewHTTPAgentExpression(o, p) }),
	"Webview":             optionsOnly(func(o ast.Expression, p ast.Pos) ast.Expression { return ast.NewNewWebviewExpression(o, p) }),
	"DatabaseSync":        buildDatabaseSync,
	"CompressionStream":   compressionStream(false),
	"DecompressionStream": compressionStream(true),
	"Error":               errorKind("Error"),
	"TypeError":           errorKind("TypeError"),
	"RangeError":          errorKind("RangeError"),
	"SyntaxError":         errorKind("SyntaxError"),
	"EvalError":           errorKind("EvalError"),
	"URIError":            errorKind("URIError"),
	"ReferenceError":      errorKind("ReferenceError"),
	"DOMException":        buildDOMException,
	"AggregateError":      buildAggregateError,
	"Date":                buildDate,
	"URL":                 buildURL,
	"EventSource":         oneArg("EventSource(url)", func(a ast.Expression, p ast.Pos) ast.Expression { return ast.NewNewEventSourceExpression(a, p) }),
	"EventTarget":         noArgs("EventTarget", func(p ast.Pos) ast.Expression { return ast.NewNewEventTargetExpression(p) }),
	"AbortController":     noArgs("AbortController", func(p ast.Pos) ast.Expression { return ast.NewNewAbortControllerExpression(p) }),
	"Event":               buildEvent,
	"CustomEvent":         buildCustomEvent,
	"WebSocket":           oneArg("WebSocket(url)", func(a ast.Expression, p ast.Pos) ast.Expression { return ast.NewNewWebSocketExpression(a, p) }),
	"Worker":              buildWorker,
	"URLSearchParams":     optionsOnly(func(o ast.Expression, p ast.Pos) ast.Expression { return ast.NewNewURLSearchParamsExpression(o, p) }),
	"URLPattern":          buildURLPattern,
	"Headers":             optionsOnly(func(o ast.Expression, p ast.Pos) ast.Expression { return ast.NewNewHeadersExpression(o, p) }),
	"Request":             buildRequest,
	"XMLHttpRequest":      noArgs("XMLHttpRequest", func(p ast.Pos) ast.Expression { return ast.NewNewXMLHttpRequestExpression(p) }),
	"ArrayBuffer":         arrayBuffer(false),
	"SharedArrayBuffer":   arrayBuffer(true),
	"BroadcastChannel":    buildBroadcastChannel,
	"MessageChannel":      buildMessageChannel,
	"Channel":             buildChannel,
	"DataView":            buildDataView,
	"TextEncoder":         noArgs("TextEncoder", func(p ast.Pos) ast.Expression { return ast.NewNewTextEncoderExpression(p) }),
	"TextDecoder":         optionsOnly(func(o ast.Expression, p ast.Pos) ast.Expression { return ast.NewNewTextDecoderExpression(o, p) }),
	"RegExp":              buildRegExp,
	"Blob":                buildBlob,
}

func init() {
	for name, kind := range typedArrayElemKinds {
		builtinConstructors[name] = typedArray(kind)
	}
}

// typedArrayElemKinds maps each supported TypedArray constructor to the element
// kind codegen resolves into a concrete element type.
var typedArrayElemKinds = map[string]string{
	"Int8Array":         "int8",
	"Uint8Array":        "uint8",
	"Uint8ClampedArray": "uint8clamped",
	"Int16Array":        "int16",
	"Uint16Array":       "uint16",
	"Int32Array":        "int32",
	"Uint32Array":       "uint32",
	"Float32Array":      "float32",
	"Float64Array":      "float64",
	"BigInt64Array":     "bigint64",
	"BigUint64Array":    "biguint64",
}

func errAt(p ast.Pos, format string, args ...any) error {
	return fmt.Errorf("%d:%d: "+format, append([]any{p.Line, p.Col}, args...)...)
}

// arity checks the argument count is within [lo, hi].
func arity(e *ast.NewExpression, sig string, lo, hi int) error {
	if n := len(e.Args); n < lo || n > hi {
		switch {
		case lo == hi && lo == 0:
			return errAt(e.GetPos(), "new %s() does not accept arguments", sig)
		case lo == hi:
			return errAt(e.GetPos(), "new %s takes %d argument(s), got %d", sig, lo, n)
		default:
			return errAt(e.GetPos(), "new %s takes %d to %d arguments, got %d", sig, lo, hi, n)
		}
	}
	return nil
}

func arg(e *ast.NewExpression, i int) ast.Expression {
	if i < len(e.Args) {
		return e.Args[i]
	}
	return nil
}

func typeArg(e *ast.NewExpression, i int) *ast.TypeAnnotation {
	if i < len(e.TypeArgs) {
		return e.TypeArgs[i]
	}
	return nil
}

func noArgs(name string, mk func(ast.Pos) ast.Expression) builder {
	return func(e *ast.NewExpression) (ast.Expression, error) {
		if err := arity(e, name, 0, 0); err != nil {
			return nil, err
		}
		return mk(e.GetPos()), nil
	}
}

func oneArg(sig string, mk func(ast.Expression, ast.Pos) ast.Expression) builder {
	return func(e *ast.NewExpression) (ast.Expression, error) {
		if err := arity(e, sig, 1, 1); err != nil {
			return nil, err
		}
		return mk(e.Args[0], e.GetPos()), nil
	}
}

func optionsOnly(mk func(ast.Expression, ast.Pos) ast.Expression) builder {
	return func(e *ast.NewExpression) (ast.Expression, error) {
		if err := arity(e, e.ClassName+"(options?)", 0, 1); err != nil {
			return nil, err
		}
		return mk(arg(e, 0), e.GetPos()), nil
	}
}

func buildArray(e *ast.NewExpression) (ast.Expression, error) {
	switch len(e.Args) {
	case 0:
		// `new Array<T>()` is an empty array (ADR-00463).
		return ast.NewNewArrayExpression(typeArg(e, 0), ast.NewNumberLiteral("0", e.GetPos()), e.GetPos()), nil
	case 1:
		return ast.NewNewArrayExpression(typeArg(e, 0), e.Args[0], e.GetPos()), nil
	}
	// `new Array(a, b, …)` is an array of those elements, as in JavaScript.
	return ast.NewArrayLiteral(e.Args, e.GetPos()), nil
}

func buildMap(e *ast.NewExpression) (ast.Expression, error) {
	if err := arity(e, "Map(entries?)", 0, 1); err != nil {
		return nil, err
	}
	return ast.NewNewMapExpression(typeArg(e, 0), typeArg(e, 1), arg(e, 0), e.GetPos()), nil
}

func buildSet(e *ast.NewExpression) (ast.Expression, error) {
	if err := arity(e, "Set(values?)", 0, 1); err != nil {
		return nil, err
	}
	return ast.NewNewSetExpression(typeArg(e, 0), arg(e, 0), e.GetPos()), nil
}

func buildWeakMap(e *ast.NewExpression) (ast.Expression, error) {
	if err := arity(e, "WeakMap", 0, 0); err != nil {
		return nil, err
	}
	return ast.NewNewWeakMapExpression(typeArg(e, 0), typeArg(e, 1), e.GetPos()), nil
}

func buildWeakSet(e *ast.NewExpression) (ast.Expression, error) {
	if err := arity(e, "WeakSet", 0, 0); err != nil {
		return nil, err
	}
	return ast.NewNewWeakSetExpression(typeArg(e, 0), e.GetPos()), nil
}

func buildWeakRef(e *ast.NewExpression) (ast.Expression, error) {
	if err := arity(e, "WeakRef(target)", 1, 1); err != nil {
		return nil, err
	}
	return ast.NewNewWeakRefExpression(typeArg(e, 0), e.Args[0], e.GetPos()), nil
}

func buildEventEmitter(e *ast.NewExpression) (ast.Expression, error) {
	if err := arity(e, "EventEmitter", 0, 0); err != nil {
		return nil, err
	}
	return ast.NewNewEventEmitterExpression(typeArg(e, 0), e.GetPos()), nil
}

func buildReadableStream(e *ast.NewExpression) (ast.Expression, error) {
	if err := arity(e, "ReadableStream(source?, strategy?)", 0, 2); err != nil {
		return nil, err
	}
	return ast.NewNewReadableStreamExpression(typeArg(e, 0), arg(e, 0), arg(e, 1), e.GetPos()), nil
}

func buildWritableStream(e *ast.NewExpression) (ast.Expression, error) {
	if err := arity(e, "WritableStream(sink?, strategy?)", 0, 2); err != nil {
		return nil, err
	}
	return ast.NewNewWritableStreamExpression(typeArg(e, 0), arg(e, 0), arg(e, 1), e.GetPos()), nil
}

func buildTransformStream(e *ast.NewExpression) (ast.Expression, error) {
	if len(e.Args) > 3 {
		return nil, errAt(e.GetPos(), "new TransformStream takes at most 3 arguments")
	}
	in, out := typeArg(e, 0), typeArg(e, 1)
	if in != nil && out == nil {
		out = in
	}
	return ast.NewNewTransformStreamExpression(in, out, arg(e, 0), arg(e, 1), arg(e, 2), e.GetPos()), nil
}

func buildDatabaseSync(e *ast.NewExpression) (ast.Expression, error) {
	if err := arity(e, "DatabaseSync(path, options?)", 1, 2); err != nil {
		return nil, err
	}
	return ast.NewNewDatabaseSyncExpression(e.Args[0], arg(e, 1), e.GetPos()), nil
}

func compressionStream(decompress bool) builder {
	return func(e *ast.NewExpression) (ast.Expression, error) {
		if err := arity(e, e.ClassName+"(format)", 1, 1); err != nil {
			return nil, err
		}
		return ast.NewNewCompressionStreamExpression(decompress, e.Args[0], e.GetPos()), nil
	}
}

func errorKind(kind string) builder {
	return func(e *ast.NewExpression) (ast.Expression, error) {
		if err := arity(e, kind+"(message?, options?)", 0, 2); err != nil {
			return nil, err
		}
		ne := ast.NewNewErrorExpression(kind, arg(e, 0), e.GetPos())
		// The options bag must be an object literal whose sole member is
		// `cause` — the one member the runtime error shape carries.
		if opts := arg(e, 1); opts != nil {
			lit, ok := opts.(*ast.ObjectLiteral)
			if !ok || len(lit.Properties) != 1 || lit.Properties[0].Key != "cause" || lit.Properties[0].Value == nil {
				return nil, errAt(e.GetPos(), "new %s's second argument must be a `{ cause: <expr> }` object literal", kind)
			}
			ne.Cause = lit.Properties[0].Value
		}
		return ne, nil
	}
}

func buildDOMException(e *ast.NewExpression) (ast.Expression, error) {
	if err := arity(e, "DOMException(message?, name?)", 0, 2); err != nil {
		return nil, err
	}
	ne := ast.NewNewErrorExpression("DOMException", arg(e, 0), e.GetPos())
	ne.Name = arg(e, 1)
	return ne, nil
}

func buildAggregateError(e *ast.NewExpression) (ast.Expression, error) {
	if err := arity(e, "AggregateError(errors?, message?)", 0, 2); err != nil {
		return nil, err
	}
	ne := ast.NewNewErrorExpression("AggregateError", arg(e, 1), e.GetPos())
	ne.Errors = arg(e, 0)
	return ne, nil
}

func buildDate(e *ast.NewExpression) (ast.Expression, error) {
	switch n := len(e.Args); {
	case n == 0:
		return ast.NewNewDateExpression(nil, e.GetPos()), nil
	case n == 1:
		return ast.NewNewDateExpression(e.Args[0], e.GetPos()), nil
	case n > 7:
		return nil, errAt(e.GetPos(), "new Date(...) accepts at most 7 arguments (year, month, day, hours, minutes, seconds, milliseconds)")
	}
	return ast.NewNewDateExpressionMulti(e.Args, e.GetPos()), nil
}

func buildURL(e *ast.NewExpression) (ast.Expression, error) {
	if err := arity(e, "URL(url, base?)", 1, 2); err != nil {
		return nil, err
	}
	if base := arg(e, 1); base != nil {
		return ast.NewNewURLExpressionWithBase(e.Args[0], base, e.GetPos()), nil
	}
	return ast.NewNewURLExpression(e.Args[0], e.GetPos()), nil
}

// objectProp returns the value of property key in an object-literal argument.
func objectProp(ex ast.Expression, key string) ast.Expression {
	obj, ok := ex.(*ast.ObjectLiteral)
	if !ok {
		return nil
	}
	var v ast.Expression
	for _, p := range obj.Properties {
		if p.Key == key {
			v = p.Value
		}
	}
	return v
}

func buildEvent(e *ast.NewExpression) (ast.Expression, error) {
	if err := arity(e, "Event(type, init?)", 1, 2); err != nil {
		return nil, err
	}
	if c := objectProp(arg(e, 1), "cancelable"); c != nil {
		return ast.NewNewEventExpressionWithInit(e.Args[0], c, e.GetPos()), nil
	}
	return ast.NewNewEventExpression(e.Args[0], e.GetPos()), nil
}

func buildCustomEvent(e *ast.NewExpression) (ast.Expression, error) {
	if err := arity(e, "CustomEvent(type, init?)", 1, 2); err != nil {
		return nil, err
	}
	detail := objectProp(arg(e, 1), "detail")
	if c := objectProp(arg(e, 1), "cancelable"); c != nil {
		return ast.NewNewCustomEventExpressionWithInit(e.Args[0], detail, c, e.GetPos()), nil
	}
	return ast.NewNewCustomEventExpression(e.Args[0], detail, e.GetPos()), nil
}

func buildWorker(e *ast.NewExpression) (ast.Expression, error) {
	if len(e.Args) == 0 {
		return nil, errAt(e.GetPos(), "new Worker(...) requires a compile-time string-literal path — the worker file is compiled into the binary, so a runtime-computed path cannot be loaded")
	}
	path, ok := e.Args[0].(*ast.StringLiteral)
	if !ok {
		p := e.Args[0].GetPos()
		return nil, errAt(p, "new Worker(...) requires a compile-time string-literal path — the worker file is compiled into the binary, so a runtime-computed path cannot be loaded")
	}
	if len(e.Args) > 2 {
		return nil, errAt(e.GetPos(), "new Worker takes a path and an optional options object")
	}
	var workerData ast.Expression
	if len(e.Args) == 2 {
		lit, ok := e.Args[1].(*ast.ObjectLiteral)
		if !ok {
			return nil, errAt(e.GetPos(), "new Worker's second argument must be an object literal (e.g. { workerData: ... })")
		}
		for _, prop := range lit.Properties {
			if prop.Key != "workerData" {
				return nil, errAt(e.GetPos(), "new Worker options: only 'workerData' is supported (found '%s')", prop.Key)
			}
			workerData = prop.Value
		}
	}
	return ast.NewNewWorkerExpression(path.Value, workerData, e.GetPos()), nil
}

func buildURLPattern(e *ast.NewExpression) (ast.Expression, error) {
	if len(e.Args) > 1 {
		return nil, errAt(e.GetPos(), "new URLPattern does not take a baseURL second argument (single object-init form only)")
	}
	return ast.NewNewURLPatternExpression(arg(e, 0), e.GetPos()), nil
}

func buildRequest(e *ast.NewExpression) (ast.Expression, error) {
	if err := arity(e, "Request(url, init?)", 1, 2); err != nil {
		return nil, err
	}
	return ast.NewNewRequestExpression(e.Args[0], arg(e, 1), e.GetPos()), nil
}

func arrayBuffer(shared bool) builder {
	return func(e *ast.NewExpression) (ast.Expression, error) {
		if err := arity(e, e.ClassName+"(byteLength, options?)", 1, 2); err != nil {
			return nil, err
		}
		ex := ast.NewNewArrayBufferExpression(e.Args[0], e.GetPos())
		ex.Shared = shared
		// `{maxByteLength: m}` marks the buffer growable (ADR-00494).
		if opts := arg(e, 1); opts != nil {
			lit, ok := opts.(*ast.ObjectLiteral)
			if !ok || len(lit.Properties) != 1 || lit.Properties[0].Key != "maxByteLength" {
				return nil, errAt(e.GetPos(), "the buffer options literal supports exactly {maxByteLength: n}")
			}
			ex.MaxByteLength = lit.Properties[0].Value
		}
		return ex, nil
	}
}

func buildBroadcastChannel(e *ast.NewExpression) (ast.Expression, error) {
	var name *ast.StringLiteral
	if len(e.Args) == 1 {
		name, _ = e.Args[0].(*ast.StringLiteral)
	}
	if name == nil {
		return nil, errAt(e.GetPos(), "new BroadcastChannel(...) requires a string-literal channel name")
	}
	return ast.NewNewBroadcastChannelExpression(name.Value, e.GetPos()), nil
}

func buildMessageChannel(e *ast.NewExpression) (ast.Expression, error) {
	if err := arity(e, "MessageChannel", 0, 0); err != nil {
		return nil, err
	}
	return ast.NewNewMessageChannelExpression(typeArg(e, 0), e.GetPos()), nil
}

func buildChannel(e *ast.NewExpression) (ast.Expression, error) {
	if err := arity(e, "Channel(capacity?)", 0, 1); err != nil {
		return nil, err
	}
	return ast.NewNewChannelExpression(typeArg(e, 0), arg(e, 0), e.GetPos()), nil
}

func buildDataView(e *ast.NewExpression) (ast.Expression, error) {
	if err := arity(e, "DataView(buffer, byteOffset?, byteLength?)", 1, 3); err != nil {
		return nil, err
	}
	return ast.NewNewDataViewExpression(e.Args[0], arg(e, 1), arg(e, 2), e.GetPos()), nil
}

func buildRegExp(e *ast.NewExpression) (ast.Expression, error) {
	if err := arity(e, "RegExp(pattern?, flags?)", 0, 2); err != nil {
		return nil, err
	}
	pattern := arg(e, 0)
	if pattern == nil {
		// `new RegExp()` is the empty pattern, whose source Node reports as `(?:)`.
		pattern = ast.NewStringLiteral("(?:)", e.GetPos())
	}
	return ast.NewNewRegExpExpression(pattern, arg(e, 1), e.GetPos()), nil
}

func buildBlob(e *ast.NewExpression) (ast.Expression, error) {
	if err := arity(e, "Blob(parts?, options?)", 0, 2); err != nil {
		return nil, err
	}
	return ast.NewNewBlobExpression(arg(e, 0), arg(e, 1), e.GetPos()), nil
}

func typedArray(kind string) builder {
	return func(e *ast.NewExpression) (ast.Expression, error) {
		if err := arity(e, e.ClassName+"(source, byteOffset?, length?)", 1, 3); err != nil {
			return nil, err
		}
		nta := ast.NewNewTypedArrayExpression(kind, e.Args[0], e.GetPos())
		nta.ByteOffset, nta.Length = arg(e, 1), arg(e, 2)
		return nta, nil
	}
}
