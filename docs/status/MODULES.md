# Modules

> Part of the [Implementation Status](README.md) index.

**Coverage**: ~31% (4/13).

**Caveats**: Whole-program compilation, not separate compilation units — `resolver.ResolveProgram` parses the entry file plus everything it transitively imports and merges them into one `*ast.Program` before codegen runs. There is no linker step, no per-file LLVM module boundary, and `codegen/llvm` never sees an `import`/`export` node. See [ADR-00022](../adr/ADR-00022.md) and `resolver/resolver.go`'s package doc for the full design. No real per-file scoping yet (all top-level names must be unique across every merged file), no import aliasing, and imported files may not run top-level side-effecting code.

| Feature | Status | Notes |
|---|---|---|
| `export function` / `const`/`let`/`var` / `interface` / `type` / `enum` | ✅ | A declaration-level modifier, nothing more — consumed entirely by the resolver |
| `import { a, b } from './relative/path'` | ✅ | Named imports only; relative paths only (`./`, `../`), `.ts` auto-appended if omitted; resolved against the importing file's own directory, not `cwd` |
| Circular imports | ✅ | Supported for the declarations-only case — verified directly with two files calling each other's exported functions |
| Diamond-shaped import graphs | ✅ | A file imported from multiple places is parsed once and merged once (memoized by absolute path) |
| Imported (non-entry) files may run top-level side-effecting code | ❌ | **Deliberate V1 scope narrowing, not an oversight** — imported files may only contain declarations (and their own imports); only the entry file's top-level statements execute. Real ES modules run a file's top-level code once, in dependency order, the first time it's imported — that "run once, in order, guard against re-running on cycles" semantics is real design/implementation work of its own, intentionally deferred. **Revisit this later**: build the fuller, real-ES-modules-shaped version, possibly gated behind a compiler flag/configuration so callers can choose between the fast/simple current behavior and full module-execution semantics once both exist. |
| True per-file module scope (mangled internal names) | ❌ | All top-level declaration names must currently be unique across *every* merged file, not just within one file — there's no real per-file scoping yet, so two unrelated files can't both declare a same-named function/interface/enum if both end up reachable from the same entry file. A real fix needs per-file symbol registries and internal name mangling (sketched in [ADR-00022](../adr/ADR-00022.md)'s Investigation) — bigger than V1's scope. |
| Import aliasing (`import { a as b }`) | ❌ | Parsed, but rejected with a clear error — no AST-level renaming is attempted (risk of colliding with local shadowing in the importing file) |
| `export default` | ❌ | Not implemented |
| `import * as ns from '...'` (namespace import) | ❌ | Not implemented |
| Re-exports (`export { x } from './other'`) | ❌ | Not implemented |
| Bare/package-style imports (`import x from 'somepackage'`) | ❌ | No package ecosystem here — only relative paths resolve to anything |
| Dynamic `import(...)` | ❌ | No support found in `parser/` or `resolver/` — this compiler only recognizes the static `import { a } from '...'` form at the top of a file |
| `import.meta` | ❌ | Not implemented — not meaningful yet given there's no dynamic `import()`/module-identity concept to expose either |
