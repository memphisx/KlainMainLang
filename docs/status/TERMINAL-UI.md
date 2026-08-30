<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/terminal-ui.json; edit the JSON, then run `make status`. -->

# Terminal UI — `klain:tui`

Native terminal-UI framework: a real flexbox layout engine (Facebook's [Yoga](https://github.com/facebook/yoga), vendored) plus a double-buffered ANSI diff painter, sitting directly on the shipped terminal primitives ([ADR-00518](../adr/ADR-00518.md)). Builder functions (`Box`/`Text`/`List`/`Spinner`/`Progress`/`TextInput`) describe a component tree; `render(root)` lays it out and paints only the cells that changed.

Design: [TDD-00150](../tdd/TDD-00150.md) (Stage 1 of a staged roadmap). Implementation: [ADR-00519](../adr/ADR-00519.md).

`import { Box, Text, List, Spinner, Progress, TextInput, render, enter, leave } from 'klain:tui'`

**Coverage**: 11/11 (100%) · **Strict Coverage**: 5/11 (~45%).

Format: [Status page format](README.md#status-page-format).

| Surface | Status | Caveats | Notes |
|---|---|---|---|
| Flexbox layout (vendored Yoga) | ✅ | • A pragmatic subset of Yoga's style surface is exposed: `flexDirection`/`flexGrow`/`flexShrink`/`flexBasis`, `justifyContent`/`alignItems`/`alignSelf`/`flexWrap`, `width`/`height`/`minWidth`/`minHeight`, `padding*`/`margin*`/`gap`. Percentage units, `position:absolute`, and `aspectRatio` are not yet surfaced | • Yoga compiled per-`.cpp` to objects (C++20) and linked only when a program uses `klain:tui` ([ADR-00519](../adr/ADR-00519.md)) |
| `Box(props?, children?)` — flex container | ✅ | | • Children may be an array literal or any runtime array of nodes (e.g. `items.map(...)`); background fill, border, and padding supported |
| `Text(text, props?)` — styled, wrapped text | ✅ | • Text is UTF-8-decoded to one grid cell per code point, but not width-aware: wide (CJK/emoji) and combining characters occupy one cell, so alignment drifts for such content | • Greedy word-wrap on the laid-out width (`wrap: false` clips to one line); auto-sizes via a Yoga measure func |
| `List(items, props?)` | ✅ | • Flat single-line items with an optional `selected` highlight; no built-in scroll/viewport for lists taller than their box | |
| `Progress(value, props?)` | ✅ | | • `value` 0..1; fills the laid-out width with block glyphs (`█`/`░`) |
| `Spinner(frame, props?)` | ✅ | | • Braille frame glyph at `frame % 10`, optional `label`; the caller advances `frame` each tick |
| `TextInput(value, props?)` | ✅ | • Renders the value plus a cursor block; editing/key handling is userland (read keys via `klain:tty`, mutate state, re-render) | |
| Styling — colours, border, text attributes | ✅ | | • `color`/`backgroundColor`/`borderColor` (16 ANSI names), `border` (`single`/`round`/`double`), `bold`/`dim`/`underline`/`inverse` |
| `render(root)` — layout + diff paint | ✅ | • Immediate-mode: the whole tree is rebuilt and re-laid-out every frame (the cell diff keeps *output* minimal); a retained/memoized node tree is a deferred perf question | • Runs Yoga layout against the live terminal size, diffs against the previous frame's cell grid, emits the minimal cursor-move + write sequence, then frees the tree |
| `enter()` / `leave()` — screen management | ✅ | | • Alternate screen buffer + cursor hide/show; pair with `process.stdin.setRawMode(true)` for interactive apps |
| `state → view → update` app loop | ✅ | • The loop is written in userland TypeScript over the `klain:tty` key reads + `SIGWINCH` — there is no built-in app-runner and no callback-driven loop yet (a closure→C function-pointer trampoline is TDD-00150 Stage 2) | • Input-driven apps block on `readKey()`; self-refreshing dashboards use `readKey(timeoutMs)` ([ADR-00520](../adr/ADR-00520.md)) to wake on a tick as well as a key. See `examples/tui/` for both patterns |

Verified on macOS (Apple Silicon); the interactive key/resize loop is confirmed manually under a terminal, the deterministic render surface by E2E tests. Linux is compile-tier (same pure-POSIX C as the primitives it builds on) — not yet run there.
