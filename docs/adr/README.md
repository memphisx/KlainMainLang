# Architecture Decision Records

This folder tracks every non-trivial implementation decision, feature addition, and bug fix made in KlainMainLang from this point forward. Every new feature and every bug fix requires an ADR — see below for the required sections.

## Numbering

- Files are named `ADR-NNNNN.md`, zero-padded to 5 digits, starting at `ADR-00001.md`.
- Numbers are assigned sequentially and never reused, even if an ADR is later superseded or reverted.
- Before creating a new one, check the **Index** below for the last number used.

## Status values

- **Completed** — the work this ADR describes was finished, tested, and verified; nothing from its own scope was left dangling.
- **Completed — extended by ADR-NNNNN** — the work was finished as scoped, but that scope was deliberately narrowed with a named follow-up in mind (e.g. "not implemented yet, a natural next step"), and a later, already-written ADR delivered exactly that follow-up. Additive, not a reversal — the original decision still stands.
- **Superseded by ADR-NNNNN** — a later ADR actually overturned or replaced this one's decision, not just built on top of it.
- **Proposed** — written before the work is done (design-only, no implementation yet). Rare in this project, since ADRs here are normally written after a feature/fix is already implemented and verified.

A scope narrowing that hasn't been picked up by any later ADR yet (e.g. "`fetch` is GET-only for now") stays **Completed** — the *deferred* item is tracked in `STATUS.md`'s roadmap, not treated as this ADR being incomplete.

## Format

Copy [`TEMPLATE.md`](TEMPLATE.md) as a starting point. At minimum, an ADR must cover:

- **Context** — the problem or need, and how it was discovered (repro steps, what surfaced it).
- **Investigation** — what was read/tested to understand the root cause before deciding on a fix; cite concrete file:line references.
- **Decision** — the approach taken, and briefly why alternatives were rejected.
- **Implementation notes** — files touched, and anything non-obvious that came up while implementing.
- **Side effects discovered** — any other bugs, limitations, or surprises found along the way, even if not fixed (link to where they ended up tracked, e.g. `STATUS.md`).
- **Verification** — how the fix/feature was confirmed to work (tests added, manual repros, build/test/example runs).

## Index

| # | Title | Status |
|---|---|---|
| [00001](ADR-00001.md) | Fix closure capture-by-value bug (share mutable state with enclosing scope) | Completed |
| [00002](ADR-00002.md) | Implement process.argv, process.exit(code), process.env | Completed |
| [00003](ADR-00003.md) | Implement String.prototype.replaceAll(from, to) | Completed |
| [00004](ADR-00004.md) | Fix empty-string-argument bugs in .split(), .padStart(), .padEnd() | Completed |
| [00005](ADR-00005.md) | Implement String.prototype.trimStart() / trimEnd() | Completed |
| [00006](ADR-00006.md) | Fix JSON.stringify(boolean[]) crash and JSON.stringify(object[]) garbage output | Completed |
| [00007](ADR-00007.md) | Implement JSON.parse(s) → object (flat objects, primitive fields) | Completed |
| [00008](ADR-00008.md) | Implement any/unknown as a runtime-tagged value (Staged V1) | Completed |
| [00009](ADR-00009.md) | Implement Math.asin/acos/atan/atan2, sinh/cosh/tanh, cbrt/expm1/log1p | Completed |
| [00010](ADR-00010.md) | Implement labeled break/continue | Completed |
| [00011](ADR-00011.md) | Implement for...of over Map and Set | Completed |
| [00012](ADR-00012.md) | Implement shorthand object properties { x } | Completed |
| [00013](ADR-00013.md) | Implement object spread { ...obj, key: val } | Completed |
| [00014](ADR-00014.md) | Implement Date (UTC-only) | Completed |
| [00015](ADR-00015.md) | Implement Date.parse(string) | Completed — extended by ADR-00017 |
| [00016](ADR-00016.md) | Implement Date setters (setFullYear, setMonth, setDate, setHours, setMinutes, setSeconds, setMilliseconds, setTime) | Completed |
| [00017](ADR-00017.md) | Extend Date.parse to support +HH:MM/-HH:MM timezone offsets | Completed |
| [00018](ADR-00018.md) | Implement Date arithmetic (adding/subtracting durations) | Completed |
| [00019](ADR-00019.md) | Implement Date formatting (toDateString / toLocaleDateString) | Completed |
| [00020](ADR-00020.md) | Link-flags plumbing (compiled programs can depend on external libraries) | Completed |
| [00021](ADR-00021.md) | Implement fetch(url) and Response (GET only, V1) | Completed |
| [00022](ADR-00022.md) | Implement import/export (named, declarations-only, V1) | Completed |
| [00023](ADR-00023.md) | Implement fs.readFileSync/writeFileSync/appendFileSync/existsSync/unlinkSync | Completed — extended by ADR-00027 |
| [00024](ADR-00024.md) | Near-zero-effort roadmap batch (NaN/Infinity, performance.now, atob/btoa, encodeURI(Component)/decodeURI(Component), crypto.getRandomValues/randomUUID, process.readLineSync) | Completed |
| [00025](ADR-00025.md) | Implement process.execFileSync(file, args?) (V1: no options object) | Completed |
| [00026](ADR-00026.md) | Implement process.cwd/chdir/pid/platform/kill | Completed |
| [00027](ADR-00027.md) | Complete the fs.* API (mkdirSync/rmdirSync/renameSync/copyFileSync/readdirSync) | Completed |
| [00028](ADR-00028.md) | Implement String.prototype charAt/codePointAt/search/localeCompare | Completed |
| [00029](ADR-00029.md) | Implement console.time/timeEnd, count/countReset, group/groupEnd, dir | Completed |
| [00030](ADR-00030.md) | Implement Memory.free(x) (Stage 1 of the manual-memory-management plan) | Completed |
| [00031](ADR-00031.md) | Implement setTimeout/clearTimeout/setInterval/clearInterval | Completed |
| [00032](ADR-00032.md) | Add a --static CLI flag for statically-linked binaries (Linux only) | Completed |
| [00033](ADR-00033.md) | Verify --static with fetch/libcurl on Alpine/musl | Completed |
| [00034](ADR-00034.md) | Fix missing -lm link flag for Math builtins on Linux | Completed |
| [00035](ADR-00035.md) | Fix JSON.stringify truncating float-typed values to integers | Completed |
| [00036](ADR-00036.md) | Fix JSON.stringify serializing Date fields as raw ms numbers | Completed |
| [00037](ADR-00037.md) | Fix parenthesized function-type return annotations | Completed |
| [00038](ADR-00038.md) | Fix new Date(aStringLiteral) crashing instead of parsing | Completed |
