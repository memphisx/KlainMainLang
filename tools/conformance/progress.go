package main

// progress.go — live-progress emission for the conformance suites
// (docs/testing/PROGRESS-DASHBOARD.md). Each suite's result loop calls
// tick() per finished file; every progressEvery files (and at lane
// start/end) the counters land in the scratch dir, one file per
// (suite, lane) so a both-lanes run shows as two dashboard rows:
//
//   progress-<suite>-<lane>.txt   suite|lane|done|total|pass|fail|skip|startedUnix|updatedUnix
//   reasons-<suite>-<lane>.txt    count|reason           (top 20, most common first)
//
// Both are single writes replaced atomically (tmp + rename) so a reader
// never sees a torn file. Flat `|`-separated lines rather than JSON on
// purpose: the viewer (apps/klainconf, a TUI written in this compiler's own
// typed subset) parses them with split+Number — no dynamic JSON object model
// needed. Runtime telemetry in the gitignored scratch dir, never a report.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const progressEvery = 25

// progressTracker accumulates one lane's counters and owns the started
// timestamp. Each lane creates a fresh tracker (so ETA restarts per lane).
// Used from a single result-collection goroutine — no locking.
type progressTracker struct {
	suite, lane            string
	total                  int
	done, pass, fail, skip int
	started                int64
	dir                    string
	reasons                map[string]int
}

func newProgressTracker(suite, lane string, total int, workDir string) *progressTracker {
	t := &progressTracker{suite: suite, lane: lane, total: total, started: time.Now().Unix(), dir: workDir, reasons: map[string]int{}}
	t.flush() // announce the lane immediately, with done=0
	return t
}

// tick records one finished file. Status semantics per suite: "PASS"/"FAIL"/
// anything-else=skip for the run suites; the TS oracle maps agreement→PASS,
// mismatch→FAIL, out-of-scope→skip before calling. reason is the (already
// suite-normalized) failure/skip reason, "" for a pass.
func (t *progressTracker) tick(status, reason string) {
	t.done++
	switch status {
	case "PASS":
		t.pass++
	case "FAIL":
		t.fail++
	default:
		t.skip++
	}
	if reason != "" {
		r := strings.ReplaceAll(reason, "\n", " ")
		if len(r) > 100 {
			r = r[:100] + "…"
		}
		t.reasons[r]++
	}
	if t.done%progressEvery == 0 || t.done == t.total {
		t.flush()
	}
}

func (t *progressTracker) flush() {
	if err := os.MkdirAll(t.dir, 0755); err != nil {
		return // telemetry only — never fail the run over it
	}
	line := fmt.Sprintf("%s|%s|%d|%d|%d|%d|%d|%d|%d\n",
		t.suite, t.lane, t.done, t.total, t.pass, t.fail, t.skip, t.started, time.Now().Unix())
	atomicWrite(filepath.Join(t.dir, "progress-"+t.suite+"-"+t.lane+".txt"), line)

	type rc struct {
		reason string
		n      int
	}
	rows := make([]rc, 0, len(t.reasons))
	for r, n := range t.reasons {
		rows = append(rows, rc{r, n})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].reason < rows[j].reason
	})
	var b strings.Builder
	for i, r := range rows {
		if i >= 20 {
			break
		}
		fmt.Fprintf(&b, "%d|%s\n", r.n, r.reason)
	}
	atomicWrite(filepath.Join(t.dir, "reasons-"+t.suite+"-"+t.lane+".txt"), b.String())
}

func atomicWrite(path, content string) {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}
