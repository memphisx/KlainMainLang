// Drives the shim's emulated-completion path directly: a listener and a
// connected socket are associated with a FOREIGN completion port first (what a
// cluster peer process, or another reactor thread, does), so the shim cannot
// associate them with its own port and must fall back to event-emulated ops.
// Everything goes through the shim's POSIX-named API.
#define WIN32_LEAN_AND_MEAN
#define NO_OLDNAMES 1
#include <windows.h>
#include <stdio.h>
#include <string.h>
#include <stdint.h>

int socket(int, int, int);
int bind(int, const void *, int);
int listen(int, int);
int accept(int, void *, int *);
int connect(int, const void *, int);
int getsockname(int, void *, int *);
int fcntl(int, int, ...);
int64_t read(int, void *, size_t);
int64_t write(int, const void *, size_t);
int close(int);
typedef struct { int64_t sec, usec; } kml_timeval;
int select(int, unsigned char *, unsigned char *, unsigned char *, kml_timeval *);
intptr_t __kml_win_fd_socket(int fd);

static void setbit(unsigned char *s, int fd) { s[fd >> 3] |= (unsigned char)(1u << (fd & 7)); }
static int isset(unsigned char *s, int fd) { return (s[fd >> 3] >> (fd & 7)) & 1; }

static int wait_readable(int fd, int ms) {
	unsigned char r[128];
	memset(r, 0, sizeof r);
	setbit(r, fd);
	kml_timeval tv = { ms / 1000, (ms % 1000) * 1000 };
	int n = select(fd + 1, r, NULL, NULL, &tv);
	return n > 0 && isset(r, fd);
}

int main(void) {
	struct { unsigned short fam, port; unsigned int addr; char z[8]; } a;
	memset(&a, 0, sizeof a);
	a.fam = 2;
	a.addr = 0x0100007f; // 127.0.0.1, network order
	int l = socket(2, 1, 0);
	if (bind(l, &a, sizeof a) != 0 || listen(l, 8) != 0) { printf("FAIL listen\n"); return 1; }
	int al = sizeof a;
	getsockname(l, &a, &al);
	fcntl(l, 4, 0x800); // O_NONBLOCK (Linux value, as the IR passes it)

	// Steal the listener's port association before the shim can take it.
	HANDLE foreign = CreateIoCompletionPort(INVALID_HANDLE_VALUE, NULL, 0, 1);
	if (!CreateIoCompletionPort((HANDLE)__kml_win_fd_socket(l), foreign, 99, 0)) { printf("FAIL foreign assoc\n"); return 1; }

	int c = socket(2, 1, 0);
	if (connect(c, &a, sizeof a) != 0) { printf("FAIL connect\n"); return 1; } // blocking connect

	if (!wait_readable(l, 3000)) { printf("FAIL listener never readable (emulated AcceptEx)\n"); return 1; }
	int s = accept(l, NULL, NULL);
	if (s < 0) { printf("FAIL accept\n"); return 1; }
	printf("accepted through an emulated AcceptEx\n");

	// Same for the accepted socket's zero-read.
	if (!CreateIoCompletionPort((HANDLE)__kml_win_fd_socket(s), foreign, 98, 0)) { printf("FAIL foreign assoc 2\n"); return 1; }
	fcntl(s, 4, 0x800);
	if (wait_readable(s, 200)) { printf("FAIL readable with nothing sent\n"); return 1; }
	write(c, "ping", 4);
	if (!wait_readable(s, 3000)) { printf("FAIL never readable (emulated zero-read)\n"); return 1; }
	char buf[16];
	int64_t n = read(s, buf, sizeof buf);
	printf("read %lld bytes through an emulated zero-read: %.*s\n", (long long)n, (int)n, buf);

	// Nothing of ours may have leaked onto the foreign port.
	DWORD nb; ULONG_PTR key; OVERLAPPED *ov = NULL;
	BOOL got = GetQueuedCompletionStatus(foreign, &nb, &key, &ov, 100);
	printf("foreign port received %s\n", (got || ov) ? "A PACKET (BUG)" : "nothing");

	close(c);
	if (!wait_readable(s, 3000)) { printf("FAIL EOF not reported\n"); return 1; }
	n = read(s, buf, sizeof buf);
	printf("EOF read -> %lld\n", (long long)n);
	close(s);
	close(l);
	printf("OK\n");
	return (got || ov) ? 1 : 0;
}
