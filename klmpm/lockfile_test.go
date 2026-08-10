package klmpm

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLockfileValid(t *testing.T) {
	path := writeTemp(t, "klain.lock", `{
		"packages": {
			"left-pad": {
				"source": "github:someuser/left-pad",
				"version": "v1.2.0",
				"commit": "abc123",
				"integrity": "sha256:deadbeef"
			}
		}
	}`)
	l, err := ParseLockfile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pkg, ok := l.Packages["left-pad"]
	if !ok {
		t.Fatalf("expected a 'left-pad' locked package, got %+v", l.Packages)
	}
	if pkg.Source != "github:someuser/left-pad" || pkg.Version != "v1.2.0" || pkg.Commit != "abc123" || pkg.Integrity != "sha256:deadbeef" {
		t.Fatalf("unexpected locked package: %+v", pkg)
	}
}

func TestParseLockfileIntegrityIsOptional(t *testing.T) {
	path := writeTemp(t, "klain.lock", `{
		"packages": {
			"left-pad": { "source": "github:someuser/left-pad", "version": "v1.2.0", "commit": "abc123" }
		}
	}`)
	l, err := ParseLockfile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l.Packages["left-pad"].Integrity != "" {
		t.Fatalf("expected empty Integrity, got %q", l.Packages["left-pad"].Integrity)
	}
}

func TestParseLockfileEmptyPackages(t *testing.T) {
	path := writeTemp(t, "klain.lock", `{"packages": {}}`)
	l, err := ParseLockfile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(l.Packages) != 0 {
		t.Fatalf("expected no packages, got %+v", l.Packages)
	}
}

func TestParseLockfileMissingSource(t *testing.T) {
	path := writeTemp(t, "klain.lock", `{
		"packages": { "left-pad": { "version": "v1.2.0", "commit": "abc123" } }
	}`)
	_, err := ParseLockfile(path)
	if err == nil || !strings.Contains(err.Error(), `"source"`) {
		t.Fatalf("expected a missing-source error, got %v", err)
	}
}

func TestParseLockfileMissingVersion(t *testing.T) {
	path := writeTemp(t, "klain.lock", `{
		"packages": { "left-pad": { "source": "github:someuser/left-pad", "commit": "abc123" } }
	}`)
	_, err := ParseLockfile(path)
	if err == nil || !strings.Contains(err.Error(), `"version"`) {
		t.Fatalf("expected a missing-version error, got %v", err)
	}
}

func TestParseLockfileMissingCommit(t *testing.T) {
	path := writeTemp(t, "klain.lock", `{
		"packages": { "left-pad": { "source": "github:someuser/left-pad", "version": "v1.2.0" } }
	}`)
	_, err := ParseLockfile(path)
	if err == nil || !strings.Contains(err.Error(), `"commit"`) {
		t.Fatalf("expected a missing-commit error, got %v", err)
	}
}

func TestParseLockfileInvalidJSON(t *testing.T) {
	path := writeTemp(t, "klain.lock", `{ not valid json `)
	_, err := ParseLockfile(path)
	if err == nil {
		t.Fatal("expected an error for invalid JSON, got none")
	}
}

func TestParseLockfileFileNotFound(t *testing.T) {
	_, err := ParseLockfile(filepath.Join(t.TempDir(), "does-not-exist.lock"))
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got none")
	}
}
