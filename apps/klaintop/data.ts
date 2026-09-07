// klaintop — the data layer: real OS data behind the view. CPU utilisation is
// the classic delta of busy-vs-idle jiffies between two samples of `os.cpus()`;
// memory comes from `os.totalmem`/`freemem`; the process table is `ps` parsed
// in-process. Everything here is pure OS/parse code with no `klain:tui` — the
// view (view.ts) and loop (main.ts) sit on top of it.

import { cpus, totalmem, freemem } from "os";
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

// Snapshot every process via `ps`, parsed into rows and sorted by `sort`. The
// BSD-style `-axo` selection works on both macOS and Linux; we sort in-process
// rather than with a platform-specific `--sort` flag. On any failure (no ps,
// odd output) we return an empty list rather than crash the loop.
export function listProcs(sort: string): Proc[] {
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
