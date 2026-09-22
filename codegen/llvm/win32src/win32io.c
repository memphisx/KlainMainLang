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
// Socket hand-off between processes (cluster round-robin). The descriptor is
// winsock2.h's WSAPROTOCOL_INFOW (628 bytes), opaque here.
typedef struct { char opaque[628]; } ws_PROTOCOL_INFOW;
static int (WINAPI *p_WSADuplicateSocketW)(ws_SOCKET, unsigned long, ws_PROTOCOL_INFOW *);
static ws_SOCKET (WINAPI *p_WSASocketW)(int, int, int, ws_PROTOCOL_INFOW *, unsigned int, unsigned long);
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
	B(ioctlsocket); B(WSAIoctl); B(WSAPoll); B(WSADuplicateSocketW); B(WSASocketW); B(WSARecv); B(WSARecvFrom); B(WSAGetOverlappedResult); B(select); B(getaddrinfo); B(freeaddrinfo); B(inet_pton);
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
// An fd is an index into a table of pointers to refcounted *descriptions*
// (TDD-00183 Stage 3) — the POSIX open file description: the OS handle, its
// `kind` tag (which read/write/close/select/fstat all dispatch on), its flags
// and its overlapped/reactor state. dup2() makes two fds share one description
// (refs++), close() drops a reference, and the handle is torn down exactly once,
// when the last fd referring to it closes. Readiness, the non-blocking flag and
// pending ops belong to the description, so they are seen through every alias.
//
// Two fd ranges share the IR's 1024-slot bitmap:
//   [0, 512)      CRT fds: regular files and the inherited stdio — the CRT owns
//                 the handle (_get_osfhandle). A plain CRT fd has no pooled
//                 description; its few flags live in a per-fd record. A slot
//                 here can also *hold* a description: a std fd classified as a
//                 pipe or the console, or the target of a dup2().
//   [512, 1024)   the owned pool: every handle this layer owns — sockets, pipe
//                 ends, and the foreign (libcurl) sockets select() must wait on —
//                 allocates from it, first free slot. The CRT never sees these
//                 handles (a CRT fd wrapped around a socket would CloseHandle it
//                 behind Winsock's back on _close, and a pipe end needs none of
//                 the CRT's services). The ceiling is the IR's 1024-bit fd_set.
// Foreign-ness is the description's kind (KFD_FOREIGN), never the fd's number.
enum { KFD_PLAIN = 0, KFD_SOCKET = 1, KFD_PIPE = 2, KFD_FOREIGN = 3, KFD_CONSOLE = 4 };
#define KFD_POOL_BASE 512

// A pending overlapped operation (TDD-00183 Stage 1). Heap-allocated so a
// completion dequeued after the fd closed (and the slot was reused) is
// recognized by generation mismatch and simply freed. Ownership rule: an op
// that went pending is freed by the port drain when its packet arrives (a
// CancelIoEx'd op still posts one). A pipe op that completed synchronously
// never reaches the port (FILE_SKIP_COMPLETION_PORT_ON_SUCCESS) and is freed
// by its creator; a socket or AFD op always posts a packet, synchronous
// success included, so the drain owns it from the moment it was issued.
struct kfd_desc;
typedef struct kfd_op {
	OVERLAPPED ov;           // OPK_AFD_POLL reinterprets ov as the IO_STATUS_BLOCK
	struct kfd_desc *d;      // the description it was issued on (pooled: never freed, so always safe to read)
	int fd;                  // the fd it was issued through — tracing only; an alias may outlive it
	unsigned gen;            // d's generation at issue; a mismatch marks the op stale
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
	struct kfd_desc *d;
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

typedef struct kfd_desc {
	ws_SOCKET sock;          // socket/foreign kinds: the Winsock SOCKET; else 0
	HANDLE h;                // an owned pipe end's handle (NULL when the CRT holds it — see crt_fd)
	int refs;                // fds referring to this description (pooled descriptions only)
	int crt_fd;              // >= 0: a classified std fd — the CRT owns the handle and does its I/O; else -1
	unsigned long owner_tid;  // KFD_FOREIGN: the thread whose libcurl multi handle reported the socket
	struct kfd_desc *next_free; // pool free list
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
	// ---- cluster round-robin (a listener shared with re-spawned workers) ----
	struct kfd_rr *rr;       // primary: the workers this listener's connections rotate over
	int rr_chan;             // worker: the fd connections arrive on from the primary (0 = none)
	unsigned char npipe;     // KFD_PIPE: a named pipe reached through the socket API (net.connect({ path }))
	unsigned char npipe_eof; // its handle was released by shutdown(): reads are end-of-file, writes EPIPE
	// rd_op (above) doubles as the socket zero-read op.
} kfd_desc;
// The fd table. kfd_tab[fd] points at the pooled description an fd refers to;
// NULL means the fd is a plain CRT fd (or closed), whose flags live in
// kfd_plain[fd]. Pooled descriptions are recycled through a free list and never
// returned to the heap, so a completion or reader thread that outlives its fd
// can always read the description it was issued on and compare generations.
static kfd_desc *kfd_tab[KFD_MAX];
static kfd_desc kfd_plain[KFD_MAX];
static kfd_desc kfd_pool[KFD_MAX];
static kfd_desc *kfd_free_list;
static int kfd_pool_used;      // high-water mark into kfd_pool
// Slot and pool allocation is the one table mutation worker threads race on
// (each opens its own sockets and pipes); I/O on an fd stays with its owner.
static SRWLOCK kfd_lock = SRWLOCK_INIT;
#define KD(fd) (kfd_tab[fd] ? kfd_tab[fd] : &kfd_plain[fd])

// Cross-file accessors (win32proc.c, win32fs.c). Functions rather than exported
// arrays so the description layout can grow across reactor stages without other
// objects rebuilding against a fixed struct. kfd_kind_of returns -1 for an
// out-of-range fd (never a valid KFD_* value).
int kfd_kind_of(int fd) { return (fd < 0 || fd >= KFD_MAX) ? -1 : KD(fd)->kind; }
void kfd_set_inherited(int fd) { if (fd >= 0 && fd < KFD_MAX) KD(fd)->inherited = 1; }

// Take a description from the pool, cleared except for the two fields that must
// survive recycling: the generation (stale ops compare against it) and the lazy
// inline-wait events. Caller holds kfd_lock.
static kfd_desc *kfd_desc_new(int kind) {
	kfd_desc *d = kfd_free_list;
	if (d) kfd_free_list = d->next_free;
	else if (kfd_pool_used < KFD_MAX) d = &kfd_pool[kfd_pool_used++];
	else return NULL;
	unsigned gen = d->gen;
	HANDLE rd_ev = d->rd_ev, wr_ev = d->wr_ev;
	memset(d, 0, sizeof *d);
	d->gen = gen;
	d->rd_ev = rd_ev;
	d->wr_ev = wr_ev;
	d->kind = (unsigned char)kind;
	d->refs = 1;
	d->crt_fd = -1;
	return d;
}
// Retire a description whose last fd closed: the generation bump is what turns
// every op and reader thread still holding it into a recognized stale one.
static void kfd_desc_retire(kfd_desc *d) {
	AcquireSRWLockExclusive(&kfd_lock);
	d->gen++;
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
		if (p_getsockname((ws_SOCKET)s, &sa, &l) == 0) { KD(fd)->family = sa.fam; KD(fd)->bound = 1; }
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
// The handle behind a pooled description (the drain reaches it through an op,
// when the fd the op was issued through may already be closed).
static HANDLE kfd_desc_handle(kfd_desc *d) {
	if (d->kind == KFD_SOCKET || d->kind == KFD_FOREIGN) return (HANDLE)d->sock;
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

// ---- high-resolution timing (TDD-00183 Stage 4) --------------------------------
// The reactor's wait is bounded by a deadline, but GetQueuedCompletionStatusEx's
// millisecond timeout inherits the system timer resolution (~15.6 ms by default),
// so a 50 ms setInterval fired only every ~62 ms and the deadline itself, taken
// from GetTickCount64, was quantised the same way. Deadlines now come from
// QueryPerformanceCounter (microseconds), and the wait is ended at the real
// deadline by a per-thread high-resolution waitable timer whose expiry is posted
// to the thread's port as a completion packet (see kml_port_wait_until).
// Pre-1803 Windows without the high-resolution flag falls back to a plain timer,
// and a machine without the API at all falls back to the old coarse millisecond
// wait.
static int kml_port_drain(int timeout_ms); // defined below; the timer helpers wait through it
#ifndef CREATE_WAITABLE_TIMER_HIGH_RESOLUTION
#define CREATE_WAITABLE_TIMER_HIGH_RESOLUTION 0x00000002
#endif
#ifndef TIMER_ALL_ACCESS
#define TIMER_ALL_ACCESS 0x1F0003
#endif
static HANDLE (WINAPI *p_CreateWaitableTimerExW)(void *, const wchar_t *, unsigned long, unsigned long);
static int (WINAPI *p_SetWaitableTimer)(HANDLE, const LARGE_INTEGER *, long, void *, void *, int);
static int (WINAPI *p_CancelWaitableTimer)(HANDLE);
// Wait completion packets (Windows 8+): the kernel posts a packet to the port
// when the associated object — here the timer — is signalled.
static LONG (WINAPI *p_NtCreateWaitCompletionPacket)(HANDLE *, ULONG, void *);
static LONG (WINAPI *p_NtAssociateWaitCompletionPacket)(HANDLE, HANDLE, HANDLE, void *, void *, LONG, ULONG_PTR, BOOLEAN *);
static LONG (WINAPI *p_NtCancelWaitCompletionPacket)(HANDLE, BOOLEAN);
#define KML_KEY_TIMER 2 // completion key of a timer-expiry packet (0 = wake, 1 = op)
static void kml_timer_load(void) {
	// Any thread may get here first (a worker can reach its first wait alongside
	// the main loop), so the flag is published only after the pointers: a racing
	// thread either resolves the same values itself or sees them all set.
	static volatile LONG done;
	if (done) return;
	HMODULE k = GetModuleHandleW(L"kernel32.dll");
	if (!k) { done = 1; return; }
	p_CreateWaitableTimerExW = (HANDLE (WINAPI *)(void *, const wchar_t *, unsigned long, unsigned long))(void *)GetProcAddress(k, "CreateWaitableTimerExW");
	p_SetWaitableTimer = (int (WINAPI *)(HANDLE, const LARGE_INTEGER *, long, void *, void *, int))(void *)GetProcAddress(k, "SetWaitableTimer");
	p_CancelWaitableTimer = (int (WINAPI *)(HANDLE))(void *)GetProcAddress(k, "CancelWaitableTimer");
	HMODULE nt = GetModuleHandleW(L"ntdll.dll");
	if (nt) {
		*(FARPROC *)&p_NtCreateWaitCompletionPacket = GetProcAddress(nt, "NtCreateWaitCompletionPacket");
		*(FARPROC *)&p_NtAssociateWaitCompletionPacket = GetProcAddress(nt, "NtAssociateWaitCompletionPacket");
		*(FARPROC *)&p_NtCancelWaitCompletionPacket = GetProcAddress(nt, "NtCancelWaitCompletionPacket");
	}
	MemoryBarrier();
	done = 1;
}
static _Thread_local HANDLE kml_hrtimer;
static _Thread_local int kml_hrtimer_tried;
static HANDLE kml_hrtimer_get(void) {
	if (kml_hrtimer || kml_hrtimer_tried) return kml_hrtimer;
	kml_hrtimer_tried = 1;
	kml_timer_load();
	if (p_CreateWaitableTimerExW) {
		kml_hrtimer = p_CreateWaitableTimerExW(NULL, NULL, CREATE_WAITABLE_TIMER_HIGH_RESOLUTION, TIMER_ALL_ACCESS);
		if (!kml_hrtimer) kml_hrtimer = p_CreateWaitableTimerExW(NULL, NULL, 0, TIMER_ALL_ACCESS);
	}
	return kml_hrtimer;
}
static void CALLBACK kml_timer_apc(void *arg, DWORD lo, DWORD hi) { (void)arg; (void)lo; (void)hi; }

// Monotonic microseconds from QPC, computed to avoid the int64 overflow a plain
// counter*1e6 would hit after a few days of uptime.
static int64_t kml_now_us(void) {
	static LARGE_INTEGER freq;
	if (!freq.QuadPart) QueryPerformanceFrequency(&freq);
	LARGE_INTEGER c;
	QueryPerformanceCounter(&c);
	int64_t q = freq.QuadPart ? freq.QuadPart : 1;
	return (c.QuadPart / q) * 1000000 + (c.QuadPart % q) * 1000000 / q;
}

// The thread's wait completion packet, binding its timer to its port.
static _Thread_local HANDLE kml_hrpacket;
static _Thread_local int kml_hrpacket_tried;
static HANDLE kml_hrpacket_get(void) {
	if (kml_hrpacket || kml_hrpacket_tried) return kml_hrpacket;
	kml_hrpacket_tried = 1;
	if (p_NtCreateWaitCompletionPacket && p_NtAssociateWaitCompletionPacket && p_NtCancelWaitCompletionPacket) {
		HANDLE h = NULL;
		if (p_NtCreateWaitCompletionPacket(&h, GENERIC_ALL, NULL) >= 0) kml_hrpacket = h;
	}
	return kml_hrpacket;
}

// Block on the port until deadline_us (absolute QPC microseconds; < 0 = forever),
// waking at the real deadline. The timer's expiry reaches the port as a
// completion packet (KML_KEY_TIMER, which the drain dequeues and ignores): a
// timer APC also ends the alertable wait, but its delivery is quantised to the
// system tick, which is the very thing being fixed — it is only the fallback
// where wait completion packets are missing (pre-Windows 8). One wait per call.
static void kml_port_wait_until(int64_t deadline_us) {
	if (deadline_us < 0) { kml_port_drain(-1); return; }
	int64_t rem = deadline_us - kml_now_us();
	if (rem <= 0) return; // deadline already passed: do not block
	HANDLE t = kml_hrtimer_get();
	if (t && p_SetWaitableTimer) {
		LARGE_INTEGER due;
		due.QuadPart = -(rem * 10); // relative, 100 ns units
		HANDLE pk = kml_hrpacket_get();
		if (pk && p_SetWaitableTimer(t, &due, 0, NULL, NULL, 0)) {
			BOOLEAN already = 0;
			LONG st = p_NtAssociateWaitCompletionPacket(pk, kml_port_get(), t, (void *)(ULONG_PTR)KML_KEY_TIMER, NULL, 0, 0, &already);
			if (st >= 0) {
				if (!already) kml_port_drain(-1); // a packet — I/O, wake or the timer's — ends it
				if (p_CancelWaitableTimer) p_CancelWaitableTimer(t);
				// Disarm, removing an expiry packet that is queued but not yet
				// dequeued, so the packet is free to be associated again.
				p_NtCancelWaitCompletionPacket(pk, 1);
				return;
			}
			if (p_CancelWaitableTimer) p_CancelWaitableTimer(t);
		}
		if (p_SetWaitableTimer(t, &due, 0, (void *)kml_timer_apc, NULL, 0)) {
			kml_port_drain(-1); // INFINITE + alertable: a packet or the timer APC ends it
			if (p_CancelWaitableTimer) p_CancelWaitableTimer(t);
			return;
		}
	}
	int64_t ms = (rem + 999) / 1000; // no high-res timer: the old coarse wait
	kml_port_drain(ms > 0x7fffffff ? 0x7fffffff : (int)ms);
}

// A precise sleep on the calling thread, shared by usleep()/nanosleep() — the
// last Sleep-slice remnants (TDD-00183 Stage 4).
void __kml_win_hr_sleep_us(int64_t us) {
	if (us <= 0) return;
	HANDLE t = kml_hrtimer_get();
	if (t && p_SetWaitableTimer) {
		LARGE_INTEGER due;
		due.QuadPart = -(us * 10);
		if (p_SetWaitableTimer(t, &due, 0, NULL, NULL, 0)) {
			WaitForSingleObject(t, INFINITE);
			return;
		}
	}
	Sleep((DWORD)((us + 999) / 1000));
}

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

// Ops are OS-heap memory, never the program's allocator.
//
// An op is memory *the kernel owns* while its operation is in flight: the
// OVERLAPPED it completes through, and for an AFD poll the result buffer it
// writes into. Nothing in this process need reference it meanwhile — an op is
// deliberately orphaned from its description when the interest is re-armed or
// the fd closed, and is reclaimed only when its (possibly cancelled) completion
// is drained.
//
// Under `-mm=gc` a plain malloc here would come from Boehm, like every other
// malloc in the link, and a conservative collector frees what it cannot see a
// pointer to. An orphaned in-flight op is exactly that: its only holder is the
// kernel, which the collector does not scan — nothing stops the block being
// recycled under a live operation, leaving the kernel to write a completion
// into another object. (Not the cause of any observed failure; found while
// tracking down the fiber-stack scanning bug below, and closed here because
// the window is real and the fix costs nothing.)
//
// Kernel-owned memory therefore comes from the process heap directly, which no
// collector manages and no GC-mode link redefines — the same reasoning as
// ADR-00351's GC_base guard, one level
// earlier: memory that crosses into the kernel never belongs to the collector
// in the first place. Payload buffers handed to WriteFile (`op->buf`) are the
// kernel's for the same duration and come from the same place.
static void *op_alloc(size_t n) {
	return HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY, n);
}
static void op_release(void *p) {
	if (p) HeapFree(GetProcessHeap(), 0, p);
}

static kfd_op *op_new(int fd, int kind) {
	kfd_op *op = (kfd_op *)op_alloc(sizeof *op);
	if (!op) return NULL;
	op->d = KD(fd);
	op->fd = fd;
	op->gen = op->d->gen;
	op->kind = (unsigned char)kind;
	return op;
}
// The one place an op is released — every path goes through it, so a queued
// write's payload and an emulated op's wait/event are never left behind.
static void op_free(kfd_op *op) {
	if (op->wait) UnregisterWait(op->wait);
	if (op->ev) CloseHandle(op->ev);
	op_release(op->buf);
	op_release(op);
}

// Associate a pipe fd's handle with the port once. FILE_SKIP_COMPLETION_PORT_ON_SUCCESS
// keeps synchronously-completing ops (data already buffered) from flooding the
// port with packets the drain would only discard.
static int kfd_assoc(int fd) {
	if (KD(fd)->assoc) return 0;
	HANDLE h = kfd_handle(fd);
	if (h == INVALID_HANDLE_VALUE) return -1;
	if (!CreateIoCompletionPort(h, kml_port_get(), 1 /* KEY_OP */, 0)) return -1;
	SetFileCompletionNotificationModes(h, FILE_SKIP_COMPLETION_PORT_ON_SUCCESS);
	KD(fd)->assoc = 1;
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
	if (KD(fd)->sock_port == port) return 0;
	if (KD(fd)->sock_port || KD(fd)->emul) return 1;
	if (CreateIoCompletionPort(kfd_handle(fd), port, 1 /* KEY_OP */, 0)) { KD(fd)->sock_port = port; return 0; }
	if (GetLastError() != ERROR_INVALID_PARAMETER) return -1;
	KD(fd)->emul = 1;
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
static int op_wsa_error(kfd_desc *d, kfd_op *op) {
	unsigned long n = 0, fl = 0;
	if (!p_WSAGetOverlappedResult) return WSAEINVAL;
	if (p_WSAGetOverlappedResult(d->sock, &op->ov, &n, FALSE, &fl)) return 0;
	return p_WSAGetLastError ? p_WSAGetLastError() : WSAEINVAL;
}

// Drain the port: turn each completed op into its slot's ready bits (when the
// generation still matches — a stale op is just freed), pump the pipe write
// queue forward, and swallow wake packets. timeout_ms 0 polls; <0 blocks
// (alertable, so queued APCs still run). Returns the number of entries
// dequeued; the caller's signal check supplies the EINTR semantics.
static void kfd_wq_pump(kfd_desc *d); // below
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
		if (!op) {
			// A timer-expiry packet only ends the wait; a bare wake packet is a
			// cross-thread wake the loop must see.
			if (ents[i].lpCompletionKey != KML_KEY_TIMER) kml_woken = 1;
			continue;
		}
		int fd = op->fd;
		int live = op->d->gen == op->gen;
		// The op's final NTSTATUS. OVERLAPPED.Internal is where the kernel leaves it
		// for an overlapped op, an AFD poll (whose IO_STATUS_BLOCK is overlaid on
		// ov), and an emulated op alike.
		LONG st = (LONG)op->ov.Internal;
		if (io_trace()) fprintf(stderr, "[io] port op fd=%d kind=%d live=%d st=0x%lx foreign=%d\n", fd, op->kind, live, (unsigned long)st, live && op->d->kind == KFD_FOREIGN);
		kfd_desc *k = live ? op->d : NULL;
		switch (op->kind) {
		case OPK_WRITE:
			if (k && k->wr_op == op) { k->wr_op = NULL; kfd_wq_pump(k); }
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
				int e = op_wsa_error(k, op);
				if (e == 0) {
					// SO_UPDATE_CONNECT_CONTEXT: until it is set a ConnectEx socket
					// refuses getpeername/shutdown.
					if (p_setsockopt) p_setsockopt(k->sock, WS_SOL_SOCKET, 0x7010, NULL, 0);
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
	kfd_op *op = KD(fd)->afd_op;
	if (op && op->dev) CancelIoEx(op->dev, &op->ov);
	// The cancellation's packet is dequeued and freed by a later drain.
}
static void kfd_arm_afd(int fd, ULONG events) {
	if (KD(fd)->afd_op) {
		if (KD(fd)->afd_armed == events) return;
		kfd_cancel_afd(fd);
		KD(fd)->afd_op = NULL; // orphaned: the drain no longer matches it to the slot
	}
	HANDLE dev = kml_afd_get();
	kfd_op *op = dev ? op_new(fd, OPK_AFD_POLL) : NULL;
	if (!op) {
		// No AFD device (or no memory): readiness cannot be observed, so report
		// it — select() permits spurious readiness, and the caller's I/O call
		// then answers EAGAIN or the real state. Never reached on a stock system.
		KD(fd)->rd_ready = KD(fd)->wr_ready = 1;
		return;
	}
	if (!KD(fd)->afd_base) KD(fd)->afd_base = afd_base_handle(kfd_sock(fd));
	op->dev = dev;
	op->afd.Timeout.QuadPart = 0x7fffffffffffffffLL; // no AFD-side timeout; we cancel to disarm
	op->afd.NumberOfHandles = 1;
	op->afd.Exclusive = 0;
	op->afd.Handles[0].Handle = KD(fd)->afd_base;
	op->afd.Handles[0].Events = events;
	op->afd.Handles[0].Status = 0;
	// ApcContext = op: that value is what the completion packet carries as its
	// OVERLAPPED pointer, and a NULL one means "queue no packet at all".
	LONG st = p_NtDeviceIoControlFile(dev, NULL, NULL, op,
		(KML_IO_STATUS_BLOCK *)&op->ov, IOCTL_AFD_POLL,
		&op->afd, sizeof op->afd, &op->afd, sizeof op->afd);
	// STATUS_PENDING and a synchronous STATUS_SUCCESS both queue a packet.
	if (st >= 0) { KD(fd)->afd_op = op; KD(fd)->afd_armed = events; return; }
	op_free(op);
	KD(fd)->rd_ready = KD(fd)->wr_ready = 1; // a dead socket: let the I/O call say so
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
	if (KD(fd)->rd_op) return 0; // persistent: already armed
	if (!p_WSARecv) return -1;
	kfd_op *op = op_new(fd, OPK_ZERO_READ);
	if (!op) return -1;
	if (op_sock_prepare(op, fd) != 0) { op_free(op); return -1; }
	ws_WSABUF b; b.len = 0; b.buf = NULL;
	unsigned long flags = KD(fd)->sock_stream ? 0 : 0x2 /* MSG_PEEK */, got = 0;
	int r = p_WSARecv(kfd_sock(fd), &b, 1, &got, &flags, &op->ov, NULL);
	int e = r == 0 ? 0 : (p_WSAGetLastError ? p_WSAGetLastError() : 0);
	if (r == 0 || e == ERROR_IO_PENDING /* == WSA_IO_PENDING */ ) { KD(fd)->rd_op = op; return 0; }
	// A synchronous failure queues no packet. Not-yet-connected / not-yet-bound
	// is simply "nothing to read"; anything else (reset, shut down) is a
	// condition the next read() reports, so the fd is readable.
	op_free(op);
	if (e != WSAENOTCONN && e != WSAEINVAL) KD(fd)->rd_ready = 1;
	return 0;
}

// ---- AcceptEx: a listener's read-interest ---------------------------------------
// The connection is accepted by the kernel into a socket created ahead of time;
// the completion makes the listener readable and accept() pops the socket. One
// AcceptEx is kept posted per watched listener. Returns 0 when handled here, -1
// when the caller must fall back to an AFD accept poll (a family AcceptEx does
// not serve).
static int kfd_family(int fd) {
	if (!KD(fd)->family && p_getsockname) {
		struct { unsigned short fam; char rest[126]; } sa = {0};
		int l = sizeof sa;
		if (p_getsockname(kfd_sock(fd), &sa, &l) == 0) KD(fd)->family = sa.fam;
	}
	return KD(fd)->family;
}
static int kfd_arm_accept(int fd) {
	if (KD(fd)->acc_have) { KD(fd)->rd_ready = 1; return 0; }
	if (KD(fd)->acc_op) return 0;
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
	if (ok || (p_WSAGetLastError && p_WSAGetLastError() == ERROR_IO_PENDING)) { KD(fd)->acc_op = op; return 0; }
	if (io_trace()) fprintf(stderr, "[io] AcceptEx fd=%d failed wsa=%d\n", fd, p_WSAGetLastError ? p_WSAGetLastError() : -1);
	p_closesocket(op->asock);
	op_free(op);
	return -1;
}
static void kfd_cancel_sock_ops(int fd) {
	// Every overlapped op this process issued on the socket (zero-read, AcceptEx,
	// ConnectEx); the AFD poll lives on the helper device and is cancelled there.
	if (KD(fd)->rd_op || KD(fd)->acc_op || KD(fd)->conn_op) CancelIoEx((HANDLE)kfd_sock(fd), NULL);
	if (KD(fd)->afd_op) kfd_cancel_afd(fd);
}

// Queued overlapped writes: post the head of the FIFO if nothing is in
// flight. A synchronous completion pumps the next entry immediately; a
// pending one is finished (and the queue pumped) by the port drain.
static void kfd_wq_pump(kfd_desc *d) {
	while (!d->wr_op && d->wq_head) {
		kfd_op *op = d->wq_head;
		d->wq_head = op->next;
		if (!d->wq_head) d->wq_tail = NULL;
		op->next = NULL;
		DWORD n = 0;
		if (!WriteFile(kfd_desc_handle(d), op->buf, op->len, &n, &op->ov)) {
			if (GetLastError() == ERROR_IO_PENDING) { d->wr_op = op; return; }
		} else if (d->npipe) {
			// A duplex handle keeps the default notification mode (its read side
			// counts on a packet for every completion), so a write that finished
			// synchronously still has its packet coming: the drain owns the op.
			d->wr_op = op;
			return;
		}
		// Done at once, or a broken pipe: drop the payload, as a POSIX write
		// would EPIPE — the reader is gone; nothing can observe the bytes either way.
		op_free(op);
	}
}

// Wait until fd's queued writes fully drained (close/flush path).
static void kfd_wq_flush(int fd) {
	while (KD(fd)->wr_op || KD(fd)->wq_head) {
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
		if (t->stop || t->d->gen != t->gen) break;
		DWORD n = 0;
		if (!ReadFile(t->h, chunk, sizeof chunk, &n, NULL) || n == 0) break; // EOF / broken pipe
		if (t->stop || t->d->gen != t->gen) break;
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
	if (KD(fd)->thr || KD(fd)->ovl) return;
	kfd_thr *t = (kfd_thr *)calloc(1, sizeof *t);
	if (!t) return;
	t->d = KD(fd);
	t->gen = KD(fd)->gen;
	t->h = kfd_handle(fd);
	t->port = kml_port_get(); // this reactor is the one the thread wakes
	InitializeCriticalSection(&t->cs);
	t->space = CreateEventW(NULL, FALSE, FALSE, NULL);
	HANDLE h = CreateThread(NULL, 0, kfd_sync_pipe_thread, t, 0, NULL);
	if (!h) { DeleteCriticalSection(&t->cs); CloseHandle(t->space); free(t); return; }
	KD(fd)->thr = h;
	KD(fd)->thr_state = t;
}

// Readable/EOF state of a sync-reader-backed pipe (level-triggered probe).
static int kfd_thr_readable(int fd) {
	kfd_thr *t = KD(fd)->thr_state;
	if (!t) return 0;
	EnterCriticalSection(&t->cs);
	int r = t->len > 0 ? 1 : (t->eof ? -1 : 0);
	LeaveCriticalSection(&t->cs);
	return r;
}

// Drain up to n bytes from the reader thread's buffer; 0 = EOF, -1/EAGAIN =
// nothing yet (only when nonblocking — a blocking read waits for data).
static int64_t kfd_thr_read(int fd, void *buf, size_t n, int nonblock) {
	kfd_thr *t = KD(fd)->thr_state;
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
	kfd_thr *t = KD(fd)->thr_state;
	if (!t) return;
	InterlockedExchange(&t->stop, 1);
	SetEvent(t->space);
	// The thread may be blocked in the kernel read; break it, re-issuing until
	// the join lands (CancelSynchronousIo misses a thread between calls).
	while (WaitForSingleObject(KD(fd)->thr, 50) == WAIT_TIMEOUT) CancelSynchronousIo(KD(fd)->thr);
	CloseHandle(KD(fd)->thr);
	DeleteCriticalSection(&t->cs);
	CloseHandle(t->space);
	free(t);
	KD(fd)->thr = NULL;
	KD(fd)->thr_state = NULL;
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
// console fd 0 becomes KFD_CONSOLE (the reader-thread ReadConsoleW path). Either
// way the fd is promoted to a pooled description — it now has reactor state an
// op or a reader thread can refer to — that leaves the handle with the CRT
// (crt_fd): the CRT keeps doing the fd's plain reads and writes and closes it.
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
	DWORD type = GetFileType(h);
	if (type == FILE_TYPE_PIPE) {
		if (kfd_promote_crt(fd, KFD_PIPE) != 0) return;
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
		if (is_sync) KD(fd)->use_reader = 1;
		else KD(fd)->ovl_h = KD(fd)->ovl_rd = 1;
	} else if (type == FILE_TYPE_CHAR) {
		DWORD mode;
		if (fd == 0 && GetConsoleMode(h, &mode)) kfd_promote_crt(fd, KFD_CONSOLE);
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
	KD(fd)->sock_stream = (type == 1); // SOCK_STREAM
	KD(fd)->family = domain;
	return fd;
}

int bind(int fd, const void *addr, int len) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	if (p_bind(kfd_sock(fd), addr, len) != 0) return set_wsa_errno();
	KD(fd)->bound = 1;
	return 0;
}
int listen(int fd, int backlog) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	if (p_listen(kfd_sock(fd), backlog) != 0) return set_wsa_errno();
	KD(fd)->listening = 1; // read-interest is a posted AcceptEx, not a zero-read
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
	if (!d->rr) d->rr = (kfd_rr *)calloc(1, sizeof(kfd_rr));
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

// Primary: give connection s to whoever's turn it is. 1 = a worker has it (s is
// closed here), 0 = it is this process's. A worker whose channel is gone leaves
// the rotation; one whose channel is full is skipped for this connection.
static int kfd_rr_dispatch(kfd_desc *d, ws_SOCKET s) {
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
				if (io_trace()) fprintf(stderr, "[io] rr sock=%llu -> pid %lu\n", (unsigned long long)s, rr->w[slot - 1].pid);
				p_closesocket(s);
				return 1;
			}
			dead = 1;
		}
		if (dead) {
			if (io_trace()) fprintf(stderr, "[io] rr worker pid %lu left the rotation\n", rr->w[slot - 1].pid);
			rr->w[slot - 1].chan = 0;
			close(chan);
		}
	}
	return 0;
}
// Worker: the next connection the primary sent. 1 = *out is it, 0 = none yet,
// -1 = the primary is gone (the caller falls back to accepting for itself).
static int kfd_rr_recv(int fd, ws_SOCKET *out) {
	int chan = KD(fd)->rr_chan;
	ws_SOCKET cs = kfd_sock(chan);
	ws_PROTOCOL_INFOW info;
	KD(chan)->rd_ready = 0;
	for (;;) {
		int n = p_recv(cs, (char *)&info, (int)sizeof info, 2 /* MSG_PEEK */);
		if (n < 0 && p_WSAGetLastError() == 10035 /* WSAEWOULDBLOCK */) return 0;
		if (n <= 0) break;
		if (n < (int)sizeof info) return 0; // the rest is in flight; the zero-read re-fires
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

// accept(): a connection the posted AcceptEx already took is popped first —
// it arrived before anything still sitting in the backlog, so this keeps
// connections in arrival order. With no AcceptEx outstanding (the listener was
// never watched, or the caller is draining a burst after popping one) the
// backlog is read directly.
int accept(int fd, void *addr, int *len) {
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	ws_SOCKET s = WS_INVALID;
	KD(fd)->rd_ready = 0;
	for (;;) {
		if (KD(fd)->rr_chan) {
			int got = kfd_rr_recv(fd, &s);
			if (got == 0) { errno = L_EAGAIN; return -1; }
			if (got == 1) {
				if (addr && len) p_getpeername(s, addr, len);
				break;
			}
		}
		if (kml_port) kml_port_drain(0); // a completed AcceptEx may be waiting on the port
		if (KD(fd)->acc_have) {
			s = KD(fd)->acc_sock;
			KD(fd)->acc_have = 0;
			KD(fd)->acc_sock = 0;
			// SO_UPDATE_ACCEPT_CONTEXT: the socket inherits the listener's
			// properties and becomes usable with getpeername/shutdown/setsockopt.
			ws_SOCKET ls = kfd_sock(fd);
			p_setsockopt(s, WS_SOL_SOCKET, 0x700B, (const char *)&ls, sizeof ls);
			if (addr && len) p_getpeername(s, addr, len);
			if (KD(fd)->rr && kfd_rr_dispatch(KD(fd), s)) continue; // a worker's turn: take the next
			break;
		}
		if (!KD(fd)->acc_op) {
			s = p_accept(kfd_sock(fd), addr, len);
			if (s == WS_INVALID) return set_wsa_errno();
			if (KD(fd)->rr && kfd_rr_dispatch(KD(fd), s)) continue;
			break;
		}
		// An AcceptEx is outstanding, so the backlog is empty by construction:
		// whatever arrives next lands in it, not in the backlog.
		if (KD(fd)->nonblock) { errno = L_EAGAIN; return -1; }
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
	KD(nfd)->connected = 1;
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
	if (!KD(fd)->nonblock || !KD(fd)->sock_stream || !addr || len < 2) return -2;
	int fam = *(const unsigned short *)addr;
	if ((fam != 2 && fam != 23) || fam != KD(fd)->family) return -2;
	afd_bind_ext(kfd_sock(fd));
	if (!p_ConnectEx) return -2;
	if (!KD(fd)->bound) {
		struct { unsigned short fam; char rest[26]; } any = {0};
		any.fam = (unsigned short)fam;
		if (p_bind(kfd_sock(fd), &any, fam == 2 ? 16 : 28) != 0) return set_wsa_errno();
		KD(fd)->bound = 1;
	}
	kfd_op *op = op_new(fd, OPK_CONNECT);
	if (!op) { errno = L_ENOMEM; return -1; }
	if (op_sock_prepare(op, fd) != 0) { op_free(op); return -2; }
	KD(fd)->conn_failed = 0;
	KD(fd)->conn_err = 0;
	KD(fd)->wr_ready = KD(fd)->ex_ready = 0;
	BOOL ok = p_ConnectEx(kfd_sock(fd), addr, len, NULL, 0, NULL, &op->ov);
	int e = ok ? 0 : p_WSAGetLastError();
	if (io_trace()) fprintf(stderr, "[io] connect fd=%d ConnectEx -> %d (wsa %d)\n", fd, (int)ok, e);
	if (ok || e == ERROR_IO_PENDING) {
		// An immediate success still queues its packet; the drain finishes the
		// connect either way, so both report "in progress".
		KD(fd)->conn_op = op;
		KD(fd)->connecting = 1;
		errno = L_EINPROGRESS;
		return -1;
	}
	op_free(op);
	errno = map_wsa_errno(e);
	return -1;
}

// ---- named pipes behind net.connect({ path }) -----------------------------------
// Node's IPC path on Windows is a named pipe (`\\.\pipe\name` or `\\?\pipe\name`),
// not a Unix-domain socket: that is what every Windows service and tool that
// speaks "local socket" listens on. The IR stays host-agnostic — it makes an
// AF_UNIX stream socket and connect()s it to a sockaddr_un — so the mapping
// happens here: a connect whose path is in the pipe namespace opens the pipe
// overlapped and turns the description into a duplex overlapped pipe end, the
// same kind a child's stdio pipe is, served by the same zero-read / queued-write
// machinery. Any other path stays a real AF_UNIX socket.
static int is_pipe_namespace(const char *p) {
	if (!p || p[0] != '\\' || p[1] != '\\' || (p[2] != '.' && p[2] != '?') || p[3] != '\\') return 0;
	return (p[4] == 'p' || p[4] == 'P') && (p[5] == 'i' || p[5] == 'I') &&
	       (p[6] == 'p' || p[6] == 'P') && (p[7] == 'e' || p[7] == 'E') && p[8] == '\\' && p[9] != 0;
}

// 0 = connected (the pipe handle is open, so there is no in-progress state),
// -1 = errno set and fd still the socket it was, so the caller's error path can
// close it as usual.
static int kfd_pipe_connect(int fd, const char *path) {
	int wn = MultiByteToWideChar(CP_UTF8, 0, path, -1, NULL, 0);
	wchar_t *w = wn > 0 ? (wchar_t *)malloc((size_t)wn * sizeof(wchar_t)) : NULL;
	if (!w) { errno = L_ENOMEM; return -1; }
	MultiByteToWideChar(CP_UTF8, 0, path, -1, w, wn);
	HANDLE h = INVALID_HANDLE_VALUE;
	DWORD err = 0;
	// Every instance busy is transient: a server is between accepting one client
	// and offering the next instance. libuv waits for one (up to 30 s, on a
	// worker thread); a bounded wait here covers the same window.
	for (int tries = 0; tries < 3; tries++) {
		h = CreateFileW(w, GENERIC_READ | GENERIC_WRITE, 0, NULL, OPEN_EXISTING, FILE_FLAG_OVERLAPPED, NULL);
		if (h != INVALID_HANDLE_VALUE) break;
		err = GetLastError();
		if (err != ERROR_PIPE_BUSY || !WaitNamedPipeW(w, 2000)) break;
	}
	free(w);
	if (h == INVALID_HANDLE_VALUE) {
		errno = err == ERROR_FILE_NOT_FOUND || err == ERROR_PATH_NOT_FOUND || err == ERROR_BAD_PATHNAME ? L_ENOENT
		      : err == ERROR_ACCESS_DENIED ? L_EACCES
		      : err == ERROR_PIPE_BUSY || err == ERROR_SEM_TIMEOUT ? L_ETIMEDOUT
		      : L_ECONNREFUSED;
		return -1;
	}
	kfd_desc *k = KD(fd);
	kfd_cancel_sock_ops(fd);
	if (k->sock && p_closesocket) p_closesocket(k->sock);
	k->sock = 0;
	k->sock_port = NULL;
	k->emul = 0;
	k->kind = KFD_PIPE;
	k->h = h;
	k->ovl = 1;     // writes queue as overlapped WriteFiles
	k->ovl_rd = 1;  // reads are readiness-driven off a zero-read
	k->npipe = 1;
	k->connected = 1;
	// One association, made here for both directions, in the sockets' style: no
	// FILE_SKIP_COMPLETION_PORT_ON_SUCCESS, which the write-only pipe ends use
	// and which would swallow the packet of a zero-read that completes at once
	// (data already waiting) — leaving the descriptor never readable.
	if (CreateIoCompletionPort(h, kml_port_get(), 1 /* KEY_OP */, 0)) {
		k->sock_port = kml_port_get();
		k->assoc = 1;
	}
	k->rd_ready = k->wr_ready = k->ex_ready = 0;
	return 0;
}

int connect(int fd, const void *addr, int len) {
	if (fd >= 0 && fd < KFD_MAX && KD(fd)->kind == KFD_PIPE && KD(fd)->npipe) { errno = L_EISCONN; return -1; }
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	// sockaddr_un: a 2-byte family, then the NUL-terminated path.
	if (addr && len > 2 && *(const unsigned short *)addr == 1 /* AF_UNIX */ && is_pipe_namespace((const char *)addr + 2))
		return kfd_pipe_connect(fd, (const char *)addr + 2);
	if (KD(fd)->conn_op) {
		// Give a just-completed ConnectEx the chance to be seen before answering.
		if (kml_port) kml_port_drain(0);
		if (KD(fd)->conn_op) { errno = L_EALREADY; return -1; }
	}
	if (KD(fd)->connected && KD(fd)->sock_stream) { errno = L_EISCONN; return -1; }
	int cx = kfd_connectex(fd, addr, len);
	if (cx != -2) return cx;
	if (p_connect(kfd_sock(fd), addr, len) == 0) {
		if (io_trace()) {
			struct { uint16_t fam, port; uint32_t a; char z[8]; } la = {0};
			int ll = sizeof la;
			p_getsockname(kfd_sock(fd), &la, &ll);
			fprintf(stderr, "[io] connect fd=%d sock=%llu -> 0 local=%u\n", fd, (unsigned long long)kfd_sock(fd), (unsigned)htons(la.port));
		}
		KD(fd)->bound = 1;
		if (KD(fd)->sock_stream) KD(fd)->connected = 1;
		return 0;
	}
	int e = p_WSAGetLastError();
	if (io_trace()) fprintf(stderr, "[io] connect fd=%d -> -1 (wsa %d)\n", fd, e);
	// A non-blocking connect reports WOULDBLOCK on Windows; POSIX callers
	// expect EINPROGRESS and then wait for writability.
	if (e == WSAEWOULDBLOCK) { KD(fd)->connecting = 2; KD(fd)->bound = 1; errno = L_EINPROGRESS; return -1; }
	errno = map_wsa_errno(e);
	return -1;
}
// The socket-API calls a net.Socket makes on its descriptor, once that
// descriptor is a named pipe. A pipe has no half-close: libuv's shutdown drains
// the writes and then lets the handle go (its EOF timer), which is how the peer
// learns the stream ended. After that this end reads as end-of-file.
static int kfd_is_npipe(int fd) { return fd >= 0 && fd < KFD_MAX && KD(fd)->kind == KFD_PIPE && KD(fd)->npipe; }
static int kfd_npipe_shutdown(int fd, int how) {
	if (how == 0 /* SHUT_RD */) return 0;
	kfd_desc *k = KD(fd);
	if (!k->h) return 0;
	if (k->wr_op || k->wq_head) kfd_wq_flush(fd);
	if (k->rd_op) { CancelIoEx(k->h, &k->rd_op->ov); k->rd_op = NULL; }
	HANDLE h = k->h;
	k->h = NULL;
	k->assoc = 0;
	k->npipe_eof = 1;
	k->rd_ready = 1;
	CloseHandle(h);
	return 0;
}
int shutdown(int fd, int how) {
	if (kfd_is_npipe(fd)) return kfd_npipe_shutdown(fd, how);
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	if (io_trace()) fprintf(stderr, "[io] shutdown fd=%d how=%d\n", fd, how);
	return p_shutdown(kfd_sock(fd), how) == 0 ? 0 : set_wsa_errno();
}
// A pipe has no address; the socket API still has to answer for one. AF_UNIX
// with an empty path is what an unbound Unix-domain socket reports.
static int kfd_npipe_name(void *addr, int *len) {
	if (addr && len && *len >= 2) { *(unsigned short *)addr = 1; *len = 2; }
	return 0;
}
int getsockname(int fd, void *addr, int *len) {
	if (kfd_is_npipe(fd)) return kfd_npipe_name(addr, len);
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	return p_getsockname(kfd_sock(fd), addr, len) == 0 ? 0 : set_wsa_errno();
}
int getpeername(int fd, void *addr, int *len) {
	if (kfd_is_npipe(fd)) return kfd_npipe_name(addr, len);
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	return p_getpeername(kfd_sock(fd), addr, len) == 0 ? 0 : set_wsa_errno();
}

int setsockopt(int fd, int level, int opt, const void *val, int len) {
	if (kfd_is_npipe(fd)) return 0; // TCP_NODELAY/SO_KEEPALIVE mean nothing to a pipe; Node ignores them too
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
	if (kfd_is_npipe(fd)) { // SO_ERROR after the connect: it succeeded, or fd would still be a socket
		if (val && len && *len >= 4) { *(int *)val = 0; *len = 4; }
		return 0;
	}
	if (!kfd_is(fd, KFD_SOCKET)) { errno = L_ENOTSOCK; return -1; }
	if (level == L_SOL_SOCKET) {
		level = WS_SOL_SOCKET;
		if (opt == L_SO_ERROR) opt = WS_SO_ERROR;
	}
	if (level == WS_SOL_SOCKET && opt == WS_SO_ERROR && val && len && *len >= 4) {
		// A ConnectEx failure is reported through its completion, not through the
		// socket's own SO_ERROR; the drain recorded it. Read-and-clear, as POSIX.
		if (KD(fd)->conn_op && kml_port) kml_port_drain(0);
		if (KD(fd)->conn_err) {
			*(int *)val = KD(fd)->conn_err;
			*len = 4;
			KD(fd)->conn_err = 0;
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
	if (!(flags & 0x2)) KD(fd)->rd_ready = 0; // consumed (a MSG_PEEK leaves it); the next select() re-arms and re-learns it
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
	KD(rfd)->ovl_rd = 1;
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
	KD(wfd)->ovl = 1;
	fds[0] = rfd;
	fds[1] = wfd;
	return 0;
}

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
	if (KD(fd)->rd_op || KD(fd)->rd_ready) return 0;
	kfd_op *op = op_new(fd, OPK_ZERO_READ);
	if (!op) return -1;
	if (op_sock_prepare(op, fd) != 0) { op_free(op); return -1; }
	DWORD got = 0;
	if (ReadFile(kfd_handle(fd), op->abuf, 0, &got, &op->ov) || GetLastError() == ERROR_IO_PENDING) {
		KD(fd)->rd_op = op; // a synchronous success queues its packet too
		return 0;
	}
	// Broken pipe (every writer gone) or any other hard failure: readable — the
	// read() that follows reports end-of-file.
	op_free(op);
	KD(fd)->rd_ready = 1;
	return 0;
}

// read() on an overlapped read end. Non-blocking: take what is buffered, or
// EAGAIN. Blocking: wait for the first bytes (a byte pipe's ReadFile returns as
// soon as any arrive). The event's low bit keeps these reads' completions off
// the port — they are waited for right here.
static int64_t kfd_ovl_pipe_read(int fd, void *buf, size_t n) {
	HANDLE h = kfd_handle(fd);
	int hinted = KD(fd)->rd_ready; // a completion said "bytes or EOF"
	KD(fd)->rd_ready = 0;
	int cancel_if_pending = 0;
	if (KD(fd)->nonblock) {
		DWORD avail = 0;
		int st = pipe_readable(h, &avail);
		if (st < 0) return 0; // every writer gone and the buffer drained: EOF
		if (st > 0) { if (n > avail) n = avail; }
		else if (!hinted) { errno = L_EAGAIN; return -1; }
		else cancel_if_pending = 1; // trust the completion over the probe: try the read itself
	}
	if (!KD(fd)->rd_ev) KD(fd)->rd_ev = CreateEventW(NULL, TRUE, FALSE, NULL);
	if (!KD(fd)->rd_ev) { errno = L_ENOMEM; return -1; }
	OVERLAPPED ov;
	memset(&ov, 0, sizeof ov);
	ov.hEvent = (HANDLE)((ULONG_PTR)KD(fd)->rd_ev | 1);
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
	if (KD(fd)->npipe_eof) return 0; // a shut-down named pipe: end-of-file
	if (io_trace()) fprintf(stderr, "[io] %llu read fd=%d kind=%d nonblock=%d ovl=%d n=%zu\n", (unsigned long long)(GetTickCount64() % 100000), fd, KD(fd)->kind, KD(fd)->nonblock, KD(fd)->ovl, n);
	if (KD(fd)->kind == KFD_SOCKET) {
		if (KD(fd)->reset) return 0; // the EOF that follows a reported reset
		KD(fd)->rd_ready = 0; // consumed; the next select() re-arms and re-learns it
		int r = p_recv(kfd_sock(fd), (char *)buf, (int)n, 0);
		if (io_trace()) {
			fprintf(stderr, "[io]   recv -> %d (wsa %d)", r, r == WS_ERROR && p_WSAGetLastError ? p_WSAGetLastError() : 0);
			if (r > 0) { fprintf(stderr, " bytes="); for (int i = 0; i < r && i < 24; i++) fprintf(stderr, "%02x", ((unsigned char *)buf)[i]); }
			fprintf(stderr, "\n");
			if (r == 0) {
				// EOF: report whether every other socket in the table is still a
				// live handle (a closed one answers WSAENOTSOCK to getsockname).
				for (int o = 0; o < KFD_MAX; o++) {
					if (KD(o)->kind != KFD_SOCKET) continue;
					struct { uint16_t fam, port; uint32_t a; char z[8]; } la = {0};
					int ll = sizeof la;
					int rc = p_getsockname(KD(o)->sock, &la, &ll);
					fprintf(stderr, "[io]     fd=%d sock=%llu getsockname=%d port=%u\n", o, (unsigned long long)KD(o)->sock, rc, (unsigned)htons(la.port));
				}
			}
		}
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
	if (KD(fd)->kind == KFD_PIPE) {
		HANDLE h = kfd_handle(fd);
		// stdin (fd 0) is served by a buffering reader thread so a read never
		// blocks the loop: never touch the handle here (it would race the
		// thread) — drain the buffer. Spawn it on the first read if select()
		// has not already.
		if (KD(fd)->use_reader) {
			if (!KD(fd)->thr_state) kfd_ensure_sync_reader(fd);
			if (KD(fd)->thr_state) return kfd_thr_read(fd, buf, n, KD(fd)->nonblock);
		}
		// A read end this layer created (or inherited) overlapped: never the
		// CRT's _read, which issues a ReadFile with no OVERLAPPED.
		if (KD(fd)->ovl_rd) return kfd_ovl_pipe_read(fd, buf, n);
		// Any other pipe (in-process worker/channel IPC, a child's stdout/stderr
		// read end): the original PeekNamedPipe + non-blocking read path.
		DWORD avail = 0;
		int st = pipe_readable(h, &avail);
		if (st < 0) return 0;               // writer gone: EOF
		if (st == 0 && KD(fd)->nonblock) { errno = L_EAGAIN; return -1; }
	}
	if (KD(fd)->kind == KFD_PIPE) {
		// A synchronous pipe end this layer owns: no CRT fd wraps it.
		DWORD got = 0;
		if (!ReadFile(kfd_handle(fd), buf, (DWORD)n, &got, NULL))
			return GetLastError() == ERROR_BROKEN_PIPE ? 0 : (errno = L_EBADF, -1);
		return got;
	}
	int r = _read(kfd_crt_fd(fd), buf, (unsigned)n);
	if (r < 0) errno = L_EBADF;
	return r;
}

int64_t write(int fd, const void *buf, size_t n) {
	if (fd < 0 || fd >= KFD_MAX) { errno = L_EBADF; return -1; }
	if (KD(fd)->npipe_eof) { errno = L_EPIPE; return -1; } // written after its own shutdown()
	if (KD(fd)->kind == KFD_SOCKET) {
		int r = p_send(kfd_sock(fd), (const char *)buf, (int)n, 0);
		if (io_trace()) fprintf(stderr, "[io] write fd=%d n=%zu -> %d (wsa %d)\n", fd, n, r, r == WS_ERROR && p_WSAGetLastError ? p_WSAGetLastError() : 0);
		if (r == WS_ERROR) {
			set_wsa_errno();
			if (errno == L_EAGAIN) KD(fd)->wr_ready = 0; // the send buffer filled: no longer writable
			return -1;
		}
		return r;
	}
	kfd_classify(fd);
	if (KD(fd)->kind == KFD_PIPE && KD(fd)->ovl_h && !KD(fd)->ovl) {
		// An overlapped handle this layer did not create (an inherited std fd):
		// every op on it needs an OVERLAPPED, so write it overlapped and wait
		// right here — the blocking-write semantics the fd has always had. The
		// event's low bit keeps the completion off any port.
		HANDLE h = kfd_handle(fd);
		if (!KD(fd)->wr_ev) KD(fd)->wr_ev = CreateEventW(NULL, TRUE, FALSE, NULL);
		if (!KD(fd)->wr_ev) { errno = L_ENOMEM; return -1; }
		OVERLAPPED ov;
		memset(&ov, 0, sizeof ov);
		ov.hEvent = (HANDLE)((ULONG_PTR)KD(fd)->wr_ev | 1);
		DWORD wr = 0, e = 0;
		if (!WriteFile(h, buf, (DWORD)n, &wr, &ov)) {
			e = GetLastError();
			if (e == ERROR_IO_PENDING) e = GetOverlappedResult(h, &ov, &wr, TRUE) ? 0 : GetLastError();
		}
		if (e) { errno = (e == ERROR_NO_DATA || e == ERROR_BROKEN_PIPE) ? L_EPIPE : L_EBADF; return -1; }
		return wr;
	}
	if (KD(fd)->kind == KFD_PIPE && KD(fd)->ovl) {
		// Overlapped end (the parent's child-stdin side): copy and queue — the
		// user-space write queue Node keeps for a Writable. write() accepts the
		// bytes immediately, the FIFO drains through overlapped completions,
		// and close() flushes — so a parent streaming a large stdin never
		// deadlocks against a child whose stdout it is not yet draining.
		if (kfd_assoc(fd) != 0) { errno = L_EBADF; return -1; }
		kfd_op *op = op_new(fd, OPK_WRITE);
		char *copy = op ? (char *)op_alloc(n ? n : 1) : NULL;
		if (!copy) { if (op) op_free(op); errno = L_EBADF; return -1; }
		memcpy(copy, buf, n);
		op->buf = copy;
		op->len = (unsigned long)n;
		if (KD(fd)->wq_tail) KD(fd)->wq_tail->next = op; else KD(fd)->wq_head = op;
		KD(fd)->wq_tail = op;
		kfd_wq_pump(KD(fd));
		return (int64_t)n;
	}
	if (KD(fd)->kind == KFD_PIPE) {
		DWORD wr = 0;
		if (!WriteFile(kfd_handle(fd), buf, (DWORD)n, &wr, NULL)) {
			DWORD e = GetLastError();
			errno = (e == ERROR_NO_DATA || e == ERROR_BROKEN_PIPE) ? L_EPIPE : L_EBADF;
			return -1;
		}
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

// close: drop fd's reference. Only the last reference tears the handle down —
// until then the description (its handle, pending ops, readiness) stays whole
// for the fds still sharing it.
int close(int fd) {
	if (fd < 0 || fd >= KFD_MAX) { errno = L_EBADF; return -1; }
	if (io_trace()) fprintf(stderr, "[io] close fd=%d kind=%d refs=%d\n", fd, KD(fd)->kind, kfd_tab[fd] ? kfd_tab[fd]->refs : 0);
	if (KD(fd)->kind == KFD_FOREIGN) { errno = L_EBADF; return -1; } // not ours to close
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
		// A socket fd never touches the CRT: closesocket does the whole
		// teardown and the slot simply returns to the pool. A connected
		// socket gets shutdown(SD_SEND) first so the peer sees a FIN: a bare
		// closesocket on Windows can turn into a reset (the Go HTTP client
		// reported "connection forcibly closed" on a Connection: close
		// response), where Linux's close() sends FIN.
		ws_SOCKET s = kfd_sock(fd);
		int inherited = KD(fd)->inherited;
		// Cancel every pending op and bump the generation: their packets still
		// arrive (a cancelled op completes too) and the drain, finding the
		// generation moved on, frees them without touching the slot's next
		// tenant. A connection AcceptEx took that accept() never popped is closed
		// with the listener — nobody else holds it.
		kfd_cancel_sock_ops(fd);
		if (KD(fd)->acc_have && p_closesocket) p_closesocket(KD(fd)->acc_sock);
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
			free(rr);
		}
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
	if (KD(fd)->kind == KFD_PIPE) {
		// Flush queued overlapped writes (the POSIX-parity point: a blocking
		// write to this pipe would have blocked here too), stop the reader
		// thread, and cancel the armed zero-read; its cancellation packet is
		// freed by a later drain, recognized stale by the generation bump.
		if (KD(fd)->ovl && (KD(fd)->wr_op || KD(fd)->wq_head)) kfd_wq_flush(fd);
		if (KD(fd)->thr) kfd_stop_sync_reader(fd);
		if (KD(fd)->rd_op) {
			CancelIoEx(kfd_handle(fd), &KD(fd)->rd_op->ov);
			KD(fd)->rd_op = NULL;
		}
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
		if (io_trace()) fprintf(stderr, "[io] fcntl fd=%d kind=%d nonblock=%d\n", fd, KD(fd)->kind, nb);
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

// ---- select over the IR's 1024-bit fd_set bitmap -----------------------------
typedef struct { int64_t sec; int64_t usec; } kml_timeval;

static inline int fd_isset(const unsigned char *set, int fd) {
	return set && (set[fd >> 3] >> (fd & 7)) & 1;
}

// Readiness of a non-socket fd for reading: pipes via PeekNamedPipe, the
// console (and any other waitable handle) via a zero-timeout wait, regular
// files are always ready. Returns 1 ready, 0 not, -1 hangup (readable EOF).
static int plain_readable(int fd) {
	HANDLE h = kfd_handle(fd);
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
	kfd_desc *k = KD(fd);
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
	int64_t deadline_us = -1; // absolute QPC microseconds; -1 = no deadline
	if (tv) {
		int64_t us = tv->sec * 1000000 + tv->usec;
		deadline_us = kml_now_us() + (us < 0 ? 0 : us);
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
			if (KD(fd)->kind == KFD_SOCKET || KD(fd)->kind == KFD_FOREIGN) {
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
				if (KD(fd)->kind == KFD_CONSOLE) {
					kcon_ensure();
					st = kcon_readable();
				} else if (KD(fd)->kind == KFD_PIPE && KD(fd)->use_reader) {
					// stdin: readiness comes from the buffering reader thread
					// (which wakes the port), never a direct handle probe.
					if (!KD(fd)->thr_state) kfd_ensure_sync_reader(fd);
					st = kfd_thr_readable(fd);
				} else if (KD(fd)->kind == KFD_PIPE && KD(fd)->ovl_rd) {
					// An overlapped read end (every pipe() this layer makes): bytes
					// already buffered answer at once; otherwise a zero-read on the
					// port ends the wait the moment the writer writes or closes.
					st = KD(fd)->rd_ready ? 1 : pipe_readable(kfd_handle(fd), NULL);
					if (st == 0 && kfd_arm_pipe_zero_read(fd) != 0) have_pollable_pipe = 1;
					if (st == 0 && KD(fd)->rd_ready) st = 1; // decided while arming (broken pipe)
				} else if (KD(fd)->kind == KFD_PIPE) {
					// A pipe handle that can be neither port-armed nor given a
					// reader thread: PeekNamedPipe, re-probed on the slice below.
					st = pipe_readable(kfd_handle(fd), NULL);
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
				int src = kfd_rd_src(fd);
				if (src != fd) {
					kfd_arm_interest(src, fd_isset(rset, fd), 0, 0);
					kfd_arm_interest(fd, 0, fd_isset(wset, fd), fd_isset(eset, fd));
				} else kfd_arm_interest(fd, fd_isset(rset, fd), fd_isset(wset, fd), fd_isset(eset, fd));
			}
			while (kml_port_drain(0) == 64) {}
			for (int i = 0; i < nsock; i++) {
				int fd = sock_fd[i];
				// "Excepted ⇒ also writable": POSIX callers wait for writability and
				// then ask SO_ERROR why.
				if (fd_isset(rset, fd) && KD(kfd_rd_src(fd))->rd_ready) { rout[fd >> 3] |= (unsigned char)(1u << (fd & 7)); ready++; }
				if (fd_isset(wset, fd) && (KD(fd)->wr_ready || KD(fd)->ex_ready)) { wout[fd >> 3] |= (unsigned char)(1u << (fd & 7)); ready++; }
				if (fd_isset(eset, fd) && KD(fd)->ex_ready) { eout[fd >> 3] |= (unsigned char)(1u << (fd & 7)); ready++; }
			}
		}
		// A wake with nothing ready ends the call with 0, exactly as a timeout
		// would: the caller's loop takes its turn (reaps the child, reads the
		// reader thread's buffer on its next pass) and comes back. Blocking again
		// here instead would strand a wake whose source has no fd in the sets.
		int woken = kml_woken;
		kml_woken = 0;
		if (ready || woken || (deadline_us >= 0 && kml_now_us() >= deadline_us)) {
			// Reported readiness is consumed: the next select() re-arms, and a
			// condition that still holds completes again at once.
			for (int i = 0; i < nsock; i++) {
				int fd = sock_fd[i];
				if (rout[fd >> 3] & (1u << (fd & 7))) KD(kfd_rd_src(fd))->rd_ready = 0;
				if (wout[fd >> 3] & (1u << (fd & 7))) KD(fd)->wr_ready = 0;
				if (eout[fd >> 3] & (1u << (fd & 7))) KD(fd)->ex_ready = 0;
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
		int64_t wake_us = deadline_us; // -1 = infinite
		if (have_pollable_pipe) {
			int64_t cap = kml_now_us() + 10000; // 10 ms re-probe
			if (wake_us < 0 || cap < wake_us) wake_us = cap;
		}
		// A signal raised before the wait: its wake packet may already have been
		// swallowed by a drain above, so don't block — deliver it now.
		if (kml_sig_pending()) wake_us = kml_now_us();
		int willblock = (wake_us < 0) || (wake_us > kml_now_us());
		// One line per blocking wait: the count of these against elapsed time is
		// what shows a reactor that polls (many short waits) from one that waits.
		if (io_trace() && willblock) {
			int64_t ms = wake_us < 0 ? -1 : (wake_us - kml_now_us()) / 1000;
			fprintf(stderr, "[io] wait slice=%lld\n", (long long)ms);
		}
		if (willblock) kml_port_wait_until(wake_us);
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

// ---- libcurl fd_set bridge ---------------------------------------------------
// The reactor merges libcurl's transfers into its select() by calling
// curl_multi_fdset with the IR's 128-byte fd bitmaps. On Windows libcurl
// fills *Winsock* fd_sets — { u_int count; SOCKET array[64] }, 516 bytes of
// raw socket handles — so that call would overflow the bitmaps and report
// nothing the bitmap select could use. This definition shadows libcurl's
// export (the linker prefers an object's symbol over the import library),
// calls the real one through the already-loaded DLL, and hands each of
// curl's sockets a stable fd number from the owned pool, under a KFD_FOREIGN
// description that select() resolves back to the SOCKET. The mapping is rebuilt
// on every call, so a socket curl closed is dropped one reactor iteration later
// at worst. Each reactor thread drives its own multi handle, so a description
// records the thread that reported it and a sweep retires only its own.
typedef struct { unsigned int fd_count; ws_SOCKET fd_array[64]; } ws_fd_set;
static int (*p_curl_multi_fdset)(void *, ws_fd_set *, ws_fd_set *, ws_fd_set *, int *);

static int foreign_fd_for(ws_SOCKET s, unsigned char *seen) {
	for (int fd = KFD_POOL_BASE; fd < KFD_MAX; fd++) {
		if (kfd_tab[fd] && kfd_tab[fd]->kind == KFD_FOREIGN && kfd_tab[fd]->sock == s && kfd_tab[fd]->owner_tid == GetCurrentThreadId()) { seen[fd] = 1; return fd; }
	}
	int nfd = kfd_slot_new(KFD_FOREIGN, -1);
	if (nfd < 0) return -1; // pool full: this transfer goes unwatched until a slot frees
	kfd_tab[nfd]->sock = s;
	kfd_tab[nfd]->owner_tid = GetCurrentThreadId();
	seen[nfd] = 1;
	return nfd;
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
	for (int fd = KFD_POOL_BASE; fd < KFD_MAX; fd++) {
		if (kfd_tab[fd] && kfd_tab[fd]->kind == KFD_FOREIGN && !seen[fd] && kfd_tab[fd]->owner_tid == GetCurrentThreadId()) {
			// curl is done with this socket: disarm its poll and retire the slot, so a
			// late completion is recognized stale and never marks the next tenant.
			if (kfd_tab[fd]->afd_op) kfd_cancel_afd(fd);
			kfd_slot_release(fd);
		}
	}
	// Preserve the reactor's "-1 means curl has nothing to wait on" contract.
	if (maxfd) *maxfd = mx;
	return 0;
}
