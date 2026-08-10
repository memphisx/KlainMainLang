# path

> Part of the [Implementation Status](README.md) index. Node's `path` module — portable filesystem path manipulation. Import-gated (`import path from 'path'` or `import { join } from 'path'`) — see [TDD-00049](../tdd/TDD-00049.md)/[ADR-00141](../adr/ADR-00141.md)/[ADR-00142](../adr/ADR-00142.md).

**Coverage**: 100% (8/8) — see [ADR-00081](../adr/ADR-00081.md).

**Caveats**: POSIX-only (this compiler doesn't cross-compile — `sep`/`delimiter` are compile-time constants, `/` and `:`, never the Windows forms). `join`/`resolve`'s `..`-above-root handling, `basename`'s `ext`-stripping edge cases (an `ext` argument that consumes the *entire* basename is left unstripped, matching real Node's own non-obvious behavior there), and multi-slash collapsing were all verified directly against a real Node install rather than assumed — see the ADR's Verification section.

| API | Status | Notes |
|---|---|---|
| `path.join(...segments)` | ✅ | Joins with `/`, then normalizes (collapses repeated slashes, drops `.` segments, resolves `..` against segments already seen in the same call) |
| `path.resolve(...segments)` | ✅ | Absolute-always: starts from `process.cwd()`, walks segments left to right, any segment starting with `/` resets the accumulator (discarding everything before it, including cwd) — equivalent to real Node's right-to-left "stop at the last absolute segment" algorithm |
| `path.dirname(p)` | ✅ | Directory portion; trailing slashes trimmed first |
| `path.basename(p, ext?)` | ✅ | Final path segment, optionally with a trailing `ext` stripped — not stripped when doing so would consume the whole segment, unless the *entire* `path` argument equals `ext` (then returns `""`), matching real Node's own asymmetric rule here |
| `path.extname(p)` | ✅ | Extension of the basename, including the leading `.`; `""` for a dotfile whose only `.` is its first character |
| `path.parse(p)` / `path.format(obj)` | ✅ | `parse` returns `{root, dir, base, ext, name}`; `format` is the inverse (`base` wins over `name`+`ext`, `dir` falls back to `root`) |
| `path.isAbsolute(p)` | ✅ | `p[0] === '/'` |
| `path.sep` / `path.delimiter` | ✅ | Compile-time constants `/` and `:` (no cross-compilation, so no Windows form is ever needed — see [process.platform](PROCESS-CLI.md)) |
