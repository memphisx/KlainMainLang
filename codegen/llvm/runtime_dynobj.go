package llvm

// runtime_dynobj.go — the D1 dynamic object runtime (TDD-00155 Stage 1): a
// per-instance property bag behind box tag 10 (kmlTagDynObject). Layout:
//
//	DynObj header (40 bytes):
//	  offset 0:  i64 flags   — low 32 bits magic 0x444C4D4B ("KMLD"); high bits
//	                           reserved for EXTENSIBLE/SEALED/FROZEN (Stage 5)
//	  offset 8:  ptr proto   — prototype pointer (live since Stage 3: get and
//	                           the `in` operator walk it; set/delete/keys stay
//	                           own-table; set_proto keeps chains acyclic)
//	  offset 16: ptr props   — property entry array (realloc-grown, doubling)
//	  offset 24: i64 count
//	  offset 32: i64 cap
//
//	Property entry (32 bytes):
//	  offset 0:  ptr key     — owned NUL-terminated UTF-8 copy
//	  offset 8:  i64 tag     — the logical any-box tag, widened to i64
//	  offset 16: i64 payload — the decoded any-box payload; for an ACCESSOR
//	                           entry: ptr to a 16-byte { getterRec, setterRec }
//	                           pair of raw dynamic-function records (either
//	                           slot null when absent)
//	  offset 24: i64 attrs   — WRITABLE(1)|ENUMERABLE(2)|CONFIGURABLE(4)|
//	                           ACCESSOR(8); =7 on plain assignment. Enforced
//	                           since Stage 5: get/set invoke accessors (with
//	                           the original receiver as `this`), set honors
//	                           WRITABLE + the header's non-extensible bit
//	                           (1<<32), delete honors CONFIGURABLE, and
//	                           Object.keys/for...in enumerate ENUMERABLE only
//
// The entry array is insertion-ordered — that IS JS string-key enumeration
// order for objects built this way — and lookup is a linear strcmp scan
// (correctness-first; a per-entry hash word and D2 hidden classes are later,
// caller-invisible optimizations). Values are self-describing any boxes: no
// static type exists at a dynamic read site, so the per-site-typed
// __kml_map_str_* store cannot back this. All memory comes from plain
// calloc/malloc/realloc so -mm=gc inherits it through the allocator shim.
//
// A deleted entry's key copy is not freed: under -mm=gc it is collected once
// unreferenced; under -mm=manual it leaks, the same contract closures and
// boxes already have.

func (e *Emitter) ensureDynObj() {
	if e.usedDynObj {
		return
	}
	e.usedDynObj = true
	e.ensureNanBox()
	e.ensureAnyOps() // Proxy set/has/delete traps coerce results via ToBoolean
	e.ensureCalloc()
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureMemcpy()
	e.ensureMemmove()
	e.ensureStrcmp()
	e.ensureStrlen()
	e.emitGlobal(`
define ptr @__kml_dynobj_new() {
entry:
  %o = call ptr @calloc(i64 1, i64 40)
  store i64 1145867595, ptr %o, align 8
  ret ptr %o
}

define i64 @__kml_dynobj_find(ptr %o, ptr %key) {
entry:
  %propsp = getelementptr i8, ptr %o, i64 16
  %props = load ptr, ptr %propsp, align 8
  %countp = getelementptr i8, ptr %o, i64 24
  %count = load i64, ptr %countp, align 8
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %next ]
  %done = icmp sge i64 %i, %count
  br i1 %done, label %miss, label %body
body:
  %off = mul i64 %i, 32
  %ep = getelementptr i8, ptr %props, i64 %off
  %ekey = load ptr, ptr %ep, align 8
  %c = call i32 @strcmp(ptr %ekey, ptr %key)
  %eq = icmp eq i32 %c, 0
  br i1 %eq, label %hit, label %next
next:
  %inext = add i64 %i, 1
  br label %loop
hit:
  ret i64 %i
miss:
  ret i64 -1
}

; v is a NaN-boxed word (TDD-00156); the entry slots keep the decoded
; (tag, payload) pair layout so dynjson.c's walkers stay unchanged.
define void @__kml_dynobj_set(ptr %o, ptr %key, i64 %v) {
entry:
  %tag8 = call i8 @__kml_nb_tag(i64 %v)
  %tag = zext i8 %tag8 to i64
  %pay = call i64 @__kml_nb_pay(i64 %v)
  %idx = call i64 @__kml_dynobj_find(ptr %o, ptr %key)
  %found = icmp sge i64 %idx, 0
  br i1 %found, label %update, label %insert
update:
  %propsp0 = getelementptr i8, ptr %o, i64 16
  %props0 = load ptr, ptr %propsp0, align 8
  %off0 = mul i64 %idx, 32
  %ep0 = getelementptr i8, ptr %props0, i64 %off0
  %tagp0 = getelementptr i8, ptr %ep0, i64 8
  store i64 %tag, ptr %tagp0, align 8
  %payp0 = getelementptr i8, ptr %ep0, i64 16
  store i64 %pay, ptr %payp0, align 8
  ret void
insert:
  %countp = getelementptr i8, ptr %o, i64 24
  %count = load i64, ptr %countp, align 8
  %capp = getelementptr i8, ptr %o, i64 32
  %cap = load i64, ptr %capp, align 8
  %full = icmp sge i64 %count, %cap
  br i1 %full, label %grow, label %append
grow:
  %iszero = icmp eq i64 %cap, 0
  %doubled = mul i64 %cap, 2
  %newcap = select i1 %iszero, i64 8, i64 %doubled
  %propspg = getelementptr i8, ptr %o, i64 16
  %propsg = load ptr, ptr %propspg, align 8
  %bytes = mul i64 %newcap, 32
  %newprops = call ptr @realloc(ptr %propsg, i64 %bytes)
  store ptr %newprops, ptr %propspg, align 8
  store i64 %newcap, ptr %capp, align 8
  br label %append
append:
  %propsp1 = getelementptr i8, ptr %o, i64 16
  %props1 = load ptr, ptr %propsp1, align 8
  %off1 = mul i64 %count, 32
  %ep1 = getelementptr i8, ptr %props1, i64 %off1
  ; key copy is length-prefixed like every heap string (TDD-00120): i64 len
  ; at base, data at base+8 — consumers read the header at key-8.
  %klen = call i64 @strlen(ptr %key)
  %kbytes = add i64 %klen, 9
  %kbase = call ptr @malloc(i64 %kbytes)
  store i64 %klen, ptr %kbase, align 8
  %kcopy = getelementptr i8, ptr %kbase, i64 8
  %kcopylen = add i64 %klen, 1
  %ignore = call ptr @memcpy(ptr %kcopy, ptr %key, i64 %kcopylen)
  store ptr %kcopy, ptr %ep1, align 8
  %tagp1 = getelementptr i8, ptr %ep1, i64 8
  store i64 %tag, ptr %tagp1, align 8
  %payp1 = getelementptr i8, ptr %ep1, i64 16
  store i64 %pay, ptr %payp1, align 8
  %attrp1 = getelementptr i8, ptr %ep1, i64 24
  store i64 7, ptr %attrp1, align 8
  %newcount = add i64 %count, 1
  store i64 %newcount, ptr %countp, align 8
  ret void
}

; get walks the prototype chain (TDD-00155 Stage 3): the own table first,
; then each proto link (acyclic by construction — set_proto refuses cycles).
; An ACCESSOR hit (Stage 5) calls its getter through the dynamic ABI with
; the *original receiver* boxed as 'this' — inherited getters see the object
; the read started from, as in JS. A getter-less accessor reads undefined.
define i64 @__kml_dynobj_get(ptr %o, ptr %key) {
entry:
  %argv = alloca [3 x i64], align 8
  %flags0 = load i64, ptr %o, align 8
  %pb = and i64 %flags0, 8589934592
  %isp = icmp ne i64 %pb, 0
  br i1 %isp, label %proxy, label %walkstart
proxy:
  ; Stage 7: a Proxy header — target at +8, handler at +16. The "get" trap
  ; runs as trap(target, key, receiver) with this=handler; no trap forwards.
  %ptargetp = getelementptr i8, ptr %o, i64 8
  %ptarget = load ptr, ptr %ptargetp, align 8
  %phandlerp = getelementptr i8, ptr %o, i64 16
  %phandler = load ptr, ptr %phandlerp, align 8
  %trap = call i64 @__kml_dynobj_get(ptr %phandler, ptr @.kml_trap_get)
  %ttag = call i8 @__kml_nb_tag(i64 %trap)
  %isfn = icmp eq i8 %ttag, 12
  br i1 %isfn, label %calltrap, label %pfwd
pfwd:
  %fv = call i64 @__kml_dynobj_get(ptr %ptarget, ptr %key)
  ret i64 %fv
calltrap:
  %reci = call i64 @__kml_nb_pay(i64 %trap)
  %rec = inttoptr i64 %reci to ptr
  %tfp = load ptr, ptr %rec, align 8
  %tenvp = getelementptr i8, ptr %rec, i64 8
  %tenv = load ptr, ptr %tenvp, align 8
  %ti = ptrtoint ptr %ptarget to i64
  %tbox = or i64 %ti, 5
  %a0 = getelementptr i64, ptr %argv, i64 0
  store i64 %tbox, ptr %a0, align 8
  %ki = ptrtoint ptr %key to i64
  %a1 = getelementptr i64, ptr %argv, i64 1
  store i64 %ki, ptr %a1, align 8
  %oi2 = ptrtoint ptr %o to i64
  %rbox = or i64 %oi2, 5
  %a2 = getelementptr i64, ptr %argv, i64 2
  store i64 %rbox, ptr %a2, align 8
  %hi = ptrtoint ptr %phandler to i64
  %hbox = or i64 %hi, 5
  %tv = call i64 %tfp(ptr %tenv, i64 %hbox, i64 3, ptr %argv)
  ret i64 %tv
walkstart:
  br label %walk
walk:
  %cur = phi ptr [ %o, %walkstart ], [ %proto, %next ]
  %idx = call i64 @__kml_dynobj_find(ptr %cur, ptr %key)
  %found = icmp sge i64 %idx, 0
  br i1 %found, label %hit, label %next
next:
  %protop = getelementptr i8, ptr %cur, i64 8
  %proto = load ptr, ptr %protop, align 8
  %endq = icmp eq ptr %proto, null
  br i1 %endq, label %miss, label %walk
hit:
  %attrs = call i64 @__kml_dynobj_attrs_at(ptr %cur, i64 %idx)
  %accb = and i64 %attrs, 8
  %isacc = icmp ne i64 %accb, 0
  br i1 %isacc, label %acc, label %data
data:
  %r = call i64 @__kml_dynobj_get_at(ptr %cur, i64 %idx)
  ret i64 %r
acc:
  %pairi = call i64 @__kml_dynobj_rawpay_at(ptr %cur, i64 %idx)
  %pair = inttoptr i64 %pairi to ptr
  %getter = load ptr, ptr %pair, align 8
  %noget = icmp eq ptr %getter, null
  br i1 %noget, label %miss, label %callget
callget:
  %fp = load ptr, ptr %getter, align 8
  %envp = getelementptr i8, ptr %getter, i64 8
  %env = load ptr, ptr %envp, align 8
  %oi = ptrtoint ptr %o to i64
  %recv = or i64 %oi, 5
  %gv = call i64 %fp(ptr %env, i64 %recv, i64 0, ptr null)
  ret i64 %gv
miss:
  ret i64 10
}

@.kml_trap_get = private unnamed_addr constant [4 x i8] c"get\00"
@.kml_trap_set = private unnamed_addr constant [4 x i8] c"set\00"
@.kml_trap_has = private unnamed_addr constant [4 x i8] c"has\00"
@.kml_trap_del = private unnamed_addr constant [15 x i8] c"deleteProperty\00"

define i64 @__kml_dynobj_attrs_at(ptr %o, i64 %i) {
entry:
  %propsp = getelementptr i8, ptr %o, i64 16
  %props = load ptr, ptr %propsp, align 8
  %off = mul i64 %i, 32
  %ep = getelementptr i8, ptr %props, i64 %off
  %ap = getelementptr i8, ptr %ep, i64 24
  %a = load i64, ptr %ap, align 8
  ret i64 %a
}

define i64 @__kml_dynobj_rawtag_at(ptr %o, i64 %i) {
entry:
  %propsp = getelementptr i8, ptr %o, i64 16
  %props = load ptr, ptr %propsp, align 8
  %off = mul i64 %i, 32
  %ep = getelementptr i8, ptr %props, i64 %off
  %tp = getelementptr i8, ptr %ep, i64 8
  %t = load i64, ptr %tp, align 8
  ret i64 %t
}

define i64 @__kml_dynobj_rawpay_at(ptr %o, i64 %i) {
entry:
  %propsp = getelementptr i8, ptr %o, i64 16
  %props = load ptr, ptr %propsp, align 8
  %off = mul i64 %i, 32
  %ep = getelementptr i8, ptr %props, i64 %off
  %pp = getelementptr i8, ptr %ep, i64 16
  %p = load i64, ptr %pp, align 8
  ret i64 %p
}

; setv — the checked assignment path (Stage 5). Status: 0 ok, 1 read-only,
; 2 not extensible, 3 accessor without a setter. Walks the chain: an
; inherited accessor's setter runs with the original receiver; an inherited
; read-only data property blocks the write; an inherited *writable* data
; property shadows as an own define, as does a miss (extensibility
; permitting).
define i64 @__kml_dynobj_setv(ptr %o, ptr %key, i64 %v) {
entry:
  %argv = alloca i64, align 8
  %pargv = alloca [4 x i64], align 8
  %flags0 = load i64, ptr %o, align 8
  %pb = and i64 %flags0, 8589934592
  %isp = icmp ne i64 %pb, 0
  br i1 %isp, label %proxy, label %walkstart
proxy:
  %ptargetp = getelementptr i8, ptr %o, i64 8
  %ptarget = load ptr, ptr %ptargetp, align 8
  %phandlerp = getelementptr i8, ptr %o, i64 16
  %phandler = load ptr, ptr %phandlerp, align 8
  %trap = call i64 @__kml_dynobj_get(ptr %phandler, ptr @.kml_trap_set)
  %ttag = call i8 @__kml_nb_tag(i64 %trap)
  %isfn = icmp eq i8 %ttag, 12
  br i1 %isfn, label %calltrap, label %pfwd
pfwd:
  %fs = call i64 @__kml_dynobj_setv(ptr %ptarget, ptr %key, i64 %v)
  ret i64 %fs
calltrap:
  %reci = call i64 @__kml_nb_pay(i64 %trap)
  %rec = inttoptr i64 %reci to ptr
  %tfp = load ptr, ptr %rec, align 8
  %tenvp = getelementptr i8, ptr %rec, i64 8
  %tenv = load ptr, ptr %tenvp, align 8
  %ti = ptrtoint ptr %ptarget to i64
  %tbox = or i64 %ti, 5
  %pa0 = getelementptr i64, ptr %pargv, i64 0
  store i64 %tbox, ptr %pa0, align 8
  %ki = ptrtoint ptr %key to i64
  %pa1 = getelementptr i64, ptr %pargv, i64 1
  store i64 %ki, ptr %pa1, align 8
  %pa2 = getelementptr i64, ptr %pargv, i64 2
  store i64 %v, ptr %pa2, align 8
  %oi2 = ptrtoint ptr %o to i64
  %rbox = or i64 %oi2, 5
  %pa3 = getelementptr i64, ptr %pargv, i64 3
  store i64 %rbox, ptr %pa3, align 8
  %hi = ptrtoint ptr %phandler to i64
  %hbox = or i64 %hi, 5
  %tv = call i64 %tfp(ptr %tenv, i64 %hbox, i64 4, ptr %pargv)
  %tb = call i1 @__kml_any_tobool(i64 %tv)
  %st = select i1 %tb, i64 0, i64 1
  ret i64 %st
walkstart:
  br label %walk
walk:
  %cur = phi ptr [ %o, %walkstart ], [ %proto, %next ]
  %idx = call i64 @__kml_dynobj_find(ptr %cur, ptr %key)
  %found = icmp sge i64 %idx, 0
  br i1 %found, label %hit, label %next
next:
  %protop = getelementptr i8, ptr %cur, i64 8
  %proto = load ptr, ptr %protop, align 8
  %endq = icmp eq ptr %proto, null
  br i1 %endq, label %defown, label %walk
hit:
  %attrs = call i64 @__kml_dynobj_attrs_at(ptr %cur, i64 %idx)
  %accb = and i64 %attrs, 8
  %isacc = icmp ne i64 %accb, 0
  br i1 %isacc, label %acc, label %datahit
acc:
  %pairi = call i64 @__kml_dynobj_rawpay_at(ptr %cur, i64 %idx)
  %pair = inttoptr i64 %pairi to ptr
  %setp = getelementptr i8, ptr %pair, i64 8
  %setter = load ptr, ptr %setp, align 8
  %noset = icmp eq ptr %setter, null
  br i1 %noset, label %ret3, label %callset
callset:
  store i64 %v, ptr %argv, align 8
  %fp = load ptr, ptr %setter, align 8
  %envp = getelementptr i8, ptr %setter, i64 8
  %env = load ptr, ptr %envp, align 8
  %oi = ptrtoint ptr %o to i64
  %recv = or i64 %oi, 5
  %ignore = call i64 %fp(ptr %env, i64 %recv, i64 1, ptr %argv)
  ret i64 0
datahit:
  %wb = and i64 %attrs, 1
  %rw = icmp eq i64 %wb, 0
  br i1 %rw, label %ret1, label %writable
writable:
  %isown = icmp eq ptr %cur, %o
  br i1 %isown, label %update, label %defown
update:
  %propsp2 = getelementptr i8, ptr %cur, i64 16
  %props2 = load ptr, ptr %propsp2, align 8
  %off2 = mul i64 %idx, 32
  %ep2 = getelementptr i8, ptr %props2, i64 %off2
  %tag8 = call i8 @__kml_nb_tag(i64 %v)
  %tag = zext i8 %tag8 to i64
  %pay = call i64 @__kml_nb_pay(i64 %v)
  %tp2 = getelementptr i8, ptr %ep2, i64 8
  store i64 %tag, ptr %tp2, align 8
  %pp2 = getelementptr i8, ptr %ep2, i64 16
  store i64 %pay, ptr %pp2, align 8
  ret i64 0
defown:
  %flags = load i64, ptr %o, align 8
  %nxb = and i64 %flags, 4294967296
  %nonext = icmp ne i64 %nxb, 0
  br i1 %nonext, label %checkown, label %doset
checkown:
  ; a non-extensible object may still UPDATE an existing own writable
  ; property (we only reach defown for inherited-writable or miss — an own
  ; hit updated above), so a miss here is a real extensibility rejection.
  ret i64 2
doset:
  call void @__kml_dynobj_set(ptr %o, ptr %key, i64 %v)
  ret i64 0
ret1:
  ret i64 1
ret3:
  ret i64 3
}

; has = own-only (Object.hasOwn / hasOwnProperty).
define i1 @__kml_dynobj_has(ptr %o, ptr %key) {
entry:
  %idx = call i64 @__kml_dynobj_find(ptr %o, ptr %key)
  %found = icmp sge i64 %idx, 0
  ret i1 %found
}

; has_chain = the "in" operator: own table, then the prototype chain. A
; Proxy consults its "has" trap, else forwards to the target.
define i1 @__kml_dynobj_has_chain(ptr %o, ptr %key) {
entry:
  %pargv = alloca [2 x i64], align 8
  %flags0 = load i64, ptr %o, align 8
  %pb = and i64 %flags0, 8589934592
  %isp = icmp ne i64 %pb, 0
  br i1 %isp, label %proxy, label %walkstart
proxy:
  %ptargetp = getelementptr i8, ptr %o, i64 8
  %ptarget = load ptr, ptr %ptargetp, align 8
  %phandlerp = getelementptr i8, ptr %o, i64 16
  %phandler = load ptr, ptr %phandlerp, align 8
  %trap = call i64 @__kml_dynobj_get(ptr %phandler, ptr @.kml_trap_has)
  %ttag = call i8 @__kml_nb_tag(i64 %trap)
  %isfn = icmp eq i8 %ttag, 12
  br i1 %isfn, label %calltrap, label %pfwd
pfwd:
  %fh = call i1 @__kml_dynobj_has_chain(ptr %ptarget, ptr %key)
  ret i1 %fh
calltrap:
  %reci = call i64 @__kml_nb_pay(i64 %trap)
  %rec = inttoptr i64 %reci to ptr
  %tfp = load ptr, ptr %rec, align 8
  %tenvp = getelementptr i8, ptr %rec, i64 8
  %tenv = load ptr, ptr %tenvp, align 8
  %ti = ptrtoint ptr %ptarget to i64
  %tbox = or i64 %ti, 5
  %pa0 = getelementptr i64, ptr %pargv, i64 0
  store i64 %tbox, ptr %pa0, align 8
  %ki = ptrtoint ptr %key to i64
  %pa1 = getelementptr i64, ptr %pargv, i64 1
  store i64 %ki, ptr %pa1, align 8
  %hi = ptrtoint ptr %phandler to i64
  %hbox = or i64 %hi, 5
  %tv = call i64 %tfp(ptr %tenv, i64 %hbox, i64 2, ptr %pargv)
  %tb = call i1 @__kml_any_tobool(i64 %tv)
  ret i1 %tb
walkstart:
  br label %walk
walk:
  %cur = phi ptr [ %o, %walkstart ], [ %proto, %next ]
  %own = call i1 @__kml_dynobj_has(ptr %cur, ptr %key)
  br i1 %own, label %yes, label %next
next:
  %protop = getelementptr i8, ptr %cur, i64 8
  %proto = load ptr, ptr %protop, align 8
  %endq = icmp eq ptr %proto, null
  br i1 %endq, label %no, label %walk
yes:
  ret i1 true
no:
  ret i1 false
}

define ptr @__kml_dynobj_get_proto(ptr %o) {
entry:
  %protop = getelementptr i8, ptr %o, i64 8
  %proto = load ptr, ptr %protop, align 8
  ret ptr %proto
}

; set_proto refuses a cycle (returns false), keeping every chain acyclic so
; the get/has walks need no visited set. proto may be null.
define i1 @__kml_dynobj_set_proto(ptr %o, ptr %proto) {
entry:
  br label %scan
scan:
  %cur = phi ptr [ %proto, %entry ], [ %parent, %step ]
  %isnull = icmp eq ptr %cur, null
  br i1 %isnull, label %ok, label %check
check:
  %same = icmp eq ptr %cur, %o
  br i1 %same, label %cycle, label %step
step:
  %pp = getelementptr i8, ptr %cur, i64 8
  %parent = load ptr, ptr %pp, align 8
  br label %scan
ok:
  %slot = getelementptr i8, ptr %o, i64 8
  store ptr %proto, ptr %slot, align 8
  ret i1 true
cycle:
  ret i1 false
}

define i1 @__kml_dynobj_delete(ptr %o, ptr %key) {
entry:
  %pargv = alloca [2 x i64], align 8
  %flags0 = load i64, ptr %o, align 8
  %pb = and i64 %flags0, 8589934592
  %isp = icmp ne i64 %pb, 0
  br i1 %isp, label %proxy, label %own
proxy:
  %ptargetp = getelementptr i8, ptr %o, i64 8
  %ptarget = load ptr, ptr %ptargetp, align 8
  %phandlerp = getelementptr i8, ptr %o, i64 16
  %phandler = load ptr, ptr %phandlerp, align 8
  %trap = call i64 @__kml_dynobj_get(ptr %phandler, ptr @.kml_trap_del)
  %ttag = call i8 @__kml_nb_tag(i64 %trap)
  %isfn = icmp eq i8 %ttag, 12
  br i1 %isfn, label %calltrap, label %pfwd
pfwd:
  %fd = call i1 @__kml_dynobj_delete(ptr %ptarget, ptr %key)
  ret i1 %fd
calltrap:
  %reci = call i64 @__kml_nb_pay(i64 %trap)
  %rec = inttoptr i64 %reci to ptr
  %tfp = load ptr, ptr %rec, align 8
  %tenvp = getelementptr i8, ptr %rec, i64 8
  %tenv = load ptr, ptr %tenvp, align 8
  %ti = ptrtoint ptr %ptarget to i64
  %tbox = or i64 %ti, 5
  %pa0 = getelementptr i64, ptr %pargv, i64 0
  store i64 %tbox, ptr %pa0, align 8
  %ki = ptrtoint ptr %key to i64
  %pa1 = getelementptr i64, ptr %pargv, i64 1
  store i64 %ki, ptr %pa1, align 8
  %hi = ptrtoint ptr %phandler to i64
  %hbox = or i64 %hi, 5
  %tv = call i64 %tfp(ptr %tenv, i64 %hbox, i64 2, ptr %pargv)
  %tb = call i1 @__kml_any_tobool(i64 %tv)
  ret i1 %tb
own:
  %idx = call i64 @__kml_dynobj_find(ptr %o, ptr %key)
  %found = icmp sge i64 %idx, 0
  br i1 %found, label %chk, label %miss
chk:
  ; a non-configurable property refuses deletion (Stage 5) — false, which
  ; the strict-JS caller surfaces as its TypeError.
  %attrs = call i64 @__kml_dynobj_attrs_at(ptr %o, i64 %idx)
  %cb = and i64 %attrs, 4
  %conf = icmp ne i64 %cb, 0
  br i1 %conf, label %del, label %nodel
nodel:
  ret i1 false
del:
  %propsp = getelementptr i8, ptr %o, i64 16
  %props = load ptr, ptr %propsp, align 8
  %countp = getelementptr i8, ptr %o, i64 24
  %count = load i64, ptr %countp, align 8
  %off = mul i64 %idx, 32
  %dst = getelementptr i8, ptr %props, i64 %off
  %nexti = add i64 %idx, 1
  %noff = mul i64 %nexti, 32
  %src = getelementptr i8, ptr %props, i64 %noff
  %tailn = sub i64 %count, %nexti
  %tailbytes = mul i64 %tailn, 32
  %ignore = call ptr @memmove(ptr %dst, ptr %src, i64 %tailbytes)
  %newcount = sub i64 %count, 1
  store i64 %newcount, ptr %countp, align 8
  ret i1 true
miss:
  ; JS: delete of a missing property is still true (false is reserved for a
  ; non-configurable property, which arrives with descriptors in Stage 5).
  ret i1 true
}

define i64 @__kml_dynobj_count(ptr %o) {
entry:
  %countp = getelementptr i8, ptr %o, i64 24
  %count = load i64, ptr %countp, align 8
  ret i64 %count
}

define ptr @__kml_dynobj_key_at(ptr %o, i64 %i) {
entry:
  %propsp = getelementptr i8, ptr %o, i64 16
  %props = load ptr, ptr %propsp, align 8
  %off = mul i64 %i, 32
  %ep = getelementptr i8, ptr %props, i64 %off
  %key = load ptr, ptr %ep, align 8
  ret ptr %key
}

define { ptr, i64 } @__kml_dynobj_keys(ptr %o) {
entry:
  %count = call i64 @__kml_dynobj_count(ptr %o)
  %bytes = mul i64 %count, 8
  %arr = call ptr @malloc(i64 %bytes)
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %body ]
  %done = icmp sge i64 %i, %count
  br i1 %done, label %out, label %body
body:
  %key = call ptr @__kml_dynobj_key_at(ptr %o, i64 %i)
  %slot = getelementptr ptr, ptr %arr, i64 %i
  store ptr %key, ptr %slot, align 8
  %inext = add i64 %i, 1
  br label %loop
out:
  %r0 = insertvalue { ptr, i64 } undef, ptr %arr, 0
  %r1 = insertvalue { ptr, i64 } %r0, i64 %count, 1
  ret { ptr, i64 } %r1
}

; merge backs object spread: own ENUMERABLE properties only, values read
; through the checked get (so a getter is invoked and its result lands as a
; plain data property on the destination — exactly JS spread).
define void @__kml_dynobj_merge(ptr %dst, ptr %src) {
entry:
  %count = call i64 @__kml_dynobj_count(ptr %src)
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %cont ]
  %done = icmp sge i64 %i, %count
  br i1 %done, label %out, label %body
body:
  %attrs = call i64 @__kml_dynobj_attrs_at(ptr %src, i64 %i)
  %eb = and i64 %attrs, 2
  %isenum = icmp ne i64 %eb, 0
  br i1 %isenum, label %copy, label %cont
copy:
  %key = call ptr @__kml_dynobj_key_at(ptr %src, i64 %i)
  %v = call i64 @__kml_dynobj_get(ptr %src, ptr %key)
  call void @__kml_dynobj_set(ptr %dst, ptr %key, i64 %v)
  br label %cont
cont:
  %inext = add i64 %i, 1
  br label %loop
out:
  ret void
}

; keys_enum — own ENUMERABLE string keys, insertion order (Object.keys /
; for...in / JSON). __kml_dynobj_keys below keeps ALL own keys
; (getOwnPropertyNames).
define { ptr, i64 } @__kml_dynobj_keys_enum(ptr %o) {
entry:
  %count = call i64 @__kml_dynobj_count(ptr %o)
  %bytes = mul i64 %count, 8
  %arr = call ptr @malloc(i64 %bytes)
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %cont ]
  %n = phi i64 [ 0, %entry ], [ %nnext, %cont ]
  %done = icmp sge i64 %i, %count
  br i1 %done, label %out, label %body
body:
  %attrs = call i64 @__kml_dynobj_attrs_at(ptr %o, i64 %i)
  %eb = and i64 %attrs, 2
  %isenum = icmp ne i64 %eb, 0
  br i1 %isenum, label %take, label %skip
take:
  %key = call ptr @__kml_dynobj_key_at(ptr %o, i64 %i)
  %slot = getelementptr ptr, ptr %arr, i64 %n
  store ptr %key, ptr %slot, align 8
  br label %cont
skip:
  br label %cont
cont:
  %ninc = phi i64 [ 1, %take ], [ 0, %skip ]
  %nnext = add i64 %n, %ninc
  %inext = add i64 %i, 1
  br label %loop
out:
  %r0 = insertvalue { ptr, i64 } undef, ptr %arr, 0
  %r1 = insertvalue { ptr, i64 } %r0, i64 %n, 1
  ret { ptr, i64 } %r1
}

; defacc installs (or fills in) an accessor property — the object-literal
; get/set path. attrs: ENUMERABLE|CONFIGURABLE|ACCESSOR (accessors have no
; WRITABLE bit). A null getRec/setRec leaves that slot as-is.
define void @__kml_dynobj_defacc(ptr %o, ptr %key, ptr %getRec, ptr %setRec) {
entry:
  %idx = call i64 @__kml_dynobj_find(ptr %o, ptr %key)
  %found = icmp sge i64 %idx, 0
  br i1 %found, label %existing, label %fresh
existing:
  %attrs = call i64 @__kml_dynobj_attrs_at(ptr %o, i64 %idx)
  %accb = and i64 %attrs, 8
  %isacc = icmp ne i64 %accb, 0
  br i1 %isacc, label %fill, label %convert
convert:
  ; a data entry converts to a fresh accessor pair
  %np = call ptr @calloc(i64 1, i64 16)
  %npi = ptrtoint ptr %np to i64
  call void @__kml_dynobj_patch(ptr %o, i64 %idx, i64 0, i64 %npi, i64 14)
  br label %fillp
fill:
  br label %fillp
fillp:
  %pairi = call i64 @__kml_dynobj_rawpay_at(ptr %o, i64 %idx)
  %pair = inttoptr i64 %pairi to ptr
  %hasg = icmp ne ptr %getRec, null
  br i1 %hasg, label %setg, label %ckset
setg:
  store ptr %getRec, ptr %pair, align 8
  br label %ckset
ckset:
  %hass = icmp ne ptr %setRec, null
  br i1 %hass, label %sets, label %done
sets:
  %sp = getelementptr i8, ptr %pair, i64 8
  store ptr %setRec, ptr %sp, align 8
  br label %done
fresh:
  %pairn = call ptr @calloc(i64 1, i64 16)
  store ptr %getRec, ptr %pairn, align 8
  %spn = getelementptr i8, ptr %pairn, i64 8
  store ptr %setRec, ptr %spn, align 8
  ; append via the raw define (undefined placeholder), then patch the entry
  ; into accessor shape
  call void @__kml_dynobj_set(ptr %o, ptr %key, i64 10)
  %nidx = call i64 @__kml_dynobj_find(ptr %o, ptr %key)
  %pi = ptrtoint ptr %pairn to i64
  call void @__kml_dynobj_patch(ptr %o, i64 %nidx, i64 0, i64 %pi, i64 14)
  br label %done
done:
  ret void
}

; patch rewrites one entry's (tag, payload, attrs) in place.
define void @__kml_dynobj_patch(ptr %o, i64 %i, i64 %tag, i64 %pay, i64 %attrs) {
entry:
  %propsp = getelementptr i8, ptr %o, i64 16
  %props = load ptr, ptr %propsp, align 8
  %off = mul i64 %i, 32
  %ep = getelementptr i8, ptr %props, i64 %off
  %tp = getelementptr i8, ptr %ep, i64 8
  store i64 %tag, ptr %tp, align 8
  %pp = getelementptr i8, ptr %ep, i64 16
  store i64 %pay, ptr %pp, align 8
  %ap = getelementptr i8, ptr %ep, i64 24
  store i64 %attrs, ptr %ap, align 8
  ret void
}

; prevent — mode 0: preventExtensions, 1: seal (also clears CONFIGURABLE),
; 2: freeze (also clears WRITABLE on data entries; accessors have none).
define void @__kml_dynobj_prevent(ptr %o, i64 %mode) {
entry:
  %flags = load i64, ptr %o, align 8
  %nf = or i64 %flags, 4294967296
  store i64 %nf, ptr %o, align 8
  %sealq = icmp sge i64 %mode, 1
  br i1 %sealq, label %sweep, label %done
sweep:
  %count = call i64 @__kml_dynobj_count(ptr %o)
  br label %loop
loop:
  %i = phi i64 [ 0, %sweep ], [ %inext, %body ]
  %ldone = icmp sge i64 %i, %count
  br i1 %ldone, label %done, label %body
body:
  %attrs = call i64 @__kml_dynobj_attrs_at(ptr %o, i64 %i)
  %noconf = and i64 %attrs, -5
  %isfreeze = icmp eq i64 %mode, 2
  %nowrite = and i64 %noconf, -2
  %swept = select i1 %isfreeze, i64 %nowrite, i64 %noconf
  %propsp = getelementptr i8, ptr %o, i64 16
  %props = load ptr, ptr %propsp, align 8
  %off = mul i64 %i, 32
  %ep = getelementptr i8, ptr %props, i64 %off
  %ap = getelementptr i8, ptr %ep, i64 24
  store i64 %swept, ptr %ap, align 8
  %inext = add i64 %i, 1
  br label %loop
done:
  ret void
}

; flags_test — mode 0: isExtensible, 1: isSealed, 2: isFrozen.
define i1 @__kml_dynobj_flags_test(ptr %o, i64 %mode) {
entry:
  %flags = load i64, ptr %o, align 8
  %nxb = and i64 %flags, 4294967296
  %nonext = icmp ne i64 %nxb, 0
  %isext = icmp eq i64 %mode, 0
  br i1 %isext, label %extq, label %sealedq
extq:
  %ext = xor i1 %nonext, true
  ret i1 %ext
sealedq:
  br i1 %nonext, label %scan, label %retfalse
scan:
  %count = call i64 @__kml_dynobj_count(ptr %o)
  br label %loop
loop:
  %i = phi i64 [ 0, %scan ], [ %inext, %pass ]
  %ldone = icmp sge i64 %i, %count
  br i1 %ldone, label %rettrue, label %body
body:
  %attrs = call i64 @__kml_dynobj_attrs_at(ptr %o, i64 %i)
  %cb = and i64 %attrs, 4
  %conf = icmp ne i64 %cb, 0
  br i1 %conf, label %retfalse, label %ckfrozen
ckfrozen:
  %isfrz = icmp eq i64 %mode, 2
  br i1 %isfrz, label %frzchk, label %pass
frzchk:
  %accb = and i64 %attrs, 8
  %isacc = icmp ne i64 %accb, 0
  br i1 %isacc, label %pass, label %wchk
wchk:
  %wb = and i64 %attrs, 1
  %wr = icmp ne i64 %wb, 0
  br i1 %wr, label %retfalse, label %pass
pass:
  %inext = add i64 %i, 1
  br label %loop
rettrue:
  ret i1 true
retfalse:
  ret i1 false
}

define i64 @__kml_dynobj_get_at(ptr %o, i64 %i) {
entry:
  %propsp = getelementptr i8, ptr %o, i64 16
  %props = load ptr, ptr %propsp, align 8
  %off = mul i64 %i, 32
  %ep = getelementptr i8, ptr %props, i64 %off
  %tagp = getelementptr i8, ptr %ep, i64 8
  %tag = load i64, ptr %tagp, align 8
  %tag8 = trunc i64 %tag to i8
  %payp = getelementptr i8, ptr %ep, i64 16
  %pay = load i64, ptr %payp, align 8
  %r = call i64 @__kml_nb_pack(i8 %tag8, i64 %pay)
  ret i64 %r
}`)
}
