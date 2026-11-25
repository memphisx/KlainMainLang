package llvm

// declare_calls.go — the program's declarations of what it calls, read off
// its calls (TDD-00230 Phase 4): an LLVM call spells the callee's whole
// signature, so a function the program calls and neither defines nor
// declares gets its `declare` from its first call site. A runtime entry
// point is then just called; no declarator has to run first.

import (
	"strings"
)

// runtimeFuncs are runtime functions whose declaration carries more than a
// call spells (a `zeroext` return): the program declares one from here when
// it names it, in place of the declaration read off the call.
var runtimeFuncs = map[string]string{}

func init() {
	// The Map/Set entry points of the string-keyed flavor (FNV-1a, content
	// compare, a null key hashing as the empty string; collections.c).
	for name, sig := range map[string]string{
		"__kml_map_copy_keys":    irPtr + " @%s(" + irPtr + ")",
		"__kml_map_copy_vals":    irPtr + " @%s(" + irPtr + ")",
		"__kml_map_str_create":   irPtr + " @%s()",
		"__kml_map_str_create_h": irPtr + " @%s(i64)",
		"__kml_map_str_set":      "void @%s(" + irPtr + ", " + irPtr + ", i64)",
		"__kml_map_str_get":      "i64 @%s(" + irPtr + ", " + irPtr + ")",
		"__kml_map_str_has":      "zeroext i1 @%s(" + irPtr + ", " + irPtr + ")",
		"__kml_map_str_delete":   "zeroext i1 @%s(" + irPtr + ", " + irPtr + ")",
		// The try frames' setjmp (_setjmp(buf, null) on mingw-w64) and the
		// unwinding longjmp: their attributes are not spelled at a call.
		"setjmp":  "i32 @%s(" + irPtr + ") returns_twice",
		"_setjmp": "i32 @%s(" + irPtr + ", " + irPtr + ") returns_twice",
		"longjmp": "void @%s(" + irPtr + ", i32) noreturn",
		// process.c: whether a listener took a process event.
		"__kml_process_hook_call": "zeroext i1 @%s(i32, i64, i64)",
	} {
		runtimeFuncs[name] = "declare " + strings.Replace(sig, "%s", name, 1)
	}
}

// runtimeGlobals are the runtime's globals, as the program declares (or, for
// its own internal ones, defines) them when it names one. A global's use
// does not spell whether it is thread-local, so unlike a function's its line
// cannot be read off the IR.
var runtimeGlobals = map[string]string{
	// libc's stdout FILE*: `__stdoutp` on macOS, `stdout` on the Linux libcs.
	"__stdoutp": "@__stdoutp = external" + irGlobalAS + " global " + irPtr,
	"stdout":    "@stdout = external" + irGlobalAS + " global " + irPtr,
	// The null of a three-state pointer (nullRef): one cell for the whole
	// binary, the library's units and the C runtime included.
	"__kml_nullref_cell":        "@__kml_nullref_cell = weak_odr" + irGlobalAS + " global { i64, [248 x i8] } zeroinitializer, align 8",
	"__kml_argc":                "@__kml_argc = linkonce_odr thread_local" + irGlobalAS + " global i64 -1, align 8",
	"__kml_array_integrity_any": "@__kml_array_integrity_any = external" + irGlobalAS + " global i8",
	"__kml_caller_frames":       "@__kml_caller_frames = external" + irGlobalAS + " global i32",
	"__kml_pending_proto_set":   "@__kml_pending_proto_set = external" + irGlobalAS + " global i32",
	"__kml_pending_proto":       "@__kml_pending_proto = external" + irGlobalAS + " global " + irPtr + ", align 8",
	"__kml_console_count_map":   "@__kml_console_count_map = internal thread_local" + irGlobalAS + " global " + irPtr + " null, align 8",
	"__kml_console_group_depth": "@__kml_console_group_depth = internal thread_local" + irGlobalAS + " global i64 0, align 8",
	"__kml_console_time_map":    "@__kml_console_time_map = internal thread_local" + irGlobalAS + " global " + irPtr + " null, align 8",
	"__kml_ctor_ret":            "@__kml_ctor_ret = internal thread_local" + irGlobalAS + " global " + irPtr + " null, align 8",
	"__kml_ctor_ret_any":        "@__kml_ctor_ret_any = internal thread_local" + irGlobalAS + " global i64 0, align 8",
	"__kml_newtarget_pending":   "@__kml_newtarget_pending = internal thread_local" + irGlobalAS + " global i64 0, align 8",
	"__kml_cur_jmp_stk":         "@__kml_cur_jmp_stk = external thread_local global " + irPtr + ", align 8",
	"__kml_curl_inited":         "@__kml_curl_inited = external" + irGlobalAS + " global i1, align 1",
	"__kml_curl_multi":          "@__kml_curl_multi = external thread_local" + irGlobalAS + " global " + irPtr + ", align 8",
	"__kml_current_task":        "@__kml_current_task = external thread_local" + irGlobalAS + " global " + irPtr + ", align 8",
	"__kml_d2s_env":             "@__kml_d2s_env = internal" + irGlobalAS + " constant i8 0, align 8",
	"__kml_error_name":          "@__kml_error_name = external" + irGlobalAS + " global " + irPtr + ", align 8",
	"__kml_interp_guard_armed":  "@__kml_interp_guard_armed = external" + irGlobalAS + " global i8, align 1",
	"__kml_error_names":         "@__kml_error_names = external" + irGlobalAS + " global [2 x " + irPtr + "], align 8",
	"__kml_frame_base":          "@__kml_frame_base = external thread_local" + irGlobalAS + " global " + irPtr,
	"__kml_frame_top":           "@__kml_frame_top = external thread_local" + irGlobalAS + " global " + irPtr,
	"__kml_inspect_opt_depth":   "@__kml_inspect_opt_depth = external" + irGlobalAS + " global i64",
	"__kml_jmp_top":             "@__kml_jmp_top = external thread_local" + irGlobalAS + " global i32, align 4",
	"__kml_loop_idle":           "@__kml_loop_idle = external thread_local" + irGlobalAS + " global i1",
	"__kml_loop_nowait":         "@__kml_loop_nowait = external thread_local" + irGlobalAS + " global i8",
	"__kml_loop_oneshot":        "@__kml_loop_oneshot = external thread_local" + irGlobalAS + " global i8",
	"__kml_module_promise":      "@__kml_module_promise = external thread_local" + irGlobalAS + " global " + irPtr + ", align 8",
	"__kml_module_task":         "@__kml_module_task = external thread_local global " + irPtr + ", align 8",
	"__kml_module_wants_loop":   "@__kml_module_wants_loop = external thread_local" + irGlobalAS + " global i1, align 1",
	"__kml_month_names":         "@__kml_month_names = external" + irGlobalAS + " constant [12 x " + irPtr + "]",
	"__kml_process_exit_code":   "@__kml_process_exit_code = external thread_local global i64, align 8",
	"__kml_root_async_ctx":      "@__kml_root_async_ctx = external thread_local" + irGlobalAS + " global " + irPtr + ", align 8",
	"__kml_sort_clos":           "@__kml_sort_clos = external thread_local" + irGlobalAS + " global " + irPtr,
	"__kml_sort_nullref":        "@__kml_sort_nullref = external thread_local" + irGlobalAS + " global " + irPtr,
	"__kml_sort_res":            "@__kml_sort_res = external thread_local" + irGlobalAS + " global i32",
	"__kml_task_active":         "@__kml_task_active = external thread_local" + irGlobalAS + " global i64, align 8",
	"__kml_task_launching":      "@__kml_task_launching = external thread_local global " + irPtr + ", align 8",
	"__kml_views_oob":           "@__kml_views_oob = external" + irGlobalAS + " global i64, align 8",
	"__kml_weekday_names":       "@__kml_weekday_names = external" + irGlobalAS + " constant [7 x " + irPtr + "]",
}

// declareCalled appends a declaration for each function ll calls, or
// takes the address of, without defining or declaring it, and the line of
// each runtime global it names (runtimeGlobals).
func declareCalled(ll string) string {
	known := map[string]bool{}
	for ln := range strings.SplitSeq(ll, "\n") {
		switch {
		case strings.HasPrefix(ln, "define"), strings.HasPrefix(ln, "declare"):
			known[firstSym(ln)] = true
		case strings.HasPrefix(ln, "@"):
			if n, ok := leadingSym(ln); ok {
				known[n] = true
			}
		}
	}
	var decls []string
	for ln := range strings.SplitSeq(ll, "\n") {
		if !strings.HasPrefix(ln, " ") {
			continue // a call is inside a function body
		}
		for rest := ln; ; {
			i := strings.Index(rest, "call ")
			if i < 0 {
				break
			}
			rest = rest[i+len("call "):]
			d, name, ok := declOfCall(rest)
			if ok && !known[name] {
				known[name] = true
				if h, isRuntime := runtimeFuncs[name]; isRuntime {
					d = h
				}
				decls = append(decls, d)
			}
		}
	}
	// A function the program only takes the address of: with opaque
	// pointers its declaration's type is never read.
	for ln := range strings.SplitSeq(ll, "\n") {
		body := strings.HasPrefix(ln, " ")
		if !body && !strings.HasPrefix(ln, "@") {
			continue
		}
		for j := strings.IndexByte(ln, '@'); j >= 0; {
			nm, ok := leadingSym(ln[j:])
			if g, isGlobal := runtimeGlobals[nm]; ok && isGlobal && !known[nm] {
				known[nm] = true
				decls = append(decls, g)
			} else if h, isRuntime := runtimeFuncs[nm]; ok && isRuntime && !known[nm] && (body || j > 0) {
				known[nm] = true
				decls = append(decls, h)
			} else if ok && !known[nm] && (body || j > 0) && !strings.HasPrefix(nm, "llvm.") {
				known[nm] = true
				sym := "@" + nm
				if !isPlainIRName(nm) {
					sym = `@"` + nm + `"`
				}
				decls = append(decls, "declare void "+sym+"()")
			}
			k := strings.IndexByte(ln[j+1:], '@')
			if k < 0 {
				break
			}
			j += k + 1
		}
	}
	if len(decls) == 0 {
		return ll
	}
	return ll + "\n" + strings.Join(decls, "\n") + "\n"
}

// leadingSym is the @name a top-level `@name = …` line defines.
func leadingSym(ln string) (string, bool) {
	if len(ln) < 2 || ln[0] != '@' {
		return "", false
	}
	if ln[1] == '"' {
		q := strings.IndexByte(ln[2:], '"')
		if q < 0 {
			return "", false
		}
		return ln[2 : 2+q], true
	}
	k := 1
	for k < len(ln) && isIRNameByte(ln[k]) {
		k++
	}
	return ln[1:k], k > 1
}

// callRetAttrs are the attributes a call may carry before its return type.
var callRetAttrs = map[string]bool{
	"fast": true, "nnan": true, "ninf": true, "nsz": true, "arcp": true, "contract": true, "afn": true, "reassoc": true,
	"ccc": true, "fastcc": true, "coldcc": true, "tailcc": true, "swiftcc": true,
	"zeroext": true, "signext": true, "noundef": true, "noalias": true, "inreg": true, "nonnull": true,
}

// declOfCall reads a call's text from just past `call ` and returns the
// callee's declaration and name; ok is false for an indirect call.
func declOfCall(s string) (decl, name string, ok bool) {
	// The attributes before the return type.
	var attrs []string
	for {
		sp := strings.IndexByte(s, ' ')
		if sp < 0 {
			return "", "", false
		}
		w := s[:sp]
		if !callRetAttrs[w] {
			break
		}
		if w == "zeroext" || w == "signext" || w == "noundef" || w == "noalias" || w == "inreg" || w == "nonnull" {
			attrs = append(attrs, w)
		}
		s = s[sp+1:]
	}
	ret, rest, ok := parseIRType(s)
	if !ok {
		return "", "", false
	}
	rest = strings.TrimLeft(rest, " ")
	// A variadic callee's call names its function type: `call i32 (ptr, ...) @f(`.
	var fnParams string
	if strings.HasPrefix(rest, "(") {
		end := matchClose(rest, 0)
		if end < 0 {
			return "", "", false
		}
		fnParams = rest[1:end]
		rest = strings.TrimLeft(rest[end+1:], " ")
	}
	if !strings.HasPrefix(rest, "@") {
		return "", "", false // through a register: nothing to declare
	}
	nm, ok := leadingSym(rest)
	if !ok {
		return "", "", false
	}
	rest = rest[1:]
	if rest[0] == '"' {
		rest = rest[len(nm)+2:]
	} else {
		rest = rest[len(nm):]
	}
	if !strings.HasPrefix(rest, "(") {
		return "", "", false
	}
	params := fnParams
	if fnParams == "" {
		end := matchClose(rest, 0)
		if end < 0 {
			return "", "", false
		}
		var tys []string
		for _, arg := range splitTopLevel(rest[1:end], ',') {
			ty, after, ok := parseIRType(strings.TrimLeft(arg, " "))
			if !ok {
				return "", "", false
			}
			// Parameter attributes stay with the type.
			for _, w := range strings.Fields(after) {
				if w == "zeroext" || w == "signext" || w == "noundef" || w == "inreg" {
					ty += " " + w
					continue
				}
				break
			}
			tys = append(tys, ty)
		}
		params = strings.Join(tys, ", ")
	}
	sym := "@" + nm
	if !isPlainIRName(nm) {
		sym = `@"` + nm + `"`
	}
	head := ret
	if len(attrs) > 0 {
		head = strings.Join(attrs, " ") + " " + ret
	}
	return "declare " + head + " " + sym + "(" + params + ")", nm, true
}

// isPlainIRName reports whether n needs no quoting after @.
func isPlainIRName(n string) bool {
	for i := 0; i < len(n); i++ {
		if !isIRNameByte(n[i]) {
			return false
		}
	}
	return n != ""
}

// parseIRType reads one LLVM type from the start of s.
func parseIRType(s string) (ty, rest string, ok bool) {
	if s == "" {
		return "", "", false
	}
	switch s[0] {
	case '{', '[', '<':
		end := matchClose(s, 0)
		if end < 0 {
			return "", "", false
		}
		return s[:end+1], s[end+1:], true
	}
	k := 0
	for k < len(s) && (isIRNameByte(s[k]) || s[k] == '%') {
		k++
	}
	if k == 0 {
		return "", "", false
	}
	ty, rest = s[:k], s[k:]
	if ty == "ptr" && strings.HasPrefix(rest, " addrspace(") {
		end := strings.IndexByte(rest, ')')
		ty, rest = ty+rest[:end+1], rest[end+1:]
	}
	return ty, rest, true
}

// matchClose is the index of the bracket closing the one at s[i].
func matchClose(s string, i int) int {
	depth := 0
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '(', '{', '[', '<':
			depth++
		case ')', '}', ']', '>':
			depth--
			if depth == 0 {
				return j
			}
		case '"':
			q := strings.IndexByte(s[j+1:], '"')
			if q < 0 {
				return -1
			}
			j += q + 1
		}
	}
	return -1
}
