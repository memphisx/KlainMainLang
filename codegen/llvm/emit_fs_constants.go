// emit_fs_constants.go — `fs.constants` (ADR-00795), modelled on
// `http2.constants` (emit_http2_settings.go).
//
// `fs.constants` is a compile-time namespace: a member read resolves to a
// numeric literal with no runtime object, and a binding (`const c =
// fs.constants`, or `import { constants } from 'fs'`) carries the IsFsConstants
// flag so reads through the alias resolve identically.
//
// The platform-invariant set is the POSIX access modes (`accessSync(path,
// mode)`) and libuv's copyFile flags (`copyFileSync(src, dest, mode)`) — the
// access bits are POSIX-standard (identical on Linux and macOS) and the
// COPYFILE_* values are libuv's own, not the host OS's, so one compile-time
// table is correct on every target.
//
// The `O_*` open flags (ADR-00987) are the
// argument `openSync(path, flags)` consumes as a raw numeric mask, passed
// straight to the host `open(2)`. Their values are genuinely host-specific
// (Darwin and glibc disagree on everything past O_RDONLY/O_WRONLY/O_RDWR), so
// they are resolved against `targetGOOS()` — the same target-aware table
// `openFlagBits` already uses for the string-flag path (`'w'`/`'a'`/…), so
// `openSync(p, O_WRONLY|O_CREAT|O_TRUNC)` and `openSync(p, 'w')` produce
// identical masks. A cross-compiled binary carries its *target's* values, which
// are what the `open(2)` it actually runs against expects.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// fsConstants maps each platform-invariant fs.constants member to its numeric
// value. Node exposes these as `number`s, so they emit as doubles. The
// host-specific O_* flags are not here — see fsOpenConstant.
var fsConstants = map[string]float64{
	// Access modes — the mode argument to accessSync (POSIX F_OK/X_OK/W_OK/R_OK).
	"F_OK": 0, "X_OK": 1, "W_OK": 2, "R_OK": 4,
	// copyFile flags — the mode argument to copyFileSync (libuv UV_FS_COPYFILE_*).
	"COPYFILE_EXCL": 1, "COPYFILE_FICLONE": 2, "COPYFILE_FICLONE_FORCE": 4,
}

// fsOpenConstant resolves an O_* open flag for the target OS, or reports absent.
// Values match each platform's <fcntl.h> (and MSVC's <fcntl.h> for Windows) —
// the same bits `openFlagBits` bakes into the string-flag masks. O_RDONLY/
// O_WRONLY/O_RDWR are the universal access-mode low bits; the rest are the
// per-OS flag bits. A flag a target's Node does not expose (e.g. O_DIRECT on
// macOS, O_SYMLINK on Linux) is absent there too, matching Node's own set.
func fsOpenConstant(name string) (float64, bool) {
	switch targetGOOS() {
	case "darwin":
		m := map[string]float64{
			"O_RDONLY": 0, "O_WRONLY": 1, "O_RDWR": 2,
			"O_APPEND": 0x0008, "O_CREAT": 0x0200, "O_TRUNC": 0x0400,
			"O_EXCL": 0x0800, "O_NOFOLLOW": 0x0100, "O_SYNC": 0x0080,
			"O_NONBLOCK": 0x0004, "O_NOCTTY": 0x20000, "O_DIRECTORY": 0x100000,
			"O_SYMLINK": 0x200000, "O_DSYNC": 0x400000,
		}
		v, ok := m[name]
		return v, ok
	case "windows":
		// The Windows fs runtime presents a Linux-ABI O_* encoding (win32fs.c's
		// L_O_* constants, and the same bits openFlagBits bakes into the
		// string-flag masks), so a numeric fs.constants mask reaches open_shared
		// in the encoding it checks. Using the MSVC _O_* values here instead
		// (O_CREAT 0x0100) made `openSync(p, O_WRONLY|O_CREAT|O_TRUNC)` miss the
		// L_O_CREAT (0x40) bit and fail ENOENT, while the string flag `'w'`
		// worked — the numeric raw values differ from Node's Windows constants,
		// but they must match this runtime's own open path. Node exposes only
		// this subset on Windows.
		m := map[string]float64{
			"O_RDONLY": 0, "O_WRONLY": 1, "O_RDWR": 2,
			"O_APPEND": 0x400, "O_CREAT": 0x40, "O_TRUNC": 0x200,
			"O_EXCL": 0x80,
		}
		v, ok := m[name]
		return v, ok
	default:
		// Linux/Android — asm-generic <fcntl.h>, identical on x86-64 and arm64.
		m := map[string]float64{
			"O_RDONLY": 0, "O_WRONLY": 1, "O_RDWR": 2,
			"O_CREAT": 0x40, "O_EXCL": 0x80, "O_NOCTTY": 0x100,
			"O_TRUNC": 0x200, "O_APPEND": 0x400, "O_NONBLOCK": 0x800,
			"O_DSYNC": 0x1000, "O_DIRECT": 0x4000, "O_DIRECTORY": 0x10000,
			"O_NOFOLLOW": 0x20000, "O_NOATIME": 0x40000, "O_SYNC": 0x101000,
		}
		v, ok := m[name]
		return v, ok
	}
}

// isFsConstantsExpr reports whether expr statically denotes fs.constants — the
// direct `fs.constants` member read or a binding carrying the flag.
func (e *Emitter) isFsConstantsExpr(expr ast.Expression) bool {
	if m, ok := expr.(*ast.MemberExpression); ok && m.Property == "constants" {
		if id, ok := m.Object.(*ast.Identifier); ok && id.Name == "fs__kml_builtin" {
			return true
		}
	}
	return e.inferExprType(expr).IsFsConstants
}

// emitFsConstant resolves one fs.constants member to its numeric literal — a
// platform-invariant member first, then a target-specific O_* open flag.
func (e *Emitter) emitFsConstant(name string, pos ast.Pos) (Value, error) {
	v, ok := fsConstants[name]
	if !ok {
		v, ok = fsOpenConstant(name)
	}
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: fs.constants has no member '%s' (the POSIX access modes F_OK/R_OK/W_OK/X_OK, the copyFile flags COPYFILE_EXCL/COPYFILE_FICLONE/COPYFILE_FICLONE_FORCE, and the O_* open flags for this target are covered)", pos.Line, pos.Col, name)
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fadd double 0.0, %s", r, llvmDoubleLit(v)))
	return Value{Ref: r, Ty: TypeF64}, nil
}
