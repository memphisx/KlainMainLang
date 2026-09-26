// Package shadow records where a new front-end pass disagrees with the old
// codegen decider it will replace (TDD-00230 P0.2).
//
// While both exist, a compile run with KML_SHADOW set asks the new pass the
// same question at every point codegen asks an old decider (an expression's
// type, an annotation's type, a global's storage type, a function's return
// type), and appends one JSON line per disagreement to the file KML_SHADOW
// names. Aggregate ranks the lines of a whole corpus run into the histogram
// `make shadow-report` writes. A decider is switched over to the new pass
// only when its disagreement count is zero, and then deleted.
package shadow

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Disagreement is one point where the two passes answered differently.
type Disagreement struct {
	// Decider is the old codegen function asked ("inferExprType").
	Decider string `json:"decider"`
	// Kind is the AST node kind the question was about ("CallExpression").
	Kind string `json:"kind"`
	// Unit is the file the compile run started from; Line and Col locate
	// the node (in whichever module of that run it belongs to).
	Unit string `json:"unit,omitempty"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	// Old and New are the two answers, rendered as representation keys;
	// Fields names the top-level fields that differ.
	Old    string   `json:"old"`
	New    string   `json:"new"`
	Fields []string `json:"fields,omitempty"`
}

// EnvVar switches recording on: it names the file disagreements are appended
// to. EnvUnit, when set, labels the run's disagreements with its entry file.
const (
	EnvVar  = "KML_SHADOW"
	EnvUnit = "KML_SHADOW_UNIT"
)

// Enabled reports whether this process records disagreements.
func Enabled() bool { return os.Getenv(EnvVar) != "" }

// Recorder appends disagreements to a JSON-lines file, once per node and
// decider.
type Recorder struct {
	mu   sync.Mutex
	w    io.Writer
	unit string
	seen map[string]bool
}

// NewRecorder records to w, labelling each disagreement with unit.
func NewRecorder(w io.Writer, unit string) *Recorder {
	return &Recorder{w: w, unit: unit, seen: map[string]bool{}}
}

// FromEnv opens the file KML_SHADOW names for appending, or returns nil when
// recording is off. The caller closes the returned closer.
func FromEnv() (*Recorder, io.Closer, error) {
	path := os.Getenv(EnvVar)
	if path == "" {
		return nil, nil, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, err
	}
	return NewRecorder(f, os.Getenv(EnvUnit)), f, nil
}

// Record writes d unless a disagreement of the same decider at the same node
// position was already written. A nil Recorder records nothing.
func (r *Recorder) Record(d Disagreement) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := d.Decider + "\x00" + d.Kind + "\x00" + strconv.Itoa(d.Line) + ":" + strconv.Itoa(d.Col)
	if r.seen[key] {
		return
	}
	r.seen[key] = true
	if d.Unit == "" {
		d.Unit = r.unit
	}
	b, _ := json.Marshal(d)
	r.w.Write(append(b, '\n'))
}

// Bucket is one row of the histogram: the disagreements of one decider on
// one node kind that differ in the same fields.
type Bucket struct {
	Decider  string
	Kind     string
	Fields   string // the differing fields, comma-separated
	Count    int
	Examples []Disagreement // up to three
}

// Aggregate reads JSON lines from r and ranks them into buckets, largest
// first (ties by decider, kind, fields). Malformed lines are skipped.
func Aggregate(r io.Reader) ([]Bucket, int, error) {
	buckets := map[string]*Bucket{}
	total := 0
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var d Disagreement
		if json.Unmarshal(sc.Bytes(), &d) != nil {
			continue
		}
		total++
		fields := strings.Join(d.Fields, ",")
		key := d.Decider + "\x00" + d.Kind + "\x00" + fields
		b := buckets[key]
		if b == nil {
			b = &Bucket{Decider: d.Decider, Kind: d.Kind, Fields: fields}
			buckets[key] = b
		}
		b.Count++
		if len(b.Examples) < 3 {
			b.Examples = append(b.Examples, d)
		}
	}
	out := make([]Bucket, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		if a.Decider != b.Decider {
			return a.Decider < b.Decider
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Fields < b.Fields
	})
	return out, total, sc.Err()
}
