# path

> Part of the [Implementation Status](README.md) index. Node's `path` module — portable filesystem path manipulation. Import-gated (`import path from 'path'` or `import { join } from 'path'`) — see [TDD-00049](../tdd/TDD-00049.md)/[ADR-00141](../adr/ADR-00141.md)/[ADR-00142](../adr/ADR-00142.md).

**Coverage**: 8/8 (100%) · **Strict Coverage**: 7/8 (~88%).

This page follows the shared status-page format ([Status page format](README.md#status-page-format)): **Status** is a bare ✅/❌; **Caveats** lists behavioral divergences from real JS/TS (a non-empty Caveats cell is what excludes an otherwise-✅ row from Strict Coverage); **Notes** carries implementation/representation detail only. One table per index category; each category's figures above derive from its table below.

| API | Status | Caveats | Notes |
|---|---|---|---|
| `path.join(...segments)` | ✅ | | • Joins with `/`, then normalizes (collapses repeated slashes, drops `.` segments, resolves `..` against segments already seen in the same call) |
| `path.resolve(...segments)` | ✅ | | • Absolute-always: starts from `process.cwd()`, walks segments left to right, any segment starting with `/` resets the accumulator (discarding everything before it, including cwd) — equivalent to real Node's right-to-left "stop at the last absolute segment" algorithm |
| `path.dirname(p)` | ✅ | | • Directory portion; trailing slashes trimmed first |
| `path.basename(p, ext?)` | ✅ | | • Final path segment, optionally with a trailing `ext` stripped — not stripped when doing so would consume the whole segment, unless the *entire* `path` argument equals `ext` (then returns `""`), matching real Node's own asymmetric rule here |
| `path.extname(p)` | ✅ | | • Extension of the basename, including the leading `.`; `""` for a dotfile whose only `.` is its first character |
| `path.parse(p)` / `path.format(obj)` | ✅ | | • `parse` returns `{root, dir, base, ext, name}`; `format` is the inverse (`base` wins over `name`+`ext`, `dir` falls back to `root`) |
| `path.isAbsolute(p)` | ✅ | | • `p[0] === '/'` |
| `path.sep` / `path.delimiter` | ✅ | • POSIX-only (this compiler doesn't cross-compile) — the compile-time constants `/` and `:`, never the Windows `\`/`;` forms | • No cross-compilation, so no Windows form is ever needed — see [process.platform](PROCESS-CLI.md) |
