package tests

import (
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
)

// libEntryProg exercises what a host-called entry must still run: argv, the
// microtask queue, a timer on the event loop, and process.exitCode as the
// returned code.
const libEntryProg = `const m = new Map<string, number>([["a", 1]]);
console.log("args", process.argv.length, process.argv[2], m.get("a"));
setTimeout(() => console.log("timer"), 10);
Promise.resolve(2).then(v => console.log("then", v));
process.exitCode = 3;
`

// libEntryHost calls kml_main from a thread of its own, not the process's
// main thread, and prints the code it returns.
const libEntryHost = `#include <pthread.h>
#include <stdio.h>
extern int kml_main(int argc, char **argv);
static void *run(void *a) {
    (void)a;
    char *argv[] = {"prog", "x", 0};
    return (void *)(long)kml_main(2, argv);
}
int main(void) {
    pthread_t t;
    void *r;
    pthread_create(&t, 0, run, 0);
    pthread_join(t, &r);
    fflush(stdout);
    printf("exit %ld\n", (long)r);
    return 0;
}
`

// TestE2ELibraryEntryFromThread: -lib builds a shared library whose
// kml_main, called by a host on a non-main thread, runs the program to
// completion (its loop included) and returns process.exitCode, printing what
// Node prints for the same program run as `node prog.ts x`.
func TestE2ELibraryEntryFromThread(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the host program uses pthreads")
	}
	cli := buildCLI(t)
	dir := tempDir(t)
	prog := filepath.Join(dir, "prog.ts")
	writeFile(t, prog, libEntryProg)
	writeFile(t, filepath.Join(dir, "host.c"), libEntryHost)
	lib := filepath.Join(dir, llvm.HostSharedLibName("prog"))
	if out, err := exec.Command(cli, "-lib", "-o", lib, prog).CombinedOutput(); err != nil {
		t.Fatalf("klainmain -lib: %v\n%s", err, out)
	}
	host := filepath.Join(dir, "host")
	if out, err := exec.Command("clang", "-o", host, filepath.Join(dir, "host.c"), "-L"+dir, "-lprog", "-pthread", "-Wl,-rpath,"+dir).CombinedOutput(); err != nil {
		t.Fatalf("link host: %v\n%s", err, out)
	}
	got, err := exec.Command(host).Output()
	if err != nil {
		t.Fatalf("host: %v\n%s", err, got)
	}

	// Node's output and exit code for the same program and argument.
	node := exec.Command(nodeStripTypesBin(t), "--experimental-strip-types", "--no-warnings", prog, "x")
	want, nerr := node.Output()
	code := 0
	var ee *exec.ExitError
	if errors.As(nerr, &ee) {
		code = ee.ExitCode()
	} else if nerr != nil {
		t.Fatalf("node: %v", nerr)
	}
	wantAll := strings.TrimRight(string(want), "\n") + "\nexit " + itoa(code)
	if g := strings.TrimRight(string(got), "\n"); g != wantAll {
		t.Fatalf("library output differs from node:\n--- ours ---\n%s\n--- node ---\n%s", g, wantAll)
	}
}

// TestE2ELibraryRefusesGC: -mm=gc replaces malloc for the whole process,
// which a library cannot do inside its host; -lib refuses it up front.
func TestE2ELibraryRefusesGC(t *testing.T) {
	cli := buildCLI(t)
	dir := tempDir(t)
	prog := filepath.Join(dir, "prog.ts")
	writeFile(t, prog, "console.log(1);\n")
	out, err := exec.Command(cli, "-lib", "-mm=gc", prog).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "-lib cannot be combined with -mm=gc") {
		t.Fatalf("want a -mm=gc refusal, got err=%v\n%s", err, out)
	}
}
