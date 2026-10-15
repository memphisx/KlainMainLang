package main

import (
	"runtime/debug"
	"sync"
)

// A compile's memory, from codegen through clang, is reserved against one
// byte budget, so the run's workers fit the memory they share. Codegen runs
// in this process: an http2 program's IR is ~70 MB, its emitter peaks near
// 750 MB and its clang near 460 MB; nine such compiles at once outran the
// run's job cap on Windows (which counts this process and every clang) and
// the machine's commit elsewhere, killing the run. A compile reserves
// compileDefault before codegen and, once its IR exists, irBytesFactor times
// the IR's size until clang is done. One compile is always admitted, so the
// run cannot stall on a compile larger than the budget.
const (
	compileDefault = 256 << 20
	irBytesFactor  = 12
)

type memBudget struct {
	mu    sync.Mutex
	cond  *sync.Cond
	limit int64
	used  int64
	held  int
}

var budget = newMemBudget(0)

func newMemBudget(limit int64) *memBudget {
	b := &memBudget{limit: limit}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// setBudget bounds the run's compiles at limit bytes (0: unbounded) and sets
// the Go heap's soft limit to it, so the collector keeps this process's heap
// near what is live rather than letting it double.
func setBudget(limit int64) {
	budget.mu.Lock()
	budget.limit = limit
	budget.mu.Unlock()
	if limit > 0 {
		debug.SetMemoryLimit(limit)
	}
}

// reservation is one compile's share of the budget.
type reservation struct{ n int64 }

// reserve waits until n bytes fit (or nothing else is held) and takes them.
func (b *memBudget) reserve(n int64) *reservation {
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.limit > 0 && b.held > 0 && b.used+n > b.limit {
		b.cond.Wait()
	}
	b.used += n
	b.held++
	return &reservation{n: n}
}

// grow raises r to n bytes without waiting: the memory is already in use,
// and two compiles each waiting to grow would wait on each other. The
// overshoot holds back new compiles (reserve) until it is released.
func (b *memBudget) grow(r *reservation, n int64) {
	if r == nil || n <= r.n {
		return
	}
	b.mu.Lock()
	b.used += n - r.n
	r.n = n
	b.mu.Unlock()
}

// release returns r's bytes; a second release is a no-op.
func (b *memBudget) release(r *reservation) {
	if r == nil || r.n < 0 {
		return
	}
	b.mu.Lock()
	b.used -= r.n
	b.held--
	r.n = -1
	b.mu.Unlock()
	b.cond.Broadcast()
}

// irReservation is the bytes a compile of an IR of irLen bytes holds.
func irReservation(irLen int) int64 {
	if n := int64(irLen) * irBytesFactor; n > compileDefault {
		return n
	}
	return compileDefault
}
