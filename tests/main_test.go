package tests

import (
	"os"
	"os/signal"
	"testing"
)

// TestMain exists to make SIGINT *caught* in the test process itself. The
// Makefile's test-par target launches each shard as `( ... ) &` from a
// non-interactive shell, and POSIX requires such async commands to start
// with SIGINT (and SIGQUIT) set to SIG_IGN. The Go runtime honors a
// SIG_IGN disposition present at startup — it does not install its own
// handler over it — and ignored dispositions survive exec, so every
// compiled program a shard spawns would inherit SIGINT=SIG_IGN. That
// silently breaks any test asserting default-disposition behavior
// (TestE2ESignalNoHandlerDefaultDisposition failed deterministically
// under test-par for exactly this reason: the server "ignored" SIGINT
// because the shard's shell told it to, not because of anything the
// runtime emitted). signal.Notify installs a real handler, flipping the
// disposition to caught — and caught signals reset to SIG_DFL across
// exec, so children always start clean, matching a foreground `go test`
// run (where the go tool's own Notify already provided this by accident).
// Exiting on receipt also restores Ctrl-C's ability to stop a
// backgrounded shard, which SIG_IGN previously blocked.
func TestMain(m *testing.M) {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt)
	go func() {
		<-sigc
		os.Exit(130)
	}()
	os.Exit(m.Run())
}
