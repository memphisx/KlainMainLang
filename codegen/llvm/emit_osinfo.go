// emit_osinfo.go — the osinfo.c-backed half of the os module (type/release/
// version/machine/uptime/loadavg/userInfo/availableParallelism/
// networkInterfaces, plus os.arch/endianness/devNull constants) and the bare
// `process.env` value (enumeration through Object.keys/for…in/spread).
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
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

// emitOSNetworkInterfaces implements os.networkInterfaces(): a dynamic object
// keyed by interface name (the keys exist only at run time), each value an
// array of `{address, netmask, family, mac, internal, cidr, scopeid?}` records
// in libuv's order — `scopeid` present on IPv6 entries only, as in Node.
func (e *Emitter) emitOSNetworkInterfaces(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: os.networkInterfaces() takes no arguments", pos.Line, pos.Col)
	}
	e.ensureOSInfoBags()
	e.ensureSprintf()
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_os_netifs_bag()", bag))
	return Value{Ref: e.emitNbTagPtr(bag, kmlTagDynObject), Ty: TypeAny}, nil
}

// emitOSInfoString implements the four uname-shaped string getters
// (os.type/release/version/machine): the sidecar's C string wrapped into a
// header string.
func (e *Emitter) emitOSInfoString(name string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: os.%s() takes no arguments", pos.Line, pos.Col, name)
	}
	e.ensureOSInfo()
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_os_%s()", raw, name))
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", out, raw))
	return Value{Ref: out, Ty: TypePtr}, nil
}

// emitOSUptime implements os.uptime(): seconds since boot as a double
// (CLOCK_BOOTTIME / kern.boottime / GetTickCount64, as libuv).
func (e *Emitter) emitOSUptime(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: os.uptime() takes no arguments", pos.Line, pos.Col)
	}
	e.ensureOSInfo()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @__kml_os_uptime()", r))
	return Value{Ref: r, Ty: TypeF64}, nil
}

// emitOSLoadavg implements os.loadavg(): the 1/5/15-minute averages as a
// three-element number[]; Windows has none and reports [0, 0, 0] like Node.
func (e *Emitter) emitOSLoadavg(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: os.loadavg() takes no arguments", pos.Line, pos.Col)
	}
	e.ensureOSInfo()
	e.ensureMalloc()
	data := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 24)", data))
	e.emitInstr(fmt.Sprintf("call void @__kml_os_loadavg(ptr %s)", data))
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, data))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 3, 1", r1, r0))
	return Value{Ref: r1, Ty: ArrayOf(TypeF64)}, nil
}

// emitOSAvailableParallelism implements os.availableParallelism(): the CPUs
// this process may run on (affinity-aware on Linux), at least 1.
func (e *Emitter) emitOSAvailableParallelism(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: os.availableParallelism() takes no arguments", pos.Line, pos.Col)
	}
	e.ensureOSInfo()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_os_avail_parallelism()", r))
	return Value{Ref: r, Ty: TypeI64}, nil
}

// userInfoType is the `{ uid, gid, username, homedir, shell }` object
// os.userInfo() returns; `shell` is `string | null` (null on Windows).
func userInfoType() Type {
	return ObjectType([]Field{
		{Name: "uid", Ty: TypeI64},
		{Name: "gid", Ty: TypeI64},
		{Name: "username", Ty: TypePtr},
		{Name: "homedir", Ty: TypePtr},
		{Name: "shell", Ty: nullablePtr()},
	})
}

// emitOSUserInfo implements os.userInfo(): the effective user's passwd entry
// on POSIX; on Windows uid/gid are -1 and shell is null, as Node reports.
// Throws a catchable SystemError-shaped Error when the lookup fails (Node's
// ERR_SYSTEM_ERROR from uv_os_get_passwd).
func (e *Emitter) emitOSUserInfo(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: os.userInfo() takes no arguments", pos.Line, pos.Col)
	}
	e.ensureOSInfo()
	e.ensureCalloc()
	uidSlot := e.freshReg()
	gidSlot := e.freshReg()
	nameSlot := e.freshReg()
	homeSlot := e.freshReg()
	shellSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", uidSlot))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", gidSlot))
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", nameSlot))
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", homeSlot))
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", shellSlot))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", shellSlot))
	rc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_os_userinfo(ptr %s, ptr %s, ptr %s, ptr %s, ptr %s)", rc, uidSlot, gidSlot, nameSlot, homeSlot, shellSlot))
	failed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", failed, rc))
	failL := e.freshLabel("os.userinfo.fail")
	okL := e.freshLabel("os.userinfo.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", failed, failL, okL))
	e.emitLabel(failL)
	e.emitInternalThrow(e.internString("A system error occurred: uv_os_get_passwd returned ENOENT (no such file or directory)"))
	e.emitLabel(okL)

	ty := userInfoType()
	obj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", obj, ty.StructSize()))
	structIR := ty.StructIR()
	storeField := func(name, ir, slot string, wrap bool) {
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", v, ir, slot))
		if wrap {
			w := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", w, v))
			v = w
		}
		idx, _, _ := ty.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, obj, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", ir, v, gep))
	}
	storeField("uid", "i64", uidSlot, false)
	storeField("gid", "i64", gidSlot, false)
	storeField("username", "ptr", nameSlot, true)
	storeField("homedir", "ptr", homeSlot, true)
	storeField("shell", "ptr", shellSlot, true) // from_cstr keeps null as null
	return Value{Ref: obj, Ty: ty}, nil
}

// osInfoCallType is the inferExprType mirror of the emit sites above.
func osInfoCallType(name string) (Type, bool) {
	switch name {
	case "type", "release", "version", "machine", "arch", "endianness":
		return TypePtr, true
	case "uptime":
		return TypeF64, true
	case "loadavg":
		return ArrayOf(TypeF64), true
	case "availableParallelism":
		return TypeI64, true
	case "userInfo":
		return userInfoType(), true
	case "networkInterfaces":
		return TypeAny, true
	}
	return Type{}, false
}

// emitOSCall dispatches the osinfo-backed os.* calls plus the compile-time
// constants os.arch()/os.endianness() (host-targeted, like process.arch).
func (e *Emitter) emitOSCall(name string, args []ast.Expression, pos ast.Pos) (Value, bool, error) {
	switch name {
	case "type", "release", "version", "machine":
		v, err := e.emitOSInfoString(name, args, pos)
		return v, true, err
	case "arch":
		if len(args) != 0 {
			return Value{}, true, fmt.Errorf("%d:%d: os.arch() takes no arguments", pos.Line, pos.Col)
		}
		return Value{Ref: e.internString(nodeArchName()), Ty: TypePtr}, true, nil
	case "endianness":
		if len(args) != 0 {
			return Value{}, true, fmt.Errorf("%d:%d: os.endianness() takes no arguments", pos.Line, pos.Col)
		}
		// Every host this compiler targets (x86-64, arm64, …) is little-endian;
		// s390x/ppc64 (big-endian) are the only Go targets that differ.
		end := "LE"
		if arch := targetGOARCH(); arch == "s390x" || arch == "ppc64" || arch == "mips" || arch == "mips64" {
			end = "BE"
		}
		return Value{Ref: e.internString(end), Ty: TypePtr}, true, nil
	case "uptime":
		v, err := e.emitOSUptime(args, pos)
		return v, true, err
	case "loadavg":
		v, err := e.emitOSLoadavg(args, pos)
		return v, true, err
	case "availableParallelism":
		v, err := e.emitOSAvailableParallelism(args, pos)
		return v, true, err
	case "userInfo":
		v, err := e.emitOSUserInfo(args, pos)
		return v, true, err
	case "networkInterfaces":
		v, err := e.emitOSNetworkInterfaces(args, pos)
		return v, true, err
	}
	return Value{}, false, nil
}

// osDevNull is os.devNull for the build host.
func osDevNull() string {
	if targetGOOS() == "windows" {
		return `\\.\nul`
	}
	return "/dev/null"
}
