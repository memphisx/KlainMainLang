package llvm

import "KlainMainLang/ast"

// collectionArity is how many arguments a Map or Set method reads: an
// omitted one is undefined, an extra one is evaluated and ignored.
var collectionArity = map[string]int{
	"set": 2, "get": 1, "has": 1, "delete": 1, "add": 1,
	"entries": 0, "keys": 0, "values": 0, "clear": 0, "forEach": 2,
}

// fitCollectionArgs fits a Map or Set call's arguments to its method's
// arity. forEach with no callback throws JavaScript's TypeError at run time
// (thrown: the caller returns v from the dead path after it).
func (e *Emitter) fitCollectionArgs(method string, args []ast.Expression, resTy Type, pos ast.Pos) (out []ast.Expression, v Value, thrown bool, err error) {
	n, ok := collectionArity[method]
	if !ok {
		return args, Value{}, false, nil
	}
	if len(args) > n {
		if args, err = e.dropArgsPast(args, n, pos); err != nil {
			return nil, Value{}, false, err
		}
	}
	if method == "forEach" && len(args) == 0 {
		e.emitThrowTypeError("undefined is not a function")
		e.emitLabel(e.freshLabel("collection.arity.dead"))
		return nil, Value{Ref: zeroRef(resTy), Ty: resTy}, true, nil
	}
	if method == "forEach" && len(args) == 2 && forEachThisUnread(args) {
		if args, err = e.dropArgsPast(args, 1, pos); err != nil {
			return nil, Value{}, false, err
		}
	}
	for method != "forEach" && len(args) < n {
		args = append(args, ast.NewNullLiteral(true, pos))
	}
	return args, Value{}, false, nil
}

// forEachThisUnread reports a forEach whose thisArg its callback cannot
// observe: an arrow function's this is its own scope's, and an undefined
// thisArg is what an absent one gives.
func forEachThisUnread(args []ast.Expression) bool {
	if _, arrow := args[0].(*ast.ArrowFunction); arrow {
		return true
	}
	u, ok := args[1].(*ast.NullLiteral)
	return ok && u.IsUndefined
}
