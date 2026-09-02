package llvm

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
)

func (e *Emitter) ensureNanBox() {
	if e.usedNanBox {
		return
	}
	e.usedNanBox = true
	e.emitGlobal(`
; pack a logical (tag, payload) pair into a NaN-boxed word.
define i64 @__kml_nb_pack(i8 %tag, i64 %pay) {
entry:
  switch i8 %tag, label %imm [
    i8 0, label %int
    i8 1, label %flt
    i8 3, label %bool
    i8 2, label %kstr
    i8 6, label %kobj
    i8 7, label %karr
    i8 8, label %kfn
    i8 9, label %kstream
    i8 10, label %kdynobj
    i8 11, label %kdynarr
    i8 12, label %kdynfn
  ]
int:
  %di = sitofp i64 %pay to double
  %bi = bitcast double %di to i64
  %ei = add i64 %bi, 562949953421312
  ret i64 %ei
flt:
  %d = bitcast i64 %pay to double
  %isnan = fcmp uno double %d, %d
  %bits = select i1 %isnan, i64 9221120237041090560, i64 %pay
  %ef = add i64 %bits, 562949953421312
  ret i64 %ef
bool:
  %bt = icmp ne i64 %pay, 0
  %bv = select i1 %bt, i64 7, i64 6
  ret i64 %bv
kstr:
  ret i64 %pay
kobj:
  %o1 = or i64 %pay, 1
  ret i64 %o1
karr:
  %o2 = or i64 %pay, 2
  ret i64 %o2
kfn:
  %o3 = or i64 %pay, 3
  ret i64 %o3
kstream:
  %o4 = or i64 %pay, 4
  ret i64 %o4
kdynobj:
  %o5 = or i64 %pay, 5
  ret i64 %o5
kdynarr:
  %o6 = or i64 %pay, 6
  ret i64 %o6
kdynfn:
  %o7 = or i64 %pay, 7
  ret i64 %o7
imm:
  %isnull = icmp eq i8 %tag, 4
  %iv = select i1 %isnull, i64 2, i64 10
  ret i64 %iv
}

; decode a word's logical tag (numbers always answer kmlTagFloat=1).
define i8 @__kml_nb_tag(i64 %v) {
entry:
  %isnum = icmp uge i64 %v, 562949953421312
  br i1 %isnum, label %num, label %notnum
num:
  ret i8 1
notnum:
  %isimm = icmp ult i64 %v, 65536
  br i1 %isimm, label %imm, label %ptr
imm:
  switch i64 %v, label %undef [
    i64 2, label %null
    i64 6, label %false
    i64 7, label %true
  ]
null:
  ret i8 4
false:
  ret i8 3
true:
  ret i8 3
undef:
  ret i8 5
ptr:
  %kind = and i64 %v, 7
  switch i64 %kind, label %tstr [
    i64 1, label %tobj
    i64 2, label %tarr
    i64 3, label %tfn
    i64 4, label %tstream
    i64 5, label %tdynobj
    i64 6, label %tdynarr
    i64 7, label %tdynfn
  ]
tstr:
  ret i8 2
tobj:
  ret i8 6
tarr:
  ret i8 7
tfn:
  ret i8 8
tstream:
  ret i8 9
tdynobj:
  ret i8 10
tdynarr:
  ret i8 11
tdynfn:
  ret i8 12
}

; decode a word's logical payload (double bits / masked pointer / 0|1).
define i64 @__kml_nb_pay(i64 %v) {
entry:
  %isnum = icmp uge i64 %v, 562949953421312
  br i1 %isnum, label %num, label %notnum
num:
  %bits = sub i64 %v, 562949953421312
  ret i64 %bits
notnum:
  %isimm = icmp ult i64 %v, 65536
  br i1 %isimm, label %imm, label %ptr
imm:
  %istrue = icmp eq i64 %v, 7
  %ib = select i1 %istrue, i64 1, i64 0
  ret i64 %ib
ptr:
  ; kind 0 (string) carries no tag bits — the value IS the pointer, so no
  ; alignment demand on string data; other kinds mask their low-3 tag off
  ; (their allocations are malloc'd, >= 8-aligned).
  %kind = and i64 %v, 7
  %iskstr = icmp eq i64 %kind, 0
  %masked = and i64 %v, -8
  %out = select i1 %iskstr, i64 %v, i64 %masked
  ret i64 %out
}`)
}
