package llvm

// coll_proto_patch.go — a Map, Set, WeakMap or WeakSet method the program
// replaces on its prototype (`Map.prototype.set = f`). Node looks every
// method up on the prototype, so an instance's call, and the constructor's
// adder (`set` / `add`), run whatever the prototype holds at that moment.
// The native fast path stays for every method the program never writes.

import (
	"fmt"

	"KlainMainLang/ast"
)

var collProtoOwners = map[string]bool{"Map": true, "Set": true, "WeakMap": true, "WeakSet": true}

// collProtoOwnerOf is the prototype owner `Owner.prototype` names, or "".
func collProtoOwnerOf(x ast.Expression) string {
	m, ok := x.(*ast.MemberExpression)
	if !ok || m.Property != "prototype" {
		return ""
	}
	id, ok := m.Object.(*ast.Identifier)
	if !ok || !collProtoOwners[id.Name] {
		return ""
	}
	return id.Name
}

// collProtoPatched reports whether the program may replace owner's method:
// an assignment to `Owner.prototype.method` (a computed key: any method), or
// `Owner.prototype` aliased (`const p = Map.prototype`) or handed to a
// property-defining call (Object.defineProperty / defineProperties / assign,
// Reflect.set / defineProperty), either of which may replace any of them.
// Reading or comparing the prototype changes nothing.
func (e *Emitter) collProtoPatched(owner, method string) bool {
	if e.programIndependent() || e.prog == nil {
		return false
	}
	if e.collProtoWrites == nil {
		e.collProtoWrites = map[string]bool{}
		definers := map[string]bool{
			"Object.defineProperty": true, "Object.defineProperties": true, "Object.assign": true,
			"Reflect.set": true, "Reflect.defineProperty": true,
		}
		ast.Inspect(e.prog, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignmentExpression:
				switch t := x.Left.(type) {
				case *ast.MemberExpression:
					if o := collProtoOwnerOf(t.Object); o != "" {
						e.collProtoWrites[o+"."+t.Property] = true
					}
				case *ast.IndexExpression:
					if o := collProtoOwnerOf(t.Object); o != "" {
						e.collProtoWrites[o+".*"] = true
					}
				}
				if o := collProtoOwnerOf(x.Right); o != "" {
					e.collProtoWrites[o+".*"] = true
				}
			case *ast.VarDeclaration:
				if o := collProtoOwnerOf(x.Init); o != "" {
					e.collProtoWrites[o+".*"] = true
				}
			case *ast.CallExpression:
				if m, ok := x.Callee.(*ast.MemberExpression); ok {
					if id, ok := m.Object.(*ast.Identifier); ok && definers[id.Name+"."+m.Property] && len(x.Args) > 0 {
						if o := collProtoOwnerOf(x.Args[0]); o != "" {
							e.collProtoWrites[o+".*"] = true
						}
					}
				}
			}
			return true
		})
	}
	return e.collProtoWrites[owner+"."+method] || e.collProtoWrites[owner+".*"]
}

// collOwner is the prototype owner of a collection type.
func collOwner(ty Type) string {
	owner := "Set"
	if ty.IsMap() {
		owner = "Map"
	}
	if ty.Weak {
		owner = "Weak" + owner
	}
	return owner
}

// emitCollProtoCall is recv.method(args) through the prototype as it is now:
// `Owner.prototype.method.call(recv, ...args)`, converted to the call's
// static type.
func (e *Emitter) emitCollProtoCall(ex *ast.CallExpression, owner string) (Value, error) {
	mem := ex.Callee.(*ast.MemberExpression)
	pos := ex.GetPos()
	resTy := e.typeOfExpr(ex)
	recv, err := e.receiverTemp(mem.Object)
	if err != nil {
		return Value{}, err
	}
	proto := ast.NewMemberExpression(ast.NewIdentifier(owner, pos), "prototype", pos)
	fn := ast.NewMemberExpression(ast.NewMemberExpression(proto, mem.Property, pos), "call", pos)
	call := ast.NewCallExpression(fn, append([]ast.Expression{recv}, ex.Args...), pos)
	v, err := e.emitExpr(call)
	if err != nil {
		return Value{}, err
	}
	if resTy.IR == "" || resTy.IsDynamic() {
		return v, nil
	}
	return e.convert(v, resTy), nil
}

// emitCollPatchedSeed is a collection constructor's seeding once the
// program may replace its adder: as Node does, the adder is read from the
// prototype once, before the iterable is touched (a non-function is a
// TypeError), and called with the new collection as `this` for each value
// the iterator yields — so an adder that throws ends the walk and closes
// the iterator.
func (e *Emitter) emitCollPatchedSeed(coll Value, init ast.Expression, pos ast.Pos) error {
	init, err := e.nullishSeedSource(init)
	if err != nil {
		return err
	}
	owner := collOwner(coll.Ty)
	isMap := coll.Ty.IsMap()
	name := "add"
	if isMap {
		name = "set"
	}
	e.weakSeedCtr++
	adder := fmt.Sprintf("__kml_adder_%d", e.weakSeedCtr)
	item := fmt.Sprintf("__kml_adder_item_%d", e.weakSeedCtr)
	proto := ast.NewMemberExpression(ast.NewIdentifier(owner, pos), "prototype", pos)
	if err := e.emitStmt(ast.NewVarDeclaration("const", adder, nil, ast.NewMemberExpression(proto, name, pos), pos)); err != nil {
		return err
	}
	notFn := ast.NewBinaryExpression("!==", ast.NewUnaryExpression("typeof", true, ast.NewIdentifier(adder, pos), pos), ast.NewStringLiteral("function", pos), pos)
	msg := ast.NewTemplateLiteral([]string{"'", "' returned for property '" + name + "' of object '#<" + owner + ">' is not a function"},
		[]ast.Expression{ast.NewIdentifier(adder, pos)}, pos)
	throw := ast.NewThrowStatement(ast.NewNewExpression("TypeError", []ast.Expression{msg}, pos), pos)
	if err := e.emitStmt(ast.NewIfStatement(notFn, ast.NewBlockStatement([]ast.Statement{throw}, pos), nil, pos)); err != nil {
		return err
	}
	target := e.bindValue(coll, pos)
	at := func(i string) ast.Expression {
		return ast.NewIndexExpression(ast.NewIdentifier(item, pos), ast.NewNumberLiteral(i, pos), pos)
	}
	args := []ast.Expression{target, ast.NewIdentifier(item, pos)}
	if isMap {
		args = []ast.Expression{target, at("0"), at("1")}
	}
	call := ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier(adder, pos), "call", pos), args, pos)
	body := []ast.Statement{ast.NewExpressionStatement(call, pos)}
	if isMap {
		body = append([]ast.Statement{entryObjectCheck(item, pos)}, body...)
	}
	src := ast.NewAsExpression(init, ast.AnyAnnotation(""), init.GetPos())
	return e.emitStmt(ast.NewForOfStatement("const", item, src, ast.NewBlockStatement(body, pos), pos))
}
