// This file is the conformance harness's defense against runaway child
// processes. Two ways a compiled test binary escapes the per-file timeout +
// process-group SIGKILL that killableCommand (main.go) already applies:
//
//   1. A test that spawns a *detached* child (child_process with
//      `detached: true` → setsid() in the fork, ADR-00765) puts that child in
//      a NEW session + process group. kill(-pgid) of the parent's group never
//      reaches it. Worse, the parent then exits 0 — so the harness sees a
//      clean PASS and never tries to kill anything, while the child loops
//      forever, re-parented to init.
//   2. A cluster.fork() / http.listen({workers:N}) peer re-execs the compiled
//      binary; when its parent test exits, the worker re-parents to init and
//      keeps serving.
//
// Either way the escapees accumulate across the ~3,957-file corpus. Measured
// once at ~880 live processes / ~42GB RSS, at which point the OS began
// OOM-killing. This reaper enumerates processes whose executable path lies
// under the run's scratch workdir (unique per run: .conformance-out/run-<pid>)
// and kills the ones that have been orphaned (re-parented away from any test
// still legitimately running), plus a hard aggregate-RSS ceiling that aborts
// the whole run rather than let the machine die.
//
// Portable via `ps` (macOS + Linux, this project's two targets) — no cgo, no
// external deps, consistent with the rest of the harness. The Windows stub
// (reaper_windows.go) keeps runTracked/startReaper compiling there; the
// process-group escape this guards against is a POSIX setsid() behavior.

//go:build !windows

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// liveChildren is the set of test-binary PIDs the harness is *currently*
// running and legitimately awaiting. The reaper spares these and their live
// descendants; everything else under the workdir is fair game.
var (
	liveMu       sync.Mutex
	liveChildren = map[int]bool{}
)

func registerChild(pid int) {
	liveMu.Lock()
	liveChildren[pid] = true
	liveMu.Unlock()
}

func unregisterChild(pid int) {
	liveMu.Lock()
	delete(liveChildren, pid)
	liveMu.Unlock()
}

func isLiveChild(pid int) bool {
	liveMu.Lock()
	defer liveMu.Unlock()
	return liveChildren[pid]
}

// runTracked runs an already-configured killableCommand while registering its
// PID as a legitimately-live child, so the reaper's orphan sweep won't kill it
// or its still-parented descendants mid-test. Start()+Wait() is equivalent to
// Run() — the context-cancel watcher and WaitDelay set on the cmd are honored
// by Wait — but lets us capture the PID between the two.
func runTracked(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	registerChild(pid)
	defer unregisterChild(pid)
	return cmd.Wait()
}

type procInfo struct {
	pid, ppid int
	rssKB     int64
	cmd       string
}

// reaper periodically enumerates workdir-scoped processes, kills orphans, and
// enforces an aggregate-RSS ceiling.
type reaper struct {
	absWorkDir string
	memCapKB   int64 // 0 disables the ceiling
	interval   time.Duration
	stop       chan struct{}
	done       chan struct{}
	mu         sync.Mutex
	killed     int
}

func newReaper(absWorkDir string, memCapMB int) *reaper {
	interval := 3 * time.Second
	if ms := os.Getenv("KML_REAPER_INTERVAL_MS"); ms != "" {
		if n, err := strconv.Atoi(ms); err == nil && n > 0 {
			interval = time.Duration(n) * time.Millisecond
		}
	}
	return &reaper{
		absWorkDir: absWorkDir,
		memCapKB:   int64(memCapMB) * 1024,
		interval:   interval,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
}

// scanWorkdirProcs lists every live process whose command line references the
// run's workdir. absWorkDir is unique per run, so a substring match is both
// sufficient and safe — it can never match an unrelated user process.
func (r *reaper) scanWorkdirProcs() (procs []procInfo, totalKB int64) {
	out, err := exec.Command("ps", "-axww", "-o", "pid=,ppid=,rss=,command=").Output()
	if err != nil {
		return nil, 0
	}
	self := os.Getpid()
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		f := strings.Fields(strings.TrimLeft(sc.Text(), " "))
		if len(f) < 4 {
			continue
		}
		pid, e1 := strconv.Atoi(f[0])
		ppid, e2 := strconv.Atoi(f[1])
		rss, e3 := strconv.ParseInt(f[2], 10, 64)
		if e1 != nil || e2 != nil || e3 != nil || pid == self {
			continue
		}
		cmd := strings.Join(f[3:], " ")
		if !strings.Contains(cmd, r.absWorkDir) {
			continue
		}
		procs = append(procs, procInfo{pid: pid, ppid: ppid, rssKB: rss, cmd: cmd})
		totalKB += rss
	}
	return procs, totalKB
}

// kill SIGKILLs a process and its group (negative pid), covering any children
// it forked into its own group.
func (r *reaper) kill(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
	r.mu.Lock()
	r.killed++
	r.mu.Unlock()
}

// orphaned reports whether a workdir process is no longer attached to any test
// the harness is actively running: neither it nor its parent is a live child.
// A re-exec'd cluster worker whose parent test is still within its timeout has
// ppid == that live child, so it is spared until the parent finishes; once the
// parent exits, the worker re-parents (ppid drifts to 1) and the next sweep
// takes it.
func (r *reaper) orphaned(p procInfo) bool {
	return !isLiveChild(p.pid) && !isLiveChild(p.ppid)
}

func (r *reaper) sweepOnce() {
	procs, totalKB := r.scanWorkdirProcs()
	for _, p := range procs {
		if r.orphaned(p) {
			if os.Getenv("KML_REAPER_DEBUG") != "" {
				fmt.Fprintf(os.Stderr, "reaper: killing orphan pid=%d ppid=%d rss=%dKB cmd=%s\n", p.pid, p.ppid, p.rssKB, p.cmd)
			}
			r.kill(p.pid)
		}
	}
	if r.memCapKB > 0 && totalKB > r.memCapKB {
		// Even the legitimately-live tests are collectively over the ceiling —
		// a genuine runaway. Kill everything under the workdir and abort the
		// whole run rather than risk an OS-level OOM. A partial report is worth
		// far less than a working machine.
		for _, p := range procs {
			r.kill(p.pid)
		}
		fmt.Fprintf(os.Stderr,
			"\n*** conformance ABORTED: workdir process RSS %dMB exceeded the --mem-cap-mb ceiling %dMB;\n"+
				"    killed all %d workdir processes to protect the machine. Raise -mem-cap-mb or\n"+
				"    lower -workers, and prefer running off battery / low-power mode. ***\n",
			totalKB/1024, r.memCapKB/1024, len(procs))
		os.Exit(3)
	}
}

func (r *reaper) run() {
	defer close(r.done)
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-t.C:
			r.sweepOnce()
		}
	}
}

// finalSweep kills any workdir process still alive, regardless of parentage —
// called once the run is over (all legitimate children have been unregistered)
// and from the signal handler. Idempotent.
func (r *reaper) finalSweep() {
	procs, _ := r.scanWorkdirProcs()
	for _, p := range procs {
		r.kill(p.pid)
	}
}

// startReaper launches the background watchdog and installs a SIGINT/SIGTERM
// handler that sweeps every workdir process before exiting — so a Ctrl-C
// mid-run can't leave the ~880-process pile behind. Returns a stop function to
// call (deferred) when the run completes normally.
func startReaper(absWorkDir string, memCapMB int) func() {
	r := newReaper(absWorkDir, memCapMB)
	go r.run()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Fprintln(os.Stderr, "\ninterrupted — sweeping workdir processes ...")
		r.finalSweep()
		os.Exit(130)
	}()

	return func() {
		close(r.stop)
		<-r.done
		signal.Stop(sig)
		r.finalSweep()
		if r.killed > 0 {
			fmt.Fprintf(os.Stderr, "reaper: killed %d escaped/orphaned workdir process(es) during the run\n", r.killed)
		}
	}
}
