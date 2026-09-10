// klaintop — the data layer: real OS data behind the view. CPU utilisation is
// the classic delta of busy-vs-idle jiffies between two samples of `os.cpus()`;
// memory comes from `os.totalmem`/`freemem`; the process table is read straight
// from the OS. Everything here is pure OS/parse code with no `klain:tui` — the
// view (view.ts) and loop (main.ts) sit on top of it.
//
// PORTABILITY (the interesting part). A process list has no single portable
// source, so `listProcs` branches on `process.platform` — the same "detect
// where you run, pick the right primitive" pattern any cross-platform native
// app needs:
//   • Linux  → read /proc directly. This is the universal Linux interface: it
//              works identically on glibc desktops, on Android/Termux, and on
//              BusyBox-based systems like Sailfish OS — none of which is true of
//              shelling out to `ps`, whose flags differ (BusyBox `ps` rejects
//              the BSD `-axo` selection). The lesson: prefer the portable
//              *interface* over a platform-specific *tool*.
//   • macOS  → BSD `ps -axo`, which has no /proc to read.
// Same compiled program, right behaviour on each OS — no runtime, no rewrite.

import { cpus, totalmem, freemem } from "os";
import { readFileSync, readdirSync } from "fs";
import { Proc } from "./types";

// Aggregate { total, idle } jiffies across every core.
export function sample(): { total: number; idle: number } {
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

// CPU utilisation between two jiffy samples: 1 - idle/total of the delta.
export function cpuUtilization(prev: { total: number; idle: number }, cur: { total: number; idle: number }): number {
  const dTotal = cur.total - prev.total;
  const dIdle = cur.idle - prev.idle;
  return dTotal > 0 ? 1 - (dIdle * 1.0) / dTotal : 0;
}

// totalmem()/freemem() are byte counts, so force floating-point division.
export function memFrac(): number {
  const total = totalmem();
  return total > 0 ? ((total - freemem()) * 1.0) / total : 0;
}

// Per-process CPU% is a rate, so it needs two samples: we remember each pid's
// busy jiffies (utime+stime) and the system-wide total jiffies from the last
// call, and report the ratio of the deltas. Module state, so `listProcs` keeps
// its simple signature and the first frame just reads 0% until the next tick.
let prevProcJiffies: Map<number, number> = new Map();
let prevTotalJiffies = 0;

// Snapshot every process, parsed into rows and sorted by `sort`. On any failure
// (missing source, odd output, a process vanishing mid-read) we skip that row
// rather than crash the loop.
export function listProcs(sort: string): Proc[] {
  let out: Proc[];
  if (process.platform === "linux") {
    out = listProcsProc();
  } else {
    out = listProcsPs();
  }
  out.sort((a, b) => (sort === "mem" ? b.mem - a.mem : b.cpu - a.cpu));
  return out;
}

// Linux: read /proc directly — works on every Linux flavour, BusyBox included.
function listProcsProc(): Proc[] {
  const ncpu = cpus().length;
  const totalMemBytes = totalmem();

  // System-wide busy+idle jiffies from /proc/stat's aggregate "cpu" line.
  let curTotal = 0;
  try {
    const statLine = readFileSync("/proc/stat", "utf8").split("\n")[0];
    const f = statLine.trim().split(/\s+/);
    for (let i = 1; i < f.length; i++) curTotal = curTotal + parseInt(f[i], 10);
  } catch (e) {
    return [];
  }
  const dTotal = curTotal - prevTotalJiffies;

  const out: Proc[] = [];
  const nextProcJiffies: Map<number, number> = new Map();
  let entries: string[] = [];
  try {
    entries = readdirSync("/proc");
  } catch (e) {
    return [];
  }

  for (let i = 0; i < entries.length; i++) {
    const name = entries[i];
    const pid = parseInt(name, 10);
    if (!(pid > 0) || String(pid) !== name) continue; // only numeric pid dirs

    let comm = "";
    let procJiffies = 0;
    try {
      // /proc/<pid>/stat: the command is wrapped in parens and may itself
      // contain spaces or ')'. A greedy `(.*)` up to the last `) ` captures
      // comm correctly; the fields after it start at `state` (overall field 3),
      // so utime/stime (fields 14/15) are indices 11/12 of that remainder.
      const stat = readFileSync("/proc/" + pid + "/stat", "utf8").trim();
      const m = stat.match(/^\d+ \((.*)\) (.+)$/);
      if (!m) continue;
      comm = m[1];
      const rest = m[2].split(/\s+/);
      procJiffies = parseInt(rest[11], 10) + parseInt(rest[12], 10);
    } catch (e) {
      continue; // process exited between readdir and here
    }

    // RSS from /proc/<pid>/status' VmRSS line, in kB — page-size-independent,
    // unlike the pages in /proc/<pid>/stat.
    let memPct = 0;
    try {
      const status = readFileSync("/proc/" + pid + "/status", "utf8");
      const m = status.match(/VmRSS:\s+(\d+) kB/);
      if (m && totalMemBytes > 0) memPct = (parseInt(m[1], 10) * 1024 * 100.0) / totalMemBytes;
    } catch (e) {
      // no VmRSS (kernel threads) — leave 0.
    }

    // CPU%: this pid's jiffy delta over the whole-system jiffy delta, scaled by
    // core count so a fully-busy single thread reads ~100% (top's Irix mode).
    const prev = prevProcJiffies.get(pid);
    const cpuPct = dTotal > 0 && prev !== undefined ? (100.0 * ncpu * (procJiffies - prev)) / dTotal : 0;
    nextProcJiffies.set(pid, procJiffies);

    out.push({ pid, cpu: cpuPct, mem: memPct, comm });
  }

  prevProcJiffies = nextProcJiffies;
  prevTotalJiffies = curTotal;
  return out;
}

// macOS (and other BSD-ps systems): the BSD `-axo` selection gives precomputed
// %CPU/%MEM, so no sampling state is needed here.
function listProcsPs(): Proc[] {
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
  return out;
}
