// win32fswatch.c — the Windows fs.watch backend (TDD-00181 Stage 2).
//
// Windows has no select()-able change fd, so each watcher runs a thread
// blocking in ReadDirectoryChangesW; a change is parsed into a
// (watcher, kind, name) record pushed onto a mutex-guarded queue, and one
// byte is written to a loopback wakeup socket. The event loop adds that
// socket's read end to its fd_set (__kml_fswatch_win_wakefd), so select()
// wakes; __kml_fswatch_win_next drains the queue and the wakeup bytes. This
// wakeup socket is the loop wakeup channel the TDD-00180 §1 audit found
// missing, built here fs.watch-first but reusable.
//
// The socket/read/write/close and socketpair here are the shim's POSIX-named
// entry points (win32io.c / win32proc.c) over its fd table, so the returned
// wakefd is a fd the shim's select() understands.
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <wchar.h>

// ---- shim entry points (POSIX-named, over the shim fd table) ----------------
extern int socketpair(int domain, int type, int protocol, int sv[2]);
extern int64_t read(int fd, void *buf, size_t n);
extern int64_t write(int fd, const void *buf, size_t n);
extern int close(int fd);
extern int fcntl(int fd, int cmd, ...);
__declspec(dllimport) int *_errno(void);
#define errno (*_errno())
enum { L_ENOENT = 2, L_EACCES = 13, L_EINVAL = 22 };

// ---- event queue ------------------------------------------------------------
typedef struct WatchEvent {
	void *ctx;
	int isRename;
	char name[520]; // a UTF-8 file name (260 UTF-16 units worst case)
	struct WatchEvent *next;
} WatchEvent;

typedef struct Watcher {
	HANDLE dir;
	HANDLE thread;
	volatile LONG stop;
	int isFileWatch;      // watching a single file: filter events by name
	wchar_t filter[260];  // the basename to match (file watch only)
} Watcher;

static CRITICAL_SECTION g_lock;
static WatchEvent *g_head, *g_tail;
static int g_wake_r = -1, g_wake_w = -1;
static LONG g_inited = 0;

static void fsw_init(void) {
	if (InterlockedCompareExchange(&g_inited, 1, 0) != 0) return;
	InitializeCriticalSection(&g_lock);
	int sv[2];
	if (socketpair(0, 1, 0, sv) == 0) {
		g_wake_r = sv[0];
		g_wake_w = sv[1];
		fcntl(g_wake_r, 4, 0x800); // F_SETFL, O_NONBLOCK — the drain must not block
	}
}

static void fsw_enqueue(void *ctx, int isRename, const char *name) {
	WatchEvent *e = (WatchEvent *)calloc(1, sizeof *e);
	if (!e) return;
	e->ctx = ctx;
	e->isRename = isRename;
	strncpy(e->name, name, sizeof e->name - 1);
	EnterCriticalSection(&g_lock);
	if (g_tail) g_tail->next = e; else g_head = e;
	g_tail = e;
	LeaveCriticalSection(&g_lock);
	if (g_wake_w >= 0) { char b = 'x'; write(g_wake_w, &b, 1); }
}

// FILE_ACTION_* → 'rename' (add/remove/rename) vs 'change' (modified).
static int action_is_rename(DWORD a) {
	return a == FILE_ACTION_ADDED || a == FILE_ACTION_REMOVED ||
	       a == FILE_ACTION_RENAMED_OLD_NAME || a == FILE_ACTION_RENAMED_NEW_NAME;
}

static DWORD WINAPI fsw_thread(LPVOID p) {
	Watcher *w = (Watcher *)p;
	// A byte buffer for FILE_NOTIFY_INFORMATION records. Aligned for the DWORDs.
	__declspec(align(4)) char buf[65536];
	const DWORD mask = FILE_NOTIFY_CHANGE_FILE_NAME | FILE_NOTIFY_CHANGE_DIR_NAME |
	                   FILE_NOTIFY_CHANGE_ATTRIBUTES | FILE_NOTIFY_CHANGE_SIZE |
	                   FILE_NOTIFY_CHANGE_LAST_WRITE | FILE_NOTIFY_CHANGE_CREATION |
	                   FILE_NOTIFY_CHANGE_SECURITY;
	while (!w->stop) {
		DWORD got = 0;
		BOOL ok = ReadDirectoryChangesW(w->dir, buf, sizeof buf, FALSE, mask, &got, NULL, NULL);
		if (w->stop) break;
		if (!ok || got == 0) continue;
		FILE_NOTIFY_INFORMATION *fni = (FILE_NOTIFY_INFORMATION *)buf;
		for (;;) {
			int nameChars = (int)(fni->FileNameLength / sizeof(WCHAR));
			// The reported name is relative to the watched directory.
			if (!(w->isFileWatch) ||
			    (nameChars == (int)wcslen(w->filter) &&
			     _wcsnicmp(fni->FileName, w->filter, nameChars) == 0)) {
				char utf8[520];
				int n = WideCharToMultiByte(CP_UTF8, 0, fni->FileName, nameChars,
				                            utf8, (int)sizeof utf8 - 1, NULL, NULL);
				if (n < 0) n = 0;
				utf8[n] = 0;
				fsw_enqueue(w, action_is_rename(fni->Action), utf8);
			}
			if (fni->NextEntryOffset == 0) break;
			fni = (FILE_NOTIFY_INFORMATION *)((char *)fni + fni->NextEntryOffset);
		}
	}
	return 0;
}

// Split `path` into a directory HANDLE plus (for a file watch) the basename to
// filter on. A directory path is watched wholesale; anything else watches its
// parent directory and filters by the trailing component.
void *__kml_fswatch_win_start(const char *path) {
	fsw_init();
	int wn = MultiByteToWideChar(CP_UTF8, 0, path, -1, NULL, 0);
	if (wn <= 0) { errno = L_EINVAL; return NULL; }
	wchar_t *wp = (wchar_t *)malloc((size_t)wn * sizeof(wchar_t));
	if (!wp) { errno = L_EINVAL; return NULL; }
	MultiByteToWideChar(CP_UTF8, 0, path, -1, wp, wn);

	DWORD attr = GetFileAttributesW(wp);
	if (attr == INVALID_FILE_ATTRIBUTES) {
		DWORD e = GetLastError();
		free(wp);
		errno = (e == ERROR_FILE_NOT_FOUND || e == ERROR_PATH_NOT_FOUND) ? L_ENOENT : L_EACCES;
		return NULL;
	}
	Watcher *w = (Watcher *)calloc(1, sizeof *w);
	if (!w) { free(wp); errno = L_EINVAL; return NULL; }

	wchar_t dirbuf[32768];
	if (attr & FILE_ATTRIBUTE_DIRECTORY) {
		w->isFileWatch = 0;
		wcsncpy(dirbuf, wp, 32767);
	} else {
		w->isFileWatch = 1;
		wcsncpy(dirbuf, wp, 32767);
		wchar_t *slash = wcsrchr(dirbuf, L'\\');
		wchar_t *slash2 = wcsrchr(dirbuf, L'/');
		if (slash2 > slash) slash = slash2;
		if (slash) { wcsncpy(w->filter, slash + 1, 259); *slash = 0; }
		else { wcsncpy(w->filter, dirbuf, 259); wcscpy(dirbuf, L"."); }
	}
	free(wp);

	w->dir = CreateFileW(dirbuf, FILE_LIST_DIRECTORY,
	                     FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
	                     NULL, OPEN_EXISTING, FILE_FLAG_BACKUP_SEMANTICS, NULL);
	if (w->dir == INVALID_HANDLE_VALUE) {
		DWORD e = GetLastError();
		free(w);
		errno = (e == ERROR_FILE_NOT_FOUND || e == ERROR_PATH_NOT_FOUND) ? L_ENOENT : L_EACCES;
		return NULL;
	}
	w->thread = CreateThread(NULL, 0, fsw_thread, w, 0, NULL);
	if (!w->thread) { CloseHandle(w->dir); free(w); errno = L_EINVAL; return NULL; }
	return w;
}

// The wakeup socket's read end — the loop adds it to its read fd_set.
int __kml_fswatch_win_wakefd(void) {
	fsw_init();
	return g_wake_r;
}

// Pop one queued event (1) or, when the queue is empty, drain the wakeup
// bytes and return 0. outName must have room for 520 bytes.
int __kml_fswatch_win_next(void **outCtx, int *outIsRename, char *outName) {
	if (!g_inited) return 0;
	EnterCriticalSection(&g_lock);
	WatchEvent *e = g_head;
	if (e) { g_head = e->next; if (!g_head) g_tail = NULL; }
	LeaveCriticalSection(&g_lock);
	if (e) {
		*outCtx = e->ctx;
		*outIsRename = e->isRename;
		strncpy(outName, e->name, 519);
		outName[519] = 0;
		free(e);
		return 1;
	}
	if (g_wake_r >= 0) { char drain[256]; while (read(g_wake_r, drain, sizeof drain) > 0) { } }
	return 0;
}

void __kml_fswatch_win_stop(void *ctx) {
	Watcher *w = (Watcher *)ctx;
	if (!w) return;
	InterlockedExchange(&w->stop, 1);
	CancelIoEx(w->dir, NULL);  // unblock the thread's ReadDirectoryChangesW
	if (w->thread) { WaitForSingleObject(w->thread, 2000); CloseHandle(w->thread); }
	CloseHandle(w->dir);
	free(w);
}
