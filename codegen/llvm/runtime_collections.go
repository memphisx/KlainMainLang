package llvm

import ()

// ensureGroupMapHelpers backs Object.groupBy's runtime map (emit_objects.go).
// Unrelated to the "group" tracking used by the Promise combinators
// (Promise.all/race/allSettled's pending-group wait bookkeeping in
// runtime_fetch.go's ensurePromiseCombinators/runtime_http.go's
// ensureHTTPRuntime) — both independently use the English word "group" for
// two different things; don't conflate them when reading across files.
func (e *Emitter) ensureGroupMapHelpers() {
	if e.usedGroupMapHelpers {
		return
	}
	e.usedGroupMapHelpers = true
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureStrcmp()
	e.ensureMemcpy()
	// Group-map header layout (48 bytes):
	//   +0  i64 count  — number of distinct keys
	//   +8  i64 cap    — capacity of key/bucket arrays
	//   +16 ptr keys   — char** (key strings)
	//   +24 ptr bptrs  — ptr* (bucket data arrays, each is i64*)
	//   +32 ptr lens   — i64* (element count per bucket)
	//   +40 ptr caps   — i64* (capacity per bucket)
	e.emitGlobal(`
define ptr @__kml_gmap_create() {
entry:
  %h = call ptr @malloc(i64 48)
  store i64 0, ptr %h, align 8
  %cap_p = getelementptr i8, ptr %h, i64 8
  store i64 8, ptr %cap_p, align 8
  %keys = call ptr @malloc(i64 64)
  %keys_p = getelementptr i8, ptr %h, i64 16
  store ptr %keys, ptr %keys_p, align 8
  %bptrs = call ptr @malloc(i64 64)
  %bptrs_p = getelementptr i8, ptr %h, i64 24
  store ptr %bptrs, ptr %bptrs_p, align 8
  %lens = call ptr @malloc(i64 64)
  %lens_p = getelementptr i8, ptr %h, i64 32
  store ptr %lens, ptr %lens_p, align 8
  %caps = call ptr @malloc(i64 64)
  %caps_p = getelementptr i8, ptr %h, i64 40
  store ptr %caps, ptr %caps_p, align 8
  ret ptr %h
}

define i64 @__kml_gmap_find_or_add(ptr %map, ptr %key) {
entry:
  %count = load i64, ptr %map, align 8
  %cap_p = getelementptr i8, ptr %map, i64 8
  %cap = load i64, ptr %cap_p, align 8
  %keys_p = getelementptr i8, ptr %map, i64 16
  %keys = load ptr, ptr %keys_p, align 8
  br label %scan
scan:
  %i = phi i64 [ 0, %entry ], [ %i_next, %scan_cont ]
  %scan_done = icmp sge i64 %i, %count
  br i1 %scan_done, label %add_key, label %scan_chk
scan_chk:
  %kslot = getelementptr ptr, ptr %keys, i64 %i
  %kptr = load ptr, ptr %kslot, align 8
  %cmp = call i32 @strcmp(ptr %kptr, ptr %key)
  %eq = icmp eq i32 %cmp, 0
  br i1 %eq, label %found, label %scan_cont
found:
  ret i64 %i
scan_cont:
  %i_next = add i64 %i, 1
  br label %scan
add_key:
  %need_grow = icmp sge i64 %count, %cap
  br i1 %need_grow, label %grow, label %do_add
grow:
  %new_cap = mul i64 %cap, 2
  %new_bytes = mul i64 %new_cap, 8
  %old_keys = load ptr, ptr %keys_p, align 8
  %nkeys = call ptr @realloc(ptr %old_keys, i64 %new_bytes)
  store ptr %nkeys, ptr %keys_p, align 8
  %bptrs_p1 = getelementptr i8, ptr %map, i64 24
  %old_bptrs = load ptr, ptr %bptrs_p1, align 8
  %nbptrs = call ptr @realloc(ptr %old_bptrs, i64 %new_bytes)
  store ptr %nbptrs, ptr %bptrs_p1, align 8
  %lens_p1 = getelementptr i8, ptr %map, i64 32
  %old_lens = load ptr, ptr %lens_p1, align 8
  %nlens = call ptr @realloc(ptr %old_lens, i64 %new_bytes)
  store ptr %nlens, ptr %lens_p1, align 8
  %caps_p1 = getelementptr i8, ptr %map, i64 40
  %old_caps = load ptr, ptr %caps_p1, align 8
  %ncaps = call ptr @realloc(ptr %old_caps, i64 %new_bytes)
  store ptr %ncaps, ptr %caps_p1, align 8
  store i64 %new_cap, ptr %cap_p, align 8
  br label %do_add
do_add:
  %keys2 = load ptr, ptr %keys_p, align 8
  %bptrs_p2 = getelementptr i8, ptr %map, i64 24
  %bptrs2 = load ptr, ptr %bptrs_p2, align 8
  %lens_p2 = getelementptr i8, ptr %map, i64 32
  %lens2 = load ptr, ptr %lens_p2, align 8
  %caps_p2 = getelementptr i8, ptr %map, i64 40
  %caps2 = load ptr, ptr %caps_p2, align 8
  %kslot2 = getelementptr ptr, ptr %keys2, i64 %count
  store ptr %key, ptr %kslot2, align 8
  %bdata = call ptr @malloc(i64 64)
  %bslot = getelementptr ptr, ptr %bptrs2, i64 %count
  store ptr %bdata, ptr %bslot, align 8
  %lslot = getelementptr i64, ptr %lens2, i64 %count
  store i64 0, ptr %lslot, align 8
  %cslot = getelementptr i64, ptr %caps2, i64 %count
  store i64 8, ptr %cslot, align 8
  %count1 = add i64 %count, 1
  store i64 %count1, ptr %map, align 8
  ret i64 %count
}

define void @__kml_gmap_append(ptr %map, i64 %idx, i64 %val) {
entry:
  %bptrs_p = getelementptr i8, ptr %map, i64 24
  %bptrs = load ptr, ptr %bptrs_p, align 8
  %lens_p = getelementptr i8, ptr %map, i64 32
  %lens = load ptr, ptr %lens_p, align 8
  %caps_p = getelementptr i8, ptr %map, i64 40
  %caps = load ptr, ptr %caps_p, align 8
  %lslot = getelementptr i64, ptr %lens, i64 %idx
  %len = load i64, ptr %lslot, align 8
  %cslot = getelementptr i64, ptr %caps, i64 %idx
  %cap = load i64, ptr %cslot, align 8
  %bslot = getelementptr ptr, ptr %bptrs, i64 %idx
  %bdata = load ptr, ptr %bslot, align 8
  %need_grow = icmp sge i64 %len, %cap
  br i1 %need_grow, label %grow, label %do_append
grow:
  %new_cap = mul i64 %cap, 2
  %new_bytes = mul i64 %new_cap, 8
  %new_bdata = call ptr @realloc(ptr %bdata, i64 %new_bytes)
  store ptr %new_bdata, ptr %bslot, align 8
  store i64 %new_cap, ptr %cslot, align 8
  br label %do_append
do_append:
  %bdata2 = load ptr, ptr %bslot, align 8
  %vslot = getelementptr i64, ptr %bdata2, i64 %len
  store i64 %val, ptr %vslot, align 8
  %len1 = add i64 %len, 1
  store i64 %len1, ptr %lslot, align 8
  ret void
}

define {ptr, i64} @__kml_gmap_get(ptr %map, ptr %key) {
entry:
  %count = load i64, ptr %map, align 8
  %keys_p = getelementptr i8, ptr %map, i64 16
  %keys = load ptr, ptr %keys_p, align 8
  br label %scan
scan:
  %i = phi i64 [ 0, %entry ], [ %i_next, %cont ]
  %done = icmp sge i64 %i, %count
  br i1 %done, label %not_found, label %chk
chk:
  %kslot = getelementptr ptr, ptr %keys, i64 %i
  %kptr = load ptr, ptr %kslot, align 8
  %cmp = call i32 @strcmp(ptr %kptr, ptr %key)
  %eq = icmp eq i32 %cmp, 0
  br i1 %eq, label %found, label %cont
found:
  %bptrs_p = getelementptr i8, ptr %map, i64 24
  %bptrs = load ptr, ptr %bptrs_p, align 8
  %bslot = getelementptr ptr, ptr %bptrs, i64 %i
  %bdata = load ptr, ptr %bslot, align 8
  %lens_p = getelementptr i8, ptr %map, i64 32
  %lens = load ptr, ptr %lens_p, align 8
  %lslot = getelementptr i64, ptr %lens, i64 %i
  %blen = load i64, ptr %lslot, align 8
  %r0 = insertvalue {ptr, i64} undef, ptr %bdata, 0
  %r1 = insertvalue {ptr, i64} %r0, i64 %blen, 1
  ret {ptr, i64} %r1
cont:
  %i_next = add i64 %i, 1
  br label %scan
not_found:
  %e0 = insertvalue {ptr, i64} undef, ptr null, 0
  %e1 = insertvalue {ptr, i64} %e0, i64 0, 1
  ret {ptr, i64} %e1
}

define {ptr, i64} @__kml_gmap_keys(ptr %map) {
entry:
  %count = load i64, ptr %map, align 8
  %keys_p = getelementptr i8, ptr %map, i64 16
  %keys = load ptr, ptr %keys_p, align 8
  %bytes = mul i64 %count, 8
  %arr = call ptr @malloc(i64 %bytes)
  call ptr @memcpy(ptr %arr, ptr %keys, i64 %bytes)
  %r0 = insertvalue {ptr, i64} undef, ptr %arr, 0
  %r1 = insertvalue {ptr, i64} %r0, i64 %count, 1
  ret {ptr, i64} %r1
}`)
}

// --- Sort helpers ---

func (e *Emitter) ensureSortClosGlobal() {
	if !e.usedSortClosGlobal {
		e.emitGlobal("@__kml_sort_clos = thread_local global ptr null")
		e.usedSortClosGlobal = true
	}
}

func (e *Emitter) ensureSortCmpI64() {
	if e.usedSortCmpI64 {
		return
	}
	e.usedSortCmpI64 = true
	e.emitGlobal(`define i32 @__kml_cmp_i64(ptr %pa, ptr %pb) {
  %a = load i64, ptr %pa, align 8
  %b = load i64, ptr %pb, align 8
  %lt = icmp slt i64 %a, %b
  %gt = icmp sgt i64 %a, %b
  %r0 = select i1 %lt, i32 -1, i32 0
  %r1 = select i1 %gt, i32 1, i32 %r0
  ret i32 %r1
}`)
}

func (e *Emitter) ensureSortCmpF64() {
	if e.usedSortCmpF64 {
		return
	}
	e.usedSortCmpF64 = true
	e.emitGlobal(`define i32 @__kml_cmp_f64(ptr %pa, ptr %pb) {
  %a = load double, ptr %pa, align 8
  %b = load double, ptr %pb, align 8
  %lt = fcmp olt double %a, %b
  %gt = fcmp ogt double %a, %b
  %r0 = select i1 %lt, i32 -1, i32 0
  %r1 = select i1 %gt, i32 1, i32 %r0
  ret i32 %r1
}`)
}

func (e *Emitter) ensureSortCmpStr() {
	if e.usedSortCmpStr {
		return
	}
	e.usedSortCmpStr = true
	e.ensureStrcmp()
	e.emitGlobal(`define i32 @__kml_cmp_str(ptr %pa, ptr %pb) {
  %a = load ptr, ptr %pa, align 8
  %b = load ptr, ptr %pb, align 8
  %r = call i32 @strcmp(ptr %a, ptr %b)
  ret i32 %r
}`)
}

// ensureSortTrampoline emits the trampoline and global closure ptr for custom sort.
// The trampoline loads the KML closure from the global, loads both elements, and
// calls the closure with (envptr, a, b), truncating the i64 result to i32.
func (e *Emitter) ensureSortTrampolineI64() {
	if e.usedSortTrampolineI64 {
		return
	}
	e.usedSortTrampolineI64 = true
	e.ensureSortClosGlobal()
	e.emitGlobal(`define i32 @__kml_sort_tramp_i64(ptr %pa, ptr %pb) {
  %clos = load ptr, ptr @__kml_sort_clos, align 8
  %a = load i64, ptr %pa, align 8
  %b = load i64, ptr %pb, align 8
  %fp_slot = getelementptr {ptr, ptr}, ptr %clos, i32 0, i32 0
  %fp = load ptr, ptr %fp_slot, align 8
  %ep_slot = getelementptr {ptr, ptr}, ptr %clos, i32 0, i32 1
  %ep = load ptr, ptr %ep_slot, align 8
  %r = call i64 (ptr, i64, i64) %fp(ptr %ep, i64 %a, i64 %b)
  %ri = trunc i64 %r to i32
  ret i32 %ri
}`)
}

func (e *Emitter) ensureSortTrampolineF64() {
	if e.usedSortTrampolineF64 {
		return
	}
	e.usedSortTrampolineF64 = true
	e.ensureSortClosGlobal()
	e.emitGlobal(`define i32 @__kml_sort_tramp_f64(ptr %pa, ptr %pb) {
  %clos = load ptr, ptr @__kml_sort_clos, align 8
  %a = load double, ptr %pa, align 8
  %b = load double, ptr %pb, align 8
  %fp_slot = getelementptr {ptr, ptr}, ptr %clos, i32 0, i32 0
  %fp = load ptr, ptr %fp_slot, align 8
  %ep_slot = getelementptr {ptr, ptr}, ptr %clos, i32 0, i32 1
  %ep = load ptr, ptr %ep_slot, align 8
  %r = call i64 (ptr, double, double) %fp(ptr %ep, double %a, double %b)
  %ri = trunc i64 %r to i32
  ret i32 %ri
}`)
}

func (e *Emitter) ensureSortTrampolineStr() {
	if e.usedSortTrampolineStr {
		return
	}
	e.usedSortTrampolineStr = true
	e.ensureSortClosGlobal()
	e.emitGlobal(`define i32 @__kml_sort_tramp_str(ptr %pa, ptr %pb) {
  %clos = load ptr, ptr @__kml_sort_clos, align 8
  %a = load ptr, ptr %pa, align 8
  %b = load ptr, ptr %pb, align 8
  %fp_slot = getelementptr {ptr, ptr}, ptr %clos, i32 0, i32 0
  %fp = load ptr, ptr %fp_slot, align 8
  %ep_slot = getelementptr {ptr, ptr}, ptr %clos, i32 0, i32 1
  %ep = load ptr, ptr %ep_slot, align 8
  %r = call i64 (ptr, ptr, ptr) %fp(ptr %ep, ptr %a, ptr %b)
  %ri = trunc i64 %r to i32
  ret i32 %ri
}`)
}

// --- Map / Set helpers ---
//
// Map header layout (32 bytes):
//   +0   i64  size  — current entry count
//   +8   i64  cap   — capacity (starts at 8)
//   +16  ptr  keys  — key array  (ptr[] for string keys, i64[] for number keys)
//   +24  ptr  vals  — value array (i64[])
//
// Set reuses the exact same layout; elements are stored as keys. vals is
// allocated but ignored. set.values() returns the keys array.

func (e *Emitter) ensureMapStrHelpers() {
	if e.usedMapStrHelpers {
		return
	}
	e.usedMapStrHelpers = true
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureStrcmp()
	e.ensureMemcpy()
	e.emitGlobal(`
define ptr @__kml_map_str_create() {
entry:
  %h = call ptr @malloc(i64 32)
  store i64 0, ptr %h, align 8
  %cap_p = getelementptr i8, ptr %h, i64 8
  store i64 8, ptr %cap_p, align 8
  %keys = call ptr @malloc(i64 64)
  %keys_p = getelementptr i8, ptr %h, i64 16
  store ptr %keys, ptr %keys_p, align 8
  %vals = call ptr @malloc(i64 64)
  %vals_p = getelementptr i8, ptr %h, i64 24
  store ptr %vals, ptr %vals_p, align 8
  ret ptr %h
}

define i64 @__kml_map_str_find(ptr %map, ptr %key) {
entry:
  %size = load i64, ptr %map, align 8
  %keys_p = getelementptr i8, ptr %map, i64 16
  %keys = load ptr, ptr %keys_p, align 8
  br label %scan
scan:
  %i = phi i64 [ 0, %entry ], [ %i_next, %cont ]
  %done = icmp sge i64 %i, %size
  br i1 %done, label %miss, label %chk
chk:
  %kslot = getelementptr ptr, ptr %keys, i64 %i
  %kptr = load ptr, ptr %kslot, align 8
  %cmp = call i32 @strcmp(ptr %kptr, ptr %key)
  %eq = icmp eq i32 %cmp, 0
  br i1 %eq, label %hit, label %cont
hit:
  ret i64 %i
cont:
  %i_next = add i64 %i, 1
  br label %scan
miss:
  ret i64 -1
}

define void @__kml_map_str_set(ptr %map, ptr %key, i64 %val) {
entry:
  %idx = call i64 @__kml_map_str_find(ptr %map, ptr %key)
  %found = icmp sge i64 %idx, 0
  br i1 %found, label %do_update, label %grow_chk
do_update:
  %vp0 = getelementptr i8, ptr %map, i64 24
  %va0 = load ptr, ptr %vp0, align 8
  %vs0 = getelementptr i64, ptr %va0, i64 %idx
  store i64 %val, ptr %vs0, align 8
  ret void
grow_chk:
  %size = load i64, ptr %map, align 8
  %cap_p = getelementptr i8, ptr %map, i64 8
  %cap = load i64, ptr %cap_p, align 8
  %need = icmp sge i64 %size, %cap
  br i1 %need, label %do_grow, label %do_ins
do_grow:
  %ncap = mul i64 %cap, 2
  %nb = mul i64 %ncap, 8
  %kp1 = getelementptr i8, ptr %map, i64 16
  %ok = load ptr, ptr %kp1, align 8
  %nk = call ptr @realloc(ptr %ok, i64 %nb)
  store ptr %nk, ptr %kp1, align 8
  %vp1 = getelementptr i8, ptr %map, i64 24
  %ov = load ptr, ptr %vp1, align 8
  %nv = call ptr @realloc(ptr %ov, i64 %nb)
  store ptr %nv, ptr %vp1, align 8
  store i64 %ncap, ptr %cap_p, align 8
  br label %do_ins
do_ins:
  %sz2 = load i64, ptr %map, align 8
  %kp2 = getelementptr i8, ptr %map, i64 16
  %ka2 = load ptr, ptr %kp2, align 8
  %ks = getelementptr ptr, ptr %ka2, i64 %sz2
  store ptr %key, ptr %ks, align 8
  %vp2 = getelementptr i8, ptr %map, i64 24
  %va2 = load ptr, ptr %vp2, align 8
  %vs = getelementptr i64, ptr %va2, i64 %sz2
  store i64 %val, ptr %vs, align 8
  %sz3 = add i64 %sz2, 1
  store i64 %sz3, ptr %map, align 8
  ret void
}

define i64 @__kml_map_str_get(ptr %map, ptr %key) {
entry:
  %idx = call i64 @__kml_map_str_find(ptr %map, ptr %key)
  %found = icmp sge i64 %idx, 0
  br i1 %found, label %hit, label %miss
hit:
  %vp = getelementptr i8, ptr %map, i64 24
  %va = load ptr, ptr %vp, align 8
  %vs = getelementptr i64, ptr %va, i64 %idx
  %v = load i64, ptr %vs, align 8
  ret i64 %v
miss:
  ret i64 0
}

define i1 @__kml_map_str_has(ptr %map, ptr %key) {
entry:
  %idx = call i64 @__kml_map_str_find(ptr %map, ptr %key)
  %found = icmp sge i64 %idx, 0
  ret i1 %found
}

define i1 @__kml_map_str_delete(ptr %map, ptr %key) {
entry:
  %idx = call i64 @__kml_map_str_find(ptr %map, ptr %key)
  %found = icmp sge i64 %idx, 0
  br i1 %found, label %do_del, label %miss
miss:
  ret i1 false
do_del:
  %size = load i64, ptr %map, align 8
  %last = sub i64 %size, 1
  %is_last = icmp eq i64 %idx, %last
  br i1 %is_last, label %shrink, label %swap
swap:
  %kp = getelementptr i8, ptr %map, i64 16
  %ka = load ptr, ptr %kp, align 8
  %dst_k = getelementptr ptr, ptr %ka, i64 %idx
  %src_k = getelementptr ptr, ptr %ka, i64 %last
  %lk = load ptr, ptr %src_k, align 8
  store ptr %lk, ptr %dst_k, align 8
  %vp = getelementptr i8, ptr %map, i64 24
  %va = load ptr, ptr %vp, align 8
  %dst_v = getelementptr i64, ptr %va, i64 %idx
  %src_v = getelementptr i64, ptr %va, i64 %last
  %lv = load i64, ptr %src_v, align 8
  store i64 %lv, ptr %dst_v, align 8
  br label %shrink
shrink:
  store i64 %last, ptr %map, align 8
  ret i1 true
}

define {ptr, i64} @__kml_map_str_keys(ptr %map) {
entry:
  %size = load i64, ptr %map, align 8
  %kp = getelementptr i8, ptr %map, i64 16
  %k = load ptr, ptr %kp, align 8
  %bytes = mul i64 %size, 8
  %arr = call ptr @malloc(i64 %bytes)
  call ptr @memcpy(ptr %arr, ptr %k, i64 %bytes)
  %r0 = insertvalue {ptr, i64} undef, ptr %arr, 0
  %r1 = insertvalue {ptr, i64} %r0, i64 %size, 1
  ret {ptr, i64} %r1
}

define {ptr, i64} @__kml_map_str_vals(ptr %map) {
entry:
  %size = load i64, ptr %map, align 8
  %vp = getelementptr i8, ptr %map, i64 24
  %v = load ptr, ptr %vp, align 8
  %bytes = mul i64 %size, 8
  %arr = call ptr @malloc(i64 %bytes)
  call ptr @memcpy(ptr %arr, ptr %v, i64 %bytes)
  %r0 = insertvalue {ptr, i64} undef, ptr %arr, 0
  %r1 = insertvalue {ptr, i64} %r0, i64 %size, 1
  ret {ptr, i64} %r1
}`)
}

func (e *Emitter) ensureMapNumHelpers() {
	if e.usedMapNumHelpers {
		return
	}
	e.usedMapNumHelpers = true
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureMemcpy()
	e.emitGlobal(`
define ptr @__kml_map_num_create() {
entry:
  %h = call ptr @malloc(i64 32)
  store i64 0, ptr %h, align 8
  %cap_p = getelementptr i8, ptr %h, i64 8
  store i64 8, ptr %cap_p, align 8
  %keys = call ptr @malloc(i64 64)
  %keys_p = getelementptr i8, ptr %h, i64 16
  store ptr %keys, ptr %keys_p, align 8
  %vals = call ptr @malloc(i64 64)
  %vals_p = getelementptr i8, ptr %h, i64 24
  store ptr %vals, ptr %vals_p, align 8
  ret ptr %h
}

define i64 @__kml_map_num_find(ptr %map, i64 %key) {
entry:
  %size = load i64, ptr %map, align 8
  %keys_p = getelementptr i8, ptr %map, i64 16
  %keys = load ptr, ptr %keys_p, align 8
  br label %scan
scan:
  %i = phi i64 [ 0, %entry ], [ %i_next, %cont ]
  %done = icmp sge i64 %i, %size
  br i1 %done, label %miss, label %chk
chk:
  %kslot = getelementptr i64, ptr %keys, i64 %i
  %kval = load i64, ptr %kslot, align 8
  %eq = icmp eq i64 %kval, %key
  br i1 %eq, label %hit, label %cont
hit:
  ret i64 %i
cont:
  %i_next = add i64 %i, 1
  br label %scan
miss:
  ret i64 -1
}

define void @__kml_map_num_set(ptr %map, i64 %key, i64 %val) {
entry:
  %idx = call i64 @__kml_map_num_find(ptr %map, i64 %key)
  %found = icmp sge i64 %idx, 0
  br i1 %found, label %do_update, label %grow_chk
do_update:
  %vp0 = getelementptr i8, ptr %map, i64 24
  %va0 = load ptr, ptr %vp0, align 8
  %vs0 = getelementptr i64, ptr %va0, i64 %idx
  store i64 %val, ptr %vs0, align 8
  ret void
grow_chk:
  %size = load i64, ptr %map, align 8
  %cap_p = getelementptr i8, ptr %map, i64 8
  %cap = load i64, ptr %cap_p, align 8
  %need = icmp sge i64 %size, %cap
  br i1 %need, label %do_grow, label %do_ins
do_grow:
  %ncap = mul i64 %cap, 2
  %nb = mul i64 %ncap, 8
  %kp1 = getelementptr i8, ptr %map, i64 16
  %ok = load ptr, ptr %kp1, align 8
  %nk = call ptr @realloc(ptr %ok, i64 %nb)
  store ptr %nk, ptr %kp1, align 8
  %vp1 = getelementptr i8, ptr %map, i64 24
  %ov = load ptr, ptr %vp1, align 8
  %nv = call ptr @realloc(ptr %ov, i64 %nb)
  store ptr %nv, ptr %vp1, align 8
  store i64 %ncap, ptr %cap_p, align 8
  br label %do_ins
do_ins:
  %sz2 = load i64, ptr %map, align 8
  %kp2 = getelementptr i8, ptr %map, i64 16
  %ka2 = load ptr, ptr %kp2, align 8
  %ks = getelementptr i64, ptr %ka2, i64 %sz2
  store i64 %key, ptr %ks, align 8
  %vp2 = getelementptr i8, ptr %map, i64 24
  %va2 = load ptr, ptr %vp2, align 8
  %vs = getelementptr i64, ptr %va2, i64 %sz2
  store i64 %val, ptr %vs, align 8
  %sz3 = add i64 %sz2, 1
  store i64 %sz3, ptr %map, align 8
  ret void
}

define i64 @__kml_map_num_get(ptr %map, i64 %key) {
entry:
  %idx = call i64 @__kml_map_num_find(ptr %map, i64 %key)
  %found = icmp sge i64 %idx, 0
  br i1 %found, label %hit, label %miss
hit:
  %vp = getelementptr i8, ptr %map, i64 24
  %va = load ptr, ptr %vp, align 8
  %vs = getelementptr i64, ptr %va, i64 %idx
  %v = load i64, ptr %vs, align 8
  ret i64 %v
miss:
  ret i64 0
}

define i1 @__kml_map_num_has(ptr %map, i64 %key) {
entry:
  %idx = call i64 @__kml_map_num_find(ptr %map, i64 %key)
  %found = icmp sge i64 %idx, 0
  ret i1 %found
}

define i1 @__kml_map_num_delete(ptr %map, i64 %key) {
entry:
  %idx = call i64 @__kml_map_num_find(ptr %map, i64 %key)
  %found = icmp sge i64 %idx, 0
  br i1 %found, label %do_del, label %miss
miss:
  ret i1 false
do_del:
  %size = load i64, ptr %map, align 8
  %last = sub i64 %size, 1
  %is_last = icmp eq i64 %idx, %last
  br i1 %is_last, label %shrink, label %swap
swap:
  %kp = getelementptr i8, ptr %map, i64 16
  %ka = load ptr, ptr %kp, align 8
  %dst_k = getelementptr i64, ptr %ka, i64 %idx
  %src_k = getelementptr i64, ptr %ka, i64 %last
  %lk = load i64, ptr %src_k, align 8
  store i64 %lk, ptr %dst_k, align 8
  %vp = getelementptr i8, ptr %map, i64 24
  %va = load ptr, ptr %vp, align 8
  %dst_v = getelementptr i64, ptr %va, i64 %idx
  %src_v = getelementptr i64, ptr %va, i64 %last
  %lv = load i64, ptr %src_v, align 8
  store i64 %lv, ptr %dst_v, align 8
  br label %shrink
shrink:
  store i64 %last, ptr %map, align 8
  ret i1 true
}

define {ptr, i64} @__kml_map_num_keys(ptr %map) {
entry:
  %size = load i64, ptr %map, align 8
  %kp = getelementptr i8, ptr %map, i64 16
  %k = load ptr, ptr %kp, align 8
  %bytes = mul i64 %size, 8
  %arr = call ptr @malloc(i64 %bytes)
  call ptr @memcpy(ptr %arr, ptr %k, i64 %bytes)
  %r0 = insertvalue {ptr, i64} undef, ptr %arr, 0
  %r1 = insertvalue {ptr, i64} %r0, i64 %size, 1
  ret {ptr, i64} %r1
}

define {ptr, i64} @__kml_map_num_vals(ptr %map) {
entry:
  %size = load i64, ptr %map, align 8
  %vp = getelementptr i8, ptr %map, i64 24
  %v = load ptr, ptr %vp, align 8
  %bytes = mul i64 %size, 8
  %arr = call ptr @malloc(i64 %bytes)
  call ptr @memcpy(ptr %arr, ptr %v, i64 %bytes)
  %r0 = insertvalue {ptr, i64} undef, ptr %arr, 0
  %r1 = insertvalue {ptr, i64} %r0, i64 %size, 1
  ret {ptr, i64} %r1
}`)
}

// ensureFrozenSet declares the global frozen-object tracker Object.freeze(obj)
// uses: a Set<number>-shaped structure (the same __kml_map_num_* helpers
// Set<T> itself is built on, keyed on ptrtoint(obj)) rather than a new
// hand-rolled hash set, since the existing map helpers already do exactly
// what's needed (membership add + O(n) lookup) and this project's Map/Set
// are themselves the same underlying representation.
//
// __kml_frozen_set_get() lazily creates the one global set on first use and
// returns it — called both by Object.freeze (to add) and by every
// object-field write site (to check), so a program that never calls
// Object.freeze never pays for the lazy-init branch beyond one null check,
// but any object mutation still pays one O(n) lookup against the (possibly
// empty) frozen set — an unconditional correctness cost, not a per-feature
// opt-in, the same trade-off ADR-00044's array bounds check already made.
func (e *Emitter) ensureFrozenSet() {
	if e.usedFrozenSet {
		return
	}
	e.usedFrozenSet = true
	e.ensureMapNumHelpers()
	e.emitGlobal(`@__kml_frozen_set = internal thread_local global ptr null, align 8`)
	e.emitGlobal(`
define ptr @__kml_frozen_set_get() {
entry:
  %cur = load ptr, ptr @__kml_frozen_set, align 8
  %isnull = icmp eq ptr %cur, null
  br i1 %isnull, label %init, label %have

init:
  %new = call ptr @__kml_map_num_create()
  store ptr %new, ptr @__kml_frozen_set, align 8
  br label %have

have:
  %final = load ptr, ptr @__kml_frozen_set, align 8
  ret ptr %final
}`)
}
