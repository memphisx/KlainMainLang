//go:build unix

package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"syscall"
)

// SIGUSR1 writes a heap profile and the memory stats beside the run's
// workdir: a long run's growth is then seen as live data or as memory the
// process holds without using.
func init() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	go func() {
		n := 0
		for range ch {
			n++
			runtime.GC()
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			path := filepath.Join(os.TempDir(), fmt.Sprintf("kml-conform-heap-%d-%d.pprof", os.Getpid(), n))
			if f, err := os.Create(path); err == nil {
				pprof.WriteHeapProfile(f)
				f.Close()
			}
			fmt.Fprintf(os.Stderr, "heapsnap %s: live %dMB, heap sys %dMB, released %dMB, stacks %dMB, other sys %dMB, total sys %dMB\n",
				path, m.HeapAlloc>>20, m.HeapSys>>20, m.HeapReleased>>20, m.StackSys>>20, (m.Sys-m.HeapSys-m.StackSys)>>20, m.Sys>>20)
		}
	}()
}
