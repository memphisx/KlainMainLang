// todo — the view: a pure function from (tasks, cursor, adding, draft) to a
// `klain:tui` component tree. No I/O; the store (store.ts) feeds it and the loop
// (main.ts) renders whatever it returns.

import { Box, Text, List, Progress, TextInput } from "klain:tui";
import { Task } from "./types";

export function view(tasks: Task[], cursor: number, adding: boolean, draft: string) {
  let done = 0;
  for (let i = 0; i < tasks.length; i++) if (tasks[i].done) done++;

  const rows = tasks.map((t) => (t.done ? "[x] " + t.text : "[ ] " + t.text));

  const children = [
    Text("To-do", { color: "green", bold: true }),
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
