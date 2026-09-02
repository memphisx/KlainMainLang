package llvm

// runtime_dynarr.go — the D1 dynamic array (TDD-00155 Stage 2), box tag 11
// (kmlTagDynArray). Layout:
//
//	DynArr header (24 bytes):
//	  offset 0:  i64 len
//	  offset 8:  i64 cap
//	  offset 16: ptr data — element array (realloc-grown, doubling)
//
//	Element (16 bytes): { i64 tag, i64 payload } — the { i8, i64 } any box
//	with the tag widened to i64, same convention as a dynobj entry.
//
// This is the element universe untyped JSON.parse needs (every element is a
// self-describing box) — deliberately distinct from tag 7, which boxes a
// *statically typed* array whose element width/type lives at the call sites.
// dynjsonsrc/dynjson.c reads this layout directly (kept in sync by the
// comment there); all memory is plain malloc/realloc for the -mm=gc shim.

func (e *Emitter) ensureDynArr() {
	if e.usedDynArr {
		return
	}
	e.usedDynArr = true
	e.ensureNanBox()
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureStrcmp()
	e.ensureStrtoll()
	e.ensureSprintf()
	e.ensureStrlen()
	e.emitGlobal(`
define ptr @__kml_dynarr_new(i64 %cap0) {
entry:
  %hdr = call ptr @malloc(i64 24)
  store i64 0, ptr %hdr, align 8
  %small = icmp slt i64 %cap0, 4
  %cap = select i1 %small, i64 4, i64 %cap0
  %capp = getelementptr i8, ptr %hdr, i64 8
  store i64 %cap, ptr %capp, align 8
  %bytes = mul i64 %cap, 16
  %data = call ptr @malloc(i64 %bytes)
  %datap = getelementptr i8, ptr %hdr, i64 16
  store ptr %data, ptr %datap, align 8
  ret ptr %hdr
}

define i64 @__kml_dynarr_len(ptr %a) {
entry:
  %len = load i64, ptr %a, align 8
  ret i64 %len
}

define void @__kml_dynarr_grow(ptr %a, i64 %need) {
entry:
  %capp = getelementptr i8, ptr %a, i64 8
  %cap = load i64, ptr %capp, align 8
  %fits = icmp slt i64 %need, %cap
  br i1 %fits, label %done, label %grow
grow:
  %doubled = mul i64 %cap, 2
  %needcap = add i64 %need, 1
  %bigger = icmp sgt i64 %doubled, %needcap
  %newcap = select i1 %bigger, i64 %doubled, i64 %needcap
  %datap = getelementptr i8, ptr %a, i64 16
  %data = load ptr, ptr %datap, align 8
  %bytes = mul i64 %newcap, 16
  %newdata = call ptr @realloc(ptr %data, i64 %bytes)
  store ptr %newdata, ptr %datap, align 8
  store i64 %newcap, ptr %capp, align 8
  br label %done
done:
  ret void
}

define void @__kml_dynarr_push(ptr %a, i64 %v) {
entry:
  %len = load i64, ptr %a, align 8
  call void @__kml_dynarr_grow(ptr %a, i64 %len)
  call void @__kml_dynarr_store(ptr %a, i64 %len, i64 %v)
  %newlen = add i64 %len, 1
  store i64 %newlen, ptr %a, align 8
  ret void
}

define void @__kml_dynarr_store(ptr %a, i64 %i, i64 %v) {
entry:
  %tag8 = call i8 @__kml_nb_tag(i64 %v)
  %tag = zext i8 %tag8 to i64
  %pay = call i64 @__kml_nb_pay(i64 %v)
  %datap = getelementptr i8, ptr %a, i64 16
  %data = load ptr, ptr %datap, align 8
  %off = mul i64 %i, 16
  %ep = getelementptr i8, ptr %data, i64 %off
  store i64 %tag, ptr %ep, align 8
  %payp = getelementptr i8, ptr %ep, i64 8
  store i64 %pay, ptr %payp, align 8
  ret void
}

define i64 @__kml_dynarr_at(ptr %a, i64 %i) {
entry:
  %len = load i64, ptr %a, align 8
  %neg = icmp slt i64 %i, 0
  %oob = icmp sge i64 %i, %len
  %bad = or i1 %neg, %oob
  br i1 %bad, label %undef, label %ok
ok:
  %datap = getelementptr i8, ptr %a, i64 16
  %data = load ptr, ptr %datap, align 8
  %off = mul i64 %i, 16
  %ep = getelementptr i8, ptr %data, i64 %off
  %tag = load i64, ptr %ep, align 8
  %tag8 = trunc i64 %tag to i8
  %payp = getelementptr i8, ptr %ep, i64 8
  %pay = load i64, ptr %payp, align 8
  %r = call i64 @__kml_nb_pack(i8 %tag8, i64 %pay)
  ret i64 %r
undef:
  ret i64 10
}

; __kml_dynarr_put implements arr[i] = v with JS extension semantics: writing
; past the end grows the array and fills the gap with undefined holes.
define void @__kml_dynarr_put(ptr %a, i64 %i, i64 %v) {
entry:
  %neg = icmp slt i64 %i, 0
  br i1 %neg, label %done, label %ok
ok:
  %len = load i64, ptr %a, align 8
  %inbounds = icmp slt i64 %i, %len
  br i1 %inbounds, label %write, label %extend
extend:
  call void @__kml_dynarr_grow(ptr %a, i64 %i)
  br label %fill
fill:
  %j = phi i64 [ %len, %extend ], [ %jnext, %fillbody ]
  %filled = icmp sge i64 %j, %i
  br i1 %filled, label %extended, label %fillbody
fillbody:
  call void @__kml_dynarr_store(ptr %a, i64 %j, i64 10)
  %jnext = add i64 %j, 1
  br label %fill
extended:
  %newlen = add i64 %i, 1
  store i64 %newlen, ptr %a, align 8
  br label %write
write:
  call void @__kml_dynarr_store(ptr %a, i64 %i, i64 %v)
  br label %done
done:
  ret void
}

; __kml_dynarr_index parses key as a canonical non-negative array index.
; Returns the index, or -1 when key isn't one (then it's a plain property).
define i64 @__kml_dynarr_index(ptr %key) {
entry:
  %endp = alloca ptr, align 8
  %v = call i64 @strtoll(ptr %key, ptr %endp, i32 10)
  %end = load ptr, ptr %endp, align 8
  %consumed = icmp ne ptr %end, %key
  %endc = load i8, ptr %end, align 1
  %atnul = icmp eq i8 %endc, 0
  %nonneg = icmp sge i64 %v, 0
  %ok0 = and i1 %consumed, %atnul
  %ok = and i1 %ok0, %nonneg
  br i1 %ok, label %yes, label %no
yes:
  ret i64 %v
no:
  ret i64 -1
}

define i64 @__kml_dynarr_get_by_key(ptr %a, ptr %key) {
entry:
  %isLen = call i32 @strcmp(ptr %key, ptr @.kml_dynarr_length)
  %lenq = icmp eq i32 %isLen, 0
  br i1 %lenq, label %retlen, label %tryidx
retlen:
  %len = load i64, ptr %a, align 8
  %l0 = call i64 @__kml_nb_pack(i8 0, i64 %len)
  ret i64 %l0
tryidx:
  %idx = call i64 @__kml_dynarr_index(ptr %key)
  %bad = icmp slt i64 %idx, 0
  br i1 %bad, label %undef, label %at
at:
  %r = call i64 @__kml_dynarr_at(ptr %a, i64 %idx)
  ret i64 %r
undef:
  ret i64 10
}

; Returns false when the key isn't a writable index (a plain expando property
; or a length write) — the caller turns that into a clean TypeError.
define i1 @__kml_dynarr_set_by_key(ptr %a, ptr %key, i64 %v) {
entry:
  %idx = call i64 @__kml_dynarr_index(ptr %key)
  %bad = icmp slt i64 %idx, 0
  br i1 %bad, label %no, label %put
put:
  call void @__kml_dynarr_put(ptr %a, i64 %idx, i64 %v)
  ret i1 true
no:
  ret i1 false
}

; Index strings "0".."len-1" as length-prefixed heap strings (Object.keys /
; for...in over a dynamic array).
define { ptr, i64 } @__kml_dynarr_keys(ptr %a) {
entry:
  %len = load i64, ptr %a, align 8
  %bytes = mul i64 %len, 8
  %arr = call ptr @malloc(i64 %bytes)
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %body ]
  %done = icmp sge i64 %i, %len
  br i1 %done, label %out, label %body
body:
  %base = call ptr @malloc(i64 32)
  %buf = getelementptr i8, ptr %base, i64 8
  %n = call i32 (ptr, ptr, ...) @sprintf(ptr %buf, ptr @.kml_dynarr_lld, i64 %i)
  %n64 = zext i32 %n to i64
  store i64 %n64, ptr %base, align 8
  %slot = getelementptr ptr, ptr %arr, i64 %i
  store ptr %buf, ptr %slot, align 8
  %inext = add i64 %i, 1
  br label %loop
out:
  %r0 = insertvalue { ptr, i64 } undef, ptr %arr, 0
  %r1 = insertvalue { ptr, i64 } %r0, i64 %len, 1
  ret { ptr, i64 } %r1
}

@.kml_dynarr_length = private unnamed_addr constant [7 x i8] c"length\00"
@.kml_dynarr_lld = private unnamed_addr constant [5 x i8] c"%lld\00"`)
}

// ensureDynJSONFromNode defines the recursive KmlJsonNode → dynamic-value
// converter behind untyped JSON.parse (TDD-00155 Stage 2): scalars box as
// their tags (a number becomes an int unless its lexeme carries a fraction/
// exponent or is too long for exact i64), a JSON array becomes a tag-11
// dynamic array, a JSON object a tag-10 dynamic bag. Strings/keys are copied
// (string_dup / the bag's own key copy), so the tree can be freed afterward.
func (e *Emitter) ensureDynJSONFromNode() {
	if e.usedDynJSONFromNode {
		return
	}
	e.usedDynJSONFromNode = true
	e.ensureJSONParseTree()
	e.ensureDynObj()
	e.ensureDynArr()
	e.ensureStrchr()
	e.ensureStrlen()
	e.ensureAtoll()
	e.ensureStrtod()
	e.emitGlobal(`declare ptr @__kml_json_key(ptr, i64)`)
	e.emitGlobal(`declare ptr @__kml_json_val(ptr, i64)`)
	e.emitGlobal(`
define i64 @__kml_dynjson_from_node(ptr %n) {
entry:
  %kind = call i32 @__kml_json_kind(ptr %n)
  switch i32 %kind, label %knull [
    i32 1, label %kbool
    i32 2, label %knum
    i32 3, label %kstr
    i32 4, label %karr
    i32 5, label %kobj
  ]
knull:
  ret i64 2
kbool:
  %b = call i32 @__kml_json_bool(ptr %n)
  %bz = zext i32 %b to i64
  %istrue = icmp ne i64 %bz, 0
  %br0 = select i1 %istrue, i64 7, i64 6
  ret i64 %br0
knum:
  %lex = call ptr @__kml_json_num_lexeme(ptr %n)
  %dot = call ptr @strchr(ptr %lex, i32 46)
  %e1 = call ptr @strchr(ptr %lex, i32 101)
  %e2 = call ptr @strchr(ptr %lex, i32 69)
  %hasdot = icmp ne ptr %dot, null
  %hase1 = icmp ne ptr %e1, null
  %hase2 = icmp ne ptr %e2, null
  %llen = call i64 @strlen(ptr %lex)
  %toolong = icmp ugt i64 %llen, 18
  %f0 = or i1 %hasdot, %hase1
  %f1 = or i1 %f0, %hase2
  %isfloat = or i1 %f1, %toolong
  br i1 %isfloat, label %numf, label %numi
numf:
  %d = call double @strtod(ptr %lex, ptr null)
  %dbits = bitcast double %d to i64
  %fr0 = call i64 @__kml_nb_pack(i8 1, i64 %dbits)
  ret i64 %fr0
numi:
  %iv = call i64 @atoll(ptr %lex)
  %ir0 = call i64 @__kml_nb_pack(i8 0, i64 %iv)
  ret i64 %ir0
kstr:
  %s = call ptr @__kml_json_string_dup(ptr %n)
  %sp = ptrtoint ptr %s to i64
  ret i64 %sp
karr:
  %alen = call i64 @__kml_json_len(ptr %n)
  %arr = call ptr @__kml_dynarr_new(i64 %alen)
  br label %aloop
aloop:
  %ai = phi i64 [ 0, %karr ], [ %ainext, %abody ]
  %adone = icmp sge i64 %ai, %alen
  br i1 %adone, label %aout, label %abody
abody:
  %item = call ptr @__kml_json_item(ptr %n, i64 %ai)
  %av = call i64 @__kml_dynjson_from_node(ptr %item)
  call void @__kml_dynarr_push(ptr %arr, i64 %av)
  %ainext = add i64 %ai, 1
  br label %aloop
aout:
  %ap = ptrtoint ptr %arr to i64
  %ar0 = or i64 %ap, 6
  ret i64 %ar0
kobj:
  %olen = call i64 @__kml_json_len(ptr %n)
  %bag = call ptr @__kml_dynobj_new()
  br label %oloop
oloop:
  %oi = phi i64 [ 0, %kobj ], [ %oinext, %obody ]
  %odone = icmp sge i64 %oi, %olen
  br i1 %odone, label %oout, label %obody
obody:
  %key = call ptr @__kml_json_key(ptr %n, i64 %oi)
  %valn = call ptr @__kml_json_val(ptr %n, i64 %oi)
  %ov = call i64 @__kml_dynjson_from_node(ptr %valn)
  call void @__kml_dynobj_set(ptr %bag, ptr %key, i64 %ov)
  %oinext = add i64 %oi, 1
  br label %oloop
oout:
  %bp = ptrtoint ptr %bag to i64
  %or0 = or i64 %bp, 5
  ret i64 %or0
}`)
}
