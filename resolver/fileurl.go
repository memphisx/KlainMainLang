package resolver

import (
	"strings"
)

// fileURLFromPath renders an absolute filesystem path the way Node renders a
// module's `import.meta.url` (url.pathToFileURL(path).href):
//
//	POSIX    /home/u/a b.ts        → file:///home/u/a%20b.ts
//	Windows  C:\Users\u\a b.ts     → file:///C:/Users/u/a%20b.ts
//	UNC      \\server\share\a.ts   → file://server/share/a.ts
//
// Percent-encoded, as Node 24 does (tools/testsel/probes/fileurl.mjs is the
// reference): C0 controls, space and DEL, every byte of a non-ASCII character
// (UTF-8), and the ASCII set " # % < > ? [ ] ^ ` { | } ~. On POSIX a backslash
// is an ordinary file-name character and is encoded too (%5C); on Windows it is
// a separator.
func fileURLFromPath(path string, windows bool) string {
	host := ""
	if windows {
		path = strings.ReplaceAll(path, `\`, "/")
		if strings.HasPrefix(path, "//") && !strings.HasPrefix(path, "///") {
			// UNC: //server/share/rest → host "server", path "/share/rest".
			rest := path[2:]
			if i := strings.IndexByte(rest, '/'); i >= 0 {
				host, path = rest[:i], rest[i:]
			} else {
				host, path = rest, "/"
			}
		} else if !strings.HasPrefix(path, "/") {
			path = "/" + path // C:/x → /C:/x
		}
	}
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.WriteString("file://")
	b.WriteString(host)
	for i := 0; i < len(path); i++ {
		c := path[i]
		switch {
		case c <= 0x20, c >= 0x7F,
			c == '"', c == '#', c == '%', c == '<', c == '>', c == '?', c == '[', c == ']', c == '^', c == '`',
			c == '{', c == '|', c == '}', c == '~',
			c == '\\': // only reachable on POSIX: Windows turned every backslash into a separator above
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0F])
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
