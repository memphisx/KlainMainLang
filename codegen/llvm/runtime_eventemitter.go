package llvm

// --- EventEmitter listener-list helpers (TDD-00023) ---
//
// Listener list header (24 bytes) — deliberately mirrors Map's own header
// field order (size, cap, then a data pointer) for consistency, even though
// it's an otherwise independent struct:
//
//	+0   i64  len   — current entry count
//	+8   i64  cap   — capacity (starts at 4)
//	+16  ptr  data  — entry array, each entry 16 bytes: {ptr listener, i64 once}
//
// The event-name -> listener-list association itself is just a plain
// Map<string,ptr> (this compiler's existing __kml_map_str_* helpers,
// runtime_collections.go, called directly by name) — only the list a map
// value points to is new here.

func (e *Emitter) ensureEventEmitterRuntime() {
	if e.usedEventEmitterRuntime {
		return
	}
	e.usedEventEmitterRuntime = true
	e.ensureMalloc()
	e.ensureRealloc()
	e.emitGlobal(`
define ptr @__kml_ee_list_create() {
entry:
  %h = call ptr @malloc(i64 24)
  store i64 0, ptr %h, align 8
  %cap_p = getelementptr i8, ptr %h, i64 8
  store i64 4, ptr %cap_p, align 8
  %data = call ptr @malloc(i64 64)
  %data_p = getelementptr i8, ptr %h, i64 16
  store ptr %data, ptr %data_p, align 8
  ret ptr %h
}

define void @__kml_ee_list_push(ptr %list, ptr %listener, i64 %once) {
entry:
  %len = load i64, ptr %list, align 8
  %cap_p = getelementptr i8, ptr %list, i64 8
  %cap = load i64, ptr %cap_p, align 8
  %need = icmp sge i64 %len, %cap
  br i1 %need, label %grow, label %ins
grow:
  %ncap = mul i64 %cap, 2
  %nb = mul i64 %ncap, 16
  %data_p1 = getelementptr i8, ptr %list, i64 16
  %od = load ptr, ptr %data_p1, align 8
  %nd = call ptr @realloc(ptr %od, i64 %nb)
  store ptr %nd, ptr %data_p1, align 8
  store i64 %ncap, ptr %cap_p, align 8
  br label %ins
ins:
  %data_p2 = getelementptr i8, ptr %list, i64 16
  %d = load ptr, ptr %data_p2, align 8
  %lp = getelementptr {ptr, i64}, ptr %d, i64 %len, i32 0
  store ptr %listener, ptr %lp, align 8
  %op = getelementptr {ptr, i64}, ptr %d, i64 %len, i32 1
  store i64 %once, ptr %op, align 8
  %len2 = add i64 %len, 1
  store i64 %len2, ptr %list, align 8
  ret void
}

define void @__kml_ee_list_remove(ptr %list, ptr %listener) {
entry:
  %isnull = icmp eq ptr %list, null
  br i1 %isnull, label %done, label %scan
scan:
  %len = load i64, ptr %list, align 8
  %data_p = getelementptr i8, ptr %list, i64 16
  %d = load ptr, ptr %data_p, align 8
  br label %loop
loop:
  %i = phi i64 [ 0, %scan ], [ %i_next, %cont ]
  %atend = icmp sge i64 %i, %len
  br i1 %atend, label %done, label %chk
chk:
  %lp = getelementptr {ptr, i64}, ptr %d, i64 %i, i32 0
  %lv = load ptr, ptr %lp, align 8
  %eq = icmp eq ptr %lv, %listener
  br i1 %eq, label %remove, label %cont
remove:
  %last = sub i64 %len, 1
  %islast = icmp eq i64 %i, %last
  br i1 %islast, label %shrink, label %swap
swap:
  %src_lp = getelementptr {ptr, i64}, ptr %d, i64 %last, i32 0
  %src_l = load ptr, ptr %src_lp, align 8
  %src_op = getelementptr {ptr, i64}, ptr %d, i64 %last, i32 1
  %src_o = load i64, ptr %src_op, align 8
  store ptr %src_l, ptr %lp, align 8
  %dst_op = getelementptr {ptr, i64}, ptr %d, i64 %i, i32 1
  store i64 %src_o, ptr %dst_op, align 8
  br label %shrink
shrink:
  store i64 %last, ptr %list, align 8
  ret void
cont:
  %i_next = add i64 %i, 1
  br label %loop
done:
  ret void
}`)
}
