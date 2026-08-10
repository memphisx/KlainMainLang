package klmpm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestParseManifestValidNoDependencies(t *testing.T) {
	path := writeTemp(t, "klain.json", `{"name": "left-pad", "version": "1.2.0", "main": "src/index.ts"}`)
	m, err := ParseManifest(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name != "left-pad" || m.Version != "1.2.0" || m.Main != "src/index.ts" {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	if len(m.Dependencies) != 0 {
		t.Fatalf("expected no dependencies, got %+v", m.Dependencies)
	}
}

func TestParseManifestMainIsOptional(t *testing.T) {
	// A plain application's own manifest legitimately has no "main" — it's
	// only enforced at the point a package is actually imported as a
	// dependency (resolver.go's own Stage 1 resolution, ADR-00146).
	path := writeTemp(t, "klain.json", `{"name": "my-app", "version": "0.1.0"}`)
	m, err := ParseManifest(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Main != "" {
		t.Fatalf("expected empty Main, got %q", m.Main)
	}
}

func TestParseManifestValidWithDependencies(t *testing.T) {
	path := writeTemp(t, "klain.json", `{
		"name": "my-app",
		"version": "0.1.0",
		"dependencies": {
			"left-pad": { "source": "github:someuser/left-pad", "version": "v1.2.0" }
		}
	}`)
	m, err := ParseManifest(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dep, ok := m.Dependencies["left-pad"]
	if !ok {
		t.Fatalf("expected a 'left-pad' dependency, got %+v", m.Dependencies)
	}
	if dep.Source != "github:someuser/left-pad" || dep.Version != "v1.2.0" {
		t.Fatalf("unexpected dependency: %+v", dep)
	}
}

func TestParseManifestMissingName(t *testing.T) {
	path := writeTemp(t, "klain.json", `{"version": "1.0.0"}`)
	_, err := ParseManifest(path)
	if err == nil || !strings.Contains(err.Error(), `"name"`) {
		t.Fatalf("expected a missing-name error, got %v", err)
	}
}

func TestParseManifestMissingVersion(t *testing.T) {
	path := writeTemp(t, "klain.json", `{"name": "left-pad"}`)
	_, err := ParseManifest(path)
	if err == nil || !strings.Contains(err.Error(), `"version"`) {
		t.Fatalf("expected a missing-version error, got %v", err)
	}
}

func TestParseManifestDependencyMissingSource(t *testing.T) {
	path := writeTemp(t, "klain.json", `{
		"name": "my-app", "version": "0.1.0",
		"dependencies": { "left-pad": { "version": "v1.2.0" } }
	}`)
	_, err := ParseManifest(path)
	if err == nil || !strings.Contains(err.Error(), `"source"`) {
		t.Fatalf("expected a missing-source error, got %v", err)
	}
}

func TestParseManifestDependencyMissingVersion(t *testing.T) {
	path := writeTemp(t, "klain.json", `{
		"name": "my-app", "version": "0.1.0",
		"dependencies": { "left-pad": { "source": "github:someuser/left-pad" } }
	}`)
	_, err := ParseManifest(path)
	if err == nil || !strings.Contains(err.Error(), `"version"`) {
		t.Fatalf("expected a missing-version error, got %v", err)
	}
}

func TestParseManifestInvalidJSON(t *testing.T) {
	path := writeTemp(t, "klain.json", `{ not valid json `)
	_, err := ParseManifest(path)
	if err == nil {
		t.Fatal("expected an error for invalid JSON, got none")
	}
}

func TestParseManifestFileNotFound(t *testing.T) {
	_, err := ParseManifest(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got none")
	}
}
