package llvm

// array_iter_patch.go — an array iterated after the program replaced or
// deleted Array.prototype[Symbol.iterator]: destructuring, spread and
// for…of run the iteration protocol through the property as it is now,
// not the array fast path.

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// arrayIterPatchable reports whether the program names both
// Array.prototype and Symbol.iterator (directly, or through an alias of
// either): only then can an array's iteration leave its fast path, which
// costs a check per iteration.
func (e *Emitter) arrayIterPatchable() bool {
	if e.programIndependent() || e.prog == nil {
		return false
	}
	if e.arrayIterScan == 0 {
		e.arrayIterScan = 1
		spreads := map[ast.Expression]bool{}
		proto, iter := false, false
		ast.Inspect(e.prog, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SpreadElement:
				spreads[x.Arg] = true
			case *ast.MemberExpression:
				if id, ok := x.Object.(*ast.Identifier); ok {
					proto = proto || id.Name == "Array" && x.Property == "prototype"
					iter = iter || id.Name == "Symbol" && x.Property == "iterator"
				}
			}
			return true
		})
		if proto && iter {
			e.arrayIterScan = 2
			e.spreadArgs = spreads
		}
	}
	return e.arrayIterScan == 2
}

// emitArrayIterPatched is an i1: whether arrays now iterate through the
// protocol (dynobj.c).
func (e *Emitter) emitArrayIterPatched() string {
	e.ensureDynJSONC()
	e.declareFn("__kml_array_iter_patched", "declare zeroext i1 @__kml_array_iter_patched()")
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call zeroext i1 @__kml_array_iter_patched()", r))
	return r
}

// emitPatchedArrayElems is the data pointer and length an iteration of the
// array hdr (ptr, length; arrTy) walks: its own elements, or once
// Array.prototype[Symbol.iterator] changed, the values the protocol yields
// (at most take, take < 0 for all), converted to elemTy.
func (e *Emitter) emitPatchedArrayElems(hdr, ptr, length string, arrTy, elemTy Type, src ast.Expression, take int) (string, string, error) {
	if !e.arrayIterPatchable() || arrTy.IsTypedArray() || arrTy.IsFlatArray() {
		return ptr, length, nil
	}
	if hdr == "" {
		hdr = e.newArrayHeader(ptr, length)
	}
	pSlot, lSlot := e.freshReg(), e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", pSlot))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lSlot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ptr, pSlot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", length, lSlot))
	patchL, joinL := e.freshLabel("arriter.patched"), e.freshLabel("arriter.join")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.emitArrayIterPatched(), patchL, joinL))
	e.emitLabel(patchL)
	boxed, err := e.emitBoxValue(Value{Ref: hdr, Ty: arrTy})
	if err != nil {
		return "", "", err
	}
	p2, l2 := e.emitAnyArrayElemsTake(boxed, src, take)
	if !elemTy.IsDynamic() {
		conv, err := e.emitUnboxAnyArray(Value{Ref: e.newArrayHeader(p2, l2), Ty: ArrayOf(TypeAny)}, elemTy)
		if err != nil {
			return "", "", err
		}
		p2, l2 = e.arrayData(conv), e.arrayLen(conv)
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", p2, pSlot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", l2, lSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(joinL)
	outP, outL := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", outP, pSlot))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", outL, lSlot))
	return outP, outL, nil
}

// emitArrayFastPathOK narrows isArr (an i1: the value is an array) to
// whether its iteration may take the array fast path.
func (e *Emitter) emitArrayFastPathOK(isArr string) string {
	if !e.arrayIterPatchable() {
		return isArr
	}
	p := e.emitArrayIterPatched()
	np, ok := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", np, p))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", ok, isArr, np))
	return ok
}

// emitIterMethodFix is m, the [Symbol.iterator] read from v for an
// iteration, or for an array Array.prototype's as it is now.
func (e *Emitter) emitIterMethodFix(v, m Value) Value {
	if !e.arrayIterPatchable() {
		return m
	}
	return e.emitIterMethodOf(v, m)
}

// emitIterMethodOf is emitIterMethodFix whatever the program names.
func (e *Emitter) emitIterMethodOf(v, m Value) Value {
	e.ensureDynJSONC()
	e.declareFn("__kml_iter_method_of", "declare i64 @__kml_iter_method_of(i64, i64)")
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_iter_method_of(i64 %s, i64 %s)", r, v.Ref, m.Ref))
	return Value{Ref: r, Ty: TypeAny}
}

// notIterableMessage is V8's TypeError text for iterating src when it is
// not iterable: the expression as its call printer renders it (a spread's
// property path as written); "" when V8 describes the value instead (a
// destructured property or parameter: emitThrowNotIterableValue).
func (e *Emitter) notIterableMessage(src ast.Expression) string {
	if s, ok := e.v8ExprText(src); ok {
		return s + " is not iterable"
	}
	if src != nil && e.iterTakeNow == 0 {
		return e.iterableDisplayName(src) + " is not iterable"
	}
	return ""
}

// v8ExprText renders x as V8's call printer does for a "not iterable"
// message: names, literals, array literals and binary operations; false
// for anything it prints generically.
func (e *Emitter) v8ExprText(x ast.Expression) (string, bool) {
	switch n := x.(type) {
	case nil:
		return "", false
	case *ast.AsExpression:
		return e.v8ExprText(n.Expr)
	case *ast.NonNullExpression:
		return e.v8ExprText(n.Arg)
	case *ast.Identifier:
		return e.displayName(n.Name), true
	case *ast.NumberLiteral:
		if n.IsBigInt {
			return "", false
		}
		return n.Value, true
	case *ast.StringLiteral:
		return `"` + n.Value + `"`, true
	case *ast.BooleanLiteral:
		if n.Value {
			return "true", true
		}
		return "false", true
	case *ast.NullLiteral:
		if n.IsUndefined {
			return "undefined", true
		}
		return "null", true
	case *ast.UnaryExpression:
		if a, ok := e.v8ExprText(n.Arg); ok && n.Prefix && (n.Op == "-" || n.Op == "+" || n.Op == "!" || n.Op == "~") {
			return n.Op + a, true
		}
	case *ast.BinaryExpression:
		l, ok1 := e.v8ExprText(n.Left)
		r, ok2 := e.v8ExprText(n.Right)
		if ok1 && ok2 {
			return "(" + l + " " + n.Op + " " + r + ")", true
		}
	case *ast.CallExpression:
		// A call prints as its callee.
		return e.v8ExprText(n.Callee)
	case *ast.ObjectLiteral:
		if len(n.Properties) == 0 {
			return "{}", true
		}
	case *ast.ArrayLiteral:
		parts := make([]string, len(n.Elements))
		for i, el := range n.Elements {
			s, ok := e.v8ExprText(el)
			if !ok {
				return "", false
			}
			parts[i] = s
		}
		return "[" + strings.Join(parts, ",") + "]", true
	}
	return "", false
}

// jsNonIterable reports whether x, in the js lane, has a static type no
// iteration accepts (a number, a boolean): JavaScript throws its TypeError
// at run time, where the typed lane rejects it at compile time.
func (e *Emitter) jsNonIterable(x ast.Expression) bool {
	if !e.compatJS() {
		return false
	}
	t := e.inferExprType(x)
	if t.IsDynamic() || t.IsArray() || t.IsTuple() || isStringTy(t) || t.IsObject() || t.IsClass() || t.IsMap() || t.IsSet() || t.IsGenerator() || t.IsCollIter() {
		return false
	}
	return t.IR != "" && t.IR != "ptr"
}

// emitThrowNotIterableValue throws V8's generic "not iterable" TypeError,
// which describes the value (v, boxed): `undefined`, `null`, `object`, or
// a primitive's type and value (`number 5`).
func (e *Emitter) emitThrowNotIterableValue(v Value) error {
	pos := ast.Pos{}
	id := e.bindValue(v, pos)
	m := "__kml_noiter_" + e.freshReg()[1:]
	mid := func() ast.Expression { return ast.NewIdentifier(m, pos) }
	str := func(s string) ast.Expression { return ast.NewStringLiteral(s, pos) }
	set := func(x ast.Expression) ast.Statement {
		return ast.NewExpressionStatement(ast.NewAssignmentExpression("=", mid(), x, pos), pos)
	}
	block := func(st ast.Statement) *ast.BlockStatement { return ast.NewBlockStatement([]ast.Statement{st}, pos) }
	prim := ast.NewBinaryExpression("&&",
		ast.NewBinaryExpression("!==", mid(), str("object"), pos),
		ast.NewBinaryExpression("!==", mid(), str("function"), pos), pos)
	describe := set(ast.NewBinaryExpression("+", ast.NewBinaryExpression("+", mid(), str(" "), pos), id, pos))
	stmts := []ast.Statement{
		ast.NewVarDeclaration("let", m, &ast.TypeAnnotation{Name: "string", Source: "ts"}, ast.NewUnaryExpression("typeof", true, id, pos), pos),
		ast.NewIfStatement(ast.NewBinaryExpression("===", id, ast.NewNullLiteral(true, pos), pos), block(set(str("undefined"))),
			ast.NewIfStatement(ast.NewBinaryExpression("===", id, ast.NewNullLiteral(false, pos), pos), block(set(str("null"))),
				ast.NewIfStatement(prim, block(describe), nil, pos), pos), pos),
	}
	e.pushScope()
	defer e.popScope()
	for _, st := range stmts {
		if err := e.emitStmt(st); err != nil {
			return err
		}
	}
	msg, err := e.emitExpr(ast.NewBinaryExpression("+", mid(), str(" is not iterable (cannot read property Symbol(Symbol.iterator))"), pos))
	if err != nil {
		return err
	}
	e.emitThrowTypeErrorValue(msg.Ref)
	return nil
}

// iterableArgCtors are the builtin constructors that iterate their first
// argument.
var iterableArgCtors = map[string]bool{
	"Set": true, "WeakSet": true, "Map": true, "WeakMap": true,
	"Int8Array": true, "Uint8Array": true, "Uint8ClampedArray": true, "Int16Array": true, "Uint16Array": true,
	"Int32Array": true, "Uint32Array": true, "Float32Array": true, "Float64Array": true,
	"BigInt64Array": true, "BigUint64Array": true,
}

// iterableCtorArg is ex with its array argument spread (`new Set([...a])`)
// when the program may replace Array.prototype[Symbol.iterator]: the
// constructor then iterates the values the protocol yields.
func (e *Emitter) iterableCtorArg(ex *ast.NewExpression) (*ast.NewExpression, bool) {
	if !iterableArgCtors[ex.ClassName] || ex.Qualified || len(ex.Args) == 0 || e.isShadowedByLocal(ex.ClassName) || !e.arrayIterPatchable() {
		return nil, false
	}
	if _, user := e.classes[ex.ClassName]; user {
		return nil, false
	}
	arg := ex.Args[0]
	t := e.inferExprType(arg)
	if !(t.IsArray() || t.IsTuple()) || t.IsDynamic() || t.IsTypedArray() || t.IsFlatArray() {
		return nil, false
	}
	e.spreadArgs[arg] = true
	nx := *ex
	nx.Args = append([]ast.Expression{ast.NewArrayLiteral([]ast.Expression{&ast.SpreadElement{Arg: arg}}, arg.GetPos())}, ex.Args[1:]...)
	return &nx, true
}

// emitMapSeedLoop fills the map at mapPtr (keyTy → valTy) from entries
// through the iteration protocol: each value's [0] and [1].
func (e *Emitter) emitMapSeedLoop(mapPtr string, entries ast.Expression, keyTy, valTy Type, pos ast.Pos) error {
	e.weakSeedCtr++
	item := fmt.Sprintf("__kml_map_item_%d", e.weakSeedCtr)
	target := e.bindValue(Value{Ref: mapPtr, Ty: MapType(keyTy, valTy)}, pos)
	at := func(i string) ast.Expression {
		return ast.NewIndexExpression(ast.NewIdentifier(item, pos), ast.NewNumberLiteral(i, pos), pos)
	}
	call := ast.NewCallExpression(ast.NewMemberExpression(target, "set", pos), []ast.Expression{at("0"), at("1")}, pos)
	// A value that is not an object is not an entry (Map's TypeError).
	id := ast.NewIdentifier(item, pos)
	typeOf := ast.NewUnaryExpression("typeof", true, id, pos)
	notObj := ast.NewBinaryExpression("||",
		ast.NewBinaryExpression("===", id, ast.NewNullLiteral(false, pos), pos),
		ast.NewBinaryExpression("&&",
			ast.NewBinaryExpression("!==", typeOf, ast.NewStringLiteral("object", pos), pos),
			ast.NewBinaryExpression("!==", typeOf, ast.NewStringLiteral("function", pos), pos), pos), pos)
	msg := ast.NewBinaryExpression("+", ast.NewBinaryExpression("+", ast.NewStringLiteral("Iterator value ", pos), id, pos),
		ast.NewStringLiteral(" is not an entry object", pos), pos)
	check := ast.NewIfStatement(notObj, ast.NewBlockStatement([]ast.Statement{
		ast.NewThrowStatement(ast.NewNewErrorExpression("TypeError", msg, pos), pos)}, pos), nil, pos)
	src := ast.NewAsExpression(entries, ast.AnyAnnotation(""), entries.GetPos())
	loop := ast.NewForOfStatement("const", item, src, ast.NewBlockStatement([]ast.Statement{check, ast.NewExpressionStatement(call, pos)}, pos), pos)
	return e.emitStmt(loop)
}

// patchedIterInit is init spread (`[...init]`) when it is an array the
// constructor iterates and the program may replace
// Array.prototype[Symbol.iterator]; else init itself.
func (e *Emitter) patchedIterInit(init ast.Expression) (ast.Expression, bool) {
	if init == nil || !e.arrayIterPatchable() {
		return init, false
	}
	if lit, ok := init.(*ast.ArrayLiteral); ok && len(lit.Elements) == 1 {
		if sp, ok := lit.Elements[0].(*ast.SpreadElement); ok && e.spreadArgs[sp.Arg] {
			return init, false // already spread
		}
	}
	t := e.inferExprType(init)
	if !(t.IsArray() || t.IsTuple()) || t.IsDynamic() || t.IsTypedArray() || t.IsFlatArray() {
		return init, false
	}
	e.spreadArgs[init] = true
	return ast.NewArrayLiteral([]ast.Expression{&ast.SpreadElement{Arg: init}}, init.GetPos()), true
}
