package tests

import "testing"

// ADR-01071: setTimeout/setInterval clamp a delay below 1 (or omitted, or above
// 2^31-1) to 1 ms, as Node does — 300 chained zero-delay timers take at least
// ~300 ms, not a burst. setImmediate is not clamped.
func TestE2ETimerZeroDelayClamp(t *testing.T) {
	const src = `
const t0 = Date.now();
let n = 0;
function step(): void {
  n++;
  if (n < 300) setTimeout(step, 0);
  else console.log("chain", Date.now() - t0 >= 250 ? "clamped" : "burst");
}
setTimeout(step);
let k = 0;
const iv = setInterval(() => { if (++k === 5) { clearInterval(iv); console.log("interval", k); } }, 0);
setTimeout(() => console.log("big delay clamped"), 3000000000);
`
	assertOutput(t, src, "big delay clamped\ninterval 5\nchain clamped")
	assertSameAsNode(t, src)
}
