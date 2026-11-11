package llvm

// layout_table.go — the size and pointer map of each object layout, by type
// id (TDD-00230 Phase 5). An object allocated through a layout carries that
// layout's type id in its header word, so a collector finds every word's
// kind from the object alone. Under -mm=gc the map is Boehm's type
// descriptor (kml_gc_alloc_typed): a word that holds no address is not
// scanned.

import (
	"fmt"
	"strings"
)

// Word kinds of a layout's pointer map, matching kml_heap.h's KML_WORD_*.
const (
	wordData     = 0 // no address: a number, a boolean, a presence bit
	wordHeader   = 1 // the header word
	wordObject   = 2 // a managed pointer to a block's start (an object, a key-order list)
	wordPrefixed = 3 // a managed pointer past a prefix word (a string, an array, a function value, a host body)
	wordBoxed    = 4 // a NaN-boxed value: a pointer under its tag bits, or none
	wordOther    = 5 // a word the layout does not classify, traced conservatively
)

// layoutWordKinds is t's pointer map: one kind per 8-byte word.
func layoutWordKinds(t Type) []byte {
	ir := t.StructIR()
	offs, size := irStructOffsets(ir)
	irs := splitIRFields(ir)
	kinds := make([]byte, (size+7)/8)
	if len(irs) != len(t.Fields) {
		for i := range kinds {
			kinds[i] = wordOther
		}
		return kinds
	}
	for i, f := range t.Fields {
		switch {
		case i == 0 && hasObjHeader(t):
			kinds[0] = wordHeader
			continue
		case f.Name == PresenceField:
			continue // a bit per optional key
		case f.Name == OrderField:
			markIRWords(kinds, offs[i], irs[i], wordObject)
			continue
		}
		markIRWords(kinds, offs[i], irs[i], fieldWordKind(f.Ty))
	}
	return kinds
}

// fieldWordKind is the kind of a pointer-sized word a field of type ty
// holds.
func fieldWordKind(ty Type) byte {
	switch {
	case ty.IsDynamic():
		return wordBoxed
	case ty.IsArray() || ty.IsFunc() || isForOfStringTy(ty) || headeredHost(ty) || hostPrefixedObject(ty):
		return wordPrefixed
	case ty.IsObject() && hasObjHeader(ty) && !ty.IsPromise():
		return wordObject
	}
	return wordOther
}

// markIRWords records the words a value of IR type ir at byte offset off
// covers: data for a type that holds no address, else kind (a nested
// aggregate's pointer-sized members unclassified).
func markIRWords(kinds []byte, off int, ir string, kind byte) {
	ir = strings.TrimSpace(ir)
	switch {
	case ir == "i1" || ir == "i8" || ir == "i16" || ir == "i32" || ir == "float" || ir == "double":
		return // the zero value is data already
	case strings.HasPrefix(ir, "{"):
		offs, _ := irStructOffsets(ir)
		for i, f := range splitIRFields(ir) {
			markIRWords(kinds, off+offs[i], f, wordOther)
		}
		return
	case strings.HasPrefix(ir, "["):
		inner := strings.TrimSuffix(strings.TrimPrefix(ir, "["), "]")
		var n int
		k := strings.Index(inner, " x ")
		fmt.Sscanf(inner[:k], "%d", &n)
		elem := inner[k+3:]
		size, _ := irTypeSizeAlign(elem)
		for i := 0; i < n; i++ {
			markIRWords(kinds, off+i*size, elem, kind)
		}
		return
	}
	if w := off / 8; w < len(kinds) && kinds[w] < kind {
		kinds[w] = kind
	}
}

// layoutRowKey is the key of t's layout row: its header word's type id.
func (e *Emitter) layoutRowKey(t Type) int64 {
	return e.objHeaderWord(t) & kmlHdrIDMask
}

// emitObjBlock allocates the block of a headered object of type t into reg,
// zeroed when zero (Boehm's typed allocation always is). Under -mm=gc it
// is allocated with its layout's pointer map.
func (e *Emitter) emitObjBlock(reg string, t Type, zero bool) {
	size := fmt.Sprint(t.StructSize())
	if e.isGCMode() && hasObjHeader(t) {
		key := e.layoutRowKey(t)
		if e.typedLayouts == nil {
			e.typedLayouts = map[int64]Type{}
		}
		if _, ok := e.typedLayouts[key]; !ok {
			e.typedLayouts[key] = t
			e.typedLayoutOrder = append(e.typedLayoutOrder, key)
		}
		e.heapAllocCall(reg, allocObject, fmt.Sprintf("kml_gc_alloc_typed(i64 %d, i64 %s)", key, size))
		return
	}
	if zero {
		e.heapCallocInto(reg, allocObject, "1", size)
		return
	}
	e.heapMallocInto(reg, allocObject, size)
}

// emitLayoutRows emits a layout row per layout a typed allocation names:
// { i64 id, i64 size, i64 nwords, ptr kinds }.
func (e *Emitter) emitLayoutRows() {
	for i, key := range e.typedLayoutOrder {
		t := e.typedLayouts[key]
		kinds := layoutWordKinds(t)
		var b strings.Builder
		for j, k := range kinds {
			if j > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "i8 %d", k)
		}
		sym := fmt.Sprintf("@__kml_layout_words.%d", i)
		e.emitGlobal(fmt.Sprintf("%s = private unnamed_addr"+irGlobalAS+" constant [%d x i8] [%s], align 8", sym, len(kinds), b.String()))
		e.addUnitRow(unitKindLayout, "{ i64, i64, i64, "+irPtr+" }", key, fmt.Sprintf("i64 %d, i64 %d, "+irPtr+" %s", t.StructSize(), len(kinds), sym))
	}
}
