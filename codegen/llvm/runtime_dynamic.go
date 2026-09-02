package llvm

import ()

// ensureAnyEq declares __kml_any_eq over two NaN-boxed words (TDD-00156),
// backing === / !== on dynamic values. Two numbers compare as doubles
// (NaN !== NaN, -0 === 0); bitwise-equal non-numbers are identical
// immediates or the same tagged pointer — true; two string-kind values
// compare by strcmp (content equality); every other combination is false.
func (e *Emitter) ensureAnyEq() {
	if e.usedAnyEq {
		return
	}
	e.usedAnyEq = true
	e.ensureStrcmp()
	e.emitGlobal(`
define i1 @__kml_any_eq(i64 %a, i64 %b) {
entry:
  %anum = icmp uge i64 %a, 562949953421312
  %bnum = icmp uge i64 %b, 562949953421312
  %bothnum = and i1 %anum, %bnum
  br i1 %bothnum, label %num, label %notnum
num:
  %abits = sub i64 %a, 562949953421312
  %bbits = sub i64 %b, 562949953421312
  %ad = bitcast i64 %abits to double
  %bd = bitcast i64 %bbits to double
  %feq = fcmp oeq double %ad, %bd
  ret i1 %feq
notnum:
  %eithernum = or i1 %anum, %bnum
  br i1 %eithernum, label %not_equal, label %nonnums
nonnums:
  %same = icmp eq i64 %a, %b
  br i1 %same, label %ret_true, label %diff
diff:
  ; both string-kind (pointer range, low-3 kind bits 0) => content compare
  %aptr = icmp uge i64 %a, 65536
  %bptr = icmp uge i64 %b, 65536
  %ak = and i64 %a, 7
  %bk = and i64 %b, 7
  %astr0 = icmp eq i64 %ak, 0
  %bstr0 = icmp eq i64 %bk, 0
  %astr = and i1 %aptr, %astr0
  %bstr = and i1 %bptr, %bstr0
  %bothstr = and i1 %astr, %bstr
  br i1 %bothstr, label %cmp_string, label %not_string
not_string:
  ; both arrayheader-kind (kind bits 2): two boxings of one array malloc two
  ; headers, so identity is the data pointer *inside* the header (ADR-00478)
  %aarr0 = icmp eq i64 %ak, 2
  %barr0 = icmp eq i64 %bk, 2
  %aarr = and i1 %aptr, %aarr0
  %barr = and i1 %bptr, %barr0
  %botharr = and i1 %aarr, %barr
  br i1 %botharr, label %cmp_array, label %not_equal
cmp_array:
  %ha = and i64 %a, -8
  %hb = and i64 %b, -8
  %hap = inttoptr i64 %ha to ptr
  %hbp = inttoptr i64 %hb to ptr
  %da = load ptr, ptr %hap, align 8
  %db = load ptr, ptr %hbp, align 8
  %arr_eq = icmp eq ptr %da, %db
  ret i1 %arr_eq
cmp_string:
  %sa = inttoptr i64 %a to ptr
  %sb = inttoptr i64 %b to ptr
  %scmp = call i32 @strcmp(ptr %sa, ptr %sb)
  %string_eq = icmp eq i32 %scmp, 0
  ret i1 %string_eq
ret_true:
  ret i1 true
not_equal:
  ret i1 false
}`)
}
