package llvm

import (
	_ "embed"
	"fmt"
	"strings"
)

// The runtime's C resolves on its own (TDD-00251 Stage 4): hookssrc/hooks.c,
// linked into every program, defines the data the C reads from the program,
// the NaN-box and string-header primitives, and a trampoline for each
// routine the program generates and the C calls. The program's own copy of
// such a routine is renamed __kml_ir_<name> (its IR calls stay direct and
// inlinable) and registered through hooks.c's __kml_hookp_<name> from a
// constructor.

//go:embed hookssrc/hooks.c
var hooksSource string

// hooksCSource is hooks.c for this program: the tasks' main context is a
// ucontext, whose size is the target's.
func (e *Emitter) hooksCSource() CSource {
	ctxSize, _, _, _ := e.ucontextLayout()
	return CSource{"hooks", hooksSource, []string{fmt.Sprintf("-DKML_MAIN_CTX_SIZE=%d", ctxSize)}, nil, ""}
}

// hookFuncs are the generated routines the runtime's C calls through
// hooks.c (without the __kml_ prefix).
var hookFuncs = []string{
	"any_eq", "any_tobool", "any_tonum",
	"classref_shown", "classref_nstatic", "classref_skey", "classref_sget",
	"error_inspect", "promise_inspect_parts",
	"fs_error_new",
	"httpc_fire_ready",
	"objproto_fill",
	"pdyn_attach", "prim_proto_of", "promise_wrapper_of",
	"process_uncaught", "unhandled_report",
	"signal_aborted", "signal_reason", "signal_timeout",
	"stream_decode",
	"throw_nullderef", "throw_range_error",
	"worker_run_loop", "worker_uncaught",
}

// hookPrims are the primitives both sides define: the IR's renamed copy
// for its own calls, hooks.c's for the C's.
var hookPrims = []string{"nb_pack", "nb_tag", "nb_pay", "str_alloc", "str_len", "str_free"}

// hookRenamed is the set of symbols renamed __kml_ir_ in the program.
var hookRenamed = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range append(append([]string{}, hookFuncs...), hookPrims...) {
		m[n] = true
	}
	return m
}()

// addHookInit adds IR run from the hooks constructor (a per-program value
// hooks.c holds).
func (e *Emitter) addHookInit(ir string) { e.hookInits = append(e.hookInits, ir) }

// emitHookRegistration emits the constructor registering every hook the
// program defines, and the per-program values. Run before the constructor
// list is finalized.
func (e *Emitter) emitHookRegistration() {
	globals, funcs := e.globals.String(), e.functions.String()
	var body strings.Builder
	for _, name := range hookFuncs {
		if !definesHook(globals, name) && !definesHook(funcs, name) {
			continue
		}
		e.emitGlobal(fmt.Sprintf("@__kml_hookp_%s = external global ptr, align 8", name))
		fmt.Fprintf(&body, "  store ptr @__kml_ir_%s, ptr @__kml_hookp_%s, align 8\n", name, name)
	}
	for _, ir := range e.hookInits {
		body.WriteString(ir)
	}
	if body.Len() == 0 {
		return
	}
	e.emitGlobal("define internal void @__kml_hooks_init() {\nentry:\n" + body.String() + "  ret void\n}")
	e.addCtor("@__kml_hooks_init")
}

// definesHook reports whether ir defines @__kml_<name>: a line starting
// "define" that names it. A search per name, not one regexp over the whole
// program's IR, which ran to a sixth of a small program's compile.
func definesHook(ir, name string) bool {
	sym := "@__kml_" + name + "("
	for i := 0; ; {
		j := strings.Index(ir[i:], sym)
		if j < 0 {
			return false
		}
		j += i
		line := strings.LastIndexByte(ir[:j], '\n') + 1
		if strings.HasPrefix(ir[line:], "define") {
			return true
		}
		i = j + len(sym)
	}
}

// renameHooks gives the program's copies of the hooks and primitives their
// __kml_ir_ names, at their definitions and every use. One pass into a
// buffer sized up front: a program's IR runs to tens of megabytes, and a
// regexp replace's doubling result would hold several copies of it.
func renameHooks(ir string) string {
	const pre = "@__kml_"
	var b strings.Builder
	b.Grow(len(ir) + len(ir)/64)
	last := 0
	for i := 0; ; {
		j := strings.Index(ir[i:], pre)
		if j < 0 {
			break
		}
		start := i + j + len(pre)
		k := start
		for k < len(ir) && isIRNameByte(ir[k]) {
			k++
		}
		if hookRenamed[ir[start:k]] {
			b.WriteString(ir[last:start])
			b.WriteString("ir_")
			last = start
		}
		i = k
	}
	if last == 0 {
		return ir
	}
	b.WriteString(ir[last:])
	return b.String()
}

// isIRNameByte reports whether c continues an LLVM identifier.
func isIRNameByte(c byte) bool {
	return c == '-' || c == '$' || c == '.' || c == '_' ||
		c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
