package tests

import (
	"os/exec"
	"regexp"
	"strconv"
	"testing"
)

// timer_precision_windows_test.go — TDD-00183 Stage 4. The Windows event loop's
// waits used to inherit the system timer resolution (~15.6 ms), so a 50 ms
// setInterval fired every ~62 ms and twenty ticks took ~1250 ms. QPC deadlines
// plus a high-resolution waitable timer (ADR-01024) bring it to the real
// period. The loop has three distinct waits, and each is covered, because a fix
// to one does not reach the others: a timers-only program sleeps in
// nanosleep(); with a signal handler installed nanosleep() sleeps in slices
// between signal checks; and once any descriptor is open the loop waits in the
// reactor's select(). This is a coarse guard, not a tight timing test: the
// pre-fix value was ~1250 ms and the post-fix value ~1010 ms on the dev box, so
// a 1150 ms ceiling catches the regression with margin, and a 950 ms floor
// catches an interval that fires too early. Windows-only: POSIX timers were
// always precise, so guarding them here would only add CI jitter.

var elapsedRe = regexp.MustCompile(`elapsed=(\d+)`)

const timerPrecisionTicks = `
  const t0 = Date.now()
  let ticks = 0
  const iv = setInterval(() => {
    ticks = ticks + 1
    if (ticks === 20) {
      clearInterval(iv)
      console.log("elapsed=" + (Date.now() - t0))
      done()
    }
  }, 50)
`

func TestE2ETimerIntervalResolution(t *testing.T) {
	cases := []struct{ name, src string }{
		{"timers-only", `
function done(): void {}
` + timerPrecisionTicks},
		{"signal-handler-installed", `
process.on("SIGINT", () => { console.log("sig") })
function done(): void { process.exit(0) }
` + timerPrecisionTicks},
		{"reactor-select", `
import * as http from "node:http"
const server = http.createServer((req, res) => { res.end("ok") })
function done(): void { server.close() }
server.listen(0, () => {` + timerPrecisionTicks + `})
`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bin := buildBinaryImports(t, c.src)
			out, err := exec.Command(bin).CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			m := elapsedRe.FindSubmatch(out)
			if m == nil {
				t.Fatalf("no elapsed=N in output:\n%s", out)
			}
			ms, _ := strconv.Atoi(string(m[1]))
			// 20 timers of 50 ms run back to back: the ideal is 1000 ms.
			if ms < 950 {
				t.Errorf("twenty 50ms intervals finished in %d ms (< 950): the interval is firing early", ms)
			}
			if ms > 1150 {
				t.Errorf("twenty 50ms intervals took %d ms (> 1150): the wait is quantised to the ~15.6ms system tick, not the real deadline", ms)
			}
		})
	}
}
