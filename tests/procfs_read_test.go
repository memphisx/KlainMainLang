package tests

import (
	"runtime"
	"strings"
	"testing"
)

// procfs_read_test.go — ADR-00811. /proc and /sys pseudo-files report st_size 0
// yet read real bytes; the size-then-fread path returned "" for them. The
// grow-until-EOF fallback fixes it. Regression: read /proc/self/stat and check
// it comes back non-empty with the expected shape. Linux-only (no /proc on
// macOS/Windows) — this runs on the Linux CI lane and skips elsewhere.

func TestE2EReadFileSyncProcfs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/proc is Linux-only")
	}
	src := `import { readFileSync } from "fs";
const s: string = readFileSync("/proc/self/stat", "utf8");
console.log("len>0:", s.length > 0);
console.log("hascomm:", s.indexOf("(") >= 0);
`
	out := compileAndRunImports(t, src)
	if !strings.Contains(out, "len>0: true") || !strings.Contains(out, "hascomm: true") {
		t.Fatalf("readFileSync on a zero-stat-size /proc file should return real content, got:\n%s", out)
	}
}
