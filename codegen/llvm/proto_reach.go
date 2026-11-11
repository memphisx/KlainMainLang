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
	if e.protoReachAll || e.protoReachNames[name] {
		return true
	}
	// A method is reached through any second name it has (trimLeft is
	// trimStart).
	for _, aliases := range protoMethodAliases {
		for alias, target := range aliases {
			if target == name && e.protoReachNames[alias] {
				return true
			}
		}
	}
	return false
}

// hostMethodReachable reports whether an iterator's getter lists method
// name (each iterator layout would otherwise carry all the helpers): as a
// prototype's (protoMethodReachable), or read off an untyped
// value by library code, or called by the iteration protocol.
func (e *Emitter) hostMethodReachable(name string) bool {
	if name == "next" || name == "return" || name == "throw" || e.protoMethodReachable(name) {
		return true
	}
	return e.libDynMemberReads[name]
}

func (e *Emitter) buildProtoReach() {
	defer e.analysisPass()() // reads every body outside its scope
	e.protoReachNames = map[string]bool{}
	e.libDynMemberReads = map[string]bool{}
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
					if e.typeOfExpr(me.Object).IsDynamic() {
						e.libDynMemberReads[me.Property] = true
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
				if k := e.typeOfExpr(x.Index); !literal(x.Index) && (k.IsDynamic() || !(k.Float || k.IsInteger())) {
					t := e.typeOfExpr(x.Object)
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

// patchedProtoClasses are the builtin classes whose primitive or array
// values call their prototype's methods directly unless the program
// replaces one (patchedProtoMethod).
var patchedProtoClasses = map[string]bool{"String": true, "Number": true, "Boolean": true, "BigInt": true, "Array": true}

// patchedProtoMethod reports whether the program's own code replaces
// class.prototype[name] (`Number.prototype.toLocaleString = f`, or
// Object.defineProperty over it): a call of that method on a value of the
// class then reads the prototype, as JavaScript always does.
func (e *Emitter) patchedProtoMethod(class, name string) bool {
	if e.patchedProtos == nil {
		e.patchedProtos = map[string]bool{}
		protoOf := func(x ast.Expression) string {
			if me, ok := x.(*ast.MemberExpression); ok && me.Property == "prototype" {
				if id, ok := me.Object.(*ast.Identifier); ok && patchedProtoClasses[id.Name] {
					return id.Name
				}
			}
			return ""
		}
		if e.prog != nil {
			for _, stmt := range e.prog.Body {
				if e.libStmts[stmt] != "" {
					continue
				}
				ast.Inspect(stmt, func(n ast.Node) bool {
					switch x := n.(type) {
					case *ast.AssignmentExpression:
						switch l := x.Left.(type) {
						case *ast.MemberExpression:
							if c := protoOf(l.Object); c != "" {
								e.patchedProtos[c+"."+l.Property] = true
							}
						case *ast.IndexExpression:
							if s, ok := l.Index.(*ast.StringLiteral); ok {
								if c := protoOf(l.Object); c != "" {
									e.patchedProtos[c+"."+s.Value] = true
								}
							}
						}
					case *ast.CallExpression:
						if me, ok := x.Callee.(*ast.MemberExpression); ok && len(x.Args) >= 2 && (me.Property == "defineProperty" || me.Property == "defineProperties") {
							if c := protoOf(x.Args[0]); c != "" {
								if s, ok := x.Args[1].(*ast.StringLiteral); ok && me.Property == "defineProperty" {
									e.patchedProtos[c+"."+s.Value] = true
								} else if ol, ok := x.Args[1].(*ast.ObjectLiteral); ok && me.Property == "defineProperties" {
									for _, p := range ol.Properties {
										e.patchedProtos[c+"."+p.Key] = true
									}
								}
							}
						}
					}
					return true
				})
			}
		}
	}
	return e.patchedProtos[class+"."+name]
}

// patchedProtoClassOf is the class patchedProtoMethod asks about for a
// receiver of static type t, or "".
func patchedProtoClassOf(t Type) string {
	switch {
	case t.IsDynamic() || t.Nullable():
		return ""
	case isStringTy(t):
		return "String"
	case t.IR == "i1":
		return "Boolean"
	case t.IsBigInt():
		return "BigInt"
	case t.IsArray() && !t.IsTypedArray() && !t.IsTuple():
		return "Array"
	case t.Float || t.IsInteger():
		return "Number"
	}
	return ""
}
