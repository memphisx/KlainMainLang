# File System (fs)

> Part of the [Implementation Status](README.md) index. Node-`fs`-shaped synchronous file I/O for reading/writing config, data, and logs — not `File`/`FileReader`/`FileSystemFileHandle` (those model browser sandbox/permission concepts that don't exist for a native CLI/microservice program with direct filesystem access).

**Coverage**: ~83% (10/12).

**Caveats**: Everything here is synchronous and blocking (no async variants — matches this compiler's original lack of an event loop; not revisited since). `readFileSync`/`copyFileSync` are text-only — a file with embedded null bytes reads back shorter than its real size (`.length` is `strlen`-based), the same limitation `fetch`'s response bodies have (see [NETWORKING.md](NETWORKING.md)'s Known Limitations).

| API | Status | Notes |
|---|---|---|
| `fs.readFileSync(path)` | ✅ | Reads the whole file into a string. Throws a catchable `Error` (built from `strerror(errno)`) if the file can't be opened. Text-only — a file with embedded null bytes reads back shorter than its real size, the same limitation `fetch`'s response bodies have (`.length` is `strlen`-based). See [ADR-00023](../adr/ADR-00023.md). |
| `fs.writeFileSync(path, data)` | ✅ | Creates or truncates the file with `data`. Throws on failure. |
| `fs.appendFileSync(path, data)` | ✅ | Like `writeFileSync`, but appends instead of truncating (creates the file if it doesn't exist). Throws on failure. |
| `fs.existsSync(path)` | ✅ | Plain existence check via POSIX `access()`. Deliberately does **not** throw for a missing path — matches real Node's own `existsSync`, one of the few `fs` functions that reports "doesn't exist" as `false` rather than an error. |
| `fs.unlinkSync(path)` | ✅ | Deletes a file. Throws on failure. |
| `fs.mkdirSync(path)` | ✅ | Creates a directory via POSIX `mkdir()`, mode `0777` reduced by the process umask as usual. No `{recursive: true}` option — throws (e.g. `EEXIST`) if the path already exists or a parent directory is missing. See [ADR-00027](../adr/ADR-00027.md). |
| `fs.rmdirSync(path)` | ✅ | Removes an *empty* directory via POSIX `rmdir()` — deliberately directory-only (fails on a plain file, unlike `remove()`/`unlinkSync`). No recursive-delete option, matching `mkdirSync`'s lack of one. See [ADR-00027](../adr/ADR-00027.md). |
| `fs.readdirSync(path)` | ✅ | Lists a directory's entries (excluding `.`/`..`) as a `string[]`, in whatever order the OS's own `readdir()` returns them — no ordering guarantee, matching real Node. Built from `struct dirent`'s `d_name` field at a `runtime.GOOS`-conditional byte offset, independently verified by a compiled `offsetof` probe on both Darwin and (via Docker, see [ADR-00051](../adr/ADR-00051.md)) x86-64 Linux. See [ADR-00027](../adr/ADR-00027.md). |
| `fs.renameSync(oldPath, newPath)` | ✅ | Renames/moves a file via POSIX `rename()`. Throws on failure. See [ADR-00027](../adr/ADR-00027.md). |
| `fs.copyFileSync(src, dest)` | ✅ | Composes the existing `readFileSync`/`writeFileSync` helpers — no new C-level I/O code. Inherits `readFileSync`'s text-only limitation (a source file with embedded null bytes copies back shorter than its real size). See [ADR-00027](../adr/ADR-00027.md). |
| Async variants (`fs.readFile`, callback/Promise-based) | ❌ | Everything here is synchronous and blocking, matching this compiler's lack of an event loop — no non-blocking variant exists to offer |
| `File` / `FileReader` / `FileSystemFileHandle` (browser-flavored File API) | ❌ | Not planned — these model browser concepts this compiler has no equivalent for |
