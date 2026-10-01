import { setTimeout as sleep, setInterval as every, scheduler } from 'timers/promises';

// Promise timers: sleep resolves with its value after the delay, an
// AbortSignal cancels it, and setInterval is an async iterator that ticks
// until the loop breaks.
const greeting = await sleep(10, 'kalimera from Thessaloniki');
console.log(greeting);

const ac = new AbortController();
const slow = sleep(60_000, 'too late', { signal: ac.signal });
ac.abort();
try {
  await slow;
} catch (err: any) {
  console.log('cancelled:', err.name);
}

let ticks = 0;
for await (const _ of every(5)) {
  ticks++;
  if (ticks === 3) break;
}
console.log('ticks:', ticks);

await scheduler.wait(5);
console.log('done');
