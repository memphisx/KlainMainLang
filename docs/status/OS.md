# os

> Part of the [Implementation Status](README.md) index. Node's `os` module — operating-system information. See [TDD-00024](../tdd/TDD-00024.md)/[ADR-00090](../adr/ADR-00090.md).

**Coverage**: 100% (7/7) — done. Fully verified on Linux (this project's own dev sandbox); the Darwin-specific implementation paths (`os.freemem()`, `os.cpus()`'s per-core `times`) are written against documented Mach/`sysctlbyname` APIs but have not been compiled or run on real hardware — see Known Limitations.

**Strict Coverage**: 5/7, ~71% — a row only counts here if it was independently repro-verified with zero known caveats or bugs, of any severity. See the 2026-08-11 audit ([ADR-00166](../adr/ADR-00166.md)) that produced this number; no false ✅ claims found on this page — the two excluded rows were already honestly caveated (Darwin-unverified) before the audit.

| API | Status | Notes |
|---|---|---|
| `os.platform()` | ✅ | Reuses `process.platform`'s existing `runtime.GOOS`-constant mechanism |
| `os.homedir()` | ✅ | `getenv("HOME")`; throws a catchable Error if unset (matches real Node) |
| `os.tmpdir()` | ✅ | `getenv("TMPDIR")`, falling back to `"/tmp"` — never throws |
| `os.hostname()` | ✅ | POSIX `gethostname()` |
| `os.cpus()` | ✅ | Real per-core `{model, speed, times: {user,nice,sys,idle,irq}}` — Linux via `/proc/cpuinfo`/`/proc/stat` parsing (verified), Darwin via `sysctlbyname`/Mach `host_processor_info` (unverified, see below) |
| `os.totalmem()` / `os.freemem()` | ✅ | `totalmem()` and Linux `freemem()` verified; Darwin `freemem()` unverified (Mach `host_statistics`, see below) |
| `os.EOL` | ✅ | Always `"\n"` — this compiler is POSIX-only (no Windows target), so there's no real `"\r\n"` case |

## Known Limitations

- **Darwin `os.freemem()` and `os.cpus()`'s `times` field are unverified on real hardware.** Both are written against publicly documented, decades-stable Mach ABI shapes (`vm_statistics_data_t`, `host_processor_info`'s per-core tick array — see [TDD-00024](../tdd/TDD-00024.md) for the exact layout reasoning), but this project's dev sandbox is Linux-only, so neither path has ever been compiled or executed. Expected to need adjustment once tested on real Apple Silicon hardware; not a silently-assumed-correct gap.
- **Darwin `os.cpus()`'s `speed` field returns 0 on Apple Silicon** — `sysctlbyname("hw.cpufrequency")` has no answer on M-series Macs (Apple removed the fixed-clock-speed model starting with M1). This matches real Node's own documented behavior on M-series Macs, not a defect.
- **Linux `os.cpus()`'s `speed` field reflects the CPU's current/scaled frequency** (from `/proc/cpuinfo`'s `cpu MHz`), not a rated base/max clock — matches what libuv/real Node itself reports on Linux.
- **`os.cpus()`'s `times.irq` is always 0 on Darwin** — Mach's per-core tick array has no `irq` bucket (only user/system/idle/nice).
