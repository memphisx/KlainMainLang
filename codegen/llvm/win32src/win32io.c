// Windows I/O layer (TDD-00177 Stage 2, TDD-00252). Compiled alongside
// win32shim.c into the cached shim object every Windows clang invocation
// links.
//
// The runtime's C is written against a small-integer file-descriptor world:
// sockets, pipes, files and stdio are all `int fd`s driven by read/write/
// close/fcntl and the POSIX socket calls. This file provides exactly that
// surface on Windows; the event loop is libuv's (TDD-00252), which polls a
// socket by its SOCKET (__kml_win_poll_socket) and drives pipes and consoles
// as its own stream handles, so nothing here waits for readiness:
//
//   * files and the inherited stdio are CRT fds; sockets and the pipe ends
//     this layer owns get fd numbers from its own range, with the handle in a
//     side table, so the CRT never wraps a socket handle — see the fd table;
//   * the side table records a per-fd non-blocking flag, so read/write/
//     close/fcntl dispatch to non-blocking Winsock calls for sockets and to
//     Win32 pipe and console calls otherwise;
//   * getcontext/makecontext/swapcontext are provided over Win32 Fibers.
//
// Constants come in as the *Linux* values the IR's Go-side helpers emit
// (httpSockConstants, httpNonblockFlag, httpEagainErrno, the fs open-flag
// map) and are translated here, so the emitters need no Windows cases for
// this layer. errno is likewise set to the Linux numbers the IR compares
// against (EAGAIN 11, EINPROGRESS 115, ECONNRESET 104, ...).
//
// Winsock is bound through GetProcAddress rather than <winsock2.h>: this
// file *defines* socket/bind/connect/... under their POSIX names and
// signatures, which would conflict with the header's prototypes.
// NO_OLDNAMES keeps io.h from declaring read/write/close/open/dup2 with the
// CRT's int-sized prototypes, which this file redefines POSIX-shaped.
#define NO_OLDNAMES 1
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <errno.h>
#include <fcntl.h>
#include <io.h>
#include <sys/stat.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdarg.h>

int close(int fd); // defined below; dup2 needs it first
unsigned short htons(unsigned short v); // defined below

// ---- Linux ABI constants the IR uses -------------------------------------
enum {
	L_EAGAIN = 11, L_EINTR = 4, L_EBADF = 9, L_EINVAL = 22, L_EPIPE = 32,
	L_ECONNRESET = 104, L_ECONNREFUSED = 111, L_EINPROGRESS = 115,
	L_EADDRINUSE = 98, L_ENOTSOCK = 88, L_ETIMEDOUT = 110, L_ENOTCONN = 107,
	L_EACCES = 13, L_ENOENT = 2, L_EEXIST = 17, L_EISDIR = 21, L_ENOTDIR = 20,
	L_EMFILE = 24, L_ENOSYS = 38,
	// Socket errno values (Linux asm-generic) the WSA table maps onto, so
	// err.code on a compiled program matches Node's (ADR-00742).
	L_EFAULT = 14, L_EDESTADDRREQ = 89, L_EMSGSIZE = 90, L_EPROTOTYPE = 91,
	L_ENOPROTOOPT = 92, L_EPROTONOSUPPORT = 93, L_EOPNOTSUPP = 95,
	L_EAFNOSUPPORT = 97, L_EADDRNOTAVAIL = 99, L_ENETDOWN = 100,
	L_ENETUNREACH = 101, L_ENETRESET = 102, L_ECONNABORTED = 103,
	L_ENOBUFS = 105, L_EISCONN = 106, L_ESHUTDOWN = 108, L_EHOSTDOWN = 112,
	L_EHOSTUNREACH = 113, L_ENOMEM = 12, L_EALREADY = 114, L_ELOOP = 40,
	L_ENAMETOOLONG = 36, L_ENOTEMPTY = 39, L_ECANCELED = 125,
	L_F_GETFL = 3, L_F_SETFL = 4, L_O_NONBLOCK = 0x800,
	L_SOL_SOCKET = 1, L_SO_REUSEADDR = 2, L_SO_KEEPALIVE = 9, L_SO_BROADCAST = 6,
	L_SO_REUSEPORT = 15, L_SO_ERROR = 4,
	L_IPPROTO_TCP = 6, L_TCP_KEEPIDLE = 4, // Linux TCP keepalive-idle; opt 4 is TCP_MAXSEG on Windows

	L_O_CREAT = 0x40, L_O_EXCL = 0x80, L_O_TRUNC = 0x200, L_O_APPEND = 0x400,
};

// ---- Winsock, bound by hand -------------------------------------------------
#define KFD_MAX 16384
typedef uintptr_t ws_SOCKET;
#define WS_INVALID ((ws_SOCKET)~(uintptr_t)0)
#define WS_ERROR (-1)
#define WS_SOL_SOCKET 0xffff
#define WS_SO_REUSEADDR 0x0004
#define WS_SO_KEEPALIVE 0x0008
#define WS_SO_BROADCAST 0x0020
#define WS_SO_ERROR 0x1007
#define WS_SO_EXCLUSIVEADDRUSE ((int)(~WS_SO_REUSEADDR))
#define WS_FIONBIO 0x8004667eUL
#define WS_POLLRDNORM 0x0100
#define WS_POLLWRNORM 0x0010
#define WS_POLLERR 0x0001
#define WS_POLLHUP 0x0002
#define WS_POLLNVAL 0x0004
// WSAE* error codes come from winerror.h (pulled in by windows.h).

typedef struct { ws_SOCKET fd; short events; short revents; } ws_pollfd;
typedef struct ws_addrinfo {
	int ai_flags, ai_family, ai_socktype, ai_protocol;
	size_t ai_addrlen;
	char *ai_canonname;
	void *ai_addr;
	struct ws_addrinfo *ai_next;
} ws_addrinfo;
typedef struct { WORD wVersion, wHighVersion; char pad[400]; } ws_WSADATA;

static int (WINAPI *p_WSAStartup)(WORD, ws_WSADATA *);
static int (WINAPI *p_WSAGetLastError)(void);
static ws_SOCKET (WINAPI *p_socket)(int, int, int);
static int (WINAPI *p_bind)(ws_SOCKET, const void *, int);
static int (WINAPI *p_listen)(ws_SOCKET, int);
static ws_SOCKET (WINAPI *p_accept)(ws_SOCKET, void *, int *);
static int (WINAPI *p_connect)(ws_SOCKET, const void *, int);
static int (WINAPI *p_recv)(ws_SOCKET, char *, int, int);
static int (WINAPI *p_send)(ws_SOCKET, const char *, int, int);
static int (WINAPI *p_recvfrom)(ws_SOCKET, char *, int, int, void *, int *);
static int (WINAPI *p_sendto)(ws_SOCKET, const char *, int, int, const void *, int);
static int (WINAPI *p_closesocket)(ws_SOCKET);
static int (WINAPI *p_shutdown)(ws_SOCKET, int);
static int (WINAPI *p_setsockopt)(ws_SOCKET, int, int, const char *, int);
static int (WINAPI *p_getsockopt)(ws_SOCKET, int, int, char *, int *);
static int (WINAPI *p_getsockname)(ws_SOCKET, void *, int *);
static int (WINAPI *p_getpeername)(ws_SOCKET, void *, int *);
static int (WINAPI *p_ioctlsocket)(ws_SOCKET, long, unsigned long *);
static int (WINAPI *p_WSAIoctl)(ws_SOCKET, unsigned long, void *, unsigned long, void *, unsigned long, unsigned long *, void *, void *);
static int (WINAPI *p_WSAPoll)(ws_pollfd *, unsigned long, int);
// Socket hand-off between processes (cluster round-robin). The descriptor is
// winsock2.h's WSAPROTOCOL_INFOW (628 bytes), opaque here.
typedef struct { char opaque[628]; } ws_PROTOCOL_INFOW;
static int (WINAPI *p_WSADuplicateSocketW)(ws_SOCKET, unsigned long, ws_PROTOCOL_INFOW *);
static ws_SOCKET (WINAPI *p_WSASocketW)(int, int, int, ws_PROTOCOL_INFOW *, unsigned int, unsigned long);
static int (WINAPI *p_getaddrinfo)(const char *, const char *, const ws_addrinfo *, ws_addrinfo **);
static void (WINAPI *p_freeaddrinfo)(ws_addrinfo *);
static int (WINAPI *p_getnameinfo)(const void *, int, char *, DWORD, char *, DWORD, int);
static int (WINAPI *p_inet_pton)(int, const char *, void *);
static const char *(WINAPI *p_inet_ntop)(int, const void *, char *, size_t);
static int (WINAPI *p_gethostname)(char *, int);
static unsigned short (WINAPI *p_htons)(unsigned short);
static unsigned short (WINAPI *p_ntohs)(unsigned short);

void ws_init(void) {
	static int done;
	if (done) return;
	done = 1;
	HMODULE m = LoadLibraryA("ws2_32.dll");
	if (!m) return;
#define B(n) *(FARPROC *)&p_##n = GetProcAddress(m, #n)
	B(WSAStartup); B(WSAGetLastError); B(socket); B(bind); B(listen); B(accept);
	B(connect); B(recv); B(send); B(recvfrom); B(sendto); B(closesocket);
	B(shutdown); B(setsockopt); B(getsockopt); B(getsockname); B(getpeername);
	B(ioctlsocket); B(WSAIoctl); B(WSAPoll); B(WSADuplicateSocketW); B(WSASocketW); B(getaddrinfo); B(freeaddrinfo); B(getnameinfo); B(inet_pton);
	B(inet_ntop); B(gethostname); B(htons); B(ntohs);
#undef B
	ws_WSADATA d;
	if (p_WSAStartup) p_WSAStartup(0x0202, &d);
}
__attribute__((constructor)) static void kml_win_io_init(void) { ws_init(); }

// ---- owned fd descriptor table (TDD-00182 Stage 1) -------------------------
// An fd is an index into a table of pointers to refcounted *descriptions*
// (TDD-00183 Stage 3) — the POSIX open file description: the OS handle, its
// `kind` tag (which read/write/close/fstat all dispatch on) and its flags.
// dup2() makes two fds share one description (refs++), close() drops a
// reference, and the handle is torn down exactly once, when the last fd
// referring to it closes. The non-blocking flag belongs to the description,
// so it is seen through every alias.
//
// Two fd ranges:
//   [0, 8192)     CRT fds: regular files and the inherited stdio — the CRT owns
//                 the handle (_get_osfhandle). A plain CRT fd has no pooled
//                 description; its few flags live in a per-fd record. A slot
//                 here can also *hold* a description: a std fd classified as a
//                 pipe or the console, or the target of a dup2().
//   [8192, 16384) the owned pool: every handle this layer owns — sockets and
//                 pipe ends — allocates from it, first free slot. The CRT never
//                 sees these handles (a CRT fd wrapped around a socket would
//                 CloseHandle it behind Winsock's back on _close). The CRT's
//                 low-level I/O stops at 8192.
enum { KFD_PLAIN = 0, KFD_SOCKET = 1, KFD_PIPE = 2, KFD_CONSOLE = 4 };
#define KFD_POOL_BASE 8192

typedef struct kfd_desc {
	ws_SOCKET sock;          // a socket's Winsock SOCKET; else 0
	HANDLE h;                // an owned pipe end's handle (NULL when the CRT holds it — see crt_fd)
	int refs;                // fds referring to this description (pooled descriptions only)
	int crt_fd;              // >= 0: a classified std fd — the CRT owns the handle and does its I/O; else -1
	struct kfd_desc *next_free; // pool free list
	unsigned char kind;      // KFD_PLAIN / KFD_SOCKET / KFD_PIPE / KFD_CONSOLE
	unsigned char nonblock;  // O_NONBLOCK: a socket's FIONBIO, emulated for pipes and the console
	// Set once a socket has reported a connection reset: Linux returns
	// ECONNRESET from one recv and end-of-file from the next, and the stream
	// code's close-on-EOF path depends on that; Winsock keeps returning the error.
	unsigned char reset;
	// Set once a socket has been handed to a child process. The child's
	// inherited handle refers to the same socket object, and closesocket
	// tears that object down for everyone — so the parent releases only its
	// own handle (CloseHandle) when it closes such an fd.
	unsigned char inherited;
	unsigned char classified;// std/CRT fd's kind probed (GetFileType) once
	unsigned char sock_stream; // KFD_SOCKET: 1 = SOCK_STREAM
	unsigned char listening;   // KFD_SOCKET: listen() succeeded
	int family;              // Winsock address family (AF_INET 2 / AF_INET6 23 / …)
	// ---- cluster round-robin (a listener shared with re-spawned workers) ----
	struct kfd_rr *rr;       // primary: the workers this listener's connections rotate over
	int rr_chan;             // worker: the fd connections arrive on from the primary (0 = none)
} kfd_desc;
// The fd table. kfd_tab[fd] points at the pooled description an fd refers to;
// NULL means the fd is a plain CRT fd (or closed), whose flags live in
// kfd_plain[fd]. Pooled descriptions are recycled through a free list.
static kfd_desc *kfd_tab[KFD_MAX];
static kfd_desc kfd_plain[KFD_MAX];
static kfd_desc kfd_pool[KFD_MAX];
static kfd_desc *kfd_free_list;
static int kfd_pool_used;      // high-water mark into kfd_pool
// Slot and pool allocation is the one table mutation worker threads race on
// (each opens its own sockets); I/O on an fd stays with its owner.
static SRWLOCK kfd_lock = SRWLOCK_INIT;
#define KD(fd) (kfd_tab[fd] ? kfd_tab[fd] : &kfd_plain[fd])

// Cross-file accessors (win32proc.c, win32fs.c). Functions rather than exported
// arrays so the description layout can change without other objects
// rebuilding against a fixed struct. kfd_kind_of returns -1 for an
// out-of-range fd (never a valid KFD_* value).
int kfd_kind_of(int fd) { return (fd < 0 || fd >= KFD_MAX) ? -1 : KD(fd)->kind; }
void kfd_set_inherited(int fd) { if (fd >= 0 && fd < KFD_MAX) KD(fd)->inherited = 1; }

// Take a description from the pool. Caller holds kfd_lock.
static kfd_desc *kfd_desc_new(int kind) {
	kfd_desc *d = kfd_free_list;
	if (d) kfd_free_list = d->next_free;
	else if (kfd_pool_used < KFD_MAX) d = &kfd_pool[kfd_pool_used++];
	else return NULL;
	memset(d, 0, sizeof *d);
	d->kind = (unsigned char)kind;
	d->refs = 1;
	d->crt_fd = -1;
	return d;
}
// Retire a description whose last fd closed.
static void kfd_desc_retire(kfd_desc *d) {
	AcquireSRWLockExclusive(&kfd_lock);
	d->kind = KFD_PLAIN;
	d->next_free = kfd_free_list;
	kfd_free_list = d;
	ReleaseSRWLockExclusive(&kfd_lock);
}

// Bind a fresh description of `kind` to a slot: exactly `want` when want >= 0
// (-1 if it is taken or outside the pool), else the first free pool slot
// (EMFILE when the pool is full).
static int kfd_slot_new(int kind, int want) {
	int fd = -1;
	AcquireSRWLockExclusive(&kfd_lock);
	if (want >= 0) {
		if (want >= KFD_POOL_BASE && want < KFD_MAX && !kfd_tab[want]) fd = want;
	} else {
		for (int i = KFD_POOL_BASE; i < KFD_MAX; i++) if (!kfd_tab[i]) { fd = i; break; }
	}
	kfd_desc *d = fd >= 0 ? kfd_desc_new(kind) : NULL;
	if (d) kfd_tab[fd] = d;
	ReleaseSRWLockExclusive(&kfd_lock);
	if (!d) { errno = want >= 0 ? L_EINVAL : L_EMFILE; return -1; }
	return fd;
}

// The socket may arrive already shaped — a listener inherited from a cluster
// primary — so its type is read off the socket itself rather than from the
// socket()/bind()/listen() calls this layer never saw.
static void kfd_sock_shape(int fd, HANDLE s) {
	KD(fd)->sock = (ws_SOCKET)s;
	if (p_getsockopt) {
		int v = 0, l = sizeof v;
		if (p_getsockopt((ws_SOCKET)s, WS_SOL_SOCKET, 0x1008 /* SO_TYPE */, (char *)&v, &l) == 0) KD(fd)->sock_stream = (v == 1);
		v = 0; l = sizeof v;
		if (p_getsockopt((ws_SOCKET)s, WS_SOL_SOCKET, 0x0002 /* SO_ACCEPTCONN */, (char *)&v, &l) == 0) KD(fd)->listening = (v != 0);
	}
	if (p_getsockname) {
		struct { unsigned short fam; char rest[126]; } sa = {0};
		int l = sizeof sa;
		if (p_getsockname((ws_SOCKET)s, &sa, &l) == 0) KD(fd)->family = sa.fam;
	}
}

// kfd_adopt_socket places socket handle s at exactly fd (used for a socket
// inherited from a parent under an agreed fd number); -1 if the slot is
// outside the pool or taken.
int kfd_adopt_socket(HANDLE s, int fd) {
	if (fd < 0 || kfd_slot_new(KFD_SOCKET, fd) < 0) { errno = L_EINVAL; return -1; }
	kfd_sock_shape(fd, s);
	return fd;
}

// kfd_register gives an owned handle (a socket or a pipe end) the first free
// pool fd.
int kfd_register(HANDLE h, int kind) {
	int fd = kfd_slot_new(kind, -1);
	if (fd < 0) return -1;
	if (kind == KFD_SOCKET) kfd_sock_shape(fd, h);
	else KD(fd)->h = h;
	return fd;
}
static inline int kfd_is(int fd, int kind) {
	return fd >= 0 && fd < KFD_MAX && KD(fd)->kind == kind;
}
// kfd_handle: the OS handle behind any fd (win32proc.c duplicates it for a
// child's stdio or an inherited IPC socket).
static HANDLE kfd_desc_handle(kfd_desc *d) {
	if (d->kind == KFD_SOCKET) return (HANDLE)d->sock;
	if (d->h) return d->h;
	return d->crt_fd >= 0 ? (HANDLE)_get_osfhandle(d->crt_fd) : INVALID_HANDLE_VALUE;
}
HANDLE kfd_handle(int fd) {
	if (fd < 0 || fd >= KFD_MAX) return INVALID_HANDLE_VALUE;
	if (kfd_tab[fd]) return kfd_desc_handle(kfd_tab[fd]);
	return (HANDLE)_get_osfhandle(fd);
}
static inline ws_SOCKET kfd_sock(int fd) { return (ws_SOCKET)kfd_handle(fd); }
// The Winsock SOCKET behind fd, for code that must hand a real socket to a
// library speaking Winsock itself (OpenSSL's socket BIO in tls.c).
intptr_t __kml_win_fd_socket(int fd) {
	if (fd < 0 || fd >= KFD_MAX) return -1;
	return (intptr_t)kfd_sock(fd);
}

// ---- waking a thread's loop -----------------------------------------------------
// A source with no handle libuv could watch — a console signal, a resize,
// a signal a program sends itself (win32proc.c) — wakes the event loops
// through the function each registered (its uv_async_send, loop.c). Every
// loop is woken: only the delivering thread finds the signal, the others
// take one empty pass. A wake before any loop registered is kept and
// delivered by the first registration.
#define KML_WAKE_MAX 64
static struct { DWORD tid; void (*fn)(void *); void *arg; } kml_wakes[KML_WAKE_MAX];
static int kml_wake_pending;
static SRWLOCK kml_wakes_lock = SRWLOCK_INIT;

// The calling thread's loop: wake it with fn(arg) from now on (any thread).
void __kml_win_set_loop_wake(void (*fn)(void *), void *arg) {
	DWORD tid = GetCurrentThreadId();
	AcquireSRWLockExclusive(&kml_wakes_lock);
	int i, free_at = -1;
	for (i = 0; i < KML_WAKE_MAX; i++) {
		if (kml_wakes[i].tid == tid) break;
		if (!kml_wakes[i].tid && free_at < 0) free_at = i;
	}
	if (i == KML_WAKE_MAX) i = free_at;
	if (i >= 0) {
		kml_wakes[i].tid = tid;
		kml_wakes[i].fn = fn;
		kml_wakes[i].arg = arg;
	}
	int pending = kml_wake_pending;
	kml_wake_pending = 0;
	ReleaseSRWLockExclusive(&kml_wakes_lock);
	if (pending) fn(arg);
}

// Before the thread's loop closes (a Worker ending).
void __kml_win_clear_loop_wake(void) {
	DWORD tid = GetCurrentThreadId();
	AcquireSRWLockExclusive(&kml_wakes_lock);
	for (int i = 0; i < KML_WAKE_MAX; i++)
		if (kml_wakes[i].tid == tid) kml_wakes[i].tid = 0, kml_wakes[i].fn = NULL;
	ReleaseSRWLockExclusive(&kml_wakes_lock);
}

// __kml_win_loop_wake: a signal was raised (win32proc.c).
void __kml_win_loop_wake(void) {
	AcquireSRWLockShared(&kml_wakes_lock);
	int any = 0;
	for (int i = 0; i < KML_WAKE_MAX; i++) {
		if (!kml_wakes[i].fn) continue;
		kml_wakes[i].fn(kml_wakes[i].arg);
		any = 1;
	}
	ReleaseSRWLockShared(&kml_wakes_lock);
	if (!any) {
		AcquireSRWLockExclusive(&kml_wakes_lock);
		kml_wake_pending = 1;
		ReleaseSRWLockExclusive(&kml_wakes_lock);
	}
}

// ---- high-resolution sleep (TDD-00183 Stage 4) -----------------------------------
// usleep()/nanosleep() on a high-resolution waitable timer: Sleep's
// millisecond argument inherits the system timer resolution (~15.6 ms by
// default). Pre-1803 Windows without the high-resolution flag falls back to
// a plain timer, and a machine without the API at all to Sleep.
#ifndef CREATE_WAITABLE_TIMER_HIGH_RESOLUTION
#define CREATE_WAITABLE_TIMER_HIGH_RESOLUTION 0x00000002
#endif
#ifndef TIMER_ALL_ACCESS
#define TIMER_ALL_ACCESS 0x1F0003
#endif
static _Thread_local HANDLE kml_hrtimer;
static _Thread_local int kml_hrtimer_tried;
static HANDLE kml_hrtimer_get(void) {
	if (kml_hrtimer || kml_hrtimer_tried) return kml_hrtimer;
	kml_hrtimer_tried = 1;
	HMODULE k = GetModuleHandleW(L"kernel32.dll");
	HANDLE (WINAPI *create)(void *, const wchar_t *, unsigned long, unsigned long) =
		k ? (HANDLE (WINAPI *)(void *, const wchar_t *, unsigned long, unsigned long))(void *)GetProcAddress(k, "CreateWaitableTimerExW") : NULL;
	if (create) {
		kml_hrtimer = create(NULL, NULL, CREATE_WAITABLE_TIMER_HIGH_RESOLUTION, TIMER_ALL_ACCESS);
		if (!kml_hrtimer) kml_hrtimer = create(NULL, NULL, 0, TIMER_ALL_ACCESS);
	}
	return kml_hrtimer;
}

// A precise sleep on the calling thread, shared by usleep()/nanosleep().
void __kml_win_hr_sleep_us(int64_t us) {
	if (us <= 0) return;
	HANDLE t = kml_hrtimer_get();
	if (t) {
		LARGE_INTEGER due;
		due.QuadPart = -(us * 10);
		if (SetWaitableTimer(t, &due, 0, NULL, NULL, 0)) {
			WaitForSingleObject(t, INFINITE);
			return;
		}
	}
	Sleep((DWORD)((us + 999) / 1000));
}

// ---- console input ----------------------------------------------------------
// A blocking read of the console (fs.readSync(0), a prompt's line) moves off
// the CRT onto a reader thread doing ReadConsoleW — cooked mode keeps the
// console's own line editing, raw mode (setRawMode cleared
// ENABLE_LINE_INPUT) returns per-keystroke, and VT input sequences arrive
// in-band — decoded UTF-16 → UTF-8 into a queue read() drains, so non-ASCII
// input is not decoded through the code page (TDD-00180 §2). The event loop
// reads a console through libuv's uv_tty_t instead (klainpool.c).
static struct {
	CRITICAL_SECTION cs;
	HANDLE thr;
	HANDLE data;             // auto-reset: bytes appended (blocking reads wait here)
	HANDLE consumed;         // auto-reset: queue drained, read the next chunk
	char buf[32768];
	int len;
	int eof;                 // Ctrl+Z at line start, CRT-compatible
	int started;
} kcon;

static DWORD WINAPI kcon_thread(LPVOID arg) {
	(void)arg;
	HANDLE h = GetStdHandle(STD_INPUT_HANDLE);
	wchar_t wbuf[4096];
	char u8[16384];
	for (;;) {
		DWORD got = 0;
		if (!ReadConsoleW(h, wbuf, 4096, &got, NULL)) break;
		if (got == 0) continue;
		if (wbuf[0] == 0x1a) break; // Ctrl+Z at buffer start: EOF, as the CRT treats it
		int n = WideCharToMultiByte(CP_UTF8, 0, wbuf, (int)got, u8, sizeof u8, NULL, NULL);
		if (n <= 0) continue;
		EnterCriticalSection(&kcon.cs);
		int room = (int)sizeof kcon.buf - kcon.len;
		if (n > room) n = room; // over-full queue: drop the tail, as a tty would
		memcpy(kcon.buf + kcon.len, u8, (size_t)n);
		kcon.len += n;
		LeaveCriticalSection(&kcon.cs);
		SetEvent(kcon.data);
		WaitForSingleObject(kcon.consumed, INFINITE);
	}
	EnterCriticalSection(&kcon.cs);
	kcon.eof = 1;
	LeaveCriticalSection(&kcon.cs);
	SetEvent(kcon.data);
	return 0;
}

static void kcon_ensure(void) {
	if (kcon.started) return;
	kcon.started = 1;
	InitializeCriticalSection(&kcon.cs);
	kcon.data = CreateEventW(NULL, FALSE, FALSE, NULL);
	kcon.consumed = CreateEventW(NULL, FALSE, FALSE, NULL);
	kcon.thr = CreateThread(NULL, 0, kcon_thread, NULL, 0, NULL);
}

static int kcon_readable(void) {
	kcon_ensure();
	EnterCriticalSection(&kcon.cs);
	int r = kcon.len > 0 || kcon.eof;
	LeaveCriticalSection(&kcon.cs);
	return r;
}

// Consume up to n decoded bytes; 0 = EOF, -1/EAGAIN = nothing yet.
static int64_t kcon_read(void *buf, size_t n, int nonblock) {
	kcon_ensure();
	for (;;) {
		EnterCriticalSection(&kcon.cs);
		if (kcon.len > 0) {
			int take = (int)(n < (size_t)kcon.len ? n : (size_t)kcon.len);
			memcpy(buf, kcon.buf, (size_t)take);
			memmove(kcon.buf, kcon.buf + take, (size_t)(kcon.len - take));
			kcon.len -= take;
			int drained = kcon.len == 0;
			LeaveCriticalSection(&kcon.cs);
			if (drained) SetEvent(kcon.consumed);
			return take;
		}
		int eof = kcon.eof;
		LeaveCriticalSection(&kcon.cs);
		if (eof) return 0;
		if (nonblock) { errno = L_EAGAIN; return -1; }
		WaitForSingleObject(kcon.data, INFINITE);
	}
}

// ---- std-fd classification --------------------------------------------------
// fds 0-2 arrive from the parent as bare CRT fds. Probed once, lazily, at the
// first read() touch: a console fd 0 becomes KFD_CONSOLE (the reader-thread
// ReadConsoleW path). The fd is promoted to a pooled description that leaves
// the handle with the CRT (crt_fd): the CRT keeps doing the fd's plain
// writes and closes it.
static int kfd_promote_crt(int fd, int kind) {
	AcquireSRWLockExclusive(&kfd_lock);
	kfd_desc *d = kfd_tab[fd] ? NULL : kfd_desc_new(kind);
	if (d) {
		d->crt_fd = fd;
		d->nonblock = kfd_plain[fd].nonblock;
		d->classified = 1;
		kfd_tab[fd] = d;
	}
	ReleaseSRWLockExclusive(&kfd_lock);
	return d ? 0 : -1;
}
static void kfd_classify(int fd) {
	if (fd < 0 || fd >= KFD_MAX || KD(fd)->classified || KD(fd)->kind != KFD_PLAIN) return;
	KD(fd)->classified = 1;
	HANDLE h = kfd_handle(fd);
	if (h == INVALID_HANDLE_VALUE) return;
	DWORD mode;
	if (fd == 0 && GetFileType(h) == FILE_TYPE_CHAR && GetConsoleMode(h, &mode)) kfd_promote_crt(fd, KFD_CONSOLE);
}

// Node's guessHandleType for fd (klainpool.c): 0 unknown, 1 file, 2 pipe,
// 3 tty, 4 tcp.
int __kml_win_fd_kind(int fd) {
	if (kfd_is(fd, KFD_SOCKET)) return 4;
	if (kfd_is(fd, KFD_CONSOLE)) return 3;
	if (kfd_is(fd, KFD_PIPE)) return 2;
	HANDLE h = kfd_handle(fd);
	if (h == INVALID_HANDLE_VALUE) return 0;
	DWORD mode;
	switch (GetFileType(h)) {
	case FILE_TYPE_DISK: return 1;
	case FILE_TYPE_PIPE: return 2;
	case FILE_TYPE_CHAR: return GetConsoleMode(h, &mode) ? 3 : 1;
	default: return 0;
	}
}
// A console's raw mode (uv_tty_set_mode): line input, echo and processed
// input off, VT input on, so keys arrive as a POSIX terminal sends them; the
// original mode back when raw is 0. 0, or -errno.
static DWORD kml_tty_saved_in;
static int kml_tty_saved = 0;
int __kml_win_tty_set_raw(int fd, int raw) {
	HANDLE h = kfd_handle(fd);
	DWORD in;
	if (h == INVALID_HANDLE_VALUE || !GetConsoleMode(h, &in)) return -L_EBADF;
	if (!kml_tty_saved) {
		kml_tty_saved_in = in;
		kml_tty_saved = 1;
	}
	DWORD m = kml_tty_saved_in;
	if (raw) m = (m & ~(ENABLE_LINE_INPUT | ENABLE_ECHO_INPUT | ENABLE_PROCESSED_INPUT)) | ENABLE_VIRTUAL_TERMINAL_INPUT;
	return SetConsoleMode(h, m) ? 0 : -L_EINVAL;
}

// A console's window size: 0, or -1 when fd is not a console.
int __kml_win_tty_size(int fd, int *cols, int *rows) {
	CONSOLE_SCREEN_BUFFER_INFO i;
	if (!GetConsoleScreenBufferInfo(kfd_handle(fd), &i)) return -1;
	*cols = i.srWindow.Right - i.srWindow.Left + 1;
	*rows = i.srWindow.Bottom - i.srWindow.Top + 1;
	return 0;
}

static int map_wsa_errno(int e) {
	switch (e) {
	// Mirrors libuv's uv_translate_sys_error, mapped to Linux errno numbers
	// (the ABI the IR and the errno→code table use). An unmapped WSA error
	// used to fall to EINVAL, so EHOSTUNREACH/ENETUNREACH/EACCES/… all
	// surfaced as "invalid argument"; each now carries its true code
	// (ADR-00742).
	case WSAEWOULDBLOCK: return L_EAGAIN;
	case WSAEINPROGRESS: return L_EINPROGRESS;
	case WSAEALREADY: return L_EALREADY;
	case WSAECONNRESET: return L_ECONNRESET;
	case WSAECONNABORTED: return L_ECONNABORTED;
	case WSAECONNREFUSED: return L_ECONNREFUSED;
	case WSAEADDRINUSE: return L_EADDRINUSE;
	case WSAEADDRNOTAVAIL: return L_EADDRNOTAVAIL;
	case WSAETIMEDOUT: return L_ETIMEDOUT;
	case WSAENOTCONN: return L_ENOTCONN;
	case WSAESHUTDOWN: return L_ESHUTDOWN;
	case WSAENOTSOCK: return L_ENOTSOCK;
	case WSAEINTR: return L_EINTR;
	case WSAEACCES: return L_EACCES;
	case WSAEFAULT: return L_EFAULT;
	case WSAEMFILE: return L_EMFILE;
	case WSAEMSGSIZE: return L_EMSGSIZE;
	case WSAENOBUFS: return L_ENOBUFS;
	case WSAEISCONN: return L_EISCONN;
	case WSAEDESTADDRREQ: return L_EDESTADDRREQ;
	case WSAEHOSTUNREACH: return L_EHOSTUNREACH;
	case WSAEHOSTDOWN: return L_EHOSTDOWN;
	case WSAENETUNREACH: return L_ENETUNREACH;
	case WSAENETDOWN: return L_ENETDOWN;
	case WSAENETRESET: return L_ENETRESET;
	case WSAEPROTOTYPE: return L_EPROTOTYPE;
	case WSAENOPROTOOPT: return L_ENOPROTOOPT;
	case WSAEPROTONOSUPPORT: return L_EPROTONOSUPPORT;
	case WSAEOPNOTSUPP: return L_EOPNOTSUPP;
	case WSAEAFNOSUPPORT: return L_EAFNOSUPPORT;
	case WSAENAMETOOLONG: return L_ENAMETOOLONG;
	case WSAELOOP: return L_ELOOP;
	case WSAENOTEMPTY: return L_ENOTEMPTY;
	case WSAEINVAL: return L_EINVAL;
	default: return L_EINVAL;
	}
}
static int set_wsa_errno(void) {
	errno = map_wsa_errno(p_WSAGetLastError ? p_WSAGetLastError() : 0);
	return -1;
}

// ---- sockets -------------------------------------------------------------------
// Linux AF_INET6 is 10; Winsock's is 23. AF_INET/AF_UNIX match.
static int map_af(int af) { return af == 10 ? 23 : af; }

int socket(int domain, int type, int protocol) {
	ws_init();
	domain = map_af(domain);
	if (!p_socket) { errno = L_ENOSYS; return -1; }
	ws_SOCKET s = p_socket(domain, type, protocol);
	if (s == WS_INVALID) return set_wsa_errno();
	// Windows defaults an IPv6 socket to IPV6_V6ONLY=1, so a `::` listener
	// refuses IPv4 clients; libuv/Node clear it for dual-stack by default
	// (ADR-00742). A program that wants v6-only can still set it via
	// setsockopt afterwards. IPPROTO_IPV6=41, IPV6_V6ONLY=27 (ws2ipdef.h).
	if (domain == 23 && p_setsockopt) {
		DWORD v6only = 0;
		p_setsockopt(s, 41, 27, (const char *)&v6only, sizeof v6only);
	}
	// A UDP socket that sent to a port nobody listens on gets the ICMP "port
	// unreachable" back as WSAECONNRESET on its *next receive* — a Windows-only
	// behaviour that would surface as a bogus read error on a server replying to
	// a client that already went away. libuv turns it off for every UDP socket
	// (SIO_UDP_CONNRESET = FALSE), so Node never reports it; do the same.
	if (type == 2 /* SOCK_DGRAM */ && p_WSAIoctl) {
		BOOL report = FALSE;
		unsigned long got = 0;
		p_WSAIoctl(s, 0x9800000CUL /* SIO_UDP_CONNRESET */, &report, sizeof report, NULL, 0, &got, NULL, NULL);
	}
	// Sockets must not be inherited by child processes (Node's CLOEXEC
	// discipline) so a spawned child never holds a listener open.
	SetHandleInformation((HANDLE)s, HANDLE_FLAG_INHERIT, 0);
	int fd = kfd_register((HANDLE)s, KFD_SOCKET);
	if (fd < 0) { p_closesocket(s); return fd; }
	KD(fd)->sock_stream = (type == 1); // SOCK_STREAM
	KD(fd)->family = domain;
	return fd;
}

int bind(int fd, const void *addr, int len) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	if (p_bind(kfd_sock(fd), addr, len) != 0) return set_wsa_errno();
	return 0;
}
int listen(int fd, int backlog) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	if (p_listen(kfd_sock(fd), backlog) != 0) return set_wsa_errno();
	KD(fd)->listening = 1;
	return 0;
}

// ---- cluster round-robin ------------------------------------------------------
// Workers of an http.listen({ workers }) cluster are re-spawned processes that
// inherit the listener. Letting each of them accept on it does not spread the
// load: the kernel completes pending accepts most-recent-first, so whichever
// process served the last connection re-arms and takes the next one as well.
// This is why libuv/Node do not share the listener on Windows either: the
// primary accepts every connection and deals them out in turn. Here the turn
// includes the primary itself (it is one of the N serving processes, as under
// fork), and a connection travels to a worker as the WSAPROTOCOL_INFOW that
// WSADuplicateSocketW makes for that worker's pid, over a socket pair the worker
// inherited. In the worker the pair's read end stands in for the listener's
// read-interest and accept() turns each descriptor back into a socket.
#define KFD_RR_MAX 64
typedef struct kfd_rr {
	int n, next; // next: 0 = this process, i = w[i-1]
	struct { unsigned long pid; int chan; } w[KFD_RR_MAX];
} kfd_rr;

// Primary: the live listening sockets, for win32proc.c to hand to a worker.
int kfd_listeners(int *out, int max) {
	int n = 0;
	for (int fd = KFD_POOL_BASE; fd < KFD_MAX && n < max; fd++)
		if (kfd_tab[fd] && kfd_tab[fd]->kind == KFD_SOCKET && kfd_tab[fd]->listening) out[n++] = fd;
	return n;
}
// Primary: worker `pid` was spawned holding the other end of chan for lfd.
void kfd_rr_add_worker(int lfd, unsigned long pid, int chan) {
	if (!kfd_is(lfd, KFD_SOCKET)) return;
	kfd_desc *d = KD(lfd);
	if (!d->rr) d->rr = (kfd_rr *)calloc(1, sizeof(kfd_rr)); /* scratch */
	if (!d->rr || d->rr->n >= KFD_RR_MAX) { close(chan); return; }
	unsigned long nb = 1;
	if (p_ioctlsocket) p_ioctlsocket(kfd_sock(chan), (long)WS_FIONBIO, &nb); // a wedged worker must not block the primary
	d->rr->w[d->rr->n].pid = pid;
	d->rr->w[d->rr->n].chan = chan;
	d->rr->n++;
}
// Worker: connections for the inherited listener lfd arrive on chan. nonblock
// is the listener's O_NONBLOCK in the primary: a file-description flag a forked
// child would share, which this process's own table has to be told.
int kfd_nonblock_of(int fd) { return fd >= 0 && fd < KFD_MAX && KD(fd)->nonblock; }
void kfd_rr_adopt(int lfd, int chan, int nonblock) {
	if (!kfd_is(lfd, KFD_SOCKET) || !kfd_is(chan, KFD_SOCKET)) return;
	KD(lfd)->nonblock = nonblock != 0;
	unsigned long nb = 1;
	if (p_ioctlsocket) p_ioctlsocket(kfd_sock(chan), (long)WS_FIONBIO, &nb);
	KD(chan)->nonblock = 1;
	KD(lfd)->rr_chan = chan;
}
// Worker: an inherited listener bound to `port` that no bind has claimed yet
// (port 0: the first unclaimed one), or -1. The re-run program binds the same
// servers the primary did; each bind takes its listener from here instead.
static unsigned char kfd_rr_claimed[KFD_MAX];
int __kml_win_inherited_listener(int port) {
	for (int fd = KFD_POOL_BASE; fd < KFD_MAX; fd++) {
		if (!kfd_tab[fd] || kfd_tab[fd]->kind != KFD_SOCKET || !kfd_tab[fd]->rr_chan || kfd_rr_claimed[fd]) continue;
		struct { uint16_t fam, port; char rest[28]; } sa = {0};
		int l = sizeof sa;
		if (port != 0 && (p_getsockname(kfd_sock(fd), &sa, &l) != 0 || htons(sa.port) != (unsigned short)port)) continue;
		kfd_rr_claimed[fd] = 1;
		return fd;
	}
	return -1;
}
// The fd whose readiness answers read-interest in fd.
static inline int kfd_rd_src(int fd) { return KD(fd)->rr_chan ? KD(fd)->rr_chan : fd; }
// The SOCKET libuv polls for fd's readiness (TDD-00252): a cluster worker's
// listener is told of connections by its channel from the primary.
intptr_t __kml_win_poll_socket(int fd) {
	if (fd < 0 || fd >= KFD_MAX) return -1;
	return (intptr_t)kfd_sock(kfd_rd_src(fd));
}

// Primary: give connection s to whoever's turn it is. 1 = a worker has it (s is
// closed here when close_it), 0 = it is this process's. A worker whose channel
// is gone leaves the rotation; one whose channel is full is skipped for this
// connection.
static int kfd_rr_dispatch(kfd_desc *d, ws_SOCKET s, int close_it) {
	kfd_rr *rr = d->rr;
	if (!p_WSADuplicateSocketW) return 0;
	for (int tries = 0; tries <= rr->n; tries++) {
		int slot = rr->next;
		rr->next = (rr->next + 1) % (rr->n + 1);
		if (slot == 0) return 0;
		int chan = rr->w[slot - 1].chan;
		if (!chan) continue;
		ws_PROTOCOL_INFOW info;
		int dead = p_WSADuplicateSocketW(s, rr->w[slot - 1].pid, &info) != 0;
		if (!dead) {
			ws_SOCKET cs = kfd_sock(chan);
			int n = p_send(cs, (const char *)&info, (int)sizeof info, 0);
			if (n < 0 && p_WSAGetLastError() == 10035 /* WSAEWOULDBLOCK */) continue; // backed up: not its turn
			if (n > 0 && n < (int)sizeof info) {
				// A descriptor must never be left half-sent: finish it blocking.
				unsigned long nb = 0;
				p_ioctlsocket(cs, (long)WS_FIONBIO, &nb);
				while (n > 0 && n < (int)sizeof info) {
					int m = p_send(cs, (const char *)&info + n, (int)sizeof info - n, 0);
					if (m <= 0) { n = -1; break; }
					n += m;
				}
				nb = 1;
				p_ioctlsocket(cs, (long)WS_FIONBIO, &nb);
			}
			if (n == (int)sizeof info) {
				if (close_it) p_closesocket(s);
				return 1;
			}
			dead = 1;
		}
		if (dead) {
			rr->w[slot - 1].chan = 0;
			close(chan);
		}
	}
	return 0;
}
// Primary: a connection libuv accepted on listener lfd (klainpool.c), dealt
// round-robin as accept() deals its own. 1 = a worker has it; the caller
// closes its handle.
int __kml_win_rr_deal(int lfd, intptr_t s) {
	if (!kfd_is(lfd, KFD_SOCKET) || !KD(lfd)->rr) return 0;
	return kfd_rr_dispatch(KD(lfd), (ws_SOCKET)s, 0);
}

// The errno (Linux numbering) for a Winsock error code, as the socket calls
// here report it: for a library that hands back the raw code (libcurl's
// CURLINFO_OS_ERRNO).
int __kml_win_wsa_errno(int wsa) { return map_wsa_errno(wsa); }

// Worker: the next connection the primary sent. 1 = *out is it, 0 = none yet,
// -1 = the primary is gone (the caller falls back to accepting for itself).
static int kfd_rr_recv(int fd, ws_SOCKET *out) {
	int chan = KD(fd)->rr_chan;
	ws_SOCKET cs = kfd_sock(chan);
	ws_PROTOCOL_INFOW info;
	for (;;) {
		int n = p_recv(cs, (char *)&info, (int)sizeof info, 2 /* MSG_PEEK */);
		if (n < 0 && p_WSAGetLastError() == 10035 /* WSAEWOULDBLOCK */) return 0;
		if (n <= 0) break;
		if (n < (int)sizeof info) return 0; // the rest is in flight; libuv's poll fires again
		p_recv(cs, (char *)&info, (int)sizeof info, 0);
		ws_SOCKET s = p_WSASocketW ? p_WSASocketW(-1, -1, -1, &info, 0, 0x01 /* WSA_FLAG_OVERLAPPED */) : WS_INVALID;
		if (s == WS_INVALID) continue; // the client is already gone: take the next one
		*out = s;
		return 1;
	}
	KD(fd)->rr_chan = 0;
	close(chan);
	return -1;
}

// accept(): a cluster worker takes the connections its primary deals it; a
// primary deals each connection it accepts to whoever's turn it is.
int accept(int fd, void *addr, int *len) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	ws_SOCKET s = WS_INVALID;
	for (;;) {
		if (KD(fd)->rr_chan) {
			int got = kfd_rr_recv(fd, &s);
			if (got == 0) { errno = L_EAGAIN; return -1; }
			if (got == 1) {
				if (addr && len) p_getpeername(s, addr, len);
				break;
			}
		}
		s = p_accept(kfd_sock(fd), addr, len);
		if (s == WS_INVALID) return set_wsa_errno();
		if (KD(fd)->rr && kfd_rr_dispatch(KD(fd), s, 1)) continue; // a worker's turn: take the next
		break;
	}
	SetHandleInformation((HANDLE)s, HANDLE_FLAG_INHERIT, 0);
	int nfd = kfd_register((HANDLE)s, KFD_SOCKET);
	if (nfd < 0) { p_closesocket(s); return nfd; }
	return nfd;
}

// A non-blocking connect reports WOULDBLOCK on Windows; POSIX callers expect
// EINPROGRESS, then writability (libuv's poll), then SO_ERROR.
int connect(int fd, const void *addr, int len) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	if (p_connect(kfd_sock(fd), addr, len) == 0) return 0;
	int e = p_WSAGetLastError();
	if (e == WSAEWOULDBLOCK) { errno = L_EINPROGRESS; return -1; }
	errno = map_wsa_errno(e);
	return -1;
}

int shutdown(int fd, int how) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	return p_shutdown(kfd_sock(fd), how) == 0 ? 0 : set_wsa_errno();
}
int getsockname(int fd, void *addr, int *len) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	return p_getsockname(kfd_sock(fd), addr, len) == 0 ? 0 : set_wsa_errno();
}
int getpeername(int fd, void *addr, int *len) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	return p_getpeername(kfd_sock(fd), addr, len) == 0 ? 0 : set_wsa_errno();
}

int setsockopt(int fd, int level, int opt, const void *val, int len) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	if (level == L_IPPROTO_TCP && opt == L_TCP_KEEPIDLE) {
		// Windows has no TCP_KEEPIDLE (opt 4 is TCP_MAXSEG); libuv/Node set the
		// keepalive-idle time through SIO_KEEPALIVE_VALS instead, in ms. The
		// caller passes seconds (the Linux TCP_KEEPIDLE unit); convert, and use
		// a 1 s probe interval to match libuv's default (ADR-00760).
		if (!p_WSAIoctl || !val || len < 4) { errno = L_EINVAL; return -1; }
		struct { unsigned long onoff, time_ms, interval_ms; } ka;
		ka.onoff = 1;
		ka.time_ms = (unsigned long)(*(const int *)val) * 1000UL;
		ka.interval_ms = 1000UL;
		unsigned long ret = 0;
		// SIO_KEEPALIVE_VALS = _WSAIOW(IOC_VENDOR, 4).
		if (p_WSAIoctl(kfd_sock(fd), 0x98000004UL, &ka, sizeof ka, NULL, 0, &ret, NULL, NULL) != 0)
			return set_wsa_errno();
		return 0;
	}
	if (level == L_SOL_SOCKET) {
		level = WS_SOL_SOCKET;
		switch (opt) {
		case L_SO_REUSEADDR:
			// Node sets SO_EXCLUSIVEADDRUSE on Windows: Winsock's
			// SO_REUSEADDR would let a second server hijack the port
			// (no EADDRINUSE), which is not the Linux/Node contract.
			opt = WS_SO_EXCLUSIVEADDRUSE;
			break;
		case L_SO_REUSEPORT:
			// No SO_REUSEPORT on Windows, and no substitute is safe: Winsock's
			// SO_REUSEADDR lets ANY local process bind over the port (hijack,
			// no EADDRINUSE), which is why libuv/Node never set it. Cluster
			// workers don't need it either — they receive the primary's
			// listening socket by handle inheritance and never bind
			// (httpListenInheritIR). Accept and do nothing (ADR-00737).
			return 0;
		case L_SO_KEEPALIVE: opt = WS_SO_KEEPALIVE; break;
		case L_SO_BROADCAST: opt = WS_SO_BROADCAST; break;
		// The Linux option numbers mean something else to Winsock (7 is not
		// SO_SNDBUF there), and two options differ in representation too.
		case 7: opt = 0x1001; break; // SO_SNDBUF
		case 8: opt = 0x1002; break; // SO_RCVBUF
		case 13: { // SO_LINGER: Linux {int l_onoff, l_linger} -> Winsock {u_short, u_short}
			if (!val || len < 8) { errno = L_EINVAL; return -1; }
			unsigned short wl[2] = { (unsigned short)(((const int *)val)[0] != 0), (unsigned short)((const int *)val)[1] };
			return p_setsockopt(kfd_sock(fd), level, 0x0080, (const char *)wl, sizeof wl) == 0 ? 0 : set_wsa_errno();
		}
		case 20: case 21: { // SO_RCVTIMEO / SO_SNDTIMEO: struct timeval {i64 s, i64 us} -> DWORD ms
			if (!val || len < 16) { errno = L_EINVAL; return -1; }
			int64_t ms64 = ((const int64_t *)val)[0] * 1000 + ((const int64_t *)val)[1] / 1000;
			unsigned long ms = ms64 < 0 ? 0 : ms64 > 0xffffffffLL ? 0xffffffffUL : (unsigned long)ms64;
			return p_setsockopt(kfd_sock(fd), level, opt == 20 ? 0x1006 : 0x1005, (const char *)&ms, sizeof ms) == 0 ? 0 : set_wsa_errno();
		}
		default: break;
		}
	}
	return p_setsockopt(kfd_sock(fd), level, opt, (const char *)val, len) == 0 ? 0 : set_wsa_errno();
}

// msg_flags translates the Linux MSG_* bits the C and IR callers use into
// Winsock's: PEEK/OOB/DONTROUTE share values, WAITALL does not, and
// NOSIGNAL/DONTWAIT have no Winsock bit (no SIGPIPE exists; non-blocking is a
// property of the socket here).
static int msg_flags(int f) {
	int w = f & 0x7;           // MSG_OOB 1, MSG_PEEK 2, MSG_DONTROUTE 4
	if (f & 0x100) w |= 0x8;   // MSG_WAITALL
	return w;
}
int getsockopt(int fd, int level, int opt, void *val, int *len) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	if (level == L_SOL_SOCKET) {
		level = WS_SOL_SOCKET;
		if (opt == L_SO_ERROR) opt = WS_SO_ERROR;
	}
	int r = p_getsockopt(kfd_sock(fd), level, opt, (char *)val, len);
	if (r != 0) return set_wsa_errno();
	if (level == WS_SOL_SOCKET && opt == WS_SO_ERROR && val && *len >= 4) {
		*(int *)val = *(int *)val ? map_wsa_errno(*(int *)val) : 0;
	}
	return 0;
}

int64_t recvfrom(int fd, void *buf, size_t n, int flags, void *addr, int *alen) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	int r = p_recvfrom(kfd_sock(fd), (char *)buf, (int)n, msg_flags(flags), addr, alen);
	return r == WS_ERROR ? set_wsa_errno() : r;
}
int64_t sendto(int fd, const void *buf, size_t n, int flags, const void *addr, int alen) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	int r = p_sendto(kfd_sock(fd), (const char *)buf, (int)n, msg_flags(flags), addr, alen);
	return r == WS_ERROR ? set_wsa_errno() : r;
}

unsigned short htons(unsigned short v) { return (unsigned short)((v << 8) | (v >> 8)); }
unsigned short ntohs(unsigned short v) { return htons(v); }
int inet_pton(int af, const char *src, void *dst) {
	ws_init();
	if (af == 2) {
		// Winsock accepts leading zeros in dotted quads ("01.2.3.4"); glibc
		// and Node's own net.isIPv4 reject them. Match the strict form.
		int digits = 0, octets = 0;
		for (const char *p = src; ; p++) {
			if (*p >= '0' && *p <= '9') {
				if (digits == 1 && p[-1] == '0') return 0;
				if (++digits > 3) return 0;
			} else if (*p == '.' || *p == 0) {
				if (digits == 0) return 0;
				octets++; digits = 0;
				if (*p == 0) break;
			} else return 0;
		}
		if (octets != 4) return 0;
	}
	return p_inet_pton ? p_inet_pton(map_af(af), src, dst) : -1;
}
const char *inet_ntop(int af, const void *src, char *dst, size_t size) {
	ws_init();
	return p_inet_ntop ? p_inet_ntop(map_af(af), src, dst, size) : NULL;
}
int gethostname(char *buf, size_t len) {
	ws_init();
	return p_gethostname ? p_gethostname(buf, (int)len) : -1;
}
int getaddrinfo(const char *node, const char *svc, const void *hints, void **res) {
	ws_init();
	if (!p_getaddrinfo) return -1;
	return p_getaddrinfo(node, svc, (const ws_addrinfo *)hints, (ws_addrinfo **)res);
}
void freeaddrinfo(void *ai) { if (p_freeaddrinfo) p_freeaddrinfo((ws_addrinfo *)ai); }
int getnameinfo(const void *sa, int salen, char *host, size_t hostlen, char *serv, size_t servlen, int flags) {
	ws_init();
	if (!p_getnameinfo) return -1;
	return p_getnameinfo(sa, salen, host, (DWORD)hostlen, serv, (DWORD)servlen, flags);
}
// A unit built against the Winsock headers (fetch.c) calls these through
// their import slots; supplying the slots routes it here and keeps the
// linker from pulling ws2_32's import members, which would collide with
// the definitions above.
const char *(*__imp_inet_ntop)(int, const void *, char *, size_t) = inet_ntop;
int (*__imp_getaddrinfo)(const char *, const char *, const void *, void **) = getaddrinfo;
void (*__imp_freeaddrinfo)(void *) = freeaddrinfo;
int (*__imp_inet_pton)(int, const char *, void *) = inet_pton;
int (*__imp_gethostname)(char *, size_t) = gethostname;
int (*__imp_getnameinfo)(const void *, int, char *, size_t, char *, size_t, int) = getnameinfo;

// The CRT fd that does the plain I/O for fd: the fd itself, or — for an alias of
// a classified std fd — the std fd the description was promoted from.
static int kfd_crt_fd(int fd) {
	return kfd_tab[fd] && kfd_tab[fd]->crt_fd >= 0 ? kfd_tab[fd]->crt_fd : fd;
}

// Set by dup2 while it replaces a std fd's description: the CRT's std fd stays
// open under the new one (it is shadowed, not closed).
static _Thread_local int kfd_keep_crt;

// A slot below the pool that holds a description sits over a CRT fd number.
// While it does, the CRT must not hand that number to the next _open: slots the
// CRT has nothing open at are parked on NUL, and freed again when the slot is.
static unsigned char kfd_parked[KFD_POOL_BASE];
static void kfd_park(int fd) {
	if (fd >= KFD_POOL_BASE || fd < 3) return; // the std fds are always open
	int nul = _open("NUL", 2 /* _O_RDWR */);
	if (nul < 0) return;
	if (nul != fd) { _dup2(nul, fd); _close(nul); } // _dup2 closes whatever fd held
	kfd_parked[fd] = 1;
}
static void kfd_unpark(int fd) {
	if (fd < KFD_POOL_BASE && kfd_parked[fd]) { kfd_parked[fd] = 0; _close(fd); }
}

// dup2: newfd comes to refer to oldfd's open file description. For a pooled
// description that is literal — both slots point at it, and it is torn down
// when the last of them closes. A plain CRT file is duplicated by the CRT, whose
// own fds share a file object the same way. A description placed on a std fd
// shadows the CRT's fd of that number: all I/O dispatches through this layer.
int dup2(int oldfd, int newfd) {
	if (oldfd < 0 || oldfd >= KFD_MAX || newfd < 0 || newfd >= KFD_MAX) { errno = L_EBADF; return -1; }
	kfd_classify(oldfd);
	kfd_desc *d = kfd_tab[oldfd];
	if (!d) {
		if ((HANDLE)_get_osfhandle(oldfd) == INVALID_HANDLE_VALUE) { errno = L_EBADF; return -1; }
		if (newfd == oldfd) return newfd;
		// A CRT file can only be duplicated onto a CRT fd number.
		if (newfd >= KFD_POOL_BASE) { errno = L_EBADF; return -1; }
		if (kfd_tab[newfd]) close(newfd);
		if (_dup2(oldfd, newfd) != 0) { errno = L_EBADF; return -1; }
		kfd_plain[newfd] = kfd_plain[oldfd];
		return newfd;
	}
	if (newfd == oldfd) return newfd;
	if (kfd_tab[newfd]) { kfd_keep_crt = newfd < 3; close(newfd); kfd_keep_crt = 0; }
	else if (newfd >= 3 && newfd < KFD_POOL_BASE) memset(&kfd_plain[newfd], 0, sizeof kfd_plain[newfd]);
	kfd_park(newfd); // closes a CRT file open at newfd, and holds the number
	AcquireSRWLockExclusive(&kfd_lock);
	d->refs++;
	kfd_tab[newfd] = d;
	ReleaseSRWLockExclusive(&kfd_lock);
	return newfd;
}

// ---- read / write / close / fcntl ----------------------------------------------
// A pipe's bytes now: 1 some, 0 none yet, -1 every writer gone (EOF).
static int pipe_readable(HANDLE h, DWORD *avail) {
	DWORD n = 0;
	if (!PeekNamedPipe(h, NULL, 0, NULL, &n, NULL)) return -1; // broken pipe → EOF
	if (avail) *avail = n;
	return n > 0;
}

int64_t read(int fd, void *buf, size_t n) {
	if (fd < 0 || fd >= KFD_MAX) { errno = L_EBADF; return -1; }
	if (KD(fd)->kind == KFD_SOCKET) {
		if (KD(fd)->reset) return 0; // the EOF that follows a reported reset
		int r = p_recv(kfd_sock(fd), (char *)buf, (int)n, 0);
		if (r == WS_ERROR) {
			int e = p_WSAGetLastError();
			if (e == WSAECONNRESET || e == WSAECONNABORTED) KD(fd)->reset = 1;
			errno = map_wsa_errno(e);
			return -1;
		}
		return r;
	}
	kfd_classify(fd); // std fds get their kind on first touch
	if (KD(fd)->kind == KFD_CONSOLE) return kcon_read(buf, n, KD(fd)->nonblock);
	HANDLE h = kfd_handle(fd);
	if (KD(fd)->kind == KFD_PIPE || (h != INVALID_HANDLE_VALUE && GetFileType(h) == FILE_TYPE_PIPE)) {
		// Non-blocking: what the pipe holds now, or EAGAIN.
		if (KD(fd)->nonblock) {
			DWORD avail = 0;
			int st = pipe_readable(h, &avail);
			if (st < 0) return 0; // writer gone: EOF
			if (st == 0) { errno = L_EAGAIN; return -1; }
			if (n > avail) n = avail;
		}
		DWORD got = 0;
		if (!ReadFile(h, buf, (DWORD)n, &got, NULL)) {
			DWORD e = GetLastError();
			return (e == ERROR_BROKEN_PIPE || e == ERROR_HANDLE_EOF || e == ERROR_PIPE_NOT_CONNECTED) ? 0 : (errno = L_EBADF, -1);
		}
		return got;
	}
	int r = _read(kfd_crt_fd(fd), buf, (unsigned)n);
	if (r < 0) {
		// A directory opened for reading (open's backup-semantics handle)
		// fails the read itself, which libuv reports as EISDIR.
		BY_HANDLE_FILE_INFORMATION fi;
		errno = h != INVALID_HANDLE_VALUE && GetFileInformationByHandle(h, &fi) &&
		        (fi.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) ? L_EISDIR : L_EBADF;
	}
	return r;
}

int64_t write(int fd, const void *buf, size_t n) {
	if (fd < 0 || fd >= KFD_MAX) { errno = L_EBADF; return -1; }
	if (KD(fd)->kind == KFD_SOCKET) {
		int r = p_send(kfd_sock(fd), (const char *)buf, (int)n, 0);
		return r == WS_ERROR ? set_wsa_errno() : r;
	}
	HANDLE h = kfd_handle(fd);
	if (KD(fd)->kind == KFD_PIPE || (h != INVALID_HANDLE_VALUE && GetFileType(h) == FILE_TYPE_PIPE)) {
		// An inherited pipe may be overlapped (a parent's libuv pipe): every
		// write to it needs an OVERLAPPED, so it is written overlapped and
		// waited for right here — the blocking write POSIX has. The event's
		// low bit keeps the completion off any port.
		OVERLAPPED ov;
		memset(&ov, 0, sizeof ov);
		HANDLE ev = CreateEventW(NULL, TRUE, FALSE, NULL);
		ov.hEvent = ev ? (HANDLE)((ULONG_PTR)ev | 1) : NULL;
		DWORD wr = 0, e = 0;
		if (!WriteFile(h, buf, (DWORD)n, &wr, &ov)) {
			e = GetLastError();
			if (e == ERROR_IO_PENDING) e = GetOverlappedResult(h, &ov, &wr, TRUE) ? 0 : GetLastError();
		}
		if (ev) CloseHandle(ev);
		if (e) { errno = (e == ERROR_NO_DATA || e == ERROR_BROKEN_PIPE) ? L_EPIPE : L_EBADF; return -1; }
		return wr;
	}
	int r = _write(kfd_crt_fd(fd), buf, (unsigned)n);
	if (r < 0) errno = GetLastError() == ERROR_NO_DATA ? L_EPIPE : L_EBADF;
	return r;
}

// Unbind fd from its description and return the description to the pool.
static void kfd_slot_release(int fd) {
	AcquireSRWLockExclusive(&kfd_lock);
	kfd_desc *d = kfd_tab[fd];
	kfd_tab[fd] = NULL;
	ReleaseSRWLockExclusive(&kfd_lock);
	if (d) kfd_desc_retire(d);
	kfd_unpark(fd);
}

// A connected socket's close: shutdown(SD_SEND) first so the peer sees a FIN —
// a bare closesocket on Windows can turn into a reset (the Go HTTP client
// reported "connection forcibly closed" on a Connection: close response),
// where Linux's close() sends FIN. closesocket() still aborts the connection
// with an RST (discarding the queued data) if the receive buffer holds bytes
// the peer sent that were never read — e.g. an HTTP server answering
// (431/413/redirect/clientError) while the client is still uploading a body.
// Linux's close() delivers the response in that case; to match, what the peer
// already sent is drained first. Only when there IS unread data: one
// non-blocking recv that answers EWOULDBLOCK or FIN (the common close) skips
// the drain. Bounded in bytes and in quiet time, so a peer that never stops
// cannot hang the close (past the cap the RST is accepted, as any bounded
// server must).
static void kfd_sock_drain(ws_SOCKET s) {
	unsigned long nb = 1;
	if (p_ioctlsocket) p_ioctlsocket(s, WS_FIONBIO, &nb); // never block the drain
	char drainbuf[65536];
	int r = p_recv(s, drainbuf, (int)sizeof drainbuf, 0);
	if (r <= 0) return;
	long budget = 32 * 1024 * 1024; // byte cap
	int idleWaits = 0;              // consecutive quiet waits before giving up
	for (;;) {
		if (r > 0) {
			budget -= r; idleWaits = 0;
			if (budget <= 0) return; // hostile flood: accept the RST
		} else if (r == 0) {
			return;                 // peer FIN — fully drained
		} else {
			// EWOULDBLOCK: the peer may still be mid-send (its writes unblock as
			// this drains). Wait briefly; give up once it has been quiet long
			// enough that the buffer is empty at close time.
			if ((p_WSAGetLastError ? p_WSAGetLastError() : 0) != WSAEWOULDBLOCK) return;
			ws_pollfd pf = { s, WS_POLLRDNORM, 0 };
			if (!p_WSAPoll || p_WSAPoll(&pf, 1, 20) <= 0) {
				if (++idleWaits >= 5) return; // ~100ms quiet: peer done
			}
		}
		r = p_recv(s, drainbuf, (int)sizeof drainbuf, 0);
	}
}

// close: drop fd's reference. Only the last reference tears the handle down.
int close(int fd) {
	if (fd < 0 || fd >= KFD_MAX) { errno = L_EBADF; return -1; }
	if (kfd_tab[fd]) {
		AcquireSRWLockExclusive(&kfd_lock);
		kfd_desc *d = kfd_tab[fd];
		int last = --d->refs == 0;
		if (!last) kfd_tab[fd] = NULL;
		ReleaseSRWLockExclusive(&kfd_lock);
		if (!last) {
			// A classified std fd whose description lives on through an alias: the
			// CRT fd stays open underneath (the description reads and writes through
			// it) and is closed with the last reference; it must not be classified
			// into a second description meanwhile.
			if (d->crt_fd == fd) kfd_plain[fd].classified = 1;
			kfd_unpark(fd);
			return 0;
		}
	}
	if (KD(fd)->kind == KFD_SOCKET) {
		ws_SOCKET s = kfd_sock(fd);
		int inherited = KD(fd)->inherited;
		// A cluster listener takes its hand-off channels with it: the workers see
		// the primary's end close and accept for themselves from then on.
		kfd_rr *rr = KD(fd)->rr;
		int rr_chan = KD(fd)->rr_chan;
		KD(fd)->rr = NULL;
		KD(fd)->rr_chan = 0;
		kfd_rr_claimed[fd] = 0;
		kfd_slot_release(fd);
		if (rr_chan) close(rr_chan);
		if (rr) {
			for (int i = 0; i < rr->n; i++) if (rr->w[i].chan) close(rr->w[i].chan);
			free(rr); /* scratch */
		}
		if (inherited) {
			// A child holds this socket too: drop this process's handle only.
			return CloseHandle((HANDLE)s) ? 0 : (errno = L_EBADF, -1);
		}
		p_shutdown(s, 1 /* SD_SEND */);
		kfd_sock_drain(s);
		return p_closesocket(s) == 0 ? 0 : set_wsa_errno();
	}
	if (kfd_tab[fd]) {
		// An owned pipe end closes its own handle; a classified std fd's handle
		// is the CRT's to close, under the fd it was promoted from.
		HANDLE h = kfd_tab[fd]->h;
		int crt = kfd_tab[fd]->crt_fd;
		kfd_slot_release(fd);
		if (h) return CloseHandle(h) ? 0 : (errno = L_EBADF, -1);
		if (crt < 0 || kfd_keep_crt) return 0;
		memset(&kfd_plain[crt], 0, sizeof kfd_plain[crt]);
		return _close(crt) == 0 ? 0 : (errno = L_EBADF, -1);
	}
	memset(&kfd_plain[fd], 0, sizeof kfd_plain[fd]);
	return _close(fd) == 0 ? 0 : (errno = L_EBADF, -1);
}

// A socket libuv takes over (klainpool.c's uv_tcp_open): its SOCKET, its
// slot freed without closing it. -1 when it is not a socket this process
// alone holds — a descriptor dup'd, a socket shared with a child, or a
// cluster listener or channel, which the table's own accept and hand-off
// keep.
intptr_t __kml_win_fd_release_socket(int fd) {
	if (!kfd_is(fd, KFD_SOCKET) || KD(fd)->rr || KD(fd)->rr_chan || KD(fd)->inherited || kfd_rr_claimed[fd]) return -1;
	if (kfd_tab[fd] && kfd_tab[fd]->refs != 1) return -1;
	ws_SOCKET s = kfd_sock(fd);
	kfd_slot_release(fd);
	return (intptr_t)s;
}

// A listener whose socket libuv owns (klainpool.c keeps its slot as the
// record worker spawning and the round-robin deal read): the record goes, its
// hand-off channels closed, the socket left to libuv's close.
void __kml_win_listener_forget(int fd) {
	if (!kfd_is(fd, KFD_SOCKET)) return;
	if (kfd_tab[fd]) {
		AcquireSRWLockExclusive(&kfd_lock);
		int last = --kfd_tab[fd]->refs == 0;
		if (!last) kfd_tab[fd] = NULL;
		ReleaseSRWLockExclusive(&kfd_lock);
		if (!last) return;
	}
	kfd_rr *rr = KD(fd)->rr;
	int rr_chan = KD(fd)->rr_chan;
	KD(fd)->rr = NULL;
	KD(fd)->rr_chan = 0;
	kfd_rr_claimed[fd] = 0;
	kfd_slot_release(fd);
	if (rr_chan) close(rr_chan);
	if (rr) {
		for (int i = 0; i < rr->n; i++) if (rr->w[i].chan) close(rr->w[i].chan);
		free(rr); /* scratch */
	}
}

int fcntl(int fd, int cmd, ...) {
	if (fd < 0 || fd >= KFD_MAX) { errno = L_EBADF; return -1; }
	if (cmd == L_F_GETFL) return KD(fd)->nonblock ? L_O_NONBLOCK : 0;
	if (cmd == L_F_SETFL) {
		va_list ap;
		va_start(ap, cmd);
		int flags = va_arg(ap, int);
		va_end(ap);
		int nb = (flags & L_O_NONBLOCK) != 0;
		KD(fd)->nonblock = (unsigned char)nb;
		if (KD(fd)->kind == KFD_SOCKET) {
			unsigned long v = (unsigned long)nb;
			if (p_ioctlsocket(kfd_sock(fd), (long)WS_FIONBIO, &v) != 0) return set_wsa_errno();
		}
		// Pipes and the console emulate non-blocking in read() above.
		return 0;
	}
	errno = L_EINVAL;
	return -1;
}

// ---- poll -------------------------------------------------------------------------
// poll() for the runtime's blocking helpers (a TLS or HTTP/2 handshake
// waiting on its socket, a write to a full socket, a prompt's timed read of
// stdin) — never the event loop, which is libuv's. Sockets are WSAPoll'd;
// a pipe is ready when PeekNamedPipe sees bytes or its end, the console when
// its reader thread has decoded input, anything else at once. A wait that
// involves anything but sockets is taken in short slices.
struct kml_pollfd { int fd; short events; short revents; };
#define KP_IN 0x0001
#define KP_OUT 0x0004
#define KP_HUP 0x0010
static int poll_once(struct kml_pollfd *fds, unsigned long n, int *only_socks) {
	int count = 0;
	*only_socks = 1;
	for (unsigned long i = 0; i < n; i++) {
		int fd = fds[i].fd;
		fds[i].revents = 0;
		if (fd < 0 || fd >= KFD_MAX) continue;
		if (kfd_is(fd, KFD_SOCKET)) {
			ws_pollfd pf = { kfd_sock(fd), (short)(((fds[i].events & KP_IN) ? WS_POLLRDNORM : 0) | ((fds[i].events & KP_OUT) ? WS_POLLWRNORM : 0)), 0 };
			if (p_WSAPoll && p_WSAPoll(&pf, 1, 0) > 0) {
				if (pf.revents & (WS_POLLRDNORM | WS_POLLHUP | WS_POLLERR)) fds[i].revents |= fds[i].events & KP_IN;
				if (pf.revents & (WS_POLLWRNORM | WS_POLLERR)) fds[i].revents |= fds[i].events & KP_OUT;
				if (pf.revents & WS_POLLHUP) fds[i].revents |= KP_HUP;
			}
		} else {
			*only_socks = 0;
			kfd_classify(fd);
			HANDLE h = kfd_handle(fd);
			if (fds[i].events & KP_IN) {
				if (KD(fd)->kind == KFD_CONSOLE) {
					if (kcon_readable()) fds[i].revents |= KP_IN;
				} else if (h != INVALID_HANDLE_VALUE && GetFileType(h) == FILE_TYPE_PIPE) {
					int st = pipe_readable(h, NULL);
					if (st != 0) fds[i].revents |= st < 0 ? (KP_IN | KP_HUP) : KP_IN;
				} else {
					fds[i].revents |= KP_IN;
				}
			}
			if (fds[i].events & KP_OUT) fds[i].revents |= KP_OUT;
		}
		if (fds[i].revents) count++;
	}
	return count;
}
int poll(struct kml_pollfd *fds, unsigned long n, int timeout) {
	ULONGLONG deadline = timeout < 0 ? 0 : GetTickCount64() + (ULONGLONG)timeout;
	for (;;) {
		int only_socks;
		int c = poll_once(fds, n, &only_socks);
		if (c > 0 || timeout == 0) return c;
		int left = timeout < 0 ? -1 : (int)(deadline > GetTickCount64() ? deadline - GetTickCount64() : 0);
		if (left == 0) return 0;
		if (only_socks && p_WSAPoll && n <= 64) {
			ws_pollfd pf[64];
			for (unsigned long i = 0; i < n; i++) {
				pf[i].fd = fds[i].fd >= 0 && fds[i].fd < KFD_MAX ? kfd_sock(fds[i].fd) : WS_INVALID;
				pf[i].events = (short)(((fds[i].events & KP_IN) ? WS_POLLRDNORM : 0) | ((fds[i].events & KP_OUT) ? WS_POLLWRNORM : 0));
				pf[i].revents = 0;
			}
			if (p_WSAPoll(pf, n, left) < 0) return set_wsa_errno();
			continue; // the next pass reports what woke it
		}
		if (n == 1 && fds[0].fd >= 0 && fds[0].fd < KFD_MAX && KD(fds[0].fd)->kind == KFD_CONSOLE) {
			WaitForSingleObject(kcon.data, left < 0 ? 10 : (DWORD)(left < 10 ? left : 10));
			continue;
		}
		Sleep(left < 0 || left > 5 ? 5 : (DWORD)left);
	}
}

// ---- fibers over Win32 Fibers ------------------------------------------------
// The IR allocates ucontextLayout() bytes per context and stores ss_sp /
// ss_size / uc_link at the offsets that function returns; on Windows those
// are the fields of this struct (see ucontextLayout's windows case).
typedef struct kml_ucontext {
	void *fiber;       // 0
	void *ss_sp;       // 8  (IR-allocated stack; unused — Win32 owns fiber stacks)
	int64_t ss_size;   // 16
	struct kml_ucontext *uc_link; // 24
	void (*fn)(void);  // 32
	int64_t argc;      // 40
	void *park_lo;     // 48  the GC root registered while this context is parked
	void *park_hi;     // 56
} kml_ucontext;

// A finished fiber, deleted on the next switch. Thread-local: the goroutine
// scheduler (klainsync.c) runs a fiber scheduler on every M thread.
static _Thread_local void *g_fiber_to_delete;

// ---- fibers and the collector (-mm=gc) --------------------------------------
// A Win32 fiber runs on a stack the *OS* allocated for it (CreateFiberEx), not
// on the `ss_sp` block the IR mallocs for POSIX's makecontext — which is why
// that block is unused here. Boehm has to be told, twice over:
//
//   * **The running fiber.** The IR points `GC_stackbottom` at the high end of
//     its malloc'd stack before swapping in (correct under ucontext, where that
//     block *is* the stack). On Windows the live SP is in the fiber's OS stack
//     instead, so a collection would scan from that SP to an address in an
//     unrelated heap block — a range spanning arbitrary, largely unmapped
//     memory. It read whatever lay between as candidate pointers and eventually
//     faulted inside the collector's own mark phase. Every resume therefore
//     re-points GC_stackbottom at the stack actually in use, taken from the TEB
//     (the fields Win32 swaps on SwitchToFiber), which is the last write before
//     execution continues on that stack.
//
//   * **A parked fiber.** Under ucontext the parked stack is a GC_malloc'd
//     block, reachable from the connection it belongs to, so the collector
//     scans it like any other object. An OS-owned fiber stack is invisible, and
//     anything live only in a parked frame — a request being assembled, a
//     response buffer — would be collected out from under the fiber that is
//     about to resume. The live part of the outgoing stack is registered as a
//     root for exactly as long as it is parked.
//
// Both are set up by gcshim.c, which is linked only under `-mm=gc`; in every
// other build the hooks stay NULL and this costs two predictable branches per
// fiber switch. Keeping the collector's name out of this file is deliberate:
// the shim must link with no GC present.
void (*__kml_gc_set_stackbottom)(void *stack_base);
void (*__kml_gc_root_add)(void *lo, void *hi_plus_one);
void (*__kml_gc_root_remove)(void *lo, void *hi_plus_one);

// The stack the calling context is really running on. On a fiber the TEB's
// NT_TIB holds that fiber's bounds, swapped in by SwitchToFiber; on the thread
// itself it holds the thread's own.
static void *kml_stack_base(void) {
	NT_TIB *tib = (NT_TIB *)NtCurrentTeb(); // a TEB begins with its NT_TIB
	return tib->StackBase;
}

// Called wherever execution (re)starts on a fiber's stack, after the switch.
static void kml_fiber_gc_resumed(void) {
	if (__kml_gc_set_stackbottom) __kml_gc_set_stackbottom(kml_stack_base());
}

static void ensure_thread_is_fiber(void) {
	if (!IsThreadAFiber()) ConvertThreadToFiber(NULL);
}

static void CALLBACK kml_fiber_tramp(void *arg) {
	kml_ucontext *ctx = (kml_ucontext *)arg;
	kml_fiber_gc_resumed(); // first run on this fiber's own stack
	ctx->fn();
	// Like makecontext's uc_link: when fn returns, resume the linked context.
	// The fiber cannot delete itself; leave it for the next switch to reap.
	g_fiber_to_delete = GetCurrentFiber();
	kml_ucontext *next = ctx->uc_link;
	if (next && next->fiber) SwitchToFiber(next->fiber);
	// No link: nothing to return to — the thread ends here, as it would on
	// POSIX when a context with a null uc_link finishes.
	ExitThread(0);
}

static void reap_finished_fiber(void) {
	if (g_fiber_to_delete && g_fiber_to_delete != GetCurrentFiber()) {
		DeleteFiber(g_fiber_to_delete);
		g_fiber_to_delete = NULL;
	}
}

int getcontext(kml_ucontext *ctx) {
	ensure_thread_is_fiber();
	ctx->fiber = GetCurrentFiber();
	ctx->park_lo = ctx->park_hi = NULL;
	return 0;
}

void makecontext(kml_ucontext *ctx, void (*fn)(void), int argc, ...) {
	(void)argc;
	ctx->fn = fn;
	ctx->argc = argc;
	SIZE_T reserve = ctx->ss_size > 0 ? (SIZE_T)ctx->ss_size : (SIZE_T)(1024 * 1024);
	ctx->fiber = CreateFiberEx(64 * 1024, reserve, 0, kml_fiber_tramp, ctx);
}

int swapcontext(kml_ucontext *from, kml_ucontext *to) {
	ensure_thread_is_fiber();
	from->fiber = GetCurrentFiber();
	if (!to->fiber) { errno = L_EINVAL; return -1; }
	// The parked range is held in this frame — which lives on the very stack
	// being parked, so it needs no bookkeeping elsewhere and cannot be lost.
	// SwitchToFiber keeps the outgoing fiber's callee-saved registers in the
	// fiber's own control block, which the collector never sees: a pointer the
	// caller holds only in rbx/rsi/rdi/r12–r15 would be invisible for as long as
	// this context is parked. Force them into this frame — above `probe`, so
	// inside the registered range.
	__builtin_unwind_init();
	volatile char probe;
	void *lo = (void *)((uintptr_t)&probe & ~(uintptr_t)15), *hi = kml_stack_base();
	from->park_lo = from->park_hi = NULL;
	if (__kml_gc_root_add && lo < hi) { __kml_gc_root_add(lo, hi); from->park_lo = lo; from->park_hi = hi; }
	SwitchToFiber(to->fiber);
	// Back on `from`, on its own stack again: it is scanned as the running
	// stack from here on, so drop the parked-root registration first.
	if (__kml_gc_root_remove && lo < hi) __kml_gc_root_remove(lo, hi);
	from->park_lo = from->park_hi = NULL;
	kml_fiber_gc_resumed();
	// Reap whatever finished while we were away.
	reap_finished_fiber();
	return 0;
}

// A coroutine that finished by switching away (it never returns through
// kml_fiber_tramp, so the reap above never sees it) is released by whoever
// resumed it: drop the parked-stack root its last switch left registered —
// that stack is about to be unmapped — and delete the fiber.
void __kml_ctx_release(kml_ucontext *ctx) {
	if (!ctx) return;
	if (ctx->park_lo && __kml_gc_root_remove) __kml_gc_root_remove(ctx->park_lo, ctx->park_hi);
	ctx->park_lo = ctx->park_hi = NULL;
	if (ctx->fiber && ctx->fiber != GetCurrentFiber()) DeleteFiber(ctx->fiber);
	ctx->fiber = NULL;
}

// ---- misc ---------------------------------------------------------------------
int usleep(unsigned usec) { __kml_win_hr_sleep_us((int64_t)usec); return 0; }

