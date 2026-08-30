package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emit_tty.go — TDD-00031 terminal-control primitives.
//
// Split by faithfulness: the Node-faithful surface (setRawMode, columns/rows,
// SIGWINCH) hangs off `process` exactly where real Node puts it; the one
// primitive Node has NO counterpart for — a synchronous single-keystroke read
// off fd 0 (Node does raw input via process.stdin.on('data') events, never a
// blocking byte read) — lives under the explicit `klain:tty` module instead.
//
// All four calls route through a single embedded C shim (TTYShimSource) rather
// than hand-laid termios/winsize structs in IR: `struct termios`'s field
// offsets and the ECHO/ICANON/ISIG bit values differ between glibc and Darwin
// (tcflag_t is 4 bytes on Linux, 8 on macOS — c_lflag lands at a different
// offset), the exact struct-layout footgun ADR-00051 hit with ucontext_t. A C
// translation unit compiled by clang against the platform's own <termios.h>
// sidesteps every one of those differences by construction, on both dev
// machines, with no per-platform IR.

// UsesTtyShim reports whether the program used any terminal-control primitive,
// so the CLI driver links the embedded C shim (EmbeddedCSources).
func (e *Emitter) UsesTtyShim() bool { return e.usedTtyRead || e.usedTermiosRaw || e.usedWinSize }

// ensureTtySetRaw declares the raw-mode toggle once.
func (e *Emitter) ensureTtySetRaw() {
	if e.usedTermiosRaw {
		return
	}
	e.usedTermiosRaw = true
	e.emitGlobal("declare void @__kml_tty_set_raw(i32)")
}

// ensureTtyWinSize declares the winsize getters once.
func (e *Emitter) ensureTtyWinSize() {
	if e.usedWinSize {
		return
	}
	e.usedWinSize = true
	e.emitGlobal("declare i32 @__kml_tty_cols(i32)")
	e.emitGlobal("declare i32 @__kml_tty_rows(i32)")
}

// ensureTtyRead declares the raw stdin readers once.
func (e *Emitter) ensureTtyRead() {
	if e.usedTtyRead {
		return
	}
	e.usedTtyRead = true
	e.emitGlobal("declare i32 @__kml_tty_read_byte()")
	e.emitGlobal("declare ptr @__kml_tty_read_key()")
}

// emitProcessSetRawMode implements process.stdin.setRawMode(enabled): the
// libuv UV_TTY_MODE_RAW termios transform on fd 0 (disables canonical mode,
// echo, and signal generation — Ctrl-C stops delivering SIGINT and must be
// watched for as the raw 0x03 byte, exactly as Node behaves). Passing false
// restores the terminal to the state captured on the first enable.
func (e *Emitter) emitProcessSetRawMode(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: process.stdin.setRawMode takes exactly 1 argument (enabled)", pos.Line, pos.Col)
	}
	v, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	v = e.coerce(v, TypeBool)
	flag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i32", flag, v.Ref))
	e.ensureTtySetRaw()
	e.emitInstr(fmt.Sprintf("call void @__kml_tty_set_raw(i32 %s)", flag))
	return Value{Ty: TypeVoid}, nil
}

// emitProcessWinSize implements process.stdout/.stderr `.columns` / `.rows`:
// a live ioctl(TIOCGWINSZ) read (never cached, matching Node). When the fd
// isn't a terminal (piped/redirected) the ioctl fails and the shim returns
// the classic 80x24 fallback rather than Node's `undefined` — a documented
// divergence, since this compiler has no clean undefined-number and 80x24 is
// the same fallback most CLI libraries substitute for that undefined anyway.
func (e *Emitter) emitProcessWinSize(fd int, field string) Value {
	e.ensureTtyWinSize()
	fn := "__kml_tty_cols"
	if field == "rows" {
		fn = "__kml_tty_rows"
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @%s(i32 %d)", r, fn, fd))
	w := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sext i32 %s to i64", w, r))
	return Value{Ref: w, Ty: TypeI64}
}

// emitTtyModuleCall dispatches `tty__kml_builtin.<member>(...)` — the
// klain:tty module's bespoke synchronous raw reads.
func (e *Emitter) emitTtyModuleCall(member string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch member {
	case "readByte":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: klain:tty readByte() takes no arguments", pos.Line, pos.Col)
		}
		e.ensureTtyRead()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_tty_read_byte()", r))
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sext i32 %s to i64", b, r))
		return Value{Ref: b, Ty: TypeI64}, nil
	case "readKey":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: klain:tty readKey() takes no arguments", pos.Line, pos.Col)
		}
		e.ensureTtyRead()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_tty_read_key()", r))
		return Value{Ref: r, Ty: TypePtr}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: klain:tty has no member '%s'", pos.Line, pos.Col, member)
}

// TTYShimSource is the embedded C behind all of TDD-00031's terminal
// primitives. It compiles against the platform's own <termios.h>/<sys/ioctl.h>
// so struct layouts and flag-bit values are always the host's, never assumed.
// readKey's returned string uses the length-prefixed layout every runtime
// string producer here shares ([i64 len][bytes][NUL], value ptr = base+8).
func TTYShimSource() string {
	return `#include <termios.h>
#include <sys/ioctl.h>
#include <unistd.h>
#include <stdlib.h>
#include <string.h>

/* Length-prefixed string alloc matching __kml_str_alloc's layout. */
static char *kmltty_str(const char *buf, long n) {
  char *b = (char *)malloc(n + 9);
  *(long *)b = n;
  if (n > 0) memcpy(b + 8, buf, n);
  b[8 + n] = 0;
  return b + 8;
}

/* Terminal state captured on the first raw-mode enable, restored on disable. */
static struct termios kmltty_saved;
static int kmltty_saved_valid = 0;

void __kml_tty_set_raw(int enabled) {
  if (enabled) {
    struct termios t;
    if (tcgetattr(0, &t) != 0) return;
    if (!kmltty_saved_valid) { kmltty_saved = t; kmltty_saved_valid = 1; }
    struct termios raw = t;
    /* libuv UV_TTY_MODE_RAW. c_oflag is left untouched (OPOST stays on) so
       console.log's '\n' still expands to '\r\n' while raw mode is active. */
    raw.c_iflag &= ~(IGNBRK | BRKINT | PARMRK | ISTRIP | INLCR | IGNCR | ICRNL | IXON);
    raw.c_lflag &= ~(ECHO | ICANON | IEXTEN | ISIG);
    raw.c_cflag &= ~(CSIZE | PARENB);
    raw.c_cflag |= CS8;
    raw.c_cc[VMIN] = 1;
    raw.c_cc[VTIME] = 0;
    tcsetattr(0, TCSANOW, &raw);
  } else if (kmltty_saved_valid) {
    tcsetattr(0, TCSANOW, &kmltty_saved);
  }
}

int __kml_tty_cols(int fd) {
  struct winsize ws;
  if (ioctl(fd, TIOCGWINSZ, &ws) == 0 && ws.ws_col > 0) return ws.ws_col;
  return 80; /* fallback when fd is not a terminal (Node yields undefined) */
}

int __kml_tty_rows(int fd) {
  struct winsize ws;
  if (ioctl(fd, TIOCGWINSZ, &ws) == 0 && ws.ws_row > 0) return ws.ws_row;
  return 24;
}

/* One blocking byte off fd 0; -1 at EOF. */
int __kml_tty_read_byte(void) {
  unsigned char c;
  ssize_t n = read(0, &c, 1);
  if (n <= 0) return -1;
  return (int)c;
}

/* One keystroke off fd 0 as a string: a single read() returns the whole
   burst of an escape sequence (arrow keys are ESC '[' 'A', 3 bytes) in raw
   mode, so a caller sees a multi-byte key as one string. Empty string at EOF. */
char *__kml_tty_read_key(void) {
  char buf[32];
  ssize_t n = read(0, buf, sizeof(buf) - 1);
  if (n <= 0) return kmltty_str("", 0);
  return kmltty_str(buf, (long)n);
}
`
}
