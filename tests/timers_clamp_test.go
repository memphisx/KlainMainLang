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

// No timer fires before its delay: one-shot timers of several delays, one
// armed from inside another's callback, and an interval's successive ticks,
// each measured against the monotonic clock from the moment it was armed.
// libuv counts whole milliseconds from the loop's cached time, so Node itself
// may fire up to 1 ms early by performance.now(): the bound is delay - 1.
// Checked again on a throttled CPU (throttledCommand). Never counts ticks.
func TestE2ETimerNeverFiresEarly(t *testing.T) {
	const src = `
const early: string[] = [];
function arm(label: string, delay: number, then?: () => void): void {
  const t0 = performance.now();
  setTimeout(() => {
    const dt = performance.now() - t0;
    if (dt < delay - 1) early.push(label + " " + dt.toFixed(2) + "<" + delay);
    if (then) then();
  }, delay);
}
for (const d of [1, 5, 17, 40, 90]) arm("t" + d, d);
arm("outer", 10, () => arm("inner", 30));
let k = 0;
const i0 = performance.now();
const iv = setInterval(() => {
  k++;
  const dt = performance.now() - i0;
  if (dt < k * 20 - 1) early.push("tick" + k + " " + dt.toFixed(2) + "<" + k * 20);
  if (k === 5) clearInterval(iv);
}, 20);
setTimeout(() => console.log(early.length === 0 ? "none early" : "early: " + early.join(", ")), 200);
`
	assertOutput(t, src, "none early")
	assertSameAsNode(t, src)
	bin := buildBinary(t, src)
	if cmd := throttledCommand(bin); cmd != nil {
		out, err := cmd.CombinedOutput()
		if err != nil || string(out) != "none early\n" {
			t.Fatalf("throttled %v: %v\n%s", cmd.Args, err, out)
		}
	}
}
