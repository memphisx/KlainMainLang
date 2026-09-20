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
//     posted wake packets (signals). Sockets are on the port too: zero-byte
//     WSARecv for read-interest, AcceptEx for listeners, ConnectEx for a
//     non-blocking connect, and IOCTL_AFD_POLL for write/except interest and
//     for sockets a library owns (libcurl's) — see the reactor primitives;
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
// Overlapped socket I/O for the IOCP reactor (TDD-00183 Stage 2). WSABUF is
// winsock2.h's { u_long len; char *buf }; defined here since that header is not
// included (it would clash with this file's POSIX-named socket definitions).
typedef struct { unsigned long len; char *buf; } ws_WSABUF;
static int (WINAPI *p_WSARecv)(ws_SOCKET, ws_WSABUF *, unsigned long, unsigned long *, unsigned long *, OVERLAPPED *, void *);
static int (WINAPI *p_WSARecvFrom)(ws_SOCKET, ws_WSABUF *, unsigned long, unsigned long *, unsigned long *, void *, int *, OVERLAPPED *, void *);
static BOOL (WINAPI *p_WSAGetOverlappedResult)(ws_SOCKET, OVERLAPPED *, unsigned long *, BOOL, unsigned long *);
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
	B(ioctlsocket); B(WSAIoctl); B(WSAPoll); B(WSARecv); B(WSARecvFrom); B(WSAGetOverlappedResult); B(select); B(getaddrinfo); B(freeaddrinfo); B(inet_pton);
	B(inet_ntop); B(gethostname); B(htons); B(ntohs);
#undef B
	ws_WSADATA d;
	if (p_WSAStartup) p_WSAStartup(0x0202, &d);
}
__attribute__((constructor)) static void kml_win_io_init(void) { ws_init(); }

// ---- IOCP reactor primitives (TDD-00183 Stage 2) ---------------------------
// Overlapped completion-model readiness for sockets, replacing the Stage-1
// Winsock-select probe. Every mechanism is drained by the one
// GetQueuedCompletionStatusEx, and a completion sets a per-fd ready bit that
// select() projects onto the IR's bitmaps (consumed when reported, cleared by
// the I/O call that uses it up; a still-true condition re-completes at once on
// the next arm, which is what makes the projection level-triggered):
//   * an owned connected stream socket expresses read-interest as a zero-byte
//     WSARecv (libuv's zero-read mode) — completes on data/EOF/reset, consumes
//     nothing; a datagram socket does the same with MSG_PEEK, which completes
//     with "more data" and leaves the datagram queued (libuv's udp zero-read);
//   * a listener's read-interest is a posted AcceptEx into a pre-created
//     socket, which accept() then pops;
//   * a non-blocking stream connect is ConnectEx (bind-first), its outcome
//     recorded per-fd so writability + SO_ERROR report it the POSIX way;
//   * write-interest, the except set, and everything about foreign (libcurl)
//     sockets use IOCTL_AFD_POLL (libuv's poll.c "fast poll"), issued on a
//     per-reactor AFD helper device handle that is associated with the port —
//     the polled socket itself needs no association, so a socket this layer
//     does not own can be watched.
// All are one-shot: re-armed at the next select() entry.

// SIO_BASE_HANDLE unwraps any layered service provider to the base socket AFD
// speaks to; SIO_GET_EXTENSION_FUNCTION_POINTER resolves AcceptEx/ConnectEx.
#define WS_SIO_BASE_HANDLE 0x48000022UL
#define WS_SIO_GET_EXTENSION_FUNCTION_POINTER 0xC8000006UL

// AFD poll: the NT device IOCTL libuv rides for externally-owned-socket
// readiness. IOCTL_AFD_POLL and the AFD_POLL_* event bits are stable, from
// libuv's src/win/winsock.h.
#define IOCTL_AFD_POLL 0x00012024UL
#define AFD_POLL_RECEIVE           0x0001
#define AFD_POLL_RECEIVE_EXPEDITED 0x0002
#define AFD_POLL_SEND              0x0004
#define AFD_POLL_DISCONNECT        0x0008
#define AFD_POLL_ABORT             0x0010
#define AFD_POLL_LOCAL_CLOSE       0x0020
#define AFD_POLL_ACCEPT            0x0080
#define AFD_POLL_CONNECT_FAIL      0x0100

typedef struct {
	LARGE_INTEGER Timeout;
	ULONG NumberOfHandles;
	ULONG Exclusive;
	struct {
		HANDLE Handle;
		ULONG Events;
		LONG Status; // NTSTATUS
	} Handles[1];
} AFD_POLL_INFO;

typedef struct {
	union { LONG Status; void *Pointer; } u; // NTSTATUS / Pointer
	ULONG_PTR Information;
} KML_IO_STATUS_BLOCK;

static LONG (WINAPI *p_NtDeviceIoControlFile)(HANDLE, HANDLE, void *, void *,
	KML_IO_STATUS_BLOCK *, ULONG, void *, ULONG, void *, ULONG);

// Extension functions, resolved once from any socket via WSAIoctl.
static BOOL (WINAPI *p_AcceptEx)(ws_SOCKET, ws_SOCKET, void *, unsigned long,
	unsigned long, unsigned long, unsigned long *, OVERLAPPED *);
static BOOL (WINAPI *p_ConnectEx)(ws_SOCKET, const void *, int, void *,
	unsigned long, unsigned long *, OVERLAPPED *);
static void (WINAPI *p_GetAcceptExSockaddrs)(void *, unsigned long, unsigned long,
	unsigned long, void **, int *, void **, int *);

// NtCreateFile opens the AFD helper device. The native structs are declared
// here rather than via <winternl.h>, which this file otherwise has no use for.
typedef struct { USHORT Length, MaximumLength; wchar_t *Buffer; } kml_ustr;
typedef struct {
	ULONG Length; HANDLE RootDirectory; kml_ustr *ObjectName; ULONG Attributes;
	void *SecurityDescriptor, *SecurityQualityOfService;
} kml_objattr;
static LONG (WINAPI *p_NtQueryInformationFile)(HANDLE, KML_IO_STATUS_BLOCK *, void *, ULONG, int);
static LONG (WINAPI *p_NtCreateFile)(HANDLE *, ULONG, kml_objattr *, KML_IO_STATUS_BLOCK *,
	LARGE_INTEGER *, ULONG, ULONG, ULONG, ULONG, void *, ULONG);

static void afd_ensure_ntdll(void) {
	if (p_NtDeviceIoControlFile) return;
	HMODULE nt = GetModuleHandleA("ntdll.dll");
	if (!nt) return;
	*(FARPROC *)&p_NtCreateFile = GetProcAddress(nt, "NtCreateFile");
	*(FARPROC *)&p_NtQueryInformationFile = GetProcAddress(nt, "NtQueryInformationFile");
	*(FARPROC *)&p_NtDeviceIoControlFile = GetProcAddress(nt, "NtDeviceIoControlFile");
}

#define KML_STATUS_CANCELLED ((LONG)0xC0000120L)

// afd_base_handle: the base socket handle IOCTL_AFD_POLL must target (a layered
// provider's visible handle is not the AFD device). Cached per fd on the slot.
static HANDLE afd_base_handle(ws_SOCKET s) {
	if (!p_WSAIoctl) return (HANDLE)s;
	ws_SOCKET base = s;
	unsigned long got = 0;
	if (p_WSAIoctl(s, WS_SIO_BASE_HANDLE, NULL, 0, &base, sizeof base, &got, NULL, NULL) != 0)
		return (HANDLE)s; // some stacks reject it; the visible handle then is the base
	return (HANDLE)base;
}

// afd_bind_ext: resolve AcceptEx/ConnectEx/GetAcceptExSockaddrs once, via any
// socket (they are provider-specific but identical across AF_INET sockets here).
static void afd_bind_ext(ws_SOCKET s) {
	if (p_AcceptEx && p_ConnectEx) return;
	if (!p_WSAIoctl) return;
	// {GUID, &fnptr} pairs.
	GUID g_accept   = {0xb5367df1,0xcbac,0x11cf,{0x95,0xca,0x00,0x80,0x5f,0x48,0xa1,0x92}};
	GUID g_connect  = {0x25a207b9,0xddf3,0x4660,{0x8e,0xe9,0x76,0xe5,0x8c,0x74,0x06,0x3e}};
	GUID g_getaddrs = {0xb5367df2,0xcbac,0x11cf,{0x95,0xca,0x00,0x80,0x5f,0x48,0xa1,0x92}};
	unsigned long got = 0;
	p_WSAIoctl(s, WS_SIO_GET_EXTENSION_FUNCTION_POINTER, &g_accept, sizeof g_accept, &p_AcceptEx, sizeof p_AcceptEx, &got, NULL, NULL);
	p_WSAIoctl(s, WS_SIO_GET_EXTENSION_FUNCTION_POINTER, &g_connect, sizeof g_connect, &p_ConnectEx, sizeof p_ConnectEx, &got, NULL, NULL);
	p_WSAIoctl(s, WS_SIO_GET_EXTENSION_FUNCTION_POINTER, &g_getaddrs, sizeof g_getaddrs, &p_GetAcceptExSockaddrs, sizeof p_GetAcceptExSockaddrs, &got, NULL, NULL);
}

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
// CancelIoEx'd op still posts one). A pipe op that completed synchronously
// never reaches the port (FILE_SKIP_COMPLETION_PORT_ON_SUCCESS) and is freed
// by its creator; a socket or AFD op always posts a packet, synchronous
// success included, so the drain owns it from the moment it was issued.
typedef struct kfd_op {
	OVERLAPPED ov;           // OPK_AFD_POLL reinterprets ov as the IO_STATUS_BLOCK
	int fd;
	unsigned gen;
	unsigned char kind;      // OPK_*
	char *buf;               // OPK_WRITE: the heap copy of the payload
	unsigned long len;
	struct kfd_op *next;     // write-queue FIFO link
	HANDLE dev;              // OPK_AFD_POLL: the helper device it was issued on (cancel target)
	AFD_POLL_INFO afd;       // OPK_AFD_POLL: the in/out poll buffer (persists until completion)
	ws_SOCKET asock;         // OPK_ACCEPT: the pre-created socket the connection lands in
	char abuf[2 * (128 + 16)]; // OPK_ACCEPT: AcceptEx's local+remote address block
	// Emulated completion (a socket that cannot join this port — see
	// kfd_sock_assoc): the op signals `ev` instead of queueing a packet, and a
	// registered wait forwards it to `port` as one.
	HANDLE ev, wait, port;
} kfd_op;
enum { OPK_ZERO_READ = 1, OPK_WRITE = 2, OPK_AFD_POLL = 3, OPK_ACCEPT = 4, OPK_CONNECT = 5 };

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
	HANDLE port;              // the reactor port to wake (the thread that started this reader)
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
	unsigned char ovl;       // overlapped pipe WRITE end (queued overlapped writes)
	unsigned char ovl_rd;    // overlapped pipe READ end (zero-read readiness, overlapped reads)
	unsigned char ovl_h;     // an inherited overlapped pipe handle: a write is an overlapped WriteFile waited for inline
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
	// ---- socket reactor state (TDD-00183 Stage 2) ----
	unsigned char sock_stream; // KFD_SOCKET: 1 = SOCK_STREAM, 0 = dgram/other (zero-read uses MSG_PEEK)
	unsigned char listening;   // KFD_SOCKET: a listener — read-interest is AcceptEx, not a zero-read
	unsigned char bound;       // bind() succeeded (ConnectEx requires a bound socket)
	unsigned char connecting;  // 1 = ConnectEx pending, 2 = a plain non-blocking connect pending
	unsigned char connected;   // a ConnectEx completed successfully (connect() answers EISCONN)
	unsigned char conn_failed; // a ConnectEx failed: stays writable+excepted, like a POSIX socket
	unsigned char rd_ready, wr_ready, ex_ready; // completion-set readiness, projected by select()
	int family;              // Winsock address family (AF_INET 2 / AF_INET6 23 / …)
	int conn_err;            // the failed connect's errno, handed out once by SO_ERROR
	HANDLE sock_port;        // the port this socket's handle is natively associated with
	unsigned char emul;      // the handle belongs to another port: its ops complete by emulation
	kfd_op *afd_op;          // pending IOCTL_AFD_POLL op (write/except interest; all of a foreign socket's)
	unsigned long afd_armed; // the AFD event mask afd_op was armed with (re-arm on change)
	HANDLE afd_base;         // cached SIO_BASE_HANDLE for AFD poll (lazy)
	kfd_op *acc_op;          // pending AcceptEx
	ws_SOCKET acc_sock;      // a completed AcceptEx's connection, waiting for accept()
	unsigned char acc_have;  // acc_sock holds one
	kfd_op *conn_op;         // pending ConnectEx
	// rd_op (above) doubles as the socket zero-read op.
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
// Clear a slot's socket reactor state (a fresh socket, or a closed one). Pending
// ops are the caller's to cancel first; their packets are recognized stale by
// the generation bump the caller also makes.
static void kfd_sock_reset(int fd) {
	kfd[fd].sock_stream = kfd[fd].listening = kfd[fd].bound = 0;
	kfd[fd].connecting = kfd[fd].connected = kfd[fd].conn_failed = 0;
	kfd[fd].rd_ready = kfd[fd].wr_ready = kfd[fd].ex_ready = 0;
	kfd[fd].family = 0;
	kfd[fd].conn_err = 0;
	kfd[fd].sock_port = NULL;
	kfd[fd].emul = 0;
	kfd[fd].rd_op = kfd[fd].afd_op = kfd[fd].acc_op = kfd[fd].conn_op = NULL;
	kfd[fd].afd_armed = 0;
	kfd[fd].afd_base = NULL;
	kfd[fd].acc_sock = 0;
	kfd[fd].acc_have = 0;
}

int kfd_adopt_socket(HANDLE s, int fd) {
	if (fd < KFD_SOCK_BASE || fd >= KFD_FOREIGN_BASE || kfd[fd].kind != KFD_PLAIN) { errno = L_EINVAL; return -1; }
	kfd[fd].kind = KFD_SOCKET;
	kfd[fd].nonblock = 0;
	kfd[fd].reset = 0;
	kfd[fd].inherited = 0;
	kfd[fd].sock = (ws_SOCKET)s;
	kfd_sock_reset(fd);
	// The socket may arrive already shaped — a listener inherited from a cluster
	// primary, a dup2 alias — so its kind is read off the socket itself rather
	// than from the socket()/bind()/listen() calls this layer never saw.
	if (p_getsockopt) {
		int v = 0, l = sizeof v;
		if (p_getsockopt((ws_SOCKET)s, WS_SOL_SOCKET, 0x1008 /* SO_TYPE */, (char *)&v, &l) == 0) kfd[fd].sock_stream = (v == 1);
		v = 0; l = sizeof v;
		if (p_getsockopt((ws_SOCKET)s, WS_SOL_SOCKET, 0x0002 /* SO_ACCEPTCONN */, (char *)&v, &l) == 0) kfd[fd].listening = (v != 0);
	}
	if (p_getsockname) {
		struct { unsigned short fam; char rest[126]; } sa = {0};
		int l = sizeof sa;
		if (p_getsockname((ws_SOCKET)s, &sa, &l) == 0) { kfd[fd].family = sa.fam; kfd[fd].bound = 1; }
	}
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
// GetQueuedCompletionStatusEx steal a wake meant for another. Every thread
// that calls select() gets one — it is the reactor's only wait. Cross-thread
// wakes are addressed, never broadcast: a reader thread wakes the port of the
// reactor that started it, and a console signal wakes the port of the thread
// that delivers signals. ("The first port created" is not a usable stand-in for
// "the main reactor": a worker spawned at top level can reach its first
// select() before the main loop does.)
static _Thread_local HANDLE kml_port;
static HANDLE kml_sig_port;  // the signal-delivering thread's port (set by its select())
static HANDLE kml_port_get(void) {
	if (!kml_port) kml_port = CreateIoCompletionPort(INVALID_HANDLE_VALUE, NULL, 0, 1);
	return kml_port;
}
static void kml_wake_port(HANDLE port) {
	if (port) PostQueuedCompletionStatus(port, 0, 0 /* KEY_WAKE */, NULL);
}

// The calling thread's reactor port, and a wake for any reactor's port — for
// background sources in other files that must end a specific reactor's wait
// (win32proc.c's child-exit watch).
void *__kml_win_reactor_port(void) { return kml_port_get(); }
void __kml_win_port_wake(void *port) { kml_wake_port((HANDLE)port); }

// __kml_win_loop_wake: a signal was raised (the console-ctrl handler, the
// resize watcher, a self-kill — all in win32proc.c): end the delivering
// thread's wait. Before that thread has ever waited there is nothing to end;
// its first select() checks the raised flag before blocking.
void __kml_win_loop_wake(void) { kml_wake_port(kml_sig_port); }

// The AFD helper device: a handle to \Device\Afd opened for this reactor and
// associated with its port. IOCTL_AFD_POLL is issued on *this* handle with the
// polled socket named inside the request, so the socket itself is never
// associated with anything — which is what lets a libcurl-owned socket, or one
// already bound to another process's port, be watched (libuv's and wepoll's
// arrangement).
static _Thread_local HANDLE kml_afd_dev;
static HANDLE kml_afd_get(void) {
	if (kml_afd_dev) return kml_afd_dev;
	afd_ensure_ntdll();
	if (!p_NtCreateFile || !p_NtDeviceIoControlFile) return NULL;
	static wchar_t name[] = L"\\Device\\Afd\\Kml";
	kml_ustr us = { (USHORT)(sizeof name - sizeof name[0]), (USHORT)sizeof name, name };
	kml_objattr oa = { sizeof oa, NULL, &us, 0, NULL, NULL };
	KML_IO_STATUS_BLOCK iosb;
	HANDLE dev = NULL;
	LONG st = p_NtCreateFile(&dev, SYNCHRONIZE, &oa, &iosb, NULL, 0,
		FILE_SHARE_READ | FILE_SHARE_WRITE, 1 /* FILE_OPEN */, 0, NULL, 0);
	if (st < 0 || !dev) return NULL;
	if (!CreateIoCompletionPort(dev, kml_port_get(), 1 /* KEY_OP */, 0)) { CloseHandle(dev); return NULL; }
	kml_afd_dev = dev;
	return dev;
}

static kfd_op *op_new(int fd, int kind) {
	kfd_op *op = (kfd_op *)calloc(1, sizeof *op);
	if (!op) return NULL;
	op->fd = fd;
	op->gen = kfd[fd].gen;
	op->kind = (unsigned char)kind;
	return op;
}
static void op_free(kfd_op *op) {
	if (op->wait) UnregisterWait(op->wait);
	if (op->ev) CloseHandle(op->ev);
	free(op->buf);
	free(op);
}

// Associate a pipe fd's handle with the port once. FILE_SKIP_COMPLETION_PORT_ON_SUCCESS
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

// ---- socket ops: native or emulated completion -------------------------------
// A socket's overlapped ops complete to the port its handle is associated with,
// and a handle can be associated exactly once, for good — the association
// belongs to the underlying socket object, not to this process's handle. Two
// sockets therefore cannot join this reactor's port: one watched from a second
// thread (already on the first thread's port), and one shared with another
// process that associated it first — a cluster worker's inherited listener, or
// the primary's after a worker got there first. Issuing a plain overlapped op on
// such a socket would queue its packet, carrying *our* OVERLAPPED pointer, to the
// other port. For those the op is issued with an event whose low bit is set —
// which tells the kernel to signal the event and queue nothing — and a
// registered wait forwards the signal to our port as an ordinary packet. This is
// libuv's UV_HANDLE_EMULATE_IOCP arrangement, for the same sockets.
//
// No FILE_SKIP_COMPLETION_PORT_ON_SUCCESS on sockets: a synchronous success
// still queues its packet, so every issued socket op has exactly one owner —
// the drain.
static void CALLBACK kfd_emul_cb(void *arg, BOOLEAN timed_out) {
	(void)timed_out;
	kfd_op *op = (kfd_op *)arg;
	PostQueuedCompletionStatus(op->port, 0, 1 /* KEY_OP */, &op->ov);
}
// 0 = natively on this port, 1 = must emulate, -1 = cannot be watched.
static int kfd_sock_assoc(int fd) {
	HANDLE port = kml_port_get();
	if (!port) return -1;
	if (kfd[fd].sock_port == port) return 0;
	if (kfd[fd].sock_port || kfd[fd].emul) return 1;
	if (CreateIoCompletionPort(kfd_handle(fd), port, 1 /* KEY_OP */, 0)) { kfd[fd].sock_port = port; return 0; }
	if (GetLastError() != ERROR_INVALID_PARAMETER) return -1;
	kfd[fd].emul = 1;
	if (io_trace()) fprintf(stderr, "[io] fd=%d belongs to another port: emulated completion\n", fd);
	return 1;
}
static int op_sock_prepare(kfd_op *op, int fd) {
	int mode = kfd_sock_assoc(fd);
	if (mode <= 0) return mode;
	op->port = kml_port_get();
	op->ev = CreateEventW(NULL, TRUE, FALSE, NULL);
	if (!op->ev) return -1;
	if (!RegisterWaitForSingleObject(&op->wait, op->ev, kfd_emul_cb, op, INFINITE,
	                                 WT_EXECUTEINWAITTHREAD | WT_EXECUTEONLYONCE)) {
		op->wait = NULL;
		return -1;
	}
	op->ov.hEvent = (HANDLE)((ULONG_PTR)op->ev | 1);
	return 0;
}
// The Winsock error a completed socket op failed with (0 = it succeeded).
static int op_wsa_error(int fd, kfd_op *op) {
	unsigned long n = 0, fl = 0;
	if (!p_WSAGetOverlappedResult) return WSAEINVAL;
	if (p_WSAGetOverlappedResult(kfd_sock(fd), &op->ov, &n, FALSE, &fl)) return 0;
	return p_WSAGetLastError ? p_WSAGetLastError() : WSAEINVAL;
}

// Drain the port: turn each completed op into its slot's ready bits (when the
// generation still matches — a stale op is just freed), pump the pipe write
// queue forward, and swallow wake packets. timeout_ms 0 polls; <0 blocks
// (alertable, so queued APCs still run). Returns the number of entries
// dequeued; the caller's signal check supplies the EINTR semantics.
static void kfd_wq_pump(int fd); // below
static int map_wsa_errno(int e); // below
// A bare wake packet was dequeued since select() last looked: some source with
// no fd of its own — a child's exit, a reader thread's data, a signal — wants
// the loop to take another turn. select() answers by returning (see there);
// the flag outlives the drain that saw the packet, because accept()/connect()
// drain the port too and must not swallow a wake meant for the loop.
static _Thread_local int kml_woken;
static int kml_port_drain(int timeout_ms) {
	OVERLAPPED_ENTRY ents[64];
	ULONG got = 0;
	if (!GetQueuedCompletionStatusEx(kml_port_get(), ents, 64, &got,
	                                 timeout_ms < 0 ? INFINITE : (DWORD)timeout_ms, TRUE)) {
		return 0; // timeout / WAIT_IO_COMPLETION: nothing dequeued
	}
	for (ULONG i = 0; i < got; i++) {
		kfd_op *op = (kfd_op *)ents[i].lpOverlapped;
		if (!op) { kml_woken = 1; continue; } // a bare wake packet
		int fd = op->fd;
		int live = fd >= 0 && fd < KFD_MAX && kfd[fd].gen == op->gen;
		// The op's final NTSTATUS. OVERLAPPED.Internal is where the kernel leaves it
		// for an overlapped op, an AFD poll (whose IO_STATUS_BLOCK is overlaid on
		// ov), and an emulated op alike.
		LONG st = (LONG)op->ov.Internal;
		if (io_trace()) fprintf(stderr, "[io] port op fd=%d kind=%d live=%d st=0x%lx\n", fd, op->kind, live, (unsigned long)st);
		kfd_slot *k = live ? &kfd[fd] : NULL;
		switch (op->kind) {
		case OPK_WRITE:
			if (k && k->wr_op == op) { k->wr_op = NULL; kfd_wq_pump(fd); }
			break;
		case OPK_ZERO_READ:
			if (k && k->rd_op == op) {
				k->rd_op = NULL;
				// Data, a datagram ("more data" under MSG_PEEK), EOF and a reset all
				// make the fd readable; only our own cancellation does not.
				if (st != KML_STATUS_CANCELLED) k->rd_ready = 1;
			}
			break;
		case OPK_AFD_POLL:
			if (k && k->afd_op == op) {
				k->afd_op = NULL;
				if (st == 0 && op->afd.NumberOfHandles >= 1) {
					ULONG ev = op->afd.Handles[0].Events;
					if (ev & (AFD_POLL_RECEIVE | AFD_POLL_RECEIVE_EXPEDITED | AFD_POLL_ACCEPT | AFD_POLL_DISCONNECT | AFD_POLL_ABORT | AFD_POLL_LOCAL_CLOSE)) k->rd_ready = 1;
					if (ev & (AFD_POLL_SEND | AFD_POLL_ABORT | AFD_POLL_CONNECT_FAIL)) k->wr_ready = 1;
					// A failed connect: excepted, and — the POSIX shape — readable and
					// writable too, so whichever the caller waits on finds SO_ERROR.
					if (ev & AFD_POLL_CONNECT_FAIL) { k->ex_ready = 1; k->rd_ready = 1; }
					if (k->connecting == 2 && (ev & (AFD_POLL_SEND | AFD_POLL_CONNECT_FAIL | AFD_POLL_ABORT))) k->connecting = 0;
				} else if (st != KML_STATUS_CANCELLED) {
					// The poll itself failed (the socket went away under it): report
					// ready so the owner's next I/O call surfaces the real error.
					k->rd_ready = k->wr_ready = 1;
				}
			}
			break;
		case OPK_ACCEPT:
			if (k && k->acc_op == op) {
				k->acc_op = NULL;
				if (st == 0) {
					k->acc_sock = op->asock;
					k->acc_have = 1;
					k->rd_ready = 1;
					op->asock = 0;
				}
				// A failed AcceptEx (the peer reset before the accept landed) is
				// dropped; the next select() posts a fresh one, as libuv re-queues.
			}
			if (op->asock && p_closesocket) p_closesocket(op->asock);
			break;
		case OPK_CONNECT:
			if (k && k->conn_op == op) {
				k->conn_op = NULL;
				k->connecting = 0;
				if (st == KML_STATUS_CANCELLED) break;
				int e = op_wsa_error(fd, op);
				if (e == 0) {
					// SO_UPDATE_CONNECT_CONTEXT: until it is set a ConnectEx socket
					// refuses getpeername/shutdown.
					if (p_setsockopt) p_setsockopt(kfd_sock(fd), WS_SOL_SOCKET, 0x7010, NULL, 0);
					k->connected = 1;
					k->wr_ready = 1;
				} else {
					k->conn_err = map_wsa_errno(e);
					k->conn_failed = 1;
					k->rd_ready = k->wr_ready = k->ex_ready = 1;
				}
			}
			break;
		}
		op_free(op);
	}
	return (int)got;
}

// ---- AFD poll -------------------------------------------------------------------
// One one-shot poll per socket, issued on the reactor's helper device. It stays
// in flight across select() calls (level-triggered by re-arming: it fires once
// when ready, the drain records the events, the next select() re-arms) and is
// cancelled-and-replaced only when the interest mask changes.
static void kfd_cancel_afd(int fd) {
	kfd_op *op = kfd[fd].afd_op;
	if (op && op->dev) CancelIoEx(op->dev, &op->ov);
	// The cancellation's packet is dequeued and freed by a later drain.
}
static void kfd_arm_afd(int fd, ULONG events) {
	if (kfd[fd].afd_op) {
		if (kfd[fd].afd_armed == events) return;
		kfd_cancel_afd(fd);
		kfd[fd].afd_op = NULL; // orphaned: the drain no longer matches it to the slot
	}
	HANDLE dev = kml_afd_get();
	kfd_op *op = dev ? op_new(fd, OPK_AFD_POLL) : NULL;
	if (!op) {
		// No AFD device (or no memory): readiness cannot be observed, so report
		// it — select() permits spurious readiness, and the caller's I/O call
		// then answers EAGAIN or the real state. Never reached on a stock system.
		kfd[fd].rd_ready = kfd[fd].wr_ready = 1;
		return;
	}
	if (!kfd[fd].afd_base) kfd[fd].afd_base = afd_base_handle(kfd_sock(fd));
	op->dev = dev;
	op->afd.Timeout.QuadPart = 0x7fffffffffffffffLL; // no AFD-side timeout; we cancel to disarm
	op->afd.NumberOfHandles = 1;
	op->afd.Exclusive = 0;
	op->afd.Handles[0].Handle = kfd[fd].afd_base;
	op->afd.Handles[0].Events = events;
	op->afd.Handles[0].Status = 0;
	// ApcContext = op: that value is what the completion packet carries as its
	// OVERLAPPED pointer, and a NULL one means "queue no packet at all".
	LONG st = p_NtDeviceIoControlFile(dev, NULL, NULL, op,
		(KML_IO_STATUS_BLOCK *)&op->ov, IOCTL_AFD_POLL,
		&op->afd, sizeof op->afd, &op->afd, sizeof op->afd);
	// STATUS_PENDING and a synchronous STATUS_SUCCESS both queue a packet.
	if (st >= 0) { kfd[fd].afd_op = op; kfd[fd].afd_armed = events; return; }
	op_free(op);
	kfd[fd].rd_ready = kfd[fd].wr_ready = 1; // a dead socket: let the I/O call say so
}

// ---- zero-read: libuv's read-interest mode ------------------------------------
// A zero-byte WSARecv completes on data / EOF (FIN) / reset without consuming
// anything, so it composes with the recv() in read() and with OpenSSL's socket
// BIO reading the same socket. On a datagram socket the same call carries
// MSG_PEEK: it completes with "more data" when a datagram is queued and leaves
// it there for recvfrom() (without MSG_PEEK the datagram would be consumed by
// the zero-length receive and lost). Returns 0 when read-interest is handled
// here, -1 when the caller must fall back to an AFD receive poll.
static int kfd_arm_zero_read(int fd) {
	if (kfd[fd].rd_op) return 0; // persistent: already armed
	if (!p_WSARecv) return -1;
	kfd_op *op = op_new(fd, OPK_ZERO_READ);
	if (!op) return -1;
	if (op_sock_prepare(op, fd) != 0) { op_free(op); return -1; }
	ws_WSABUF b; b.len = 0; b.buf = NULL;
	unsigned long flags = kfd[fd].sock_stream ? 0 : 0x2 /* MSG_PEEK */, got = 0;
	int r = p_WSARecv(kfd_sock(fd), &b, 1, &got, &flags, &op->ov, NULL);
	int e = r == 0 ? 0 : (p_WSAGetLastError ? p_WSAGetLastError() : 0);
	if (r == 0 || e == ERROR_IO_PENDING /* == WSA_IO_PENDING */ ) { kfd[fd].rd_op = op; return 0; }
	// A synchronous failure queues no packet. Not-yet-connected / not-yet-bound
	// is simply "nothing to read"; anything else (reset, shut down) is a
	// condition the next read() reports, so the fd is readable.
	op_free(op);
	if (e != WSAENOTCONN && e != WSAEINVAL) kfd[fd].rd_ready = 1;
	return 0;
}

// ---- AcceptEx: a listener's read-interest ---------------------------------------
// The connection is accepted by the kernel into a socket created ahead of time;
// the completion makes the listener readable and accept() pops the socket. One
// AcceptEx is kept posted per watched listener. Returns 0 when handled here, -1
// when the caller must fall back to an AFD accept poll (a family AcceptEx does
// not serve).
static int kfd_family(int fd) {
	if (!kfd[fd].family && p_getsockname) {
		struct { unsigned short fam; char rest[126]; } sa = {0};
		int l = sizeof sa;
		if (p_getsockname(kfd_sock(fd), &sa, &l) == 0) kfd[fd].family = sa.fam;
	}
	return kfd[fd].family;
}
static int kfd_arm_accept(int fd) {
	if (kfd[fd].acc_have) { kfd[fd].rd_ready = 1; return 0; }
	if (kfd[fd].acc_op) return 0;
	int fam = kfd_family(fd);
	if (fam != 2 && fam != 23) return -1;
	afd_bind_ext(kfd_sock(fd));
	if (!p_AcceptEx || !p_socket) return -1;
	kfd_op *op = op_new(fd, OPK_ACCEPT);
	if (!op) return -1;
	if (op_sock_prepare(op, fd) != 0) { op_free(op); return -1; }
	op->asock = p_socket(fam, 1 /* SOCK_STREAM */, 0);
	if (op->asock == WS_INVALID) { op->asock = 0; op_free(op); return -1; }
	SetHandleInformation((HANDLE)op->asock, HANDLE_FLAG_INHERIT, 0);
	unsigned long got = 0;
	BOOL ok = p_AcceptEx(kfd_sock(fd), op->asock, op->abuf, 0, 128 + 16, 128 + 16, &got, &op->ov);
	if (ok || (p_WSAGetLastError && p_WSAGetLastError() == ERROR_IO_PENDING)) { kfd[fd].acc_op = op; return 0; }
	p_closesocket(op->asock);
	op_free(op);
	return -1;
}
static void kfd_cancel_sock_ops(int fd) {
	// Every overlapped op this process issued on the socket (zero-read, AcceptEx,
	// ConnectEx); the AFD poll lives on the helper device and is cancelled there.
	if (kfd[fd].rd_op || kfd[fd].acc_op || kfd[fd].conn_op) CancelIoEx((HANDLE)kfd_sock(fd), NULL);
	if (kfd[fd].afd_op) kfd_cancel_afd(fd);
}

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
	// FILE_READ_ATTRIBUTES alongside write access: a child's runtime may query
	// the handle (GetFileType, pipe state) — libuv opens its child ends the same.
	HANDLE c = CreateFileW(name, server_reads ? (GENERIC_WRITE | FILE_READ_ATTRIBUTES) : GENERIC_READ,
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
			kml_wake_port(t->port);
			if (off < n) WaitForSingleObject(t->space, INFINITE); // buffer full: wait for a drain
		}
	}
	EnterCriticalSection(&t->cs);
	t->eof = 1;
	LeaveCriticalSection(&t->cs);
	kml_wake_port(t->port);
	return 0;
}

static void kfd_ensure_sync_reader(int fd) {
	if (kfd[fd].thr || kfd[fd].ovl) return;
	kfd_thr *t = (kfd_thr *)calloc(1, sizeof *t);
	if (!t) return;
	t->fd = fd;
	t->gen = kfd[fd].gen;
	t->h = kfd_handle(fd);
	t->port = kml_port_get(); // this reactor is the one the thread wakes
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
	HANDLE port;             // the reactor port to wake (the thread that first read the console)
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
			kml_wake_port(kcon.port);
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
		kml_wake_port(kcon.port);
		WaitForSingleObject(kcon.consumed, INFINITE);
	}
	EnterCriticalSection(&kcon.cs);
	kcon.eof = 1;
	LeaveCriticalSection(&kcon.cs);
	SetEvent(kcon.data);
	kml_wake_port(kcon.port);
	return 0;
}

static void kcon_ensure(void) {
	if (kcon.started) return;
	kcon.started = 1;
	kcon.port = kml_port_get(); // this reactor is the one the console thread wakes
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
		// A pipe handle this layer did not create (an inherited std fd, a named
		// pipe opened by path): how it must be driven depends on how it was
		// opened, which only the handle knows — libuv asks it the same way. An
		// overlapped handle takes the overlapped paths (zero-read readiness,
		// overlapped reads and writes); a synchronous one is read through a
		// buffering reader thread, so the loop never blocks in a ReadFile.
		afd_ensure_ntdll();
		ULONG mode = 0;
		KML_IO_STATUS_BLOCK iosb;
		int is_sync = 1;
		if (p_NtQueryInformationFile &&
		    p_NtQueryInformationFile(h, &iosb, &mode, sizeof mode, 16 /* FileModeInformation */) >= 0)
			is_sync = (mode & (0x10 /* FILE_SYNCHRONOUS_IO_ALERT */ | 0x20 /* FILE_SYNCHRONOUS_IO_NONALERT */)) != 0;
		if (is_sync) kfd[fd].use_reader = 1;
		else kfd[fd].ovl_h = kfd[fd].ovl_rd = 1;
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
	kfd[fd].sock_stream = (type == 1); // SOCK_STREAM
	kfd[fd].family = domain;
	return fd;
}

int bind(int fd, const void *addr, int len) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	if (p_bind(kfd_sock(fd), addr, len) != 0) return set_wsa_errno();
	kfd[fd].bound = 1;
	return 0;
}
int listen(int fd, int backlog) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	if (p_listen(kfd_sock(fd), backlog) != 0) return set_wsa_errno();
	kfd[fd].listening = 1; // read-interest is a posted AcceptEx, not a zero-read
	return 0;
}

// accept(): a connection the posted AcceptEx already took is popped first —
// it arrived before anything still sitting in the backlog, so this keeps
// connections in arrival order. With no AcceptEx outstanding (the listener was
// never watched, or the caller is draining a burst after popping one) the
// backlog is read directly.
int accept(int fd, void *addr, int *len) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	ws_SOCKET s = WS_INVALID;
	kfd[fd].rd_ready = 0;
	for (;;) {
		if (kml_port) kml_port_drain(0); // a completed AcceptEx may be waiting on the port
		if (kfd[fd].acc_have) {
			s = kfd[fd].acc_sock;
			kfd[fd].acc_have = 0;
			kfd[fd].acc_sock = 0;
			// SO_UPDATE_ACCEPT_CONTEXT: the socket inherits the listener's
			// properties and becomes usable with getpeername/shutdown/setsockopt.
			ws_SOCKET ls = kfd_sock(fd);
			p_setsockopt(s, WS_SOL_SOCKET, 0x700B, (const char *)&ls, sizeof ls);
			if (addr && len) p_getpeername(s, addr, len);
			break;
		}
		if (!kfd[fd].acc_op) {
			s = p_accept(kfd_sock(fd), addr, len);
			if (s == WS_INVALID) return set_wsa_errno();
			break;
		}
		// An AcceptEx is outstanding, so the backlog is empty by construction:
		// whatever arrives next lands in it, not in the backlog.
		if (kfd[fd].nonblock) { errno = L_EAGAIN; return -1; }
		kml_port_drain(-1);
	}
	SetHandleInformation((HANDLE)s, HANDLE_FLAG_INHERIT, 0);
	int nfd = kfd_register((HANDLE)s, KFD_SOCKET);
	if (io_trace()) {
		struct { uint16_t fam, port; uint32_t addr; char z[8]; } pa = {0}, la = {0};
		int pl = sizeof pa, ll = sizeof la;
		p_getpeername(s, &pa, &pl);
		p_getsockname(s, &la, &ll);
		fprintf(stderr, "[io] accept fd=%d -> fd=%d sock=%llu peer=%u local=%u\n", fd, nfd, (unsigned long long)s, (unsigned)htons(pa.port), (unsigned)htons(la.port));
	}
	if (nfd < 0) { p_closesocket(s); return nfd; }
	kfd[nfd].connected = 1;
	return nfd;
}

// A non-blocking stream connect is ConnectEx: the socket is bound first (the
// call requires it; a wildcard bind is what connect() does implicitly), the
// request is issued overlapped, and its completion — success or the failure's
// error — is recorded on the slot by the drain. The caller sees the POSIX
// shape: EINPROGRESS now, writability when it is decided, then SO_ERROR (or a
// second connect() answering EISCONN) to learn which way. Returns -2 when
// ConnectEx does not apply and the plain connect path should run.
static int kfd_connectex(int fd, const void *addr, int len) {
	if (!kfd[fd].nonblock || !kfd[fd].sock_stream || !addr || len < 2) return -2;
	int fam = *(const unsigned short *)addr;
	if ((fam != 2 && fam != 23) || fam != kfd[fd].family) return -2;
	afd_bind_ext(kfd_sock(fd));
	if (!p_ConnectEx) return -2;
	if (!kfd[fd].bound) {
		struct { unsigned short fam; char rest[26]; } any = {0};
		any.fam = (unsigned short)fam;
		if (p_bind(kfd_sock(fd), &any, fam == 2 ? 16 : 28) != 0) return set_wsa_errno();
		kfd[fd].bound = 1;
	}
	kfd_op *op = op_new(fd, OPK_CONNECT);
	if (!op) { errno = L_ENOMEM; return -1; }
	if (op_sock_prepare(op, fd) != 0) { op_free(op); return -2; }
	kfd[fd].conn_failed = 0;
	kfd[fd].conn_err = 0;
	kfd[fd].wr_ready = kfd[fd].ex_ready = 0;
	BOOL ok = p_ConnectEx(kfd_sock(fd), addr, len, NULL, 0, NULL, &op->ov);
	int e = ok ? 0 : p_WSAGetLastError();
	if (io_trace()) fprintf(stderr, "[io] connect fd=%d ConnectEx -> %d (wsa %d)\n", fd, (int)ok, e);
	if (ok || e == ERROR_IO_PENDING) {
		// An immediate success still queues its packet; the drain finishes the
		// connect either way, so both report "in progress".
		kfd[fd].conn_op = op;
		kfd[fd].connecting = 1;
		errno = L_EINPROGRESS;
		return -1;
	}
	op_free(op);
	errno = map_wsa_errno(e);
	return -1;
}

int connect(int fd, const void *addr, int len) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	if (kfd[fd].conn_op) {
		// Give a just-completed ConnectEx the chance to be seen before answering.
		if (kml_port) kml_port_drain(0);
		if (kfd[fd].conn_op) { errno = L_EALREADY; return -1; }
	}
	if (kfd[fd].connected && kfd[fd].sock_stream) { errno = L_EISCONN; return -1; }
	int cx = kfd_connectex(fd, addr, len);
	if (cx != -2) return cx;
	if (p_connect(kfd_sock(fd), addr, len) == 0) {
		if (io_trace()) {
			struct { uint16_t fam, port; uint32_t a; char z[8]; } la = {0};
			int ll = sizeof la;
			p_getsockname(kfd_sock(fd), &la, &ll);
			fprintf(stderr, "[io] connect fd=%d sock=%llu -> 0 local=%u\n", fd, (unsigned long long)kfd_sock(fd), (unsigned)htons(la.port));
		}
		kfd[fd].bound = 1;
		if (kfd[fd].sock_stream) kfd[fd].connected = 1;
		return 0;
	}
	int e = p_WSAGetLastError();
	if (io_trace()) fprintf(stderr, "[io] connect fd=%d -> -1 (wsa %d)\n", fd, e);
	// A non-blocking connect reports WOULDBLOCK on Windows; POSIX callers
	// expect EINPROGRESS and then wait for writability.
	if (e == WSAEWOULDBLOCK) { kfd[fd].connecting = 2; kfd[fd].bound = 1; errno = L_EINPROGRESS; return -1; }
	errno = map_wsa_errno(e);
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
	if (level == WS_SOL_SOCKET && opt == WS_SO_ERROR && val && len && *len >= 4) {
		// A ConnectEx failure is reported through its completion, not through the
		// socket's own SO_ERROR; the drain recorded it. Read-and-clear, as POSIX.
		if (kfd[fd].conn_op && kml_port) kml_port_drain(0);
		if (kfd[fd].conn_err) {
			*(int *)val = kfd[fd].conn_err;
			*len = 4;
			kfd[fd].conn_err = 0;
			return 0;
		}
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
	kfd[fd].rd_ready = 0; // consumed; the next select() re-arms and re-learns it
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
// pipe(): fds[0] is an overlapped read end, fds[1] a synchronous write end.
// Every pipe() in the emitted IR has the same shape — the read end stays with a
// reactor (the loop watching a child's stdout/stderr, a worker or channel
// reading its inbox, execSync collecting output) and the write end goes to
// whoever produces the bytes: another thread, or a child process that inherits
// it. So the read end is the overlapped one: select() arms a zero-byte
// overlapped ReadFile on the port (libuv's zero-read mode for pipes) and the
// reactor blocks until the writer writes — no polling. The write end is
// synchronous because a child's CRT writes it with a plain WriteFile, which is
// undefined on an overlapped handle; an in-process writer thread is served
// equally well by a blocking WriteFile into the 64 KiB pipe buffer. (The
// opposite shape, a child that *reads*, is __kml_win_pipe_pw below.)
int pipe(int fds[2]) {
	HANDLE r, w;
	if (kml_pipe_pair(&r, &w, 1 /* server reads */, 1 /* client sync */) != 0) { errno = L_EMFILE; return -1; }
	int rfd = kfd_register(r, KFD_PIPE);
	if (rfd < 0) { CloseHandle(r); CloseHandle(w); return -1; }
	int wfd = kfd_register(w, KFD_PIPE);
	if (wfd < 0) { close(rfd); CloseHandle(w); return -1; }
	kfd[rfd].ovl_rd = 1;
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
	kfd[newfd].ovl_rd = kfd[oldfd].ovl_rd;
	kfd[newfd].ovl_h = kfd[oldfd].ovl_h;
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

// ---- overlapped pipe read ends (pipe()'s fds[0]) -------------------------------
// Read-interest is a zero-byte overlapped ReadFile on the port: it completes
// when the writer writes (or at once, over already-buffered bytes) and when the
// last writer closes. The completion sets rd_ready — readiness is taken from
// the completion itself, never re-derived by probing the pipe afterwards.
// Returns 0 when armed (or decided on the spot), -1 when the handle cannot be
// watched and the caller must fall back to the poll slice.
static int kfd_arm_pipe_zero_read(int fd) {
	if (kfd[fd].rd_op || kfd[fd].rd_ready) return 0;
	kfd_op *op = op_new(fd, OPK_ZERO_READ);
	if (!op) return -1;
	if (op_sock_prepare(op, fd) != 0) { op_free(op); return -1; }
	DWORD got = 0;
	if (ReadFile(kfd_handle(fd), op->abuf, 0, &got, &op->ov) || GetLastError() == ERROR_IO_PENDING) {
		kfd[fd].rd_op = op; // a synchronous success queues its packet too
		return 0;
	}
	// Broken pipe (every writer gone) or any other hard failure: readable — the
	// read() that follows reports end-of-file.
	op_free(op);
	kfd[fd].rd_ready = 1;
	return 0;
}

// read() on an overlapped read end. Non-blocking: take what is buffered, or
// EAGAIN. Blocking: wait for the first bytes (a byte pipe's ReadFile returns as
// soon as any arrive). The event's low bit keeps these reads' completions off
// the port — they are waited for right here.
static int64_t kfd_ovl_pipe_read(int fd, void *buf, size_t n) {
	HANDLE h = kfd_handle(fd);
	int hinted = kfd[fd].rd_ready; // a completion said "bytes or EOF"
	kfd[fd].rd_ready = 0;
	int cancel_if_pending = 0;
	if (kfd[fd].nonblock) {
		DWORD avail = 0;
		int st = pipe_readable(h, &avail);
		if (st < 0) return 0; // every writer gone and the buffer drained: EOF
		if (st > 0) { if (n > avail) n = avail; }
		else if (!hinted) { errno = L_EAGAIN; return -1; }
		else cancel_if_pending = 1; // trust the completion over the probe: try the read itself
	}
	if (!kfd[fd].rd_ev) kfd[fd].rd_ev = CreateEventW(NULL, TRUE, FALSE, NULL);
	if (!kfd[fd].rd_ev) { errno = L_ENOMEM; return -1; }
	OVERLAPPED ov;
	memset(&ov, 0, sizeof ov);
	ov.hEvent = (HANDLE)((ULONG_PTR)kfd[fd].rd_ev | 1);
	DWORD got = 0, e = 0;
	if (!ReadFile(h, buf, (DWORD)n, &got, &ov)) {
		e = GetLastError();
		if (e == ERROR_IO_PENDING) {
			if (cancel_if_pending) CancelIoEx(h, &ov);
			e = GetOverlappedResult(h, &ov, &got, TRUE) ? 0 : GetLastError();
		}
	}
	if (e == ERROR_OPERATION_ABORTED && got == 0) { errno = L_EAGAIN; return -1; }
	if (e == ERROR_BROKEN_PIPE || e == ERROR_HANDLE_EOF || e == ERROR_PIPE_NOT_CONNECTED) return 0;
	if (e && got == 0) { errno = L_EBADF; return -1; }
	return got;
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
		kfd[fd].rd_ready = 0; // consumed; the next select() re-arms and re-learns it
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
		// A read end this layer created (or inherited) overlapped: never the
		// CRT's _read, which issues a ReadFile with no OVERLAPPED.
		if (kfd[fd].ovl_rd) return kfd_ovl_pipe_read(fd, buf, n);
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
		if (r == WS_ERROR) {
			set_wsa_errno();
			if (errno == L_EAGAIN) kfd[fd].wr_ready = 0; // the send buffer filled: no longer writable
			return -1;
		}
		return r;
	}
	kfd_classify(fd);
	if (kfd[fd].kind == KFD_PIPE && kfd[fd].ovl_h && !kfd[fd].ovl) {
		// An overlapped handle this layer did not create (an inherited std fd):
		// every op on it needs an OVERLAPPED, so write it overlapped and wait
		// right here — the blocking-write semantics the fd has always had. The
		// event's low bit keeps the completion off any port.
		HANDLE h = (HANDLE)_get_osfhandle(fd);
		if (!kfd[fd].wr_ev) kfd[fd].wr_ev = CreateEventW(NULL, TRUE, FALSE, NULL);
		if (!kfd[fd].wr_ev) { errno = L_ENOMEM; return -1; }
		OVERLAPPED ov;
		memset(&ov, 0, sizeof ov);
		ov.hEvent = (HANDLE)((ULONG_PTR)kfd[fd].wr_ev | 1);
		DWORD wr = 0, e = 0;
		if (!WriteFile(h, buf, (DWORD)n, &wr, &ov)) {
			e = GetLastError();
			if (e == ERROR_IO_PENDING) e = GetOverlappedResult(h, &ov, &wr, TRUE) ? 0 : GetLastError();
		}
		if (e) { errno = (e == ERROR_NO_DATA || e == ERROR_BROKEN_PIPE) ? L_EPIPE : L_EBADF; return -1; }
		return wr;
	}
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
		// Cancel every pending op and bump the generation: their packets still
		// arrive (a cancelled op completes too) and the drain, finding the
		// generation moved on, frees them without touching the slot's next
		// tenant. A connection AcceptEx took that accept() never popped is closed
		// with the listener — nobody else holds it.
		kfd_cancel_sock_ops(fd);
		if (kfd[fd].acc_have && p_closesocket) p_closesocket(kfd[fd].acc_sock);
		kfd_sock_reset(fd);
		kfd[fd].assoc = 0;
		kfd[fd].gen++;
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
		// Graceful-close drain: shutdown(SD_SEND) sends our queued data + FIN, but
		// closesocket() still aborts the connection with an RST (discarding that
		// queued data) if the receive buffer holds bytes the peer sent that we
		// never read — e.g. an HTTP server answering (431/413/redirect/clientError)
		// while the client is still uploading a body. Linux's close() delivers the
		// response in that case; to match, drain what the peer already sent so no
		// unread input forces the RST, then closesocket() delivers our response.
		//
		// Only pay for this when there IS unread data: a first non-blocking recv
		// that returns EWOULDBLOCK (the common case — we consumed the whole
		// request, or it is an idle keep-alive close) skips the loop entirely, so
		// normal closes add just one recv and no latency. When data is present the
		// peer may still be mid-send (its writes unblock as we drain), so we ride
		// out the in-flight remainder with short readability waits, stopping once
		// the peer goes quiet — bounded in bytes and total wait so a peer that
		// never stops cannot hang the close (past the cap we accept the RST, as any
		// bounded server must).
		{
			unsigned long nb = 1;
			if (p_ioctlsocket) p_ioctlsocket(s, WS_FIONBIO, &nb); // never block the drain
			char drainbuf[65536];
			// One non-blocking probe: only unread data (r > 0) triggers the drain,
			// so the common close (r <= 0, EWOULDBLOCK/FIN) adds a single recv and
			// no latency.
			int r = p_recv(s, drainbuf, (int)sizeof drainbuf, 0);
			if (r > 0) {
				long budget = 32 * 1024 * 1024; // byte cap
				int idleWaits = 0;              // consecutive quiet waits before giving up
				for (;;) {
					if (r > 0) {
						budget -= r; idleWaits = 0;
						if (budget <= 0) break; // hostile flood: accept the RST
					} else if (r == 0) {
						break;                  // peer FIN — fully drained
					} else {
						// EWOULDBLOCK: the peer may still be mid-send (its writes
						// unblock as we drain). Wait briefly; give up once it has been
						// quiet long enough that the buffer is empty at close time.
						int e = p_WSAGetLastError ? p_WSAGetLastError() : 0;
						if (e != WSAEWOULDBLOCK) break;
						ws_big_fd_set rf; rf.fd_count = 1; rf.fd_array[0] = s;
						ws_timeval tv; tv.tv_sec = 0; tv.tv_usec = 20000; // 20ms
						if (!p_select || p_select(0, &rf, NULL, NULL, &tv) <= 0) {
							if (++idleWaits >= 5) break; // ~100ms quiet: peer done
						}
					}
					r = p_recv(s, drainbuf, (int)sizeof drainbuf, 0);
				}
			}
		}
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
	kfd[fd].ovl = kfd[fd].ovl_rd = kfd[fd].ovl_h = 0;
	kfd[fd].use_reader = 0;
	kfd[fd].rd_ready = 0;
	kfd[fd].sock_port = NULL;
	kfd[fd].emul = 0;
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
extern volatile long __kml_win_sig_wake; // raised by the console-ctrl handler
int __kml_win_sig_deliver(void);
int __kml_win_on_sig_thread(void);
// A raised-but-undelivered signal is pending on the delivering thread: its wake
// packet may already have been drained (a bare wake the loop-top drain
// discards), so a blocking wait must not be entered — checked before the wait
// so the flag is delivered right after. Gated to the signal-owning thread so a
// worker's event loop never busy-polls the global flag it cannot deliver.
static int kml_sig_pending(void) { return __kml_win_sig_wake && __kml_win_on_sig_thread(); }
static int kml_sig_interrupted(void) {
	if (__kml_win_sig_deliver()) { errno = L_EINTR; return 1; }
	return 0;
}

// Arm whatever completion source answers the interest in one socket fd that is
// not already answered by a ready bit (see the primitives near the top).
static void kfd_arm_interest(int fd, int r, int w, int x) {
	kfd_slot *k = &kfd[fd];
	ULONG mask = 0;
	const ULONG rd_events = AFD_POLL_RECEIVE | AFD_POLL_ACCEPT | AFD_POLL_DISCONNECT | AFD_POLL_ABORT;
	if (k->kind == KFD_FOREIGN) {
		// Not ours: no overlapped op may be issued on it. AFD poll both ways.
		if (r && !k->rd_ready) mask |= rd_events | AFD_POLL_CONNECT_FAIL;
	} else {
		// A failed connect stays failed: readable, writable and excepted for as
		// long as anyone asks, the way a POSIX socket holds its error state.
		if (k->conn_failed) k->rd_ready = k->wr_ready = k->ex_ready = 1;
		if (r && !k->rd_ready) {
			int via_afd = 0;
			if (k->listening) via_afd = kfd_arm_accept(fd) != 0;
			else if (!k->connecting) via_afd = kfd_arm_zero_read(fd) != 0;
			if (via_afd) mask |= rd_events;
		}
	}
	// Write/except interest. A pending ConnectEx answers both by completing.
	if ((w || x) && k->connecting != 1 && !(w && k->wr_ready) && !(x && k->ex_ready))
		mask |= (w ? AFD_POLL_SEND : 0) | AFD_POLL_CONNECT_FAIL | AFD_POLL_ABORT;
	if (mask) kfd_arm_afd(fd, mask);
}

int select(int nfds, unsigned char *rset, unsigned char *wset, unsigned char *eset, kml_timeval *tv) {
	if (nfds > KFD_MAX) nfds = KFD_MAX;
	if (nfds < 0) nfds = 0;
	kml_port_get(); // the reactor's one wait; every selecting thread owns a port
	if (__kml_win_on_sig_thread()) kml_sig_port = kml_port;
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
	static _Thread_local int sock_fd[KFD_MAX];
	unsigned char rout[128], wout[128], eout[128];
	for (;;) {
		// Reap queued completions/packets first so ready bits and armed state
		// (rd_op, write queues) are fresh for this pass.
		while (kml_port_drain(0) == 64) {}
		memset(rout, 0, sizeof rout); memset(wout, 0, sizeof wout); memset(eout, 0, sizeof eout);
		int ready = 0, nsock = 0, have_pollable_pipe = 0;
		for (int fd = 0; fd < nfds; fd++) {
			int r = fd_isset(rset, fd), w = fd_isset(wset, fd), x = fd_isset(eset, fd);
			if (!r && !w && !x) continue;
			if (kfd[fd].kind == KFD_SOCKET || kfd[fd].kind == KFD_FOREIGN) {
				sock_fd[nsock++] = fd;
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
				} else if (kfd[fd].kind == KFD_PIPE && kfd[fd].ovl_rd) {
					// An overlapped read end (every pipe() this layer makes): bytes
					// already buffered answer at once; otherwise a zero-read on the
					// port ends the wait the moment the writer writes or closes.
					st = kfd[fd].rd_ready ? 1 : pipe_readable((HANDLE)_get_osfhandle(fd), NULL);
					if (st == 0 && kfd_arm_pipe_zero_read(fd) != 0) have_pollable_pipe = 1;
					if (st == 0 && kfd[fd].rd_ready) st = 1; // decided while arming (broken pipe)
				} else if (kfd[fd].kind == KFD_PIPE) {
					// A pipe handle that can be neither port-armed nor given a
					// reader thread: PeekNamedPipe, re-probed on the slice below.
					st = pipe_readable((HANDLE)_get_osfhandle(fd), NULL);
					have_pollable_pipe = 1;
				} else {
					st = plain_readable(fd);
				}
				if (st != 0) { rout[fd >> 3] |= (unsigned char)(1u << (fd & 7)); ready++; }
			}
			if (w) { wout[fd >> 3] |= (unsigned char)(1u << (fd & 7)); ready++; } // pipes/files: writable
		}
		// Sockets: arm each interest that no ready bit answers yet, pick up what
		// completed on the spot (a zero-read over queued data, a poll of an
		// already-writable socket), then project the ready bits.
		if (nsock) {
			for (int i = 0; i < nsock; i++) {
				int fd = sock_fd[i];
				kfd_arm_interest(fd, fd_isset(rset, fd), fd_isset(wset, fd), fd_isset(eset, fd));
			}
			while (kml_port_drain(0) == 64) {}
			for (int i = 0; i < nsock; i++) {
				int fd = sock_fd[i];
				// "Excepted ⇒ also writable": POSIX callers wait for writability and
				// then ask SO_ERROR why.
				if (fd_isset(rset, fd) && kfd[fd].rd_ready) { rout[fd >> 3] |= (unsigned char)(1u << (fd & 7)); ready++; }
				if (fd_isset(wset, fd) && (kfd[fd].wr_ready || kfd[fd].ex_ready)) { wout[fd >> 3] |= (unsigned char)(1u << (fd & 7)); ready++; }
				if (fd_isset(eset, fd) && kfd[fd].ex_ready) { eout[fd >> 3] |= (unsigned char)(1u << (fd & 7)); ready++; }
			}
		}
		// A wake with nothing ready ends the call with 0, exactly as a timeout
		// would: the caller's loop takes its turn (reaps the child, reads the
		// reader thread's buffer on its next pass) and comes back. Blocking again
		// here instead would strand a wake whose source has no fd in the sets.
		int woken = kml_woken;
		kml_woken = 0;
		if (ready || woken || (deadline_ms >= 0 && (int64_t)GetTickCount64() >= deadline_ms)) {
			// Reported readiness is consumed: the next select() re-arms, and a
			// condition that still holds completes again at once.
			for (int i = 0; i < nsock; i++) {
				int fd = sock_fd[i];
				if (rout[fd >> 3] & (1u << (fd & 7))) kfd[fd].rd_ready = 0;
				if (wout[fd >> 3] & (1u << (fd & 7))) kfd[fd].wr_ready = 0;
				if (eout[fd >> 3] & (1u << (fd & 7))) kfd[fd].ex_ready = 0;
			}
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
		// The reactor's one blocking wait: the port, ended by a completion, a
		// reader-thread packet or a signal's wake packet — bounded only by the
		// caller's deadline. The single exception is an anonymous pollable pipe
		// (worker/channel IPC, a child's stdout/stderr), which cannot post to a
		// port and so caps the wait at a 10 ms re-probe.
		int slice;
		if (deadline_ms < 0) slice = -1; else {
			int64_t left = deadline_ms - (int64_t)GetTickCount64();
			slice = left < 0 ? 0 : (int)(left > 0x7fffffff ? 0x7fffffff : left);
		}
		if (have_pollable_pipe && (slice < 0 || slice > 10)) slice = 10;
		// A signal raised before the wait: its wake packet may already have been
		// swallowed by a drain above, so don't block — deliver it now.
		if (kml_sig_pending()) slice = 0;
		// One line per blocking wait: the count of these against elapsed time is
		// what shows a reactor that polls (many short waits) from one that waits.
		if (io_trace() && slice != 0) fprintf(stderr, "[io] wait slice=%d\n", slice);
		if (slice != 0) kml_port_drain(slice);
		if (kml_sig_interrupted()) return -1;
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

static void kfd_cancel_sock_ops_foreign(int fd) {
	if (kfd[fd].afd_op) kfd_cancel_afd(fd);
	kfd_sock_reset(fd);
	kfd[fd].gen++;
}

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
		if (kfd[fd].kind == KFD_FOREIGN && !seen[fd]) {
			// curl is done with this socket: disarm its poll and retire the slot, so a
			// late completion is recognized stale and never marks the next tenant.
			kfd_cancel_sock_ops_foreign(fd);
			kfd[fd].kind = KFD_PLAIN;
			kfd[fd].sock = 0;
		}
	}
	// Preserve the reactor's "-1 means curl has nothing to wait on" contract.
	if (maxfd) *maxfd = mx;
	return 0;
}
