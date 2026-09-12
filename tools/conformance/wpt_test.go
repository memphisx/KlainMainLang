package main

import (
	"strings"
	"testing"
)

// TestTransformWPTSource covers the WPT transform's core cases: META stripping,
// harness prepend, the fetch-shim gate, and the out-of-scope classifications.
func TestTransformWPTSource(t *testing.T) {
	t.Run("plain any.js gets harness, no fetch shim", func(t *testing.T) {
		src := "test(() => { assert_equals(1 + 1, 2); }, 'x');\n"
		out, skip, fail := transformWPTSource(src, ".")
		if skip != "" || fail != "" {
			t.Fatalf("unexpected skip=%q fail=%q", skip, fail)
		}
		if !strings.Contains(out, "function assert_equals") {
			t.Errorf("harness not prepended:\n%s", out)
		}
		if strings.Contains(out, "import fs from 'fs'") {
			t.Errorf("fetch shim leaked into a no-fetch file:\n%s", out)
		}
		if !strings.Contains(out, "__wpt_drain") {
			t.Errorf("drain suffix missing")
		}
	})

	t.Run("META frontmatter stripped", func(t *testing.T) {
		src := "// META: global=window,dedicatedworker\n// META: timeout=long\ntest(() => {}, 'x');\n"
		out, skip, fail := transformWPTSource(src, ".")
		if skip != "" || fail != "" {
			t.Fatalf("unexpected skip=%q fail=%q", skip, fail)
		}
		if strings.Contains(out, "META:") {
			t.Errorf("META line not stripped:\n%s", out)
		}
	})

	t.Run("fetch use pulls in the file-backed shim", func(t *testing.T) {
		src := "promise_test(() => fetch('resources/x.json').then(r => r.json()), 'load');\n"
		out, skip, fail := transformWPTSource(src, ".")
		if skip != "" || fail != "" {
			t.Fatalf("unexpected skip=%q fail=%q", skip, fail)
		}
		if !strings.Contains(out, "import fs from 'fs'") || !strings.Contains(out, "function fetch") {
			t.Errorf("fetch shim not injected:\n%s", out)
		}
	})

	t.Run("DOM-touching file is out of scope", func(t *testing.T) {
		src := "test(() => { const e = document.createElement('div'); }, 'x');\n"
		_, skip, fail := transformWPTSource(src, ".")
		if fail != "" {
			t.Fatalf("should skip, not fail: %s", fail)
		}
		if !strings.Contains(skip, "document") {
			t.Errorf("expected a DOM skip, got %q", skip)
		}
	})

	t.Run("idlharness include is out of scope", func(t *testing.T) {
		src := "// META: script=/resources/idlharness.js\nidl_test(['url'], []);\n"
		_, skip, _ := transformWPTSource(src, ".")
		if !strings.Contains(skip, "idlharness") {
			t.Errorf("expected an idlharness skip, got %q", skip)
		}
	})

	t.Run("a DOM needle inside a string does not trip the filter", func(t *testing.T) {
		src := "test(() => { assert_equals('document', 'document'); }, 'the word document in a string');\n"
		out, skip, fail := transformWPTSource(src, ".")
		if skip != "" || fail != "" {
			t.Fatalf("string-only 'document' wrongly classified: skip=%q fail=%q", skip, fail)
		}
		if !strings.Contains(out, "function test") {
			t.Errorf("harness not prepended")
		}
	})
}

func TestWordPresent(t *testing.T) {
	cases := []struct {
		s, needle string
		want      bool
	}{
		{"a fetch( call", "fetch", true},
		{"prefetch(x)", "fetch", false},
		{"window.foo", "window.", true},
		{"the window is open", "window.", false},
		{"document", "document", true},
		{"documentation", "document", false},
	}
	for _, c := range cases {
		if got := wordPresent(c.s, c.needle); got != c.want {
			t.Errorf("wordPresent(%q, %q) = %v, want %v", c.s, c.needle, got, c.want)
		}
	}
}
