// stats — the per-request result shape and the aggregated statistics.
//
// Workers produce a `Result` per request (engine.ts); the collector folds each
// into a `Stats` value by reference. Stats holds a log-bucketed latency
// histogram plus success/failure tallies by HTTP status class and a rolling
// throughput window for the live sparkline. All derived numbers (percentiles,
// mean, success rate) are read-only functions over a Stats.

export const NBUCKETS: number = 28; // bucket b holds [2^b, 2^(b+1)) microseconds

// One completed request. `status` is the HTTP status, or 0 for a connection /
// network failure (synchronous XHR reports that as status 0, never a throw).
export interface Result {
  latencyUs: number;
  status: number;
  bytes: number;
}

export interface Stats {
  hist: number[];        // latency histogram
  completed: number;
  minUs: number;         // -1 until the first result
  maxUs: number;
  sumUs: number;         // for the mean
  bytes: number;         // total response bytes
  // status-class tallies
  errors: number;        // status 0 — couldn't connect / transport error
  c1xx: number;
  c2xx: number;
  c3xx: number;
  c4xx: number;
  c5xx: number;
  // rolling throughput window (for the sparkline)
  rpsSamples: number[];
  lastCompleted: number;
  lastStamp: number;
  peakRps: number;
}

export function newStats(startStamp: number): Stats {
  const hist: number[] = [];
  for (let i = 0; i < NBUCKETS; i++) hist.push(0);
  return {
    hist: hist,
    completed: 0,
    minUs: -1,
    maxUs: 0,
    sumUs: 0,
    bytes: 0,
    errors: 0,
    c1xx: 0,
    c2xx: 0,
    c3xx: 0,
    c4xx: 0,
    c5xx: 0,
    rpsSamples: [],
    lastCompleted: 0,
    lastStamp: startStamp,
    peakRps: 0,
  };
}

function bucketOf(micros: number): number {
  let b = 0;
  let v = micros;
  while (v > 1 && b < NBUCKETS - 1) { v = Math.floor(v / 2); b += 1; }
  return b;
}

export function record(s: Stats, r: Result): void {
  s.hist[bucketOf(r.latencyUs)] += 1;
  s.completed += 1;
  s.sumUs += r.latencyUs;
  s.bytes += r.bytes;
  if (r.latencyUs > s.maxUs) s.maxUs = r.latencyUs;
  if (s.minUs < 0 || r.latencyUs < s.minUs) s.minUs = r.latencyUs;

  const st = r.status;
  if (st === 0) s.errors += 1;
  else if (st < 200) s.c1xx += 1;
  else if (st < 300) s.c2xx += 1;
  else if (st < 400) s.c3xx += 1;
  else if (st < 500) s.c4xx += 1;
  else s.c5xx += 1;
}

// A request counts as a success if it came back 2xx or 3xx; everything else —
// 4xx, 5xx, and connection errors — is a failure.
export function successes(s: Stats): number { return s.c2xx + s.c3xx; }
export function failures(s: Stats): number { return s.c1xx + s.c4xx + s.c5xx + s.errors; }
export function successRate(s: Stats): number {
  if (s.completed === 0) return 0;
  return (successes(s) * 100.0) / s.completed;
}

// The pth-percentile latency, read off the cumulative histogram (2^b lower edge).
export function pct(s: Stats, p: number): number {
  const target = p * s.completed;
  let cum = 0;
  for (let b = 0; b < NBUCKETS; b++) {
    cum += s.hist[b];
    if (cum >= target) {
      let v = 1;
      for (let i = 0; i < b; i++) v *= 2;
      return v;
    }
  }
  return s.maxUs;
}

export function meanUs(s: Stats): number {
  if (s.completed === 0) return 0;
  return Math.round(s.sumUs / s.completed);
}

export function fmtMicros(us: number): string {
  if (us >= 1000000) return (us / 1000000).toFixed(2) + "s";
  if (us >= 1000) return (us / 1000).toFixed(1) + "ms";
  return Math.round(us) + "µs";
}

// Fold requests completed since the last sample into a per-second rate and push
// it onto the rolling sparkline window.
export function sampleRps(s: Stats, now: number): void {
  const dt = now - s.lastStamp;
  if (dt <= 0) return;
  const rps = Math.round(((s.completed - s.lastCompleted) * 1000) / dt);
  s.rpsSamples.push(rps);
  if (s.rpsSamples.length > 32) s.rpsSamples.shift();
  if (rps > s.peakRps) s.peakRps = rps;
  s.lastCompleted = s.completed;
  s.lastStamp = now;
}

export function sparkline(s: Stats): string {
  const sparkChars = ["▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"];
  if (s.rpsSamples.length === 0) return "";
  let hi = 1;
  for (let i = 0; i < s.rpsSamples.length; i++) if (s.rpsSamples[i] > hi) hi = s.rpsSamples[i];
  let out = "";
  for (let i = 0; i < s.rpsSamples.length; i++) {
    let idx = Math.floor((s.rpsSamples[i] * 7) / hi);
    if (idx < 0) idx = 0;
    if (idx > 7) idx = 7;
    out += sparkChars[idx];
  }
  return out;
}
