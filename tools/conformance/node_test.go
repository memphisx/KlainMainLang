package main

import "strings"

import "testing"

// TestTransformNodeSource covers the CJS→ESM rewrite's core cases: require
// rewriting, node: prefix normalization, use-strict stripping, common/global
// injection, and the out-of-scope classifications.
func TestTransformNodeSource(t *testing.T) {
	t.Run("basic require rewrite", func(t *testing.T) {
		src := "'use strict';\nrequire('../common');\nconst assert = require('assert');\nconst path = require('node:path');\nassert.ok(path.basename('/a/b') === 'b');\n"
		out, _, skip := transformNodeSource(src, "/tmp/test-x.js")
		if skip != "" {
			t.Fatalf("unexpected skip: %s", skip)
		}
		if !strings.Contains(out, "import assert from 'assert'") {
			t.Errorf("missing assert import:\n%s", out)
		}
		if !strings.Contains(out, "import path from 'path'") {
			t.Errorf("node: prefix not normalized to 'path':\n%s", out)
		}
		if strings.Contains(out, "use strict") || strings.Contains(out, "require(") {
			t.Errorf("residual use-strict/require:\n%s", out)
		}
	})

	t.Run("destructured require", func(t *testing.T) {
		src := "const { basename } = require('path');\n"
		out, _, skip := transformNodeSource(src, "/tmp/t.js")
		if skip != "" || !strings.Contains(out, "import {") || !strings.Contains(out, "basename") || !strings.Contains(out, "from 'path'") {
			t.Errorf("destructure rewrite failed: skip=%q out=%s", skip, out)
		}
	})

	t.Run("global injection only when used", func(t *testing.T) {
		with := "const path = require('path');\nconsole.log(path.basename(__filename));\n"
		out, _, _ := transformNodeSource(with, "/tmp/test-name.js")
		if !strings.Contains(out, `const __filename = "/tmp/test-name.js"`) {
			t.Errorf("__filename not injected when used:\n%s", out)
		}
		without := "const path = require('path');\nconsole.log('hi');\n"
		out2, _, _ := transformNodeSource(without, "/tmp/t.js")
		if strings.Contains(out2, "__filename") {
			t.Errorf("__filename injected when unused:\n%s", out2)
		}
	})

	t.Run("out-of-scope patterns", func(t *testing.T) {
		cases := map[string]string{
			"win32 namespace":    "const path = require('path');\npath.win32.basename('x');\n",
			"posix namespace":    "const path = require('path');\npath.posix.basename('x');\n",
			"unsupported module": "const insp = require('inspector');\n",
		}
		for name, src := range cases {
			if _, _, skip := transformNodeSource(src, "/tmp/t.js"); skip == "" {
				t.Errorf("%s: expected out-of-scope skip, got none", name)
			}
		}
	})
}

func TestNormalizeNodeModule(t *testing.T) {
	if got := normalizeNodeModule("node:url"); got != "url" {
		t.Errorf("node:url → %q, want url", got)
	}
	if got := normalizeNodeModule("path"); got != "path" {
		t.Errorf("path → %q, want path", got)
	}
}
