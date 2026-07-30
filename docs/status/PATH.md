# path

> Part of the [Implementation Status](README.md) index. Node's `path` module — portable filesystem path manipulation. Not tracked anywhere until now, despite being directly relevant to this project's own stated CLI-application priority (see [CLAUDE.md](../../CLAUDE.md)'s Project direction) — almost any real file-handling CLI script needs to join/resolve/split paths, and there is currently no way to do that portably at all (a program would have to hand-roll string concatenation with a hardcoded `/`, which breaks on Windows and is exactly the kind of bug this module exists to prevent).

**Coverage**: 0% (0/8) — not implemented, confirmed zero references anywhere in `codegen/llvm/`.

**Caveats**: Nothing here exists yet. Given the CLI-priority tiebreaker in [CLAUDE.md](../../CLAUDE.md), `path.join`/`.resolve`/`.dirname`/`.basename`/`.extname` are the highest-value subset — they're pure string/path-segment manipulation (no new C dependency, no event loop involvement), a similar effort profile to the `fs.*` functions already built.

| API | Status | Notes |
|---|---|---|
| `path.join(...segments)` | ❌ | Joins path segments with the platform separator, normalizing `.`/`..` |
| `path.resolve(...segments)` | ❌ | Resolves to an absolute path, right-to-left, falling back to `process.cwd()` |
| `path.dirname(p)` | ❌ | Directory portion of a path |
| `path.basename(p, ext?)` | ❌ | Final path segment, optionally with a suffix stripped |
| `path.extname(p)` | ❌ | File extension including the leading `.` |
| `path.parse(p)` / `path.format(obj)` | ❌ | Structured decompose/recompose of a path into `{root, dir, base, ext, name}` |
| `path.isAbsolute(p)` | ❌ | Platform-aware absolute-path check |
| `path.sep` / `path.delimiter` | ❌ | Platform-specific separator constants (`/` vs `\`, `:` vs `;`) — this compiler doesn't cross-compile ([process.platform](PROCESS-CLI.md) is already a `runtime.GOOS`-baked compile-time constant, the same approach would apply here) |
