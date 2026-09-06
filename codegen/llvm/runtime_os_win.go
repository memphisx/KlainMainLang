package llvm

import (
	"fmt"
	"strings"
)

// ensureOSCpusWin defines __kml_os_cpus_win() -> {ptr, i64}: os.cpus() on
// Windows (TDD-00177 Stage 5). The per-CPU facts come from the shim's
// __kml_win_cpu_info (registry model/MHz, NtQuerySystemInformation times,
// the same sources Node's uv_cpu_info reads there); this builds the same
// CPUInfoType/CPUTimesType objects the Linux and Darwin readers produce, so
// the property accessors above see one shape.
func (e *Emitter) ensureOSCpusWin() {
	if e.usedOSCpusWin {
		return
	}
	e.usedOSCpusWin = true
	e.ensureMalloc()
	e.ensureStrHeaderRuntime()
	e.emitGlobal("declare i32 @__kml_win_cpu_count()")
	e.emitGlobal("declare i32 @__kml_win_cpu_info(i32, ptr, i32, ptr, ptr)")

	f := newCPUInfoFieldIndexes()
	var b strings.Builder
	fmt.Fprintf(&b, "\ndefine {ptr, i64} @__kml_os_cpus_win() {\n")
	fmt.Fprintf(&b, "entry:\n")
	fmt.Fprintf(&b, "  %%count32 = call i32 @__kml_win_cpu_count()\n")
	fmt.Fprintf(&b, "  %%count = sext i32 %%count32 to i64\n")
	fmt.Fprintf(&b, "  %%outbytes = mul i64 %%count, 8\n")
	fmt.Fprintf(&b, "  %%out = call ptr @malloc(i64 %%outbytes)\n")
	fmt.Fprintf(&b, "  %%i_p = alloca i64, align 8\n")
	fmt.Fprintf(&b, "  %%mhz_p = alloca i64, align 8\n")
	fmt.Fprintf(&b, "  %%times_p = alloca [5 x i64], align 8\n")
	fmt.Fprintf(&b, "  store i64 0, ptr %%i_p, align 8\n")
	fmt.Fprintf(&b, "  br label %%loop\n")
	fmt.Fprintf(&b, "loop:\n")
	fmt.Fprintf(&b, "  %%i = load i64, ptr %%i_p, align 8\n")
	fmt.Fprintf(&b, "  %%atend = icmp sge i64 %%i, %%count\n")
	fmt.Fprintf(&b, "  br i1 %%atend, label %%done, label %%body\n")
	fmt.Fprintf(&b, "body:\n")
	fmt.Fprintf(&b, "  %%i32 = trunc i64 %%i to i32\n")
	fmt.Fprintf(&b, "  %%modelbuf = call ptr @malloc(i64 256)\n")
	fmt.Fprintf(&b, "  call i32 @__kml_win_cpu_info(i32 %%i32, ptr %%modelbuf, i32 256, ptr %%mhz_p, ptr %%times_p)\n")
	fmt.Fprintf(&b, "  %%entry_obj = call ptr @malloc(i64 %d)\n", f.infoSize)
	fmt.Fprintf(&b, "  %%times_obj = call ptr @malloc(i64 %d)\n", f.timesSize)
	fmt.Fprintf(&b, "  %%model_hdr = call ptr @__kml_str_from_cstr(ptr %%modelbuf)\n")
	fmt.Fprintf(&b, "  %%model_slot = getelementptr %s, ptr %%entry_obj, i32 0, i32 %d\n", f.infoStructIR, f.modelIdx)
	fmt.Fprintf(&b, "  store ptr %%model_hdr, ptr %%model_slot, align 8\n")
	fmt.Fprintf(&b, "  %%mhz = load i64, ptr %%mhz_p, align 8\n")
	fmt.Fprintf(&b, "  %%speed_slot = getelementptr %s, ptr %%entry_obj, i32 0, i32 %d\n", f.infoStructIR, f.speedIdx)
	fmt.Fprintf(&b, "  store i64 %%mhz, ptr %%speed_slot, align 8\n")
	for k, fld := range []struct {
		idx int
		reg string
	}{{f.userIdx, "tu"}, {f.niceIdx, "tn"}, {f.sysIdx, "ts"}, {f.idleIdx, "ti"}, {f.irqIdx, "tirq"}} {
		fmt.Fprintf(&b, "  %%%s_src = getelementptr [5 x i64], ptr %%times_p, i32 0, i32 %d\n", fld.reg, k)
		fmt.Fprintf(&b, "  %%%s_v = load i64, ptr %%%s_src, align 8\n", fld.reg, fld.reg)
		fmt.Fprintf(&b, "  %%%s = getelementptr %s, ptr %%times_obj, i32 0, i32 %d\n", fld.reg, f.timesStructIR, fld.idx)
		fmt.Fprintf(&b, "  store i64 %%%s_v, ptr %%%s, align 8\n", fld.reg, fld.reg)
	}
	fmt.Fprintf(&b, "  %%times_slot = getelementptr %s, ptr %%entry_obj, i32 0, i32 %d\n", f.infoStructIR, f.timesFieldIdx)
	fmt.Fprintf(&b, "  store ptr %%times_obj, ptr %%times_slot, align 8\n")
	fmt.Fprintf(&b, "  %%out_slot = getelementptr ptr, ptr %%out, i64 %%i\n")
	fmt.Fprintf(&b, "  store ptr %%entry_obj, ptr %%out_slot, align 8\n")
	fmt.Fprintf(&b, "  %%i_next = add i64 %%i, 1\n")
	fmt.Fprintf(&b, "  store i64 %%i_next, ptr %%i_p, align 8\n")
	fmt.Fprintf(&b, "  br label %%loop\n")
	fmt.Fprintf(&b, "done:\n")
	fmt.Fprintf(&b, "  %%r0 = insertvalue {ptr, i64} undef, ptr %%out, 0\n")
	fmt.Fprintf(&b, "  %%r1 = insertvalue {ptr, i64} %%r0, i64 %%count, 1\n")
	fmt.Fprintf(&b, "  ret {ptr, i64} %%r1\n")
	fmt.Fprintf(&b, "}\n")
	e.emitGlobal(b.String())
}
