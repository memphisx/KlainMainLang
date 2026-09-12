// klainconf — a live dashboard for conformance runs, in the terminal. The
// conformance tool writes one `progress-<suite>.txt` line per suite into its
// scratch dir (see docs/testing/PROGRESS-DASHBOARD.md); this app watches
// those files and renders a row per suite: lane, progress bar, done/total,
// pass/fail/skip, files/sec, ETA — and a STALL marker when a suite's line
// stops advancing, answering "is it wedged or just slow?" at a glance.
//
//   ./klainconf                    # watch .conformance-out/ (1s refresh, q quits)
//   ./klainconf some/scratch/dir   # watch a non-default -workdir
//   ./klainconf </dev/null         # non-TTY: print one snapshot and exit
//
// Read-only and decoupled from the run: start it before, during, or after,
// in any terminal or tmux pane; several viewers can watch one run.

import { render, enter, leave, Box, Text, Progress } from "klain:tui";
import { readKey } from "klain:tty";
import fs from "fs";
import { spawn } from "child_process";

class Row {
  suite: string = "";
  reasonsPath: string = "";
  dir: string = "";
  lane: string = "";
  done: number = 0;
  total: number = 0;
  pass: number = 0;
  fail: number = 0;
  skip: number = 0;
  started: number = 0;
  updated: number = 0;
}

const dir: string = process.argv[1] ?? ".conformance-out";

// ── controller state (docs/testing/PROGRESS-DASHBOARD.md) ──────────────────
// One suite at a time — conformance runs are isolation-sensitive (concurrent
// CPU load manufactures RUN_TIMEOUT jitter), so the controller enforces it.
const SUITES: string[] = ["test262", "node", "ts", "wpt"];
let runningSuite: string = "";
let runningPid: number = 0;
let pendingRestart: string = "";
let message: string = "";

function alive(pid: number): boolean {
  if (pid <= 0) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch (e) {
    return false;
  }
}

// Launch one suite (both lanes). `sh` builds the runner then execs it, so the
// child pid IS the runner (kill reaches the real process, not a wrapper) and
// its output goes to a log file instead of a pipe nobody drains.
// Delete a suite's telemetry files everywhere in the scratch dir, so a fresh
// start begins with clean rows instead of a dead run's leftovers.
function purgeSuiteTelemetry(suite: string): void {
  if (!fs.existsSync(dir)) return;
  for (const name of fs.readdirSync(dir)) {
    const sub: string = dir + "/" + name;
    const targets: string[] = [];
    if ((name.startsWith("progress-" + suite + "-") || name.startsWith("reasons-" + suite + "-")) && name.endsWith(".txt")) {
      targets.push(sub);
    } else if (name.startsWith("run-")) {
      for (const inner of fs.readdirSync(sub)) {
        if ((inner.startsWith("progress-" + suite + "-") || inner.startsWith("reasons-" + suite + "-")) && inner.endsWith(".txt")) {
          targets.push(sub + "/" + inner);
        }
      }
    }
    for (const t of targets) {
      try {
        fs.unlinkSync(t);
      } catch (e) {
        // another process may hold it — harmless, newest-run filtering copes
      }
    }
  }
}

let lastStopped: string = "";

function startSuite(suite: string): void {
  if (!fs.existsSync("tools/conformance")) {
    message = "start needs the repo root as cwd (tools/conformance/ not found)";
    return;
  }
  if (runningSuite !== "" && alive(runningPid)) {
    message = "refusing: " + runningSuite + " is still running (suites run isolated, one at a time)";
    return;
  }
  const bin: string = dir + "/klainconf-runner";
  const log: string = dir + "/klainconf-" + suite + ".log";
  const cmd: string = "go build -o " + bin + " ./tools/conformance && exec " + bin + " -suite=" + suite + " -compat=both > " + log + " 2>&1";
  purgeSuiteTelemetry(suite);
  if (lastStopped === suite) lastStopped = "";
  const child = spawn("sh", ["-c", cmd]);
  runningSuite = suite;
  runningPid = child.pid;
  message = "started " + suite + " (both lanes, pid " + child.pid + ", log " + log + ")";
}

function stopSuite(suite: string): void {
  if (suite !== runningSuite || !alive(runningPid)) {
    message = suite + " is not running under this dashboard";
    return;
  }
  try {
    process.kill(runningPid);
    lastStopped = suite;
    message = "stopped " + suite + " (pid " + runningPid + ")";
  } catch (e) {
    message = "kill failed for pid " + runningPid;
  }
}

// Called each tick: reap a finished/killed child and fire a pending restart.
function reapAndRestart(): void {
  if (runningSuite !== "" && !alive(runningPid)) {
    const finished: string = runningSuite;
    runningSuite = "";
    runningPid = 0;
    if (pendingRestart !== "") {
      const again: string = pendingRestart;
      pendingRestart = "";
      startSuite(again);
    } else if (message.startsWith("started " + finished)) {
      message = finished + " finished";
    }
  }
}

function parseRow(path: string): Row | undefined {
  const line: string = fs.readFileSync(path, "utf8").trim();
  const rp: string = path.replace("progress-", "reasons-");
  const slash: number = path.lastIndexOf("/");
  const fileDir: string = slash >= 0 ? path.slice(0, slash) : ".";
  const f: string[] = line.split("|");
  if (f.length < 9) return undefined;
  const r = new Row();
  r.suite = f[0];
  r.lane = f[1];
  r.done = Number(f[2]);
  r.total = Number(f[3]);
  r.pass = Number(f[4]);
  r.fail = Number(f[5]);
  r.skip = Number(f[6]);
  r.started = Number(f[7]);
  r.updated = Number(f[8]);
  r.reasonsPath = rp;
  r.dir = fileDir;
  return r;
}

// Progress files live both at the scratch root and inside per-process
// `run-<pid>/` subdirs; old runs leave stale files behind, so keep only the
// most recently updated line per (suite, lane) — a both-lanes run shows as
// two rows, strict staying visible while js runs.
function readRows(): Row[] {
  const all: Row[] = [];
  if (!fs.existsSync(dir)) return [];
  const names: string[] = fs.readdirSync(dir);
  names.sort();
  for (const name of names) {
    const paths: string[] = [];
    if (name.startsWith("progress-") && name.endsWith(".txt")) {
      paths.push(dir + "/" + name);
    } else if (name.startsWith("run-")) {
      const sub: string = dir + "/" + name;
      for (const inner of fs.readdirSync(sub)) {
        if (inner.startsWith("progress-") && inner.endsWith(".txt")) paths.push(sub + "/" + inner);
      }
    }
    for (const p of paths) {
      const r = parseRow(p);
      if (r === undefined) continue;
      all.push(r);
    }
  }
  // A suite's rows must all come from ONE invocation — the run dir holding
  // its most recently updated progress file — or a killed run's leftover in
  // one dir mixes with an older finished run in another (one lane "done",
  // the other stalled: incoherent).
  const newestDir = new Map<string, string>();
  const newestUpd = new Map<string, number>();
  for (const r of all) {
    const u = newestUpd.get(r.suite);
    if (u === undefined || r.updated > u) {
      newestUpd.set(r.suite, r.updated);
      newestDir.set(r.suite, r.dir);
    }
  }
  const best = new Map<string, Row>();
  for (const r of all) {
    if (newestDir.get(r.suite) !== r.dir) continue;
    const key: string = r.suite + "|" + r.lane;
    const prev = best.get(key);
    if (prev === undefined || r.updated >= prev.updated) best.set(key, r);
  }
  const rows: Row[] = [];
  for (const r of best.values()) rows.push(r);
  for (const suite of SUITES) {
    let seen = false;
    for (const r of rows) {
      if (r.suite === suite) seen = true;
    }
    if (!seen) {
      const ph = new Row();
      ph.suite = suite;
      ph.lane = "-";
      rows.push(ph);
    }
  }
  rows.sort((a: Row, b: Row) => {
    const ka: string = a.suite + "|" + a.lane;
    const kb: string = b.suite + "|" + b.lane;
    return ka < kb ? -1 : 1;
  });
  return rows;
}

function pad(s: string, w: number): string {
  if (s.length >= w) return s.slice(0, w);
  return s + " ".repeat(w - s.length);
}
function lpad(s: string, w: number): string {
  if (s.length >= w) return s;
  return " ".repeat(w - s.length) + s;
}

function fmtEta(seconds: number): string {
  if (seconds < 0) return "--:--";
  const m = Math.floor(seconds / 60);
  const s = Math.floor(seconds % 60);
  const ss: string = s < 10 ? "0" + s : "" + s;
  if (m >= 60) {
    const h = Math.floor(m / 60);
    const mm = m % 60;
    const mms: string = mm < 10 ? "0" + mm : "" + mm;
    return h + "h" + mms + "m";
  }
  return m + ":" + ss;
}

function suiteRow(r: Row, now: number, selected: boolean) {
  const frac: number = r.total > 0 ? r.done / r.total : 0;
  const elapsed: number = r.updated - r.started;
  const rate: number = elapsed > 0 ? r.done / elapsed : 0;
  const eta: number = rate > 0 ? (r.total - r.done) / rate : -1;
  const stale: number = now - r.updated;
  const running: boolean = r.done < r.total;

  // klain:tui colors must be string literals, so each status/bar variant is
  // its own component call rather than a computed color.
  let statusText = Text("done", { color: "green" });
  if (r.total === 0) {
    statusText = Text("not run yet — press s", { color: "gray" });
  } else if (running) {
    if (r.suite === runningSuite && alive(runningPid) && stale <= 15) {
      statusText = Text(rate.toFixed(1) + "/s ETA " + fmtEta(eta) + "  [pid " + runningPid + "]", { color: "yellow" });
    } else if (stale > 15 && r.suite === lastStopped) {
      statusText = Text("stopped", { color: "gray" });
    } else if (stale > 15) {
      statusText = Text("STALL " + Math.floor(stale) + "s", { color: "red" });
    } else {
      statusText = Text(rate.toFixed(1) + "/s ETA " + fmtEta(eta), { color: "yellow" });
    }
  }

  // Result-proportion bar: green █ = passing, red █ = failed, blue █ = skipped
  // (processed, out of scope), gray ░ = not run yet. Widths are rounded but
  // any nonzero class keeps at least one cell so small counts stay visible.
  const barW = 24;
  let greenW: number = r.total > 0 ? Math.round((barW * r.pass) / r.total) : 0;
  let redW: number = r.total > 0 ? Math.round((barW * r.fail) / r.total) : 0;
  let skipW: number = r.total > 0 ? Math.round((barW * r.skip) / r.total) : 0;
  if (r.pass > 0 && greenW === 0) greenW = 1;
  if (r.fail > 0 && redW === 0) redW = 1;
  if (r.skip > 0 && skipW === 0) skipW = 1;
  while (greenW + redW + skipW > barW) {
    if (skipW > 1) skipW = skipW - 1;
    else if (redW > 1) redW = redW - 1;
    else greenW = greenW - 1;
  }
  if (!running && greenW + redW + skipW < barW) skipW = barW - greenW - redW; // finished: no gray remainder
  const restW: number = barW - greenW - redW - skipW;
  const bar = Box({ flexDirection: "row" }, [
    Text("█".repeat(greenW), { color: "green" }),
    Text("█".repeat(redW), { color: "red" }),
    Text("█".repeat(skipW), { color: "blue" }),
    Text("░".repeat(restW), { color: "gray" }),
  ]);

  const marker = selected ? Text("▸", { color: "magenta" }) : Text(" ", {});
  return Box({ flexDirection: "row", gap: 1 }, [
    marker,
    Text(pad(r.suite, 8), { color: "cyan" }),
    Text(pad(r.lane, 6), { color: "gray" }),
    bar,
    Text(lpad("" + r.done, 6) + "/" + r.total, { width: 13 }),
    Text(lpad("" + r.pass, 6) + " pass", { color: "green" }),
    Text(lpad("" + r.fail, 6) + " fail", { color: "red" }),
    Text(lpad("" + r.skip, 6) + " skip", { color: "blue" }),
    statusText,
  ]);
}

class Reason {
  n: number = 0;
  text: string = "";
}

function readReasons(path: string): Reason[] {
  const out: Reason[] = [];
  if (!fs.existsSync(path)) return out;
  const lines: string[] = fs.readFileSync(path, "utf8").split("\n");
  for (const line of lines) {
    const i: number = line.indexOf("|");
    if (i <= 0) continue;
    const r = new Reason();
    r.n = Number(line.slice(0, i));
    r.text = line.slice(i + 1);
    out.push(r);
  }
  return out;
}

function view(rows: Row[], cursor: number, details: boolean, tick: number) {
  const now: number = Math.floor(Date.now() / 1000);
  const children = [
    Box({ flexDirection: "row", gap: 1 }, [
      Text("klainconf", { color: "magenta", bold: true }),
      Text("— " + dir + "  (\u2191/\u2193 select \u00b7 d details \u00b7 s start \u00b7 x stop \u00b7 r restart \u00b7 q quits)", { color: "gray" }),
    ]),
    Text("", {}),
  ];
  if (message !== "") {
    children.push(Text(message, { color: "yellow" }));
    children.push(Text("", {}));
  }
  for (let i = 0; i < rows.length; i++) {
    children.push(suiteRow(rows[i], now, i === cursor));
  }
  if (details && cursor >= 0 && cursor < rows.length) {
    const sel: Row = rows[cursor];
    children.push(Text("", {}));
    children.push(Text("top failure/skip reasons — " + sel.suite + " (" + sel.lane + "), live from this run:", { color: "magenta" }));
    const reasons: Reason[] = readReasons(sel.reasonsPath);
    if (reasons.length === 0) {
      children.push(Text("  (none recorded yet)", { color: "gray" }));
    }
    let shown = 0;
    for (const re of reasons) {
      if (shown >= 10) break;
      shown = shown + 1;
      children.push(Box({ flexDirection: "row", gap: 1 }, [
        Text(lpad("" + re.n, 6), { color: "yellow" }),
        Text(re.text, {}),
      ]));
    }
  }
  return Box({ flexDirection: "column", padding: 1 }, children);
}

let rows: Row[] = readRows();
let cursor = 0;
let details = false;
enter();
render(view(rows, cursor, details, 0));

function clampCursor(): void {
  if (cursor >= rows.length) cursor = rows.length - 1;
  if (cursor < 0) cursor = 0;
}

if (!process.stdin.isTTY) {
  leave();
} else {
  process.stdin.setRawMode(true);
  process.on("SIGWINCH", () => {
    render(view(rows, cursor, details, 0));
  });
  let running = true;
  let tick = 0;
  while (running) {
    const key: string = readKey(1000);
    const code: number = key.length > 0 ? key.charCodeAt(0) : -1;
    if (key === "q" || code === 3) {
      running = false;
    } else if (key === "\x1b[A") {
      cursor = cursor - 1;
      clampCursor();
      render(view(rows, cursor, details, tick));
    } else if (key === "\x1b[B") {
      cursor = cursor + 1;
      clampCursor();
      render(view(rows, cursor, details, tick));
    } else if (key === "d" || key === "\r" || key === "\n") {
      details = !details;
      render(view(rows, cursor, details, tick));
    } else if (key === "s") {
      if (cursor >= 0 && cursor < rows.length) startSuite(rows[cursor].suite);
      render(view(rows, cursor, details, tick));
    } else if (key === "x") {
      if (cursor >= 0 && cursor < rows.length) stopSuite(rows[cursor].suite);
      render(view(rows, cursor, details, tick));
    } else if (key === "r") {
      if (cursor >= 0 && cursor < rows.length) {
        const suite: string = rows[cursor].suite;
        if (suite === runningSuite && alive(runningPid)) {
          pendingRestart = suite;
          stopSuite(suite);
          message = "restarting " + suite + " ...";
        } else {
          startSuite(suite);
        }
      }
      render(view(rows, cursor, details, tick));
    } else {
      tick = tick + 1;
      reapAndRestart();
      rows = readRows();
      clampCursor();
      render(view(rows, cursor, details, tick));
    }
  }
  leave();
}
