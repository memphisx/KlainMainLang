# Architecture Decision Records

This folder tracks every non-trivial implementation decision, feature addition, and bug fix made in KlainMainLang from this point forward. Every new feature and every bug fix requires an ADR — see below for the required sections.

An ADR is written *after* a feature/fix is implemented, tested, and verified — it's a log of what was decided and why while building something, not a design proposal (that's what a `docs/tdd/` Technical Design Document is for, written *before* implementation, when the design might still change). Because of that, an ADR has no "is this done yet" status to track: by the time one exists, the work it describes is already finished. There's no `Proposed` state in this project's ADRs, and never will be — a not-yet-implemented idea belongs in a TDD instead.

## Numbering

- Files are named `ADR-NNNNN.md`, zero-padded to 5 digits, starting at `ADR-00001.md`.
- Numbers are assigned sequentially and never reused, even if an ADR is later superseded or reverted.
- Before creating a new one, check the **Index** below for the last number used.

## Relations

Optional field, omitted entirely when there's nothing to note. Captures how this ADR connects to others, or to the TDD it originated from:

- **`Extends ADR-NNNNN`** / **`Extended by ADR-NNNNN`** — this ADR built on an earlier one's deliberately narrowed scope (or was later built upon), additive rather than a reversal. Written on *both* ends of the relationship, so either ADR is discoverable from the other without searching.
- **`Supersedes ADR-NNNNN`** / **`Superseded by ADR-NNNNN`** — a later ADR actually overturned or replaced an earlier one's decision, not just built on top of it.
- **`Implements TDD-NNNNN`** — this ADR is the (or a) real implementation of a design scoped out in `docs/tdd/`.

Combine as needed on one line, e.g. `Extends ADR-00012, ADR-00037`. A scope narrowing that hasn't been picked up by any later ADR yet (e.g. "`fetch` is GET-only for now") gets no `Relations` line at all — the *deferred* item is tracked in `STATUS.md`'s roadmap, not treated as this ADR being incomplete.

## Format

Copy [`TEMPLATE.md`](TEMPLATE.md) as a starting point. At minimum, an ADR must cover:

- **Context** — the problem or need, and how it was discovered (repro steps, what surfaced it).
- **Investigation** — what was read/tested to understand the root cause before deciding on a fix; cite concrete file:line references.
- **Decision** — the approach taken, and briefly why alternatives were rejected.
- **Implementation notes** — files touched, and anything non-obvious that came up while implementing.
- **Side effects discovered** — any other bugs, limitations, or surprises found along the way, even if not fixed (link to where they ended up tracked, e.g. `STATUS.md`).
- **Verification** — how the fix/feature was confirmed to work (tests added, manual repros, build/test/example runs).

## Index

| # | Title | Relations |
|---|---|---|
| [00001](ADR-00001.md) | Fix closure capture-by-value bug (share mutable state with enclosing scope) | |
| [00002](ADR-00002.md) | Implement process.argv, process.exit(code), process.env | |
| [00003](ADR-00003.md) | Implement String.prototype.replaceAll(from, to) | |
| [00004](ADR-00004.md) | Fix empty-string-argument bugs in .split(), .padStart(), .padEnd() | |
| [00005](ADR-00005.md) | Implement String.prototype.trimStart() / trimEnd() | |
| [00006](ADR-00006.md) | Fix JSON.stringify(boolean[]) crash and JSON.stringify(object[]) garbage output | |
| [00007](ADR-00007.md) | Implement JSON.parse(s) → object (flat objects, primitive fields) | |
| [00008](ADR-00008.md) | Implement any/unknown as a runtime-tagged value (Staged V1) | |
| [00009](ADR-00009.md) | Implement Math.asin/acos/atan/atan2, sinh/cosh/tanh, cbrt/expm1/log1p | |
| [00010](ADR-00010.md) | Implement labeled break/continue | |
| [00011](ADR-00011.md) | Implement for...of over Map and Set | |
| [00012](ADR-00012.md) | Implement shorthand object properties { x } | Extended by ADR-00041 |
| [00013](ADR-00013.md) | Implement object spread { ...obj, key: val } | |
| [00014](ADR-00014.md) | Implement Date (UTC-only) | |
| [00015](ADR-00015.md) | Implement Date.parse(string) | Extended by ADR-00017 |
| [00016](ADR-00016.md) | Implement Date setters (setFullYear, setMonth, setDate, setHours, setMinutes, setSeconds, setMilliseconds, setTime) | |
| [00017](ADR-00017.md) | Extend Date.parse to support +HH:MM/-HH:MM timezone offsets | Extends ADR-00015 |
| [00018](ADR-00018.md) | Implement Date arithmetic (adding/subtracting durations) | |
| [00019](ADR-00019.md) | Implement Date formatting (toDateString / toLocaleDateString) | |
| [00020](ADR-00020.md) | Link-flags plumbing (compiled programs can depend on external libraries) | |
| [00021](ADR-00021.md) | Implement fetch(url) and Response (GET only, V1) | |
| [00022](ADR-00022.md) | Implement import/export (named, declarations-only, V1) | |
| [00023](ADR-00023.md) | Implement fs.readFileSync/writeFileSync/appendFileSync/existsSync/unlinkSync | Extended by ADR-00027 |
| [00024](ADR-00024.md) | Near-zero-effort roadmap batch (NaN/Infinity, performance.now, atob/btoa, encodeURI(Component)/decodeURI(Component), crypto.getRandomValues/randomUUID, process.readLineSync) | |
| [00025](ADR-00025.md) | Implement process.execFileSync(file, args?) (V1: no options object) | |
| [00026](ADR-00026.md) | Implement process.cwd/chdir/pid/platform/kill | |
| [00027](ADR-00027.md) | Complete the fs.* API (mkdirSync/rmdirSync/renameSync/copyFileSync/readdirSync) | Extends ADR-00023 |
| [00028](ADR-00028.md) | Implement String.prototype charAt/codePointAt/search/localeCompare | |
| [00029](ADR-00029.md) | Implement console.time/timeEnd, count/countReset, group/groupEnd, dir | |
| [00030](ADR-00030.md) | Implement Memory.free(x) (Stage 1 of the manual-memory-management plan) | Implements TDD-00001 |
| [00031](ADR-00031.md) | Implement setTimeout/clearTimeout/setInterval/clearInterval | Implements TDD-00002 |
| [00032](ADR-00032.md) | Add a --static CLI flag for statically-linked binaries (Linux only) | |
| [00033](ADR-00033.md) | Verify --static with fetch/libcurl on Alpine/musl | |
| [00034](ADR-00034.md) | Fix missing -lm link flag for Math builtins on Linux | |
| [00035](ADR-00035.md) | Fix JSON.stringify truncating float-typed values to integers | |
| [00036](ADR-00036.md) | Fix JSON.stringify serializing Date fields as raw ms numbers | |
| [00037](ADR-00037.md) | Fix parenthesized function-type return annotations | Extended by ADR-00041 |
| [00038](ADR-00038.md) | Fix new Date(aStringLiteral) crashing instead of parsing | |
| [00039](ADR-00039.md) | Implement the multi-argument new Date(year, month, ...) constructor | |
| [00040](ADR-00040.md) | Support JSDoc @type overrides on interface fields | |
| [00041](ADR-00041.md) | Infer return types for unannotated functions and arrow functions | Extends ADR-00012, ADR-00037 |
| [00042](ADR-00042.md) | Reject non-numeric arguments to unannotated parameters at call sites | Implements TDD-00005 |
| [00043](ADR-00043.md) | Fix forEach/HOF callbacks with console.log bodies or non-numeric elements | |
| [00044](ADR-00044.md) | Fix array index out-of-bounds reads/writes with a runtime bounds check | |
| [00045](ADR-00045.md) | Reject const reassignment with a Symbol.IsConst check in emitAssign | |
| [00046](ADR-00046.md) | Fix (FuncType)[] parser gap and enable calling closures stored in arrays/object fields | |
| [00047](ADR-00047.md) | Fix bitwise shift operators to use JS's 32-bit semantics | |
| [00048](ADR-00048.md) | select()-based event loop (TDD-00006 Part 1) and a minimal HTTP server (TDD-00004 V1) | Implements TDD-00004, TDD-00006 |
