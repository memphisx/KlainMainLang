// klainload — a real interactive HTTP load tester.
//
// Point it at any URL and it drives a pool of goroutine "agents" that hammer
// the endpoint with synchronous requests, for a fixed duration or until you
// stop it. Three screens: a config form (edit the target and parameters), a
// live dashboard (stoppable at any time), and a results screen whose report is
// also printed to stdout so it survives after you exit.
//
//   klainload                         # opens the config screen (in a terminal)
//   klainload http://host/path        # pre-fills the config screen
//   klainload http://host/path --run -c 32 -d 30
//                                     # skips the screen, runs 30s with 32 agents
//   klainload http://host/path -d inf # run until stopped ([s] in the dashboard)
//   klainload </dev/null              # non-TTY: headless, prints a text report
//
// Files, by concern: config.ts (CLI + Config), engine.ts (the agent goroutines),
// stats.ts (aggregation), report.ts (text summary), screens.ts (live + results
// TUI), configform.ts (the config screen). This file just wires them together.
import { Channel, select, defaultCase } from "klain:sync";
import { render, enter, leave } from "klain:tui";
import { readKey } from "klain:tty";
import { Config, parseArgs, usage } from "./loadtest/config";
import { Result, Stats, newStats, record, sampleRps } from "./loadtest/stats";
import { startRun } from "./loadtest/engine";
import { renderReport } from "./loadtest/report";
import { renderRunning, renderResults } from "./loadtest/screens";
import { FormState, newForm, handleKey, renderForm, formToConfig } from "./loadtest/configform";

// Drive one load run to completion, folding results into `stats`. When `live`
// is true the dashboard is painted and a keypress can stop the run early;
// returns whether it was stopped manually. Shared by the interactive and
// headless paths (headless passes live=false).
function runLoad(cfg: Config, stats: Stats, start: number, live: boolean): boolean {
  const results = new Channel<Result>(8192);
  const done = new Channel<number>(0);
  const stop = new Channel<number>(0);
  const headerBlob = cfg.headers.join("\n");
  startRun(cfg.concurrency, cfg.method, cfg.url, headerBlob, cfg.body, results, done, stop);

  const deadline = start + cfg.durationMs;
  let finished = 0;
  let stopped = false;
  let signaled = false;
  let lastRender = 0;

  while (finished < cfg.concurrency) {
    // Drain every result currently available without blocking.
    let draining = true;
    while (draining) {
      select(
        results.recvCase((r: Result) => { record(stats, r); }),
        done.recvCase((_: number) => { finished = finished + 1; }),
        defaultCase(() => { draining = false; }),
      );
    }

    const now = performance.now();
    if (!cfg.infinite && !signaled && now >= deadline) { stop.close(); signaled = true; }

    if (live) {
      const key: string = readKey(60); // pace ~60ms and poll for a stop key
      if (key !== "") {
        const code = key.charCodeAt(0);
        if (code === 3) { leave(); process.stdin.setRawMode(false); process.exit(130); }
        if ((key === "s" || key === "q" || key === "\x1b") && !signaled) {
          stop.close(); signaled = true; stopped = true;
        }
      }
      if (now - lastRender > 100) {
        sampleRps(stats, now);
        render(renderRunning(cfg, stats, now - start));
        lastRender = now;
      }
    }
  }

  // Drain whatever is still buffered behind the final done signals.
  let draining2 = true;
  while (draining2) {
    select(
      results.recvCase((r: Result) => { record(stats, r); }),
      defaultCase(() => { draining2 = false; }),
    );
  }
  return stopped;
}

function runInteractive(cfg: Config): void {
  enter();
  const start = performance.now();
  const stats: Stats = newStats(start);
  const stopped = runLoad(cfg, stats, start, true);
  const totalMs = performance.now() - start;
  render(renderResults(cfg, stats, totalMs, stopped));
  readKey(-1); // wait for a keystroke to dismiss
  leave();
  process.stdin.setRawMode(false);
  console.log(renderReport(cfg, stats, totalMs, stopped)); // persist to scrollback
  process.exit(0);
}

function runHeadless(cfg: Config): void {
  // Non-TTY: bound the run (never infinite) and, unless a duration was given
  // explicitly, keep it short so a scripted / `make examples` invocation stays
  // quick.
  const durMs = cfg.durationSet && !cfg.infinite ? cfg.durationMs : 2000;
  const c: Config = {
    url: cfg.url, method: cfg.method, concurrency: cfg.concurrency,
    durationMs: durMs, infinite: false,
    headers: cfg.headers, body: cfg.body, runNow: true, durationSet: true,
  };
  const start = performance.now();
  const stats: Stats = newStats(start);
  runLoad(c, stats, start, false);
  const totalMs = performance.now() - start;
  console.log(renderReport(c, stats, totalMs, false));
  process.exit(0);
}

// --- entry ---
const cfg = parseArgs(process.argv);
if (cfg.url === " help") {
  console.log(usage());
  process.exit(0);
}

let isTTY = false;
isTTY = process.stdin.isTTY;

if (isTTY) {
  process.stdin.setRawMode(true);
  if (cfg.runNow && cfg.url !== "") {
    runInteractive(cfg);
  } else {
    // Config screen first (pre-filled from any CLI args).
    enter();
    let form: FormState = newForm(cfg);
    let action = "editing";
    while (action === "editing") {
      render(renderForm(form));
      const k: string = readKey(-1);
      action = handleKey(form, k);
    }
    if (action === "quit") {
      leave();
      process.stdin.setRawMode(false);
      process.exit(0);
    }
    // leave() then re-enter inside runInteractive keeps one clean alt-screen.
    leave();
    runInteractive(formToConfig(form));
  }
} else {
  runHeadless(cfg);
}
