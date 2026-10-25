package llvm

import (
	"reflect"
	"testing"
)

// irNames finds the names irSymRe does.
func TestIRNamesMatchesRegexp(t *testing.T) {
	for _, text := range []string{
		`  %t1 = call i64 @__kml_x.y(ptr @"quoted name", i64 %a) ; @tail`,
		`@a = internal global ptr @b, align 8`,
		`store ptr @, ptr %p`,
		`@"unterminated`,
		`c"@not\00" @x-1$z`,
	} {
		var want []string
		for _, m := range irSymRe.FindAllStringSubmatch(text, -1) {
			want = append(want, m[1])
		}
		if got := irNames(text, '@'); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %q, want %q", text, got, want)
		}
	}
}
