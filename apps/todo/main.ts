// A complete terminal to-do list — the flagship `klain:tui` walkthrough app.
//
//   ./todo              # interactive: ↑/↓ move · space toggle · a add · d delete · q quit
//   ./todo </dev/null   # non-TTY: paints one frame from the saved file and exits
//
// It shows the whole Stage 1 surface working together: a flexbox layout, a
// selectable List, a TextInput for adding items, a Progress summary, coloured
// styling — plus real `fs` persistence and the Elm-style `state → view → update`
// loop over `klain:tty`. Split by concern: types.ts (Task), store.ts (fs
// load/save), view.ts (state → tree), and this file (the update loop).

import { render, enter, leave } from "klain:tui";
import { readKey } from "klain:tty";
import { load, save } from "./store";
import { view } from "./view";

const tasks = load();
let cursor = 0;
let adding = false;
let draft = "";

enter();
render(view(tasks, cursor, adding, draft));

if (!process.stdin.isTTY) {
  leave();
} else {
  process.stdin.setRawMode(true);
  let running = true;
  while (running) {
    const key: string = readKey();
    const code: number = key.length > 0 ? key.charCodeAt(0) : -1;

    if (adding) {
      if (key === "\x1b") {
        adding = false;
        draft = "";
      } else if (key === "\r" || key === "\n") {
        if (draft.length > 0) {
          tasks.push({ text: draft, done: false });
          cursor = tasks.length - 1;
        }
        adding = false;
        draft = "";
      } else if (code === 127 || code === 8) {
        draft = draft.slice(0, draft.length - 1);
      } else if (code >= 32 && code < 127) {
        draft = draft + key;
      }
    } else if (key === "q" || code === 3) {
      running = false;
    } else if (key === "\x1b[A") {
      if (tasks.length > 0) cursor = (cursor + tasks.length - 1) % tasks.length;
    } else if (key === "\x1b[B") {
      if (tasks.length > 0) cursor = (cursor + 1) % tasks.length;
    } else if (key === " ") {
      if (tasks.length > 0) tasks[cursor].done = !tasks[cursor].done;
    } else if (key === "a") {
      adding = true;
      draft = "";
    } else if (key === "d") {
      if (tasks.length > 0) {
        tasks.splice(cursor, 1);
        if (cursor >= tasks.length && cursor > 0) cursor = cursor - 1;
      }
    }

    render(view(tasks, cursor, adding, draft));
  }
  save(tasks);
  leave();
}
