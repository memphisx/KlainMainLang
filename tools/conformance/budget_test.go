package main

import (
	"testing"
	"time"
)

// A compile that does not fit waits for one to be released; one larger than
// the whole budget is admitted when nothing else is held; growing never
// waits, so two held compiles growing past the budget do not deadlock.
func TestMemBudget(t *testing.T) {
	b := newMemBudget(100)
	big := b.reserve(500) // alone: admitted although over the budget
	got := make(chan *reservation)
	go func() { got <- b.reserve(10) }()
	select {
	case <-got:
		t.Fatal("admitted while the budget is overdrawn")
	case <-time.After(50 * time.Millisecond):
	}
	b.release(big)
	r1 := <-got

	r2 := b.reserve(60)
	done := make(chan bool)
	go func() {
		b.grow(r1, 90)
		b.grow(r2, 90)
		done <- true
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("grow waited")
	}
	go func() { got <- b.reserve(1) }()
	select {
	case <-got:
		t.Fatal("admitted while grown past the budget")
	case <-time.After(50 * time.Millisecond):
	}
	b.release(r1)
	b.release(r2)
	b.release(r2) // a second release is a no-op
	b.release(<-got)
	if b.used != 0 || b.held != 0 {
		t.Fatalf("used %d held %d after every release", b.used, b.held)
	}
}
