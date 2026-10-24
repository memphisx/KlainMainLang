package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// genProtoNames are the property names through which a program can observe
// an object's prototype.
var genProtoNames = map[string]bool{"prototype": true, "__proto__": true, "isPrototypeOf": true, "getPrototypeOf": true, "setPrototypeOf": true}

// prepareGenProtos sets genProtos when the program names a prototype (a
// member or a string key). Only then does a generator instance record the
// prototype it inherits; otherwise it is the root, which nothing observes.
func (e *Emitter) prepareGenProtos() {
	if e.prog == nil {
		return
	}
	for _, st := range e.prog.Body {
		ast.Inspect(st, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.MemberExpression:
				e.genProtos = e.genProtos || genProtoNames[n.Property]
			case *ast.StringLiteral:
				e.genProtos = e.genProtos || genProtoNames[n.Value]
			}
			return !e.genProtos
		})
		if e.genProtos {
			return
		}
	}
}

// ensureGenProtoRuntime declares fnmeta.c's generator-prototype entry points.
func (e *Emitter) ensureGenProtoRuntime() {
	e.ensureFnMeta()
	e.ensureFnBagSeed()
	e.declareFn("__kml_gen_fn_bag", "declare ptr @__kml_gen_fn_bag(ptr)")
	e.declareFn("__kml_gen_instance_proto", "declare i64 @__kml_gen_instance_proto(ptr, i32)")
	e.declareFn("__kml_gen_root", "declare ptr @__kml_gen_root(i32)")
	e.declareFn("__kml_fn_proto_of", "declare i64 @__kml_fn_proto_of(ptr, i64)")
	e.declareFn("__kml_genfn_proto_parent", "declare i64 @__kml_genfn_proto_parent(ptr, i64, i64)")
	e.declareFn("__kml_shape_generator_kind", "declare i32 @__kml_shape_generator_kind(ptr)")
	e.declareFn("__kml_shape_generator_proto", "declare i64 @__kml_shape_generator_proto(ptr, i32)")
}

// emitGenInstanceProto is the prototype a new instance of info's generator
// inherits: its function's `prototype` when that is an object, else the
// root. A capture-free generator is one function value (its valueGlobal),
// whose property bag is cached beside it; a capturing one's instances take
// the root. 0 when the program cannot observe it; a library generator always
// records it, since the library compiles the same for every program.
func (e *Emitter) emitGenInstanceProto(info *GeneratorInfo) string {
	if !e.genProtos && e.inLib == "" {
		return "0"
	}
	e.ensureGenProtoRuntime()
	if !e.fnDecls["__kml_gen_proto_off"] {
		// Where the field sits, the same in every generator layout (it is
		// first): shape.c reads an instance's prototype there.
		e.fnDecls["__kml_gen_proto_off"] = true
		idx, _, _ := info.GenTy.FieldIndex(GeneratorProtoField)
		e.emitGlobal(fmt.Sprintf("@__kml_gen_proto_off = constant i64 ptrtoint (ptr getelementptr (%s, ptr null, i32 0, i32 %d) to i64), align 8", info.GenTy.StructIR(), idx))
	}
	async := 0
	if info.IsAsync {
		async = 1
	}
	if len(info.Captures) > 0 {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_gen_instance_proto(ptr null, i32 %d)", r, async))
		return r
	}
	if info.ctorFn == "" {
		name := info.displayName
		if n, ok := e.genShownNames[name]; ok {
			name = n
		}
		e.emitGeneratorCtorThunk(info, name)
	}
	if info.valueGlobal == "" {
		info.valueGlobal = genSymbol(info, "@__kml_genval")
		e.emitGlobal(info.valueGlobal + " = internal global ptr null, align 8")
	}
	bagGlobal := info.valueGlobal + "_bag"
	if !e.fnDecls[bagGlobal] {
		e.fnDecls[bagGlobal] = true
		e.emitGlobal(bagGlobal + " = internal global ptr null, align 8")
	}
	// The function value, then its bag: each made on first use.
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cur, info.valueGlobal))
	mkHdr, haveHdr := e.freshLabel("genproto.mkhdr"), e.freshLabel("genproto.hdr")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(cur), mkHdr, haveHdr))
	e.emitLabel(mkHdr)
	hdr := e.buildBuiltinClosure(info.ctorFn, "null")
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hdr, info.valueGlobal))
	e.emitTerminator(fmt.Sprintf("br label %%%s", haveHdr))
	e.emitLabel(haveHdr)
	fn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fn, info.valueGlobal))
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", bag, bagGlobal))
	mkBag, haveBag := e.freshLabel("genproto.mkbag"), e.freshLabel("genproto.bag")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(bag), mkBag, haveBag))
	e.emitLabel(mkBag)
	made := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_gen_fn_bag(ptr %s)", made, fn))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", made, bagGlobal))
	e.emitTerminator(fmt.Sprintf("br label %%%s", haveBag))
	e.emitLabel(haveBag)
	b := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", b, bagGlobal))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_gen_instance_proto(ptr %s, i32 %d)", r, b, async))
	return r
}

// emitGenProtoOf is a generator instance's prototype: the one it recorded,
// else (the program could not observe it at creation) the root.
func (e *Emitter) emitGenProtoOf(genObj string, genTy Type) Value {
	e.ensureGenProtoRuntime()
	rec := e.loadGeneratorField(genObj, genTy, GeneratorProtoField)
	async := 0
	if genTy.GeneratorIsAsync {
		async = 1
	}
	root := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_gen_root(i32 %d)", root, async))
	rootBox := e.emitNbTagPtr(root, kmlTagDynObject)
	isZero, r := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isZero, rec.Ref))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", r, isZero, rootBox, rec.Ref))
	return Value{Ref: r, Ty: TypeAny}
}
