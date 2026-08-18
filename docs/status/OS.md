# os

> Part of the [Implementation Status](README.md) index. Node's `os` module — operating-system information. See [TDD-00024](../tdd/TDD-00024.md)/[ADR-00090](../adr/ADR-00090.md).

**Coverage**: 7/7 (100%) · **Strict Coverage**: 6/7 (~86%).

This page follows the shared status-page format ([Status page format](README.md#status-page-format)): **Status** is a bare ✅/❌; **Caveats** lists behavioral divergences from real JS/TS (a non-empty Caveats cell is what excludes an otherwise-✅ row from Strict Coverage); **Notes** carries implementation/representation detail only. One table per index category; each category's figures above derive from its table below.

| API | Status | Caveats | Notes |
|---|---|---|---|
| `os.platform()` | ✅ | | • Reuses `process.platform`'s existing `runtime.GOOS`-constant mechanism |
| `os.homedir()` | ✅ | | • `getenv("HOME")`; throws a catchable Error if unset (matches real Node) |
| `os.tmpdir()` | ✅ | | • `getenv("TMPDIR")`, falling back to `"/tmp"` — never throws |
| `os.hostname()` | ✅ | | • POSIX `gethostname()` |
| `os.cpus()` | ✅ | • Darwin `speed` is 0 on Apple Silicon — `sysctlbyname("hw.cpufrequency")` has no answer on M-series (Apple removed the fixed-clock model from M1 on); matches real Node's documented M-series behavior, not a defect<br>• Darwin `times.irq` is always 0 — Mach's per-core tick array has no `irq` bucket (only user/system/idle/nice) | • Real per-core `{model, speed, times: {user,nice,sys,idle,irq}}` — Linux via `/proc/cpuinfo`/`/proc/stat` parsing, Darwin via `sysctlbyname`/Mach `host_processor_info` — both verified (Darwin on Apple Silicon M4 Pro: `model` = `"Apple M4 Pro"`, live tick counters)<br>• Linux `speed` reflects the CPU's current/scaled frequency (from `/proc/cpuinfo`'s `cpu MHz`), not a rated base/max clock — matches what libuv/real Node reports on Linux |
| `os.totalmem()` / `os.freemem()` | ✅ | | • Verified on Linux and on Apple Silicon M4 Pro — `totalmem()` matches `sysctl hw.memsize`; Darwin `freemem()` (Mach `host_statistics`) returns a live free-page figure |
| `os.EOL` | ✅ | | • Always `"\n"` — this compiler is POSIX-only (no Windows target), so there's no real `"\r\n"` case |
