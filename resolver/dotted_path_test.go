package resolver

import (
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

// hasDottedPath matches exactly what the regexp it replaced matched.
func TestHasDottedPathMatchesRegexp(t *testing.T) {
	paths := [][]string{{"Array", "prototype"}, {"Array", "from"}, {"Object", "prototype", "toString"}, {"a", "b"}}
	pieces := []string{"Array", "prototype", "from", "Object", "toString", "a", "b", ".", "?.", " ", "\t", "\n", "?", "x", "$", "_", "é", "(", ";", "Arrays", "1"}
	r := rand.New(rand.NewSource(1))
	for _, parts := range paths {
		q := make([]string, len(parts))
		for i, p := range parts {
			q[i] = regexp.QuoteMeta(p)
		}
		re := regexp.MustCompile(`(^|[^\w$.])` + strings.Join(q, `\s*\??\.\s*`) + `([^\w$]|$)`)
		for n := 0; n < 200000; n++ {
			var b strings.Builder
			for k := r.Intn(12); k >= 0; k-- {
				b.WriteString(pieces[r.Intn(len(pieces))])
			}
			src := []byte(b.String())
			if got, want := hasDottedPath(src, parts), re.Match(src); got != want {
				t.Fatalf("%q in %q: got %v, regexp %v", parts, src, got, want)
			}
		}
	}
}
