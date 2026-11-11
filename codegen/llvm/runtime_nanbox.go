package llvm

import "fmt"

// runtime_nanbox.go — the single-word NaN-boxed dynamic value (TDD-00156).
// A dynamic value is one i64:
//
//	Numbers:    double_bits + (1 << 49), NaN canonicalized first — every
//	            encoded number is >= 2^49. (The int/float split is gone: a
//	            JS number IS a double; >2^53 integers round exactly as JS.)
//	Immediates: undefined = 0x0A, null = 0x02, false = 0x06, true = 0x07
//	            (JSC's constants), all < 0x10000.
//	Pointers:   raw address | low-3-bit kind (allocations are >= 8-aligned).
//	            Low-bit tagging keeps Boehm happy: a tagged pointer is an
//	            *interior* pointer, which the conservative scan treats as a
//	            live reference (classic high-bit NaN-tagging would hide the
//	            pointer from the GC entirely — the load-bearing decision,
//	            see the TDD).
//
//	kind 0 string · 1 static object · 2 array header · 3 funcRef ·
//	4 stream · 5 dynobj · 6 dynarr · 7 dynfunc
//
// The compiler's logical kmlTag* model survives unchanged: __kml_nb_tag /
// __kml_nb_pay decode a word into the familiar (tag, payload) pair — a
// number always decodes to kmlTagFloat with the double bits as payload —
// and __kml_nb_pack is the inverse. All dispatch glue keeps its structure.

const (
	nbUndefined    = 10
	nbNull         = 2
	nbFalse        = 6
	nbTrue         = 7
	nbDoubleOffset = int64(1) << 49
	// undefF64 is undefined held in a double slot (TDD-00241): a signaling
	// NaN (quiet bit clear) with the sign bit set, 0xFFF4000000000001.
	// Copies keep it; arithmetic, negation and fabs change its bits, so
	// they read NaN. Mirrored as KML_UNDEF_F64 in dtoasrc/dtoa.c.
	undefF64 = int64(-3377699720527871)
)

func (e *Emitter) ensureNanBox() {
	if e.usedNanBox {
		return
	}
	e.usedNanBox = true
	e.buildNbPack()
	e.buildNbTag()
	e.buildNbPay()
}

// nbKindTags are the pointer kinds' logical tags, in kind order 1..7
// (kind 0, a string, has no tag bits).
var nbKindTags = []int{kmlTagObject, kmlTagArray, kmlTagFuncRef, kmlTagStream, kmlTagDynObject, kmlTagDynArray, kmlTagDynFunc}

// buildNbPack defines __kml_nb_pack(tag, pay): a logical (tag, payload) pair
// packed into a NaN-boxed word. weak_odr, so each unit inlines its own copy.
func (e *Emitter) buildNbPack() {
	restore := e.beginDetachedFunc()
	intL, fltL, boolL, strL, immL := e.freshLabel("int"), e.freshLabel("flt"), e.freshLabel("bool"), e.freshLabel("kstr"), e.freshLabel("imm")
	kindL := make([]string, len(nbKindTags))
	cases := fmt.Sprintf("i8 %d, label %%%s i8 %d, label %%%s i8 %d, label %%%s i8 %d, label %%%s", kmlTagInt, intL, kmlTagFloat, fltL, kmlTagBoolean, boolL, kmlTagString, strL)
	for i, t := range nbKindTags {
		kindL[i] = e.freshLabel("kind")
		cases += fmt.Sprintf(" i8 %d, label %%%s", t, kindL[i])
	}
	e.emitTerminator(fmt.Sprintf("switch i8 %%tag, label %%%s [ %s ]", immL, cases))
	e.emitLabel(intL)
	di, bi, ei := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i64 %%pay to double", di))
	e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", bi, di))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", ei, bi, nbDoubleOffset))
	e.emitTerminator("ret i64 " + ei)
	e.emitLabel(fltL)
	d, isNaN, bits, ef := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = bitcast i64 %%pay to double", d))
	e.emitInstr(fmt.Sprintf("%s = fcmp uno double %s, %s", isNaN, d, d))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 9221120237041090560, i64 %%pay", bits, isNaN))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", ef, bits, nbDoubleOffset))
	e.emitTerminator("ret i64 " + ef)
	e.emitLabel(boolL)
	bt, bv := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %%pay, 0", bt))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %d", bv, bt, nbTrue, nbFalse))
	e.emitTerminator("ret i64 " + bv)
	e.emitLabel(strL)
	e.emitTerminator("ret i64 %pay")
	for i, l := range kindL {
		e.emitLabel(l)
		o := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i64 %%pay, %d", o, i+1))
		e.emitTerminator("ret i64 " + o)
	}
	e.emitLabel(immL)
	isNull, iv := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %%tag, %d", isNull, kmlTagNull))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %d", iv, isNull, nbNull, nbUndefined))
	e.emitTerminator("ret i64 " + iv)
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("weak_odr i64 @__kml_nb_pack(i8 %tag, i64 %pay)", body)
}

// nbSplit branches on word %v: a number to numL, an immediate (< 2^16) to
// immL, a pointer to ptrL.
func (e *Emitter) nbSplit(numL, immL, ptrL string) {
	isNum, notNumL := e.freshReg(), e.freshLabel("notnum")
	e.emitInstr(fmt.Sprintf("%s = icmp uge i64 %%v, %d", isNum, nbDoubleOffset))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNum, numL, notNumL))
	e.emitLabel(notNumL)
	isImm := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %%v, 65536", isImm))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isImm, immL, ptrL))
}

// buildNbTag defines __kml_nb_tag(v): a word's logical tag (a number always
// answers kmlTagFloat).
func (e *Emitter) buildNbTag() {
	restore := e.beginDetachedFunc()
	numL, immL, ptrL := e.freshLabel("num"), e.freshLabel("imm"), e.freshLabel("ptr")
	e.nbSplit(numL, immL, ptrL)
	e.emitLabel(numL)
	e.emitTerminator(fmt.Sprintf("ret i8 %d", kmlTagFloat))
	e.emitLabel(immL)
	nullL, boolL, undefL := e.freshLabel("null"), e.freshLabel("bool"), e.freshLabel("undef")
	e.emitTerminator(fmt.Sprintf("switch i64 %%v, label %%%s [ i64 %d, label %%%s i64 %d, label %%%s i64 %d, label %%%s ]", undefL, nbNull, nullL, nbFalse, boolL, nbTrue, boolL))
	e.emitLabel(nullL)
	e.emitTerminator(fmt.Sprintf("ret i8 %d", kmlTagNull))
	e.emitLabel(boolL)
	e.emitTerminator(fmt.Sprintf("ret i8 %d", kmlTagBoolean))
	e.emitLabel(undefL)
	e.emitTerminator(fmt.Sprintf("ret i8 %d", kmlTagUndefined))
	e.emitLabel(ptrL)
	kind, strL := e.freshReg(), e.freshLabel("tstr")
	e.emitInstr(fmt.Sprintf("%s = and i64 %%v, 7", kind))
	kindL := make([]string, len(nbKindTags))
	cases := ""
	for i := range nbKindTags {
		kindL[i] = e.freshLabel("tkind")
		cases += fmt.Sprintf(" i64 %d, label %%%s", i+1, kindL[i])
	}
	e.emitTerminator(fmt.Sprintf("switch i64 %s, label %%%s [%s ]", kind, strL, cases))
	e.emitLabel(strL)
	e.emitTerminator(fmt.Sprintf("ret i8 %d", kmlTagString))
	for i, l := range kindL {
		e.emitLabel(l)
		e.emitTerminator(fmt.Sprintf("ret i8 %d", nbKindTags[i]))
	}
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("weak_odr i8 @__kml_nb_tag(i64 %v)", body)
}

// buildNbPay defines __kml_nb_pay(v): a word's logical payload (the double
// bits, the pointer, or 0/1 for a boolean). A string (kind 0) carries no
// tag bits, so its data needs no alignment; the other kinds mask theirs off.
func (e *Emitter) buildNbPay() {
	restore := e.beginDetachedFunc()
	numL, immL, ptrL := e.freshLabel("num"), e.freshLabel("imm"), e.freshLabel("ptr")
	e.nbSplit(numL, immL, ptrL)
	e.emitLabel(numL)
	bits := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %%v, %d", bits, nbDoubleOffset))
	e.emitTerminator("ret i64 " + bits)
	e.emitLabel(immL)
	isTrue, ib := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %%v, %d", isTrue, nbTrue))
	e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", ib, isTrue))
	e.emitTerminator("ret i64 " + ib)
	e.emitLabel(ptrL)
	kind, isStr, masked, out := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %%v, 7", kind))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isStr, kind))
	e.emitInstr(fmt.Sprintf("%s = and i64 %%v, -8", masked))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %%v, i64 %s", out, isStr, masked))
	e.emitTerminator("ret i64 " + out)
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("weak_odr i64 @__kml_nb_pay(i64 %v)", body)
}
