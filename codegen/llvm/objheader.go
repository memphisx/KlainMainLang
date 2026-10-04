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
	"hash/fnv"
	"strings"

	"KlainMainLang/ast"
)

const (
	kmlHdrMagic      int64 = 0x4B4D << 32
	kmlHdrMagicMask  int64 = 0x7FFF << 32
	kmlHdrIDMask     int64 = 0xFFFFFFFF
	kmlFirstLayoutID int64 = 1 << 16
)

// kmlStableIDBase splits the 32-bit type-id space (TDD-00238 Stage 2). Ids
// below it are numbered per program; ids from it up are a hash of a key that
// is the same in every program, so a separately compiled library object and
// the program agree on them.
const kmlStableIDBase int64 = 1 << 31

// allocTypeID hands out the next per-program type id. The value is the header
// word itself (magic included), which is what a class's TagID is.
func (e *Emitter) allocTypeID() int64 {
	if e.nextClassTagID == 0 {
		e.nextClassTagID = kmlHdrMagic | kmlFirstLayoutID
	}
	id := e.nextClassTagID
	if id&kmlHdrIDMask >= kmlStableIDBase {
		panic("internal: the per-program type ids ran into the stable range")
	}
	e.nextClassTagID++
	return id
}

// stableTypeID is key's type id from the stable range: the same in every
// program. Two keys hashing to one id is a compile error, not a probe, since
// a probed id would depend on what else the program holds.
func (e *Emitter) stableTypeID(key string) int64 {
	h := fnv.New32a()
	h.Write([]byte(key))
	id := kmlStableIDBase | int64(h.Sum32())&(kmlStableIDBase-1)
	if e.stableIDKeys == nil {
		e.stableIDKeys = map[int64]string{}
	}
	if prev, ok := e.stableIDKeys[id]; ok && prev != key {
		panic(fmt.Sprintf("internal: type ids of %q and %q collide", prev, key))
	}
	e.stableIDKeys[id] = key
	return kmlHdrMagic | id
}

// classTypeID is the type id of the class named name: stable for a class of
// the builtin library (its name carries no per-program file suffix, which
// also excludes a generic instantiation over a program's own type), else the
// next per-program id.
func (e *Emitter) classTypeID(name string) int64 {
	if ast.IsLibraryName(name) {
		return e.stableTypeID("C" + name)
	}
	return e.allocTypeID()
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
	id := e.stableTypeID("L" + key)
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
