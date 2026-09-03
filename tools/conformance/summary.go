package main

// summary.go — a machine-readable projection of the headline conformance
// numbers, emitted beside the human Markdown reports as
// docs/testing/conformance-summary.json. It exists so downstream consumers (the
// website landing/docs) can source exact, per-lane figures from data instead of
// hand-copying them from prose — the same "data is the source, Markdown is
// generated" discipline docs/status/data/*.json already follows.
//
// The three suites (test262/node/ts) are three independent `make` targets, and
// each lane writes its own report, so the file is merge-updated: a write
// replaces only its own suites[suite].lanes[lane] entry and leaves every other
// suite and lane intact. No timestamps are stored, so the file changes only
// when a number changes (no per-run git churn). Map keys marshal sorted, so the
// output is deterministic.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

const summaryPath = "docs/testing/conformance-summary.json"

var summaryMu sync.Mutex

type summaryFile struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Suites        map[string]*summarySuite `json:"suites"`
}

type summarySuite struct {
	CorpusCommit string                     `json:"corpusCommit,omitempty"`
	Lanes        map[string]json.RawMessage `json:"lanes"`
}

type passTotal struct {
	Pass  int `json:"pass"`
	Total int `json:"total"`
}

// test262SummaryLane is one compat lane's Test262 figures.
type test262SummaryLane struct {
	Overall passTotal      `json:"overall"`
	InScope passTotal      `json:"inScope"`
	ByPhase map[string]int `json:"byPhase,omitempty"`
}

// nodeSummaryLane is one compat lane's Node-core figures.
type nodeSummaryLane struct {
	Pass     int `json:"pass"`
	Fail     int `json:"fail"`
	Skip     int `json:"skip"`
	Runnable int `json:"runnable"`
	Total    int `json:"total"`
}

// tsSummaryLane is one compat lane's TypeScript-oracle figures.
type tsSummaryLane struct {
	Agree       int `json:"agree"`
	Classified  int `json:"classified"`
	FalseAccept int `json:"falseAccept"`
	FalseReject int `json:"falseReject"`
	Skipped     int `json:"skipped"`
}

// updateConformanceSummary merges one lane's stats into the shared summary file
// under suites[suite].lanes[lane], preserving every other suite and lane. A
// malformed or missing file is treated as empty (rebuilt from what this run
// knows). corpusCommit, when non-empty, is recorded at the suite level.
func updateConformanceSummary(suite, lane string, laneStats any, corpusCommit string) error {
	summaryMu.Lock()
	defer summaryMu.Unlock()

	doc := &summaryFile{SchemaVersion: 1, Suites: map[string]*summarySuite{}}
	if b, err := os.ReadFile(summaryPath); err == nil {
		_ = json.Unmarshal(b, doc) // tolerate an old/corrupt file
		if doc.Suites == nil {
			doc.Suites = map[string]*summarySuite{}
		}
		doc.SchemaVersion = 1
	}

	s := doc.Suites[suite]
	if s == nil {
		s = &summarySuite{Lanes: map[string]json.RawMessage{}}
		doc.Suites[suite] = s
	}
	if s.Lanes == nil {
		s.Lanes = map[string]json.RawMessage{}
	}
	if corpusCommit != "" {
		s.CorpusCommit = corpusCommit
	}

	raw, err := json.Marshal(laneStats)
	if err != nil {
		return err
	}
	s.Lanes[lane] = raw

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(summaryPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(summaryPath, append(out, '\n'), 0644)
}
