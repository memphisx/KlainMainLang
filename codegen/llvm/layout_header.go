package llvm

import (
	"fmt"
	"sort"
	"strings"
)

// The runtime's C files reach into structs codegen lays out (TDD-00240):
// kml_layout.h, generated from the same IR type strings the emitter uses,
// gives them each field's offset and each struct's size, so a layout
// change reaches C instead of shifting under it.

// layoutDef is one struct the C runtime reads: its macro prefix, its IR
// type and the fields C names.
type layoutDef struct {
	prefix string
	ir     string
	fields map[int]string
}

// layoutDefs are the structs in kml_layout.h.
var layoutDefs = []layoutDef{
	{"KML_PROMISE", promiseStructIR, map[int]string{0: "STATE", 1: "WAITER", 2: "V0", 3: "V1", 4: "REACTIONS", 5: "BOXED", promiseFlagsSlot: "FLAGS"}},
	{"KML_CLOSURE", "{ ptr, ptr }", map[int]string{0: "FN", 1: "ENV"}},
	// A promise's reaction list node (promise field REACTIONS).
	{"KML_REACTION", "{ ptr, ptr }", map[int]string{0: "CLOSURE", 1: "NEXT"}},
	{"KML_TASK", taskStructIR, map[int]string{taskCtx: "CTX", taskStack: "STACK", taskPromiseSlot: "PROMISE", taskState: "STATE",
		taskPendingFetch: "PENDING_FETCH", taskPendingGroup: "PENDING_GROUP", taskPendingProm: "PENDING_PROMISE", taskFn: "FN",
		taskArgs: "ARGS", taskResumerCtx: "RESUMER", taskJmpStk: "JMPSTK", taskSavedJmpTop: "JMPTOP", taskAsyncCtx: "ASYNC_CTX",
		taskStackSize: "STACK_SIZE"}},
	// The fields of an in-flight fetch a parked task polls.
	{"KML_FETCHREQ", "{ ptr, ptr, i64, i64, i64, ptr, i64, ptr, i64 }", map[int]string{2: "DONE", 6: "HEADERS_DONE"}},
	// A connection slot of the HTTP server's connection array.
	{"KML_CONN", "{ i64, ptr, ptr, ptr, ptr }", map[int]string{0: "FD", 1: "CTX"}},
	{"KML_ERROR", errorObjType.StructIR(), map[int]string{0: "KIND", 1: "MSG", 2: "NAME", 3: "CODE", 4: "ERRCODE", 5: "ERRSTR", 6: "SYSCALL", 7: "PATH", 8: "ERRNO", 9: "DEST"}},
	{"KML_ERR", errorObjType.StructIR(), map[int]string{1: "MESSAGE", 2: "NAME", 3: "CODE", 4: "ERRCODE", 5: "ERRSTR", 8: "ERRNO", 10: "CAUSE", 11: "ADDRESS", 12: "PORT", 13: "EXTRA", 14: "NAME_OWN"}},
	{"KML_CP", cpStructIR, map[int]string{0: "PID", 1: "STDIN", 2: "STDOUT", 3: "STDERR", 4: "STATE", 5: "EXITCODE", 6: "OUT_DATA", 9: "ERR_END", 10: "CLOSE_L", 11: "EXIT_L", 12: "ERROR_L", 13: "MODE", 14: "OUT_ACC", 15: "ERR_ACC", 16: "EXEC_CB", 17: "IPC_FD", 18: "MESSAGE", 19: "CHAN", 20: "SPAWN_ERRNO", 21: "KILL_SIG", 22: "DEADLINE", 23: "TIMEOUT_SIG", 24: "UNREF", 25: "WAIT_STATUS", 26: "PLAIN_CODE", 27: "MAXBUF", 28: "MAXBUF_HIT"}},
	{"KML_NETSOCK", netSocketIR, map[int]string{0: "FD", 1: "STATE", 2: "DATA", 3: "END", 5: "SSL", 6: "CLOSE"}},
	{"KML_FETCHPENDING", "{ ptr, ptr, i64, i64, i64, ptr, i64, ptr, i64, ptr }", map[int]string{9: "BRIDGE"}},
	{"KML_PBOX", promiseBoxType().StructIR(), map[int]string{1: "PROMISE", 2: "BOXFN"}},
	{"KML_RESP", ResponseType().StructIR(), respLayoutFields()},
	{"KML_RSTREAM", rstreamStructIR, nil},
	{"KML_WSTREAM", wstreamStructIR, nil},
	{"KML_PIPE", pipeCtxStructIR, nil},
	{"KML_TEE", teeCtxStructIR, nil},
	{"KML_TS", tsCtxStructIR, nil},
	{"KML_ZCTX", zctxStructIR, nil},
}

// layoutConsts are the flag values C shares with the emitter.
var layoutConsts = []struct {
	name  string
	value int64
}{
	{"KML_PROMISE_FLAG_HANDLED", promiseFlagHandled},
	{"KML_PROMISE_FLAG_QUEUED", promiseFlagQueued},
	{"KML_TAG_ERROR", kmlTagError},
	{"KML_NB_UNDEFINED", nbUndefined},
	{"KML_NB_NULL", nbNull},
	{"KML_TAG_UNDEFINED", kmlTagUndefined},
	{"KML_PDYN_THEN", pdynThen},
	{"KML_TAG_OBJECT", kmlTagObject},
	{"KML_ERROR_SUBCLASS_BASE", errorSubclassTagBase},
	{"KML_CP_BYTES", cpStructBytes},
	{"KML_NB_FALSE", nbFalse},
	{"KML_NB_TRUE", nbTrue},
	{"KML_ERRKIND_ERROR", errorTypeIDStored(errorKindIDs["Error"])},
	{"KML_ERRKIND_RANGE", errorTypeIDStored(errorKindIDs["RangeError"])},
	{"KML_ERROR_TYPE_FLAG", errorTypeIDFlag},
}

// layoutHeader is kml_layout.h.
func layoutHeader() string {
	var b strings.Builder
	b.WriteString("/* kml_layout.h: generated from codegen's struct layouts (layout_header.go). */\n")
	b.WriteString("#ifndef KML_LAYOUT_H\n#define KML_LAYOUT_H\n")
	for _, d := range layoutDefs {
		offs, size := irStructOffsets(d.ir)
		var idx []int
		for i := range d.fields {
			idx = append(idx, i)
		}
		sort.Ints(idx)
		for _, i := range idx {
			fmt.Fprintf(&b, "#define %s_%s %d\n", d.prefix, d.fields[i], offs[i])
		}
		fmt.Fprintf(&b, "#define %s_SIZE %d\n", d.prefix, size)
	}
	for _, c := range layoutConsts {
		fmt.Fprintf(&b, "#define %s %d\n", c.name, c.value)
	}
	b.WriteString("#endif\n")
	return b.String()
}

// irStructOffsets is each field's byte offset in the IR struct type ir
// (`{ i64, ptr, … }`, natural alignment, nested structs and arrays allowed)
// and the struct's size.
func irStructOffsets(ir string) ([]int, int) {
	fields := splitIRFields(ir)
	offs := make([]int, len(fields))
	off, maxAlign := 0, 1
	for i, f := range fields {
		size, align := irTypeSizeAlign(f)
		off = (off + align - 1) / align * align
		offs[i] = off
		off += size
		if align > maxAlign {
			maxAlign = align
		}
	}
	return offs, (off + maxAlign - 1) / maxAlign * maxAlign
}

// splitIRFields is the top-level field types of struct type ir.
func splitIRFields(ir string) []string {
	ir = strings.TrimSpace(ir)
	ir = strings.TrimSuffix(strings.TrimPrefix(ir, "{"), "}")
	var out []string
	depth, start := 0, 0
	for i, c := range ir {
		switch c {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(ir[start:i]))
				start = i + 1
			}
		}
	}
	if s := strings.TrimSpace(ir[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// irTypeSizeAlign is an IR type's size and alignment on the 64-bit hosts.
func irTypeSizeAlign(t string) (int, int) {
	t = strings.TrimSpace(t)
	switch {
	case t == "i1" || t == "i8":
		return 1, 1
	case t == "i16":
		return 2, 2
	case t == "i32" || t == "float":
		return 4, 4
	case t == "i64" || t == "double" || t == "ptr":
		return 8, 8
	case strings.HasPrefix(t, "{"):
		offs, size := irStructOffsets(t)
		align := 1
		for _, f := range splitIRFields(t) {
			if _, a := irTypeSizeAlign(f); a > align {
				align = a
			}
		}
		_ = offs
		return size, align
	case strings.HasPrefix(t, "["):
		var n int
		var elem string
		inner := strings.TrimSuffix(strings.TrimPrefix(t, "["), "]")
		if k := strings.Index(inner, " x "); k > 0 {
			fmt.Sscanf(inner[:k], "%d", &n)
			elem = inner[k+3:]
		}
		size, align := irTypeSizeAlign(elem)
		return n * size, align
	}
	panic("irTypeSizeAlign: " + t)
}

// respLayoutFields names the Response fields C reads.
func respLayoutFields() map[int]string {
	r := ResponseType()
	idx := func(f string) int { i, _, _ := r.FieldIndex(f); return i }
	return map[int]string{idx("status"): "STATUS", idx("ok"): "OK", idx("__kml_pending"): "PENDING"}
}
