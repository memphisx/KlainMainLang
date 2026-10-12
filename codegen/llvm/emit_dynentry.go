package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emit_dynentry.go — a function literal's dynamic entry. A parameter typed
// as a class reads its fields at the class's fixed offsets; a call through
// the dynamic ABI (an emitter's listener, a callback held as `any`) can pass
// it another object, as JS allows (an HTTP/1 request handed to a handler
// typed for HTTP/2's). Such a literal gets a second body with those
// parameters as `any`, over the same captured environment; the boxing
// adapter (emitDynClosureAdapter) runs it when an argument for one of them
// is an object that is not an instance of the class.

// dynEntryClass reports a parameter type a dynamic entry widens: a plain
// user class's instance (its fields at fixed offsets behind a class tag).
func (e *Emitter) dynEntryClass(p Type) (ClassInfo, bool) {
	if !p.IsClass || p.IR != "ptr" || p.IsArray || p.IsDynamic || p.ClassName == "" {
		return ClassInfo{}, false
	}
	info, ok := e.classes[p.ClassName]
	if !ok || info.HostBase != nil || info.Ty.IsDynamic || info.IsErrorSubclass {
		return ClassInfo{}, false
	}
	return info, true
}

// dynEntryParams is paramTypes with each dynEntryClass parameter as `any`;
// ok is false when there is none. A `this` parameter keeps its type: it is
// the receiver, not an argument.
func (e *Emitter) dynEntryParams(params []ast.Param, paramTypes []Type) ([]Type, bool) {
	var out []Type
	for i, p := range paramTypes {
		if i < len(params) && params[i].Name == "this" {
			continue
		}
		if _, ok := e.dynEntryClass(p); !ok {
			continue
		}
		if out == nil {
			out = append([]Type(nil), paramTypes...)
		}
		out[i] = TypeAny
	}
	return out, out != nil
}

// registerDynEntry records alt as the dynamic entry of the function sym.
func (e *Emitter) registerDynEntry(sym, alt string) {
	e.addUnitRowLit(unitKindDynEntry, "{ i64, ptr }", fmt.Sprintf("{ i64 ptrtoint (ptr %s to i64), ptr %s }", sym, alt))
}

// emitForeignClassArg is whether the boxed argument word is an object that
// is not an instance of info's class (or a subclass): one only the dynamic
// entry can read. A primitive, null or undefined is not (the typed body
// reads it as its null instance, as before).
func (e *Emitter) emitForeignClassArg(word string, info ClassInfo) string {
	tag, payload := e.emitUnboxTagPayload(Value{Ref: word, Ty: TypeAny})
	isPtr, isRaw := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp uge i8 %s, %d", isPtr, tag, kmlTagObject))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isRaw, tag, kmlTagObject))
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", slot))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", isPtr, slot))
	rawL, doneL := e.freshLabel("dynentry.raw"), e.freshLabel("dynentry.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isRaw, rawL, doneL))
	e.emitLabel(rawL)
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", p, payload))
	w := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", w, p))
	is := e.emitClassIs(w, info.TagID)
	not := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", not, is))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", not, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", r, slot))
	return r
}

// emitDynEntryOf is the dynamic entry registered for the code pointer fp,
// or null.
func (e *Emitter) emitDynEntryOf(fp string) string {
	e.ensureUnitReg()
	key, row := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", key, fp))
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_unit_find(i64 %d, i64 %s)", row, unitKindDynEntry, key))
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", slot))
	none := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", none, row))
	hitL, doneL := e.freshLabel("dynentry.hit"), e.freshLabel("dynentry.found")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", none, doneL, hitL))
	e.emitLabel(hitL)
	ap, a := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", ap, row))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", a, ap))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", a, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
	return r
}

// widenDynEntryParams marks the parameters alt widens from paramTypes as
// read through their binding while a dynamic entry's body is emitted; the
// returned function restores the previous marks.
func (e *Emitter) widenDynEntryParams(params []ast.Param, paramTypes, alt []Type) func() {
	saved := e.dynEntryWidened
	next := map[string]bool{}
	for k := range saved {
		next[k] = true
	}
	for i, p := range params {
		if i < len(alt) && alt[i].IsDynamic && !paramTypes[i].IsDynamic && p.Name != "" {
			next[p.Name] = true
		}
	}
	e.dynEntryWidened = next
	return func() { e.dynEntryWidened = saved }
}

// dynEntryWidenedIdent reports id as a parameter widenDynEntryParams marked,
// bound as a box in the body being emitted.
func (e *Emitter) dynEntryWidenedIdent(id *ast.Identifier) bool {
	if !e.dynEntryWidened[id.Name] {
		return false
	}
	sym, found := e.lookup(id.Name)
	return found && sym.Ty.IsDynamic
}

// widenedRooted reports e as a parameter widened reports, or a member read,
// index or call rooted at one: a value read off a box.
func widenedRooted(e ast.Expression, widened func(*ast.Identifier) bool) bool {
	switch x := e.(type) {
	case *ast.Identifier:
		return widened(x)
	case *ast.MemberExpression:
		return widenedRooted(x.Object, widened)
	case *ast.IndexExpression:
		return widenedRooted(x.Object, widened)
	case *ast.NonNullExpression:
		return widenedRooted(x.Arg, widened)
	case *ast.CallExpression:
		return widenedRooted(x.Callee, widened)
	}
	return false
}
