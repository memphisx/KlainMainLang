// klaintop — a live process manager, htop-style, in the terminal. It pairs the
// self-refreshing `klain:tui` loop with real OS data: `os` for the CPU/memory
// header bars, and `ps` (via `process.execFileSync`) for a scrolling, sortable
// process table. The selected process can be killed with a y/n confirm.
//
//   ./klaintop              # interactive: ↑/↓ select · c/m sort · k kill · q quit
//   ./klaintop </dev/null   # non-TTY: paints one sample and exits
//
// The refresh trick is `readKey(1500)` from `klain:tty`: it waits up to 1.5s for
// a keystroke and returns "" if none came — so the loop re-samples on a tick
// *and* responds to keys, with no background thread.
//
// Split by concern: `types.ts` (the State shape), `data.ts` (OS/ps data — CPU
// jiffies, memory, the process table), `view.ts` (State -> component tree), and
// this file (the update loop). main.ts owns the one piece of loop state the data
// layer can't: `prev`, the previous jiffy sample the CPU delta is measured from.

import { render, enter, leave } from "klain:tui";
import { readKey } from "klain:tty";
import { State } from "./types";
import { sample, cpuUtilization, listProcs } from "./data";
import { view } from "./view";

let prev = sample();
const state: State = { procs: listProcs("cpu"), cursor: 0, sort: "cpu", confirming: false, tick: 0 };

// The most recently computed CPU fraction, so a resize repaint (SIGWINCH) can
// redraw without consuming a fresh jiffy sample (which would read ~0%).
let lastCpu = 0;

// CPU utilisation since the previous sample; updates `prev` as a side effect.
function cpuFrac(): number {
  const cur = sample();
  const frac = cpuUtilization(prev, cur);
  prev = cur;
  lastCpu = frac;
  return frac;
}

// Clamp the cursor into range whenever the list changes size.
function clampCursor(): void {
  if (state.cursor < 0) state.cursor = 0;
  if (state.cursor >= state.procs.length) state.cursor = state.procs.length - 1;
  if (state.cursor < 0) state.cursor = 0;
}

// q or Ctrl-C quits.
function isQuit(key: string, code: number): boolean {
  return key === "q" || code === 3;
}

enter();
render(view(state, 0));

if (!process.stdin.isTTY) {
  leave();
} else {
  process.stdin.setRawMode(true);
  // Repaint immediately on a terminal resize; the view reads the new
  // columns/rows each render, so the box refits the window.
  process.on("SIGWINCH", () => {
    render(view(state, lastCpu));
  });
  let running = true;
  while (running) {
    const key: string = readKey(1500); // wake on a key OR after ~1.5s
    const code: number = key.length > 0 ? key.charCodeAt(0) : -1;

    if (isQuit(key, code)) {
      running = false;
    } else if (state.confirming) {
      // In a kill confirmation, only y/n matter.
      if (key === "y" && state.procs.length > 0) {
        try {
          process.kill(state.procs[state.cursor].pid);
        } catch (e) {
          // process already gone / not permitted — ignore, the next sample shows the truth.
        }
        state.procs = listProcs(state.sort);
        clampCursor();
      }
      state.confirming = false;
      render(view(state, cpuFrac()));
    } else if (key === "\x1b[A") {
      state.cursor = state.cursor - 1;
      clampCursor();
      render(view(state, cpuFrac()));
    } else if (key === "\x1b[B") {
      state.cursor = state.cursor + 1;
      clampCursor();
      render(view(state, cpuFrac()));
    } else if (key === "c" || key === "m") {
      state.sort = key === "m" ? "mem" : "cpu";
      state.procs = listProcs(state.sort);
      clampCursor();
      render(view(state, cpuFrac()));
    } else if (key === "k") {
      if (state.procs.length > 0) state.confirming = true;
      render(view(state, cpuFrac()));
    } else {
      // A tick (or any other key): re-sample and refresh the table.
      state.tick = state.tick + 1;
      state.procs = listProcs(state.sort);
      clampCursor();
      render(view(state, cpuFrac()));
    }
  }
  leave();
}
