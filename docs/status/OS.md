# os

> Part of the [Implementation Status](README.md) index. Node's `os` module — operating-system information. Not tracked anywhere until now; only `process.platform` (a compile-time constant, see [PROCESS-CLI.md](PROCESS-CLI.md)) currently covers any of this ground.

**Coverage**: 0% (0/7) — not implemented, confirmed zero references anywhere in `codegen/llvm/`.

**Caveats**: `os.platform()` would be redundant with the already-shipped `process.platform` (same `runtime.GOOS`-constant approach). `os.homedir()`/`.tmpdir()` are the most immediately useful pair for CLI tools (config file locations, scratch files) and are thin POSIX wrappers (`getenv("HOME")`, `getenv("TMPDIR")` falling back to `/tmp`) — similarly cheap to the existing `process.cwd()`/`.chdir()`.

| API | Status | Notes |
|---|---|---|
| `os.platform()` | ❌ | Would duplicate `process.platform` — see [PROCESS-CLI.md](PROCESS-CLI.md) |
| `os.homedir()` | ❌ | User's home directory |
| `os.tmpdir()` | ❌ | Default temp-file directory |
| `os.hostname()` | ❌ | Machine hostname, via POSIX `gethostname()` |
| `os.cpus()` | ❌ | Per-core CPU info; not obviously useful without a threading model ([Worker](CONCURRENCY-WORKERS.md) doesn't exist yet either) |
| `os.totalmem()` / `os.freemem()` | ❌ | System memory stats |
| `os.EOL` | ❌ | Platform newline constant (`\n` vs `\r\n`) — same compile-time-constant shape as `process.platform` |
