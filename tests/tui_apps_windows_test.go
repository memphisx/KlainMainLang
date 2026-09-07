package tests

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tui_apps_windows_test.go — drives the *actual* landing-page gallery TUI apps
// (built from their real source under apps/, not a synthetic stand-in)
// through the ConPTY harness, exactly as the Linux side drives menus under a pty.
// This is the guard the showcase apps previously lacked: `make examples` only
// checks they exit 0 on a non-tty (and runs with ucrt64/bin on PATH, so it can't
// see a non-self-contained binary), and every other tui test uses a synthetic
// program. Building the real file catches (a) a build break in the app itself,
// (b) a regression to dynamic mingw DLLs (ADR-00771), and (c) broken menu
// navigation. Windows-only via the _windows_test.go suffix (the ConPTY harness
// is Windows).

// buildExampleApp compiles a showcase app from its real source path (relative to
// the repo root), following its relative imports — so both single-file apps and
// multi-module ones (klaintop = main + types/data/view) build correctly.
func buildExampleApp(t *testing.T, rel string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", rel))
	if err != nil {
		t.Fatalf("abs %s: %v", rel, err)
	}
	return buildBinaryFromFile(t, abs)
}

// The real apps/menu/main.ts fruit-picker: ↓ moves the cursor, space toggles
// the highlighted item ("[ ]" -> "[x]") and bumps the chosen count, q quits.
func TestE2ETuiMenuAppNavigates(t *testing.T) {
	staticBuild(t)
	bin := buildExampleApp(t, "apps/menu/main.ts")

	// The real shipped binary must also be self-contained (ADR-00771).
	assertNoBundledRuntimeDLLs(t, bin, "menu app")

	res := runInConPTY(t, bin, 60, 24, 30*time.Second, func(write func(string)) {
		time.Sleep(900 * time.Millisecond)
		write("\x1b[B") // apple -> banana
		time.Sleep(400 * time.Millisecond)
		write("\x1b[B") // banana -> cherry
		time.Sleep(400 * time.Millisecond)
		write(" ") // toggle the highlighted item (cherry)
		time.Sleep(400 * time.Millisecond)
		write("q") // quit
	})

	// The initial frame paints in full — title, every item unchecked, count 0.
	for _, want := range []string{"fruit picker", "[ ] apple", "[ ] cherry", "[ ] elderberry", "chosen: 0/5"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("menu app: expected %q in the initial frame; output:\n%q", want, res.Output)
		}
	}
	// klain:tui's painter diffs frames and re-emits only changed cells, so a
	// toggle is a cursor move + a single "x" over the checkbox space (never the
	// whole "[x] cherry" string). "Hx" is that cell write — proof the space key,
	// after two ↓ arrows landed the cursor on cherry, checked it.
	if !strings.Contains(res.Output, "Hx") {
		t.Errorf("menu app: no checkbox-toggle cell write ('Hx') after navigating + space; the menu did not respond to input. Output:\n%q", res.Output)
	}
	if res.ExitCode != 0 {
		t.Errorf("menu app exited %d, want 0 (q should quit cleanly)", res.ExitCode)
	}
}

// klaintop is a MULTI-MODULE app (main + types/data/view, split by concern like
// the guide teaches). This guards the whole chain: the resolver following its
// relative imports, a self-contained --static build, and the app rendering. We
// drive the non-tty path (paints one frame from real `os`/`ps` data, then
// exits) rather than the interactive loop — klaintop re-spawns `ps` every tick,
// and child-process + tty-input under the Windows reactor gap (TDD-00182) is
// flaky; the interactive-navigation guarantee is the menu test's job.
func TestE2ETuiKlaintopApp(t *testing.T) {
	staticBuild(t)
	bin := buildExampleApp(t, "apps/klaintop/main.ts")
	assertNoBundledRuntimeDLLs(t, bin, "klaintop app")

	out, err := exec.Command(bin).Output() // stdin is not a tty → one frame + exit
	if err != nil {
		t.Fatalf("klaintop run: %v", err)
	}
	// The header/CPU/MEM chrome paints regardless of whether `ps` yields rows.
	for _, want := range []string{"klaintop", "CPU", "MEM", "COMMAND"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("klaintop app: expected %q in the rendered frame; got:\n%q", want, string(out))
		}
	}
}

// todo (multi-module: types/store/view/main) — non-tty paints one frame from the
// saved (or starter) list, then exits.
func TestE2ETuiTodoApp(t *testing.T) {
	staticBuild(t)
	bin := buildExampleApp(t, "apps/todo/main.ts")
	assertNoBundledRuntimeDLLs(t, bin, "todo app")
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("todo run: %v", err)
	}
	for _, want := range []string{"To-do", "done"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("todo app: expected %q in the rendered frame; got:\n%q", want, string(out))
		}
	}
}

// files (multi-module: fs/view/main) — non-tty paints the current directory: the
// preview pane of ".." shows the parent's "N entries".
func TestE2ETuiFilesApp(t *testing.T) {
	staticBuild(t)
	bin := buildExampleApp(t, "apps/files/main.ts")
	assertNoBundledRuntimeDLLs(t, bin, "files app")
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("files run: %v", err)
	}
	if !strings.Contains(string(out), "entries") {
		t.Errorf("files app: expected a directory preview ('entries') in the frame; got:\n%q", string(out))
	}
}
