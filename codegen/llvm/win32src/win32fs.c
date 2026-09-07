// Windows fs layer (TDD-00177 Stage 3). Compiled alongside win32shim.c and
// win32io.c into the cached shim objects every Windows link pulls in.
//
// The emitted IR calls the POSIX fs surface (stat/lstat/fstat, open/fopen,
// opendir/readdir, mkdir/rmdir/unlink/rename, realpath/readlink/symlink,
// ...) with UTF-8 paths and reads `struct stat` at the glibc x86-64 field
// offsets (statLayout in runtime_fs.go). This file provides that surface
// on Windows, written against Win32 directly with the semantics Node
// itself has there (libuv's src/win/fs.c is the reference):
//
//   * paths are UTF-8 in, UTF-16 to the kernel (never the ANSI code page);
//   * st_mode is synthesised: S_IFDIR/S_IFREG/S_IFLNK plus 0666, minus the
//     write bits when FILE_ATTRIBUTE_READONLY is set, plus 0111 for
//     directories; uid/gid are 0; blksize is 4096; ino/dev are the real
//     file index and volume serial;
//   * birthtime is real (creation time) — stored in the glibc struct's
//     reserved tail, see statLayout's windows case;
//   * fopen/open are always binary: Node's fs is byte-exact;
//   * chmod only toggles the read-only attribute, like Node;
//   * errno is set to the *Linux* numbers the IR's errno→code table and
//     the rest of the shim use; strerror() renders them in libuv's wording.
//
// No <stdio.h>/<io.h> here: those declare fopen/ftell/fseek/open with
// CRT-sized prototypes, and this file redefines them 64-bit/POSIX-shaped.
//
// NO_OLDNAMES: this file exports POSIX names (mkdir/getcwd/chdir/rmdir/
// unlink/...) with our own signatures. Newer mingw-w64 header sets (the CI
// runner's, ADR-00734/00737/00741) declare the CRT "old name" aliases
// (`int mkdir(const char*)`, `char *getcwd(char*,int)`) whose signatures
// differ from ours — a hard "conflicting types" error under a strict-enough
// clang. Suppressing the old-name aliases here (exactly as win32proc.c
// does, which is why it never hit this) removes that whole class of
// conflict; stat/fstat are not old-name-gated and are handled by the
// asm-symbol aliases below.
#define NO_OLDNAMES 1
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <winioctl.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <wchar.h>

// CRT pieces used without their headers.
typedef struct _iobuf FILE;
__declspec(dllimport) int fclose(FILE *);
__declspec(dllimport) int _fseeki64(FILE *, int64_t, int);
__declspec(dllimport) int64_t _ftelli64(FILE *);
__declspec(dllimport) intptr_t _get_osfhandle(int);
// open()/fopen() go through CreateFileW (below) so the share mode includes
// FILE_SHARE_DELETE — the CRT's _wopen/_wfopen omit it, which blocks
// unlinking or renaming a file this process holds open (ADR-00743). The
// kernel HANDLE is then wrapped into a CRT fd / FILE* so the rest of the
// read/write/seek path is unchanged.
__declspec(dllimport) int _open_osfhandle(intptr_t, int);
__declspec(dllimport) FILE *_fdopen(int, const char *);
__declspec(dllimport) int _close(int);
__declspec(dllimport) int *_errno(void);
#define errno (*_errno())
// _open_osfhandle flags.
#define CRT_O_RDONLY 0x0000
#define CRT_O_APPEND 0x0008
#define CRT_O_TEXT   0x4000
#define CRT_O_BINARY 0x8000

enum {
	L_EPERM = 1, L_ENOENT = 2, L_EIO = 5, L_EBADF = 9, L_EACCES = 13,
	L_EBUSY = 16, L_EEXIST = 17, L_EXDEV = 18, L_ENOTDIR = 20, L_EISDIR = 21,
	L_EINVAL = 22, L_EMFILE = 24, L_ENOSPC = 28, L_ESPIPE = 29, L_EROFS = 30,
	L_EPIPE = 32, L_ENAMETOOLONG = 36, L_ENOTEMPTY = 39, L_ELOOP = 40,
	L_ENOSYS = 38,
	L_O_CREAT = 0x40, L_O_EXCL = 0x80, L_O_TRUNC = 0x200, L_O_APPEND = 0x400,
	L_O_DIRECTORY = 0x10000,
};
/* Linux mode bits (what the IR's stat layout carries). Some mingw-w64 header
 * sets pull in <sys/stat.h> through <windows.h> and #define these names to
 * the CRT's own values (0xF000, …), which turned this enum into a syntax
 * error on the CI toolchain; the macros are dropped first (ADR-00734). */
#undef S_IFMT
#undef S_IFDIR
#undef S_IFREG
#undef S_IFLNK
#undef S_IFCHR
#undef S_IFIFO
enum {
	S_IFMT = 0170000, S_IFDIR = 0040000, S_IFREG = 0100000, S_IFLNK = 0120000,
	S_IFCHR = 0020000, S_IFIFO = 0010000,
};
#define WIN_O_RDONLY 0x0000
#define WIN_O_WRONLY 0x0001
#define WIN_O_RDWR 0x0002
#define WIN_O_APPEND 0x0008
#define WIN_O_CREAT 0x0100
#define WIN_O_TRUNC 0x0200
#define WIN_O_EXCL 0x0400
#define WIN_O_BINARY 0x8000
#define WIN_S_IREAD 0x0100
#define WIN_S_IWRITE 0x0080

// ---- errno --------------------------------------------------------------------
static int win_errno(DWORD e) {
	switch (e) {
	case ERROR_FILE_NOT_FOUND: case ERROR_PATH_NOT_FOUND: case ERROR_INVALID_NAME:
	case ERROR_BAD_NETPATH: case ERROR_INVALID_DRIVE: return L_ENOENT;
	case ERROR_ACCESS_DENIED: case ERROR_LOCK_VIOLATION:
	case ERROR_CURRENT_DIRECTORY: return L_EACCES;
	// libuv maps a sharing violation to EBUSY, not EACCES — Node reports an
	// open-elsewhere file as "resource busy or locked".
	case ERROR_SHARING_VIOLATION: return L_EBUSY;
	case ERROR_PRIVILEGE_NOT_HELD: return L_EPERM;
	case ERROR_ALREADY_EXISTS: case ERROR_FILE_EXISTS: return L_EEXIST;
	case ERROR_DIR_NOT_EMPTY: return L_ENOTEMPTY;
	case ERROR_DIRECTORY: return L_ENOTDIR;
	case ERROR_NOT_SAME_DEVICE: return L_EXDEV;
	case ERROR_TOO_MANY_OPEN_FILES: return L_EMFILE;
	case ERROR_DISK_FULL: case ERROR_HANDLE_DISK_FULL: return L_ENOSPC;
	case ERROR_WRITE_PROTECT: return L_EROFS;
	case ERROR_FILENAME_EXCED_RANGE: case ERROR_BUFFER_OVERFLOW: return L_ENAMETOOLONG;
	case ERROR_INVALID_HANDLE: return L_EBADF;
	case ERROR_INVALID_PARAMETER: case ERROR_INVALID_FUNCTION: return L_EINVAL;
	case ERROR_BROKEN_PIPE: case ERROR_NO_DATA: return L_EPIPE;
	case ERROR_CANT_RESOLVE_FILENAME: return L_ELOOP;
	case ERROR_BUSY: return L_EBUSY;
	default: return L_EIO;
	}
}
static int fail(void) { errno = win_errno(GetLastError()); return -1; }
static void *failp(void) { errno = win_errno(GetLastError()); return NULL; }
// free() can reach HeapFree, which may clobber GetLastError() between a
// failed Win32 call and the fail()/failp() that reads it — a dozen error
// paths in this file freed the wide path first (ADR-00739). Every free in
// this file goes through this preserving wrapper; the cost on success
// paths is one SetLastError.
static void free_keep_err(void *p) {
	DWORD e = GetLastError();
	free(p);
	SetLastError(e);
}

// libuv wording (uv_strerror), which is what Node's messages carry.
char *strerror(int e) {
	switch (e) {
	case L_EPERM: return (char *)"operation not permitted";
	case L_ENOENT: return (char *)"no such file or directory";
	case L_EIO: return (char *)"i/o error";
	case L_EBADF: return (char *)"bad file descriptor";
	case L_EACCES: return (char *)"permission denied";
	case L_EBUSY: return (char *)"resource busy or locked";
	case L_EEXIST: return (char *)"file already exists";
	case L_EXDEV: return (char *)"cross-device link not permitted";
	case L_ENOTDIR: return (char *)"not a directory";
	case L_EISDIR: return (char *)"illegal operation on a directory";
	case L_EINVAL: return (char *)"invalid argument";
	case L_EMFILE: return (char *)"too many open files";
	case L_ENOSPC: return (char *)"no space left on device";
	case L_ESPIPE: return (char *)"invalid seek";
	case L_EROFS: return (char *)"read-only file system";
	case L_EPIPE: return (char *)"broken pipe";
	case L_ENAMETOOLONG: return (char *)"name too long";
	case L_ENOTEMPTY: return (char *)"directory not empty";
	case L_ELOOP: return (char *)"too many symbolic links encountered";
	case L_ENOSYS: return (char *)"function not implemented";
	case 11: return (char *)"resource temporarily unavailable";
	case 98: return (char *)"address already in use";
	case 104: return (char *)"connection reset by peer";
	case 107: return (char *)"socket is not connected";
	case 110: return (char *)"connection timed out";
	case 111: return (char *)"connection refused";
	case 115: return (char *)"operation in progress";
	default: return (char *)"unknown error";
	}
}

// ---- UTF-8 <-> UTF-16 ------------------------------------------------------------
// Returns a malloc'd wide string, or NULL with errno set. Forward slashes
// are accepted by every Win32 call used here, so no separator rewriting.
static wchar_t *to_wide(const char *s) {
	if (!s) { errno = L_EINVAL; return NULL; }
	int n = MultiByteToWideChar(CP_UTF8, 0, s, -1, NULL, 0);
	if (n <= 0) { errno = L_EINVAL; return NULL; }
	wchar_t *w = (wchar_t *)malloc((size_t)n * sizeof(wchar_t));
	if (!w) { errno = L_ENOSPC; return NULL; }
	MultiByteToWideChar(CP_UTF8, 0, s, -1, w, n);
	return w;
}
static char *to_utf8(const wchar_t *w) {
	int n = WideCharToMultiByte(CP_UTF8, 0, w, -1, NULL, 0, NULL, NULL);
	if (n <= 0) return NULL;
	char *s = (char *)malloc((size_t)n);
	if (!s) return NULL;
	WideCharToMultiByte(CP_UTF8, 0, w, -1, s, n, NULL, NULL);
	return s;
}
static void to_utf8_into(const wchar_t *w, char *dst, size_t cap) {
	WideCharToMultiByte(CP_UTF8, 0, w, -1, dst, (int)cap, NULL, NULL);
	dst[cap - 1] = 0;
}

// ---- stat ---------------------------------------------------------------------------
// glibc x86-64 struct stat (144 bytes) plus birthtime in the reserved tail.
typedef struct {
	uint64_t st_dev;      // 0
	uint64_t st_ino;      // 8
	uint64_t st_nlink;    // 16
	uint32_t st_mode;     // 24
	uint32_t st_uid;      // 28
	uint32_t st_gid;      // 32
	uint32_t pad0;        // 36
	uint64_t st_rdev;     // 40
	int64_t st_size;      // 48
	int64_t st_blksize;   // 56
	int64_t st_blocks;    // 64
	int64_t atime_sec, atime_nsec;   // 72, 80
	int64_t mtime_sec, mtime_nsec;   // 88, 96
	int64_t ctime_sec, ctime_nsec;   // 104, 112
	int64_t birth_sec, birth_nsec;   // 120, 128
	int64_t reserved;                // 136
} kml_stat;

static void filetime_to_ts(const FILETIME *ft, int64_t *sec, int64_t *nsec) {
	uint64_t t = ((uint64_t)ft->dwHighDateTime << 32) | ft->dwLowDateTime;
	if (t < 116444736000000000ULL) { *sec = 0; *nsec = 0; return; }
	t -= 116444736000000000ULL;
	*sec = (int64_t)(t / 10000000ULL);
	*nsec = (int64_t)(t % 10000000ULL) * 100;
}

static int is_reparse_link(HANDLE h) {
	// A symlink or junction reports S_IFLNK from lstat, like libuv.
	FILE_ATTRIBUTE_TAG_INFO tag;
	if (!GetFileInformationByHandleEx(h, FileAttributeTagInfo, &tag, sizeof tag)) return 0;
	if (!(tag.FileAttributes & FILE_ATTRIBUTE_REPARSE_POINT)) return 0;
	return tag.ReparseTag == IO_REPARSE_TAG_SYMLINK || tag.ReparseTag == IO_REPARSE_TAG_MOUNT_POINT;
}

static int stat_handle(HANDLE h, kml_stat *st, int as_link) {
	BY_HANDLE_FILE_INFORMATION bi;
	if (!GetFileInformationByHandle(h, &bi)) return fail();
	memset(st, 0, sizeof *st);
	DWORD attrs = bi.dwFileAttributes;
	uint32_t mode;
	DWORD type = GetFileType(h);
	if (type == FILE_TYPE_CHAR) mode = S_IFCHR | 0666;
	else if (type == FILE_TYPE_PIPE) mode = S_IFIFO | 0666;
	else if (as_link && is_reparse_link(h)) mode = S_IFLNK | 0777;
	else if (attrs & FILE_ATTRIBUTE_DIRECTORY) mode = S_IFDIR | 0777;
	else mode = S_IFREG | 0666;
	if ((attrs & FILE_ATTRIBUTE_READONLY) && (mode & S_IFMT) != S_IFLNK) mode &= ~(uint32_t)0222;
	st->st_mode = mode;
	st->st_dev = bi.dwVolumeSerialNumber;
	st->st_ino = ((uint64_t)bi.nFileIndexHigh << 32) | bi.nFileIndexLow;
	st->st_nlink = bi.nNumberOfLinks;
	st->st_size = (mode & S_IFMT) == S_IFDIR ? 0 : (int64_t)(((uint64_t)bi.nFileSizeHigh << 32) | bi.nFileSizeLow);
	st->st_blksize = 4096;
	st->st_blocks = (st->st_size + 511) / 512;
	filetime_to_ts(&bi.ftLastAccessTime, &st->atime_sec, &st->atime_nsec);
	filetime_to_ts(&bi.ftLastWriteTime, &st->mtime_sec, &st->mtime_nsec);
	filetime_to_ts(&bi.ftCreationTime, &st->birth_sec, &st->birth_nsec);
	// ctime: the metadata change time, which Win32 exposes as ChangeTime.
	FILE_BASIC_INFO fb;
	if (GetFileInformationByHandleEx(h, FileBasicInfo, &fb, sizeof fb)) {
		FILETIME ct = { (DWORD)fb.ChangeTime.LowPart, (DWORD)fb.ChangeTime.HighPart };
		filetime_to_ts(&ct, &st->ctime_sec, &st->ctime_nsec);
	} else {
		st->ctime_sec = st->mtime_sec; st->ctime_nsec = st->mtime_nsec;
	}
	return 0;
}

static int stat_path(const char *path, kml_stat *st, int follow) {
	wchar_t *w = to_wide(path);
	if (!w) return -1;
	DWORD flags = FILE_FLAG_BACKUP_SEMANTICS | (follow ? 0 : FILE_FLAG_OPEN_REPARSE_POINT);
	HANDLE h = CreateFileW(w, FILE_READ_ATTRIBUTES, FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE, NULL, OPEN_EXISTING, flags, NULL);
	free_keep_err(w);
	if (h == INVALID_HANDLE_VALUE) return fail();
	int r = stat_handle(h, st, !follow);
	CloseHandle(h);
	return r;
}

// stat/fstat (and mkdir/getcwd below) collide with prototypes newer
// mingw-w64 headers pull in transitively (struct stat vs kml_stat, one-arg
// mkdir) — the exact header drift that broke the CI runner twice
// (ADR-00734, ADR-00737). Define them under shim-local C names and bind
// the exported assembly symbol explicitly, so no header prototype can
// conflict no matter what a future header set declares.
int kml_win_stat(const char *path, kml_stat *st) __asm__("stat");
int kml_win_stat(const char *path, kml_stat *st) { return stat_path(path, st, 1); }
int lstat(const char *path, kml_stat *st) { return stat_path(path, st, 0); }
int kml_win_fstat(int fd, kml_stat *st) __asm__("fstat");
int kml_win_fstat(int fd, kml_stat *st) {
	HANDLE h = (HANDLE)_get_osfhandle(fd);
	if (h == INVALID_HANDLE_VALUE) { errno = L_EBADF; return -1; }
	return stat_handle(h, st, 0);
}

// ---- open / fopen ---------------------------------------------------------------------
static int is_dir_path(const wchar_t *w) {
	DWORD a = GetFileAttributesW(w);
	return a != INVALID_FILE_ATTRIBUTES && (a & FILE_ATTRIBUTE_DIRECTORY);
}

// open_shared opens a file via CreateFileW with FILE_SHARE_DELETE in the
// share mode and returns a CRT fd wrapping the kernel handle, so the file
// can be unlinked/renamed while this fd is open (ADR-00743). Returns -1
// with errno set. Shared by open() and fopen().
static int open_shared(const wchar_t *w, int flags, int mode) {
	int acc = flags & 3;
	DWORD access = acc == 0 ? GENERIC_READ : acc == 1 ? GENERIC_WRITE : (GENERIC_READ | GENERIC_WRITE);
	if (flags & L_O_APPEND) {
		// Kernel append (FILE_APPEND_DATA), not the CRT's seek-then-write.
		access = (access & ~(DWORD)GENERIC_WRITE) | FILE_APPEND_DATA;
	}
	DWORD disp;
	if (flags & L_O_CREAT) {
		if (flags & L_O_EXCL) disp = CREATE_NEW;
		else if (flags & L_O_TRUNC) disp = CREATE_ALWAYS;
		else disp = OPEN_ALWAYS;
	} else {
		disp = (flags & L_O_TRUNC) ? TRUNCATE_EXISTING : OPEN_EXISTING;
	}
	DWORD attr = ((flags & L_O_CREAT) && !(mode & 0200)) ? FILE_ATTRIBUTE_READONLY : FILE_ATTRIBUTE_NORMAL;
	HANDLE h = CreateFileW(w, access, FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
	                       NULL, disp, attr, NULL);
	if (h == INVALID_HANDLE_VALUE) { errno = win_errno(GetLastError()); return -1; }
	int osf = CRT_O_BINARY | ((acc == 0) ? CRT_O_RDONLY : 0) | ((flags & L_O_APPEND) ? CRT_O_APPEND : 0);
	int fd = _open_osfhandle((intptr_t)h, osf);
	if (fd < 0) { CloseHandle(h); errno = L_EMFILE; return -1; }
	return fd;
}

int open(const char *path, int flags, ...) {
	wchar_t *w = to_wide(path);
	if (!w) return -1;
	int mode = 0;
	if (flags & L_O_CREAT) {
		va_list ap;
		va_start(ap, flags);
		mode = va_arg(ap, int);
		va_end(ap);
	}
	if (is_dir_path(w)) {
		// Node: opening a directory for reading is EISDIR (libuv maps the
		// CreateFile refusal to that, matching POSIX read attempts).
		free_keep_err(w);
		errno = L_EISDIR;
		return -1;
	}
	int fd = open_shared(w, flags, mode);
	free_keep_err(w);
	return fd;
}

FILE *fopen(const char *path, const char *mode) {
	wchar_t *w = to_wide(path);
	if (!w) return NULL;
	if (is_dir_path(w)) { free_keep_err(w); errno = L_EISDIR; return NULL; }
	// Translate the stdio mode to O_* flags + an fdopen mode. Always binary
	// (Node's fs is byte-exact).
	int flags, plus = 0;
	for (const char *p = mode; *p; p++) if (*p == '+') plus = 1;
	switch (mode[0]) {
	case 'r': flags = plus ? WIN_O_RDWR : WIN_O_RDONLY; break;
	case 'w': flags = (plus ? WIN_O_RDWR : WIN_O_WRONLY) | L_O_CREAT | L_O_TRUNC; break;
	case 'a': flags = (plus ? WIN_O_RDWR : WIN_O_WRONLY) | L_O_CREAT | L_O_APPEND; break;
	default: free_keep_err(w); errno = L_EINVAL; return NULL;
	}
	int fd = open_shared(w, flags, WIN_S_IREAD | WIN_S_IWRITE);
	free_keep_err(w);
	if (fd < 0) return NULL;
	const char *fm = plus ? (mode[0] == 'r' ? "r+b" : mode[0] == 'w' ? "w+b" : "a+b")
	                      : (mode[0] == 'r' ? "rb" : mode[0] == 'w' ? "wb" : "ab");
	FILE *f = _fdopen(fd, fm);
	if (!f) { _close(fd); errno = L_EINVAL; return NULL; }
	return f;
}

// 64-bit seek/tell: the IR passes/reads i64; the CRT's fseek/ftell are long.
int fseek(FILE *f, int64_t off, int whence) { return _fseeki64(f, off, whence); }
int64_t ftell(FILE *f) { return _ftelli64(f); }

// ---- directories -----------------------------------------------------------------------
// struct dirent as the IR reads it (direntNameOffset() == 8 on Windows).
typedef struct { uint32_t d_ino; uint16_t d_reclen; uint16_t d_namlen; char d_name[1024]; } kml_dirent;
typedef struct { HANDLE h; WIN32_FIND_DATAW fd; int pending; int done; kml_dirent ent; } kml_dir;

void *opendir(const char *path) {
	wchar_t *w = to_wide(path);
	if (!w) return NULL;
	size_t len = wcslen(w);
	wchar_t *pat = (wchar_t *)malloc((len + 3) * sizeof(wchar_t));
	if (!pat) { free_keep_err(w); errno = L_ENOSPC; return NULL; }
	wcscpy(pat, w);
	if (len > 0 && pat[len - 1] != L'\\' && pat[len - 1] != L'/') pat[len++] = L'\\';
	pat[len++] = L'*';
	pat[len] = 0;
	// A missing or non-directory path must fail here, not at the first read.
	DWORD a = GetFileAttributesW(w);
	free_keep_err(w);
	if (a == INVALID_FILE_ATTRIBUTES) { free_keep_err(pat); return failp(); }
	if (!(a & FILE_ATTRIBUTE_DIRECTORY)) { free_keep_err(pat); errno = L_ENOTDIR; return NULL; }
	kml_dir *d = (kml_dir *)calloc(1, sizeof *d);
	if (!d) { free_keep_err(pat); errno = L_ENOSPC; return NULL; }
	d->h = FindFirstFileW(pat, &d->fd);
	free_keep_err(pat);
	if (d->h == INVALID_HANDLE_VALUE) {
		if (GetLastError() == ERROR_FILE_NOT_FOUND) { d->done = 1; return d; } // empty dir
		free_keep_err(d);
		return failp();
	}
	d->pending = 1;
	return d;
}

void *readdir(void *dp) {
	kml_dir *d = (kml_dir *)dp;
	if (!d || d->done) return NULL;
	if (!d->pending) {
		if (!FindNextFileW(d->h, &d->fd)) { d->done = 1; return NULL; }
	}
	d->pending = 0;
	to_utf8_into(d->fd.cFileName, d->ent.d_name, sizeof d->ent.d_name);
	d->ent.d_namlen = (uint16_t)strlen(d->ent.d_name);
	d->ent.d_reclen = (uint16_t)sizeof d->ent;
	return &d->ent;
}

int closedir(void *dp) {
	kml_dir *d = (kml_dir *)dp;
	if (!d) { errno = L_EBADF; return -1; }
	if (d->h != INVALID_HANDLE_VALUE && d->h) FindClose(d->h);
	free_keep_err(d);
	return 0;
}

int kml_win_mkdir(const char *path, int mode) __asm__("mkdir");
int kml_win_mkdir(const char *path, int mode) {
	(void)mode;
	wchar_t *w = to_wide(path);
	if (!w) return -1;
	BOOL ok = CreateDirectoryW(w, NULL);
	free_keep_err(w);
	return ok ? 0 : fail();
}

int rmdir(const char *path) {
	wchar_t *w = to_wide(path);
	if (!w) return -1;
	BOOL ok = RemoveDirectoryW(w);
	free_keep_err(w);
	return ok ? 0 : fail();
}

static int unlink_w(wchar_t *w) {
	DWORD a = GetFileAttributesW(w);
	if (a == INVALID_FILE_ATTRIBUTES) return fail();
	// Node on Windows reports EPERM for unlink of a directory (libuv's
	// fs__unlink), not Linux's EISDIR; macOS Node reports EPERM too.
	if ((a & FILE_ATTRIBUTE_DIRECTORY) && !(a & FILE_ATTRIBUTE_REPARSE_POINT)) { errno = L_EPERM; return -1; }
	if (a & FILE_ATTRIBUTE_DIRECTORY) return RemoveDirectoryW(w) ? 0 : fail(); // a junction/dir symlink
	if (a & FILE_ATTRIBUTE_READONLY) {
		// Node/libuv clears read-only before unlinking, so unlink of a
		// read-only file succeeds as it does on POSIX.
		SetFileAttributesW(w, a & ~(DWORD)FILE_ATTRIBUTE_READONLY);
	}
	if (DeleteFileW(w)) return 0;
	DWORD err = GetLastError();
	// A failed unlink must leave the file as it was: restore the read-only
	// attribute libuv restores too (ADR-00739).
	if (a & FILE_ATTRIBUTE_READONLY) SetFileAttributesW(w, a);
	errno = win_errno(err);
	return -1;
}
int unlink(const char *path) {
	wchar_t *w = to_wide(path);
	if (!w) return -1;
	int r = unlink_w(w);
	free_keep_err(w);
	return r;
}
int remove(const char *path) {
	wchar_t *w = to_wide(path);
	if (!w) return -1;
	DWORD a = GetFileAttributesW(w);
	int r;
	if (a != INVALID_FILE_ATTRIBUTES && (a & FILE_ATTRIBUTE_DIRECTORY) && !(a & FILE_ATTRIBUTE_REPARSE_POINT))
		r = RemoveDirectoryW(w) ? 0 : fail();
	else
		r = unlink_w(w);
	free_keep_err(w);
	return r;
}

int rename(const char *from, const char *to) {
	wchar_t *wf = to_wide(from), *wt = to_wide(to);
	if (!wf || !wt) { free_keep_err(wf); free_keep_err(wt); return -1; }
	// POSIX rename replaces an existing target; MOVEFILE_REPLACE_EXISTING.
	BOOL ok = MoveFileExW(wf, wt, MOVEFILE_REPLACE_EXISTING | MOVEFILE_COPY_ALLOWED);
	free_keep_err(wf); free_keep_err(wt);
	return ok ? 0 : fail();
}

int access(const char *path, int mode) {
	wchar_t *w = to_wide(path);
	if (!w) return -1;
	DWORD a = GetFileAttributesW(w);
	free_keep_err(w);
	if (a == INVALID_FILE_ATTRIBUTES) return fail();
	// W_OK (2) against a read-only file fails with EPERM (libuv's fs__access
	// sets UV_EPERM, not EACCES); R_OK/X_OK/F_OK succeed if it exists.
	if ((mode & 2) && (a & FILE_ATTRIBUTE_READONLY) && !(a & FILE_ATTRIBUTE_DIRECTORY)) { errno = L_EPERM; return -1; }
	return 0;
}

int chmod(const char *path, int mode) {
	wchar_t *w = to_wide(path);
	if (!w) return -1;
	DWORD a = GetFileAttributesW(w);
	if (a == INVALID_FILE_ATTRIBUTES) { free_keep_err(w); return fail(); }
	// Only the read-only bit exists here: any owner write bit clears it.
	if (mode & 0222) a &= ~(DWORD)FILE_ATTRIBUTE_READONLY; else a |= FILE_ATTRIBUTE_READONLY;
	BOOL ok = SetFileAttributesW(w, a);
	free_keep_err(w);
	return ok ? 0 : fail();
}

int chdir(const char *path) {
	wchar_t *w = to_wide(path);
	if (!w) return -1;
	BOOL ok = SetCurrentDirectoryW(w);
	free_keep_err(w);
	return ok ? 0 : fail();
}

char *kml_win_getcwd(char *buf, size_t size) __asm__("getcwd");
char *kml_win_getcwd(char *buf, size_t size) {
	wchar_t w[32768];
	DWORD n = GetCurrentDirectoryW(32768, w);
	if (n == 0 || n >= 32768) return failp();
	char *u = to_utf8(w);
	if (!u) { errno = L_EINVAL; return NULL; }
	if (!buf) return u; // glibc extension: allocate
	if (strlen(u) + 1 > size) { free_keep_err(u); errno = L_ENAMETOOLONG; return NULL; }
	strcpy(buf, u);
	free_keep_err(u);
	return buf;
}

int truncate(const char *path, int64_t len) {
	wchar_t *w = to_wide(path);
	if (!w) return -1;
	HANDLE h = CreateFileW(w, GENERIC_WRITE, FILE_SHARE_READ | FILE_SHARE_WRITE, NULL, OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, NULL);
	free_keep_err(w);
	if (h == INVALID_HANDLE_VALUE) return fail();
	LARGE_INTEGER li; li.QuadPart = len;
	int r = 0;
	if (!SetFilePointerEx(h, li, NULL, FILE_BEGIN) || !SetEndOfFile(h)) r = fail();
	CloseHandle(h);
	return r;
}

// ---- links and canonical paths ---------------------------------------------------------
// Strip the \\?\ prefix GetFinalPathNameByHandle produces, like libuv.
static char *final_path_utf8(HANDLE h) {
	wchar_t buf[32768];
	DWORD n = GetFinalPathNameByHandleW(h, buf, 32768, 0);
	if (n == 0 || n >= 32768) return NULL;
	const wchar_t *p = buf;
	if (wcsncmp(p, L"\\\\?\\UNC\\", 8) == 0) { p += 6; wchar_t *q = (wchar_t *)p; q[0] = L'\\'; }
	else if (wcsncmp(p, L"\\\\?\\", 4) == 0) p += 4;
	return to_utf8(p);
}

char *realpath(const char *path, char *resolved) {
	wchar_t *w = to_wide(path);
	if (!w) return NULL;
	HANDLE h = CreateFileW(w, 0, FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE, NULL, OPEN_EXISTING, FILE_FLAG_BACKUP_SEMANTICS, NULL);
	free_keep_err(w);
	if (h == INVALID_HANDLE_VALUE) return failp();
	char *u = final_path_utf8(h);
	CloseHandle(h);
	if (!u) { errno = L_EINVAL; return NULL; }
	if (!resolved) return u;
	strncpy(resolved, u, 4096);
	resolved[4095] = 0;
	free_keep_err(u);
	return resolved;
}

typedef struct {
	ULONG ReparseTag; USHORT ReparseDataLength; USHORT Reserved;
	union {
		struct { USHORT SubstituteNameOffset, SubstituteNameLength, PrintNameOffset, PrintNameLength; ULONG Flags; WCHAR PathBuffer[1]; } sym;
		struct { USHORT SubstituteNameOffset, SubstituteNameLength, PrintNameOffset, PrintNameLength; WCHAR PathBuffer[1]; } mnt;
	} u;
} kml_reparse;

int64_t readlink(const char *path, char *buf, size_t cap) {
	// process.execPath: the IR asks for /proc/self/exe on every host.
	if (strcmp(path, "/proc/self/exe") == 0) {
		wchar_t w[32768];
		DWORD n = GetModuleFileNameW(NULL, w, 32768);
		if (n == 0 || n >= 32768) return fail();
		char *u = to_utf8(w);
		if (!u) { errno = L_EINVAL; return -1; }
		size_t len = strlen(u);
		if (len > cap) len = cap;
		memcpy(buf, u, len);
		free_keep_err(u);
		return (int64_t)len;
	}
	wchar_t *w = to_wide(path);
	if (!w) return -1;
	HANDLE h = CreateFileW(w, FILE_READ_ATTRIBUTES, FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE, NULL, OPEN_EXISTING, FILE_FLAG_OPEN_REPARSE_POINT | FILE_FLAG_BACKUP_SEMANTICS, NULL);
	free_keep_err(w);
	if (h == INVALID_HANDLE_VALUE) return fail();
	char raw[16 * 1024];
	DWORD got = 0;
	BOOL ok = DeviceIoControl(h, FSCTL_GET_REPARSE_POINT, NULL, 0, raw, sizeof raw, &got, NULL);
	CloseHandle(h);
	if (!ok) { errno = L_EINVAL; return -1; } // not a link
	kml_reparse *rp = (kml_reparse *)raw;
	const WCHAR *name; USHORT nlen;
	if (rp->ReparseTag == IO_REPARSE_TAG_SYMLINK) {
		name = rp->u.sym.PathBuffer + rp->u.sym.PrintNameOffset / 2; nlen = rp->u.sym.PrintNameLength / 2;
		if (nlen == 0) { name = rp->u.sym.PathBuffer + rp->u.sym.SubstituteNameOffset / 2; nlen = rp->u.sym.SubstituteNameLength / 2; }
	} else if (rp->ReparseTag == IO_REPARSE_TAG_MOUNT_POINT) {
		name = rp->u.mnt.PathBuffer + rp->u.mnt.PrintNameOffset / 2; nlen = rp->u.mnt.PrintNameLength / 2;
		if (nlen == 0) { name = rp->u.mnt.PathBuffer + rp->u.mnt.SubstituteNameOffset / 2; nlen = rp->u.mnt.SubstituteNameLength / 2; }
	} else { errno = L_EINVAL; return -1; }
	wchar_t tmp[32768];
	if (nlen >= 32768) nlen = 32767;
	memcpy(tmp, name, nlen * sizeof(wchar_t));
	tmp[nlen] = 0;
	const wchar_t *p = tmp;
	if (wcsncmp(p, L"\\??\\", 4) == 0) p += 4;
	char *u = to_utf8(p);
	if (!u) { errno = L_EINVAL; return -1; }
	size_t len = strlen(u);
	if (len > cap) len = cap;
	memcpy(buf, u, len);
	free_keep_err(u);
	return (int64_t)len;
}

int symlink(const char *target, const char *path) {
	wchar_t *wt = to_wide(target), *wp = to_wide(path);
	if (!wt || !wp) { free_keep_err(wt); free_keep_err(wp); return -1; }
	// Node picks 'dir' vs 'file' from the target when no type is given; a
	// relative target is resolved against the link's directory for that.
	DWORD flags = 0;
	{
		wchar_t full[32768];
		const wchar_t *probe = wt;
		if (!(wt[0] == L'\\' || wt[0] == L'/' || (wt[1] == L':'))) {
			wcscpy(full, wp);
			wchar_t *slash = wcsrchr(full, L'\\');
			wchar_t *slash2 = wcsrchr(full, L'/');
			if (slash2 > slash) slash = slash2;
			if (slash) { slash[1] = 0; wcscat(full, wt); probe = full; }
		}
		DWORD a = GetFileAttributesW(probe);
		if (a != INVALID_FILE_ATTRIBUTES && (a & FILE_ATTRIBUTE_DIRECTORY)) flags |= SYMBOLIC_LINK_FLAG_DIRECTORY;
	}
	flags |= SYMBOLIC_LINK_FLAG_ALLOW_UNPRIVILEGED_CREATE; // Developer Mode; ignored otherwise
	BOOL ok = CreateSymbolicLinkW(wp, wt, flags);
	if (!ok && GetLastError() == ERROR_INVALID_PARAMETER) {
		// Older Windows rejects the unprivileged flag outright; retry without.
		ok = CreateSymbolicLinkW(wp, wt, flags & ~(DWORD)SYMBOLIC_LINK_FLAG_ALLOW_UNPRIVILEGED_CREATE);
	}
	DWORD err = GetLastError();
	free_keep_err(wt); free_keep_err(wp);
	if (ok) return 0;
	errno = err == ERROR_PRIVILEGE_NOT_HELD ? L_EPERM : win_errno(err);
	return -1;
}

char *mkdtemp(char *tmpl) {
	size_t len = strlen(tmpl);
	if (len < 6 || strcmp(tmpl + len - 6, "XXXXXX") != 0) { errno = L_EINVAL; return NULL; }
	static const char alphabet[] = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
	for (int attempt = 0; attempt < 100; attempt++) {
		// CSPRNG names, as Node's mkdtemp uses (uv__random): the old
		// GetTickCount64 × pid mix was predictable (ADR-00739). getrandom is
		// the shim's BCryptGenRandom wrapper (win32shim.c).
		extern int64_t getrandom(void *, size_t, unsigned);
		uint64_t r = 0;
		if (getrandom(&r, sizeof r, 0) != (int64_t)sizeof r)
			r = ((uint64_t)GetTickCount64() * 6364136223846793005ULL) ^ ((uint64_t)GetCurrentProcessId() << 32) ^ (uint64_t)attempt * 0x9E3779B97F4A7C15ULL;
		for (int i = 0; i < 6; i++) { tmpl[len - 6 + i] = alphabet[r % 62]; r /= 62; }
		wchar_t *w = to_wide(tmpl);
		if (!w) return NULL;
		BOOL ok = CreateDirectoryW(w, NULL);
		DWORD err = GetLastError();
		free_keep_err(w);
		if (ok) return tmpl;
		if (err != ERROR_ALREADY_EXISTS) { errno = win_errno(err); return NULL; }
	}
	errno = L_EEXIST;
	return NULL;
}
