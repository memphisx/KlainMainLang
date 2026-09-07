// todo — persistence: the task list is saved to and loaded from a file, one task
// per line as "1 text" (done) or "0 text" (open). Real `fs`, no `klain:tui`.

import { existsSync, readFileSync, writeFileSync } from "fs";
import { Task } from "./types";

export const FILE = ".klain-todo.txt";

// Load the saved list, or a small starter list on first run.
export function load(): Task[] {
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

export function save(tasks: Task[]): void {
  let body = "";
  for (let i = 0; i < tasks.length; i++) {
    body = body + (tasks[i].done ? "1 " : "0 ") + tasks[i].text + "\n";
  }
  writeFileSync(FILE, body);
}
