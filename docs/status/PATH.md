<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/path.json; edit the JSON, then run `make status`. -->

# path

> Part of the [Implementation Status](README.md) index. Node's `path` module — portable filesystem path manipulation. Import-gated (`import path from 'path'` or `import { join } from 'path'`) — see [TDD-00049](../tdd/TDD-00049.md)/[ADR-00141](../adr/ADR-00141.md)/[ADR-00142](../adr/ADR-00142.md).

**Coverage**: 8/8 (100%) · **Strict Coverage**: 6/8 (75%).

Format: [Status page format](README.md#status-page-format).

| API | Status | Caveats | Notes |
|---|---|---|---|
| `path.join(...segments)` | ✅ | • Drops a trailing slash on the result — `path.join('foo', 'bar/')` is `'foo/bar'`, where Node preserves `'foo/bar/'`. | • Joins with `/`, then normalizes (collapses repeated slashes, drops `.` segments, resolves `..` against segments already seen in the same call) |
| `path.resolve(...segments)` | ✅ | | • Absolute-always: starts from `process.cwd()`, walks segments left to right, any segment starting with `/` resets the accumulator (discarding everything before it, including cwd) — equivalent to real Node's right-to-left "stop at the last absolute segment" algorithm |
| `path.dirname(p)` | ✅ | | • Directory portion; trailing slashes trimmed first |
| `path.basename(p, ext?)` | ✅ | | • Final path segment, optionally with a trailing `ext` stripped — not stripped when doing so would consume the whole segment, unless the *entire* `path` argument equals `ext` (then returns `""`), matching real Node's own asymmetric rule here<br>• See [ADR-00368](../adr/ADR-00368.md) |
| `path.extname(p)` | ✅ | | • Extension of the basename, including the leading `.`; `""` for a dotfile whose only `.` is its first character |
| `path.parse(p)` / `path.format(obj)` | ✅ | | • `parse` returns `{root, dir, base, ext, name}`; `format` is the inverse (`base` wins over `name`+`ext`, `dir` falls back to `root`) |
| `path.isAbsolute(p)` | ✅ | | • `p[0] === '/'` |
| `path.sep` / `path.delimiter` | ✅ | • POSIX-only (this compiler doesn't cross-compile) — the compile-time constants `/` and `:`, never the Windows `\`/`;` forms | • No cross-compilation, so no Windows form is ever needed — see [process.platform](PROCESS-CLI.md) |
