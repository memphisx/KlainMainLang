// A concurrent prime counter — the flagship klain:sync showcase.
//
// This is a real fan-out / fan-in pipeline that counts the primes below N
// across a pool of goroutines running in parallel on every core, exactly the
// way you'd write it in Go:
//
//   - `go(fn)` spawns each worker onto the M:N work-stealing scheduler.
//   - a buffered `Channel<number>` streams every prime a worker finds back to
//     a single collector, and an unbuffered `Channel` signals completion.
//   - `select(...)` lets the collector wait on both channels at once — counting
//     primes as they arrive while tallying which workers have finished — and a
//     `defaultCase` drains the last buffered results without blocking.
//
// Because the scheduler is preemptive, no worker can starve the others, and the
// whole thing is a single native binary with no runtime or GC required.
import { go, Channel, select, defaultCase } from 'klain:sync';

const N = 500000;
const WORKERS = 8;

const primes = new Channel<number>(1024); // every prime found, streamed to the collector
const done = new Channel<number>(0);      // a worker signals when its slice is exhausted

function isPrime(n: number): boolean {
  if (n < 2) return false;
  if (n % 2 === 0) return n === 2;
  for (let d = 3; d * d <= n; d += 2) {
    if (n % d === 0) return false;
  }
  return true;
}

// Fan out: worker `id` tests the interleaved slice id, id+WORKERS, id+2·WORKERS…
// so the work is evenly spread. Each captures only top-level channels.
function spawnWorker(id: number): void {
  go(() => {
    for (let n = 2 + id; n <= N; n += WORKERS) {
      if (isPrime(n)) {
        primes.send(n);
      }
    }
    done.send(id);
  });
}

for (let w = 0; w < WORKERS; w++) {
  spawnWorker(w);
}

// Fan in: count primes as they stream in and tally worker completions. select
// takes whichever channel is ready; when every worker has reported done, drain
// any still-buffered primes with a non-blocking default and stop.
let count = 0;
let finished = 0;

while (finished < WORKERS) {
  select(
    primes.recvCase((p: number) => { count += 1; }),
    done.recvCase((id: number) => { finished += 1; }),
  );
}

let draining = true;
while (draining) {
  select(
    primes.recvCase((p: number) => { count += 1; }),
    defaultCase(() => { draining = false; }),
  );
}

console.log(`primes below ${N}: ${count}`);
console.log(`(found by ${WORKERS} goroutines running in parallel)`);
