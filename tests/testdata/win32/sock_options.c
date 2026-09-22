// Drives the shim's socket-option and message-flag translation directly. The
// C helpers and the emitted IR speak the Linux ABI (option numbers, MSG_* bits,
// struct layouts); the shim must turn those into Winsock's, or a MSG_PEEK
// consumes its data and SO_SNDBUF sets some unrelated option. No E2E program
// passes these today, so they are exercised through the POSIX-named API.
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
int setsockopt(int, int, int, const void *, int);
int getsockopt(int, int, int, void *, int *);
int64_t recvfrom(int, void *, size_t, int, void *, int *);
int64_t read(int, void *, size_t);
int64_t write(int, const void *, size_t);
int close(int);
typedef struct { int64_t sec, usec; } kml_timeval;
int select(int, unsigned char *, unsigned char *, unsigned char *, kml_timeval *);

static int wait_readable(int fd, int ms) {
	unsigned char r[128];
	memset(r, 0, sizeof r);
	r[fd >> 3] |= (unsigned char)(1u << (fd & 7));
	kml_timeval tv = { ms / 1000, (ms % 1000) * 1000 };
	return select(fd + 1, r, NULL, NULL, &tv) > 0;
}

#define FAIL(...) do { printf("FAIL " __VA_ARGS__); printf("\n"); return 1; } while (0)

int main(void) {
	setvbuf(stdout, NULL, _IONBF, 0);
	struct { unsigned short fam, port; unsigned int addr; char z[8]; } a;
	memset(&a, 0, sizeof a);
	a.fam = 2;
	a.addr = 0x0100007f;
	int l = socket(2, 1, 0);
	if (bind(l, &a, sizeof a) != 0 || listen(l, 8) != 0) FAIL("listen");
	int al = sizeof a;
	getsockname(l, &a, &al);
	int c = socket(2, 1, 0);
	if (connect(c, &a, sizeof a) != 0) FAIL("connect");
	if (!wait_readable(l, 3000)) FAIL("listener never readable");
	int s = accept(l, NULL, NULL);
	if (s < 0) FAIL("accept");

	// ---- MSG_PEEK (Linux 0x2) must not consume ----
	if (write(c, "hello", 5) != 5) FAIL("write");
	if (!wait_readable(s, 3000)) FAIL("never readable");
	char buf[16] = {0};
	if (recvfrom(s, buf, sizeof buf, 0x2, NULL, NULL) != 5 || memcmp(buf, "hello", 5)) FAIL("peek errno=%d", errno);
	memset(buf, 0, sizeof buf);
	if (read(s, buf, sizeof buf) != 5 || memcmp(buf, "hello", 5)) FAIL("read after peek: the peek consumed the data");
	printf("MSG_PEEK left the data in place\n");

	// ---- SO_SNDBUF / SO_RCVBUF by their Linux numbers (7 / 8) ----
	int want = 65536, got = 0, gl = sizeof got;
	if (setsockopt(c, 1, 8, &want, sizeof want) != 0) FAIL("SO_RCVBUF errno=%d", errno);
	if (setsockopt(c, 1, 7, &want, sizeof want) != 0) FAIL("SO_SNDBUF errno=%d", errno);
	printf("SO_SNDBUF/SO_RCVBUF accepted\n");

	// ---- SO_LINGER: the Linux {int,int} layout ----
	int lg[2] = { 1, 0 };
	if (setsockopt(c, 1, 13, lg, sizeof lg) != 0) FAIL("SO_LINGER errno=%d", errno);
	printf("SO_LINGER accepted\n");

	// ---- SO_RCVTIMEO: a Linux timeval becomes milliseconds ----
	kml_timeval tv = { 0, 150000 };
	if (setsockopt(s, 1, 20, &tv, sizeof tv) != 0) FAIL("SO_RCVTIMEO errno=%d", errno);
	printf("SO_RCVTIMEO accepted\n");
	(void)got; (void)gl;

	close(c); close(s); close(l);
	printf("OK\n");
	return 0;
}
