// runtime_diagch.go — diagnostics_channel's pub/sub core: a name-keyed
// channel registry, per-channel subscriber lists (closure header + declared
// arity, so publish can call 0/1/2-parameter subscribers correctly), and
// pointer-identity unsubscribe. Channel struct layout (all 8-byte slots):
// {subsRoot@0, subsN@8, subsCap@16, aritiesRoot@24, name@32}.
package llvm

func (e *Emitter) ensureDiagChRuntime() {
	if e.usedDiagChRuntime {
		return
	}
	e.usedDiagChRuntime = true
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureCalloc()
	e.ensureStrcmp()

	// Registry: parallel name/channel arrays (few channels; linear scan).
	e.emitGlobal(`@__kml_dc_names = internal global ptr null, align 8`)
	e.emitGlobal(`@__kml_dc_chans = internal global ptr null, align 8`)
	e.emitGlobal(`@__kml_dc_n = internal global i64 0, align 8`)
	e.emitGlobal(`@__kml_dc_cap = internal global i64 0, align 8`)

	e.emitGlobal(`
define ptr @__kml_dc_channel(ptr %name) {
entry:
  %n = load i64, ptr @__kml_dc_n, align 8
  %names = load ptr, ptr @__kml_dc_names, align 8
  br label %scan
scan:
  %i = phi i64 [ 0, %entry ], [ %inext, %cont ]
  %done = icmp sge i64 %i, %n
  br i1 %done, label %create, label %chk
chk:
  %ns = getelementptr ptr, ptr %names, i64 %i
  %np = load ptr, ptr %ns, align 8
  %c = call i32 @strcmp(ptr %np, ptr %name)
  %eq = icmp eq i32 %c, 0
  br i1 %eq, label %hit, label %cont
hit:
  %cs = load ptr, ptr @__kml_dc_chans, align 8
  %chs = getelementptr ptr, ptr %cs, i64 %i
  %ch0 = load ptr, ptr %chs, align 8
  ret ptr %ch0
cont:
  %inext = add i64 %i, 1
  br label %scan
create:
  %cap = load i64, ptr @__kml_dc_cap, align 8
  %full = icmp sge i64 %n, %cap
  br i1 %full, label %grow, label %store
grow:
  %cap2 = mul i64 %cap, 2
  %ge4 = icmp sgt i64 %cap2, 4
  %newcap = select i1 %ge4, i64 %cap2, i64 4
  %bytes = mul i64 %newcap, 8
  %oldn = load ptr, ptr @__kml_dc_names, align 8
  %newn = call ptr @realloc(ptr %oldn, i64 %bytes)
  store ptr %newn, ptr @__kml_dc_names, align 8
  %oldc = load ptr, ptr @__kml_dc_chans, align 8
  %newc = call ptr @realloc(ptr %oldc, i64 %bytes)
  store ptr %newc, ptr @__kml_dc_chans, align 8
  store i64 %newcap, ptr @__kml_dc_cap, align 8
  br label %store
store:
  %ch = call ptr @calloc(i64 1, i64 40)
  %namef = getelementptr i8, ptr %ch, i64 32
  store ptr %name, ptr %namef, align 8
  %names2 = load ptr, ptr @__kml_dc_names, align 8
  %ns2 = getelementptr ptr, ptr %names2, i64 %n
  store ptr %name, ptr %ns2, align 8
  %chans2 = load ptr, ptr @__kml_dc_chans, align 8
  %cs2 = getelementptr ptr, ptr %chans2, i64 %n
  store ptr %ch, ptr %cs2, align 8
  %n2 = add i64 %n, 1
  store i64 %n2, ptr @__kml_dc_n, align 8
  ret ptr %ch
}

define void @__kml_dc_subscribe(ptr %ch, ptr %closure, i64 %arity) {
entry:
  %np = getelementptr i8, ptr %ch, i64 8
  %n = load i64, ptr %np, align 8
  %capp = getelementptr i8, ptr %ch, i64 16
  %cap = load i64, ptr %capp, align 8
  %full = icmp sge i64 %n, %cap
  br i1 %full, label %grow, label %store
grow:
  %cap2 = mul i64 %cap, 2
  %ge4 = icmp sgt i64 %cap2, 4
  %newcap = select i1 %ge4, i64 %cap2, i64 4
  %bytes = mul i64 %newcap, 8
  %rootp = getelementptr i8, ptr %ch, i64 0
  %old = load ptr, ptr %rootp, align 8
  %new = call ptr @realloc(ptr %old, i64 %bytes)
  store ptr %new, ptr %rootp, align 8
  %arootp = getelementptr i8, ptr %ch, i64 24
  %aold = load ptr, ptr %arootp, align 8
  %anew = call ptr @realloc(ptr %aold, i64 %bytes)
  store ptr %anew, ptr %arootp, align 8
  store i64 %newcap, ptr %capp, align 8
  br label %store
store:
  %rootp2 = getelementptr i8, ptr %ch, i64 0
  %data = load ptr, ptr %rootp2, align 8
  %slot = getelementptr ptr, ptr %data, i64 %n
  store ptr %closure, ptr %slot, align 8
  %arootp2 = getelementptr i8, ptr %ch, i64 24
  %adata = load ptr, ptr %arootp2, align 8
  %aslot = getelementptr i64, ptr %adata, i64 %n
  store i64 %arity, ptr %aslot, align 8
  %n2 = add i64 %n, 1
  store i64 %n2, ptr %np, align 8
  ret void
}

; remove by closure-pointer identity (compact by moving the tail entry down).
define i1 @__kml_dc_unsubscribe(ptr %ch, ptr %closure) {
entry:
  %np = getelementptr i8, ptr %ch, i64 8
  %n = load i64, ptr %np, align 8
  %rootp = getelementptr i8, ptr %ch, i64 0
  %data = load ptr, ptr %rootp, align 8
  %arootp = getelementptr i8, ptr %ch, i64 24
  %adata = load ptr, ptr %arootp, align 8
  br label %scan
scan:
  %i = phi i64 [ 0, %entry ], [ %inext, %cont ]
  %done = icmp sge i64 %i, %n
  br i1 %done, label %miss, label %chk
chk:
  %slot = getelementptr ptr, ptr %data, i64 %i
  %c = load ptr, ptr %slot, align 8
  %eq = icmp eq ptr %c, %closure
  br i1 %eq, label %hit, label %cont
cont:
  %inext = add i64 %i, 1
  br label %scan
hit:
  %last = sub i64 %n, 1
  %lslot = getelementptr ptr, ptr %data, i64 %last
  %lv = load ptr, ptr %lslot, align 8
  store ptr %lv, ptr %slot, align 8
  %laslot = getelementptr i64, ptr %adata, i64 %last
  %lav = load i64, ptr %laslot, align 8
  %aslot = getelementptr i64, ptr %adata, i64 %i
  store i64 %lav, ptr %aslot, align 8
  store i64 %last, ptr %np, align 8
  ret i1 true
miss:
  ret i1 false
}

define i1 @__kml_dc_has_subscribers(ptr %ch) {
entry:
  %np = getelementptr i8, ptr %ch, i64 8
  %n = load i64, ptr %np, align 8
  %has = icmp sgt i64 %n, 0
  ret i1 %has
}

; publish: call each subscriber with (message[, name]) per its declared arity.
define void @__kml_dc_publish(ptr %ch, ptr %msg) {
entry:
  %np = getelementptr i8, ptr %ch, i64 8
  %n = load i64, ptr %np, align 8
  %rootp = getelementptr i8, ptr %ch, i64 0
  %data = load ptr, ptr %rootp, align 8
  %arootp = getelementptr i8, ptr %ch, i64 24
  %adata = load ptr, ptr %arootp, align 8
  %namef = getelementptr i8, ptr %ch, i64 32
  %name = load ptr, ptr %namef, align 8
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %cont ]
  %done = icmp sge i64 %i, %n
  br i1 %done, label %ret, label %body
body:
  %slot = getelementptr ptr, ptr %data, i64 %i
  %c = load ptr, ptr %slot, align 8
  %aslot = getelementptr i64, ptr %adata, i64 %i
  %ar = load i64, ptr %aslot, align 8
  %fpp = getelementptr { ptr, ptr }, ptr %c, i32 0, i32 0
  %fp = load ptr, ptr %fpp, align 8
  %epp = getelementptr { ptr, ptr }, ptr %c, i32 0, i32 1
  %ep = load ptr, ptr %epp, align 8
  %is2 = icmp eq i64 %ar, 2
  br i1 %is2, label %call2, label %chk1
call2:
  call void %fp(ptr %ep, ptr %msg, ptr %name)
  br label %cont
chk1:
  %is1 = icmp eq i64 %ar, 1
  br i1 %is1, label %call1, label %call0
call1:
  call void %fp(ptr %ep, ptr %msg)
  br label %cont
call0:
  call void %fp(ptr %ep)
  br label %cont
cont:
  %inext = add i64 %i, 1
  br label %loop
ret:
  ret void
}`)
}
