package tests

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A Date's local getters, component constructor, local setters, toString and
// getTimezoneOffset follow the host's time zone, as Node's do; Date.UTC and
// the UTC accessors stay UTC.
func TestE2EDateLocalTime(t *testing.T) {
	src := `
const d = new Date(Date.UTC(2024, 6, 15, 10, 30));
console.log(d.getHours(), d.getDate(), d.getDay(), d.getUTCHours(), d.getTimezoneOffset());
console.log(d.toString());
console.log(d.toDateString(), d.toISOString());
const l = new Date(2024, 6, 15, 10, 30);
console.log(l.getTime(), l.getHours(), l.toISOString());
const w = new Date(2024, 0, 1, 0, 0);
console.log(w.toString(), w.getTimezoneOffset());
let s = new Date(Date.UTC(2024, 2, 30, 12)); s.setHours(3, 30); console.log(s.toISOString(), s.getHours());
s.setUTCHours(5); console.log(s.toISOString());
s.setDate(31); console.log(s.toString());
console.log(Date.UTC(2024, 0, 15, 10, 30, 45, 123), Date.UTC(99, 1));
`
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	prev := os.Getenv("TZ")
	defer os.Setenv("TZ", prev)
	for _, zone := range []string{"UTC", "Europe/Athens", "America/New_York", "Asia/Tokyo", "Europe/London"} {
		os.Setenv("TZ", zone)
		theirs := runNodeTS(t, src)
		ours := compileAndRun(t, src)
		if strings.TrimSpace(ours) != strings.TrimSpace(theirs) {
			t.Fatalf("TZ=%s: output differs from node:\n--- ours ---\n%s\n--- node ---\n%s", zone, ours, theirs)
		}
	}
}

// Date.parse reads the ECMAScript format (a date-only form as UTC, a date-time
// without an offset as local time) and V8's legacy forms; an unparseable
// string is NaN, and a Date made from one is an Invalid Date.
func TestE2EDateParseAndInvalid(t *testing.T) {
	src := `
console.log(Date.parse("2024-01-15T10:30:00"), Date.parse("2024-07-15T10:30:00.500"), Date.parse("2024-01-15"), Date.parse("2024-01-15T10:30:00Z"));
console.log(new Date("2024-07-15T10:30").toISOString(), Date.parse("2024-07-15T10:30+02:00"), Date.parse("2024-07"), Date.parse("2024"));
console.log(Date.parse("nope"), isNaN(Date.parse("nope")), Date.parse("+275760-09-13T00:00:00Z"), Date.parse("2024-13-01"));
const t = new Date(Date.UTC(2024, 6, 15, 10, 30, 45));
console.log(Date.parse(t.toString()) === t.getTime(), Date.parse(t.toUTCString()) === t.getTime());
console.log(Date.parse("Jul 15 2024"), Date.parse("July 15, 2024 10:30"), Date.parse("2024/07/15 10:30"), Date.parse("15 Jul 2024 10:30:00 GMT"));
const bad = new Date("garbage");
console.log(bad.getTime(), String(bad), bad.toString(), JSON.stringify({ d: bad }), bad);
try { bad.toISOString(); } catch (e) { console.log((e as Error).name, (e as Error).message); }
console.log(new Date(0).getTime(), typeof new Date(0).getTime());
`
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	prev := os.Getenv("TZ")
	defer os.Setenv("TZ", prev)
	for _, zone := range []string{"UTC", "Europe/Athens", "America/New_York", "Asia/Kolkata"} {
		os.Setenv("TZ", zone)
		theirs := runNodeTS(t, src)
		ours := compileAndRun(t, src)
		if strings.TrimSpace(ours) != strings.TrimSpace(theirs) {
			t.Fatalf("TZ=%s: output differs from node:\n--- ours ---\n%s\n--- node ---\n%s", zone, ours, theirs)
		}
	}
}
