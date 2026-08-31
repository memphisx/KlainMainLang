package tests

import (
	"os/exec"
	"strings"
	"testing"
)

// runTui compiles a klain:tui program (resolver-aware, for the import) and runs
// it with no stdin — the non-TTY path, where the painter falls back to 80x24
// and renders a single frame to stdout.
func runTui(t *testing.T, src string) string {
	t.Helper()
	bin := buildBinaryImports(t, src)
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return string(out)
}

// TDD-00150 Stage 1: native klain:tui framework — Yoga flexbox layout + a
// double-buffered ANSI diff painter. The interactive state->view->update loop
// (raw key reads via klain:tty) is exercised manually under a pty; these E2E
// tests cover the deterministic surface: that the vendored Yoga engine links,
// that a component tree lays out and paints, and that the painter emits the
// alt-screen enter/leave envelope and the expected glyphs/text.

// A full frame paints: alt-screen enter, a rounded border, coloured/attributed
// text laid out by flexbox, list/progress/spinner components, then leave.
func TestE2ETuiRenderFrame(t *testing.T) {
	out := runTui(t, `
import { Box, Text, List, Progress, Spinner, render, enter, leave } from 'klain:tui'
enter()
render(Box(
  { flexDirection: 'column', padding: 1, border: 'round', borderColor: 'cyan', width: 30 },
  [
    Text('Hello TUI', { color: 'green', bold: true }),
    Box({ flexDirection: 'row', justifyContent: 'space-between' }, [
      Text('L', { color: 'yellow' }),
      Text('R', { color: 'magenta' }),
    ]),
    List(['one', 'two', 'three'], { selected: 1 }),
    Progress(0.5),
    Spinner(2, { label: 'work' }),
  ]
))
leave()
`)
	checks := []struct {
		what, sub string
	}{
		{"alt-screen enter", "\x1b[?1049h"},
		{"cursor hidden", "\x1b[?25l"},
		{"rounded border top-left", "╭"},   // ╭
		{"rounded border bottom-right", "╯"}, // ╯
		{"horizontal border", "─"},          // ─
		{"title text", "Hello TUI"},
		{"green fg SGR", "\x1b[32m"},
		{"bold SGR", "\x1b[1m"},
		{"list items", "three"},
		{"progress fill", "█"},  // █
		{"progress track", "░"}, // ░
		{"spinner label", "work"},
		{"cursor shown", "\x1b[?25h"},
		{"alt-screen leave", "\x1b[?1049l"},
	}
	for _, c := range checks {
		if !strings.Contains(out, c.sub) {
			t.Errorf("frame missing %s (%q)", c.what, c.sub)
		}
	}
}

// space-between on a row pushes the two children to opposite ends: 'L' at the
// left content edge and 'R' at the right, several columns apart on the same
// row — a direct check that Yoga's flexbox justification drives the paint.
func TestE2ETuiFlexJustify(t *testing.T) {
	out := runTui(t, `
import { Box, Text, render } from 'klain:tui'
render(Box({ flexDirection: 'row', justifyContent: 'space-between', width: 20 }, [
  Text('L'),
  Text('R'),
]))
`)
	// space-between pushes them apart: L at the left content edge, R ~18 columns
	// later at the right of the 20-wide row. The first frame paints the whole
	// changed row as one contiguous run, so the justification shows up directly
	// as a wide gap of spaces between the two glyphs.
	li, ri := strings.IndexByte(out, 'L'), strings.IndexByte(out, 'R')
	if li < 0 || ri < 0 {
		t.Fatalf("missing L/R glyphs in %q", out)
	}
	gap := ri - li - 1
	if gap < 15 {
		t.Errorf("expected L and R justified apart (gap>=15), got gap=%d in %q", gap, out)
	}
}

// Width-aware painting: a wide (CJK) glyph occupies two columns, so in a
// space-between row of fixed width the trailing marker is pushed by the wide
// glyph's real display width, not one column. Here a 6-wide row holds '世'
// (width 2) and 'X' (width 1); flex leaves exactly 6-2-1 = 3 columns of gap.
// A painter that mis-counted the wide glyph as one column would emit 4 spaces.
func TestE2ETuiWideCharWidth(t *testing.T) {
	out := runTui(t, `
import { Box, Text, render } from 'klain:tui'
render(Box({ flexDirection: 'row', justifyContent: 'space-between', width: 6 }, [
  Text('世'),
  Text('X'),
]))
`)
	wi := strings.Index(out, "世")
	if wi < 0 {
		t.Fatalf("missing wide glyph in %q", out)
	}
	rest := out[wi+len("世"):]
	xi := strings.IndexByte(rest, 'X')
	if xi < 0 {
		t.Fatalf("missing marker after wide glyph in %q", out)
	}
	gap := rest[:xi]
	if gap != "   " { // exactly 3 spaces
		t.Errorf("expected 3-space gap after width-2 glyph, got %d bytes %q", len(gap), gap)
	}
}

// List viewport: a list taller than its bounded box scrolls to keep the
// selected item visible and paints a scrollbar, instead of overflowing. Here
// 12 items live in a height-5 box (3 content rows) with item 8 selected — only
// a window around 8 shows, item 0 is scrolled off, and a scrollbar thumb (█)
// paints on the right.
func TestE2ETuiListViewport(t *testing.T) {
	out := runTui(t, `
import { Box, List, render } from 'klain:tui'
const items: string[] = []
for (let i = 0; i < 12; i++) items.push('item-' + i)
render(Box({ border: 'single', width: 16, height: 5 }, [
  List(items, { selected: 8 }),
]))
`)
	if !strings.Contains(out, "item-8") {
		t.Errorf("selected item not visible in %q", out)
	}
	if strings.Contains(out, "item-0") {
		t.Errorf("off-screen item-0 should have been scrolled out of the viewport")
	}
	if !strings.Contains(out, "█") { // scrollbar thumb
		t.Errorf("expected a scrollbar thumb for an overflowing list")
	}
}
