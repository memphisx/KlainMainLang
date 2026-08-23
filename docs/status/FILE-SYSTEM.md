# File System (fs)

> Part of the [Implementation Status](README.md) index. Node-`fs`-shaped synchronous file I/O for reading/writing config, data, and logs — not `File`/`FileReader`/`FileSystemFileHandle` (those model browser sandbox/permission concepts that don't exist for a native CLI/microservice program with direct filesystem access).

**Coverage**: 11/13 (~85%) · **Strict Coverage**: 7/13 (~54%).

Format: [Status page format](README.md#status-page-format).

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `fs.readFileSync(path)` | ✅ | • Text-only by design — a file with embedded null bytes reads back shorter than its real size (`.length` is `strlen`-based); use `fs.readFileSyncBytes(path)` for a binary file | • Reads the whole file into a string<br>• Throws a catchable `Error` (built from `strerror(errno)`) if the file can't be opened<br>• See [ADR-00023](../adr/ADR-00023.md) |
| `fs.readFileSyncBytes(path)` | ✅ | | • Binary-safe sibling of `readFileSync`: returns a `Uint8Array` with the file's exact byte count (no `strlen` involved), so a file with an embedded null byte reads back whole<br>• No `Buffer` class — this compiler has none; plain `ArrayBuffer`/TypedArrays instead<br>• See [ADR-00094](../adr/ADR-00094.md) |
| `fs.writeFileSync(path, data)` | ✅ | | • Creates or truncates the file with `data`. Throws on failure<br>• `data` may be a string (the original, `strlen`-based path, unchanged) or an `ArrayBuffer`/TypedArray (binary-safe, writes the real byte count via `fwrite` directly — [ADR-00094](../adr/ADR-00094.md)), dispatched on `data`'s inferred type |
| `fs.appendFileSync(path, data)` | ✅ | | • Like `writeFileSync`, but appends instead of truncating (creates the file if it doesn't exist). Throws on failure<br>• Same string/`ArrayBuffer`/TypedArray dispatch as `writeFileSync` |
| `fs.existsSync(path)` | ✅ | | • Plain existence check via POSIX `access()`<br>• Deliberately does **not** throw for a missing path — matches real Node's own `existsSync`, one of the few `fs` functions that reports "doesn't exist" as `false` rather than an error |
| `fs.unlinkSync(path)` | ✅ | | • Deletes a file. Throws on failure |
| `fs.mkdirSync(path)` | ✅ | • No `{recursive: true}` option — throws (e.g. `EEXIST`) if the path already exists or a parent directory is missing | • Creates a directory via POSIX `mkdir()`, mode `0777` reduced by the process umask as usual<br>• See [ADR-00027](../adr/ADR-00027.md) |
| `fs.rmdirSync(path)` | ✅ | • No recursive-delete option, matching `mkdirSync`'s lack of one | • Removes an *empty* directory via POSIX `rmdir()` — deliberately directory-only (fails on a plain file, unlike `remove()`/`unlinkSync`)<br>• See [ADR-00027](../adr/ADR-00027.md) |
| `fs.readdirSync(path)` | ✅ | | • Lists a directory's entries (excluding `.`/`..`) as a `string[]`, in whatever order the OS's own `readdir()` returns them — no ordering guarantee, matching real Node<br>• Built from `struct dirent`'s `d_name` field at a `runtime.GOOS`-conditional byte offset, independently verified by a compiled `offsetof` probe on both Darwin and (via Docker, see [ADR-00051](../adr/ADR-00051.md)) x86-64 Linux<br>• See [ADR-00027](../adr/ADR-00027.md) |
| `fs.renameSync(oldPath, newPath)` | ✅ | | • Renames/moves a file via POSIX `rename()`. Throws on failure<br>• See [ADR-00027](../adr/ADR-00027.md) |
| `fs.copyFileSync(src, dest)` | ✅ | • Still inherits `readFileSync`'s text-only limitation — a source file with embedded null bytes copies back shorter than its real size; not rewritten to use the binary-safe `readFileSyncBytes`/`writeFileSync(path, ArrayBuffer)` path ([ADR-00094](../adr/ADR-00094.md)), a real, separate follow-up if a binary-safe copy is ever needed | • Composes the existing string-based `readFileSync`/`writeFileSync` helpers — no new C-level I/O code<br>• See [ADR-00027](../adr/ADR-00027.md) |
| Async variants (`fs.readFile`, callback/Promise-based) | ❌ | | • Everything here is synchronous and blocking by design; the event loop + streams exist ([TDD-00097](../tdd/TDD-00097.md)), so non-blocking variants (and `fs.createReadStream`/`createWriteStream`) are implementable when prioritized |
| `File` / `FileReader` / `FileSystemFileHandle` (browser-flavored File API) | ❌ | | • Not planned — these model browser concepts this compiler has no equivalent for |
