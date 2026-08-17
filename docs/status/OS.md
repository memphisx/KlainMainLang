# os

> Part of the [Implementation Status](README.md) index. Node's `os` module — operating-system information. See [TDD-00024](../tdd/TDD-00024.md)/[ADR-00090](../adr/ADR-00090.md).

**Coverage**: 100% (7/7) — done. Verified on Linux and on Apple Silicon (M4 Pro, darwin/arm64): the Darwin-specific paths (`os.freemem()`, `os.cpus()`'s per-core `times`) return correct values on real hardware — `totalmem` matches `hw.memsize`, `cpus().length` matches `hw.ncpu`, `model` reads `"Apple M4 Pro"`, and the tick counters are live. The only remaining Darwin specifics are permanent, documented behaviors (`speed` is 0 on M-series, no `irq` tick bucket) — see Known Limitations.

**Strict Coverage**: 6/7, ~86% — a row only counts here if it was independently repro-verified with zero known caveats or bugs, of any severity. The 2026-08-11 audit ([ADR-00166](../adr/ADR-00166.md)) recorded 5/7 with `os.cpus()` and the memory row both excluded as Darwin-unverified; the memory row is now verified on Apple Silicon, leaving only `os.cpus()` excluded (its `speed`/`irq` caveats below).

| API | Status | Notes |
|---|---|---|
| `os.platform()` | ✅ | Reuses `process.platform`'s existing `runtime.GOOS`-constant mechanism |
| `os.homedir()` | ✅ | `getenv("HOME")`; throws a catchable Error if unset (matches real Node) |
| `os.tmpdir()` | ✅ | `getenv("TMPDIR")`, falling back to `"/tmp"` — never throws |
| `os.hostname()` | ✅ | POSIX `gethostname()` |
| `os.cpus()` | ✅ | Real per-core `{model, speed, times: {user,nice,sys,idle,irq}}` — Linux via `/proc/cpuinfo`/`/proc/stat` parsing, Darwin via `sysctlbyname`/Mach `host_processor_info` — both verified (Darwin on Apple Silicon M4 Pro: `model` = `"Apple M4 Pro"`, live tick counters; `speed`/`irq` caveats below) |
| `os.totalmem()` / `os.freemem()` | ✅ | Verified on Linux and on Apple Silicon M4 Pro — `totalmem()` matches `sysctl hw.memsize`; Darwin `freemem()` (Mach `host_statistics`) returns a live free-page figure |
| `os.EOL` | ✅ | Always `"\n"` — this compiler is POSIX-only (no Windows target), so there's no real `"\r\n"` case |

## Known Limitations

- **Darwin `os.cpus()`'s `speed` field returns 0 on Apple Silicon** — `sysctlbyname("hw.cpufrequency")` has no answer on M-series Macs (Apple removed the fixed-clock-speed model starting with M1). This matches real Node's own documented behavior on M-series Macs, not a defect.
- **Linux `os.cpus()`'s `speed` field reflects the CPU's current/scaled frequency** (from `/proc/cpuinfo`'s `cpu MHz`), not a rated base/max clock — matches what libuv/real Node itself reports on Linux.
- **`os.cpus()`'s `times.irq` is always 0 on Darwin** — Mach's per-core tick array has no `irq` bucket (only user/system/idle/nice).
