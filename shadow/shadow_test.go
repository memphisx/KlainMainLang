package shadow

import (
	"bytes"
	"strings"
	"testing"
)

func TestRecordOncePerNodeAndAggregate(t *testing.T) {
	var buf bytes.Buffer
	r := NewRecorder(&buf, "a.ts")
	d := Disagreement{Decider: "inferExprType", Kind: "CallExpression", Line: 3, Col: 4, Old: "x", New: "y", Fields: []string{"IR"}}
	r.Record(d)
	r.Record(d) // same node again
	r.Record(Disagreement{Decider: "inferExprType", Kind: "CallExpression", Line: 5, Col: 1, Old: "x", New: "y", Fields: []string{"IR"}})
	r.Record(Disagreement{Decider: "resolveType", Kind: "UnionType", Line: 1, Col: 1, Old: "p", New: "q", Fields: []string{"Nullable"}})
	var nilRec *Recorder
	nilRec.Record(d) // a nil recorder records nothing
	if n := strings.Count(buf.String(), "\n"); n != 3 {
		t.Fatalf("%d lines:\n%s", n, buf.String())
	}
	buckets, total, err := Aggregate(strings.NewReader(buf.String() + "not json\n"))
	if err != nil || total != 3 || len(buckets) != 2 {
		t.Fatalf("buckets %+v total %d err %v", buckets, total, err)
	}
	if b := buckets[0]; b.Decider != "inferExprType" || b.Count != 2 || b.Fields != "IR" || b.Examples[0].Unit != "a.ts" {
		t.Errorf("first bucket %+v", b)
	}
}
