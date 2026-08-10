// Package klmpm defines the on-disk formats klmpm (TDD-00054) reads and
// writes — klain.json package manifests and klain.lock lockfiles. Nothing
// in the compiler itself imports this package: resolver.go's own Stage 1
// resolution (ADR-00146) only ever needs a package's "main" field, and
// reads it directly with its own minimal, compiler-only struct rather than
// depending on this one — deliberately, so the compiler's module
// resolution never carries package-manager-tooling code as a dependency.
// This package is forward-looking infrastructure for klmpm's later stages
// (Stage 3's MVS resolution, Stage 4's CLI), not yet wired into anything.
package klmpm

import (
	"encoding/json"
	"fmt"
	"os"
)

// Dependency is one entry in a Manifest's "dependencies" map — a package
// name mapped to where klmpm should fetch it from and the minimum version
// to require. TDD-00054's Design section: a single "at least this version"
// floor (a semver tag), never a range operator — resolved via Minimal
// Version Selection (Stage 3, not built yet), not a constraint solver.
type Dependency struct {
	Source  string `json:"source"`
	Version string `json:"version"`
}

// Manifest is klain.json's schema — the same shape for a project's own
// root manifest and any dependency's own manifest ("a klmpm project IS
// itself just a package"). Main is optional here: it only matters at the
// point a package is actually used as someone else's dependency, which
// resolver.go's own Stage 1 resolution already enforces independently
// (ADR-00146) — a plain end-user application's own manifest legitimately
// has no reason to set it. Deliberately no "scripts" field, anywhere — see
// the TDD's Design section for why that's structural, not a policy this
// type has to enforce.
type Manifest struct {
	Name         string                `json:"name"`
	Version      string                `json:"version"`
	Main         string                `json:"main,omitempty"`
	Dependencies map[string]Dependency `json:"dependencies,omitempty"`
}

// ParseManifest reads and validates a klain.json file at path. Validation
// is deliberately structural only (required fields present, correctly
// shaped) — it does not check that a "source" is actually fetchable or
// that a "version" is valid semver; those are Stage 3 concerns (MVS
// resolution, git-backed fetch), not yet built.
func ParseManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: invalid JSON: %w", path, err)
	}
	if m.Name == "" {
		return nil, fmt.Errorf(`%s: missing required field "name"`, path)
	}
	if m.Version == "" {
		return nil, fmt.Errorf(`%s: missing required field "version"`, path)
	}
	for name, dep := range m.Dependencies {
		if dep.Source == "" {
			return nil, fmt.Errorf(`%s: dependency %q has no "source"`, path, name)
		}
		if dep.Version == "" {
			return nil, fmt.Errorf(`%s: dependency %q has no "version"`, path, name)
		}
	}
	return &m, nil
}
