package checker_test

import (
	"os"
	"path/filepath"
)

// globFiles returns the contents of the files matching pattern.
func globFiles(pattern string) ([]string, error) {
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil {
			out = append(out, string(b))
		}
	}
	return out, nil
}
