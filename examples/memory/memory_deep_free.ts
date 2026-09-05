// -mm=auto deep reclamation (TDD-00175 Stage 1) — type-directed deep free
// for transitively-owned typed JSON.parse trees. Compile with:
//
//   klainmain -mm=auto examples/memory/memory_deep_free.ts
//
// The implicit auto layer's block-exit free is shallow: it releases a
// binding's own top-level allocation (an array's data buffer, an object's
// struct) and nothing reachable through it. For a binding whose static type
// is a tree of freeable types AND whose flow provably keeps every interior
// pointer inside the block, the compiler instead synthesizes a recursive
// per-type free routine and reclaims the whole graph: per-row objects,
// their strings, their nested array buffers.
//
// Stage 1 covers typed JSON.parse / res.json() trees — graphs that are
// fully fresh by construction (every projected string is a heap copy).
// Extracting an interior pointer into a longer-lived binding, mutating the
// graph, iterating it, or passing it to a call the analysis hasn't audited
// simply downgrades the binding to the shallow free — never an error, and
// never a use-after-free.
//
// This file also compiles and runs identically under the default manual
// mode (that's what `make examples` does) — inserted frees are never
// observable.

interface Reading {
  sensor: string;
  city: string;
  values: number[];
}

function sample(): string {
  const rows: Reading[] = [
    { sensor: "temp-1", city: "Thessaloniki", values: [21, 22, 23] },
    { sensor: "hum-1", city: "Thessaloniki", values: [40, 41] },
  ];
  return JSON.stringify(rows);
}

function churn(rounds: number): number {
  let checksum = 0;
  for (let r = 0; r < rounds; r++) {
    // `as Reading[]` supplies the projection target (exactly as a
    // `const parsed: Reading[] = ...` annotation would), and the whole
    // parsed graph — row structs, sensor/city strings, values buffers —
    // is deep-freed at each iteration's block exit.
    const parsed = JSON.parse(sample()) as Reading[];
    checksum += parsed.length;
    checksum += parsed[0].values[2];
  }
  return checksum;
}

console.log("checksum: " + churn(1000));
