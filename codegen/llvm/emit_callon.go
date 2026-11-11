package llvm

// emit_callon.go — a class's constructor run on an existing object,
// `C.call(this, …)`: the pre-class inheritance pattern (`function F() {
// C.call(this); } util.inherits(F, C)`). A class here is no JavaScript
// function, so `new F()` makes its instance as an instance of C given
// F.prototype (dynobj.c's __kml_ctor_this), and the call runs C's
// constructor on it.

import (
	"fmt"

	"KlainMainLang/ast"
)

// callOnClasses are the classes the program calls as `C.call(…)` (with no
// static `call` of their own): their prototypes register an allocator.
func (e *Emitter) callOnClasses() map[string]bool {
	if e.callOn != nil {
		return e.callOn
	}
	e.callOn = map[string]bool{}
	if e.prog == nil {
		return e.callOn
	}
	ast.Inspect(e.prog, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpression)
		if !ok {
			return true
		}
		mem, ok := call.Callee.(*ast.MemberExpression)
		if !ok || mem.Property != "call" {
			return true
		}
		id, ok := mem.Object.(*ast.Identifier)
		if !ok {
			return true
		}
		// Only a library class: Node's are functions; a class of the
		// program throws for a call without `new` there as here.
		info, found := e.classes[id.Name]
		if !found || info.TagID&kmlHdrIDMask < kmlStableIDBase || info.HostBase != nil || e.classHasStatic(id.Name, "call") {
			return true
		}
		for cls := id.Name; cls != ""; cls = e.classes[cls].BaseClass {
			if len(e.capturesOf(cls)) > 0 {
				return true
			}
			if _, ok := e.classes[cls]; !ok {
				break
			}
		}
		e.callOn[id.Name] = true
		return true
	})
	return e.callOn
}

// classHasStatic reports a static member name of class c.
func (e *Emitter) classHasStatic(c, name string) bool {
	_, ok := e.classes[c].StaticMethodSigs[name]
	return ok
}

// ensureClassAlloc emits `ptr @<C>__kml_alloc()`: an instance of c with its
// words written and its constructor not yet run.
func (e *Emitter) ensureClassAlloc(c string) string {
	fn := "@" + llvmSafeSymbol(c) + "__kml_alloc"
	if e.classAllocs == nil {
		e.classAllocs = map[string]bool{}
	}
	if e.classAllocs[c] {
		return fn
	}
	e.classAllocs[c] = true
	info := e.classes[c]
	restore := e.beginDetachedFunc()
	r := e.emitObjAlloc(info.Ty)
	e.initInstanceWords(r, c, info)
	e.emitTerminator("ret " + irPtr + " " + r)
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("internal "+irPtr+" "+fn+"()", body)
	return fn
}

// emitClassCallOn is `C.call(thisArg, …args)`: C's constructor run on
// thisArg, an instance of C (one `new F()` made for a function inheriting
// C.prototype, or C's own); anything else is the TypeError a class gives
// for a call without `new`.
func (e *Emitter) emitClassCallOn(c string, args []ast.Expression, pos ast.Pos) (Value, error) {
	var thisArg ast.Expression = ast.NewNullLiteral(true, pos)
	if len(args) > 0 {
		thisArg = args[0]
		args = args[1:]
	}
	tv, err := e.emitExprWithObjectHint(thisArg, TypeAny)
	if err != nil {
		return Value{}, err
	}
	tb, err := e.emitBoxValue(tv)
	if err != nil {
		return Value{}, err
	}
	is, err := e.emitAnyInstanceOfClass(tb, c)
	if err != nil {
		return Value{}, err
	}
	is = e.convert(is, TypeBool)
	tag, payload := e.emitUnboxTagPayload(tb)
	obj, both := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", obj, tag, kmlTagObject))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", both, obj, is.Ref))
	runL, badL := e.freshLabel("callon.run"), e.freshLabel("callon.bad")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", both, runL, badL))
	e.emitLabel(badL)
	e.emitThrowTypeError("Class constructor " + inspectClassName(c) + " cannot be invoked without 'new'")
	e.emitLabel(runL)
	e.newInto = e.emitIntToPtr(payload)
	if _, err := e.emitNewExpression(ast.NewNewExpression(c, args, pos)); err != nil {
		e.newInto = ""
		return Value{}, err
	}
	return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil
}

// protoUse records how the program can give an instance of a class another
// prototype.
type protoUse struct {
	newTarget bool // Reflect.construct with a newTarget
	replaced  bool // that, or Object/Reflect.setPrototypeOf, or C.call(this)
}

// protoUses scans the program once for the forms that replace a class
// instance's prototype; without them every instance keeps its class's.
func (e *Emitter) protoUses() *protoUse {
	if e.protoUse != nil {
		return e.protoUse
	}
	u := &protoUse{}
	e.protoUse = u
	if e.prog != nil {
		ast.Inspect(e.prog, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpression)
			if !ok {
				return true
			}
			mem, ok := call.Callee.(*ast.MemberExpression)
			if !ok {
				return true
			}
			obj, _ := mem.Object.(*ast.Identifier)
			if obj == nil {
				return true
			}
			switch {
			case obj.Name == "Reflect" && mem.Property == "construct" && len(call.Args) >= 3:
				u.newTarget, u.replaced = true, true
			case (obj.Name == "Object" || obj.Name == "Reflect") && mem.Property == "setPrototypeOf":
				u.replaced = true
			}
			return true
		})
	}
	if len(e.callOnClasses()) > 0 {
		u.replaced = true
	}
	return u
}
