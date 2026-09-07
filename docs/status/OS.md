<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/os.json; edit the JSON, then run `make status`. -->

# os

> Part of the [Implementation Status](README.md) index. Node's `os` module — operating-system information. See [TDD-00024](../tdd/TDD-00024.md)/[ADR-00090](../adr/ADR-00090.md).

**Coverage**: 7/7 (100%) · **Strict Coverage**: 5/7 (~71%).

Format: [Status page format](README.md#status-page-format).

| API | Status | Caveats | Notes |
|---|---|---|---|
| `os.platform()` | ✅ | | • Reuses `process.platform`'s existing `runtime.GOOS`-constant mechanism |
| `os.homedir()` | ✅ | • Throws when `$HOME` is unset (`os.homedir()` errors) instead of Node's passwd-database fallback (`getpwuid`), which returns the home dir without throwing. | • `getenv("HOME")`; throws a catchable Error if unset (matches real Node) |
| `os.tmpdir()` | ✅ | • On POSIX hosts it consults only `$TMPDIR` (not `$TMP`/`$TEMP`) and keeps a trailing slash — `TMPDIR=/foo/` yields `'/foo/'`, where Node returns `'/foo'` (the Windows side strips, [ADR-00739](../adr/ADR-00739.md)). | • `getenv("TMPDIR")`, falling back to `"/tmp"` — never throws<br>• On Windows: `TEMP`, then `TMP`, then `<SystemRoot|windir>\temp`, with one trailing backslash stripped unless it names a drive root — Node's exact algorithm ([ADR-00739](../adr/ADR-00739.md)) |
| `os.hostname()` | ✅ | | • POSIX `gethostname()` |
| `os.cpus()` | ✅ | | • Real per-core `{model, speed, times: {user,nice,sys,idle,irq}}` — Linux via `/proc/cpuinfo`/`/proc/stat` parsing, Darwin via `sysctlbyname`/Mach `host_processor_info` — both verified (Darwin on Apple Silicon M4 Pro: `model` = `"Apple M4 Pro"`, live tick counters)<br>• Darwin `speed`: `hw.cpufrequency` is unavailable on Apple Silicon (M-series removed the fixed-clock model), so a fixed `2400` MHz nominal is reported there — byte-for-byte what real Node/libuv reports on the same hardware; an Intel Mac's real value flows through unchanged ([ADR-00569](../adr/ADR-00569.md))<br>• Darwin `times.irq` is always `0` — Mach's per-core tick array has no `irq` bucket (only user/system/idle/nice); real Node reports `irq: 0` on Darwin too, so this is parity, not a gap<br>• Linux `speed` is cpufreq's `scaling_max_freq` (`/sys/devices/system/cpu/cpuN/cpufreq/`, kHz → MHz) and `0` where cpufreq is absent — VMs and containers included — exactly what Node 22's libuv reports; `/proc/cpuinfo`'s `cpu MHz` is no longer consulted. On aarch64, with no `model name` line, `model` comes from the `CPU part` code through libuv's ARM part table (`Neoverse-N1`, `Cortex-A72`, …), else `unknown` ([ADR-00733](../adr/ADR-00733.md)) |
| `os.totalmem()` / `os.freemem()` | ✅ | | • Verified on Linux and on Apple Silicon M4 Pro — `totalmem()` matches `sysctl hw.memsize`; Darwin `freemem()` (Mach `host_statistics`) returns a live free-page figure |
| `os.EOL` | ✅ | | • Always `"\n"` — this compiler is POSIX-only (no Windows target), so there's no real `"\r\n"` case |
