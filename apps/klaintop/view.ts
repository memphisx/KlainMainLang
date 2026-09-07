// klaintop — the view: a pure function from `State` (+ the current CPU fraction)
// to a `klain:tui` component tree. No I/O and no loop state; the data layer
// (data.ts) feeds it and the loop (main.ts) renders whatever it returns.

import { Box, Text, List, Progress, Spinner } from "klain:tui";
import { totalmem, freemem, cpus, hostname, platform } from "os";
import { Proc, State } from "./types";
import { memFrac } from "./data";

// Right-align a number in `w` columns (poor-man's printf); `d` decimals.
function pad(n: number, w: number, d: number): string {
  const s: string = n.toFixed(d);
  return s.length >= w ? s : " ".repeat(w - s.length) + s;
}
function padRight(s: string, w: number): string {
  if (s.length > w) return s.slice(0, w);
  return s + " ".repeat(w - s.length);
}

// One process → a fixed-width row: "  PID   CPU   MEM  COMMAND".
function procRow(p: Proc): string {
  return pad(p.pid, 6, 0) + " " + pad(p.cpu, 5, 1) + " " + pad(p.mem, 5, 1) + "  " + padRight(p.comm, 22);
}

function cpuBar(frac: number) {
  return Box({ flexDirection: "row", gap: 1 }, [
    Text("CPU", { color: "gray", width: 4 }),
    Progress(frac, { color: "yellow", width: 22 }),
    Text(Math.round(frac * 100) + "%", { color: "yellow", width: 5 }),
  ]);
}
function memBar(frac: number) {
  return Box({ flexDirection: "row", gap: 1 }, [
    Text("MEM", { color: "gray", width: 4 }),
    Progress(frac, { color: "magenta", width: 22 }),
    Text(Math.round(frac * 100) + "%", { color: "magenta", width: 5 }),
  ]);
}

export function view(s: State, cpuFrac: number) {
  const totalMB = Math.round(totalmem() / 1048576);
  const usedMB = Math.round((totalmem() - freemem()) / 1048576);
  const cores = cpus().length;

  const killing = s.confirming && s.procs.length > 0;
  const killPid: number = killing ? s.procs[s.cursor].pid : 0;
  const killComm: string = killing ? s.procs[s.cursor].comm : "";
  const rows = s.procs.map((p) => procRow(p));

  const children = [
    Box({ flexDirection: "row", justifyContent: "space-between" }, [
      Text("klaintop", { color: "green", bold: true }),
      Text(hostname() + " · " + platform(), { color: "gray", dim: true }),
    ]),
    Box({ height: 1 }, []),
    cpuBar(cpuFrac),
    memBar(memFrac()),
    Box({ flexDirection: "row", justifyContent: "space-between" }, [
      Text(usedMB + " / " + totalMB + " MB · " + cores + " cores", { color: "gray" }),
      Spinner(s.tick, { label: "sorted by " + s.sort, color: "blue" }),
    ]),
    Box({ height: 1 }, []),
    // Column header, then the scrolling process list (flexGrow fills the rest of
    // the box, so List scrolls to keep the selection visible).
    Text("   PID   CPU   MEM  COMMAND", { color: "cyan", bold: true }),
    List(rows, { selected: s.cursor, flexGrow: 1 }),
  ];

  // The footer is either the shortcut hint or a kill confirmation — pushed
  // rather than a ternary so each keeps its own literal style props.
  if (killing) {
    children.push(Text("kill " + killPid + " (" + killComm + ")?  y / n", { color: "red", bold: true }));
  } else {
    children.push(Text("↑/↓ select · c/m sort · k kill · q quit", { color: "gray", dim: true }));
  }

  return Box(
    { flexDirection: "column", width: 48, height: 20, border: "round", borderColor: "cyan", padding: 1 },
    children,
  );
}
