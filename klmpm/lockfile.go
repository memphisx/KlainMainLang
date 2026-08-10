package klmpm

import (
	"encoding/json"
	"fmt"
	"os"
)

// LockedPackage is one resolved dependency's pinned state in klain.lock —
// klmpm's own reproducibility record, never read by the compiler. Commit is
// the exact git commit klmpm fetched (the primary integrity anchor, git's
// own content-addressed identity). Integrity is an additional content hash
// over the fetched tree, for defense-in-depth beyond trusting git's SHA
// alone — still an open question in the TDD (worth adding or not), so the
// field is optional rather than required.
type LockedPackage struct {
	Source    string `json:"source"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Integrity string `json:"integrity,omitempty"`
}

// Lockfile is klain.lock's schema — one resolved, pinned entry per package
// name in the fully-resolved transitive dependency graph.
type Lockfile struct {
	Packages map[string]LockedPackage `json:"packages"`
}

// ParseLockfile reads and validates a klain.lock file at path. Same
// structural-only validation posture as ParseManifest — no semver or
// fetchability checks here.
func ParseLockfile(path string) (*Lockfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var l Lockfile
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("%s: invalid JSON: %w", path, err)
	}
	for name, pkg := range l.Packages {
		if pkg.Source == "" {
			return nil, fmt.Errorf(`%s: locked package %q has no "source"`, path, name)
		}
		if pkg.Version == "" {
			return nil, fmt.Errorf(`%s: locked package %q has no "version"`, path, name)
		}
		if pkg.Commit == "" {
			return nil, fmt.Errorf(`%s: locked package %q has no "commit"`, path, name)
		}
	}
	return &l, nil
}
