// Package scratch implements the KML_SCRATCH knob: one environment variable
// that moves every throwaway write of a test or conformance run (t.TempDir,
// the compiler's build directories, clang's temp objects, the conformance
// workdir) onto a volume of the caller's choosing — typically a RAM drive, to
// keep the multi-gigabyte churn of a full run off an SSD.
package scratch

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnvVar names the scratch-root override.
const EnvVar = "KML_SCRATCH"

// Apply redirects the process's temp directory — and so that of every child
// it spawns — under $KML_SCRATCH, and returns the root ("" when the variable
// is unset, in which case nothing changes). os.TempDir reads TMP/TEMP on
// Windows and TMPDIR elsewhere; all three are set so a POSIX-flavoured child
// on Windows (an MSYS tool) follows too.
func Apply() (string, error) {
	root := os.Getenv(EnvVar)
	if root == "" {
		return "", nil
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("%s: %w", EnvVar, err)
	}
	tmp := filepath.Join(abs, "tmp")
	if err := os.MkdirAll(tmp, 0755); err != nil {
		return "", fmt.Errorf("%s: %w", EnvVar, err)
	}
	for _, k := range []string{"TMP", "TEMP", "TMPDIR"} {
		if err := os.Setenv(k, tmp); err != nil {
			return "", fmt.Errorf("%s: %w", EnvVar, err)
		}
	}
	return abs, nil
}
