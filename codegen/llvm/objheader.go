// objheader.go — the header word every object layout starts with (TDD-00230
// phase 5). Field 0 of an object struct (ClassTagField) holds:
//
//	bits  0..31  type id: indexes the program's layout table (objlayout.go)
//	bits 32..46  kmlHdrMagic, marking a header written by this scheme
//	bit  47      symbolTypeIDFlag (a Symbol, ADR-01059)
//	bit  48      errorTypeIDFlag (an Error; the low bits are its kind/TagID)
//	bit  49      hostTypeIDFlag (a host value boxed into `any`)
//	bit  50      hostInlineFlag (with bit 49: the header of a host body that
//	             follows it, a Map's or a Set's, rather than of a two-word
//	             cell holding a handle; TDD-00247)
//	bits 51..52  the integrity level Object.freeze, seal and
//	             preventExtensions record (staticIntegrity*): 0 none
//	bit  53      kmlHdrImmortal: a shared sentinel cell, never changed
//
// Bits 51..53 are object state, not type: a type test compares a header
// with them cleared (headerIdentity).
//
// A class instance's header is its TagID (which carries the magic), an
// object literal's the id its layout was registered under. A header with
// neither the magic nor a flag is "no shape": nothing is known about the
// object at run time.
package llvm

import (
	"crypto/sha256"
	"fmt"
	"hash/fnv"
	"strings"

	"KlainMainLang/ast"
)

const (
	kmlHdrLevelShift       = 51
	kmlHdrLevelMask  int64 = 3 << kmlHdrLevelShift
	kmlHdrImmortal   int64 = 1 << 53
	kmlHdrStateMask        = kmlHdrLevelMask | kmlHdrImmortal
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
	return layoutKeyMemo(t, map[*Field]string{})
}

func layoutFieldKey(t Type) string {
	return layoutFieldKeyMemo(t, map[*Field]string{})
}

// layoutKeyMemo is layoutKey with the keys of the object types already
// rendered in this call, by their field slice: copies of one type share it,
// and a type that reaches the same nested type along many paths (a generic
// interface instantiated inside itself) would otherwise render it once per
// path, exponentially in the nesting depth.
func layoutKeyMemo(t Type, memo map[*Field]string) string {
	fs := t.UserFields()
	var id *Field
	if len(fs) > 0 {
		id = &fs[0]
		if k, ok := memo[id]; ok && !t.IsTuple() {
			return k
		}
	}
	var b strings.Builder
	if t.IsTuple() {
		b.WriteString("T|")
	}
	for _, f := range fs {
		b.WriteString(f.Name)
		b.WriteByte(':')
		b.WriteString(layoutFieldKeyMemo(f.Ty, memo))
		b.WriteByte(';')
	}
	// The hidden words are part of the layout: a declared `{ value, done }`
	// has them and a builtin one of the same fields does not.
	if t.presenceIndex() >= 0 {
		b.WriteString("|P")
	}
	if t.orderIndex() >= 0 {
		b.WriteString("|O")
	}
	// A generator's id selects its method table: a sync and an async one
	// of the same fields answer next() differently.
	if t.IsGenerator() {
		b.WriteString("|G")
		if t.GeneratorIsAsync {
			b.WriteString("A")
		}
	}
	k := b.String()
	if id != nil && !t.IsTuple() {
		memo[id] = k
	}
	return k
}

func layoutFieldKeyMemo(t Type, memo map[*Field]string) string {
	switch {
	case t.IsArray() && t.ElemType != nil:
		return "[" + layoutFieldKeyMemo(*t.ElemType, memo) + "]"
	case t.IsObject() && !t.IsClass():
		k := layoutKeyMemo(t, memo)
		if len(k) > 128 {
			// A nested layout is named by its digest past a short length:
			// a type nested in itself many times spells out exponentially.
			k = fmt.Sprintf("#%x", sha256.Sum256([]byte(k)))
		}
		return "{" + k + "}"
	case t.IsClass():
		return "C" + t.ClassName
	}
	// The pointer kinds read differently (a string field is not a
	// dictionary's map), so each is its own layout.
	kind := ""
	switch {
	case isHostHandle(t):
		kind = "H" + hostKey(t)
	case t.IsDynamicObject():
		kind = "D"
	case t.IsFunc():
		// A function member's signature is part of the layout: a box's
		// method adapter calls it with its own parameters' types.
		var ps []string
		for _, p := range t.FuncParams {
			ps = append(ps, layoutFieldKeyMemo(p, memo))
		}
		ret := ""
		if t.FuncRetType != nil {
			ret = layoutFieldKeyMemo(*t.FuncRetType, memo)
		}
		kind = "F(" + strings.Join(ps, ",") + ")" + ret + fmt.Sprint(t.FuncHasRest)
	case t.IsPromise():
		kind = "P"
	case isStringTy(t):
		kind = "s"
	}
	return fmt.Sprintf("%s%s/%v/%v/%v/%v", kind, StructFieldIR(t), t.IsDynamic(), t.Nullable(), t.IsUndefined(), t.Signed)
}

// objHeaderWord returns the header word stored at field 0 of a freshly
// allocated t: a class's TagID, an Error's flagged kind (set by its own
// builders), or the interned layout id of any other object type.
func (e *Emitter) objHeaderWord(t Type) int64 {
	if t.IsClass() {
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

// hdrStateDefines is the C side of a header word's object state, in every
// runtime member's prelude.
func hdrStateDefines() string {
	return fmt.Sprintf("#define KML_HDR_LEVEL_SHIFT %d\n#define KML_HDR_LEVEL_MASK %dLL\n#define KML_HDR_IMMORTAL %dLL\n#define KML_HDR_STATE_MASK %dLL\n",
		kmlHdrLevelShift, kmlHdrLevelMask, kmlHdrImmortal, kmlHdrStateMask) + hdrLevelHelpers
}

// hdrLevelHelpers read and tighten a header word's integrity level, in
// every runtime member that includes the state defines: a level only
// tightens, and an immortal header is never written.
const hdrLevelHelpers = `static inline long long kml_hdr_level(const long long *w) { return (*w & KML_HDR_LEVEL_MASK) >> KML_HDR_LEVEL_SHIFT; }
static inline void kml_hdr_raise_level(long long *w, long long lvl) {
    if (*w & KML_HDR_IMMORTAL) return;
    long long cur = kml_hdr_level(w);
    if (cur != 0 && cur <= lvl) return;
    *w = (*w & ~KML_HDR_LEVEL_MASK) | (lvl << KML_HDR_LEVEL_SHIFT);
}
`

// headerIdentity is header word w without its object state (bits 51..53):
// the part a type test compares.
func (e *Emitter) headerIdentity(w string) string {
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", r, w, ^kmlHdrStateMask))
	return r
}

// emitStoreObjHeader writes t's header word into the object at ptr.
func (e *Emitter) emitStoreObjHeader(ptr string, t Type) {
	if !hasObjHeader(t) {
		return // a tuple or a host class layout
	}
	e.heapStore("i64", fmt.Sprint(e.objHeaderWord(t)), ptr, 8)
	// A malloc'd object starts with no key absent and its layout's order.
	if i := t.presenceIndex(); i >= 0 {
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, t.StructIR(), ptr, i))
		e.heapStore("i64", "0", g, 8)
	}
	if i := t.orderIndex(); i >= 0 {
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, t.StructIR(), ptr, i))
		e.heapStore("ptr", "null", g, 8)
	}
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
