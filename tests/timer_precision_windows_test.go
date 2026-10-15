package tests

import (
	"os/exec"
	"regexp"
	"strconv"
	"testing"
)

// timer_precision_windows_test.go — Windows timers keep Node's timing. The
// loop is libuv's (TDD-00252), whose Windows wait has the system timer's
// resolution (~15.6 ms), as Node's does: twenty 50 ms intervals take about
// 1250 ms under Node 24 on the dev box, and here. (ADR-01024's
// high-resolution wait made them about 1010 ms, unlike Node.) Three programs,
// one per kind of wait the loop used to have: timers only, a signal handler
// installed, a socket open. The floor catches an interval that fires early;
// the ceiling, a wait that loses whole ticks. Windows-only: POSIX timers do
// not quantise this way.

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
server.listen(0, '127.0.0.1', () => {` + timerPrecisionTicks + `})
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
			if ms > 1400 {
				t.Errorf("twenty 50ms intervals took %d ms (> 1400; Node takes about 1250): the wait loses whole ticks", ms)
			}
		})
	}
}
