package main

import (
	"bytes"
	"os"
	"runtime/debug"
	"runtime/metrics"
	"sync"
	"sync/atomic"
	"time"
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
	// inClang counts the reservations whose clang is running.
	inClang int
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
	// A lower GOMEMLIMIT holds: a machine with less room than its free
	// commit suggests sets it.
	if os.Getenv("GOMEMLIMIT") != "" {
		if cur := debug.SetMemoryLimit(-1); limit <= 0 || cur < limit {
			limit = cur
		}
	}
	budget.mu.Lock()
	budget.limit = limit
	budget.mu.Unlock()
	// The heap's soft limit is what the concurrent front ends need, not the
	// budget: the budget counts compiles' memory, most of it clang's, and a
	// limit raised to it (tens of GB on a large machine, at GOGC=400) let
	// the front end's garbage reach that before a collection. 1.5 GB plus
	// 384 MB per front-end slot (14 slots: 6.9 GB, measured peak 5-6 GB);
	// a flat 1.5 GB made the collector run without pause and compiles time
	// out. Never above the budget.
	soft := int64(1536 << 20)
	if frontEndSlots != nil {
		soft += int64(cap(frontEndSlots)) * (384 << 20)
	}
	want := soft
	if limit > 0 && limit < soft {
		soft = limit
	}
	if os.Getenv("GOMEMLIMIT") == "" || debug.SetMemoryLimit(-1) > soft {
		debug.SetMemoryLimit(soft)
	}
	// GOGC=400 (main) is for a limit with that room. Under a lower one its
	// heap goal sits past the limit, so every cycle is the limit's; once
	// those cycles want more than the collector's CPU cap (half the CPU)
	// the runtime lets the heap pass the limit, and a two-worker macOS run
	// held 2.7 GB of mostly garbage under a 1.6 GB limit. The default
	// GOGC collects at twice the live heap (100-400 MB here) instead.
	if os.Getenv("GOGC") == "" && debug.SetMemoryLimit(-1) < want {
		debug.SetGCPercent(100)
	}
}

// reservation is one compile's share of the budget.
type reservation struct {
	n     int64
	clang bool
}

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
	if r.clang {
		b.inClang--
	}
	r.n = -1
	b.mu.Unlock()
	b.cond.Broadcast()
}

// admitClang raises r to the bytes its clang will take (n), waiting until
// they fit beside what the others hold. Its clang has not started, so the
// memory is not yet in use; one clang is always admitted when none runs,
// so the waits cannot close a cycle.
func (b *memBudget) admitClang(r *reservation, n int64) {
	if r == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if n < r.n {
		n = r.n
	}
	for b.limit > 0 && b.inClang > 0 && b.used-r.n+n > b.limit {
		b.cond.Wait()
	}
	b.used += n - r.n
	r.n = n
	if !r.clang {
		r.clang = true
		b.inClang++
	}
}

// irFactor is the largest clang peak per IR byte seen this run (at least
// irBytesFactor): a library-heavy program's clang peaks at 3-4 GiB, far
// past what a fixed factor gives its IR, and the run's later compiles of
// such programs are reserved at what the earlier ones took.
var irFactor atomic.Int64

// noteClangPeak records a finished clang's peak resident bytes for an IR of
// irLen bytes.
func noteClangPeak(irLen int, ps *os.ProcessState) {
	peak, ok := peakRSS(ps)
	if !ok || irLen <= 0 {
		return
	}
	f := peak/int64(irLen) + 1
	if f > 64 {
		f = 64 // a tiny IR beside the runtime's objects: no per-byte measure
	}
	for {
		cur := irFactor.Load()
		if f <= cur || irFactor.CompareAndSwap(cur, f) {
			return
		}
	}
}

// irReservation is the bytes a compile of an IR of irLen bytes holds.
func irReservation(irLen int) int64 {
	f := irFactor.Load()
	if f < irBytesFactor {
		f = irBytesFactor
	}
	if n := int64(irLen) * f; n > compileDefault {
		return n
	}
	return compileDefault
}

// frontEndSlots bounds the front ends alive at once at the worker count. A
// timed-out compile cannot be stopped, only abandoned, and it keeps
// allocating: starting another beside it fed a spiral (more memory, more
// collector work, slower compiles, more timeouts) that ended in the kernel
// killing the run. So an abandoned compile holds its slot until it ends.
var frontEndSlots chan struct{}

func setFrontEndSlots(n int) {
	if n > 0 {
		frontEndSlots = make(chan struct{}, n)
	}
}

func acquireFrontEnd() {
	if frontEndSlots != nil {
		frontEndSlots <- struct{}{}
	}
	// A front end starts only while the heap has room for it, unless it
	// would be the only one: a heavy compile (one whose global object
	// escapes emits every global's code and peaks near 1.3 GB; a typed-array
	// test's prototype rows are close behind) is known only once it runs,
	// and a few of them side by side outran a small machine while each was
	// still counted at compileDefault.
	if gate := heapGate(); gate > 0 {
		for activeFrontEnds.Load() > 0 && heapObjectBytes() > gate {
			time.Sleep(20 * time.Millisecond)
		}
	}
	activeFrontEnds.Add(1)
}

func releaseFrontEnd() {
	activeFrontEnds.Add(-1)
	if frontEndSlots != nil {
		<-frontEndSlots
	}
}

var activeFrontEnds atomic.Int64

// heapGate is the heap past which a new front end waits: three fifths of
// the run's memory limit, so a big machine's run (a limit of many GB) never
// waits; 0 with no limit.
func heapGate() uint64 {
	budget.mu.Lock()
	limit := budget.limit
	budget.mu.Unlock()
	if limit <= 0 {
		return 0
	}
	return uint64(limit) * 3 / 5
}

var heapSample = []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
var heapSampleMu sync.Mutex

// heapObjectBytes is the heap live at the last collection (garbage left
// out, which under a memory limit would hold the gate shut for nothing),
// read without stopping the world.
func heapObjectBytes() uint64 {
	heapSampleMu.Lock()
	defer heapSampleMu.Unlock()
	metrics.Read(heapSample)
	return heapSample[0].Value.Uint64()
}

// heavyBytes is what a front end whose program's global object escapes
// holds at its peak: it emits every global's code (about 105 MB of IR) and
// peaks near 1.3 GB, against ~200 MB for a typical program. Such a program
// is known once it is resolved, before that peak, so heavy codegens are
// bounded at the number the run's memory limit fits (the heap gate above
// sees only what a compile already holds, and a stretch of Test262, the
// Temporal tests among them, is hundreds of them in a row).
const heavyBytes = 1400 << 20

var heavyMu sync.Mutex
var heavyCond = sync.NewCond(&heavyMu)
var heavyActive int

func heavySlots() int {
	budget.mu.Lock()
	limit := budget.limit
	budget.mu.Unlock()
	if limit <= 0 {
		return 1 << 30
	}
	if n := int(limit / heavyBytes); n > 1 {
		return n
	}
	return 1
}

// acquireHeavy waits for a heavy codegen's turn; it is taken after the
// front-end slot, and nothing is waited on while it is held.
func acquireHeavy() {
	heavyMu.Lock()
	for heavyActive >= heavySlots() {
		heavyCond.Wait()
	}
	heavyActive++
	heavyMu.Unlock()
}

func releaseHeavy() {
	heavyMu.Lock()
	heavyActive--
	heavyMu.Unlock()
	heavyCond.Broadcast()
}

// outputCap bounds what a test program's stdout or stderr keeps: no test
// expects more, and a runaway program's output otherwise grows this
// process's heap with it.
const outputCap = 16 << 20

// capBuffer keeps the first outputCap bytes written to it and drops the
// rest, still reporting every write as done so the program is not stopped
// by a write error.
type capBuffer struct {
	bytes.Buffer
	dropped int64
}

func (c *capBuffer) Write(p []byte) (int, error) {
	if room := outputCap - c.Len(); room < len(p) {
		if room > 0 {
			c.Buffer.Write(p[:room])
		}
		c.dropped += int64(len(p) - max(room, 0))
		return len(p), nil
	}
	return c.Buffer.Write(p)
}
