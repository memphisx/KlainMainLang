// setTimeout / setInterval clamp their delay to [1, 2^31-1] ms exactly as Node
// does: an omitted, zero, negative or oversized delay becomes 1 ms. A chain of
// "zero-delay" timers therefore advances one millisecond per link — the
// cadence every Node polling loop relies on — instead of firing in a burst.

const t0 = Date.now();
let n = 0;
function step(): void {
  n++;
  if (n < 200) setTimeout(step, 0);
  else console.log("200 links took at least 150 ms:", Date.now() - t0 >= 150);
}
setTimeout(step);                                            // no delay → 1 ms
setTimeout(() => console.log("huge delay is 1 ms too"), 3000000000);
