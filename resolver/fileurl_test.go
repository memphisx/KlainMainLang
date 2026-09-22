package resolver

import "testing"

// Expected values are Node 24's url.pathToFileURL(p, { windows }).href
// (tools/testsel/probes/fileurl.mjs prints them).
func TestFileURLFromPath(t *testing.T) {
	cases := []struct {
		path    string
		windows bool
		want    string
	}{
		{`C:\dir x\a b.ts`, true, "file:///C:/dir%20x/a%20b.ts"},
		{`C:\dir x\a#b`, true, "file:///C:/dir%20x/a%23b"},
		{`C:\dir x\a%b`, true, "file:///C:/dir%20x/a%25b"},
		{`C:\dir x\a^b`, true, "file:///C:/dir%20x/a%5Eb"},
		{"C:\\dir x\\a`b", true, "file:///C:/dir%20x/a%60b"},
		{`C:\dir x\a{b}`, true, "file:///C:/dir%20x/a%7Bb%7D"},
		{`C:\dir x\a[b]`, true, "file:///C:/dir%20x/a%5Bb%5D"},
		{`C:\dir x\a~b`, true, "file:///C:/dir%20x/a%7Eb"},
		{`C:\dir x\é.ts`, true, "file:///C:/dir%20x/%C3%A9.ts"},
		{`C:\dir x\a'b`, true, "file:///C:/dir%20x/a'b"},
		{`C:\dir x\a!b$&()*+,;=:@b`, true, "file:///C:/dir%20x/a!b$&()*+,;=:@b"},
		{`\\server\share\a b.ts`, true, "file://server/share/a%20b.ts"},
		{"/dir x/a b.ts", false, "file:///dir%20x/a%20b.ts"},
		{"/dir x/a?b", false, "file:///dir%20x/a%3Fb"},
		{`/dir x/a"b`, false, "file:///dir%20x/a%22b"},
		{"/dir x/a<b>", false, "file:///dir%20x/a%3Cb%3E"},
		{"/dir x/a|b", false, "file:///dir%20x/a%7Cb"},
		{"/dir x/a\tb", false, "file:///dir%20x/a%09b"},
		{"/dir x/a\nb", false, "file:///dir%20x/a%0Ab"},
		{"/dir x/a\x7fb", false, "file:///dir%20x/a%7Fb"},
		{`/home/u/a\b c.ts`, false, "file:///home/u/a%5Cb%20c.ts"},
	}
	for _, c := range cases {
		if got := fileURLFromPath(c.path, c.windows); got != c.want {
			t.Errorf("fileURLFromPath(%q, windows=%v) = %q, want %q", c.path, c.windows, got, c.want)
		}
	}
}
