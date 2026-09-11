// Windows I/O layer (TDD-00177 Stage 2). Compiled alongside win32shim.c
// into the cached shim object every Windows clang invocation links.
//
// The emitted IR is written against a small-integer file-descriptor world:
// sockets, pipes, files and stdio are all `int fd`s driven by read/write/
// close/fcntl, and the reactor waits on them with select() over a 1024-bit
// fd_set bitmap. This file provides exactly that surface on Windows:
//
//   * files and pipes are real CRT fds (below 512); sockets get fd numbers
//     from this layer's own range (512..895) with the SOCKET kept in a side
//     table, so the CRT never wraps a socket handle — see the fd table below;
//   * the side table also records a per-fd non-blocking flag, so read/write/
//     close/fcntl dispatch to Winsock for sockets and Win32 pipe calls
//     otherwise;
//   * select() is a projection over an I/O completion port (TDD-00183): pipe
//     and console readiness is probed level-triggered at the source
//     (PeekNamedPipe, the console thread's decoded queue) and the wait is a
//     single alertable GetQueuedCompletionStatusEx, woken by zero-byte
//     overlapped reads (libuv's zero-read mode), reader-thread packets, and
//     posted wake packets (signals). While *sockets* are watched, the wait
//     temporarily remains Winsock select in short slices (not WSAPoll, which
//     never reports a failed connect) — the Stage 1→2 migration bridge,
//     removed when sockets join the port via overlapped ops;
//   * getcontext/makecontext/swapcontext are provided over Win32 Fibers.
//
// Constants come in as the *Linux* values the IR's Go-side helpers emit
// (httpSockConstants, httpNonblockFlag, httpEagainErrno, the fs open-flag
// map) and are translated here, so the emitters need no Windows cases for
// this layer. errno is likewise set to the Linux numbers the IR compares
// against (EAGAIN 11, EINPROGRESS 115, ECONNRESET 104, ...).
//
// Winsock is bound through GetProcAddress rather than <winsock2.h>: this
// file *defines* socket/bind/select/... under their POSIX names and
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

int close(int fd); // defined below; dup2 needs it first
static int io_trace(void); // defined with read() below
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
#define KFD_MAX 1024
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
// Winsock's select() reads fd_count and never assumes FD_SETSIZE, so a
// wider array is fine; declared big enough for every fd this layer can hold.
typedef struct { unsigned int fd_count; ws_SOCKET fd_array[KFD_MAX]; } ws_big_fd_set;
typedef struct { int32_t tv_sec; int32_t tv_usec; } ws_timeval;
static int (WINAPI *p_select)(int, ws_big_fd_set *, ws_big_fd_set *, ws_big_fd_set *, const ws_timeval *);
static int (WINAPI *p_getaddrinfo)(const char *, const char *, const ws_addrinfo *, ws_addrinfo **);
static void (WINAPI *p_freeaddrinfo)(ws_addrinfo *);
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
	B(ioctlsocket); B(WSAIoctl); B(WSAPoll); B(select); B(getaddrinfo); B(freeaddrinfo); B(inet_pton);
	B(inet_ntop); B(gethostname); B(htons); B(ntohs);
#undef B
	ws_WSADATA d;
	if (p_WSAStartup) p_WSAStartup(0x0202, &d);
}
__attribute__((constructor)) static void kml_win_io_init(void) { ws_init(); }

// ---- owned fd descriptor table (TDD-00182 Stage 1) -------------------------
// One slot per fd, replacing the former parallel kfd_kind/kfd_nonblock/
// kfd_sock_handle/kfd_reset/kfd_inherited arrays with a single owned
// description keyed by fd. The slot owns the `kind` tag that read/write/close/
// select/fstat all dispatch on. Socket/foreign kinds keep the Winsock SOCKET
// in `sock` (the CRT never wraps it — see the range note below); CRT-range fds
// leave their OS handle with the CRT (_get_osfhandle), exactly as before, so
// this consolidation is behaviour-preserving. Later reactor stages extend the
// slot in place — per-handle overlapped read/write state (Stage 3) and a
// refcounted shared description for dup2 aliasing (Stage 5) — rather than
// adding new parallel arrays.
//
// Three fd ranges still share the IR's 1024-slot bitmap (unchanged in Stage 1;
// the socket-range partition dissolves in Stage 5):
//   [0, 512)      CRT fds: files, pipes, stdio — the CRT owns the handle;
//   [512, 896)    sockets this layer created — the SOCKET lives in slot.sock
//                 and the CRT never sees it (a CRT fd wrapped around a socket
//                 would CloseHandle it behind Winsock's back on _close, which
//                 raises under a debugger and skips the socket's own teardown);
//   [896, 1024)   foreign sockets owned by a library (libcurl) that select()
//                 must still wait on — see curl_multi_fdset below.
enum { KFD_PLAIN = 0, KFD_SOCKET = 1, KFD_PIPE = 2, KFD_FOREIGN = 3, KFD_CONSOLE = 4 };
#define KFD_SOCK_BASE 512
#define KFD_FOREIGN_BASE 896

// A pending overlapped operation (TDD-00183 Stage 1). Heap-allocated so a
// completion dequeued after the fd closed (and the slot was reused) is
// recognized by generation mismatch and simply freed. Ownership rule: an op
// that went pending is freed by the port drain when its packet arrives (a
// CancelIoEx'd op still posts one); an op that completed synchronously never
// reaches the port (FILE_SKIP_COMPLETION_PORT_ON_SUCCESS) and is freed by
// its creator.
typedef struct kfd_op {
	OVERLAPPED ov;
	int fd;
	unsigned gen;
	unsigned char kind;      // OPK_*
	char *buf;               // OPK_WRITE: the heap copy of the payload
	unsigned long len;
	struct kfd_op *next;     // write-queue FIFO link
} kfd_op;
enum { OPK_ZERO_READ = 1, OPK_WRITE = 2 };

// Reader-thread state for a synchronous (inherited) pipe — libuv's
// non-overlapped-pipe thread. A zero-byte ReadFile is the WRONG primitive on
// a synchronous byte pipe (it returns immediately without waiting for data,
// and, worse, races a concurrent ReadFile in read() so the real read stalls
// until the next flush). So the thread instead blocks reading REAL bytes into
// a ring buffer and posts a wake; read() drains the buffer and never touches
// the handle — the same shape as the console reader. Jointly owned: the
// closer signals stop, cancels the blocked read, joins, then frees.
typedef struct {
	int fd;
	unsigned gen;
	HANDLE h;
	CRITICAL_SECTION cs;
	HANDLE space;            // auto-reset: buffer had room freed, resume reading
	char buf[65536];
	int len;
	int eof;
	volatile LONG stop;
} kfd_thr;

typedef struct {
	ws_SOCKET sock;          // socket/foreign kinds: the Winsock SOCKET; else 0
	unsigned char kind;      // KFD_PLAIN / KFD_SOCKET / KFD_PIPE / KFD_FOREIGN / KFD_CONSOLE
	unsigned char nonblock;  // O_NONBLOCK emulation flag
	// Set once a socket has reported a connection reset: Linux returns
	// ECONNRESET from one recv and end-of-file from the next, and the reactor's
	// close-on-EOF path depends on that; Winsock keeps returning the error.
	unsigned char reset;
	// Set by win32proc.c once a socket has been handed to a child process. The
	// child's inherited handle refers to the same socket object, and closesocket
	// tears that object down for everyone — so the parent releases only its own
	// handle (CloseHandle) when it closes such an fd.
	unsigned char inherited;
	// ---- reactor state (TDD-00183 Stage 1) ----
	unsigned char ovl;       // pipe handle created FILE_FLAG_OVERLAPPED by this layer
	unsigned char assoc;     // handle associated with the completion port
	unsigned char classified;// std/CRT fd's kind probed (GetFileType) once
	unsigned char use_reader;// serve reads from a buffering reader thread (stdin fd 0)
	unsigned gen;            // bumped on close; stale completions/threads detected
	kfd_op *rd_op;           // armed zero-read (wake-only), if pending
	kfd_op *wr_op;           // the queued overlapped write currently posted
	kfd_op *wq_head, *wq_tail; // queued overlapped writes not yet posted
	HANDLE rd_ev, wr_ev;     // inline-wait events (lazy, kept across slot reuse)
	HANDLE thr;              // sync-pipe reader thread handle
	kfd_thr *thr_state;      // its shared state (stop flag / consumed event)
} kfd_slot;
static kfd_slot kfd[KFD_MAX];

// Cross-file accessors (win32proc.c, win32fs.c). Functions rather than exported
// arrays so the slot layout can grow across reactor stages without other
// objects rebuilding against a fixed struct. kfd_kind_of returns -1 for an
// out-of-range fd (never a valid KFD_* value).
int kfd_kind_of(int fd) { return (fd < 0 || fd >= KFD_MAX) ? -1 : kfd[fd].kind; }
void kfd_set_inherited(int fd) { if (fd >= 0 && fd < KFD_MAX) kfd[fd].inherited = 1; }

// kfd_adopt_socket places socket handle s at exactly fd (used for a socket
// inherited from a parent under an agreed fd number); -1 if the slot is
// outside the socket range or taken.
int kfd_adopt_socket(HANDLE s, int fd) {
	if (fd < KFD_SOCK_BASE || fd >= KFD_FOREIGN_BASE || kfd[fd].kind != KFD_PLAIN) { errno = L_EINVAL; return -1; }
	kfd[fd].kind = KFD_SOCKET;
	kfd[fd].nonblock = 0;
	kfd[fd].reset = 0;
	kfd[fd].inherited = 0;
	kfd[fd].sock = (ws_SOCKET)s;
	return fd;
}

int kfd_register(HANDLE h, int kind) {
	if (kind == KFD_SOCKET) {
		for (int fd = KFD_SOCK_BASE; fd < KFD_FOREIGN_BASE; fd++) {
			if (kfd[fd].kind == KFD_PLAIN) return kfd_adopt_socket(h, fd);
		}
		errno = L_EMFILE;
		return -1;
	}
	// CRT range: _setmaxstdio raises the default 512-fd ceiling once so the
	// CRT can hand out every slot below KFD_SOCK_BASE.
	static int raised;
	if (!raised) { _setmaxstdio(KFD_SOCK_BASE); raised = 1; }
	int fd = _open_osfhandle((intptr_t)h, 0);
	if (fd < 0 || fd >= KFD_SOCK_BASE) {
		if (fd >= 0) _close(fd);
		errno = L_EMFILE;
		return -1;
	}
	kfd[fd].kind = (unsigned char)kind;
	kfd[fd].nonblock = 0;
	return fd;
}
static inline int kfd_is(int fd, int kind) {
	return fd >= 0 && fd < KFD_MAX && kfd[fd].kind == kind;
}
static inline ws_SOCKET kfd_sock(int fd) {
	if (kfd[fd].kind == KFD_SOCKET || kfd[fd].kind == KFD_FOREIGN) return kfd[fd].sock;
	return (ws_SOCKET)_get_osfhandle(fd);
}
// The Winsock SOCKET behind fd, for code that must hand a real socket to a
// library speaking Winsock itself (OpenSSL's socket BIO in tls.c).
intptr_t __kml_win_fd_socket(int fd) {
	if (fd < 0 || fd >= KFD_MAX) return -1;
	return (intptr_t)kfd_sock(fd);
}
// kfd_handle: the OS handle behind any fd (win32proc.c duplicates it for a
// child's stdio or an inherited IPC socket).
HANDLE kfd_handle(int fd) {
	if (fd < 0 || fd >= KFD_MAX) return INVALID_HANDLE_VALUE;
	if (kfd[fd].kind == KFD_SOCKET || kfd[fd].kind == KFD_FOREIGN) return (HANDLE)kfd[fd].sock;
	return (HANDLE)_get_osfhandle(fd);
}

// ---- the completion port (TDD-00183) ----------------------------------------
// One I/O completion port PER REACTOR THREAD (thread-local): a worker thread
// runs its own event loop, and a shared port would let one thread's
// GetQueuedCompletionStatusEx steal a wake meant for another. Stage 1 only the
// main reactor actually uses a port (stdin/console/child-stdio are main-thread
// concerns); a worker thread's port stays null and its select() falls back to
// the poll slice. Reader/console/signal wakes all belong to the main reactor,
// so they target kml_main_port.
static _Thread_local HANDLE kml_port;
static HANDLE kml_main_port; // the first port created (the main reactor's), for cross-thread wakes
static HANDLE kml_port_get(void) {
	if (!kml_port) {
		kml_port = CreateIoCompletionPort(INVALID_HANDLE_VALUE, NULL, 0, 1);
		if (!kml_main_port) kml_main_port = kml_port;
	}
	return kml_port;
}

// __kml_win_loop_wake: post a wake packet so the main reactor re-checks its
// world — the console-ctrl handler (win32proc.c), the stdin/console reader
// threads, and any future background source call this, all on the main
// reactor's behalf. Harmless before the port exists or from any thread.
void __kml_win_loop_wake(void) {
	if (kml_main_port) PostQueuedCompletionStatus(kml_main_port, 0, 0 /* KEY_WAKE */, NULL);
}

static kfd_op *op_new(int fd, int kind) {
	kfd_op *op = (kfd_op *)calloc(1, sizeof *op);
	if (!op) return NULL;
	op->fd = fd;
	op->gen = kfd[fd].gen;
	op->kind = (unsigned char)kind;
	return op;
}

// Associate fd's handle with the port once. FILE_SKIP_COMPLETION_PORT_ON_SUCCESS
// keeps synchronously-completing ops (data already buffered) from flooding the
// port with packets the drain would only discard.
static int kfd_assoc(int fd) {
	if (kfd[fd].assoc) return 0;
	HANDLE h = kfd_handle(fd);
	if (h == INVALID_HANDLE_VALUE) return -1;
	if (!CreateIoCompletionPort(h, kml_port_get(), 1 /* KEY_OP */, 0)) return -1;
	SetFileCompletionNotificationModes(h, FILE_SKIP_COMPLETION_PORT_ON_SUCCESS);
	kfd[fd].assoc = 1;
	return 0;
}

// Drain the port: free completed op records (clearing the owning slot's
// pending pointer when the generation still matches), pump the write queue
// forward, and count wake packets. timeout_ms 0 polls; <0 blocks (alertable,
// so queued APCs still run). Returns the number of entries dequeued, or -1
// with errno=EINTR semantics left to the caller's signal check.
static void kfd_wq_pump(int fd); // below
static int kml_port_drain(int timeout_ms) {
	OVERLAPPED_ENTRY ents[64];
	ULONG got = 0;
	if (!GetQueuedCompletionStatusEx(kml_port_get(), ents, 64, &got,
	                                 timeout_ms < 0 ? INFINITE : (DWORD)timeout_ms, TRUE)) {
		return 0; // timeout / WAIT_IO_COMPLETION: nothing dequeued
	}
	for (ULONG i = 0; i < got; i++) {
		kfd_op *op = (kfd_op *)ents[i].lpOverlapped;
		if (!op) continue; // a bare wake packet
		int fd = op->fd;
		int live = fd >= 0 && fd < KFD_MAX && kfd[fd].gen == op->gen;
		if (io_trace()) fprintf(stderr, "[io] port op fd=%d kind=%d live=%d\n", fd, op->kind, live);
		if (live) {
			if (op->kind == OPK_ZERO_READ && kfd[fd].rd_op == op) kfd[fd].rd_op = NULL;
			if (op->kind == OPK_WRITE && kfd[fd].wr_op == op) { kfd[fd].wr_op = NULL; kfd_wq_pump(fd); }
		}
		free(op->buf);
		free(op);
	}
	return (int)got;
}

// (Zero-byte overlapped read arming — libuv's zero-read wake mode for
// overlapped read ends — lands in Stage 2, where sockets and any overlapped
// pipe read ends need it. Stage 1's pipe() read ends are anonymous/polled and
// stdin is served by a reader thread, so nothing arms a zero-read yet; the
// OPK_ZERO_READ slot state and its drain handling are kept ready for it.)

// Queued overlapped writes: post the head of the FIFO if nothing is in
// flight. A synchronous completion pumps the next entry immediately; a
// pending one is finished (and the queue pumped) by the port drain.
static void kfd_wq_pump(int fd) {
	while (!kfd[fd].wr_op && kfd[fd].wq_head) {
		kfd_op *op = kfd[fd].wq_head;
		kfd[fd].wq_head = op->next;
		if (!kfd[fd].wq_head) kfd[fd].wq_tail = NULL;
		op->next = NULL;
		DWORD n = 0;
		if (!WriteFile(kfd_handle(fd), op->buf, op->len, &n, &op->ov)) {
			if (GetLastError() == ERROR_IO_PENDING) { kfd[fd].wr_op = op; return; }
			// Broken pipe: drop the payload, as a POSIX write would EPIPE —
			// the reader is gone; nothing can observe the bytes either way.
		}
		free(op->buf);
		free(op);
	}
}

// Wait until fd's queued writes fully drained (close/flush path).
static void kfd_wq_flush(int fd) {
	while (kfd[fd].wr_op || kfd[fd].wq_head) {
		if (kml_port_drain(-1) == 0) continue;
	}
}

// ---- named-pipe pairs -------------------------------------------------------
// CreatePipe gives a synchronous anonymous pipe — the 4 KB blocking child
// stdio TDD-00180 §1 flags. These pairs are libuv's shape instead: a
// uniquely-named one-instance named pipe, the server end overlapped (the
// loop's side), the client end synchronous when it is destined for a child
// process (an arbitrary child's CRT calls ReadFile/WriteFile with no
// OVERLAPPED, which is undefined on an overlapped handle).
static int kml_pipe_pair(HANDLE *server, HANDLE *client, int server_reads, int client_sync) {
	static volatile LONG seq;
	wchar_t name[96];
	wsprintfW(name, L"\\\\.\\pipe\\kml-%u-%u", (unsigned)GetCurrentProcessId(),
	          (unsigned)InterlockedIncrement(&seq));
	DWORD open_mode = (server_reads ? PIPE_ACCESS_INBOUND : PIPE_ACCESS_OUTBOUND) |
	                  FILE_FLAG_OVERLAPPED | FILE_FLAG_FIRST_PIPE_INSTANCE;
	HANDLE s = CreateNamedPipeW(name, open_mode, PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_WAIT,
	                            1, 65536, 65536, 0, NULL);
	if (s == INVALID_HANDLE_VALUE) return -1;
	HANDLE c = CreateFileW(name, server_reads ? GENERIC_WRITE : GENERIC_READ,
	                       0, NULL, OPEN_EXISTING,
	                       client_sync ? 0 : FILE_FLAG_OVERLAPPED, NULL);
	if (c == INVALID_HANDLE_VALUE) { CloseHandle(s); return -1; }
	*server = s;
	*client = c;
	return 0;
}

// ---- synchronous-pipe reader threads ---------------------------------------
// For a pipe handle this layer did not create overlapped (our own inherited
// fd 0 when the parent piped it), the wake comes from a thread blocked in a
// zero-byte synchronous ReadFile — it returns when data is available and
// consumes nothing. One post per batch; the thread re-arms only after read()
// has drained (the consumed event), so it never floods the port.
static DWORD WINAPI kfd_sync_pipe_thread(LPVOID arg) {
	kfd_thr *t = (kfd_thr *)arg;
	char chunk[16384];
	for (;;) {
		if (t->stop || kfd[t->fd].gen != t->gen) break;
		DWORD n = 0;
		if (!ReadFile(t->h, chunk, sizeof chunk, &n, NULL) || n == 0) break; // EOF / broken pipe
		if (t->stop || kfd[t->fd].gen != t->gen) break;
		DWORD off = 0;
		while (off < n) {
			EnterCriticalSection(&t->cs);
			int room = (int)sizeof t->buf - t->len;
			if (room > 0) {
				int take = (int)(n - off < (DWORD)room ? n - off : (DWORD)room);
				memcpy(t->buf + t->len, chunk + off, (size_t)take);
				t->len += take;
				off += (DWORD)take;
			}
			LeaveCriticalSection(&t->cs);
			__kml_win_loop_wake();
			if (off < n) WaitForSingleObject(t->space, INFINITE); // buffer full: wait for a drain
		}
	}
	EnterCriticalSection(&t->cs);
	t->eof = 1;
	LeaveCriticalSection(&t->cs);
	__kml_win_loop_wake();
	return 0;
}

static void kfd_ensure_sync_reader(int fd) {
	if (kfd[fd].thr || kfd[fd].ovl) return;
	kml_port_get(); // establish this reactor's port so the thread's wake blocks/ends the wait
	kfd_thr *t = (kfd_thr *)calloc(1, sizeof *t);
	if (!t) return;
	t->fd = fd;
	t->gen = kfd[fd].gen;
	t->h = kfd_handle(fd);
	InitializeCriticalSection(&t->cs);
	t->space = CreateEventW(NULL, FALSE, FALSE, NULL);
	HANDLE h = CreateThread(NULL, 0, kfd_sync_pipe_thread, t, 0, NULL);
	if (!h) { DeleteCriticalSection(&t->cs); CloseHandle(t->space); free(t); return; }
	kfd[fd].thr = h;
	kfd[fd].thr_state = t;
}

// Readable/EOF state of a sync-reader-backed pipe (level-triggered probe).
static int kfd_thr_readable(int fd) {
	kfd_thr *t = kfd[fd].thr_state;
	if (!t) return 0;
	EnterCriticalSection(&t->cs);
	int r = t->len > 0 ? 1 : (t->eof ? -1 : 0);
	LeaveCriticalSection(&t->cs);
	return r;
}

// Drain up to n bytes from the reader thread's buffer; 0 = EOF, -1/EAGAIN =
// nothing yet (only when nonblocking — a blocking read waits for data).
static int64_t kfd_thr_read(int fd, void *buf, size_t n, int nonblock) {
	kfd_thr *t = kfd[fd].thr_state;
	for (;;) {
		EnterCriticalSection(&t->cs);
		if (t->len > 0) {
			int take = (int)(n < (size_t)t->len ? n : (size_t)t->len);
			memcpy(buf, t->buf, (size_t)take);
			memmove(t->buf, t->buf + take, (size_t)(t->len - take));
			t->len -= take;
			LeaveCriticalSection(&t->cs);
			SetEvent(t->space); // room freed: let the thread resume
			return take;
		}
		int eof = t->eof;
		LeaveCriticalSection(&t->cs);
		if (eof) return 0;
		if (nonblock) { errno = L_EAGAIN; return -1; }
		Sleep(1); // blocking read on a rarely-used path; the thread fills the buffer
	}
}

static void kfd_stop_sync_reader(int fd) {
	kfd_thr *t = kfd[fd].thr_state;
	if (!t) return;
	InterlockedExchange(&t->stop, 1);
	SetEvent(t->space);
	// The thread may be blocked in the kernel read; break it, re-issuing until
	// the join lands (CancelSynchronousIo misses a thread between calls).
	while (WaitForSingleObject(kfd[fd].thr, 50) == WAIT_TIMEOUT) CancelSynchronousIo(kfd[fd].thr);
	CloseHandle(kfd[fd].thr);
	DeleteCriticalSection(&t->cs);
	CloseHandle(t->space);
	free(t);
	kfd[fd].thr = NULL;
	kfd[fd].thr_state = NULL;
}

// ---- console input ----------------------------------------------------------
// Console reads move off the CRT onto a dedicated reader thread doing
// ReadConsoleW — cooked mode keeps the console's own line editing, raw mode
// (setRawMode cleared ENABLE_LINE_INPUT) returns per-keystroke, and VT input
// sequences arrive in-band — decoded UTF-16 → UTF-8 into a queue read()
// drains. This replaces both wrongnesses of the old path: readable-on-any-
// input-record (readiness is now "decoded bytes queued") and code-page
// decoding of non-ASCII input (TDD-00180 §2). The resize watcher
// (win32proc.c) only polls GetConsoleScreenBufferInfo, so it never races
// this thread for input records.
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
		if (wbuf[0] == 0x1a) { // Ctrl+Z at buffer start: EOF, as the CRT treats it
			EnterCriticalSection(&kcon.cs);
			kcon.eof = 1;
			LeaveCriticalSection(&kcon.cs);
			SetEvent(kcon.data);
			__kml_win_loop_wake();
			break;
		}
		int n = WideCharToMultiByte(CP_UTF8, 0, wbuf, (int)got, u8, sizeof u8, NULL, NULL);
		if (n <= 0) continue;
		EnterCriticalSection(&kcon.cs);
		int room = (int)sizeof kcon.buf - kcon.len;
		if (n > room) n = room; // over-full queue: drop the tail, as a tty would
		memcpy(kcon.buf + kcon.len, u8, (size_t)n);
		kcon.len += n;
		LeaveCriticalSection(&kcon.cs);
		SetEvent(kcon.data);
		__kml_win_loop_wake();
		WaitForSingleObject(kcon.consumed, INFINITE);
	}
	EnterCriticalSection(&kcon.cs);
	kcon.eof = 1;
	LeaveCriticalSection(&kcon.cs);
	SetEvent(kcon.data);
	__kml_win_loop_wake();
	return 0;
}

static void kcon_ensure(void) {
	if (kcon.started) return;
	kcon.started = 1;
	kml_port_get(); // establish this reactor's port so the console thread's wake ends the wait
	InitializeCriticalSection(&kcon.cs);
	kcon.data = CreateEventW(NULL, FALSE, FALSE, NULL);
	kcon.consumed = CreateEventW(NULL, FALSE, FALSE, NULL);
	kcon.thr = CreateThread(NULL, 0, kcon_thread, NULL, 0, NULL);
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

static int kcon_readable(void) {
	if (!kcon.started) return 0;
	EnterCriticalSection(&kcon.cs);
	int r = kcon.len > 0 || kcon.eof;
	LeaveCriticalSection(&kcon.cs);
	return r;
}

// ---- std-fd classification --------------------------------------------------
// fds 0-2 arrive from the parent as bare CRT fds; the reactor needs their
// kind. Probed once, lazily, at the first read()/select() touch: a piped
// stdin (fd 0) becomes KFD_PIPE served by the buffering reader thread; a
// console fd 0 becomes KFD_CONSOLE (the reader-thread ReadConsoleW path).
static void kfd_classify(int fd) {
	if (fd < 0 || fd >= KFD_MAX || kfd[fd].classified || kfd[fd].kind != KFD_PLAIN) return;
	kfd[fd].classified = 1;
	HANDLE h = (HANDLE)_get_osfhandle(fd);
	if (h == INVALID_HANDLE_VALUE) return;
	DWORD type = GetFileType(h);
	if (type == FILE_TYPE_PIPE) {
		kfd[fd].kind = KFD_PIPE;
		// Only stdin (fd 0) — an inherited pipe the loop must read without
		// blocking — is served by a buffering reader thread. Other pipes are
		// created by pipe() in-process and stay on the PeekNamedPipe poll path,
		// which cross-thread worker/channel IPC depends on.
		if (fd == 0) kfd[fd].use_reader = 1;
	} else if (type == FILE_TYPE_CHAR) {
		DWORD mode;
		if (fd == 0 && GetConsoleMode(h, &mode)) kfd[fd].kind = KFD_CONSOLE;
	}
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
	// Sockets must not be inherited by child processes (Node's CLOEXEC
	// discipline) so a spawned child never holds a listener open.
	SetHandleInformation((HANDLE)s, HANDLE_FLAG_INHERIT, 0);
	int fd = kfd_register((HANDLE)s, KFD_SOCKET);
	if (fd < 0) p_closesocket(s);
	return fd;
}

int bind(int fd, const void *addr, int len) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	return p_bind(kfd_sock(fd), addr, len) == 0 ? 0 : set_wsa_errno();
}
int listen(int fd, int backlog) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	return p_listen(kfd_sock(fd), backlog) == 0 ? 0 : set_wsa_errno();
}
int accept(int fd, void *addr, int *len) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	ws_SOCKET s = p_accept(kfd_sock(fd), addr, len);
	if (s == WS_INVALID) return set_wsa_errno();
	SetHandleInformation((HANDLE)s, HANDLE_FLAG_INHERIT, 0);
	int nfd = kfd_register((HANDLE)s, KFD_SOCKET);
	if (io_trace()) {
		struct { uint16_t fam, port; uint32_t addr; char z[8]; } pa = {0}, la = {0};
		int pl = sizeof pa, ll = sizeof la;
		p_getpeername(s, &pa, &pl);
		p_getsockname(s, &la, &ll);
		fprintf(stderr, "[io] accept fd=%d -> fd=%d sock=%llu peer=%u local=%u\n", fd, nfd, (unsigned long long)s, (unsigned)htons(pa.port), (unsigned)htons(la.port));
	}
	if (nfd < 0) p_closesocket(s);
	return nfd;
}
int connect(int fd, const void *addr, int len) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	if (p_connect(kfd_sock(fd), addr, len) == 0) {
		if (io_trace()) {
			struct { uint16_t fam, port; uint32_t a; char z[8]; } la = {0};
			int ll = sizeof la;
			p_getsockname(kfd_sock(fd), &la, &ll);
			fprintf(stderr, "[io] connect fd=%d sock=%llu -> 0 local=%u\n", fd, (unsigned long long)kfd_sock(fd), (unsigned)htons(la.port));
		}
		return 0;
	}
	int e = p_WSAGetLastError();
	if (io_trace()) fprintf(stderr, "[io] connect fd=%d -> -1 (wsa %d)\n", fd, e);
	// A non-blocking connect reports WOULDBLOCK on Windows; POSIX callers
	// expect EINPROGRESS and then wait for writability.
	errno = e == WSAEWOULDBLOCK ? L_EINPROGRESS : map_wsa_errno(e);
	return -1;
}
int shutdown(int fd, int how) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	if (io_trace()) fprintf(stderr, "[io] shutdown fd=%d how=%d\n", fd, how);
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
		default: break;
		}
	}
	return p_setsockopt(kfd_sock(fd), level, opt, (const char *)val, len) == 0 ? 0 : set_wsa_errno();
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
	int r = p_recvfrom(kfd_sock(fd), (char *)buf, (int)n, flags, addr, alen);
	return r == WS_ERROR ? set_wsa_errno() : r;
}
int64_t sendto(int fd, const void *buf, size_t n, int flags, const void *addr, int alen) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	int r = p_sendto(kfd_sock(fd), (const char *)buf, (int)n, flags, addr, alen);
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

// ---- pipes ---------------------------------------------------------------------
// pipe(): an anonymous synchronous pipe, unchanged from the pre-reactor layer.
// Both ends live in-process (worker/BroadcastChannel IPC, fork IPC) or the
// write end is inherited by a child that writes it (child stdout/stderr,
// execSync); the loop-side read end is watched by select() and probed with
// PeekNamedPipe (the slice fallback keeps it responsive — anonymous pipes
// cannot be overlapped, so they are not port-armable). Only the two cases
// that genuinely need completion behaviour opt out of pipe(): the child-stdin
// write end (__kml_win_pipe_pw below, an overlapped write-queued server) and
// process.stdin (fd 0, served by a reader thread once classified). Keeping
// pipe() anonymous is deliberate — the reactor's stdin/child-stdio symptoms
// are fixed by those two, and converting these in-process pipe pairs to
// named/overlapped ends regressed cross-thread worker IPC.
int pipe(int fds[2]) {
	HANDLE r, w;
	SECURITY_ATTRIBUTES sa = { sizeof sa, NULL, FALSE };
	if (!CreatePipe(&r, &w, &sa, 0)) { errno = L_EMFILE; return -1; }
	int rfd = kfd_register(r, KFD_PIPE);
	if (rfd < 0) { CloseHandle(r); CloseHandle(w); return -1; }
	int wfd = kfd_register(w, KFD_PIPE);
	if (wfd < 0) { close(rfd); CloseHandle(w); return -1; }
	fds[0] = rfd;
	fds[1] = wfd;
	return 0;
}

// __kml_win_pipe_pw: the parent-writes pair (child stdin). fds[0] is the
// synchronous client read end the child inherits; fds[1] is the overlapped
// server write end the loop keeps — writes on it are queued overlapped ops
// (the Node user-space write queue), so a parent streaming more than a
// pipe-buffer of stdin no longer deadlocks against an unread child stdout.
int __kml_win_pipe_pw(int fds[2]) {
	HANDLE r, w;
	if (kml_pipe_pair(&w, &r, 0 /* server writes */, 1 /* client sync */) != 0) { errno = L_EMFILE; return -1; }
	int rfd = kfd_register(r, KFD_PIPE);
	if (rfd < 0) { CloseHandle(r); CloseHandle(w); return -1; }
	int wfd = kfd_register(w, KFD_PIPE);
	if (wfd < 0) { close(rfd); CloseHandle(w); return -1; }
	kfd[wfd].ovl = 1;
	fds[0] = rfd;
	fds[1] = wfd;
	return 0;
}

int dup2(int oldfd, int newfd) {
	if (oldfd < 0 || oldfd >= KFD_MAX || newfd < 0 || newfd >= KFD_MAX) { errno = L_EBADF; return -1; }
	if (kfd[oldfd].kind == KFD_SOCKET) {
		// Socket slots are this layer's own; only a socket-range target works.
		if (newfd == oldfd) return newfd;
		if (kfd[newfd].kind != KFD_PLAIN) close(newfd);
		return kfd_adopt_socket((HANDLE)kfd[oldfd].sock, newfd);
	}
	if (_dup2(oldfd, newfd) != 0) { errno = L_EBADF; return -1; }
	kfd[newfd].kind = kfd[oldfd].kind;
	kfd[newfd].nonblock = kfd[oldfd].nonblock;
	kfd[newfd].ovl = kfd[oldfd].ovl;
	kfd[newfd].classified = kfd[oldfd].classified;
	return newfd;
}

// ---- read / write / close / fcntl ----------------------------------------------
static int pipe_readable(HANDLE h, DWORD *avail) {
	DWORD n = 0;
	if (!PeekNamedPipe(h, NULL, 0, NULL, &n, NULL)) return -1; // broken pipe → EOF
	if (avail) *avail = n;
	return n > 0;
}

// KML_IO_TRACE=1 in the environment logs every read/select decision to
// stderr — the one debugging aid this layer keeps, since a reactor that
// blocks or spins is otherwise opaque from outside.
static int io_trace(void) {
	static int t = -1;
	if (t < 0) {
		char b[4];
		t = GetEnvironmentVariableA("KML_IO_TRACE", b, sizeof b) > 0;
		if (t) setvbuf(stderr, NULL, _IONBF, 0); // don't lose lines to buffering
	}
	return t;
}

int64_t read(int fd, void *buf, size_t n) {
	if (fd < 0 || fd >= KFD_MAX) { errno = L_EBADF; return -1; }
	if (io_trace()) fprintf(stderr, "[io] %llu read fd=%d kind=%d nonblock=%d ovl=%d n=%zu\n", (unsigned long long)(GetTickCount64() % 100000), fd, kfd[fd].kind, kfd[fd].nonblock, kfd[fd].ovl, n);
	if (kfd[fd].kind == KFD_SOCKET) {
		if (kfd[fd].reset) return 0; // the EOF that follows a reported reset
		int r = p_recv(kfd_sock(fd), (char *)buf, (int)n, 0);
		if (io_trace()) {
			fprintf(stderr, "[io]   recv -> %d (wsa %d)", r, r == WS_ERROR && p_WSAGetLastError ? p_WSAGetLastError() : 0);
			if (r > 0) { fprintf(stderr, " bytes="); for (int i = 0; i < r && i < 24; i++) fprintf(stderr, "%02x", ((unsigned char *)buf)[i]); }
			fprintf(stderr, "\n");
			if (r == 0) {
				// EOF: report whether every other socket in the table is still a
				// live handle (a closed one answers WSAENOTSOCK to getsockname).
				for (int o = KFD_SOCK_BASE; o < KFD_FOREIGN_BASE; o++) {
					if (kfd[o].kind != KFD_SOCKET) continue;
					struct { uint16_t fam, port; uint32_t a; char z[8]; } la = {0};
					int ll = sizeof la;
					int rc = p_getsockname(kfd[o].sock, &la, &ll);
					fprintf(stderr, "[io]     fd=%d sock=%llu getsockname=%d port=%u\n", o, (unsigned long long)kfd[o].sock, rc, (unsigned)htons(la.port));
				}
			}
		}
		if (r == WS_ERROR) {
			int e = p_WSAGetLastError();
			if (e == WSAECONNRESET || e == WSAECONNABORTED) kfd[fd].reset = 1;
			errno = map_wsa_errno(e);
			return -1;
		}
		return r;
	}
	kfd_classify(fd); // std fds get their kind on first touch
	if (kfd[fd].kind == KFD_CONSOLE) return kcon_read(buf, n, kfd[fd].nonblock);
	if (kfd[fd].kind == KFD_PIPE) {
		HANDLE h = (HANDLE)_get_osfhandle(fd);
		// stdin (fd 0) is served by a buffering reader thread so a read never
		// blocks the loop: never touch the handle here (it would race the
		// thread) — drain the buffer. Spawn it on the first read if select()
		// has not already.
		if (kfd[fd].use_reader) {
			if (!kfd[fd].thr_state) kfd_ensure_sync_reader(fd);
			if (kfd[fd].thr_state) return kfd_thr_read(fd, buf, n, kfd[fd].nonblock);
		}
		// Any other pipe (in-process worker/channel IPC, a child's stdout/stderr
		// read end): the original PeekNamedPipe + non-blocking read path.
		DWORD avail = 0;
		int st = pipe_readable(h, &avail);
		if (st < 0) return 0;               // writer gone: EOF
		if (st == 0 && kfd[fd].nonblock) { errno = L_EAGAIN; return -1; }
	}
	if (kfd[fd].kind == KFD_PIPE) {
		int r = _read(fd, buf, (unsigned)n);
		if (r < 0) return GetLastError() == ERROR_BROKEN_PIPE ? 0 : (errno = L_EBADF, -1);
		return r;
	}
	int r = _read(fd, buf, (unsigned)n);
	if (r < 0) errno = L_EBADF;
	return r;
}

int64_t write(int fd, const void *buf, size_t n) {
	if (fd < 0 || fd >= KFD_MAX) { errno = L_EBADF; return -1; }
	if (kfd[fd].kind == KFD_SOCKET) {
		int r = p_send(kfd_sock(fd), (const char *)buf, (int)n, 0);
		if (io_trace()) fprintf(stderr, "[io] write fd=%d n=%zu -> %d (wsa %d)\n", fd, n, r, r == WS_ERROR && p_WSAGetLastError ? p_WSAGetLastError() : 0);
		return r == WS_ERROR ? set_wsa_errno() : r;
	}
	kfd_classify(fd);
	if (kfd[fd].kind == KFD_PIPE && kfd[fd].ovl) {
		// Overlapped end (the parent's child-stdin side): copy and queue — the
		// user-space write queue Node keeps for a Writable. write() accepts the
		// bytes immediately, the FIFO drains through overlapped completions,
		// and close() flushes — so a parent streaming a large stdin never
		// deadlocks against a child whose stdout it is not yet draining.
		if (kfd_assoc(fd) != 0) { errno = L_EBADF; return -1; }
		kfd_op *op = op_new(fd, OPK_WRITE);
		char *copy = op ? (char *)malloc(n ? n : 1) : NULL;
		if (!copy) { free(op); errno = L_EBADF; return -1; }
		memcpy(copy, buf, n);
		op->buf = copy;
		op->len = (unsigned long)n;
		if (kfd[fd].wq_tail) kfd[fd].wq_tail->next = op; else kfd[fd].wq_head = op;
		kfd[fd].wq_tail = op;
		kfd_wq_pump(fd);
		return (int64_t)n;
	}
	if (kfd[fd].kind == KFD_PIPE) {
		DWORD wr = 0;
		if (!WriteFile((HANDLE)_get_osfhandle(fd), buf, (DWORD)n, &wr, NULL)) {
			DWORD e = GetLastError();
			errno = (e == ERROR_NO_DATA || e == ERROR_BROKEN_PIPE) ? L_EPIPE : L_EBADF;
			return -1;
		}
		return wr;
	}
	int r = _write(fd, buf, (unsigned)n);
	if (r < 0) errno = GetLastError() == ERROR_NO_DATA ? L_EPIPE : L_EBADF;
	return r;
}

int close(int fd) {
	if (fd < 0 || fd >= KFD_MAX) { errno = L_EBADF; return -1; }
	if (io_trace()) fprintf(stderr, "[io] close fd=%d kind=%d\n", fd, kfd[fd].kind);
	if (kfd[fd].kind == KFD_SOCKET) {
		// A socket fd never touches the CRT: closesocket does the whole
		// teardown and the slot simply returns to the pool. A connected
		// socket gets shutdown(SD_SEND) first so the peer sees a FIN: a bare
		// closesocket on Windows can turn into a reset (the Go HTTP client
		// reported "connection forcibly closed" on a Connection: close
		// response), where Linux's close() sends FIN.
		ws_SOCKET s = kfd_sock(fd);
		int inherited = kfd[fd].inherited;
		kfd[fd].kind = KFD_PLAIN;
		kfd[fd].nonblock = 0;
		kfd[fd].reset = 0;
		kfd[fd].inherited = 0;
		kfd[fd].sock = 0;
		if (inherited) {
			// A child holds this socket too: drop this process's handle only.
			return CloseHandle((HANDLE)s) ? 0 : (errno = L_EBADF, -1);
		}
		p_shutdown(s, 1 /* SD_SEND */);
		return p_closesocket(s) == 0 ? 0 : set_wsa_errno();
	}
	if (kfd[fd].kind == KFD_FOREIGN) { errno = L_EBADF; return -1; } // not ours to close
	if (kfd[fd].kind == KFD_PIPE) {
		// Flush queued overlapped writes (the POSIX-parity point: a blocking
		// write to this pipe would have blocked here too), stop the reader
		// thread, and cancel the armed zero-read; its cancellation packet is
		// freed by a later drain, recognized stale by the generation bump.
		if (kfd[fd].ovl && (kfd[fd].wr_op || kfd[fd].wq_head)) kfd_wq_flush(fd);
		if (kfd[fd].thr) kfd_stop_sync_reader(fd);
		if (kfd[fd].rd_op) {
			CancelIoEx((HANDLE)_get_osfhandle(fd), &kfd[fd].rd_op->ov);
			kfd[fd].rd_op = NULL;
		}
	}
	kfd[fd].kind = KFD_PLAIN;
	kfd[fd].nonblock = 0;
	kfd[fd].ovl = 0;
	kfd[fd].assoc = 0;
	kfd[fd].classified = 0;
	kfd[fd].wr_op = NULL;
	kfd[fd].wq_head = kfd[fd].wq_tail = NULL;
	kfd[fd].gen++;
	return _close(fd) == 0 ? 0 : (errno = L_EBADF, -1);
}

int fcntl(int fd, int cmd, ...) {
	if (fd < 0 || fd >= KFD_MAX) { errno = L_EBADF; return -1; }
	if (cmd == L_F_GETFL) return kfd[fd].nonblock ? L_O_NONBLOCK : 0;
	if (cmd == L_F_SETFL) {
		va_list ap;
		va_start(ap, cmd);
		int flags = va_arg(ap, int);
		va_end(ap);
		int nb = (flags & L_O_NONBLOCK) != 0;
		kfd[fd].nonblock = (unsigned char)nb;
		if (io_trace()) fprintf(stderr, "[io] fcntl fd=%d kind=%d nonblock=%d\n", fd, kfd[fd].kind, nb);
		if (kfd[fd].kind == KFD_SOCKET) {
			unsigned long v = (unsigned long)nb;
			if (p_ioctlsocket(kfd_sock(fd), (long)WS_FIONBIO, &v) != 0) return set_wsa_errno();
		}
		// Pipes and the console emulate non-blocking in read() above.
		return 0;
	}
	errno = L_EINVAL;
	return -1;
}

// ---- select over the IR's 1024-bit fd_set bitmap -----------------------------
typedef struct { int64_t sec; int64_t usec; } kml_timeval;

static inline int fd_isset(const unsigned char *set, int fd) {
	return set && (set[fd >> 3] >> (fd & 7)) & 1;
}

// Readiness of a non-socket fd for reading: pipes via PeekNamedPipe, the
// console (and any other waitable handle) via a zero-timeout wait, regular
// files are always ready. Returns 1 ready, 0 not, -1 hangup (readable EOF).
static int plain_readable(int fd) {
	HANDLE h = (HANDLE)_get_osfhandle(fd);
	if (h == INVALID_HANDLE_VALUE) return 0; // closed fd: never ready (POSIX would EBADF)
	DWORD type = GetFileType(h);
	if (type == FILE_TYPE_PIPE) {
		DWORD avail;
		int st = pipe_readable(h, &avail);
		return st < 0 ? -1 : st;
	}
	if (type == FILE_TYPE_CHAR) {
		DWORD mode;
		if (GetConsoleMode(h, &mode)) {
			// A console: WaitForSingleObject signals on any input record,
			// including key-ups a subsequent read would discard; that only
			// costs a spurious EAGAIN, never a lost byte.
			return WaitForSingleObject(h, 0) == WAIT_OBJECT_0;
		}
		return 1; // NUL or another character device: read returns EOF at once
	}
	return 1;
}

static void trace_set(const char *tag, const unsigned char *set, int nfds) {
	if (!set) return;
	fprintf(stderr, " %s[", tag);
	for (int fd = 0; fd < nfds; fd++) if ((set[fd >> 3] >> (fd & 7)) & 1) fprintf(stderr, "%d,", fd);
	fprintf(stderr, "]");
}

// Signal wake-up (win32proc.c, ADR-00728): while a console control handler
// is installed, a blocking wait is taken in slices and a raised flag ends it
// with EINTR, the way a POSIX signal interrupts select().
extern int __kml_win_sig_installed;
int __kml_win_sig_deliver(void);
static int kml_sig_interrupted(void) {
	if (__kml_win_sig_deliver()) { errno = L_EINTR; return 1; }
	return 0;
}

int select(int nfds, unsigned char *rset, unsigned char *wset, unsigned char *eset, kml_timeval *tv) {
	if (nfds > KFD_MAX) nfds = KFD_MAX;
	if (kml_sig_interrupted()) return -1;
	if (io_trace()) {
		fprintf(stderr, "[io] %llu select nfds=%d", (unsigned long long)(GetTickCount64() % 100000), nfds);
		trace_set("r", rset, nfds); trace_set("w", wset, nfds); trace_set("x", eset, nfds);
		fprintf(stderr, " timeout=%lld\n", tv ? (long long)(tv->sec * 1000 + tv->usec / 1000) : -1LL);
	}
	int64_t deadline_ms = -1;
	if (tv) {
		int64_t ms = tv->sec * 1000 + (tv->usec + 999) / 1000;
		deadline_ms = (int64_t)GetTickCount64() + (ms < 0 ? 0 : ms);
	}
	ws_pollfd pfds[KFD_MAX];
	int pfd_fd[KFD_MAX];
	unsigned char rout[128], wout[128], eout[128];
	for (;;) {
		// Reap queued completions/packets first so armed state (rd_op, write
		// queues) is fresh for this pass's probes — including on the socket
		// bridge path, which never blocks on the port.
		if (kml_port) kml_port_drain(0);
		memset(rout, 0, sizeof rout); memset(wout, 0, sizeof wout); memset(eout, 0, sizeof eout);
		int ready = 0, npfd = 0, have_pollable_pipe = 0;
		for (int fd = 0; fd < nfds; fd++) {
			int r = fd_isset(rset, fd), w = fd_isset(wset, fd), x = fd_isset(eset, fd);
			if (!r && !w && !x) continue;
			if (kfd[fd].kind == KFD_SOCKET || kfd[fd].kind == KFD_FOREIGN) {
				pfds[npfd].fd = kfd_sock(fd);
				pfds[npfd].events = (short)((r ? WS_POLLRDNORM : 0) | (w ? WS_POLLWRNORM : 0));
				pfds[npfd].revents = 0;
				pfd_fd[npfd++] = fd;
				continue;
			}
			if (r) {
				// Readiness is answered level-triggered at the source: the
				// console's decoded queue, the stdin reader thread's buffer, or
				// PeekNamedPipe for a plain pipe. stdin/console back their
				// readiness with a thread that wakes the port so the wait blocks;
				// a plain pipe sets have_pollable_pipe so the slice keeps it live.
				kfd_classify(fd);
				int st;
				if (kfd[fd].kind == KFD_CONSOLE) {
					kcon_ensure();
					st = kcon_readable();
				} else if (kfd[fd].kind == KFD_PIPE && kfd[fd].use_reader) {
					// stdin: readiness comes from the buffering reader thread
					// (which wakes the port), never a direct handle probe.
					if (!kfd[fd].thr_state) kfd_ensure_sync_reader(fd);
					st = kfd_thr_readable(fd);
				} else if (kfd[fd].kind == KFD_PIPE) {
					// An in-process / child-stdio anonymous pipe: PeekNamedPipe,
					// with the slice fallback below keeping it responsive (it is
					// not port-armable).
					st = pipe_readable((HANDLE)_get_osfhandle(fd), NULL);
					have_pollable_pipe = 1;
				} else {
					st = plain_readable(fd);
				}
				if (st != 0) { rout[fd >> 3] |= (unsigned char)(1u << (fd & 7)); ready++; }
			}
			if (w) { wout[fd >> 3] |= (unsigned char)(1u << (fd & 7)); ready++; } // pipes/files: writable
		}
		// Wait length. Two things still can't wake the completion port and so
		// need a bounded poll slice: a watched socket (Stage 1 migration bridge
		// — sockets are still on Winsock select, which the port can't interrupt;
		// deleted in Stage 2) and an anonymous pollable pipe (worker/channel IPC
		// and child stdout/stderr — not port-armable). When neither is present,
		// the wait is the port itself: single, blocking, woken by completions
		// and packets (the stdin reader thread, the console-ctrl handler), so
		// only the caller's timer deadline bounds it — the 10 ms idle spin is
		// gone for a pure pipe(reader)/console/stdin program.
		int slice;
		if (deadline_ms < 0) slice = -1; else {
			int64_t left = deadline_ms - (int64_t)GetTickCount64();
			slice = left < 0 ? 0 : (int)(left > 0x7fffffff ? 0x7fffffff : left);
		}
		if (ready) slice = 0;
		else if (have_pollable_pipe && (slice < 0 || slice > 10)) slice = 10;
		else if (npfd > 0 && __kml_win_sig_installed && (slice < 0 || slice > 50)) slice = 50;
		if (npfd > 0) {
			// Winsock select rather than WSAPoll: WSAPoll never reports a failed
			// non-blocking connect (a long-standing Windows defect), so a refused
			// connection would sit until the caller's own timeout. select()
			// reports it in the except set; POSIX callers expect "writable, then
			// SO_ERROR says why", so an excepted socket is reported writable too.
			static ws_big_fd_set sr, sw, sx;
			sr.fd_count = sw.fd_count = sx.fd_count = 0;
			for (int i = 0; i < npfd; i++) {
				if (pfds[i].events & WS_POLLRDNORM) sr.fd_array[sr.fd_count++] = pfds[i].fd;
				if (pfds[i].events & WS_POLLWRNORM) { sw.fd_array[sw.fd_count++] = pfds[i].fd; sx.fd_array[sx.fd_count++] = pfds[i].fd; }
			}
			ws_timeval stv = { slice / 1000, (slice % 1000) * 1000 };
			int pr = p_select(0, sr.fd_count ? &sr : NULL, sw.fd_count ? &sw : NULL, sx.fd_count ? &sx : NULL, slice < 0 ? NULL : &stv);
			if (pr == WS_ERROR) return set_wsa_errno();
			for (int i = 0; i < npfd; i++) {
				int fd = pfd_fd[i];
				ws_SOCKET s = pfds[i].fd;
				int rd = 0, wr = 0, ex = 0;
				for (unsigned int k = 0; k < sr.fd_count; k++) if (sr.fd_array[k] == s) { rd = 1; break; }
				for (unsigned int k = 0; k < sw.fd_count; k++) if (sw.fd_array[k] == s) { wr = 1; break; }
				for (unsigned int k = 0; k < sx.fd_count; k++) if (sx.fd_array[k] == s) { ex = 1; break; }
				if (rd && fd_isset(rset, fd)) { rout[fd >> 3] |= (unsigned char)(1u << (fd & 7)); ready++; }
				if ((wr || ex) && fd_isset(wset, fd)) { wout[fd >> 3] |= (unsigned char)(1u << (fd & 7)); ready++; }
				if (ex && fd_isset(eset, fd)) { eout[fd >> 3] |= (unsigned char)(1u << (fd & 7)); ready++; }
			}
		} else if (!ready && slice != 0) {
			// The reactor's one blocking wait. With a port (main reactor:
			// stdin/console/overlapped), block on it — a completion or a posted
			// wake ends it early. Without one (a worker thread, or before any
			// port source exists), fall back to the old poll slice so a plain
			// pipe stays responsive and an infinite request never hard-blocks.
			if (kml_port) kml_port_drain(slice);
			else Sleep((DWORD)(slice < 0 ? 10 : slice));
		} else if (kml_port) {
			// Returning ready (or polling): reap any queued packets so armed
			// state stays fresh and wake packets never accumulate.
			kml_port_drain(0);
		}
		if (!ready && kml_sig_interrupted()) return -1;
		if (ready || (deadline_ms >= 0 && (int64_t)GetTickCount64() >= deadline_ms)) {
			if (io_trace()) {
				fprintf(stderr, "[io] select ->%d", ready);
				trace_set("r", rout, nfds); trace_set("w", wout, nfds); trace_set("x", eout, nfds);
				fprintf(stderr, "\n");
			}
			if (rset) memcpy(rset, rout, 128);
			if (wset) memcpy(wset, wout, 128);
			if (eset) memcpy(eset, eout, 128);
			return ready;
		}
	}
}

// poll() for the embedded C helpers (http2.c / tls.c wait on one socket).
struct kml_pollfd { int fd; short events; short revents; };
int poll(struct kml_pollfd *fds, unsigned long n, int timeout) {
	unsigned char r[128] = {0}, w[128] = {0};
	int maxfd = 0;
	for (unsigned long i = 0; i < n; i++) {
		int fd = fds[i].fd;
		if (fd < 0 || fd >= KFD_MAX) continue;
		if (fds[i].events & 0x0001) r[fd >> 3] |= (unsigned char)(1u << (fd & 7)); // POLLIN
		if (fds[i].events & 0x0004) w[fd >> 3] |= (unsigned char)(1u << (fd & 7)); // POLLOUT
		if (fd + 1 > maxfd) maxfd = fd + 1;
	}
	kml_timeval tv = { timeout / 1000, (timeout % 1000) * 1000 };
	int rc = select(maxfd, r, w, NULL, timeout < 0 ? NULL : &tv);
	if (rc < 0) return -1;
	int count = 0;
	for (unsigned long i = 0; i < n; i++) {
		int fd = fds[i].fd;
		fds[i].revents = 0;
		if (fd < 0 || fd >= KFD_MAX) continue;
		if (fd_isset(r, fd)) fds[i].revents |= 0x0001;
		if (fd_isset(w, fd)) fds[i].revents |= 0x0004;
		if (fds[i].revents) count++;
	}
	return count;
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
} kml_ucontext;

// A finished fiber, deleted on the next switch. Thread-local: the goroutine
// scheduler (klainsync.c) runs a fiber scheduler on every M thread.
static _Thread_local void *g_fiber_to_delete;

static void ensure_thread_is_fiber(void) {
	if (!IsThreadAFiber()) ConvertThreadToFiber(NULL);
}

static void CALLBACK kml_fiber_tramp(void *arg) {
	kml_ucontext *ctx = (kml_ucontext *)arg;
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
	SwitchToFiber(to->fiber);
	// Back on `from`: reap whatever finished while we were away.
	reap_finished_fiber();
	return 0;
}


// ---- misc ---------------------------------------------------------------------
int usleep(unsigned usec) { Sleep((usec + 999) / 1000); return 0; }

// ---- libcurl fd_set bridge ---------------------------------------------------
// The reactor merges libcurl's transfers into its select() by calling
// curl_multi_fdset with the IR's 128-byte fd bitmaps. On Windows libcurl
// fills *Winsock* fd_sets — { u_int count; SOCKET array[64] }, 516 bytes of
// raw socket handles — so that call would overflow the bitmaps and report
// nothing the bitmap select could use. This definition shadows libcurl's
// export (the linker prefers an object's symbol over the import library),
// calls the real one through the already-loaded DLL, and hands each of
// curl's sockets a stable fd number in the foreign range that select()
// resolves back to the SOCKET. The mapping is rebuilt on every call, so a
// socket curl closed is dropped one reactor iteration later at worst.
typedef struct { unsigned int fd_count; ws_SOCKET fd_array[64]; } ws_fd_set;
static int (*p_curl_multi_fdset)(void *, ws_fd_set *, ws_fd_set *, ws_fd_set *, int *);

static int foreign_fd_for(ws_SOCKET s, unsigned char *seen) {
	int free_fd = -1;
	for (int fd = KFD_FOREIGN_BASE; fd < KFD_MAX; fd++) {
		if (kfd[fd].kind == KFD_FOREIGN && kfd[fd].sock == s) { seen[fd] = 1; return fd; }
		if (kfd[fd].kind != KFD_FOREIGN && free_fd < 0) free_fd = fd;
	}
	if (free_fd < 0) return -1;
	kfd[free_fd].kind = KFD_FOREIGN;
	kfd[free_fd].sock = s;
	seen[free_fd] = 1;
	return free_fd;
}

static void bridge_set(const ws_fd_set *src, unsigned char *dst, int *maxfd, unsigned char *seen) {
	for (unsigned int i = 0; i < src->fd_count && i < 64; i++) {
		int fd = foreign_fd_for(src->fd_array[i], seen);
		if (fd < 0) continue;
		dst[fd >> 3] |= (unsigned char)(1u << (fd & 7));
		if (fd > *maxfd) *maxfd = fd;
	}
}

int curl_multi_fdset(void *multi, unsigned char *rset, unsigned char *wset, unsigned char *eset, int *maxfd) {
	if (!p_curl_multi_fdset) {
		HMODULE m = GetModuleHandleA("libcurl-4.dll");
		if (!m) m = GetModuleHandleA("libcurl.dll");
		if (m) *(FARPROC *)&p_curl_multi_fdset = GetProcAddress(m, "curl_multi_fdset");
		if (!p_curl_multi_fdset) return 1; // CURLM_BAD_HANDLE
	}
	ws_fd_set r = {0}, w = {0}, x = {0};
	int real_max = -1;
	int rc = p_curl_multi_fdset(multi, &r, &w, &x, &real_max);
	if (rc != 0) return rc;
	unsigned char seen[KFD_MAX] = {0};
	int mx = -1;
	if (rset) bridge_set(&r, rset, &mx, seen);
	if (wset) bridge_set(&w, wset, &mx, seen);
	if (eset) bridge_set(&x, eset, &mx, seen);
	for (int fd = KFD_FOREIGN_BASE; fd < KFD_MAX; fd++) {
		if (kfd[fd].kind == KFD_FOREIGN && !seen[fd]) { kfd[fd].kind = KFD_PLAIN; kfd[fd].sock = 0; }
	}
	// Preserve the reactor's "-1 means curl has nothing to wait on" contract.
	if (maxfd) *maxfd = mx;
	return 0;
}
