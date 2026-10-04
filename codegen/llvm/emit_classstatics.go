package llvm

import (
	"fmt"
	"sort"
	"strings"

	"KlainMainLang/ast"
)

// emit_classstatics.go — the statics of a class that is a class per
// evaluation (TDD-00242). Each evaluation's record has a static area of its
// own (unitregsrc `__kml_classrec_statics`), laid out as every static field
// the class has, then an own flag per inherited one. Code reaches the area
// of the record it runs against:
//
//   - in the function declaring the class, the local holding this
//     evaluation's record;
//   - in a static member, the receiver its call passed (a direct call
//     passes the record it names; a dispatch through the class as a value
//     notes the record it looked up);
//   - in an instance member, the record its instance was built from.
//
// A class with no record keeps its statics in globals.

// staticArea is a record class's static layout.
type staticArea struct {
	ir   string         // the area's struct type
	slot map[string]int // a static field's index
	own  map[string]int // an inherited static field's own flag's index
}

// recordStaticClass reports whether class c keeps its statics per
// evaluation.
func (e *Emitter) recordStaticClass(c string) bool {
	return e.classRecords[c] && len(e.classes[c].StaticFieldTypes) > 0
}

// staticAreaOf is record class c's static layout.
func (e *Emitter) staticAreaOf(c string) *staticArea {
	if a, ok := e.staticAreas[c]; ok {
		return a
	}
	info := e.classes[c]
	names := make([]string, 0, len(info.StaticFieldTypes))
	for n := range info.StaticFieldTypes {
		names = append(names, n)
	}
	sort.Strings(names)
	a := &staticArea{slot: map[string]int{}, own: map[string]int{}}
	var parts []string
	for _, n := range names {
		a.slot[n] = len(parts)
		parts = append(parts, info.StaticFieldTypes[n].IR)
	}
	for _, n := range names {
		if _, own := info.OwnStaticFieldTypes[n]; !own {
			a.own[n] = len(parts)
			parts = append(parts, "i1")
		}
	}
	a.ir = "{ " + strings.Join(parts, ", ") + " }"
	if e.staticAreas == nil {
		e.staticAreas = map[string]*staticArea{}
	}
	e.staticAreas[c] = a
	return a
}

// recordFor is the record of class c the code being emitted runs against,
// or a null pointer.
func (e *Emitter) recordFor(c string) string {
	e.ensureClassRec()
	if sym, ok := e.lookup(c + classRecSuffix); ok {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, sym.Ptr))
		return r
	}
	if sym, ok := e.lookup("this"); ok && sym.Ty.IsClass && !sym.Ty.IsDynamic && (sym.Ty.ClassName == c || e.classDerives(sym.Ty.ClassName, c)) {
		this, of, up := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", this, sym.Ptr))
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_classrec_of(ptr %s)", of, this))
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_classrec_up(ptr %s, ptr %s)", up, of, e.classRefPtr(c)))
		return up
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_classrec_static_cur(ptr %s)", r, e.classRefPtr(c)))
	return r
}

// staticAreaPtr is the static area of the record of class c the code runs
// against (the class's own when there is none).
func (e *Emitter) staticAreaPtr(c string) string {
	a := e.staticAreaOf(c)
	rec := e.recordFor(c)
	area := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_classrec_statics(ptr %s, ptr %s, i64 ptrtoint (ptr getelementptr (%s, ptr null, i32 1) to i64))", area, rec, e.classRefPtr(c), a.ir))
	return area
}

// staticSlotPtr is the storage of class c's static field f: its global, or
// its slot in the record's area.
func (e *Emitter) staticSlotPtr(c, f string) string {
	if !e.recordStaticClass(c) {
		return "@" + llvmSafeSymbol(c+"_static_"+f)
	}
	a := e.staticAreaOf(c)
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d", p, a.ir, e.staticAreaPtr(c), a.slot[f]))
	return p
}

// staticOwnPtr is the own flag of class c's inherited static field f.
func (e *Emitter) staticOwnPtr(c, f string) string {
	if !e.recordStaticClass(c) {
		return "@" + llvmSafeSymbol(c+"_static_"+f+"__own")
	}
	a := e.staticAreaOf(c)
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d", p, a.ir, e.staticAreaPtr(c), a.own[f]))
	return p
}

// bindStaticReceiver opens a static member of record class c (or its
// static initialization): the receiver its call passed is the record its
// statics are read from, and each local class it extends reads its own up
// the chain.
func (e *Emitter) bindStaticReceiver(c string) {
	if !e.classRecords[c] {
		return
	}
	e.ensureClassRec()
	for b, i := c, 0; b != "" && i < 64 && e.classRecords[b]; i++ {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_classrec_static_cur(ptr %s)", r, e.classRefPtr(b)))
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", r, slot))
		e.define(b+classRecSuffix, Symbol{Ptr: slot, Ty: TypePtr})
		b = e.classes[b].BaseClass
	}
}

// passStaticReceiver passes a direct call of a static member of record
// class c the record it names; the returned function restores the
// previous receiver after the call.
func (e *Emitter) passStaticReceiver(c string) func() {
	if !e.classRecords[c] {
		return func() {}
	}
	rec := e.recordFor(c)
	old := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_classrec_recv(ptr %s)", old, rec))
	return func() {
		e.emitInstr(fmt.Sprintf("call ptr @__kml_classrec_recv(ptr %s)", old))
	}
}

// staticThisToClass rewrites `this` in a class's static members (static
// methods and accessors, static field initializers, static blocks) to the
// class's name: there `this` is the class. A nested non-arrow function has
// its own `this` and is left alone.
func staticThisToClass(prog *ast.Program) {
	var rw func(cls string, n ast.Node) ast.Node
	rw = func(cls string, n ast.Node) ast.Node {
		switch n.(type) {
		case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.ClassExpression:
			return n // its own `this`
		case *ast.ThisExpression:
			return ast.NewIdentifier(cls, n.GetPos())
		}
		ast.RewriteChildren(n, func(m ast.Node) ast.Node { return rw(cls, m) })
		return n
	}
	ast.Inspect(prog, func(n ast.Node) bool {
		var cd *ast.ClassDeclaration
		switch n := n.(type) {
		case *ast.ClassDeclaration:
			cd = n
		case *ast.ClassExpression:
			cd = n.Decl
		}
		if cd == nil || cd.Name == "" {
			return true
		}
		for i := range cd.Fields {
			if cd.Fields[i].Static && cd.Fields[i].Initializer != nil {
				cd.Fields[i].Initializer = rw(cd.Name, cd.Fields[i].Initializer).(ast.Expression)
			}
		}
		for _, m := range cd.Methods {
			if m.IsStatic && m.Body != nil {
				ast.RewriteChildren(m.Body, func(x ast.Node) ast.Node { return rw(cd.Name, x) })
			}
		}
		for _, sb := range cd.StaticBlocks {
			ast.RewriteChildren(sb, func(x ast.Node) ast.Node { return rw(cd.Name, x) })
		}
		return true
	})
}
