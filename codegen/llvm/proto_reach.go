package llvm

// proto_reach.go — which methods a builtin prototype object carries. Each
// method of Array.prototype (and String's, a host class's) is a record
// with its own adapter, thousands of IR lines for the Array set, so a
// program that reads `Array.prototype.map` would otherwise build them all.
// A builtin method is non-enumerable: `for…in`, Object.keys, spread and
// Object.assign never list it, so a program reaches one only by a name it
// writes, or through own-name reflection (getOwnPropertyNames, ownKeys,
// descriptors) or a key computed at run time. With neither, the prototype
// carries the methods the program names (and the ones the runtime itself
// calls); with either, every method, as in Node.

import (
	"reflect"

	"KlainMainLang/ast"
	"KlainMainLang/checker"
)

// protoAlwaysNames are the methods the runtime reaches on a prototype
// without the program naming them (String(x), join, iteration).
var protoAlwaysNames = []string{"toString", "toLocaleString", "valueOf", "join", "values", "constructor"}

// protoReflectNames name the reflection that lists or reads own keys the
// program never writes.
var protoReflectNames = map[string]bool{
	"getOwnPropertyNames": true, "getOwnPropertyDescriptors": true, "ownKeys": true,
}

// protoKeyCalls take a property key as their second argument.
var protoKeyCalls = map[string]bool{
	"getOwnPropertyDescriptor": true, "get": true, "has": true, "hasOwn": true,
	"defineProperty": true, "deleteProperty": true, "set": true,
}

// protoMethodReachable reports whether a builtin prototype carries method
// name in this program.
func (e *Emitter) protoMethodReachable(name string) bool {
	if !e.protoReachBuilt {
		e.protoReachBuilt = true
		e.buildProtoReach()
	}
	return e.protoReachAll || e.protoReachNames[name]
}

func (e *Emitter) buildProtoReach() {
	e.protoReachNames = map[string]bool{}
	for _, n := range protoAlwaysNames {
		e.protoReachNames[n] = true
	}
	if e.prog == nil {
		e.protoReachAll = true
		return
	}
	literal := func(x ast.Expression) bool {
		switch x.(type) {
		case *ast.StringLiteral, *ast.NumberLiteral:
			return true
		}
		return false
	}
	for _, stmt := range e.prog.Body {
		if e.libStmts[stmt] != "" {
			// A library names a prototype method as `X.prototype.m`.
			ast.Inspect(stmt, func(n ast.Node) bool {
				if me, ok := n.(*ast.MemberExpression); ok {
					if obj, ok := me.Object.(*ast.MemberExpression); ok && obj.Property == "prototype" {
						e.protoReachNames[me.Property] = true
					}
				}
				return true
			})
			continue
		}
		collectProtoNames(reflect.ValueOf(stmt), e.protoReachNames, map[uintptr]bool{})
		ast.Inspect(stmt, func(n ast.Node) bool {
			if e.protoReachAll {
				return false
			}
			switch x := n.(type) {
			case *ast.MemberExpression:
				if protoReflectNames[x.Property] {
					e.protoReachAll = true
				}
			case *ast.IndexExpression:
				if k := e.inferExprType(x.Index); !literal(x.Index) && (k.IsDynamic() || !(k.Float || k.IsInteger())) {
					t := e.inferExprType(x.Object)
					if !(t.IsArray() || t.IsTypedArray() || isStringTy(t) || t.IsMap() || e.notAPrototype(x.Object)) {
						e.protoReachAll = true
					}
				}
			case *ast.BinaryExpression:
				if x.Op == "in" && !literal(x.Left) {
					e.protoReachAll = true
				}
			case *ast.CallExpression:
				if me, ok := x.Callee.(*ast.MemberExpression); ok && protoKeyCalls[me.Property] && len(x.Args) >= 2 && !literal(x.Args[1]) {
					if id, ok := me.Object.(*ast.Identifier); ok && (id.Name == "Reflect" || id.Name == "Object") {
						e.protoReachAll = true
					}
				}
			}
			return true
		})
		if e.protoReachAll {
			return
		}
	}
}

// protoNameFields are the AST fields that hold a name the program writes.
var protoNameFields = map[string]bool{"Property": true, "Key": true, "Name": true, "Value": true, "Local": true}

// collectProtoNames adds every name a user statement writes: a property
// read, a destructured or literal key, an identifier, a string literal.
func collectProtoNames(v reflect.Value, out map[string]bool, seen map[uintptr]bool) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || seen[v.Pointer()] {
			return
		}
		seen[v.Pointer()] = true
		collectProtoNames(v.Elem(), out, seen)
	case reflect.Interface:
		if !v.IsNil() {
			collectProtoNames(v.Elem(), out, seen)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			collectProtoNames(v.Index(i), out, seen)
		}
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if !t.Field(i).IsExported() {
				continue
			}
			if protoNameFields[t.Field(i).Name] && f.Kind() == reflect.String {
				out[f.String()] = true
				continue
			}
			collectProtoNames(f, out, seen)
		}
	}
}

// notAPrototype reports whether the checker types obj as an object literal
// or tuple: never a builtin prototype (whose type is its class's interface,
// or any).
func (e *Emitter) notAPrototype(obj ast.Expression) bool {
	c := e.front()
	if c == nil {
		return false
	}
	t := c.TypeOf(obj)
	if t == nil || c.Unanswered(t) || t.Flags&checker.Object == 0 {
		return false
	}
	return t.Kind == checker.Anonymous || t.Kind == checker.Tuple
}
