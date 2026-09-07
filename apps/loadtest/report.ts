// report — the persistent text summary printed after a run.
//
// Used in headless mode and printed to stdout after the interactive TUI exits,
// so the numbers survive in the terminal scrollback (unlike the alt-screen
// dashboard, which vanishes on leave()).

import { Config } from "./config";
import {
  Stats, successes, failures, successRate, pct, meanUs, fmtMicros,
} from "./stats";

function pad(s: string, w: number): string {
  let out = s;
  while (out.length < w) out = out + " ";
  return out;
}

function bytesHuman(n: number): string {
  if (n >= 1048576) return (n / 1048576).toFixed(1) + " MiB";
  if (n >= 1024) return (n / 1024).toFixed(1) + " KiB";
  return n + " B";
}

export function renderReport(cfg: Config, s: Stats, elapsedMs: number, stopped: boolean): string {
  const secs = elapsedMs / 1000;
  const rps = secs > 0 ? Math.round(s.completed / secs) : 0;
  const rate = successRate(s);
  const dur = cfg.infinite ? "until stopped" : (cfg.durationMs / 1000).toFixed(0) + "s";
  const note = stopped ? "  (stopped early)" : "";

  let out = "";
  out += "klainload — " + cfg.method + " " + cfg.url + "\n";
  out += "  " + cfg.concurrency + " agents · " + dur + note + "\n";
  out += "\n";
  out += "Summary\n";
  out += "  " + pad("requests", 12) + s.completed + "\n";
  out += "  " + pad("duration", 12) + secs.toFixed(2) + "s\n";
  out += "  " + pad("throughput", 12) + rps + " req/s\n";
  out += "  " + pad("success", 12) + successes(s) + " (" + rate.toFixed(1) + "%)\n";
  out += "  " + pad("failed", 12) + failures(s) + " (" + (100 - rate).toFixed(1) + "%)\n";
  out += "\n";
  out += "Latency\n";
  out += "  " + pad("mean", 8) + fmtMicros(meanUs(s)) +
    "   min " + fmtMicros(s.minUs < 0 ? 0 : s.minUs) +
    "   max " + fmtMicros(s.maxUs) + "\n";
  out += "  " + pad("p50", 8) + fmtMicros(pct(s, 0.5)) +
    "   p90 " + fmtMicros(pct(s, 0.9)) +
    "   p99 " + fmtMicros(pct(s, 0.99)) + "\n";
  out += "\n";
  out += "Status codes\n";
  out += "  2xx " + s.c2xx + "   3xx " + s.c3xx + "   4xx " + s.c4xx +
    "   5xx " + s.c5xx + "   errors " + s.errors + "\n";
  out += "\n";
  out += "Transfer\n";
  out += "  " + bytesHuman(s.bytes) + " total";
  if (secs > 0) out += "   (" + bytesHuman(Math.round(s.bytes / secs)) + "/s)";
  out += "\n";
  return out;
}
