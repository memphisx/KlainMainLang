package llvm

import "fmt"

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
	e.ensureBoxedBigIntHooks()
	e.ensureFnMeta()
	e.declareFn("__kml_fn_identity_dyn", "declare ptr @__kml_fn_identity_dyn(ptr)")
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
  ; both arrayheader-kind (kind bits 2): field 0 of the any-array box is the
  ; shared live array header pointer (TDD-00212 Stage 3), so comparing it below
  ; is reference identity — two aliases of one array share the header and stay
  ; equal across a reallocating push (ADR-00478)
  %aarr0 = icmp eq i64 %ak, 2
  %barr0 = icmp eq i64 %bk, 2
  %aarr = and i1 %aptr, %aarr0
  %barr = and i1 %bptr, %barr0
  %botharr = and i1 %aarr, %barr
  br i1 %botharr, label %cmp_array, label %not_array
not_array:
  ; both dynamic-function records (kind bits 7): one function boxed twice
  ; (each boxing builds its own adapter record around the same closure
  ; header) is one function — compare the records' identities
  %afn0 = icmp eq i64 %ak, 7
  %bfn0 = icmp eq i64 %bk, 7
  %afn = and i1 %aptr, %afn0
  %bfn = and i1 %bptr, %bfn0
  %bothfn = and i1 %afn, %bfn
  br i1 %bothfn, label %cmp_fn, label %not_fn
cmp_fn:
  %ra = and i64 %a, -8
  %rb = and i64 %b, -8
  %rap = inttoptr i64 %ra to ptr
  %rbp = inttoptr i64 %rb to ptr
  %ida = call ptr @__kml_fn_identity_dyn(ptr %rap)
  %idb = call ptr @__kml_fn_identity_dyn(ptr %rbp)
  %fn_eq = icmp eq ptr %ida, %idb
  ret i1 %fn_eq
not_fn:
  ; both object-kind (kind bits 1) boxed bigint cells (field 0 = the exact
  ; magic word, emit_bigint_box.go): bigints compare by value (TDD-00229)
  %aobj0 = icmp eq i64 %ak, 1
  %bobj0 = icmp eq i64 %bk, 1
  %aobj = and i1 %aptr, %aobj0
  %bobj = and i1 %bptr, %bobj0
  %bothobj = and i1 %aobj, %bobj
  br i1 %bothobj, label %cmp_obj, label %not_equal
cmp_obj:
  %oa = and i64 %a, -8
  %ob = and i64 %b, -8
  %oap = inttoptr i64 %oa to ptr
  %obp = inttoptr i64 %ob to ptr
  %fa = load i64, ptr %oap, align 8
  %fb = load i64, ptr %obp, align 8
  %abig = icmp eq i64 %fa, 9219994340110199574
  %bbig = icmp eq i64 %fb, 9219994340110199574
  %bothbig = and i1 %abig, %bbig
  br i1 %bothbig, label %cmp_big, label %host_chk
host_chk:
  ; two host boxes (emit_hostbox.go) of one handle are one value
  %ham = and i64 %fa, ` + fmt.Sprint(kmlHdrMagicMask|hostTypeIDFlag) + `
  %hbm = and i64 %fb, ` + fmt.Sprint(kmlHdrMagicMask|hostTypeIDFlag) + `
  %ahost = icmp eq i64 %ham, ` + fmt.Sprint(kmlHdrMagic|hostTypeIDFlag) + `
  %bhost = icmp eq i64 %hbm, ` + fmt.Sprint(kmlHdrMagic|hostTypeIDFlag) + `
  %bothhost = and i1 %ahost, %bhost
  br i1 %bothhost, label %cmp_host, label %not_equal
cmp_host:
  %hpa = getelementptr { i64, ptr }, ptr %oap, i32 0, i32 1
  %hpb = getelementptr { i64, ptr }, ptr %obp, i32 0, i32 1
  %hva = load ptr, ptr %hpa, align 8
  %hvb = load ptr, ptr %hpb, align 8
  %host_eq = icmp eq ptr %hva, %hvb
  ret i1 %host_eq
cmp_big:
  %big_eq = call i1 @__kml_boxed_bigint_eq(ptr %oap, ptr %obp)
  ret i1 %big_eq
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

// ensureObjectIs defines Object.is (lib/es.d.ts): SameValue over two boxed
// values — a number is itself (NaN is NaN, +0 is not -0), anything else is
// strict equality.
func (e *Emitter) ensureObjectIs() {
	if e.fnDecls["__kml_Object_is"] {
		return
	}
	e.fnDecls["__kml_Object_is"] = true
	e.ensureAnyEq()
	e.emitGlobal(fmt.Sprintf(`
define zeroext i1 @__kml_Object_is(i64 %%a, i64 %%b) {
entry:
  %%anum = icmp uge i64 %%a, %[1]d
  %%bnum = icmp uge i64 %%b, %[1]d
  %%bothnum = and i1 %%anum, %%bnum
  br i1 %%bothnum, label %%num, label %%other
num:
  %%samebits = icmp eq i64 %%a, %%b
  %%abits = sub i64 %%a, %[1]d
  %%bbits = sub i64 %%b, %[1]d
  %%ad = bitcast i64 %%abits to double
  %%bd = bitcast i64 %%bbits to double
  %%anan = fcmp uno double %%ad, %%ad
  %%bnan = fcmp uno double %%bd, %%bd
  %%bothnan = and i1 %%anan, %%bnan
  %%r = or i1 %%samebits, %%bothnan
  ret i1 %%r
other:
  %%eq = call i1 @__kml_any_eq(i64 %%a, i64 %%b)
  ret i1 %%eq
}`, nbDoubleOffset))
}
