package tests

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/resolver"
)

// klain:sync — Go-fidelity goroutine runtime (TDD-00143, Stage 1): the GMP
// scheduler, `go`, and CSP channels. Outputs are order-independent (the
// scheduler is nondeterministic), so every assertion is over a deterministic
// aggregate, never per-goroutine interleaving.

// TestE2EGoroutineRendezvous: a single producer goroutine hands one value to
// the main goroutine over an unbuffered channel (a rendezvous).
func TestE2EGoroutineRendezvous(t *testing.T) {
	out := compileAndRunImports(t, `
import { go, Channel } from 'klain:sync';
const ch = new Channel<number>(0);
go(() => { ch.send(42); });
console.log("got " + ch.receive());
`)
	if out != "got 42" {
		t.Fatalf("want %q, got %q", "got 42", out)
	}
}

// TestE2EGoroutinePipeline: a three-stage CSP pipeline (generate → square →
// sum) running as three concurrent goroutines over unbuffered channels.
func TestE2EGoroutinePipeline(t *testing.T) {
	out := compileAndRunImports(t, `
const N = 20;
import { go, Channel } from 'klain:sync';
const nums = new Channel<number>(0);
const squares = new Channel<number>(0);
go(() => { for (let i = 1; i <= N; i++) nums.send(i); });
go(() => { for (let i = 0; i < N; i++) { const n = nums.receive(); squares.send(n * n); } });
let total = 0;
for (let i = 0; i < N; i++) total += squares.receive();
console.log("total " + total);
`)
	if out != "total 2870" { // sum of squares 1..20
		t.Fatalf("want %q, got %q", "total 2870", out)
	}
}

// TestE2EGoroutineBufferedFanIn: many goroutines fan results into one buffered
// channel; the sum is deterministic regardless of interleaving. Goroutines are
// spawned by a top-level generator goroutine so each captures only top-level
// channels (the working capture pattern in Stage 1).
func TestE2EGoroutineBufferedFanIn(t *testing.T) {
	out := compileAndRunImports(t, `
import { go, Channel } from 'klain:sync';
const results = new Channel<number>(64);
// Three explicit producer goroutines, each sending a fixed disjoint range.
go(() => { for (let i = 0;  i < 10; i++) results.send(i); });
go(() => { for (let i = 10; i < 20; i++) results.send(i); });
go(() => { for (let i = 20; i < 30; i++) results.send(i); });
let sum = 0;
for (let i = 0; i < 30; i++) sum += results.receive();
console.log("sum " + sum);
`)
	if out != "sum 435" { // 0+1+...+29
		t.Fatalf("want %q, got %q", "sum 435", out)
	}
}

// TestE2EGoroutineStringChannel: a Channel<string> round-trips a pointer-typed
// element through the runtime's 8-byte slot.
func TestE2EGoroutineStringChannel(t *testing.T) {
	out := compileAndRunImports(t, `
import { go, Channel } from 'klain:sync';
const ch = new Channel<string>(1);
go(() => { ch.send("hello from a goroutine"); });
console.log(ch.receive());
`)
	if out != "hello from a goroutine" {
		t.Fatalf("want %q, got %q", "hello from a goroutine", out)
	}
}

// TestE2EGoroutineChannelInHelperFunction: a top-level channel is promoted to
// a module global (ADR-00541), so a named helper function can reference it with
// its type intact and spawn goroutines that capture it — the idiomatic fan-out
// pattern. Each worker's value arrives as a function parameter (a fresh
// binding), sidestepping the separate loop-`let` capture limitation.
func TestE2EGoroutineChannelInHelperFunction(t *testing.T) {
	out := compileAndRunImports(t, `
import { go, Channel } from 'klain:sync';
const results = new Channel<number>(16);
function spawn(base: number): void {
  go(() => { results.send(base * base); });
}
for (let i = 0; i < 5; i++) spawn(i);
let sum = 0;
for (let k = 0; k < 5; k++) sum += results.receive();
console.log("sum " + sum);
`)
	if out != "sum 30" { // 0+1+4+9+16
		t.Fatalf("want %q, got %q", "sum 30", out)
	}
}

// TestE2EGoroutinePreemption: a goroutine spinning in an infinite, channel-free
// tight loop must be cooperatively preempted (sysmon flags it, the compiler-
// inserted loop-back-edge safepoint yields it) so a sibling goroutine's channel
// send still completes. Run with GOMAXPROCS=1 so the two goroutines genuinely
// contend for one M — without preemption the spinner monopolises it forever and
// the program hangs (caught by the context timeout).
func TestE2EGoroutinePreemption(t *testing.T) {
	bin := buildBinaryImports(t, `
import { go, Channel } from 'klain:sync';
const ch = new Channel<number>(1);
go(() => { let x = 0; while (true) { x = x + 1; } });
go(() => { ch.send(42); });
console.log("received " + ch.receive());
`)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	cmd.Env = append(os.Environ(), "GOMAXPROCS=1")
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatal("timed out — the spinning goroutine was not preempted (it starved the sender)")
	}
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimRight(string(out), "\n"); got != "received 42" {
		t.Fatalf("want %q, got %q", "received 42", got)
	}
}

// TestE2EGoroutineChannelParam: a `Channel<T>` passed as a function parameter
// keeps its channel type, so .send()/.receive()/close()/`for..of` dispatch on
// it — not just a top-level `new Channel` local. This is what lets channel
// plumbing be split across helper functions and modules (ADR-00617); before it,
// a `Channel<number>` parameter resolved to a plain array ("an array has no
// method 'send'").
func TestE2EGoroutineChannelParam(t *testing.T) {
	out := compileAndRunImports(t, `
import { go, Channel } from 'klain:sync';
function fill(ch: Channel<number>, n: number): void {
  for (let i = 0; i < n; i++) ch.send(i * i);
  ch.close();
}
function drain(ch: Channel<number>): number {
  let sum = 0;
  for (const v of ch) sum = sum + v;
  return sum;
}
const ch = new Channel<number>(64);
go(() => { fill(ch, 6); });
console.log("sum " + drain(ch));
`)
	if out != "sum 55" { // 0+1+4+9+16+25
		t.Fatalf("want %q, got %q", "sum 55", out)
	}
}

// TestE2EGoroutineHTTPListenLoadTest: an in-process HTTP server running inside a
// goroutine (http.listen), hammered by a pool of worker goroutines making real
// synchronous XMLHttpRequest calls to it — the load-tester pattern (ADR-00616).
//
// http.listen's event loop is thread-affine (its ucontext connection fibers and
// its per-loop state — @__kml_listen_fd etc. — are thread-bound). Before the fix
// the server goroutine could be migrated to another M by the work-stealing
// scheduler, land on a thread whose thread-local listen_fd was the default -1,
// decide it had no listener, and return out from under the live server — after
// which every worker's request blocked forever (caught here by the timeout).
// klainsync_lock_os_thread (Go's runtime.LockOSThread), applied at event-loop
// entry, pins the reactor goroutine to its M so it never migrates. Runs with the
// default multi-M GOMAXPROCS: with only one M the bug cannot occur.
func TestE2EGoroutineHTTPListenLoadTest(t *testing.T) {
	bin := buildBinaryImports(t, `
import { go, Channel } from 'klain:sync';
import http from 'klain:http';

interface Res { status: number; body: string; headers: Map<string, string> }

const PORT = 8213;
const N = 8;
const TOTAL = 600;

go(() => {
  http.listen(PORT, (req: HttpRequest): Res => {
    const h: Map<string, string> = new Map<string, string>();
    h.set('Content-Type', 'text/plain');
    // A little real per-request work so the reactor goroutine actually spends
    // time in the (safepoint-bearing) handler — the case that used to migrate.
    let acc = 0;
    for (let i = 0; i < 2000; i++) acc = (acc + i) >>> 0;
    return { status: 200, body: acc + '', headers: h };
  });
});

const reqs = new Channel<number>(256);
const done = new Channel<number>(0);
go(() => { for (let i = 0; i < TOTAL; i++) reqs.send(i); reqs.close(); });
for (let w = 0; w < N; w++) go(() => {
  let ok = 0;
  for (const id of reqs) {
    const xhr = new XMLHttpRequest();
    xhr.open('GET', 'http://127.0.0.1:' + PORT + '/work', false);
    xhr.send();
    if (xhr.status === 200) ok = ok + 1;
  }
  done.send(ok);
});

let ok = 0;
for (let i = 0; i < N; i++) ok = ok + done.receive();
console.log('ok=' + ok);
process.exit(0);
`)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin).Output()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatal("timed out — the http.listen server goroutine was migrated off its M and exited, orphaning the workers")
	}
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimRight(string(out), "\n"); got != "ok=600" {
		t.Fatalf("want %q, got %q", "ok=600", got)
	}
}

// TestE2EChannelRange: `for (const v of ch)` ranges a channel until it is
// closed and drained (Go's channel range).
func TestE2EChannelRange(t *testing.T) {
	out := compileAndRunImports(t, `
import { go, Channel } from 'klain:sync';
const ch = new Channel<number>(0);
go(() => { for (let i = 1; i <= 5; i++) ch.send(i * 10); ch.close(); });
let sum = 0;
for (const v of ch) { sum += v; }
console.log("range " + sum);
`)
	if out != "range 150" { // 10+20+30+40+50
		t.Fatalf("want %q, got %q", "range 150", out)
	}
}

// TestE2ESelectRecv: select over two channels; a goroutine makes one ready.
func TestE2ESelectRecv(t *testing.T) {
	out := compileAndRunImports(t, `
import { go, Channel, select } from 'klain:sync';
const c1 = new Channel<number>(0);
const c2 = new Channel<string>(0);
go(() => { c1.send(99); });
select(
  c1.recvCase((v: number) => { console.log("num " + v); }),
  c2.recvCase((s: string) => { console.log("str " + s); }),
);
`)
	if out != "num 99" {
		t.Fatalf("want %q, got %q", "num 99", out)
	}
}

// TestE2ESelectDefault: a select with a default runs it when nothing is ready.
func TestE2ESelectDefault(t *testing.T) {
	out := compileAndRunImports(t, `
import { Channel, select, defaultCase } from 'klain:sync';
const c = new Channel<number>(0);
select(
  c.recvCase((v: number) => { console.log("recv"); }),
  defaultCase(() => { console.log("default"); }),
);
`)
	if out != "default" {
		t.Fatalf("want %q, got %q", "default", out)
	}
}

// TestE2ESelectSend: a select send case into a buffered channel, then drain it.
func TestE2ESelectSend(t *testing.T) {
	out := compileAndRunImports(t, `
import { Channel, select } from 'klain:sync';
const c = new Channel<number>(1);
select(
  c.sendCase(7, () => { console.log("sent"); }),
);
console.log("got " + c.receive());
`)
	if out != "sent\ngot 7" {
		t.Fatalf("want %q, got %q", "sent\\ngot 7", out)
	}
}

// TestE2ESelectFanIn: a select-loop draining two producer goroutines over
// unbuffered channels — the random-fair blocking select over multiple channels.
// Sum is deterministic regardless of which case fires in which order.
func TestE2ESelectFanIn(t *testing.T) {
	out := compileAndRunImports(t, `
import { go, Channel, select } from 'klain:sync';
const a = new Channel<number>(0);
const b = new Channel<number>(0);
go(() => { for (let i = 0; i < 5; i++) a.send(i); });       // 0..4
go(() => { for (let i = 0; i < 5; i++) b.send(100 + i); });  // 100..104
let sum = 0;
for (let k = 0; k < 10; k++) {
  select(
    a.recvCase((v: number) => { sum += v; }),
    b.recvCase((v: number) => { sum += v; }),
  );
}
console.log("sum " + sum);
`)
	if out != "sum 520" { // (0+..+4) + (100+..+104)
		t.Fatalf("want %q, got %q", "sum 520", out)
	}
}

// syncCompileError asserts a compile-stage error containing wantSub, going
// through the resolver so the klain:sync import resolves.
func syncCompileError(t *testing.T, src, wantSub string) {
	t.Helper()
	dir := writeMultiFile(t, map[string]string{"main.ts": src})
	prog, err := resolver.ResolveProgram(filepath.Join(dir, "main.ts"))
	if err == nil {
		_, err = llvm.NewEmitter().EmitProgram(prog)
	}
	if err == nil {
		t.Fatalf("expected a compile error containing %q, got success", wantSub)
	}
	if !strings.Contains(err.Error(), wantSub) {
		t.Fatalf("expected error containing %q, got: %v", wantSub, err)
	}
}

func TestGoRejectsNonFunction(t *testing.T) {
	syncCompileError(t, `
import { go } from 'klain:sync';
go(42);
`, "must be a function")
}

func TestGoRejectsParameterizedFunction(t *testing.T) {
	syncCompileError(t, `
import { go } from 'klain:sync';
go((x: number) => { console.log(x); });
`, "must take no parameters")
}
