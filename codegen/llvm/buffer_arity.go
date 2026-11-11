package llvm

import "KlainMainLang/ast"

// bufferArity is how many arguments a Buffer method reads (max; the rest
// are evaluated after them and ignored, as JavaScript does), Node's
// ERR_INVALID_ARG_TYPE message for each leading one it requires (in order),
// and the value an omitted optional leading one takes (pad).
type bufferArity struct {
	max  int
	need []string
	pad  func(pos ast.Pos) ast.Expression
}

const (
	bufMsgFrom   = `The first argument must be of type string or an instance of Buffer, ArrayBuffer, or Array or an Array-like Object. Received undefined`
	bufMsgSize   = `The "size" argument must be of type number. Received undefined`
	bufMsgTarget = `The "target" argument must be an instance of Buffer or Uint8Array. Received undefined`
)

func undefinedPad(pos ast.Pos) ast.Expression { return ast.NewNullLiteral(true, pos) }

// bufferStaticArity is Buffer.<method>'s.
var bufferStaticArity = map[string]bufferArity{
	"from":            {max: 3, need: []string{bufMsgFrom}},
	"alloc":           {max: 3, need: []string{bufMsgSize}},
	"allocUnsafe":     {max: 1, need: []string{bufMsgSize}},
	"allocUnsafeSlow": {max: 1, need: []string{bufMsgSize}},
	"concat":          {max: 2, need: []string{`The "list" argument must be an instance of Array. Received undefined`}},
	"compare": {max: 2, need: []string{
		`The "buf1" argument must be an instance of Buffer or Uint8Array. Received undefined`,
		`The "buf2" argument must be an instance of Buffer or Uint8Array. Received undefined`}},
	"isBuffer":   {max: 1, pad: undefinedPad},
	"byteLength": {max: 2, need: []string{`The "string" argument must be of type string or an instance of Buffer or ArrayBuffer. Received undefined`}},
	"isEncoding": {max: 1, pad: undefinedPad},
}

// bufferInstanceArity is buffer.<method>'s.
var bufferInstanceArity = map[string]bufferArity{
	"toString": {max: 3},
	"write":    {max: 4, need: []string{"argument must be a string"}},
	"copy":     {max: 4, need: []string{bufMsgTarget}},
	"equals":   {max: 1, need: []string{`The "otherBuffer" argument must be an instance of Buffer or Uint8Array. Received undefined`}},
	"compare":  {need: []string{bufMsgTarget}},
}

// applyBufferArity fits a Buffer call's arguments to its method's arity
// (call is the whole call, for its result type): it evaluates and drops the
// extra ones, pads an omitted optional one, and, when a required one is
// missing, evaluates the rest and throws Node's TypeError, returning the
// call's value on the dead path after it (thrown).
func (e *Emitter) applyBufferArity(a bufferArity, args []ast.Expression, call *ast.CallExpression, pos ast.Pos) (out []ast.Expression, v Value, thrown bool, err error) {
	if a.max > 0 && len(args) > a.max {
		if args, err = e.dropArgsPast(args, a.max, pos); err != nil {
			return nil, Value{}, false, err
		}
	}
	given := len(args)
	for given > 0 && e.omittedArg(args[given-1]) {
		given-- // undefined padding an omitted argument
	}
	if given < len(a.need) {
		for _, x := range args[:given] {
			if _, err := e.emitExpr(x); err != nil {
				return nil, Value{}, false, err
			}
		}
		e.emitThrowCoded("TypeError", "ERR_INVALID_ARG_TYPE", e.internString(a.need[given]))
		e.emitLabel(e.freshLabel("buffer.arity.dead"))
		ty := e.typeOfExpr(call)
		return nil, Value{Ref: zeroRef(ty), Ty: ty}, true, nil
	}
	if len(args) == 0 && a.pad != nil {
		args = []ast.Expression{a.pad(pos)}
	}
	return args, Value{}, false, nil
}
