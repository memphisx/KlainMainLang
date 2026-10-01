// emit_osinfo.go — the osinfo.c-backed half of the os module (type/release/
// version/machine/uptime/loadavg/userInfo/availableParallelism/
// networkInterfaces, plus os.arch/endianness/devNull constants) and the bare
// `process.env` value (enumeration through Object.keys/for…in/spread).
package llvm

import (
	"fmt"
)

// ensureOSInfoBags emits the two IR builders that turn the sidecar's flat C
// data into D1 dynamic objects: @__kml_process_env_bag (KEY → string) and
// @__kml_os_netifs_bag (name → array of address records). Both are dynamic
// bags because their keys exist only at run time.
func (e *Emitter) ensureOSInfoBags() {
	if e.usedOSInfoBags {
		return
	}
	e.usedOSInfoBags = true
	e.ensureOSInfo()
	e.ensureDynObj()
	e.ensureDynArr()
	e.ensureStrchr()
	e.ensureMalloc()
	e.ensureMemcpy()
	e.emitGlobal(`@.kml_osi_address = private unnamed_addr constant [8 x i8] c"address\00"
@.kml_osi_netmask = private unnamed_addr constant [8 x i8] c"netmask\00"
@.kml_osi_family = private unnamed_addr constant [7 x i8] c"family\00"
@.kml_osi_mac = private unnamed_addr constant [4 x i8] c"mac\00"
@.kml_osi_internal = private unnamed_addr constant [9 x i8] c"internal\00"
@.kml_osi_cidr = private unnamed_addr constant [5 x i8] c"cidr\00"
@.kml_osi_scopeid = private unnamed_addr constant [8 x i8] c"scopeid\00"
@.kml_osi_ipv4 = private unnamed_addr constant [5 x i8] c"IPv4\00"
@.kml_osi_ipv6 = private unnamed_addr constant [5 x i8] c"IPv6\00"
@.kml_osi_slash = private unnamed_addr constant [6 x i8] c"%s/%d\00"

; process.env as a value: one bag entry per "KEY=VALUE" of the live block
; (a snapshot — Node's object is live, see ADR-01077).
define ptr @__kml_process_env_bag() {
entry:
  %bag = call ptr @__kml_dynobj_new()
  %arr = call ptr @__kml_env_entries()
  %isnull = icmp eq ptr %arr, null
  br i1 %isnull, label %done, label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %next ]
  %slot = getelementptr ptr, ptr %arr, i64 %i
  %ent = load ptr, ptr %slot, align 8
  %end = icmp eq ptr %ent, null
  br i1 %end, label %done, label %body
body:
  %eq = call ptr @strchr(ptr %ent, i32 61)
  %noeq = icmp eq ptr %eq, null
  br i1 %noeq, label %next, label %split
split:
  %eqi = ptrtoint ptr %eq to i64
  %enti = ptrtoint ptr %ent to i64
  %klen = sub i64 %eqi, %enti
  %kempty = icmp eq i64 %klen, 0
  br i1 %kempty, label %next, label %store
store:
  %klen1 = add i64 %klen, 1
  %key = call ptr @malloc(i64 %klen1)
  call ptr @memcpy(ptr %key, ptr %ent, i64 %klen)
  %kend = getelementptr i8, ptr %key, i64 %klen
  store i8 0, ptr %kend, align 1
  %vraw = getelementptr i8, ptr %eq, i64 1
  %v = call ptr @__kml_str_from_cstr(ptr %vraw)
  %vbox = ptrtoint ptr %v to i64
  call void @__kml_dynobj_set(ptr %bag, ptr %key, i64 %vbox)
  br label %next
next:
  %inext = add i64 %i, 1
  br label %loop
done:
  ret ptr %bag
}

; os.networkInterfaces(): { name: [ {address, netmask, family, mac, internal,
; cidr, scopeid?}, … ] } — the kml_ifaddr record is 8 × 8 bytes:
; name, address, netmask, family, mac, internal, scopeid, prefix.
define ptr @__kml_os_netifs_bag() {
entry:
  %cnt = alloca i64, align 8
  store i64 0, ptr %cnt, align 8
  %bag = call ptr @__kml_dynobj_new()
  %arr = call ptr @__kml_os_netifs(ptr %cnt)
  %n = load i64, ptr %cnt, align 8
  %isnull = icmp eq ptr %arr, null
  br i1 %isnull, label %done, label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %next ]
  %more = icmp slt i64 %i, %n
  br i1 %more, label %body, label %done
body:
  %off = mul i64 %i, 64
  %rec = getelementptr i8, ptr %arr, i64 %off
  %name = load ptr, ptr %rec, align 8
  %addrp = getelementptr i8, ptr %rec, i64 8
  %addr = load ptr, ptr %addrp, align 8
  %maskp = getelementptr i8, ptr %rec, i64 16
  %mask = load ptr, ptr %maskp, align 8
  %famp = getelementptr i8, ptr %rec, i64 24
  %fam = load i64, ptr %famp, align 8
  %macp = getelementptr i8, ptr %rec, i64 32
  %mac = load ptr, ptr %macp, align 8
  %intp = getelementptr i8, ptr %rec, i64 40
  %int = load i64, ptr %intp, align 8
  %scopep = getelementptr i8, ptr %rec, i64 48
  %scope = load i64, ptr %scopep, align 8
  %prefp = getelementptr i8, ptr %rec, i64 56
  %pref = load i64, ptr %prefp, align 8
  ; the per-name list, created on first sight (insertion order = Node's)
  %have = call i64 @__kml_dynobj_get(ptr %bag, ptr %name)
  %absent = icmp eq i64 %have, 10
  br i1 %absent, label %newlist, label %getlist
newlist:
  %fresh = call ptr @__kml_dynarr_new(i64 2)
  %freshbox0 = ptrtoint ptr %fresh to i64
  %freshbox = or i64 %freshbox0, 6
  call void @__kml_dynobj_set(ptr %bag, ptr %name, i64 %freshbox)
  br label %getlist
getlist:
  %listbox = call i64 @__kml_dynobj_get(ptr %bag, ptr %name)
  %listpay = call i64 @__kml_nb_pay(i64 %listbox)
  %list = inttoptr i64 %listpay to ptr
  %o = call ptr @__kml_dynobj_new()
  %addrs = call ptr @__kml_str_from_cstr(ptr %addr)
  %addrb = ptrtoint ptr %addrs to i64
  call void @__kml_dynobj_set(ptr %o, ptr @.kml_osi_address, i64 %addrb)
  %masks = call ptr @__kml_str_from_cstr(ptr %mask)
  %maskb = ptrtoint ptr %masks to i64
  call void @__kml_dynobj_set(ptr %o, ptr @.kml_osi_netmask, i64 %maskb)
  %is4 = icmp eq i64 %fam, 4
  %famc = select i1 %is4, ptr @.kml_osi_ipv4, ptr @.kml_osi_ipv6
  %fams = call ptr @__kml_str_from_cstr(ptr %famc)
  %famb = ptrtoint ptr %fams to i64
  call void @__kml_dynobj_set(ptr %o, ptr @.kml_osi_family, i64 %famb)
  %macs = call ptr @__kml_str_from_cstr(ptr %mac)
  %macb = ptrtoint ptr %macs to i64
  call void @__kml_dynobj_set(ptr %o, ptr @.kml_osi_mac, i64 %macb)
  %isint = icmp ne i64 %int, 0
  %intb = select i1 %isint, i64 7, i64 6
  call void @__kml_dynobj_set(ptr %o, ptr @.kml_osi_internal, i64 %intb)
  %nocidr = icmp slt i64 %pref, 0
  br i1 %nocidr, label %cidrnull, label %cidrfmt
cidrfmt:
  %cbuf = call ptr @__kml_str_alloc(i64 80)
  %pref32 = trunc i64 %pref to i32
  call i32 (ptr, ptr, ...) @sprintf(ptr %cbuf, ptr @.kml_osi_slash, ptr %addr, i32 %pref32)
  call void @__kml_str_finalize(ptr %cbuf)
  %cidrb = ptrtoint ptr %cbuf to i64
  br label %cidrset
cidrnull:
  br label %cidrset
cidrset:
  %cidrv = phi i64 [ %cidrb, %cidrfmt ], [ 2, %cidrnull ]
  call void @__kml_dynobj_set(ptr %o, ptr @.kml_osi_cidr, i64 %cidrv)
  br i1 %is4, label %push, label %scopeid
scopeid:
  %scopeb = call i64 @__kml_nb_pack(i8 0, i64 %scope)
  call void @__kml_dynobj_set(ptr %o, ptr @.kml_osi_scopeid, i64 %scopeb)
  br label %push
push:
  %ob0 = ptrtoint ptr %o to i64
  %ob = or i64 %ob0, 5
  call void @__kml_dynarr_push(ptr %list, i64 %ob)
  br label %next
next:
  %inext = add i64 %i, 1
  br label %loop
done:
  ret ptr %bag
}`)
}

// emitProcessEnvValue implements the bare `process.env` value (not a keyed
// read): a dynamic object holding every variable of the live environment
// block, so Object.keys / for…in / spread / JSON.stringify all work through
// the D1 paths. The bag is a snapshot taken at this expression's evaluation
// (ADR-01077): keyed reads and writes (`process.env.X`) stay live.
func (e *Emitter) emitProcessEnvValue() (Value, error) {
	e.ensureOSInfoBags()
	e.ensureSprintf()
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_process_env_bag()", bag))
	return Value{Ref: e.emitNbTagPtr(bag, kmlTagDynObject), Ty: TypeAny}, nil
}
