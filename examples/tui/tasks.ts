// A complete terminal task manager — the flagship `klain:tui` walkthrough app.
//
//   ./tasks              # interactive: ↑/↓ move · space toggle · a add · d delete · q quit
//   ./tasks </dev/null   # non-TTY: paints one frame from the saved file and exits
//
// It shows the whole Stage 1 surface working together: a flexbox layout, a
// selectable List, a TextInput for adding items, a Progress summary, coloured
// styling — plus real `fs` persistence (tasks are saved to and loaded from a
// file) and the Elm-style `state → view → update` loop over `klain:tty`.

import { Box, Text, List, Progress, TextInput, render, enter, leave } from "klain:tui";
import { readKey } from "klain:tty";
import { existsSync, readFileSync, writeFileSync } from "fs";

const FILE = ".klain-tasks.txt";

type Task = { text: string; done: boolean };

// --- persistence: one task per line, "1 text" (done) or "0 text" (open) ------
function load(): Task[] {
  if (!existsSync(FILE)) {
    return [
      { text: "wire up CI", done: true },
      { text: "write the docs walkthrough", done: false },
      { text: "ship v0.11", done: false },
    ];
  }
  const out: Task[] = [];
  const lines = readFileSync(FILE).split("\n");
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (line.length < 2) continue;
    out.push({ done: line.charAt(0) === "1", text: line.slice(2) });
  }
  return out;
}

function save(tasks: Task[]): void {
  let body = "";
  for (let i = 0; i < tasks.length; i++) {
    body = body + (tasks[i].done ? "1 " : "0 ") + tasks[i].text + "\n";
  }
  writeFileSync(FILE, body);
}

// --- view: a pure function of state ------------------------------------------
function view(tasks: Task[], cursor: number, adding: boolean, draft: string) {
  let done = 0;
  for (let i = 0; i < tasks.length; i++) if (tasks[i].done) done++;

  const rows = tasks.map((t) => (t.done ? "[x] " + t.text : "[ ] " + t.text));

  const children = [
    Text("Tasks", { color: "green", bold: true }),
    Box({ height: 1 }, []),
    List(rows, { selected: adding ? -1 : cursor }),
    Box({ height: 1 }, []),
  ];

  if (adding) {
    children.push(
      Box({ flexDirection: "row" }, [
        Text("new: ", { color: "yellow" }),
        TextInput(draft, { color: "cyan" }),
      ]),
    );
  } else {
    children.push(
      Box({ flexDirection: "row", justifyContent: "space-between" }, [
        Text(done + "/" + tasks.length + " done", { color: "yellow" }),
        Progress(tasks.length > 0 ? done / tasks.length : 0, { color: "green", width: 20 }),
      ]),
    );
  }

  children.push(Box({ height: 1 }, []));
  children.push(
    Text(
      adding ? "type · Enter save · Esc cancel" : "↑/↓ move · space toggle · a add · d delete · q quit",
      { color: "gray", dim: true },
    ),
  );

  return Box(
    { flexDirection: "column", width: 44, border: "round", borderColor: "cyan", padding: 1 },
    children,
  );
}

// --- state + loop ------------------------------------------------------------
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
