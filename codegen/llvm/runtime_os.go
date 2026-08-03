// runtime_os.go — os module C-runtime helpers (TDD-00024): growable procfs
// reading, and the Linux/Darwin implementations of os.cpus(). Simple
// single-call wrappers (getenv, gethostname, sysconf, sysctlbyname) are
// emitted inline in emit_os.go instead — this file is only for the pieces
// substantial enough to warrant their own hand-written IR function, mirroring
// runtime_collections.go/runtime_fs.go's own split.
package llvm

import (
	"fmt"
	"strings"
)

func (e *Emitter) ensureGethostname() {
	if e.usedGethostname {
		return
	}
	e.usedGethostname = true
	e.emitGlobal("declare i32 @gethostname(ptr noundef, i64 noundef)")
}

func (e *Emitter) ensureSysconf() {
	if e.usedSysconf {
		return
	}
	e.usedSysconf = true
	e.emitGlobal("declare i64 @sysconf(i32 noundef)")
}

// ensureSysctlbyname declares Darwin's string-keyed sysctl lookup — used
// instead of sysconf for anything Darwin-specific, since sysconf's numeric
// constants are a completely different (unverified-here) enum on Darwin
// than on Linux; a string MIB name needs no numeric constant to get wrong.
func (e *Emitter) ensureSysctlbyname() {
	if e.usedSysctlbyname {
		return
	}
	e.usedSysctlbyname = true
	e.emitGlobal("declare i32 @sysctlbyname(ptr noundef, ptr noundef, ptr noundef, ptr noundef, i64 noundef)")
}

// ensureMachVM declares the Mach kernel APIs os.freemem()/os.cpus() need on
// Darwin (host_statistics for free-memory, host_processor_info for per-core
// times) — UNVERIFIED on real hardware, see docs/tdd/TDD-00024.md and
// docs/adr/ADR-00090.md. mach_port_t/natural_t are both `unsigned int`
// (i32) on every Darwin architecture — a decades-stable Mach ABI
// convention (unlike ucontext_t, ADR-00051, which is a deliberately-opaque
// scratch buffer whose *size* varies by arch) — so this is lower
// struct-layout risk even though it's still functionally untested.
// getpagesize() (plain libc, no Mach call needed) supplies the page size,
// rather than the Mach host_page_size() call.
func (e *Emitter) ensureMachVM() {
	if e.usedMachVM {
		return
	}
	e.usedMachVM = true
	e.emitGlobal("declare i32 @mach_host_self()")
	e.emitGlobal("declare i32 @getpagesize()")
	e.emitGlobal("declare i32 @host_statistics(i32 noundef, i32 noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @host_processor_info(i32 noundef, i32 noundef, ptr noundef, ptr noundef, ptr noundef)")
}

// ensureOSReadProcFile declares __kml_os_read_proc_file: reads an entire
// file into a malloc'd, null-terminated buffer via a growable-buffer
// fread-until-EOF loop. Deliberately NOT a reuse of emit_fs.go's
// __kml_fs_read_file, whose sizing is SEEK_END+ftell-based — /proc files
// (cpuinfo, stat) aren't real fixed-size files (the kernel synthesizes
// their content on read), so ftell() after seeking doesn't return a usable
// size on them. Throws (via the same __kml_fs_throw fs.* already uses) if
// the file can't be opened.
func (e *Emitter) ensureOSReadProcFile() {
	if e.usedOSReadProcFile {
		return
	}
	e.usedOSReadProcFile = true
	e.ensureFsThrow()
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureFopen()
	e.ensureFclose()
	e.emitGlobal("declare i64 @fread(ptr noundef, i64 noundef, i64 noundef, ptr noundef)")
	modePtr := e.internString("rb")
	opDescPtr := e.internString("cannot open file for reading")

	var b strings.Builder
	fmt.Fprintf(&b, "\ndefine ptr @__kml_os_read_proc_file(ptr %%path) {\n")
	fmt.Fprintf(&b, "entry:\n")
	fmt.Fprintf(&b, "  %%f = call ptr @fopen(ptr %%path, ptr %s)\n", modePtr)
	fmt.Fprintf(&b, "  %%isnull = icmp eq ptr %%f, null\n")
	fmt.Fprintf(&b, "  br i1 %%isnull, label %%fail, label %%ok\n")
	fmt.Fprintf(&b, "fail:\n")
	fmt.Fprintf(&b, "  call void @__kml_fs_throw(ptr %s, ptr %%path)\n", opDescPtr)
	fmt.Fprintf(&b, "  unreachable\n")
	fmt.Fprintf(&b, "ok:\n")
	fmt.Fprintf(&b, "  %%buf_p = alloca ptr, align 8\n")
	fmt.Fprintf(&b, "  %%cap_p = alloca i64, align 8\n")
	fmt.Fprintf(&b, "  %%len_p = alloca i64, align 8\n")
	fmt.Fprintf(&b, "  %%init_buf = call ptr @malloc(i64 4096)\n")
	fmt.Fprintf(&b, "  store ptr %%init_buf, ptr %%buf_p, align 8\n")
	fmt.Fprintf(&b, "  store i64 4096, ptr %%cap_p, align 8\n")
	fmt.Fprintf(&b, "  store i64 0, ptr %%len_p, align 8\n")
	fmt.Fprintf(&b, "  br label %%loop\n")
	fmt.Fprintf(&b, "loop:\n")
	fmt.Fprintf(&b, "  %%cap = load i64, ptr %%cap_p, align 8\n")
	fmt.Fprintf(&b, "  %%len = load i64, ptr %%len_p, align 8\n")
	fmt.Fprintf(&b, "  %%space = sub i64 %%cap, %%len\n")
	fmt.Fprintf(&b, "  %%needgrow = icmp slt i64 %%space, 1024\n")
	fmt.Fprintf(&b, "  br i1 %%needgrow, label %%grow, label %%doread\n")
	fmt.Fprintf(&b, "grow:\n")
	fmt.Fprintf(&b, "  %%ncap = mul i64 %%cap, 2\n")
	fmt.Fprintf(&b, "  %%oldbuf = load ptr, ptr %%buf_p, align 8\n")
	fmt.Fprintf(&b, "  %%nbuf = call ptr @realloc(ptr %%oldbuf, i64 %%ncap)\n")
	fmt.Fprintf(&b, "  store ptr %%nbuf, ptr %%buf_p, align 8\n")
	fmt.Fprintf(&b, "  store i64 %%ncap, ptr %%cap_p, align 8\n")
	fmt.Fprintf(&b, "  br label %%doread\n")
	fmt.Fprintf(&b, "doread:\n")
	fmt.Fprintf(&b, "  %%cap2 = load i64, ptr %%cap_p, align 8\n")
	fmt.Fprintf(&b, "  %%len2 = load i64, ptr %%len_p, align 8\n")
	fmt.Fprintf(&b, "  %%space2 = sub i64 %%cap2, %%len2\n")
	fmt.Fprintf(&b, "  %%buf2 = load ptr, ptr %%buf_p, align 8\n")
	fmt.Fprintf(&b, "  %%dst = getelementptr i8, ptr %%buf2, i64 %%len2\n")
	fmt.Fprintf(&b, "  %%n = call i64 @fread(ptr %%dst, i64 1, i64 %%space2, ptr %%f)\n")
	fmt.Fprintf(&b, "  %%len3 = add i64 %%len2, %%n\n")
	fmt.Fprintf(&b, "  store i64 %%len3, ptr %%len_p, align 8\n")
	fmt.Fprintf(&b, "  %%isdone = icmp eq i64 %%n, 0\n")
	fmt.Fprintf(&b, "  br i1 %%isdone, label %%finish, label %%loop\n")
	fmt.Fprintf(&b, "finish:\n")
	fmt.Fprintf(&b, "  %%finalbuf = load ptr, ptr %%buf_p, align 8\n")
	fmt.Fprintf(&b, "  %%finallen = load i64, ptr %%len_p, align 8\n")
	fmt.Fprintf(&b, "  %%needed = add i64 %%finallen, 1\n")
	fmt.Fprintf(&b, "  %%trimmed = call ptr @realloc(ptr %%finalbuf, i64 %%needed)\n")
	fmt.Fprintf(&b, "  %%termptr = getelementptr i8, ptr %%trimmed, i64 %%finallen\n")
	fmt.Fprintf(&b, "  store i8 0, ptr %%termptr, align 1\n")
	fmt.Fprintf(&b, "  call i32 @fclose(ptr %%f)\n")
	fmt.Fprintf(&b, "  ret ptr %%trimmed\n")
	fmt.Fprintf(&b, "}")
	e.emitGlobal(b.String())
}

// cpuInfoFieldIndexes bundles CPUInfoType()/CPUTimesType()'s field indices
// and IR type strings once, so both ensureOSCpusLinux and
// ensureOSCpusDarwin build GEPs off the same computed (not hardcoded)
// layout — if either Type's field order ever changes, both functions pick
// it up automatically instead of silently going stale.
type cpuInfoFieldIndexes struct {
	infoStructIR, timesStructIR               string
	infoSize, timesSize                       int64
	modelIdx, speedIdx, timesFieldIdx         int
	userIdx, niceIdx, sysIdx, idleIdx, irqIdx int
}

func newCPUInfoFieldIndexes() cpuInfoFieldIndexes {
	infoTy := CPUInfoType()
	timesTy := CPUTimesType()
	f := cpuInfoFieldIndexes{
		infoStructIR:  infoTy.StructIR(),
		timesStructIR: timesTy.StructIR(),
		infoSize:      infoTy.StructSize(),
		timesSize:     timesTy.StructSize(),
	}
	f.modelIdx, _, _ = infoTy.FieldIndex("model")
	f.speedIdx, _, _ = infoTy.FieldIndex("speed")
	f.timesFieldIdx, _, _ = infoTy.FieldIndex("times")
	f.userIdx, _, _ = timesTy.FieldIndex("user")
	f.niceIdx, _, _ = timesTy.FieldIndex("nice")
	f.sysIdx, _, _ = timesTy.FieldIndex("sys")
	f.idleIdx, _, _ = timesTy.FieldIndex("idle")
	f.irqIdx, _, _ = timesTy.FieldIndex("irq")
	return f
}

// ensureOSCpusLinux declares __kml_os_cpus_linux: the real Linux
// implementation of os.cpus() — sysconf(_SC_NPROCESSORS_ONLN) for the core
// count (constant 84, verified by compiling and running a probe on a real
// Linux box, not recalled from memory), then /proc/cpuinfo (model, speed)
// and /proc/stat (times) parsed via strstr+sscanf. /proc/cpuinfo's
// per-processor blocks are walked in strict file order (documented Linux
// kernel behavior) by repeatedly finding the NEXT "processor\t:" occurrence
// forward from the current block, rather than substring-matching a literal
// "cpuN" marker — "cpu1" is itself a substring of "cpu10"/"cpu11"/…, so a
// naive per-index string search would be ambiguous. /proc/stat's per-core
// lines are walked the same line-position way (skip the first aggregate
// "cpu " line, then read exactly count more lines in order) for the
// identical reason. Field mapping (user/nice/sys/idle skip-iowait/irq) and
// the jiffies-to-milliseconds conversion (* 1000 / sysconf(_SC_CLK_TCK),
// _SC_CLK_TCK constant 2, also verified on real hardware) matches
// libuv/real Node's own os.cpus() on Linux.
func (e *Emitter) ensureOSCpusLinux() {
	if e.usedOSCpusLinux {
		return
	}
	e.usedOSCpusLinux = true
	e.ensureOSReadProcFile()
	e.ensureSysconf()
	e.ensureStrstr()
	e.ensureSscanf()
	e.ensureMalloc()
	e.emitGlobal("declare ptr @strchr(ptr noundef, i32 noundef)")

	f := newCPUInfoFieldIndexes()

	cpuinfoPathPtr := e.internString("/proc/cpuinfo")
	statPathPtr := e.internString("/proc/stat")
	modelNamePtr := e.internString("model name")
	cpuMHzPtr := e.internString("cpu MHz")
	processorMarkerPtr := e.internString("processor\t:")
	modelScanFmtPtr := e.internString(": %[^\n]")
	mhzScanFmtPtr := e.internString(": %lf")
	statScanFmtPtr := e.internString("%*s %lld %lld %lld %lld %lld %lld")
	unknownModelPtr := e.internString("unknown")

	var b strings.Builder
	fmt.Fprintf(&b, "\ndefine {ptr, i64} @__kml_os_cpus_linux() {\n")
	fmt.Fprintf(&b, "entry:\n")
	fmt.Fprintf(&b, "  %%count = call i64 @sysconf(i32 84)\n")
	fmt.Fprintf(&b, "  %%clktck = call i64 @sysconf(i32 2)\n")
	fmt.Fprintf(&b, "  %%cpuinfo = call ptr @__kml_os_read_proc_file(ptr %s)\n", cpuinfoPathPtr)
	fmt.Fprintf(&b, "  %%statbuf = call ptr @__kml_os_read_proc_file(ptr %s)\n", statPathPtr)
	fmt.Fprintf(&b, "  %%outbytes = mul i64 %%count, 8\n")
	fmt.Fprintf(&b, "  %%out = call ptr @malloc(i64 %%outbytes)\n")
	fmt.Fprintf(&b, "  %%first_nl = call ptr @strchr(ptr %%statbuf, i32 10)\n")
	fmt.Fprintf(&b, "  %%stat_line0 = getelementptr i8, ptr %%first_nl, i64 1\n")
	fmt.Fprintf(&b, "  %%block_p = alloca ptr, align 8\n")
	fmt.Fprintf(&b, "  %%line_p = alloca ptr, align 8\n")
	fmt.Fprintf(&b, "  %%i_p = alloca i64, align 8\n")
	fmt.Fprintf(&b, "  store ptr %%cpuinfo, ptr %%block_p, align 8\n")
	fmt.Fprintf(&b, "  store ptr %%stat_line0, ptr %%line_p, align 8\n")
	fmt.Fprintf(&b, "  store i64 0, ptr %%i_p, align 8\n")
	fmt.Fprintf(&b, "  br label %%loop\n")

	fmt.Fprintf(&b, "loop:\n")
	fmt.Fprintf(&b, "  %%i = load i64, ptr %%i_p, align 8\n")
	fmt.Fprintf(&b, "  %%atend = icmp sge i64 %%i, %%count\n")
	fmt.Fprintf(&b, "  br i1 %%atend, label %%done, label %%body\n")

	fmt.Fprintf(&b, "body:\n")
	fmt.Fprintf(&b, "  %%block = load ptr, ptr %%block_p, align 8\n")
	fmt.Fprintf(&b, "  %%line = load ptr, ptr %%line_p, align 8\n")
	fmt.Fprintf(&b, "  %%entry_obj = call ptr @malloc(i64 %d)\n", f.infoSize)
	fmt.Fprintf(&b, "  %%times_obj = call ptr @malloc(i64 %d)\n", f.timesSize)

	// model
	fmt.Fprintf(&b, "  %%model_at = call ptr @strstr(ptr %%block, ptr %s)\n", modelNamePtr)
	fmt.Fprintf(&b, "  %%model_isnull = icmp eq ptr %%model_at, null\n")
	fmt.Fprintf(&b, "  br i1 %%model_isnull, label %%model_fallback, label %%model_found\n")
	fmt.Fprintf(&b, "model_fallback:\n")
	fmt.Fprintf(&b, "  %%model_slot0 = getelementptr %s, ptr %%entry_obj, i32 0, i32 %d\n", f.infoStructIR, f.modelIdx)
	fmt.Fprintf(&b, "  store ptr %s, ptr %%model_slot0, align 8\n", unknownModelPtr)
	fmt.Fprintf(&b, "  br label %%speed\n")
	fmt.Fprintf(&b, "model_found:\n")
	fmt.Fprintf(&b, "  %%model_colon = call ptr @strchr(ptr %%model_at, i32 58)\n")
	fmt.Fprintf(&b, "  %%modelbuf = call ptr @malloc(i64 256)\n")
	fmt.Fprintf(&b, "  %%mrc = call i32 (ptr, ptr, ...) @sscanf(ptr %%model_colon, ptr %s, ptr %%modelbuf)\n", modelScanFmtPtr)
	fmt.Fprintf(&b, "  %%model_slot1 = getelementptr %s, ptr %%entry_obj, i32 0, i32 %d\n", f.infoStructIR, f.modelIdx)
	fmt.Fprintf(&b, "  store ptr %%modelbuf, ptr %%model_slot1, align 8\n")
	fmt.Fprintf(&b, "  br label %%speed\n")

	// speed
	fmt.Fprintf(&b, "speed:\n")
	fmt.Fprintf(&b, "  %%mhz_at = call ptr @strstr(ptr %%block, ptr %s)\n", cpuMHzPtr)
	fmt.Fprintf(&b, "  %%mhz_isnull = icmp eq ptr %%mhz_at, null\n")
	fmt.Fprintf(&b, "  br i1 %%mhz_isnull, label %%speed_fallback, label %%speed_found\n")
	fmt.Fprintf(&b, "speed_fallback:\n")
	fmt.Fprintf(&b, "  %%speed_slot0 = getelementptr %s, ptr %%entry_obj, i32 0, i32 %d\n", f.infoStructIR, f.speedIdx)
	fmt.Fprintf(&b, "  store i64 0, ptr %%speed_slot0, align 8\n")
	fmt.Fprintf(&b, "  br label %%nextblock\n")
	fmt.Fprintf(&b, "speed_found:\n")
	fmt.Fprintf(&b, "  %%mhz_colon = call ptr @strchr(ptr %%mhz_at, i32 58)\n")
	fmt.Fprintf(&b, "  %%mhzval_p = alloca double, align 8\n")
	fmt.Fprintf(&b, "  %%src = call i32 (ptr, ptr, ...) @sscanf(ptr %%mhz_colon, ptr %s, ptr %%mhzval_p)\n", mhzScanFmtPtr)
	fmt.Fprintf(&b, "  %%mhzval = load double, ptr %%mhzval_p, align 8\n")
	fmt.Fprintf(&b, "  %%mhzint = fptosi double %%mhzval to i64\n")
	fmt.Fprintf(&b, "  %%speed_slot1 = getelementptr %s, ptr %%entry_obj, i32 0, i32 %d\n", f.infoStructIR, f.speedIdx)
	fmt.Fprintf(&b, "  store i64 %%mhzint, ptr %%speed_slot1, align 8\n")
	fmt.Fprintf(&b, "  br label %%nextblock\n")

	// advance to next cpuinfo block + parse this stat line
	fmt.Fprintf(&b, "nextblock:\n")
	fmt.Fprintf(&b, "  %%block_next1 = getelementptr i8, ptr %%block, i64 1\n")
	fmt.Fprintf(&b, "  %%next_marker = call ptr @strstr(ptr %%block_next1, ptr %s)\n", processorMarkerPtr)
	fmt.Fprintf(&b, "  store ptr %%next_marker, ptr %%block_p, align 8\n")

	fmt.Fprintf(&b, "  %%user_p = alloca i64, align 8\n")
	fmt.Fprintf(&b, "  %%nice_p = alloca i64, align 8\n")
	fmt.Fprintf(&b, "  %%sys_p = alloca i64, align 8\n")
	fmt.Fprintf(&b, "  %%idle_p = alloca i64, align 8\n")
	fmt.Fprintf(&b, "  %%iowait_p = alloca i64, align 8\n")
	fmt.Fprintf(&b, "  %%irq_p = alloca i64, align 8\n")
	fmt.Fprintf(&b, "  %%src2 = call i32 (ptr, ptr, ...) @sscanf(ptr %%line, ptr %s, ptr %%user_p, ptr %%nice_p, ptr %%sys_p, ptr %%idle_p, ptr %%iowait_p, ptr %%irq_p)\n", statScanFmtPtr)

	fmt.Fprintf(&b, "  %%uv = load i64, ptr %%user_p, align 8\n")
	fmt.Fprintf(&b, "  %%nv = load i64, ptr %%nice_p, align 8\n")
	fmt.Fprintf(&b, "  %%sv = load i64, ptr %%sys_p, align 8\n")
	fmt.Fprintf(&b, "  %%iv = load i64, ptr %%idle_p, align 8\n")
	fmt.Fprintf(&b, "  %%irqv = load i64, ptr %%irq_p, align 8\n")

	fmt.Fprintf(&b, "  %%um = mul i64 %%uv, 1000\n")
	fmt.Fprintf(&b, "  %%ums = sdiv i64 %%um, %%clktck\n")
	fmt.Fprintf(&b, "  %%nm = mul i64 %%nv, 1000\n")
	fmt.Fprintf(&b, "  %%nms = sdiv i64 %%nm, %%clktck\n")
	fmt.Fprintf(&b, "  %%sm = mul i64 %%sv, 1000\n")
	fmt.Fprintf(&b, "  %%sms = sdiv i64 %%sm, %%clktck\n")
	fmt.Fprintf(&b, "  %%im = mul i64 %%iv, 1000\n")
	fmt.Fprintf(&b, "  %%ims = sdiv i64 %%im, %%clktck\n")
	fmt.Fprintf(&b, "  %%irqm = mul i64 %%irqv, 1000\n")
	fmt.Fprintf(&b, "  %%irqms = sdiv i64 %%irqm, %%clktck\n")

	fmt.Fprintf(&b, "  %%tu = getelementptr %s, ptr %%times_obj, i32 0, i32 %d\n", f.timesStructIR, f.userIdx)
	fmt.Fprintf(&b, "  store i64 %%ums, ptr %%tu, align 8\n")
	fmt.Fprintf(&b, "  %%tn = getelementptr %s, ptr %%times_obj, i32 0, i32 %d\n", f.timesStructIR, f.niceIdx)
	fmt.Fprintf(&b, "  store i64 %%nms, ptr %%tn, align 8\n")
	fmt.Fprintf(&b, "  %%ts = getelementptr %s, ptr %%times_obj, i32 0, i32 %d\n", f.timesStructIR, f.sysIdx)
	fmt.Fprintf(&b, "  store i64 %%sms, ptr %%ts, align 8\n")
	fmt.Fprintf(&b, "  %%ti = getelementptr %s, ptr %%times_obj, i32 0, i32 %d\n", f.timesStructIR, f.idleIdx)
	fmt.Fprintf(&b, "  store i64 %%ims, ptr %%ti, align 8\n")
	fmt.Fprintf(&b, "  %%tirq = getelementptr %s, ptr %%times_obj, i32 0, i32 %d\n", f.timesStructIR, f.irqIdx)
	fmt.Fprintf(&b, "  store i64 %%irqms, ptr %%tirq, align 8\n")

	fmt.Fprintf(&b, "  %%times_slot = getelementptr %s, ptr %%entry_obj, i32 0, i32 %d\n", f.infoStructIR, f.timesFieldIdx)
	fmt.Fprintf(&b, "  store ptr %%times_obj, ptr %%times_slot, align 8\n")

	fmt.Fprintf(&b, "  %%next_line_nl = call ptr @strchr(ptr %%line, i32 10)\n")
	fmt.Fprintf(&b, "  %%next_line = getelementptr i8, ptr %%next_line_nl, i64 1\n")
	fmt.Fprintf(&b, "  store ptr %%next_line, ptr %%line_p, align 8\n")

	fmt.Fprintf(&b, "  %%out_slot = getelementptr ptr, ptr %%out, i64 %%i\n")
	fmt.Fprintf(&b, "  store ptr %%entry_obj, ptr %%out_slot, align 8\n")

	fmt.Fprintf(&b, "  %%i_next = add i64 %%i, 1\n")
	fmt.Fprintf(&b, "  store i64 %%i_next, ptr %%i_p, align 8\n")
	fmt.Fprintf(&b, "  br label %%loop\n")

	fmt.Fprintf(&b, "done:\n")
	fmt.Fprintf(&b, "  %%r0 = insertvalue {ptr, i64} undef, ptr %%out, 0\n")
	fmt.Fprintf(&b, "  %%r1 = insertvalue {ptr, i64} %%r0, i64 %%count, 1\n")
	fmt.Fprintf(&b, "  ret {ptr, i64} %%r1\n")
	fmt.Fprintf(&b, "}")
	e.emitGlobal(b.String())
}

// ensureOSCpusDarwin declares __kml_os_cpus_darwin — UNVERIFIED on real
// hardware, see docs/tdd/TDD-00024.md and docs/adr/ADR-00090.md. model/speed
// come from sysctlbyname (machdep.cpu.brand_string / hw.cpufrequency) —
// identical for every logical core, matching real Node's own Darwin
// behavior; hw.cpufrequency is known to return 0/fail on Apple Silicon
// (Apple removed the fixed-clock-speed model starting with M1), which is
// also real Node's own documented behavior on M-series Macs, not a gap to
// work around. times come from
// host_processor_info(PROCESSOR_CPU_LOAD_INFO=2) — each core's 4
// natural_t ticks are CPU_STATE_USER/SYSTEM/IDLE/NICE in that order,
// per <mach/processor_info.h>; irq has no Darwin equivalent and is always
// 0. Tick rate assumed 100Hz (10ms/tick), matching libuv's own hardcoded
// Darwin constant (Darwin has no sysconf(_SC_CLK_TCK) to read this from
// instead).
func (e *Emitter) ensureOSCpusDarwin() {
	if e.usedOSCpusDarwin {
		return
	}
	e.usedOSCpusDarwin = true
	e.ensureMachVM()
	e.ensureSysctlbyname()
	e.ensureMalloc()

	f := newCPUInfoFieldIndexes()
	brandKeyPtr := e.internString("machdep.cpu.brand_string")
	freqKeyPtr := e.internString("hw.cpufrequency")

	var b strings.Builder
	fmt.Fprintf(&b, "\ndefine {ptr, i64} @__kml_os_cpus_darwin() {\n")
	fmt.Fprintf(&b, "entry:\n")
	fmt.Fprintf(&b, "  %%modelbuf = call ptr @malloc(i64 256)\n")
	fmt.Fprintf(&b, "  %%modelsize_p = alloca i64, align 8\n")
	fmt.Fprintf(&b, "  store i64 256, ptr %%modelsize_p, align 8\n")
	fmt.Fprintf(&b, "  %%mrc = call i32 @sysctlbyname(ptr %s, ptr %%modelbuf, ptr %%modelsize_p, ptr null, i64 0)\n", brandKeyPtr)

	fmt.Fprintf(&b, "  %%freq_p = alloca i64, align 8\n")
	fmt.Fprintf(&b, "  store i64 0, ptr %%freq_p, align 8\n")
	fmt.Fprintf(&b, "  %%freqsize_p = alloca i64, align 8\n")
	fmt.Fprintf(&b, "  store i64 8, ptr %%freqsize_p, align 8\n")
	fmt.Fprintf(&b, "  %%frc = call i32 @sysctlbyname(ptr %s, ptr %%freq_p, ptr %%freqsize_p, ptr null, i64 0)\n", freqKeyPtr)
	fmt.Fprintf(&b, "  %%freqhz = load i64, ptr %%freq_p, align 8\n")
	fmt.Fprintf(&b, "  %%freqmhz = sdiv i64 %%freqhz, 1000000\n")

	fmt.Fprintf(&b, "  %%host = call i32 @mach_host_self()\n")
	fmt.Fprintf(&b, "  %%count_out = alloca i32, align 4\n")
	fmt.Fprintf(&b, "  %%info_out = alloca ptr, align 8\n")
	fmt.Fprintf(&b, "  %%infocnt_out = alloca i32, align 4\n")
	fmt.Fprintf(&b, "  %%hs = call i32 @host_processor_info(i32 %%host, i32 2, ptr %%count_out, ptr %%info_out, ptr %%infocnt_out)\n")
	fmt.Fprintf(&b, "  %%ncores32 = load i32, ptr %%count_out, align 4\n")
	fmt.Fprintf(&b, "  %%ncores = zext i32 %%ncores32 to i64\n")
	fmt.Fprintf(&b, "  %%infoarr = load ptr, ptr %%info_out, align 8\n")

	fmt.Fprintf(&b, "  %%outbytes = mul i64 %%ncores, 8\n")
	fmt.Fprintf(&b, "  %%out = call ptr @malloc(i64 %%outbytes)\n")
	fmt.Fprintf(&b, "  %%i_p = alloca i64, align 8\n")
	fmt.Fprintf(&b, "  store i64 0, ptr %%i_p, align 8\n")
	fmt.Fprintf(&b, "  br label %%loop\n")

	fmt.Fprintf(&b, "loop:\n")
	fmt.Fprintf(&b, "  %%i = load i64, ptr %%i_p, align 8\n")
	fmt.Fprintf(&b, "  %%atend = icmp sge i64 %%i, %%ncores\n")
	fmt.Fprintf(&b, "  br i1 %%atend, label %%done, label %%body\n")

	fmt.Fprintf(&b, "body:\n")
	fmt.Fprintf(&b, "  %%entry_obj = call ptr @malloc(i64 %d)\n", f.infoSize)
	fmt.Fprintf(&b, "  %%times_obj = call ptr @malloc(i64 %d)\n", f.timesSize)

	fmt.Fprintf(&b, "  %%model_slot = getelementptr %s, ptr %%entry_obj, i32 0, i32 %d\n", f.infoStructIR, f.modelIdx)
	fmt.Fprintf(&b, "  store ptr %%modelbuf, ptr %%model_slot, align 8\n")
	fmt.Fprintf(&b, "  %%speed_slot = getelementptr %s, ptr %%entry_obj, i32 0, i32 %d\n", f.infoStructIR, f.speedIdx)
	fmt.Fprintf(&b, "  store i64 %%freqmhz, ptr %%speed_slot, align 8\n")

	fmt.Fprintf(&b, "  %%base = mul i64 %%i, 4\n")
	fmt.Fprintf(&b, "  %%useridx = add i64 %%base, 0\n")
	fmt.Fprintf(&b, "  %%userp = getelementptr i32, ptr %%infoarr, i64 %%useridx\n")
	fmt.Fprintf(&b, "  %%userval32 = load i32, ptr %%userp, align 4\n")
	fmt.Fprintf(&b, "  %%userval = zext i32 %%userval32 to i64\n")
	fmt.Fprintf(&b, "  %%userms = mul i64 %%userval, 10\n")

	fmt.Fprintf(&b, "  %%sysidx = add i64 %%base, 1\n")
	fmt.Fprintf(&b, "  %%sysp = getelementptr i32, ptr %%infoarr, i64 %%sysidx\n")
	fmt.Fprintf(&b, "  %%sysval32 = load i32, ptr %%sysp, align 4\n")
	fmt.Fprintf(&b, "  %%sysval = zext i32 %%sysval32 to i64\n")
	fmt.Fprintf(&b, "  %%sysms = mul i64 %%sysval, 10\n")

	fmt.Fprintf(&b, "  %%idleidx = add i64 %%base, 2\n")
	fmt.Fprintf(&b, "  %%idlep = getelementptr i32, ptr %%infoarr, i64 %%idleidx\n")
	fmt.Fprintf(&b, "  %%idleval32 = load i32, ptr %%idlep, align 4\n")
	fmt.Fprintf(&b, "  %%idleval = zext i32 %%idleval32 to i64\n")
	fmt.Fprintf(&b, "  %%idlems = mul i64 %%idleval, 10\n")

	fmt.Fprintf(&b, "  %%niceidx = add i64 %%base, 3\n")
	fmt.Fprintf(&b, "  %%nicep = getelementptr i32, ptr %%infoarr, i64 %%niceidx\n")
	fmt.Fprintf(&b, "  %%niceval32 = load i32, ptr %%nicep, align 4\n")
	fmt.Fprintf(&b, "  %%niceval = zext i32 %%niceval32 to i64\n")
	fmt.Fprintf(&b, "  %%nicems = mul i64 %%niceval, 10\n")

	fmt.Fprintf(&b, "  %%tu = getelementptr %s, ptr %%times_obj, i32 0, i32 %d\n", f.timesStructIR, f.userIdx)
	fmt.Fprintf(&b, "  store i64 %%userms, ptr %%tu, align 8\n")
	fmt.Fprintf(&b, "  %%tn = getelementptr %s, ptr %%times_obj, i32 0, i32 %d\n", f.timesStructIR, f.niceIdx)
	fmt.Fprintf(&b, "  store i64 %%nicems, ptr %%tn, align 8\n")
	fmt.Fprintf(&b, "  %%ts = getelementptr %s, ptr %%times_obj, i32 0, i32 %d\n", f.timesStructIR, f.sysIdx)
	fmt.Fprintf(&b, "  store i64 %%sysms, ptr %%ts, align 8\n")
	fmt.Fprintf(&b, "  %%ti = getelementptr %s, ptr %%times_obj, i32 0, i32 %d\n", f.timesStructIR, f.idleIdx)
	fmt.Fprintf(&b, "  store i64 %%idlems, ptr %%ti, align 8\n")
	fmt.Fprintf(&b, "  %%tirq = getelementptr %s, ptr %%times_obj, i32 0, i32 %d\n", f.timesStructIR, f.irqIdx)
	fmt.Fprintf(&b, "  store i64 0, ptr %%tirq, align 8\n")

	fmt.Fprintf(&b, "  %%times_slot = getelementptr %s, ptr %%entry_obj, i32 0, i32 %d\n", f.infoStructIR, f.timesFieldIdx)
	fmt.Fprintf(&b, "  store ptr %%times_obj, ptr %%times_slot, align 8\n")

	fmt.Fprintf(&b, "  %%out_slot = getelementptr ptr, ptr %%out, i64 %%i\n")
	fmt.Fprintf(&b, "  store ptr %%entry_obj, ptr %%out_slot, align 8\n")

	fmt.Fprintf(&b, "  %%i_next = add i64 %%i, 1\n")
	fmt.Fprintf(&b, "  store i64 %%i_next, ptr %%i_p, align 8\n")
	fmt.Fprintf(&b, "  br label %%loop\n")

	fmt.Fprintf(&b, "done:\n")
	fmt.Fprintf(&b, "  %%r0 = insertvalue {ptr, i64} undef, ptr %%out, 0\n")
	fmt.Fprintf(&b, "  %%r1 = insertvalue {ptr, i64} %%r0, i64 %%ncores, 1\n")
	fmt.Fprintf(&b, "  ret {ptr, i64} %%r1\n")
	fmt.Fprintf(&b, "}")
	e.emitGlobal(b.String())
}
