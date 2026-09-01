// klain:sync — select and channel range (TDD-00143 Stage 3).
//
// select() is Go's select: it runs whichever case is ready, or blocks until one
// fires (unless a defaultCase makes it non-blocking); when several are ready it
// picks pseudo-randomly. A recvCase's handler receives the value; a sendCase's
// runs once the value is taken. `for (const v of ch)` ranges a channel until it
// is closed and drained.
import { go, Channel, select, defaultCase } from 'klain:sync';

// --- select over two producers, non-blocking poll with a default ---------
const fast = new Channel<number>(1);
const slow = new Channel<number>(1);
fast.send(1); // make `fast` ready

select(
  fast.recvCase((v: number) => { console.log(`fast ready: ${v}`); }),
  slow.recvCase((v: number) => { console.log(`slow ready: ${v}`); }),
  defaultCase(() => { console.log("nothing ready"); }),
);

// --- a select-driven fan-in loop draining two goroutines -----------------
const a = new Channel<number>(0);
const b = new Channel<number>(0);
go(() => { for (let i = 0; i < 4; i++) a.send(i); });        // 0..3
go(() => { for (let i = 0; i < 4; i++) b.send(10 + i); });    // 10..13

let total = 0;
for (let k = 0; k < 8; k++) {
  select(
    a.recvCase((v: number) => { total += v; }),
    b.recvCase((v: number) => { total += v; }),
  );
}
console.log(`fan-in total = ${total}`); // (0+1+2+3) + (10+11+12+13) = 52

// --- channel range: a generator goroutine closes when done ---------------
const nums = new Channel<number>(0);
go(() => {
  for (let i = 1; i <= 5; i++) nums.send(i * i);
  nums.close(); // ends the range below
});

let squares = 0;
for (const v of nums) {
  squares += v;
}
console.log(`sum of squares 1..5 = ${squares}`); // 1+4+9+16+25 = 55
