// Drives the shim's refcounted fd descriptions directly (TDD-00183 Stage 3):
// dup2() makes two fds share one description, the handle is torn down once —
// by the last close, not the first — readiness is seen through every alias, and
// every owned kind allocates from one 512-slot pool. Nothing in the emitted
// runtime calls dup2 on this platform (child stdio is handed over by handle),
// so no E2E program reaches these paths. Everything goes through the shim's
// POSIX-named API.
#define WIN32_LEAN_AND_MEAN
#define NO_OLDNAMES 1
#include <windows.h>
#include <errno.h>
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
int pipe(int[2]);
int dup2(int, int);
int isatty(int);
int64_t read(int, void *, size_t);
int64_t write(int, const void *, size_t);
int close(int);
typedef struct { int64_t sec, usec; } kml_timeval;
int select(int, unsigned char *, unsigned char *, unsigned char *, kml_timeval *);

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

#define FAIL(...) do { printf("FAIL " __VA_ARGS__); printf("\n"); return 1; } while (0)

int main(void) {
	setvbuf(stdout, NULL, _IONBF, 0);
	struct { unsigned short fam, port; unsigned int addr; char z[8]; } a;
	memset(&a, 0, sizeof a);
	a.fam = 2;
	a.addr = 0x0100007f; // 127.0.0.1, network order
	int l = socket(2, 1, 0);
	if (l < 512) FAIL("listener fd %d is not a pool fd", l);
	if (bind(l, &a, sizeof a) != 0 || listen(l, 8) != 0) FAIL("listen");
	int al = sizeof a;
	getsockname(l, &a, &al);
	int c = socket(2, 1, 0);
	if (connect(c, &a, sizeof a) != 0) FAIL("connect");
	if (!wait_readable(l, 3000)) FAIL("listener never readable");
	int s = accept(l, NULL, NULL);
	if (s < 0) FAIL("accept");

	// ---- a socket alias outlives the fd it was made from ----
	const int alias = 1000;
	if (dup2(s, alias) != alias) FAIL("dup2(sock) errno=%d", errno);
	if (close(s) != 0) FAIL("close of the original socket fd");
	if (write(c, "ping", 4) != 4) FAIL("client write");
	if (!wait_readable(alias, 3000)) FAIL("alias never readable: the first close tore the socket down");
	char buf[16] = {0};
	if (read(alias, buf, sizeof buf) != 4 || memcmp(buf, "ping", 4)) FAIL("read through the alias");
	if (write(alias, "pong", 4) != 4) FAIL("write through the alias");
	memset(buf, 0, sizeof buf);
	if (!wait_readable(c, 3000) || read(c, buf, sizeof buf) != 4 || memcmp(buf, "pong", 4)) FAIL("client read of the alias's reply");
	printf("socket alias survived the original's close: ping/pong\n");
	// The freed number is a free slot again, and reusing it must not disturb the alias.
	int reuse = socket(2, 2, 0);
	if (reuse != s) FAIL("closed fd %d was not reused (got %d)", s, reuse);
	if (write(c, "more", 4) != 4 || !wait_readable(alias, 3000) || read(alias, buf, sizeof buf) != 4) FAIL("alias broken by slot reuse");
	close(reuse);
	// The last close is the one that closes the socket: only now does the peer see EOF.
	if (wait_readable(c, 150)) FAIL("peer saw EOF/data while an alias was still open");
	if (close(alias) != 0) FAIL("close of the alias");
	if (!wait_readable(c, 3000) || read(c, buf, sizeof buf) != 0) FAIL("peer did not see EOF after the last close");
	printf("socket closed once, by the last reference\n");
	close(c);

	// ---- readiness belongs to the description: both fds of a pair see it ----
	int c2 = socket(2, 1, 0);
	if (connect(c2, &a, sizeof a) != 0 || !wait_readable(l, 3000)) FAIL("second connect");
	int s2 = accept(l, NULL, NULL);
	if (dup2(s2, 1001) != 1001) FAIL("dup2 #2");
	write(c2, "x", 1);
	unsigned char r[128];
	memset(r, 0, sizeof r);
	setbit(r, s2); setbit(r, 1001);
	kml_timeval tv = { 3, 0 };
	if (select(1002, r, NULL, NULL, &tv) != 2 || !isset(r, s2) || !isset(r, 1001)) FAIL("both aliases should report readable");
	printf("readiness seen through both aliases\n");
	close(s2); close(1001); close(c2); close(l);

	// ---- pipes are pool fds, and alias the same way ----
	int p[2];
	if (pipe(p) != 0) FAIL("pipe");
	if (p[0] < 512 || p[1] < 512) FAIL("pipe fds %d,%d are not pool fds", p[0], p[1]);
	if (isatty(p[0])) FAIL("isatty(pipe)");
	if (dup2(p[1], 1002) != 1002) FAIL("dup2(pipe)");
	close(p[1]);
	if (write(1002, "pipe", 4) != 4) FAIL("write through the pipe alias");
	if (!wait_readable(p[0], 3000) || read(p[0], buf, sizeof buf) != 4 || memcmp(buf, "pipe", 4)) FAIL("pipe read");
	if (wait_readable(p[0], 150)) FAIL("pipe reader saw EOF while a write alias was still open");
	close(1002);
	if (!wait_readable(p[0], 3000) || read(p[0], buf, sizeof buf) != 0) FAIL("pipe reader did not see EOF after the last close");
	close(p[0]);
	printf("pipe alias: write end closed once, by the last reference\n");

	// ---- one 512-slot pool: more than the old 384-socket cap, then EMFILE ----
	static int fds[600];
	int n = 0;
	for (; n < 600; n++) {
		fds[n] = socket(2, 2, 0);
		if (fds[n] < 0) break;
		if (fds[n] < 512 || fds[n] > 1023) FAIL("socket fd %d outside the pool", fds[n]);
	}
	if (n != 512) FAIL("opened %d sockets, want exactly the 512-slot pool", n);
	if (errno != 24 /* EMFILE, Linux value */) FAIL("pool exhaustion errno=%d, want EMFILE", errno);
	int pp[2];
	if (pipe(pp) == 0) FAIL("pipe() succeeded with the pool full");
	for (int i = 0; i < n; i++) if (close(fds[i]) != 0) FAIL("close #%d", i);
	if (pipe(pp) != 0) FAIL("pipe() after the pool drained");
	close(pp[0]); close(pp[1]);
	printf("opened %d sockets from the shared pool, then EMFILE\n", n);

	printf("OK\n");
	return 0;
}
