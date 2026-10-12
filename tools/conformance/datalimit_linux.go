package main

import (
	"fmt"
	"os/exec"
)

// programDataKB is the data-segment limit for compiled test programs, 0 for
// none. Set from -mem-cap-mb by limitChildData.
var programDataKB int64

func limitChildData(memCapMB int) {
	if memCapMB > 0 {
		programDataKB = int64(memCapMB) << 10
	}
}

// limitProgram runs cmd, a compiled test program, under RLIMIT_DATA. A test
// program that allocates gigabytes in under a second outruns the reaper's
// RSS polling; with the limit its allocation fails at the cap instead of the
// kernel OOM-killing the run (ADR-01582). Only test programs get it: the Go
// runtime of the runner and the compiler reserves more than the cap as heap
// metadata on x86-64. The shell execs the program, so its pid and process
// group are the program's.
func limitProgram(cmd *exec.Cmd) {
	if programDataKB <= 0 {
		return
	}
	script := fmt.Sprintf(`ulimit -d %d && exec "$0" "$@"`, programDataKB)
	cmd.Args = append([]string{"/bin/sh", "-c", script, cmd.Path}, cmd.Args[1:]...)
	cmd.Path = "/bin/sh"
}
