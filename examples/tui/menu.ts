// Native terminal UI (TDD-00150 Stage 1): a flexbox-laid-out, double-buffered
// component tree from `klain:tui`, driven by an Elm-style state -> view -> update
// loop written in plain TypeScript over the `klain:tty` raw key reads.
//
//   ./menu              # interactive: ↑/↓ move, space toggles, q quits
//   ./menu </dev/null   # non-TTY: paints one frame and exits cleanly
//
// The layout engine is Facebook's Yoga (vendored, flexbox); the painter diffs
// each frame against the last and emits only the cells that changed. No runtime,
// no reconciler — the builders (Box/Text/List/Progress/Spinner) map straight
// onto native layout nodes, and the loop is ordinary control flow.

import { Box, Text, List, Progress, Spinner, render, enter, leave } from "klain:tui";
import { readKey } from "klain:tty";

const fruits = ["apple", "banana", "cherry", "date", "elderberry"];

// The whole app state is a plain object; `view` is a pure function of it.
type State = { cursor: number; picked: boolean[]; tick: number };

function view(s: State) {
  const chosen = s.picked.filter((p) => p).length;
  return Box(
    { flexDirection: "column", width: 40, border: "round", borderColor: "cyan", padding: 1 },
    [
      Text("klain:tui — fruit picker", { color: "green", bold: true }),
      Box({ height: 1 }, []),
      List(
        fruits.map((f, i) => (s.picked[i] ? "[x] " + f : "[ ] " + f)),
        { selected: s.cursor },
      ),
      Box({ height: 1 }, []),
      Box({ flexDirection: "row", justifyContent: "space-between" }, [
        Text("chosen: " + chosen + "/" + fruits.length, { color: "yellow" }),
        Spinner(s.tick, { label: "live", color: "blue" }),
      ]),
      Progress(chosen / fruits.length, { color: "green" }),
    ],
  );
}

const state: State = {
  cursor: 0,
  picked: [false, false, false, false, false],
  tick: 0,
};

enter();
render(view(state));

// Non-interactive (piped/redirected): one frame is enough — leave and exit.
if (!process.stdin.isTTY) {
  leave();
} else {
  process.stdin.setRawMode(true);
  let running = true;
  while (running) {
    const key: string = readKey();
    const code: number = key.length > 0 ? key.charCodeAt(0) : -1;
    if (code === -1 || key === "q" || code === 3) {
      running = false;
    } else if (key === "\x1b[A") {
      state.cursor = (state.cursor + fruits.length - 1) % fruits.length;
    } else if (key === "\x1b[B") {
      state.cursor = (state.cursor + 1) % fruits.length;
    } else if (key === " ") {
      state.picked[state.cursor] = !state.picked[state.cursor];
    }
    state.tick = state.tick + 1;
    render(view(state));
  }
  leave();
}
