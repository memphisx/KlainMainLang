// screens — the live running dashboard and the results screen (klain:tui).
//
// Pure projections of a Config + Stats snapshot into a node tree. TUI colors
// must be string literals, so runtime color choices go through valueText.

import { Box, Text, Progress } from "klain:tui";
import { Config } from "./config";
import {
  Stats, successes, failures, successRate, pct, meanUs, fmtMicros, sparkline,
} from "./stats";

function valueText(value: string, color: string) {
  if (color === "cyan") return Text(value, { color: "cyan", bold: true });
  if (color === "green") return Text(value, { color: "green", bold: true });
  if (color === "yellow") return Text(value, { color: "yellow", bold: true });
  if (color === "red") return Text(value, { color: "red", bold: true });
  return Text(value, { color: "white", bold: true });
}

function stat(label: string, value: string, color: string) {
  return Box({ flexDirection: "column", width: 20 }, [
    Text(label, { color: "gray", dim: true }),
    valueText(value, color),
  ]);
}

function elapsedStr(elapsedMs: number): string {
  return (elapsedMs / 1000).toFixed(1) + "s";
}

function commonRows(cfg: Config, s: Stats, elapsedMs: number) {
  const secs = elapsedMs / 1000;
  const rps = secs > 0 ? Math.round(s.completed / secs) : 0;
  const rate = successRate(s);

  const statsRow = Box({ flexDirection: "row", gap: 2 }, [
    stat("throughput", rps + " rps", "cyan"),
    stat("requests", s.completed + "", "white"),
    stat("elapsed", elapsedStr(elapsedMs), "white"),
  ]);
  const okRow = Box({ flexDirection: "row", gap: 2 }, [
    stat("success", successes(s) + " (" + rate.toFixed(1) + "%)", "green"),
    stat("failed", failures(s) + " (" + (100 - rate).toFixed(1) + "%)", "red"),
  ]);
  const latRow = Box({ flexDirection: "row", gap: 2 }, [
    stat("p50", fmtMicros(pct(s, 0.5)), "green"),
    stat("p90", fmtMicros(pct(s, 0.9)), "yellow"),
    stat("p99", fmtMicros(pct(s, 0.99)), "red"),
  ]);
  const statusRow = Box({ flexDirection: "row", gap: 2 }, [
    Text("2xx " + s.c2xx, { color: "green" }),
    Text("3xx " + s.c3xx, { color: "cyan" }),
    Text("4xx " + s.c4xx, { color: "yellow" }),
    Text("5xx " + s.c5xx, { color: "red" }),
    Text("err " + s.errors, { color: "red" }),
  ]);
  return [statsRow, okRow, latRow, statusRow];
}

export function renderRunning(cfg: Config, s: Stats, elapsedMs: number) {
  const header = Box({ flexDirection: "row", justifyContent: "space-between" }, [
    Text("klainload", { color: "green", bold: true }),
    Text(cfg.concurrency + " agents", { color: "gray", dim: true }),
  ]);
  const target = Text(cfg.method + " " + cfg.url, { color: "cyan" });

  // Initialised with a node so its type is the tui-node type (an uninitialised
  // `let` would infer a non-node type and break the children array).
  let progress = Text("running (infinite) — press s to stop", { color: "gray", dim: true });
  if (cfg.infinite) {
    progress = Text("running (infinite) — press s to stop", { color: "gray", dim: true });
  } else {
    const frac = cfg.durationMs > 0 ? elapsedMs / cfg.durationMs : 0;
    const f = frac > 1 ? 1 : frac;
    progress = Box({ flexDirection: "row", gap: 1 }, [
      Progress(f, { color: "cyan", width: 34 }),
      Text(Math.round(f * 100) + "%", { color: "cyan", width: 5 }),
    ]);
  }

  const sparkRow = Box({ flexDirection: "row", gap: 1 }, [
    Text("rps", { color: "gray", width: 4 }),
    Text(sparkline(s), { color: "cyan" }),
    Text("peak " + s.peakRps, { color: "gray", dim: true }),
  ]);
  const footer = Text("[s] stop   ·   [Ctrl-C] quit", { color: "gray", dim: true });

  const rows = commonRows(cfg, s, elapsedMs);
  const children = [
    header, target,
    Box({ height: 1 }, []),
    progress,
    Box({ height: 1 }, []),
    rows[0], Box({ height: 1 }, []),
    rows[1], Box({ height: 1 }, []),
    rows[2], Box({ height: 1 }, []),
    rows[3], Box({ height: 1 }, []),
    sparkRow,
    Box({ height: 1 }, []),
    footer,
  ];
  return Box(
    { flexDirection: "column", width: 60, border: "round", borderColor: "cyan", padding: 1 },
    children,
  );
}

export function renderResults(cfg: Config, s: Stats, elapsedMs: number, stopped: boolean) {
  const title = stopped ? "results (stopped)" : "results";
  const header = Box({ flexDirection: "row", justifyContent: "space-between" }, [
    Text("klainload — " + title, { color: "green", bold: true }),
    Text(cfg.method + " " + cfg.url, { color: "gray", dim: true }),
  ]);

  const rate = successRate(s);
  const rateColor = rate >= 99.0 ? "green" : (rate >= 90.0 ? "yellow" : "red");
  const big = Box({ flexDirection: "row", gap: 2 }, [
    stat("total requests", s.completed + "", "white"),
    stat("success rate", rate.toFixed(1) + "%", rateColor),
  ]);

  const rows = commonRows(cfg, s, elapsedMs);
  const footer = Text("press any key to exit", { color: "green", bold: true });
  const children = [
    header,
    Box({ height: 1 }, []),
    big,
    Box({ height: 1 }, []),
    rows[0], Box({ height: 1 }, []),
    rows[1], Box({ height: 1 }, []),
    rows[2], Box({ height: 1 }, []),
    rows[3],
    Box({ height: 1 }, []),
    footer,
  ];
  return Box(
    { flexDirection: "column", width: 60, border: "round", borderColor: "green", padding: 1 },
    children,
  );
}
