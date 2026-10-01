// objheader.go — the header word every object layout starts with (TDD-00230
// phase 5). Field 0 of an object struct (ClassTagField) holds:
//
//	bits  0..31  type id: indexes the program's layout table (objlayout.go)
//	bits 32..46  kmlHdrMagic, marking a header written by this scheme
//	bit  47      symbolTypeIDFlag (a Symbol, ADR-01059)
//	bit  48      errorTypeIDFlag (an Error; the low bits are its kind/TagID)
//
// A class instance's header is its TagID (which carries the magic), an
// object literal's the id its layout was registered under. A header with
// neither the magic nor a flag is "no shape": nothing is known about the
// object at run time.
package llvm

import (
	"fmt"
	"strings"
)

const (
	kmlHdrMagic      int64 = 0x4B4D << 32
	kmlHdrMagicMask  int64 = 0x7FFF << 32
	kmlHdrIDMask     int64 = 0xFFFFFFFF
	kmlFirstLayoutID int64 = 1 << 16
)

// allocTypeID hands out the next type id, shared by classes and object
// layouts so the two never collide. The value is the header word itself
// (magic included), which is what a class's TagID is.
func (e *Emitter) allocTypeID() int64 {
	if e.nextClassTagID == 0 {
		e.nextClassTagID = kmlHdrMagic | kmlFirstLayoutID
	}
	id := e.nextClassTagID
	e.nextClassTagID++
	return id
}

// layoutKey identifies a layout for interning: its field names and their
// storage, plus the flags that change how it reads (a tuple renders as an
// array).
func layoutKey(t Type) string {
	var b strings.Builder
	if t.IsTuple {
		b.WriteString("T|")
	}
	for _, f := range t.UserFields() {
		b.WriteString(f.Name)
		b.WriteByte(':')
		b.WriteString(layoutFieldKey(f.Ty))
		b.WriteByte(';')
	}
	return b.String()
}

func layoutFieldKey(t Type) string {
	switch {
	case t.IsArray && t.ElemType != nil:
		return "[" + layoutFieldKey(*t.ElemType) + "]"
	case t.IsObject && !t.IsClass:
		return "{" + layoutKey(t) + "}"
	case t.IsClass:
		return "C" + t.ClassName
	}
	// The pointer kinds read differently (a string field is not a
	// dictionary's map), so each is its own layout.
	kind := ""
	switch {
	case isHostHandle(t):
		kind = "H" + hostKey(t)
	case t.IsDynamicObject:
		kind = "D"
	case t.IsFunc:
		kind = "F"
	case t.IsPromise:
		kind = "P"
	case isStringTy(t):
		kind = "s"
	}
	return fmt.Sprintf("%s%s/%v/%v/%v/%v", kind, StructFieldIR(t), t.IsDynamic, t.Nullable, t.IsUndefined, t.Signed)
}

// objHeaderWord returns the header word stored at field 0 of a freshly
// allocated t: a class's TagID, an Error's flagged kind (set by its own
// builders), or the interned layout id of any other object type.
func (e *Emitter) objHeaderWord(t Type) int64 {
	if t.IsClass {
		if info, ok := e.classes[t.ClassName]; ok {
			if info.IsErrorSubclass {
				return errorTypeIDStored(info.TagID)
			}
			return info.TagID
		}
	}
	key := layoutKey(t)
	if id, ok := e.layoutIDs[key]; ok {
		return id
	}
	if e.layoutIDs == nil {
		e.layoutIDs = map[string]int64{}
	}
	id := e.allocTypeID()
	e.layoutIDs[key] = id
	e.layouts = append(e.layouts, registeredLayout{id: id, ty: t})
	return id
}

// emitStoreObjHeader writes t's header word into the object at ptr.
func (e *Emitter) emitStoreObjHeader(ptr string, t Type) {
	if !hasObjHeader(t) {
		return // a tuple or a host class layout
	}
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", e.objHeaderWord(t), ptr))
}

// registeredLayout is one object layout that received a type id.
type registeredLayout struct {
	id int64
	ty Type
}

// hasObjHeader reports whether t's layout starts with the header word.
func hasObjHeader(t Type) bool {
	return len(t.Fields) > 0 && t.Fields[0].Name == ClassTagField
}

// emitLayoutHeaderGlobal defines name as a constant holding t's header word,
// for a hand-written runtime routine that allocates a t: it stores the word it
// loads from name, so the layout's id is the program's.
func (e *Emitter) emitLayoutHeaderGlobal(name string, t Type) {
	e.emitGlobal(fmt.Sprintf("%s = internal constant i64 %d, align 8", name, e.objHeaderWord(t)))
}
