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
// *and* responds to keys, with no background thread. CPU utilisation is the
// classic delta of busy-vs-idle jiffies between two samples of `os.cpus()`; the
// process rows come straight from `ps`, parsed and sorted in-process. The List
// component scrolls itself to keep the selection in view, so a machine with a
// hundred processes needs no manual paging here.

import { Box, Text, List, Progress, Spinner, render, enter, leave } from "klain:tui";
import { readKey } from "klain:tty";
import { cpus, totalmem, freemem, hostname, platform } from "os";

type Proc = { pid: number; cpu: number; mem: number; comm: string };
type State = {
  procs: Proc[];
  cursor: number;
  sort: string; // "cpu" | "mem"
  confirming: boolean;
  tick: number;
};

// --- data: CPU jiffies, memory, and the process table -----------------------

// Aggregate { total, idle } jiffies across every core.
function sample(): { total: number; idle: number } {
  const cs = cpus();
  let total = 0;
  let idle = 0;
  for (let i = 0; i < cs.length; i++) {
    const t = cs[i].times;
    total = total + t.user + t.nice + t.sys + t.idle + t.irq;
    idle = idle + t.idle;
  }
  return { total, idle };
}

// totalmem()/freemem() are byte counts, so force floating-point division.
function memFrac(): number {
  const total = totalmem();
  return total > 0 ? ((total - freemem()) * 1.0) / total : 0;
}

// Snapshot every process via `ps`, parsed into rows and sorted by `sort`. The
// BSD-style `-axo` selection works on both macOS and Linux; we sort in-process
// rather than with a platform-specific `--sort` flag. On any failure (no ps,
// odd output) we return an empty list rather than crash the loop.
function listProcs(sort: string): Proc[] {
  let raw = "";
  try {
    raw = process.execFileSync("ps", ["-axo", "pid,pcpu,pmem,comm"]);
  } catch (e) {
    return [];
  }
  const lines = raw.split("\n");
  const out: Proc[] = [];
  for (let i = 1; i < lines.length; i++) {
    // "  PID %CPU %MEM COMM" — collapse runs of spaces, then split.
    const parts = lines[i].trim().split(/\s+/);
    if (parts.length < 4) continue;
    const pid = parseInt(parts[0], 10);
    if (!(pid > 0)) continue;
    const comm = parts.slice(3).join(" ");
    out.push({ pid, cpu: parseFloat(parts[1]), mem: parseFloat(parts[2]), comm });
  }
  out.sort((a, b) => (sort === "mem" ? b.mem - a.mem : b.cpu - a.cpu));
  return out;
}

// --- view -------------------------------------------------------------------

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

function view(s: State, cpuFrac: number) {
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

// --- app --------------------------------------------------------------------

let prev = sample();
const state: State = { procs: listProcs("cpu"), cursor: 0, sort: "cpu", confirming: false, tick: 0 };

// Clamp the cursor into range whenever the list changes size.
function clampCursor(): void {
  if (state.cursor < 0) state.cursor = 0;
  if (state.cursor >= state.procs.length) state.cursor = state.procs.length - 1;
  if (state.cursor < 0) state.cursor = 0;
}

enter();
render(view(state, 0));

if (!process.stdin.isTTY) {
  leave();
} else {
  process.stdin.setRawMode(true);
  let running = true;
  while (running) {
    const key: string = readKey(1500); // wake on a key OR after ~1.5s
    const code: number = key.length > 0 ? key.charCodeAt(0) : -1;

    if (s_isQuit(key, code)) {
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

// CPU utilisation since the previous sample; updates `prev` as a side effect.
function cpuFrac(): number {
  const cur = sample();
  const dTotal = cur.total - prev.total;
  const dIdle = cur.idle - prev.idle;
  prev = cur;
  return dTotal > 0 ? 1 - (dIdle * 1.0) / dTotal : 0;
}

// q or Ctrl-C quits.
function s_isQuit(key: string, code: number): boolean {
  return key === "q" || code === 3;
}
