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

// A clang waits while its bytes do not fit beside a running clang's, and
// starts once that one is done; with none running it starts at once, over
// the budget or not, so two waiting clangs never wait on each other.
func TestMemBudgetAdmitClang(t *testing.T) {
	b := newMemBudget(100)
	a, c := b.reserve(10), b.reserve(10)
	b.admitClang(a, 80) // none running: admitted
	started := make(chan bool)
	go func() { b.admitClang(c, 80); started <- true }()
	select {
	case <-started:
		t.Fatal("a clang over the budget started beside a running one")
	case <-time.After(50 * time.Millisecond):
	}
	b.release(a)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("the waiting clang did not start once the other was done")
	}
	b.release(c)
	d := b.reserve(10)
	b.admitClang(d, 500) // alone: admitted although over the budget
	b.release(d)
	if b.used != 0 || b.held != 0 || b.inClang != 0 {
		t.Fatalf("budget not empty after releases: used=%d held=%d inClang=%d", b.used, b.held, b.inClang)
	}
}

// A clang peak raises the factor later reservations take; a lower one does
// not lower it.
func TestIRFactorFromPeak(t *testing.T) {
	irFactor.Store(0)
	defer irFactor.Store(0)
	if got := irReservation(100 << 20); got != (100<<20)*irBytesFactor {
		t.Fatalf("default reservation %d", got)
	}
	irFactor.Store(40)
	if got := irReservation(100 << 20); got != (100<<20)*40 {
		t.Fatalf("raised reservation %d", got)
	}
}
