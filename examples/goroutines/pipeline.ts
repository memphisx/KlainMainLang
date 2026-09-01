// klain:sync — Go-style goroutines and CSP channels (TDD-00143, Stage 1).
//
// An explicitly-non-Node opt-in: `go` spawns a cheap goroutine onto a
// preemptive M:N work-stealing scheduler, and a Channel<T> is a Go channel —
// a blocking send/receive that parks the *goroutine* (not the OS thread), as
// an unbuffered rendezvous or a buffered ring.
//
// Two patterns in one program, both running concurrently across cores:
//
//   1. A fan-out worker pool: a spawn() helper launches WORKERS goroutines,
//      each squaring one slice of the inputs and sending results over a shared
//      buffered channel; main fans the results in. Because a top-level channel
//      is a module global, the helper sees it with its type intact, and each
//      worker's slice base arrives as a function parameter (a fresh binding).
//
//   2. A three-stage pipeline (generate -> square -> sum) as three goroutines
//      handing values along unbuffered channels as rendezvous.
import { go, Channel } from 'klain:sync';

// --- Pattern 1: fan-out worker pool -------------------------------------
const WORKERS = 4;
const PER = 25;
const results = new Channel<number>(64);

function spawn(base: number): void {
  go(() => {
    for (let i = 0; i < PER; i++) {
      const n = base * PER + i;
      results.send(n * n);
    }
  });
}

for (let w = 0; w < WORKERS; w++) spawn(w);

let poolSum = 0;
for (let i = 0; i < WORKERS * PER; i++) poolSum += results.receive();

let poolExpect = 0;
for (let n = 0; n < WORKERS * PER; n++) poolExpect += n * n;
console.log(`pool: ${WORKERS} workers, sum of squares = ${poolSum} ${poolSum === poolExpect ? "ok" : "FAIL"}`);

// --- Pattern 2: three-stage CSP pipeline --------------------------------
const N = 20;
const nums = new Channel<number>(0);
const squares = new Channel<number>(0);

go(() => { for (let i = 1; i <= N; i++) nums.send(i); });
go(() => { for (let i = 0; i < N; i++) { const n = nums.receive(); squares.send(n * n); } });

let pipeSum = 0;
for (let i = 0; i < N; i++) pipeSum += squares.receive();
console.log(`pipeline: sum of squares 1..${N} = ${pipeSum} ${pipeSum === 2870 ? "ok" : "FAIL"}`);
