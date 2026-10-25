package llvm

// emit_builtin_proto.go — a builtin class's prototype object as a value
// (`Map.prototype`, TDD-00230). Each method of a host class (a Map, a Set,
// a RegExp, …) is one dynamic-function record per class, the same one an
// instance's method read gives (`m.get`), so `Map.prototype.get === m.get`
// holds whatever the instance's type arguments. The record's adapter calls
// the method of the receiver's own host layout (emitHostMethodAdapter),
// which each layout's shape getter also lists under a hidden key; a
// receiver of another class is a TypeError, as in Node.

import (
	"fmt"
	"sort"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/parser"
	"KlainMainLang/sema"
)

// protoHiddenKey is the key a host layout's shape getter answers its own
// typed method record under, for class.prototype's record to call.
func protoHiddenKey(class, method string) string {
	return "__kml_lm_" + class + "." + method
}

// protoMethodRecord returns class.prototype's record for method m: one per
// class and method.
func (e *Emitter) protoMethodRecord(class string, m hostMember) string {
	key := "proto." + class + "." + m.name
	if e.shapeMethodRecs == nil {
		e.shapeMethodRecs = map[string]string{}
	}
	if rec, ok := e.shapeMethodRecs[key]; ok {
		return rec
	}
	base := "__kml_proto_m_" + llvmSafeSymbol(class) + "_" + llvmSafeSymbol(m.name)
	fn, rec := "@"+base, "@"+base+"_rec"
	e.shapeMethodRecs[key] = rec
	arity := len(m.sig.Params)
	if m.sig.HasRestParam() {
		arity--
	}
	e.registerFnMeta(fn, m.name, arity, fnKindPlain)
	e.emitGlobal(fmt.Sprintf("%s = internal constant { ptr, ptr, i64 } { ptr %s, ptr null, i64 %d }", rec, fn, arity))

	// The same in every program: a library unit of its own (libOwner).
	restore := e.sharedBody(e.beginDetachedFunc())
	e.ensureShapeRuntime()
	e.declareFn("__kml_proto_call", "declare i64 @__kml_proto_call(i64, ptr, i64, ptr, ptr)")
	ok := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", ok))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_proto_call(i64 %%this, ptr %s, i64 %%argc, ptr %%argv, ptr %s)", r, e.internString(protoHiddenKey(class, m.name)), ok))
	okv, okb := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", okv, ok))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", okb, okv))
	goodL, badL := e.freshLabel("proto.ok"), e.freshLabel("proto.bad")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", okb, goodL, badL))
	e.emitLabel(badL)
	e.emitThrowTypeErrorValue(e.emitReceiverMessage("Method "+class+".prototype."+m.name+" called on incompatible receiver ", "%this"))
	e.emitLabel(goodL)
	e.emitTerminator(fmt.Sprintf("ret i64 %s", r))
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc(fmt.Sprintf("internal i64 %s(ptr %%env, i64 %%this, i64 %%argc, ptr %%argv)", fn), body)
	return rec
}

// protoGetterRecord returns class.prototype's getter record for the
// readonly property m: one per class and property, calling the property of
// the receiver's own host layout, a TypeError on a receiver of another class.
func (e *Emitter) protoGetterRecord(class string, m hostMember, probe string) string {
	key := "protoget." + class + "." + m.name
	if e.shapeMethodRecs == nil {
		e.shapeMethodRecs = map[string]string{}
	}
	if rec, ok := e.shapeMethodRecs[key]; ok {
		return rec
	}
	base := "__kml_proto_g_" + llvmSafeSymbol(class) + "_" + llvmSafeSymbol(m.name)
	fn, rec := "@"+base, "@"+base+"_rec"
	e.shapeMethodRecs[key] = rec
	e.registerFnMeta(fn, "get "+m.name, 0, fnKindPlain)
	e.emitGlobal(fmt.Sprintf("%s = internal constant { ptr, ptr, i64 } { ptr %s, ptr null, i64 0 }", rec, fn))

	restore := e.sharedBody(e.beginDetachedFunc())
	e.ensureShapeRuntime()
	e.declareFn("__kml_proto_get", "declare i64 @__kml_proto_get(i64, ptr, ptr, ptr, ptr)")
	ok := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", ok))
	probeRef := "null"
	if probe != "" {
		probeRef = e.internString(probe)
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_proto_get(i64 %%this, ptr %s, ptr %s, ptr %s, ptr %s)", r, probeRef, e.internString(class), e.internString(m.name), ok))
	okv, okb := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", okv, ok))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", okb, okv))
	goodL, badL := e.freshLabel("protog.ok"), e.freshLabel("protog.bad")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", okb, goodL, badL))
	e.emitLabel(badL)
	e.emitThrowTypeErrorValue(e.emitReceiverMessage("Method get "+class+".prototype."+m.name+" called on incompatible receiver ", "%this"))
	e.emitLabel(goodL)
	e.emitTerminator(fmt.Sprintf("ret i64 %s", r))
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc(fmt.Sprintf("internal i64 %s(ptr %%env, i64 %%this, i64 %%argc, ptr %%argv)", fn), body)
	return rec
}

// protoAccessorClass reports whether class's readonly properties are its
// prototype's getters (a host layout's: Map's size, ArrayBuffer's
// byteLength), rather than each value's own data (an array's length).
func protoAccessorClass(class string) bool {
	return !dataLengthClasses[class] && primProtoTypeof[class] == ""
}

// dataLengthClasses hold their readonly properties as each value's own
// data (an array's or a string's length), or have none (a promise).
var dataLengthClasses = map[string]bool{"Array": true, "String": true, "Promise": true}

// builtinProtoClass reports whether id names a builtin class whose
// instances are host values, so id.prototype is its prototype object.
func (e *Emitter) builtinProtoClass(id *ast.Identifier) bool {
	if _, user := e.classes[id.Name]; user || e.isShadowedByLocal(id.Name) {
		return false
	}
	if _, ok := hostProtoClasses[id.Name]; !ok && !isErrorKindName(id.Name) {
		return false
	}
	c := e.front()
	if c == nil || !e.isBuiltinGlobal(c, id) {
		return false
	}
	return isErrorKindName(id.Name) || len(e.hostMembers(id.Name)) > 0
}

// protoExtraAccessors are prototype accessors a class's declaration leaves
// out (ES2024's resizable buffers, which the declared library predates).
var protoExtraAccessors = map[string][]string{
	"ArrayBuffer":       {"maxByteLength", "resizable", "detached"},
	"SharedArrayBuffer": {"growable", "maxByteLength"},
}

// emitBuiltinProto is class.prototype: the class's one prototype object,
// its methods' records and `constructor` (the class), made on first read.
func (e *Emitter) emitBuiltinProto(class string) Value {
	// One builder per class, the program's own: its methods are the ones
	// the program reaches (proto_reach.go), whoever asks first, library
	// code included, whose units then stay the same in every program.
	fn := "@__kml_proto_get_" + llvmSafeSymbol(class)
	if e.protoBuilders == nil {
		e.protoBuilders = map[string]bool{}
	}
	if !e.protoBuilders[fn] {
		e.protoBuilders[fn] = true
		savedLib, savedShared, savedLit := e.inLib, e.sharedCode, e.sharedLitUnit
		e.inLib, e.sharedCode, e.sharedLitUnit = "", 0, ""
		restore := e.beginDetachedFunc()
		// Built once: a later read returns the bag its slot holds.
		slot := "@__kml_proto_obj_" + llvmSafeSymbol(class)
		if e.protoSlots == nil {
			e.protoSlots = map[string]bool{}
		}
		if !e.protoSlots[slot] {
			e.protoSlots[slot] = true
			e.emitGlobal(fmt.Sprintf("%s = internal global ptr null, align 8", slot))
		}
		have := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", have, slot))
		builtL, buildL := e.freshLabel("protoget.built"), e.freshLabel("protoget.build")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(have), buildL, builtL))
		e.emitLabel(builtL)
		e.emitTerminator("ret i64 " + e.emitDynObjBox(have).Ref)
		e.emitLabel(buildL)
		v := e.emitBuiltinProtoBag(class)
		e.emitTerminator("ret i64 " + v.Ref)
		body := e.allocas.String() + e.body.String()
		restore()
		e.inLib, e.sharedCode, e.sharedLitUnit = savedLib, savedShared, savedLit
		e.writeFunc("i64 "+fn+"()", body)
	}
	e.declareFn(fn[1:], "declare i64 "+fn+"()")
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 %s()", r, fn))
	return Value{Ref: r, Ty: TypeAny}
}

// emitBuiltinProtoBag builds class.prototype's object (emitBuiltinProto).
func (e *Emitter) emitBuiltinProtoBag(class string) Value {
	e.ensureDynObj()
	e.declareFn("__kml_class_proto", "declare ptr @__kml_class_proto(ptr, i64, ptr, ptr, ptr, ptr)")
	if isErrorKindName(class) {
		return e.emitErrorProtoBag(class)
	}
	slot := "@__kml_proto_obj_" + llvmSafeSymbol(class)
	if e.protoSlots == nil {
		e.protoSlots = map[string]bool{}
	}
	if !e.protoSlots[slot] {
		e.protoSlots[slot] = true
		e.emitGlobal(fmt.Sprintf("%s = internal global ptr null, align 8", slot))
	}
	var keys, vals, sets []string
	probe := ""
	for _, m := range e.hostMembers(class) {
		if m.method && !m.optional {
			probe = protoHiddenKey(class, m.name)
			break
		}
	}
	if protoAccessorClass(class) {
		for _, name := range protoExtraAccessors[class] {
			rec := e.protoGetterRecord(class, hostMember{name: name}, probe)
			w := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", w, rec))
			keys, vals, sets = append(keys, e.internString(name)), append(vals, w), append(sets, "0")
		}
	}
	for _, m := range e.hostMembers(class) {
		if !m.method && m.readonly && !m.optional && protoAccessorClass(class) {
			rec := e.protoGetterRecord(class, m, probe)
			w := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", w, rec))
			keys, vals, sets = append(keys, e.internString(m.name)), append(vals, w), append(sets, "0")
			continue
		}
		if !m.method || m.optional || !e.protoMethodReachable(m.name) {
			continue
		}
		rec := ""
		if valueProtoClassName(class) {
			rec = e.valueProtoRecord(class, m)
		} else if class == "Promise" {
			// A promise boxes as its own wrapper, whose methods are one
			// record each already (emit_anyprom.go).
			rec = e.promiseMethodRecord(m.name)
		} else {
			rec = e.protoMethodRecord(class, m)
		}
		keys = append(keys, e.internString(m.name))
		vals = append(vals, e.emitNbTagPtr(rec, kmlTagDynFunc))
		sets = append(sets, "-1")
	}
	// Array.prototype[Symbol.iterator] is its `values`, the one function.
	if class == "Array" {
		for i, k := range keys {
			if k == e.internString("values") {
				keys = append(keys, e.internString("@@iterator"))
				vals, sets = append(vals, vals[i]), append(sets, "-1")
				break
			}
		}
	}
	// constructor: the class's own value, as a read of its name gives it
	// (String's call signature's function), so the two are identical.
	ctor := e.emitNbTagPtr(e.internString(class), kmlTagFuncRef)
	if v, ok, err := e.builtinCtorValue(class); ok && err == nil {
		ctor = v.Ref
	}
	keys = append(keys, e.internString("constructor"))
	vals, sets = append(vals, ctor), append(sets, "-1")
	if hostProtoClasses[class] {
		keys = append(keys, e.internString("@@toStringTag"))
		vals, sets = append(vals, e.emitNbTagPtr(e.internString(class), kmlTagString)), append(sets, "-1")
	}
	bag := e.emitProtoBagCall(slot, keys, vals, sets, "null")
	if class == "Array" {
		// Array.prototype is itself an Array.
		e.declareFn("__kml_dynobj_array_exotic", "declare void @__kml_dynobj_array_exotic(ptr)")
		e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_array_exotic(ptr %s)", bag))
	}
	return e.emitDynObjBox(bag)
}

// emitProtoBagCall makes slot's prototype object once: keys[i] holding
// vals[i] (a method when sets[i] is -1, else an accessor), its own
// [[Prototype]] parent (null for Object.prototype).
func (e *Emitter) emitProtoBagCall(slot string, keys, vals, sets []string, parent string) string {
	n := len(keys)
	karr, varr, sarr := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca [%d x ptr], align 8", karr, n))
	e.emitAlloca(fmt.Sprintf("%s = alloca [%d x i64], align 8", varr, n))
	e.emitAlloca(fmt.Sprintf("%s = alloca [%d x i64], align 8", sarr, n))
	for i := range keys {
		kp, vp, sp := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr [%d x ptr], ptr %s, i64 0, i64 %d", kp, n, karr, i))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", keys[i], kp))
		e.emitInstr(fmt.Sprintf("%s = getelementptr [%d x i64], ptr %s, i64 0, i64 %d", vp, n, varr, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", vals[i], vp))
		e.emitInstr(fmt.Sprintf("%s = getelementptr [%d x i64], ptr %s, i64 0, i64 %d", sp, n, sarr, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sets[i], sp))
	}
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_class_proto(ptr %s, i64 %d, ptr %s, ptr %s, ptr %s, ptr %s)", bag, slot, n, karr, varr, sarr, parent))
	return bag
}

// errorProtoToString is Error.prototype.toString (ECMA-262 20.5.3.4).
const errorProtoToString = "(function toString() {\n" +
	"\tif (this === null || typeof this !== \"object\" && typeof this !== \"function\") throw new TypeError(\"Error.prototype.toString requires that 'this' be an Object\");\n" +
	"\tconst n = this.name, m = this.message;\n" +
	"\tconst name = n === undefined ? \"Error\" : `${n}`, msg = m === undefined ? \"\" : `${m}`;\n" +
	"\tif (name === \"\") return msg;\n" +
	"\tif (msg === \"\") return name;\n" +
	"\treturn name + \": \" + msg;\n" +
	"})"

// emitErrorProtoBag builds error kind class's prototype object: its name,
// an empty message and its constructor; Error.prototype also has toString,
// and every other kind's inherits Error.prototype.
func (e *Emitter) emitErrorProtoBag(class string) Value {
	slot := "@__kml_proto_obj_" + llvmSafeSymbol(class)
	parent := "null"
	if class != "Error" {
		pp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", pp, e.emitBuiltinProto("Error").Ref))
		parent = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", parent, pp))
	}
	str := func(s string) string { return e.emitNbTagPtr(e.internString(s), kmlTagString) }
	keys := []string{e.internString("constructor")}
	vals := []string{e.emitNbTagPtr(e.internString(class), kmlTagFuncRef)}
	if class != "DOMException" {
		// A DOMException's name and message are accessors of its instances.
		keys = append(keys, e.internString("name"), e.internString("message"))
		vals = append(vals, str(class), str(""))
	}
	if class == "Error" {
		prog, err := parser.Parse(errorProtoToString)
		if err == nil {
			err = sema.Prepare(prog)
		}
		var v Value
		if err == nil {
			fe := prog.Body[0].(*ast.ExpressionStatement).Expr.(*ast.FunctionExpression)
			v, err = e.emitDynFunctionExpression(fe, ast.Pos{})
		}
		if err == nil {
			v, err = e.emitBoxValue(v)
		}
		if err != nil {
			panic(fmt.Sprintf("Error.prototype.toString: %v", err))
		}
		keys, vals = append(keys, e.internString("toString")), append(vals, v.Ref)
	}
	sets := make([]string, len(keys))
	for i := range sets {
		sets[i] = "-1"
	}
	return e.emitDynObjBox(e.emitProtoBagCall(slot, keys, vals, sets, parent))
}

// hostProtoClasses are the ECMAScript classes whose instances box as host
// values (emit_hostbox.go), whose methods a host layout's shape getter
// lists, and Array and String (valueProtoRecord), each true when its prototype carries a Symbol.toStringTag of its
// name (Date's and RegExp's do not).
var hostProtoClasses = map[string]bool{
	"Map": true, "Set": true, "WeakMap": true, "WeakSet": true, "WeakRef": true, "FinalizationRegistry": true,
	"Promise": true, "DataView": true, "ArrayBuffer": true, "SharedArrayBuffer": true, "Date": false, "RegExp": false,
	"Array": false, "String": false,
	"Number": false, "Boolean": false, "Symbol": true, "BigInt": true,
}

// valueProtoClassName reports whether class's prototype methods take its
// values (valueProtoRecord) rather than a host box's layout.
func valueProtoClassName(class string) bool {
	return class == "Array" || class == "String" || primProtoTypeof[class] != ""
}

// primProtoTypeof is the typeof of a primitive class's values, whose
// prototype methods take only such a receiver (thisNumberValue and kin).
var primProtoTypeof = map[string]string{
	"Number": "number", "Boolean": "boolean", "Symbol": "symbol", "BigInt": "bigint",
}

// emitPrimReceiver is %this for a prototype method of a primitive class:
// a TypeError unless typeof %this is prim.
func (e *Emitter) emitPrimReceiver(class, prim, method string) string {
	this := Value{Ref: "%this", Ty: TypeAny}
	tv, err := e.emitDynamicTypeof(this)
	if err != nil {
		return "%this"
	}
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitInstr(fmt.Sprintf("store i64 %%this, ptr %s, align 8", slot))
	ok := e.emitStrEq(tv.Ref, e.internString(prim))
	goodL, wrapL, badL := e.freshLabel("precv.ok"), e.freshLabel("precv.wrap"), e.freshLabel("precv.bad")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, goodL, wrapL))
	e.emitLabel(wrapL)
	// A wrapper object of the class (`Object(false)`): its primitive value,
	// as its own valueOf answers it.
	e.ensureShapeRuntime()
	e.declareFn("__kml_proto_call", "declare i64 @__kml_proto_call(i64, ptr, i64, ptr, ptr)")
	okSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", okSlot))
	e.emitInstr(fmt.Sprintf("store i32 0, ptr %s, align 4", okSlot))
	pv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_proto_call(i64 %%this, ptr %s, i64 0, ptr null, ptr %s)", pv, e.internString(protoHiddenKey(class, "valueOf")), okSlot))
	okv, okb := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", okv, okSlot))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", okb, okv))
	unwrapL := e.freshLabel("precv.unwrap")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", okb, unwrapL, badL))
	e.emitLabel(unwrapL)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", pv, slot))
	e.emitTerminator("br label %" + goodL)
	e.emitLabel(badL)
	e.emitThrowTypeError(class + ".prototype." + method + " requires that 'this' be a " + class)
	e.emitLabel(goodL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, slot))
	return r
}

// emitReceiverMessage is prefix followed by how Node names the receiver
// word recv in a TypeError: `#<Set>` for an object, a primitive's string.
func (e *Emitter) emitReceiverMessage(prefix, recv string) string {
	if !e.recvMsgEmitted {
		e.recvMsgEmitted = true
		restore := e.sharedBody(e.beginDetachedFunc())
		msg := e.emitReceiverMessageInline("%prefix", "%recv")
		e.emitTerminator("ret ptr " + msg)
		body := e.allocas.String() + e.body.String()
		restore()
		e.writeFunc("internal ptr @__kml_recv_msg(ptr %prefix, i64 %recv)", body)
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_recv_msg(ptr %s, i64 %s)", r, e.internString(prefix), recv))
	return r
}

// emitReceiverMessageInline is emitReceiverMessage's body, with prefix a
// string register: emitted once, as __kml_recv_msg, which every
// incompatible-receiver throw calls.
func (e *Emitter) emitReceiverMessageInline(prefix, recv string) string {
	e.ensureShapeRuntime()
	e.declareFn("__kml_receiver_desc", "declare ptr @__kml_receiver_desc(i64)")
	desc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_receiver_desc(i64 %s)", desc, recv))
	objL, primL, doneL := e.freshLabel("recv.obj"), e.freshLabel("recv.prim"), e.freshLabel("recv.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(desc), primL, objL))
	e.emitLabel(primL)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", recv, slot))
	e.define("__kml_recv", Symbol{Ptr: slot, Ty: TypeAny})
	str, err := e.emitGlobalStringConv([]ast.Expression{ast.NewIdentifier("__kml_recv", ast.Pos{})}, ast.Pos{})
	if err != nil {
		str = Value{Ref: e.internString(""), Ty: TypePtr}
	}
	primEnd := e.freshLabel("recv.primend")
	e.emitTerminator("br label %" + primEnd)
	e.emitLabel(primEnd)
	e.emitTerminator("br label %" + doneL)
	e.emitLabel(objL)
	e.emitTerminator("br label %" + doneL)
	e.emitLabel(doneL)
	name := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi ptr [ %s, %%%s ], [ %s, %%%s ]", name, str.Ref, primEnd, desc, objL))
	msg, err := e.emitStringConcat(Value{Ref: prefix, Ty: TypePtr}, Value{Ref: name, Ty: TypePtr})
	if err != nil {
		return prefix
	}
	return msg.Ref
}

// emitBuiltinProtoOf is the prototype object of the builtin class whose
// instance is the object cell, or null when it is none: a call to
// __kml_builtin_proto_of, generated once the program's host layouts are
// all known (emitBuiltinProtoOfFinalize).
func (e *Emitter) emitBuiltinProtoOf(cell string) string {
	e.ensureShapeRuntime()
	if !e.usedProtoOf {
		e.usedProtoOf = true
		e.emitGlobal("declare i64 @__kml_builtin_proto_of(ptr)")
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_builtin_proto_of(ptr %s)", r, cell))
	return r
}

// emitBuiltinProtoOfFinalize generates `i64 __kml_builtin_proto_of(ptr
// cell)`: the cell's layout class name against each builtin class the
// program boxes, and a boxed promise's wrapper by its header word.
func (e *Emitter) emitBuiltinProtoOfFinalize() {
	if !e.usedProtoOf {
		return
	}
	seen := map[string]bool{}
	var classes []string
	for _, h := range e.hostLayouts {
		if _, ok := hostProtoClasses[h.class]; ok && !seen[h.class] {
			seen[h.class] = true
			classes = append(classes, h.class)
		}
	}
	restore := e.beginDetachedFunc()
	if e.genProtos && e.fnDecls["__kml_gen_proto_off"] {
		// A generator instance: the prototype it recorded.
		k, isGen := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_shape_generator_kind(ptr %%cell)", k))
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", isGen, k))
		genL, restL := e.freshLabel("protoof.gen"), e.freshLabel("protoof.notgen")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isGen, genL, restL))
		e.emitLabel(genL)
		gp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_shape_generator_proto(ptr %%cell, i32 %s)", gp, k))
		e.emitTerminator(fmt.Sprintf("ret i64 %s", gp))
		e.emitLabel(restL)
	}
	if e.usedPromiseBox {
		// A boxed promise is its wrapper, known by its header word.
		hdr, isProm := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %%cell, align 8", hdr))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isProm, e.headerIdentity(hdr), e.promiseBoxHeader()))
		promL, restL := e.freshLabel("protoof.promise"), e.freshLabel("protoof.rest")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isProm, promL, restL))
		e.emitLabel(promL)
		e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitBuiltinProto("Promise").Ref))
		e.emitLabel(restL)
	}
	// A user class's instance: its class's prototype, the most derived
	// class first (an instance of D is one of its base C too).
	var users []string
	for name, info := range e.classes {
		if !info.Ty.IsDynamic() && (info.TagID&kmlHdrIDMask < kmlStableIDBase || e.classProtoSlotted(name)) {
			users = append(users, name)
		}
	}
	sort.Slice(users, func(i, j int) bool {
		di, dj := len(e.classes[users[i]].AncestorChain), len(e.classes[users[j]].AncestorChain)
		if di != dj {
			return di > dj
		}
		return users[i] < users[j]
	})
	if len(users) > 0 {
		cw, word := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %%cell to i64", cw))
		e.emitInstr(fmt.Sprintf("%s = or i64 %s, 1", word, cw))
		for _, cls := range users {
			hit, err := e.emitAnyInstanceOfClass(Value{Ref: word, Ty: TypeAny}, cls)
			if err != nil {
				continue
			}
			yesL, nextL := e.freshLabel("protoof.cls"), e.freshLabel("protoof.clsnext")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hit.Ref, yesL, nextL))
			e.emitLabel(yesL)
			e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitClassProto(cls).Ref))
			e.emitLabel(nextL)
		}
	}
	if e.usedErrorProto() {
		// A builtin error: its kind's prototype, by the kind in field 0.
		f0, flagged, isErr := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %%cell, align 8", f0))
		e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", flagged, f0, errorTypeIDFlag))
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isErr, flagged))
		errL, restL := e.freshLabel("protoof.err"), e.freshLabel("protoof.noterr")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isErr, errL, restL))
		e.emitLabel(errL)
		kind := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", kind, f0, errorTypeIDFlag-1))
		for i, k := range errorKinds {
			hit, yesL, nextL := e.freshReg(), e.freshLabel("protoof.kind"), e.freshLabel("protoof.kindnext")
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", hit, kind, i))
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hit, yesL, nextL))
			e.emitLabel(yesL)
			e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitBuiltinProto(k).Ref))
			e.emitLabel(nextL)
		}
		e.emitTerminator("br label %" + restL)
		e.emitLabel(restL)
	}
	name := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_shape_class_name(ptr %%cell)", name))
	noneL, someL := e.freshLabel("protoof.none"), e.freshLabel("protoof.some")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(name), noneL, someL))
	e.emitLabel(someL)
	e.ensureStrcmp()
	for _, class := range classes {
		eq, hit := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", eq, name, e.internString(class)))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", hit, eq))
		yesL, nextL := e.freshLabel("protoof.yes"), e.freshLabel("protoof.next")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hit, yesL, nextL))
		e.emitLabel(yesL)
		e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitBuiltinProto(class).Ref))
		e.emitLabel(nextL)
	}
	e.emitTerminator("br label %" + noneL)
	e.emitLabel(noneL)
	e.emitTerminator(fmt.Sprintf("ret i64 %d", nbNull))
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("internal i64 @__kml_builtin_proto_of(ptr %cell)", body)
}

// valueProtoRecord returns class.prototype's record for method m, for
// Array and String, whose values are not host boxes: one per method, so
// `Array.prototype.map === a.map` holds. Its adapter makes the call
// `this.m(…)` on the receiver as that class's value (emitArrayReceiver,
// emitStringReceiver) with the arguments it was given (the value's own
// typed method, as an `any` value's call reaches it): one branch per count
// of fixed parameters passed. That call takes no spread, so of the methods
// with a rest parameter push, unshift and concat take their arguments one
// call each (unshift from the last), and splice inserts its items one
// splice each (emitSpliceItems).
func (e *Emitter) valueProtoRecord(class string, m hostMember) string {
	if class == "Array" && m.name == "values" {
		// Array.prototype.values (and [Symbol.iterator]) is the array
		// iterator's own maker, the one function every read gives.
		return e.arrayValuesRecord()
	}
	key := "proto." + class + "." + m.name
	// A method the program links an array-like algorithm for (kml_array.ts)
	// is a record of its own: the same in every program that links it.
	generic := ""
	if class == "Array" {
		generic = e.globalLinks["__kml_Array_generic_"+m.name]
	}
	if generic != "" {
		key += ".generic"
	}
	if e.shapeMethodRecs == nil {
		e.shapeMethodRecs = map[string]string{}
	}
	if rec, ok := e.shapeMethodRecs[key]; ok {
		return rec
	}
	base := "__kml_proto_m_" + class + "_" + llvmSafeSymbol(m.name)
	if generic != "" {
		base += "_generic"
	}
	fn, rec := "@"+base, "@"+base+"_rec"
	e.shapeMethodRecs[key] = rec
	fixed := len(m.sig.Params)
	rest := m.sig.HasRestParam()
	if rest {
		fixed--
	}
	e.registerFnMeta(fn, m.name, fixed, fnKindPlain)
	e.emitGlobal(fmt.Sprintf("%s = internal constant { ptr, ptr, i64 } { ptr %s, ptr null, i64 %d }", rec, fn, fixed))

	// The same in every program: a library unit of its own (libOwner).
	restore := e.sharedBody(e.beginDetachedFunc())
	pos := ast.Pos{}
	if generic != "" {
		e.emitArrayLikeMethod(m.name, generic)
	}
	recv := ""
	if class == "String" {
		recv = e.emitStringReceiver(m.name)
	} else if prim := primProtoTypeof[class]; prim != "" {
		recv = e.emitPrimReceiver(class, prim, m.name)
	} else if typedKindIndex(class) < 0 {
		recv = e.emitArrayReceiver(m.name)
	}
	var self ast.Expression
	if typedKindIndex(class) >= 0 {
		// The TypedArray's dispatcher (typedDispatchRecord) has checked
		// the receiver's kind.
		self = e.bindValue(e.convert(Value{Ref: "%this", Ty: TypeAny}, e.resolveTypeName(class)), pos)
	} else {
		self = e.bindValue(Value{Ref: recv, Ty: TypeAny}, pos)
	}
	// call is recv.m(args) as a box; false, with nothing emitted, when it
	// does not compile.
	call := func(recv ast.Expression, args []ast.Expression) (string, bool) {
		body, allocas, done := e.body.String(), e.allocas.String(), e.blockDone
		v, err := e.emitExpr(ast.NewCallExpression(ast.NewMemberExpression(recv, m.name, pos), args, pos))
		if err == nil && v.Ty.IR != "" && v.Ty.IR != "void" && !v.Ty.IsDynamic() {
			v, err = e.emitBoxValue(v)
		}
		if err != nil {
			e.body.Reset()
			e.body.WriteString(body)
			e.allocas.Reset()
			e.allocas.WriteString(allocas)
			e.blockDone = done
			return "", false
		}
		if v.Ty.IR == "" || v.Ty.IR == "void" {
			return fmt.Sprint(nbUndefined), true
		}
		return v.Ref, true
	}
	argAt := func(i string) ast.Expression {
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %%p_argv, i64 %s", slot, i))
		w := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", w, slot))
		return e.bindValue(Value{Ref: w, Ty: TypeAny}, pos)
	}
	// callFit is the call with the c arguments passed, or, when the typed
	// method takes no such count, the nearest it takes: an argument it
	// ignores (a thisArg) dropped, a missing one undefined.
	callFit := func(c int) {
		var passed []ast.Expression
		for i := 0; i < c; i++ {
			passed = append(passed, argAt(fmt.Sprint(i)))
		}
		undef := ast.NewIdentifier("undefined", pos)
		var tries [][]ast.Expression
		for k := c; k >= 0; k-- {
			tries = append(tries, passed[:k])
		}
		for k := c + 1; k <= fixed; k++ {
			pad := append([]ast.Expression{}, passed...)
			for len(pad) < k {
				pad = append(pad, undef)
			}
			tries = append(tries, pad)
		}
		for _, args := range tries {
			if r, ok := call(self, args); ok {
				if !e.blockDone {
					e.emitTerminator("ret i64 " + r)
				}
				return
			}
		}
		if !e.blockDone {
			e.emitThrowTypeError(class + ".prototype." + m.name + " cannot be called this way")
		}
	}
	argc := func(op string, c int) {
		hit := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp %s i64 %%p_argc, %d", hit, op, c))
		yesL, nextL := e.freshLabel("aproto.argc"), e.freshLabel("aproto.next")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hit, yesL, nextL))
		e.emitLabel(yesL)
		callFit(c)
		e.emitLabel(nextL)
	}
	switch {
	case !rest:
		for c := 0; c < fixed; c++ {
			argc("eq", c)
		}
		callFit(fixed)
	case m.name == "splice":
		for c := 0; c <= fixed; c++ {
			argc("eq", c)
		}
		e.emitSpliceItems(self, call, argAt)
	default:
		// push, unshift, concat: one argument per call.
		argc("eq", 0)
		res, recvSlot, iSlot := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", res))
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", recvSlot))
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", iSlot))
		e.emitInstr(fmt.Sprintf("store i64 %%this, ptr %s, align 8", recvSlot))
		first, step := "0", "1"
		if m.name == "unshift" {
			last := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = sub i64 %%p_argc, 1", last))
			first, step = last, "-1"
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", first, iSlot))
		condL, bodyL, doneL := e.freshLabel("aproto.cond"), e.freshLabel("aproto.body"), e.freshLabel("aproto.done")
		e.emitTerminator("br label %" + condL)
		e.emitLabel(condL)
		i := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, iSlot))
		lo, hi, in := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", lo, i))
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %%p_argc", hi, i))
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", in, lo, hi))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", in, bodyL, doneL))
		e.emitLabel(bodyL)
		recvW := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", recvW, recvSlot))
		recv := e.bindValue(Value{Ref: recvW, Ty: TypeAny}, pos)
		r, ok := call(recv, []ast.Expression{argAt(i)})
		if !ok {
			if !e.blockDone {
				e.emitThrowTypeError(class + ".prototype." + m.name + " cannot be called this way")
			}
		} else {
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", r, res))
			if m.name == "concat" {
				// The next item concatenates onto this result.
				e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", r, recvSlot))
			}
			next := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", next, i, step))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, iSlot))
			e.emitTerminator("br label %" + condL)
		}
		e.emitLabel(doneL)
		out := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, res))
		e.emitTerminator("ret i64 " + out)
	}
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc(fmt.Sprintf("internal i64 %s(ptr %%env, i64 %%this, i64 %%p_argc, ptr %%p_argv)", fn), body)
	return rec
}

// emitArrayLikeMethod sends an object receiver that is not an array
// (`Array.prototype.pop.call({ length: 2 })`) to the method's array-like
// algorithm fn, which takes the receiver and the call's arguments as one
// array and works on the object's own properties: copying it into an array
// first would run a mutator on the copy, and allocate `length` elements
// however few the object holds. An array, a string, a primitive or a
// nullish receiver goes on to the array path.
func (e *Emitter) emitArrayLikeMethod(method, fn string) {
	tag, _ := e.emitUnboxTagPayload(Value{Ref: "%this", Ty: TypeAny})
	isObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
	for _, k := range []int{kmlTagFuncRef, kmlTagDynObject, kmlTagDynFunc, kmlTagError} {
		hit, any := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", hit, tag, k))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", any, isObj, hit))
		isObj = any
	}
	// A primitive receiver is ToObject'd (`Array.prototype.pop.call("ab")`
	// works on a String wrapper); null and undefined throw.
	isPrim, isNullish := "false", "false"
	for _, k := range []int{kmlTagString, kmlTagInt, kmlTagFloat, kmlTagBoolean} {
		hit, any := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", hit, tag, k))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", any, isPrim, hit))
		isPrim = any
	}
	for _, k := range []int{kmlTagNull, kmlTagUndefined} {
		hit, any := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", hit, tag, k))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", any, isNullish, hit))
		isNullish = any
	}
	likeL, arrL := e.freshLabel("alike.generic"), e.freshLabel("alike.array")
	primL, notPrimL, nullL, notNullL := e.freshLabel("alike.prim"), e.freshLabel("alike.notprim"), e.freshLabel("alike.nullish"), e.freshLabel("alike.notnullish")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNullish, nullL, notNullL))
	e.emitLabel(nullL)
	e.emitThrowTypeError("Array.prototype." + method + " called on null or undefined")
	e.emitLabel(notNullL)
	pos := ast.Pos{}
	this := Value{Ref: "%this", Ty: TypeAny}
	recvSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", recvSlot))
	e.emitInstr(fmt.Sprintf("store i64 %%this, ptr %s, align 8", recvSlot))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isPrim, primL, notPrimL))
	e.emitLabel(primL)
	if w, err := e.emitExpr(ast.NewCallExpression(ast.NewIdentifier("Object", pos), []ast.Expression{e.bindValue(this, pos)}, pos)); err == nil {
		if b, err := e.emitBoxValue(w); err == nil {
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", b.Ref, recvSlot))
		}
	}
	e.emitTerminator("br label %" + likeL)
	e.emitLabel(notPrimL)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, likeL, arrL))
	e.emitLabel(likeL)
	recv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", recv, recvSlot))
	hdr, _ := e.emitArgvRestArray(0, TypeAny)
	args := []ast.Expression{e.bindValue(Value{Ref: recv, Ty: TypeAny}, pos), e.bindValue(Value{Ref: hdr, Ty: ArrayOf(TypeAny)}, pos)}
	v, err := e.emitExpr(ast.NewCallExpression(ast.NewIdentifier(fn, pos), args, pos))
	if err == nil && !v.Ty.IsDynamic() {
		v, err = e.emitBoxValue(v)
	}
	if err != nil {
		e.emitThrowTypeError("Array.prototype." + method + " cannot be called this way")
	} else if !e.blockDone {
		e.emitTerminator("ret i64 " + v.Ref)
	}
	e.emitLabel(arrL)
}

// typedKinds are the TypedArray classes, in __kml_any_typed_index's order.
var typedKinds = []string{"Float64Array", "Float32Array", "BigInt64Array", "BigUint64Array",
	"Int32Array", "Uint32Array", "Int16Array", "Uint16Array", "Int8Array", "Uint8Array", "Uint8ClampedArray"}

// typedIncompatibleReceiver are the %TypedArray%.prototype methods whose
// non-TypedArray receiver V8 reports as incompatible, naming it, rather
// than "this is not a typed array.".
var typedIncompatibleReceiver = map[string]bool{
	"at": true, "join": true, "slice": true, "sort": true, "subarray": true,
	"toReversed": true, "toSorted": true, "toLocaleString": true,
}

// typedKindIndex is class's index in typedKinds, else -1.
func typedKindIndex(class string) int {
	for i, k := range typedKinds {
		if k == class {
			return i
		}
	}
	return -1
}

// typedDispatchRecord returns %TypedArray%.prototype's record for method
// name: one for every kind, as in JavaScript, calling the method of the
// receiver's own kind (valueProtoRecord); a TypeError on any other
// receiver.
func (e *Emitter) typedDispatchRecord(name string) string {
	key := "proto.TypedArray." + name
	if e.shapeMethodRecs == nil {
		e.shapeMethodRecs = map[string]string{}
	}
	if rec, ok := e.shapeMethodRecs[key]; ok {
		return rec
	}
	base := "__kml_proto_m_TypedArray_" + llvmSafeSymbol(name)
	fn, rec := "@"+base, "@"+base+"_rec"
	e.shapeMethodRecs[key] = rec
	arity := 0
	type arm struct {
		idx int
		fn  string
	}
	var arms []arm
	for i, k := range typedKinds {
		m, ok := e.classMethod(k, name)
		if !ok {
			continue
		}
		if arity == 0 {
			arity = len(m.sig.Params)
			if m.sig.HasRestParam() {
				arity--
			}
		}
		arms = append(arms, arm{i, strings.TrimSuffix(e.valueProtoRecord(k, m), "_rec")})
	}
	e.registerFnMeta(fn, name, arity, fnKindPlain)
	e.emitGlobal(fmt.Sprintf("%s = internal constant { ptr, ptr, i64 } { ptr %s, ptr null, i64 %d }", rec, fn, arity))

	restore := e.sharedBody(e.beginDetachedFunc())
	e.declareFn("__kml_any_typed_index", "declare i64 @__kml_any_typed_index(i64)")
	k := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_any_typed_index(i64 %%this)", k))
	badL := e.freshLabel("tproto.bad")
	var cases strings.Builder
	labels := make([]string, len(arms))
	for i, a := range arms {
		labels[i] = e.freshLabel("tproto.k")
		fmt.Fprintf(&cases, " i64 %d, label %%%s", a.idx, labels[i])
	}
	e.emitTerminator(fmt.Sprintf("switch i64 %s, label %%%s [%s ]", k, badL, cases.String()))
	for i, a := range arms {
		e.emitLabel(labels[i])
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 %s(ptr %%env, i64 %%this, i64 %%argc, ptr %%argv)", r, a.fn))
		e.emitTerminator("ret i64 " + r)
	}
	e.emitLabel(badL)
	if typedIncompatibleReceiver[name] {
		e.emitThrowTypeErrorValue(e.emitReceiverMessage("Method %TypedArray%.prototype."+name+" called on incompatible receiver ", "%this"))
	} else {
		e.emitThrowTypeError("this is not a typed array.")
	}
	if !e.blockDone {
		e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
	}
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc(fmt.Sprintf("internal i64 %s(ptr %%env, i64 %%this, i64 %%argc, ptr %%argv)", fn), body)
	return rec
}

// typedGetterRecord returns %TypedArray%.prototype's getter record for
// property prop (`byteLength`): the receiver's own, a TypeError naming any
// other receiver.
func (e *Emitter) typedGetterRecord(prop string) string {
	key := "protoget.TypedArray." + prop
	if e.shapeMethodRecs == nil {
		e.shapeMethodRecs = map[string]string{}
	}
	if rec, ok := e.shapeMethodRecs[key]; ok {
		return rec
	}
	base := "__kml_proto_g_TypedArray_" + llvmSafeSymbol(prop)
	fn, rec := "@"+base, "@"+base+"_rec"
	e.shapeMethodRecs[key] = rec
	e.registerFnMeta(fn, "get "+prop, 0, fnKindPlain)
	e.emitGlobal(fmt.Sprintf("%s = internal constant { ptr, ptr, i64 } { ptr %s, ptr null, i64 0 }", rec, fn))

	restore := e.sharedBody(e.beginDetachedFunc())
	e.declareFn("__kml_any_typed_index", "declare i64 @__kml_any_typed_index(i64)")
	k, ok := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_any_typed_index(i64 %%this)", k))
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", ok, k))
	goodL, badL := e.freshLabel("tget.ok"), e.freshLabel("tget.bad")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, goodL, badL))
	e.emitLabel(badL)
	e.emitThrowTypeErrorValue(e.emitReceiverMessage("Method get %TypedArray%.prototype."+prop+" called on incompatible receiver ", "%this"))
	e.emitLabel(goodL)
	v, err := e.emitDynAnyMemberGetNamed(Value{Ref: "%this", Ty: TypeAny}, e.internString(prop), prop, ast.Pos{})
	if err != nil || e.blockDone {
		if !e.blockDone {
			e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
		}
	} else {
		e.emitTerminator("ret i64 " + v.Ref)
	}
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc(fmt.Sprintf("internal i64 %s(ptr %%env, i64 %%this, i64 %%argc, ptr %%argv)", fn), body)
	return rec
}

// emitProtoBag is __kml_class_proto over keys and their values: a data
// property where sets[i] is -1, else an accessor (vals[i] and sets[i] its
// getter and setter record words).
func (e *Emitter) emitProtoBag(slot string, keys, vals, sets []string, parent string) string {
	e.ensureDynObj()
	e.declareFn("__kml_class_proto", "declare ptr @__kml_class_proto(ptr, i64, ptr, ptr, ptr, ptr)")
	n := len(keys)
	karr, varr, sarr := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca [%d x ptr], align 8", karr, n))
	e.emitAlloca(fmt.Sprintf("%s = alloca [%d x i64], align 8", varr, n))
	e.emitAlloca(fmt.Sprintf("%s = alloca [%d x i64], align 8", sarr, n))
	for i := range keys {
		kp, vp, sp := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr [%d x ptr], ptr %s, i64 0, i64 %d", kp, n, karr, i))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", keys[i], kp))
		e.emitInstr(fmt.Sprintf("%s = getelementptr [%d x i64], ptr %s, i64 0, i64 %d", vp, n, varr, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", vals[i], vp))
		e.emitInstr(fmt.Sprintf("%s = getelementptr [%d x i64], ptr %s, i64 0, i64 %d", sp, n, sarr, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sets[i], sp))
	}
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_class_proto(ptr %s, i64 %d, ptr %s, ptr %s, ptr %s, ptr %s)", bag, slot, n, karr, varr, sarr, parent))
	return bag
}

// protoSlot is the global holding a prototype object named name (null
// until first made).
func (e *Emitter) protoSlot(name string) string {
	slot := "@__kml_proto_obj_" + llvmSafeSymbol(name)
	if e.protoSlots == nil {
		e.protoSlots = map[string]bool{}
	}
	if !e.protoSlots[slot] {
		e.protoSlots[slot] = true
		e.emitGlobal(fmt.Sprintf("%s = internal global ptr null, align 8", slot))
	}
	return slot
}

// emitTypedArrayProto is %TypedArray%.prototype, every TypedArray kind's
// prototype's [[Prototype]]: the methods (typedDispatchRecord) and the
// buffer, byteLength, byteOffset and length getters, as a bag pointer.
func (e *Emitter) emitTypedArrayProto() string {
	var keys, vals, sets []string
	for _, p := range []string{"buffer", "byteLength", "byteOffset", "length"} {
		w := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", w, e.typedGetterRecord(p)))
		keys, vals, sets = append(keys, e.internString(p)), append(vals, w), append(sets, "0")
	}
	for _, m := range e.hostMembers("Uint8Array") {
		if !m.method || m.optional || !e.protoMethodReachable(m.name) {
			continue
		}
		keys = append(keys, e.internString(m.name))
		vals, sets = append(vals, e.emitNbTagPtr(e.typedDispatchRecord(m.name), kmlTagDynFunc)), append(sets, "-1")
	}
	return e.emitProtoBag(e.protoSlot("TypedArray"), keys, vals, sets, "null")
}

// emitTypedKindProto is class.prototype for a TypedArray kind: its
// constructor and BYTES_PER_ELEMENT, over %TypedArray%.prototype.
func (e *Emitter) emitTypedKindProto(class string) Value {
	parent := e.emitTypedArrayProto()
	ctor := e.emitNbTagPtr(e.internString(class), kmlTagFuncRef)
	if v, ok, err := e.builtinCtorValue(class); ok && err == nil {
		ctor = v.Ref
	}
	size := map[string]int{"Float64Array": 8, "Float32Array": 4, "BigInt64Array": 8, "BigUint64Array": 8,
		"Int32Array": 4, "Uint32Array": 4, "Int16Array": 2, "Uint16Array": 2}[class]
	if size == 0 {
		size = 1
	}
	bpe, err := e.emitBoxValue(Value{Ref: fmt.Sprintf("%d.0", size), Ty: TypeF64})
	if err != nil {
		bpe = Value{Ref: fmt.Sprint(nbUndefined)}
	}
	keys := []string{e.internString("constructor"), e.internString("BYTES_PER_ELEMENT")}
	return e.emitDynObjBox(e.emitProtoBag(e.protoSlot(class), keys, []string{ctor, bpe.Ref}, []string{"-1", "-1"}, parent))
}

// usesTypedArrays reports whether the program names a TypedArray class
// (so a value in `any` can be one, and its methods are dispatched).
func (e *Emitter) usesTypedArrays() bool {
	if e.prog == nil {
		return false
	}
	if used, ok := e.typedUse[e.prog]; ok {
		return used
	}
	used := false
	ast.Inspect(e.prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Identifier:
			used = used || typedKindIndex(x.Name) >= 0
		case *ast.NewExpression:
			used = used || typedKindIndex(x.ClassName) >= 0
		case *ast.NewTypedArrayExpression:
			used = true
		}
		return !used
	})
	if e.typedUse == nil {
		e.typedUse = map[*ast.Program]bool{}
	}
	e.typedUse[e.prog] = used
	return used
}

// typedMethodNames are the TypedArray method names the program names as a
// property (called or not), which its typed values in `any` answer.
func (e *Emitter) typedMethodNames() []string {
	if e.prog == nil || !e.usesTypedArrays() {
		return nil
	}
	names := map[string]bool{}
	ast.Inspect(e.prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.MemberExpression:
			names[x.Property] = true
		case *ast.StringLiteral:
			names[x.Value] = true
		}
		return true
	})
	var out []string
	for _, m := range e.hostMembers("Uint8Array") {
		if m.method && !m.optional && names[m.name] {
			out = append(out, m.name)
		}
	}
	return out
}

// emitArrayReceiver is an Array.prototype method's receiver %this as an
// array: itself when it is one, else Array.from of it (the methods are
// generic over array-likes; a mutation of a non-array receiver is not
// written back). A null or undefined receiver is Node's TypeError.
func (e *Emitter) emitArrayReceiver(method string) string {
	this := Value{Ref: "%this", Ty: TypeAny}
	tag, payload := e.emitUnboxTagPayload(this)
	isDyn, isStatic, isNull, isUndef := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isDyn, tag, kmlTagDynArray))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isStatic, tag, kmlTagArray))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isNull, tag, kmlTagNull))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isUndef, tag, kmlTagUndefined))
	nullish := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", nullish, isNull, isUndef))
	throwL, checkL := e.freshLabel("arecv.throw"), e.freshLabel("arecv.check")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", nullish, throwL, checkL))
	e.emitLabel(throwL)
	e.emitThrowTypeError("Array.prototype." + method + " called on null or undefined")
	e.emitLabel(checkL)
	staticL, otherL, fromL, doneL := e.freshLabel("arecv.static"), e.freshLabel("arecv.other"), e.freshLabel("arecv.from"), e.freshLabel("arecv.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isDyn, doneL, otherL))
	e.emitLabel(otherL)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isStatic, staticL, fromL))
	e.emitLabel(staticL)
	e.ensureDynJSONC()
	e.declareFn("__kml_anyarr_is_typed", "declare i32 @__kml_anyarr_is_typed(ptr)")
	box, typed, plain := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", box, payload))
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_anyarr_is_typed(ptr %s)", typed, box))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", plain, typed))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", plain, doneL, fromL))
	e.emitLabel(fromL)
	from := fmt.Sprint(nbUndefined)
	v, err := e.emitExpr(arrayFromCall(ast.Pos{}, []ast.Expression{e.bindValue(this, ast.Pos{})}))
	if err == nil && !v.Ty.IsDynamic() {
		v, err = e.emitBoxValue(v)
	}
	if err == nil {
		from = v.Ref
	}
	fromEnd := e.freshLabel("arecv.fromend")
	e.emitTerminator("br label %" + fromEnd)
	e.emitLabel(fromEnd)
	e.emitTerminator("br label %" + doneL)
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi i64 [ %%this, %%%s ], [ %%this, %%%s ], [ %s, %%%s ]", r, checkL, staticL, from, fromEnd))
	return r
}

// emitStringReceiver is a String.prototype method's receiver %this as a
// string: itself when it is one, else String of it. A null or undefined
// receiver is Node's TypeError.
func (e *Emitter) emitStringReceiver(method string) string {
	this := Value{Ref: "%this", Ty: TypeAny}
	tag, pay := e.emitUnboxTagPayload(this)
	isStr, isNull, isUndef, nullish := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isStr, tag, kmlTagString))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isNull, tag, kmlTagNull))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isUndef, tag, kmlTagUndefined))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", nullish, isNull, isUndef))
	throwL, checkL, convL, doneL := e.freshLabel("srecv.throw"), e.freshLabel("srecv.check"), e.freshLabel("srecv.conv"), e.freshLabel("srecv.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", nullish, throwL, checkL))
	e.emitLabel(throwL)
	e.emitThrowTypeError("String.prototype." + method + " called on null or undefined")
	e.emitLabel(checkL)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isStr, doneL, convL))
	e.emitLabel(convL)
	// ToString of a Symbol throws (String(sym) is the one form that names it).
	isObj, symL, objL, plainL := e.freshReg(), e.freshLabel("srecv.sym"), e.freshLabel("srecv.obj"), e.freshLabel("srecv.plain")
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, plainL))
	e.emitLabel(objL)
	optr, f0, bit, isSym := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", optr, pay))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", f0, optr))
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", bit, f0, symbolTypeIDFlag))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isSym, bit))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isSym, symL, plainL))
	e.emitLabel(symL)
	e.emitThrowTypeError("Cannot convert a Symbol value to a string")
	e.emitLabel(plainL)
	conv := fmt.Sprint(nbUndefined)
	if v, err := e.emitGlobalStringConv([]ast.Expression{e.bindValue(this, ast.Pos{})}, ast.Pos{}); err == nil {
		if b, err := e.emitBoxValue(v); err == nil {
			conv = b.Ref
		}
	}
	convEnd := e.freshLabel("srecv.convend")
	e.emitTerminator("br label %" + convEnd)
	e.emitLabel(convEnd)
	e.emitTerminator("br label %" + doneL)
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi i64 [ %%this, %%%s ], [ %s, %%%s ]", r, checkL, conv, convEnd))
	return r
}

// emitSpliceItems is `this.splice(start, deleteCount, ...items)` with three
// or more arguments as calls without a spread: the removal, then each item
// inserted at the start relative to the length before it (a negative start
// counts from the end, clamped to the array).
func (e *Emitter) emitSpliceItems(self ast.Expression, call func(ast.Expression, []ast.Expression) (string, bool), argAt func(string) ast.Expression) {
	pos := ast.Pos{}
	lenV, err := e.emitExpr(ast.NewMemberExpression(self, "length", pos))
	if err != nil {
		e.emitThrowTypeError("Array.prototype.splice cannot be called this way")
		return
	}
	n := e.convert(lenV, TypeF64)
	startV, err := e.emitExpr(argAt("0"))
	if err != nil {
		e.emitThrowTypeError("Array.prototype.splice cannot be called this way")
		return
	}
	sf := e.convert(startV, TypeF64)
	// ToIntegerOrInfinity, then relative to the length and clamped.
	isNaN, z := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fcmp uno double %s, %s", isNaN, sf.Ref, sf.Ref))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, double 0.0, double %s", z, isNaN, sf.Ref))
	tr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @llvm.trunc.f64(double %s)", tr, z))
	neg, rel, lo, hi := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fcmp olt double %s, 0.0", neg, tr))
	e.emitInstr(fmt.Sprintf("%s = fadd double %s, %s", rel, n.Ref, tr))
	e.emitInstr(fmt.Sprintf("%s = call double @llvm.maxnum.f64(double %s, double 0.0)", lo, rel))
	e.emitInstr(fmt.Sprintf("%s = call double @llvm.minnum.f64(double %s, double %s)", hi, tr, n.Ref))
	start := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, double %s, double %s", start, neg, lo, hi))
	e.declareFn("llvm.trunc.f64", "declare double @llvm.trunc.f64(double)")
	e.declareFn("llvm.maxnum.f64", "declare double @llvm.maxnum.f64(double, double)")
	e.declareFn("llvm.minnum.f64", "declare double @llvm.minnum.f64(double, double)")
	removed, ok := call(self, []ast.Expression{argAt("0"), argAt("1")})
	if !ok {
		if !e.blockDone {
			e.emitThrowTypeError("Array.prototype.splice cannot be called this way")
		}
		return
	}
	iSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", iSlot))
	e.emitInstr(fmt.Sprintf("store i64 2, ptr %s, align 8", iSlot))
	condL, bodyL, doneL := e.freshLabel("splice.cond"), e.freshLabel("splice.body"), e.freshLabel("splice.done")
	e.emitTerminator("br label %" + condL)
	e.emitLabel(condL)
	i := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, iSlot))
	more := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %%p_argc", more, i))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, doneL))
	e.emitLabel(bodyL)
	k, kf, at := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 2", k, i))
	e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", kf, k))
	e.emitInstr(fmt.Sprintf("%s = fadd double %s, %s", at, start, kf))
	atV := e.bindValue(Value{Ref: at, Ty: TypeF64}, pos)
	if _, ok := call(self, []ast.Expression{atV, ast.NewNumberLiteral("0", pos), argAt(i)}); !ok {
		if !e.blockDone {
			e.emitThrowTypeError("Array.prototype.splice cannot be called this way")
		}
	} else {
		next := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, iSlot))
		e.emitTerminator("br label %" + condL)
	}
	e.emitLabel(doneL)
	e.emitTerminator("ret i64 " + removed)
}

// classMethod is class's declared method name, or false.
func (e *Emitter) classMethod(class, name string) (hostMember, bool) {
	for _, m := range e.hostMembers(class) {
		if m.method && !m.optional && m.name == name {
			return m, true
		}
	}
	return hostMember{}, false
}

// methodValueNames are class's method names the program reads as a value
// (`a.map` not called, `a["map"]`): those a dynamic property read of its
// value answers (emitValueProtoGetFinalize).
func (e *Emitter) methodValueNames(class string) []string {
	if e.prog == nil {
		return nil
	}
	called := map[ast.Node]bool{}
	names := map[string]bool{}
	ast.Inspect(e.prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpression:
			called[x.Callee] = true
		case *ast.MemberExpression:
			if !called[x] {
				names[x.Property] = true
			}
		case *ast.StringLiteral:
			names[x.Value] = true
		}
		return true
	})
	// An array's [Symbol.iterator] is its `values`.
	if class == "Array" && names["iterator"] {
		names["values"] = true
	}
	var out []string
	for _, m := range e.hostMembers(class) {
		if m.method && !m.optional && names[m.name] {
			out = append(out, m.name)
		}
	}
	return out
}

// emitValueProtoGetFinalize generates `i64 __kml_array_proto_get(ptr key)`
// and `__kml_string_proto_get` when the program reads such a method as a
// value: the method's record for each such name, else undefined. An array's
// or a string's property read (dynjson.c, dynarr.c) asks it for a key that
// is not an index.
func (e *Emitter) emitValueProtoGetFinalize() {
	if e.programIndependent() {
		return
	}
	if names := e.typedMethodNames(); len(names) > 0 {
		restore := e.beginDetachedFunc()
		for _, name := range names {
			miss := e.emitKeyIs("%key", name)
			e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitNbTagPtr(e.typedDispatchRecord(name), kmlTagDynFunc)))
			e.emitLabel(miss)
		}
		e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
		body := e.allocas.String() + e.body.String()
		restore()
		e.writeFunc("i64 @__kml_typed_proto_get(ptr %key)", body)
	}
	for _, class := range []string{"Array", "String"} {
		names := e.methodValueNames(class)
		if len(names) == 0 {
			continue
		}
		restore := e.beginDetachedFunc()
		for _, name := range names {
			m, _ := e.classMethod(class, name)
			miss := e.emitKeyIs("%key", name)
			e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitNbTagPtr(e.valueProtoRecord(class, m), kmlTagDynFunc)))
			e.emitLabel(miss)
			if class == "Array" && name == "values" {
				miss := e.emitKeyIs("%key", "@@iterator")
				e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitNbTagPtr(e.valueProtoRecord(class, m), kmlTagDynFunc)))
				e.emitLabel(miss)
			}
		}
		e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
		body := e.allocas.String() + e.body.String()
		restore()
		e.writeFunc(fmt.Sprintf("i64 @__kml_%s_proto_get(ptr %%key)", strings.ToLower(class)), body)
	}
}

// valueProtoClass is Array or String for a static array or string type,
// whose method read as a value is valueProtoRecord's, else "".
func valueProtoClass(t Type) string {
	switch {
	case t.IsDynamic() || t.IsTypedArray() || t.IsBuffer():
		return ""
	case t.IsArray():
		return "Array"
	case isForOfStringTy(t):
		return "String"
	}
	return ""
}

// emitClassProto is the user class's prototype object, C.prototype, read
// through the class's one builder (`__kml_cproto_get_<C>`): a library
// class's reference row names it alike in every program.
func (e *Emitter) emitClassProto(className string) Value {
	fn := "@__kml_cproto_get_" + llvmSafeSymbol(className)
	if e.protoBuilders == nil {
		e.protoBuilders = map[string]bool{}
	}
	if !e.protoBuilders[fn] {
		e.protoBuilders[fn] = true
		savedLib, savedShared, savedLit := e.inLib, e.sharedCode, e.sharedLitUnit
		e.inLib, e.sharedCode, e.sharedLitUnit = "", 0, ""
		restore := e.beginDetachedFunc()
		v := e.emitClassProtoBag(className)
		e.emitTerminator("ret i64 " + v.Ref)
		body := e.allocas.String() + e.body.String()
		restore()
		e.inLib, e.sharedCode, e.sharedLitUnit = savedLib, savedShared, savedLit
		e.writeFunc("i64 "+fn+"()", body)
	}
	e.declareFn(fn[1:], "declare i64 "+fn+"()")
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 %s()", r, fn))
	return Value{Ref: r, Ty: TypeAny}
}

// emitClassProtoBag builds C.prototype (emitClassProto): its own methods'
// records (shapeMethodRecord, so `C.prototype.m === c.m`), its own accessors
// and `constructor` (C), in declaration order, its [[Prototype]] its base
// class's, made on first read.
func (e *Emitter) emitClassProtoBag(className string) Value {
	info := e.classes[className]
	parent := "null"
	var pv Value
	switch {
	case info.ErrorBaseKind != "" && info.BaseClass == info.ErrorBaseKind:
		// A class extending an error kind (`extends TypeError`).
		pv = e.emitBuiltinProto(info.ErrorBaseKind)
	case info.BaseClass != "":
		pv = e.emitClassProto(info.BaseClass)
	case info.HostAnnot != nil && hostProtoClassKnown(info.HostAnnot.Name):
		// A class extending a builtin (`extends Array<T>`): its prototype.
		pv = e.emitBuiltinProto(info.HostAnnot.Name)
	}
	if pv.Ref != "" {
		pp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", pp, pv.Ref))
		parent = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", parent, pp))
	}
	e.ensureDynObj()
	e.ensureNanBox()
	e.declareFn("__kml_class_proto", "declare ptr @__kml_class_proto(ptr, i64, ptr, ptr, ptr, ptr)")
	slot := e.classProtoSlot(className)
	keys := []string{e.internString("constructor")}
	ctor := fmt.Sprint(nbUndefined)
	if v, err := e.emitExpr(ast.NewIdentifier(className, ast.Pos{})); err == nil {
		if !v.Ty.IsDynamic() {
			v, err = e.emitBoxValue(v)
		}
		if err == nil {
			ctor = v.Ref
		}
	}
	vals := []string{ctor}
	sets := []string{"-1"}
	accessor := map[string]int{}
	recWord := func(m string) string {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", r, e.shapeMethodRecord(&info, info.Ty, m)))
		return r
	}
	for _, m := range info.MethodOrder {
		if info.MethodImplementor[m] != className {
			continue
		}
		// An abstract method has no function, and no property in JavaScript.
		if d := info.Methods[m]; d != nil && d.Body == nil {
			continue
		}
		get, set := strings.HasPrefix(m, "__kml_get_"), strings.HasPrefix(m, "__kml_set_")
		if get || set {
			key := strings.TrimPrefix(strings.TrimPrefix(m, "__kml_get_"), "__kml_set_")
			i, seen := accessor[key]
			if !seen {
				i = len(keys)
				accessor[key] = i
				keys = append(keys, e.internString(key))
				vals, sets = append(vals, "0"), append(sets, "0")
			}
			if get {
				vals[i] = recWord(m)
			} else {
				sets[i] = recWord(m)
			}
			continue
		}
		if key, _ := shapeMemberKey(m); key != "" {
			keys = append(keys, e.internString(key))
			vals = append(vals, e.emitNbTagPtr(e.shapeMethodRecord(&info, info.Ty, m), kmlTagDynFunc))
			sets = append(sets, "-1")
		}
	}
	// Generator methods hold no vtable slot, so MethodOrder leaves them out:
	// add them in declaration order.
	var gens []*ast.FunctionDeclaration
	for name, m := range info.Methods {
		if m != nil && m.IsGenerator && !m.IsStatic && info.MethodImplementor[name] == className {
			gens = append(gens, m)
		}
	}
	sort.Slice(gens, func(i, j int) bool {
		a, b := gens[i].GetPos(), gens[j].GetPos()
		return a.Line < b.Line || a.Line == b.Line && a.Col < b.Col
	})
	for _, m := range gens {
		if key, _ := shapeMemberKey(m.Name); key != "" {
			keys = append(keys, e.internString(key))
			vals = append(vals, e.emitNbTagPtr(e.shapeMethodRecord(&info, info.Ty, m.Name), kmlTagDynFunc))
			sets = append(sets, "-1")
		}
	}
	n := len(keys)
	karr, varr, sarr := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca [%d x ptr], align 8", karr, n))
	e.emitAlloca(fmt.Sprintf("%s = alloca [%d x i64], align 8", varr, n))
	e.emitAlloca(fmt.Sprintf("%s = alloca [%d x i64], align 8", sarr, n))
	for i := range keys {
		kp, vp, sp := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr [%d x ptr], ptr %s, i64 0, i64 %d", kp, n, karr, i))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", keys[i], kp))
		e.emitInstr(fmt.Sprintf("%s = getelementptr [%d x i64], ptr %s, i64 0, i64 %d", vp, n, varr, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", vals[i], vp))
		e.emitInstr(fmt.Sprintf("%s = getelementptr [%d x i64], ptr %s, i64 0, i64 %d", sp, n, sarr, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sets[i], sp))
	}
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_class_proto(ptr %s, i64 %d, ptr %s, ptr %s, ptr %s, ptr %s)", bag, slot, n, karr, varr, sarr, parent))
	return e.emitDynObjBox(bag)
}

// classProtoSlot is the global holding the user class's prototype object
// (null until emitClassProto first makes it).
// usedErrorProto reports whether the program made an error kind's
// prototype object, so an error's prototype can be one of them.
func (e *Emitter) usedErrorProto() bool {
	return e.protoBuilders["@__kml_proto_get_Error"]
}

// classProtoSlotted reports whether the program made className's
// prototype object (a library class's, read as `K.prototype`).
func (e *Emitter) classProtoSlotted(className string) bool {
	return e.protoSlots["@__kml_cproto_obj_"+llvmSafeSymbol(className)]
}

func (e *Emitter) classProtoSlot(className string) string {
	slot := "@__kml_cproto_obj_" + llvmSafeSymbol(className)
	if e.protoSlots == nil {
		e.protoSlots = map[string]bool{}
	}
	if !e.protoSlots[slot] {
		e.protoSlots[slot] = true
		e.emitGlobal(fmt.Sprintf("%s = internal global ptr null, align 8", slot))
	}
	return slot
}

// hostProtoClassKnown reports whether name is a builtin class whose
// prototype object emitBuiltinProto makes.
func hostProtoClassKnown(name string) bool {
	_, ok := hostProtoClasses[name]
	return ok || isErrorKindName(name)
}

// objProtoMethodSrc is Object.prototype's own methods as values, each the
// static `Object.prototype.m.call` form over its receiver (objectProtoCall).
var objProtoMethodSrc = [][2]string{
	{"hasOwnProperty", "(function hasOwnProperty(V) { return Object.prototype.hasOwnProperty.call(this, V); })"},
	{"propertyIsEnumerable", "(function propertyIsEnumerable(V) { return Object.prototype.propertyIsEnumerable.call(this, V); })"},
	{"toString", "(function toString() { return Object.prototype.toString.call(this); })"},
	{"toLocaleString", "(function toLocaleString() { return this.toString(); })"},
	{"valueOf", `(function valueOf() { "use strict"; if (this === null || this === undefined) throw new TypeError("Cannot convert undefined or null to object"); return Object(this); })`},
	{"isPrototypeOf", `(function isPrototypeOf(V) {
		"use strict";
		if (V === null || typeof V !== "object" && typeof V !== "function") return false;
		if (this === null || this === undefined) throw new TypeError("Object.prototype.isPrototypeOf called on null or undefined");
		const O = Object(this);
		let p = Object.getPrototypeOf(V);
		while (p !== null) { if (p === O) return true; p = Object.getPrototypeOf(p); }
		return false;
	})`},
	{"__defineGetter__", `(function __defineGetter__(P, getter) {
		"use strict"; if (this === null || this === undefined) throw new TypeError("Cannot convert undefined or null to object");
		const O = Object(this);
		if (typeof getter !== "function") throw new TypeError("Object.prototype.__defineGetter__: Expecting function");
		Object.defineProperty(O, P, { get: getter, enumerable: true, configurable: true });
	})`},
	{"__defineSetter__", `(function __defineSetter__(P, setter) {
		"use strict"; if (this === null || this === undefined) throw new TypeError("Cannot convert undefined or null to object");
		const O = Object(this);
		if (typeof setter !== "function") throw new TypeError("Object.prototype.__defineSetter__: Expecting function");
		Object.defineProperty(O, P, { set: setter, enumerable: true, configurable: true });
	})`},
	{"__lookupGetter__", `(function __lookupGetter__(P) {
		"use strict"; if (this === null || this === undefined) throw new TypeError("Cannot convert undefined or null to object");
		let O = Object(this);
		while (O !== null) {
			const d = Object.getOwnPropertyDescriptor(O, P);
			if (d !== undefined) return d.get;
			O = Object.getPrototypeOf(O);
		}
		return undefined;
	})`},
	{"__lookupSetter__", `(function __lookupSetter__(P) {
		"use strict"; if (this === null || this === undefined) throw new TypeError("Cannot convert undefined or null to object");
		let O = Object(this);
		while (O !== null) {
			const d = Object.getOwnPropertyDescriptor(O, P);
			if (d !== undefined) return d.set;
			O = Object.getPrototypeOf(O);
		}
		return undefined;
	})`},
}

// objProtoMember is the keys ensureObjProtoFill installs.
var objProtoMember = func() map[string]bool {
	m := map[string]bool{"constructor": true}
	for _, kv := range objProtoMethodSrc {
		m[kv[0]] = true
	}
	return m
}()

// ensureObjProtoFill defines (once) the objproto_fill hook, which installs
// objProtoMethodSrc's functions into Object.prototype's bag when it is
// made.
func (e *Emitter) ensureObjProtoFill() {
	if e.usedObjProtoFill {
		return
	}
	e.usedObjProtoFill = true
	e.ensureDynObj()
	e.declareFn("__kml_dynobj_set_hidden", "declare void @__kml_dynobj_set_hidden(ptr, ptr, i64)")
	// The same in every program: a unit of its own, its literals named by
	// position (sharedLitUnit).
	restore := e.sharedBody(e.beginDetachedFunc())
	defer restore()
	savedLit := e.sharedLitUnit
	e.sharedLitUnit = objProtoUnit
	defer func() { e.sharedLitUnit = savedLit }()
	for _, m := range objProtoMethodSrc {
		prog, err := parser.Parse(m[1])
		if err == nil {
			err = sema.Prepare(prog)
		}
		var v Value
		if err == nil {
			markObjectIntrinsics(prog)
			fe := prog.Body[0].(*ast.ExpressionStatement).Expr.(*ast.FunctionExpression)
			v, err = e.emitDynFunctionExpression(fe, ast.Pos{})
		}
		if err == nil {
			v, err = e.emitBoxValue(v)
		}
		if err != nil {
			panic(fmt.Sprintf("Object.prototype.%s: %v", m[0], err))
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set_hidden(ptr %%b, ptr %s, i64 %s)", e.internString(m[0]), v.Ref))
	}
	// __proto__: an accessor reading and setting the receiver's prototype.
	var recs [2]string
	for i, src := range []string{
		`(function () { return Object.getPrototypeOf(Object(this)); })`,
		`(function (proto) {
			if (this === null || this === undefined) throw new TypeError("Object.prototype.__proto__ called on null or undefined");
			if (proto !== null && typeof proto !== "object" && typeof proto !== "function") return undefined;
			if (typeof this !== "object" && typeof this !== "function") return undefined;
			Object.setPrototypeOf(this, proto);
		})`,
	} {
		prog, err := parser.Parse(src)
		if err == nil {
			err = sema.Prepare(prog)
		}
		var v Value
		if err == nil {
			markObjectIntrinsics(prog)
			v, err = e.emitDynFunctionExpression(prog.Body[0].(*ast.ExpressionStatement).Expr.(*ast.FunctionExpression), ast.Pos{})
		}
		if err == nil {
			v, err = e.emitBoxValue(v)
		}
		if err != nil {
			panic(fmt.Sprintf("Object.prototype.__proto__: %v", err))
		}
		pay := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", pay, v.Ref))
		recs[i] = e.emitIntToPtr(pay)
	}
	e.declareFn("__kml_dynobj_defacc_hidden", "declare void @__kml_dynobj_defacc_hidden(ptr, ptr, ptr, ptr)")
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_defacc_hidden(ptr %%b, ptr %s, ptr %s, ptr %s)", e.internString("__proto__"), recs[0], recs[1]))
	// constructor: Object, as a read of the name gives it (emitBuiltinProto).
	ctor := e.emitNbTagPtr(e.internString("Object"), kmlTagFuncRef)
	if v, ok, err := e.builtinCtorValue("Object"); ok && err == nil {
		ctor = v.Ref
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set_hidden(ptr %%b, ptr %s, i64 %s)", e.internString("constructor"), ctor))
	e.emitTerminator("ret void")
	e.writeFunc("void @__kml_objproto_fill(ptr %b)", e.allocas.String()+e.body.String())
}

// markObjectIntrinsics routes a parsed helper's `Object.f(…)` calls to the
// statics' intrinsics: the helper is not bound against their declarations.
func markObjectIntrinsics(prog *ast.Program) {
	ast.Inspect(prog, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpression); ok && c.Intrinsic == "" {
			if me, ok := c.Callee.(*ast.MemberExpression); ok {
				if id, ok := me.Object.(*ast.Identifier); ok && id.Name == "Object" {
					if _, known := intrinsics["Object."+me.Property]; known {
						c.Intrinsic = "Object." + me.Property
					}
				}
			}
		}
		return true
	})
}

// emitPrimProtoOfFinalize defines `i64 __kml_prim_proto_of(i64 v)`, the
// hook a dynamic read on a number or a boolean asks for its class's
// prototype (Number.prototype.toFixed as a value), when the program reads
// one of that class's methods so: the boxed bag, or 0.
func (e *Emitter) emitPrimProtoOfFinalize() {
	var classes []string
	for _, class := range []string{"Number", "Boolean"} {
		for _, m := range e.hostMembers(class) {
			if m.method && !m.optional && e.primReadKeys[m.name] {
				classes = append(classes, class)
				break
			}
		}
	}
	if len(classes) == 0 {
		return
	}
	restore := e.beginDetachedFunc()
	tag, _ := e.emitUnboxTagPayload(Value{Ref: "%v", Ty: TypeAny})
	for _, class := range classes {
		hit := e.freshReg()
		if class == "Number" {
			isI, isF := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isI, tag, kmlTagInt))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isF, tag, kmlTagFloat))
			e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", hit, isI, isF))
		} else {
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", hit, tag, kmlTagBoolean))
		}
		yesL, nextL := e.freshLabel("primproto.yes"), e.freshLabel("primproto.next")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hit, yesL, nextL))
		e.emitLabel(yesL)
		e.emitTerminator("ret i64 " + e.emitBuiltinProto(class).Ref)
		e.emitLabel(nextL)
	}
	e.emitTerminator("ret i64 0")
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("i64 @__kml_prim_proto_of(i64 %v)", body)
}
