// osinfo.c — the host-information runtime behind the `os` module's
// type/release/version/machine/uptime/loadavg/userInfo/availableParallelism/
// networkInterfaces and `process.env` enumeration. Everything here is read
// against the platform's own headers so no struct layout (utsname, passwd,
// ifaddrs, IP_ADAPTER_ADDRESSES) is reproduced in IR; the IR side receives
// plain C strings and flat int64/double values and wraps them itself.
//
// Every returned string is malloc'd (or a string literal for os.type, which
// the IR copies anyway) and never freed here: the caller wraps it into a
// header string at once, and under -mm=gc the allocator shim collects it.

#ifndef _WIN32
#define _GNU_SOURCE 1 // sched_getaffinity / CPU_COUNT (glibc), getloadavg
#endif
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
	char *name;     // interface name ("lo", "eth0", "Ethernet")
	char *address;  // textual IPv4/IPv6 address
	char *netmask;  // textual netmask
	int64_t family; // 4 or 6
	char *mac;      // "aa:bb:cc:dd:ee:ff"
	int64_t internal;
	int64_t scopeid; // IPv6 only (0 for IPv4)
	int64_t prefix;  // CIDR prefix length, -1 when the netmask is not contiguous
} kml_ifaddr;

static char *dupstr(const char *s) {
	size_t n = strlen(s) + 1;
	char *d = (char *)malloc(n);
	if (d) memcpy(d, s, n);
	return d;
}

// Prefix length of a contiguous netmask (Node's getCIDR), -1 when the set
// bits are not a prefix (Node then reports `cidr: null`).
static int64_t mask_prefix(const unsigned char *m, size_t n) {
	int64_t bits = 0;
	size_t i = 0;
	for (; i < n && m[i] == 0xff; i++) bits += 8;
	if (i < n) {
		unsigned char b = m[i];
		while (b & 0x80) { bits++; b = (unsigned char)(b << 1); }
		if (b != 0) return -1;
		for (i++; i < n; i++) if (m[i] != 0) return -1;
	}
	return bits;
}

// Textual IPv4/IPv6 from raw network-order bytes (what inet_ntop prints:
// dotted quad; RFC 5952 lowercase hex with the longest zero run collapsed).
// Hand-rolled so the Windows build never references the Winsock prototypes
// the win32 shim owns under their POSIX names.
static void fmt_ip4(const unsigned char *b, char *out) {
	snprintf(out, 64, "%u.%u.%u.%u", b[0], b[1], b[2], b[3]);
}

static void fmt_ip6(const unsigned char *b, char *out) {
	unsigned w[8];
	int i, best = -1, bestlen = 0, cur = -1, curlen = 0;
	for (i = 0; i < 8; i++) w[i] = ((unsigned)b[2 * i] << 8) | b[2 * i + 1];
	for (i = 0; i < 8; i++) {
		if (w[i] == 0) {
			if (cur < 0) { cur = i; curlen = 1; } else curlen++;
			if (curlen > bestlen) { best = cur; bestlen = curlen; }
		} else cur = -1;
	}
	if (bestlen < 2) best = -1;
	char *p = out;
	for (i = 0; i < 8; i++) {
		if (i == best) {
			// "::" = this colon plus the next group's leading one, or an
			// explicit second one when the run closes the address.
			*p++ = ':';
			if (i + bestlen == 8) *p++ = ':';
			i += bestlen - 1;
			continue;
		}
		if (i > 0) *p++ = ':';
		p += sprintf(p, "%x", w[i]);
	}
	*p = 0;
}

static char *mac_string(const unsigned char *p, size_t n) {
	char buf[18];
	if (n < 6) { strcpy(buf, "00:00:00:00:00:00"); return dupstr(buf); }
	snprintf(buf, sizeof buf, "%02x:%02x:%02x:%02x:%02x:%02x", p[0], p[1], p[2], p[3], p[4], p[5]);
	return dupstr(buf);
}

#ifdef _WIN32
// ============================================================ Windows

#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#include <iphlpapi.h>
#include <errno.h>

static char *utf8_of(const wchar_t *w) {
	int n = WideCharToMultiByte(CP_UTF8, 0, w, -1, NULL, 0, NULL, NULL);
	if (n <= 0) return dupstr("");
	char *u = (char *)malloc((size_t)n);
	if (u) WideCharToMultiByte(CP_UTF8, 0, w, -1, u, n, NULL, NULL);
	return u;
}

// process.env enumeration: the live Win32 block (GetEnvironmentStringsW), the
// same source the getenv shim reads, so a variable set moments ago is listed.
// The "=C:=C:\dir" per-drive entries are hidden bookkeeping — Node skips them.
char **__kml_env_entries(void) {
	wchar_t *block = GetEnvironmentStringsW();
	size_t cap = 64, n = 0;
	char **out = (char **)malloc(cap * sizeof(char *));
	if (block && out) {
		for (wchar_t *p = block; *p; p += wcslen(p) + 1) {
			if (*p == L'=') continue;
			if (n + 1 >= cap) { cap *= 2; out = (char **)realloc(out, cap * sizeof(char *)); }
			out[n++] = utf8_of(p);
		}
		FreeEnvironmentStringsW(block);
	}
	if (out) out[n] = NULL;
	return out;
}

const char *__kml_os_type(void) { return "Windows_NT"; }

typedef LONG(WINAPI *RtlGetVersionFn)(PRTL_OSVERSIONINFOW);

static int rtl_version(RTL_OSVERSIONINFOW *v) {
	memset(v, 0, sizeof *v);
	v->dwOSVersionInfoSize = sizeof *v;
	HMODULE nt = GetModuleHandleW(L"ntdll.dll");
	RtlGetVersionFn fn = nt ? (RtlGetVersionFn)(void *)GetProcAddress(nt, "RtlGetVersion") : NULL;
	return fn && fn(v) == 0;
}

// os.release(): "major.minor.build" from RtlGetVersion (the un-shimmed
// kernel version, unlike GetVersionEx's manifest-dependent answer) — libuv's
// uv_os_uname.
char *__kml_os_release(void) {
	RTL_OSVERSIONINFOW v;
	char buf[64];
	if (!rtl_version(&v)) return dupstr("");
	snprintf(buf, sizeof buf, "%lu.%lu.%lu", (unsigned long)v.dwMajorVersion, (unsigned long)v.dwMinorVersion, (unsigned long)v.dwBuildNumber);
	return dupstr(buf);
}

// os.version(): the registry ProductName ("Windows 10 Enterprise"); a build of
// 22000 or later still says "Windows 10" there, so libuv rewrites the prefix to
// "Windows 11" and Node reports that.
char *__kml_os_version(void) {
	HKEY k;
	wchar_t wbuf[256];
	DWORD sz = sizeof wbuf, type = 0;
	if (RegOpenKeyExW(HKEY_LOCAL_MACHINE, L"SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion", 0, KEY_QUERY_VALUE, &k) != ERROR_SUCCESS)
		return dupstr("");
	LONG r = RegQueryValueExW(k, L"ProductName", NULL, &type, (LPBYTE)wbuf, &sz);
	RegCloseKey(k);
	if (r != ERROR_SUCCESS || type != REG_SZ) return dupstr("");
	wbuf[sz / sizeof(wchar_t) < 256 ? sz / sizeof(wchar_t) : 255] = 0;
	RTL_OSVERSIONINFOW v;
	if (rtl_version(&v) && v.dwBuildNumber >= 22000 && wcsncmp(wbuf, L"Windows 10", 10) == 0)
		wbuf[9] = L'1';
	return utf8_of(wbuf);
}

char *__kml_os_machine(void) {
	SYSTEM_INFO si;
	GetNativeSystemInfo(&si);
	switch (si.wProcessorArchitecture) {
	case PROCESSOR_ARCHITECTURE_AMD64: return dupstr("x86_64");
	case PROCESSOR_ARCHITECTURE_ARM64: return dupstr("arm64");
	case PROCESSOR_ARCHITECTURE_INTEL: return dupstr("i386");
	case PROCESSOR_ARCHITECTURE_ARM: return dupstr("arm");
	default: return dupstr("unknown");
	}
}

double __kml_os_uptime(void) { return (double)GetTickCount64() / 1000.0; }

// Windows has no load average; Node reports [0, 0, 0].
void __kml_os_loadavg(double *out) { out[0] = out[1] = out[2] = 0; }

int64_t __kml_os_avail_parallelism(void) {
	DWORD n = GetActiveProcessorCount(ALL_PROCESSOR_GROUPS);
	return n ? (int64_t)n : 1;
}

char *__kml_os_homedir(void); // win32shim.c

// os.userInfo(): uid/gid are -1 on Windows and shell is null (Node); username
// from GetUserNameW, homedir the same answer os.homedir() gives.
int __kml_os_userinfo(int64_t *uid, int64_t *gid, char **username, char **homedir, char **shell) {
	wchar_t wname[257];
	DWORD n = 257;
	*uid = -1; *gid = -1; *shell = NULL;
	if (!GetUserNameW(wname, &n)) return -1;
	*username = utf8_of(wname);
	char *h = __kml_os_homedir();
	*homedir = h ? dupstr(h) : dupstr("");
	return 0;
}

// fs.statfsSync(path): libuv's uv__fs_statfs on Windows — GetDiskFreeSpaceW
// of the path's volume root; type/files/ffree are 0 there, as Node reports.
// out = { type, bsize, frsize, blocks, bfree, bavail, files, ffree }; -1 sets errno
// (Linux numbering, what the IR's fs error path decodes).
int __kml_fs_statfs(const char *path, int64_t *out) {
	int n = MultiByteToWideChar(CP_UTF8, 0, path, -1, NULL, 0);
	wchar_t *w = n > 0 ? (wchar_t *)malloc((size_t)n * sizeof(wchar_t)) : NULL;
	if (!w) { errno = 22; return -1; }
	MultiByteToWideChar(CP_UTF8, 0, path, -1, w, n);
	// The path itself must exist (libuv probes it first): the volume lookup
	// below would happily answer for a missing file on an existing drive.
	if (GetFileAttributesW(w) == INVALID_FILE_ATTRIBUTES) {
		DWORD err = GetLastError();
		free(w);
		errno = (err == ERROR_FILE_NOT_FOUND || err == ERROR_PATH_NOT_FOUND || err == ERROR_INVALID_NAME) ? 2 : (err == ERROR_ACCESS_DENIED ? 13 : 22);
		return -1;
	}
	wchar_t root[MAX_PATH + 1];
	if (!GetVolumePathNameW(w, root, MAX_PATH + 1)) {
		DWORD err = GetLastError();
		free(w);
		errno = (err == ERROR_FILE_NOT_FOUND || err == ERROR_PATH_NOT_FOUND) ? 2 : (err == ERROR_ACCESS_DENIED ? 13 : 22);
		return -1;
	}
	free(w);
	DWORD spc, bps, freec, totalc;
	if (!GetDiskFreeSpaceW(root, &spc, &bps, &freec, &totalc)) {
		DWORD err = GetLastError();
		errno = err == ERROR_ACCESS_DENIED ? 13 : 2;
		return -1;
	}
	out[0] = 0;
	out[1] = (int64_t)spc * bps;
	out[2] = out[1]; // frsize: libuv reports the cluster size for both
	out[3] = totalc;
	out[4] = freec;
	out[5] = freec;
	out[6] = 0;
	out[7] = 0;
	return 0;
}

kml_ifaddr *__kml_os_netifs(int64_t *count) {
	ULONG sz = 15 * 1024, flags = GAA_FLAG_INCLUDE_PREFIX | GAA_FLAG_SKIP_ANYCAST | GAA_FLAG_SKIP_MULTICAST | GAA_FLAG_SKIP_DNS_SERVER;
	IP_ADAPTER_ADDRESSES *aa = NULL;
	ULONG r;
	*count = 0;
	for (int tries = 0; tries < 4; tries++) {
		aa = (IP_ADAPTER_ADDRESSES *)malloc(sz);
		if (!aa) return NULL;
		r = GetAdaptersAddresses(AF_UNSPEC, flags, NULL, aa, &sz);
		if (r != ERROR_BUFFER_OVERFLOW) break;
		free(aa); aa = NULL;
	}
	if (r != NO_ERROR) { free(aa); return NULL; }
	size_t cap = 16, n = 0;
	kml_ifaddr *out = (kml_ifaddr *)malloc(cap * sizeof *out);
	for (IP_ADAPTER_ADDRESSES *a = aa; a; a = a->Next) {
		if (a->OperStatus != IfOperStatusUp || !a->FirstUnicastAddress) continue;
		for (IP_ADAPTER_UNICAST_ADDRESS *u = a->FirstUnicastAddress; u; u = u->Next) {
			struct sockaddr *sa = u->Address.lpSockaddr;
			if (sa->sa_family != AF_INET && sa->sa_family != AF_INET6) continue;
			if (n + 1 > cap) { cap *= 2; out = (kml_ifaddr *)realloc(out, cap * sizeof *out); }
			kml_ifaddr *e = &out[n++];
			char abuf[64], mbuf[64];
			e->name = utf8_of(a->FriendlyName);
			e->internal = a->IfType == IF_TYPE_SOFTWARE_LOOPBACK;
			e->mac = mac_string(a->PhysicalAddress, a->PhysicalAddressLength);
			e->prefix = u->OnLinkPrefixLength;
			if (sa->sa_family == AF_INET) {
				struct sockaddr_in *s4 = (struct sockaddr_in *)sa;
				unsigned char m[4];
				memset(m, 0, sizeof m);
				for (int b = 0; b < e->prefix && b < 32; b++) m[b / 8] |= (unsigned char)(0x80 >> (b % 8));
				fmt_ip4((const unsigned char *)&s4->sin_addr, abuf);
				fmt_ip4(m, mbuf);
				e->family = 4;
				e->scopeid = 0;
			} else {
				struct sockaddr_in6 *s6 = (struct sockaddr_in6 *)sa;
				unsigned char m[16];
				memset(m, 0, sizeof m);
				for (int b = 0; b < e->prefix && b < 128; b++) m[b / 8] |= (unsigned char)(0x80 >> (b % 8));
				fmt_ip6((const unsigned char *)&s6->sin6_addr, abuf);
				fmt_ip6(m, mbuf);
				e->family = 6;
				e->scopeid = s6->sin6_scope_id;
			}
			e->address = dupstr(abuf);
			e->netmask = dupstr(mbuf);
		}
	}
	free(aa);
	*count = (int64_t)n;
	return out;
}

#else
// ============================================================ POSIX

#include <sys/utsname.h>
#include <sys/types.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <net/if.h>
#include <ifaddrs.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include <pwd.h>
#include <unistd.h>
#include <time.h>
#include <errno.h>
#ifdef __linux__
#include <sched.h>
#include <linux/if_packet.h>
#include <sys/vfs.h>
#else
#include <net/if_dl.h>
#include <sys/sysctl.h>
#include <sys/param.h>
#include <sys/mount.h>
#endif

extern char **environ;

// process.env enumeration: a copy of environ (the block getenv/setenv keep
// live), so the enumerated snapshot always agrees with the keyed reads.
char **__kml_env_entries(void) {
	size_t n = 0;
	while (environ && environ[n]) n++;
	char **out = (char **)malloc((n + 1) * sizeof(char *));
	if (!out) return NULL;
	for (size_t i = 0; i < n; i++) out[i] = dupstr(environ[i]);
	out[n] = NULL;
	return out;
}

const char *__kml_os_type(void) {
	static struct utsname u;
	static int have;
	if (!have) { have = uname(&u) == 0; }
	return have ? u.sysname : "";
}

char *__kml_os_release(void) {
	struct utsname u;
	return dupstr(uname(&u) == 0 ? u.release : "");
}

char *__kml_os_version(void) {
	struct utsname u;
	return dupstr(uname(&u) == 0 ? u.version : "");
}

char *__kml_os_machine(void) {
	struct utsname u;
	return dupstr(uname(&u) == 0 ? u.machine : "");
}

// os.uptime(): libuv reads CLOCK_BOOTTIME on Linux (counts suspend) and
// kern.boottime on Darwin.
double __kml_os_uptime(void) {
#ifdef __linux__
	struct timespec ts;
	if (clock_gettime(CLOCK_BOOTTIME, &ts) == 0) return (double)ts.tv_sec + (double)ts.tv_nsec / 1e9;
	if (clock_gettime(CLOCK_MONOTONIC, &ts) == 0) return (double)ts.tv_sec + (double)ts.tv_nsec / 1e9;
	return 0;
#else
	struct timeval boot;
	size_t sz = sizeof boot;
	int mib[2] = {CTL_KERN, KERN_BOOTTIME};
	if (sysctl(mib, 2, &boot, &sz, NULL, 0) != 0) return 0;
	struct timeval now;
	gettimeofday(&now, NULL);
	return (double)(now.tv_sec - boot.tv_sec) + (double)(now.tv_usec - boot.tv_usec) / 1e6;
#endif
}

void __kml_os_loadavg(double *out) {
	out[0] = out[1] = out[2] = 0;
	getloadavg(out, 3);
}

// os.availableParallelism(): the CPUs this process may run on (libuv's
// sched_getaffinity count on Linux), else the online count.
int64_t __kml_os_avail_parallelism(void) {
#ifdef __linux__
	cpu_set_t set;
	CPU_ZERO(&set);
	if (sched_getaffinity(0, sizeof set, &set) == 0) {
		int c = CPU_COUNT(&set);
		if (c > 0) return c;
	}
#endif
	long n = sysconf(_SC_NPROCESSORS_ONLN);
	return n > 0 ? (int64_t)n : 1;
}

// os.userInfo(): the passwd entry of the effective uid (libuv uv_os_get_passwd).
int __kml_os_userinfo(int64_t *uid, int64_t *gid, char **username, char **homedir, char **shell) {
	struct passwd *pw = getpwuid(geteuid());
	if (!pw) return -1;
	*uid = pw->pw_uid;
	*gid = pw->pw_gid;
	*username = dupstr(pw->pw_name ? pw->pw_name : "");
	*homedir = dupstr(pw->pw_dir ? pw->pw_dir : "");
	*shell = pw->pw_shell ? dupstr(pw->pw_shell) : NULL;
	return 0;
}

// fs.statfsSync(path): statfs(2) read against the platform's own struct.
int __kml_fs_statfs(const char *path, int64_t *out) {
	struct statfs s;
	if (statfs(path, &s) != 0) return -1;
	out[0] = (int64_t)s.f_type;
	out[1] = (int64_t)s.f_bsize;
#ifdef __linux__
	out[2] = (int64_t)s.f_frsize;
#else
	out[2] = (int64_t)s.f_bsize; // Darwin's statfs has no fragment size; libuv reports bsize
#endif
	out[3] = (int64_t)s.f_blocks;
	out[4] = (int64_t)s.f_bfree;
	out[5] = (int64_t)s.f_bavail;
	out[6] = (int64_t)s.f_files;
	out[7] = (int64_t)s.f_ffree;
	return 0;
}

// os.networkInterfaces(): getifaddrs, keeping only up+running interfaces with
// an IPv4/IPv6 address (libuv uv_interface_addresses); the link-layer entry
// of the same name supplies the MAC.
kml_ifaddr *__kml_os_netifs(int64_t *count) {
	struct ifaddrs *list = NULL, *ifa;
	*count = 0;
	if (getifaddrs(&list) != 0) return NULL;
	size_t cap = 16, n = 0;
	kml_ifaddr *out = (kml_ifaddr *)malloc(cap * sizeof *out);
	for (ifa = list; ifa; ifa = ifa->ifa_next) {
		if (!ifa->ifa_addr || !ifa->ifa_netmask) continue;
		if (!(ifa->ifa_flags & IFF_UP) || !(ifa->ifa_flags & IFF_RUNNING)) continue;
		int fam = ifa->ifa_addr->sa_family;
		if (fam != AF_INET && fam != AF_INET6) continue;
		if (n + 1 > cap) { cap *= 2; out = (kml_ifaddr *)realloc(out, cap * sizeof *out); }
		kml_ifaddr *e = &out[n++];
		char abuf[64], mbuf[64];
		e->name = dupstr(ifa->ifa_name);
		e->internal = (ifa->ifa_flags & IFF_LOOPBACK) != 0;
		e->mac = NULL;
		if (fam == AF_INET) {
			struct sockaddr_in *s4 = (struct sockaddr_in *)ifa->ifa_addr, *m4 = (struct sockaddr_in *)ifa->ifa_netmask;
			inet_ntop(AF_INET, &s4->sin_addr, abuf, sizeof abuf);
			inet_ntop(AF_INET, &m4->sin_addr, mbuf, sizeof mbuf);
			e->family = 4;
			e->scopeid = 0;
			e->prefix = mask_prefix((const unsigned char *)&m4->sin_addr, 4);
		} else {
			struct sockaddr_in6 *s6 = (struct sockaddr_in6 *)ifa->ifa_addr, *m6 = (struct sockaddr_in6 *)ifa->ifa_netmask;
			inet_ntop(AF_INET6, &s6->sin6_addr, abuf, sizeof abuf);
			inet_ntop(AF_INET6, &m6->sin6_addr, mbuf, sizeof mbuf);
			e->family = 6;
			e->scopeid = s6->sin6_scope_id;
			e->prefix = mask_prefix((const unsigned char *)&m6->sin6_addr, 16);
		}
		e->address = dupstr(abuf);
		e->netmask = dupstr(mbuf);
	}
	// Second pass: MAC addresses from the link-layer entries.
	for (ifa = list; ifa; ifa = ifa->ifa_next) {
		if (!ifa->ifa_addr) continue;
		const unsigned char *hw = NULL;
		size_t hwlen = 0;
#ifdef __linux__
		if (ifa->ifa_addr->sa_family == AF_PACKET) {
			struct sockaddr_ll *ll = (struct sockaddr_ll *)ifa->ifa_addr;
			hw = ll->sll_addr; hwlen = ll->sll_halen;
		}
#else
		if (ifa->ifa_addr->sa_family == AF_LINK) {
			struct sockaddr_dl *dl = (struct sockaddr_dl *)ifa->ifa_addr;
			hw = (const unsigned char *)LLADDR(dl); hwlen = dl->sdl_alen;
		}
#endif
		if (!hw) continue;
		for (size_t i = 0; i < n; i++)
			if (!out[i].mac && strcmp(out[i].name, ifa->ifa_name) == 0) out[i].mac = mac_string(hw, hwlen);
	}
	for (size_t i = 0; i < n; i++) if (!out[i].mac) out[i].mac = mac_string(NULL, 0);
	freeifaddrs(list);
	*count = (int64_t)n;
	return out;
}

#endif

// ============================================================ lib/node/os.ts
// The natives lib/node/os.ts calls (__kml_native.os*), on every platform:
// libuv's uv_os_*/uv_cpu_info/uv_get_*_memory read against the host. The
// variable-length results (CPUs, interfaces, the passwd entry) are read into
// a snapshot the getters then index. Strings come back as header strings.

#include <errno.h>
#include <signal.h>
#ifdef _WIN32
int __kml_win_cpu_count(void);                                        // win32shim.c
int __kml_win_cpu_info(int i, char *model, int cap, int64_t *mhz, int64_t times[5]);
#else
#include <dlfcn.h>
#include <sys/resource.h>
#ifdef __APPLE__
#include <mach/mach.h>
#endif
#endif

extern char *__kml_str_alloc(int64_t n);
extern void __kml_str_finalize(char *s);

static char *os_kstr(const char *s) {
	if (!s) s = "";
	int64_t n = (int64_t)strlen(s);
	char *out = __kml_str_alloc(n + 1);
	memcpy(out, s, (size_t)n + 1);
	__kml_str_finalize(out);
	return out;
}

// The errno of the last failed os native (0 when it succeeded).
static int os_errno;
double __kml_native_os_errno(void) { return (double)os_errno; }

// uv_os_homedir: a set HOME wins, an empty one included, else the passwd
// entry (USERPROFILE, else the profile directory, on Windows).
static char *os_homedir(void) {
#ifdef _WIN32
	return __kml_os_homedir();
#else
	const char *h = getenv("HOME");
	if (h) return dupstr(h);
	errno = 0;
	struct passwd *pw = getpwuid(geteuid());
	if (!pw || !pw->pw_dir) { os_errno = errno ? errno : ENOENT; return NULL; }
	return dupstr(pw->pw_dir);
#endif
}

// 0 type, 1 version, 2 release, 3 machine, 4 hostname, 5 homedir. A failed
// read (hostname, homedir) sets os_errno and gives "".
char *__kml_native_os_string(double which) {
	os_errno = 0;
	switch ((int)which) {
	case 0: return os_kstr(__kml_os_type());
	case 1: return os_kstr(__kml_os_version());
	case 2: return os_kstr(__kml_os_release());
	case 3: return os_kstr(__kml_os_machine());
	case 4: {
		char buf[256];
		if (gethostname(buf, sizeof buf) != 0) { os_errno = errno ? errno : EIO; return os_kstr(""); }
		buf[sizeof buf - 1] = 0;
		return os_kstr(buf);
	}
	case 5: {
		char *h = os_homedir();
		if (!h && !os_errno) os_errno = ENOENT;
		return os_kstr(h ? h : "");
	}
	}
	return os_kstr("");
}

#ifdef __linux__
// A /proc/meminfo field in bytes (libuv uv__read_proc_meminfo), 0 when absent.
static double os_meminfo(const char *what) {
	FILE *f = fopen("/proc/meminfo", "r");
	if (!f) return 0;
	char line[256];
	size_t wl = strlen(what);
	double v = 0;
	while (fgets(line, sizeof line, f)) {
		if (strncmp(line, what, wl) == 0) {
			unsigned long long kb = 0;
			if (sscanf(line + wl, "%llu", &kb) == 1) v = (double)kb * 1024.0;
			break;
		}
	}
	fclose(f);
	return v;
}
#endif

static double os_totalmem(void) {
#ifdef _WIN32
	MEMORYSTATUSEX m;
	m.dwLength = sizeof m;
	return GlobalMemoryStatusEx(&m) ? (double)m.ullTotalPhys : 0;
#elif defined(__linux__)
	double v = os_meminfo("MemTotal:");
	if (v == 0) v = (double)sysconf(_SC_PHYS_PAGES) * (double)sysconf(_SC_PAGESIZE);
	return v;
#else
	uint64_t mem = 0;
	size_t sz = sizeof mem;
	if (sysctlbyname("hw.memsize", &mem, &sz, NULL, 0) != 0) return 0;
	return (double)mem;
#endif
}

static double os_freemem(void) {
#ifdef _WIN32
	MEMORYSTATUSEX m;
	m.dwLength = sizeof m;
	return GlobalMemoryStatusEx(&m) ? (double)m.ullAvailPhys : 0;
#elif defined(__linux__)
	double v = os_meminfo("MemAvailable:");
	if (v == 0) v = (double)sysconf(_SC_AVPHYS_PAGES) * (double)sysconf(_SC_PAGESIZE);
	return v;
#else
	vm_statistics_data_t info;
	mach_msg_type_number_t count = HOST_VM_INFO_COUNT;
	if (host_statistics(mach_host_self(), HOST_VM_INFO, (host_info_t)&info, &count) != KERN_SUCCESS) return 0;
	return (double)info.free_count * (double)sysconf(_SC_PAGESIZE);
#endif
}

// 0 uptime, 1 totalmem, 2 freemem, 3 availableParallelism, 4-6 loadavg,
// 7 whether the host is big-endian.
double __kml_native_os_number(double which) {
	int w = (int)which;
	switch (w) {
	case 0: return __kml_os_uptime();
	case 1: return os_totalmem();
	case 2: return os_freemem();
	case 3: return (double)__kml_os_avail_parallelism();
	case 4: case 5: case 6: {
		double avg[3];
		__kml_os_loadavg(avg);
		return avg[w - 4];
	}
	case 7: {
		union { uint16_t u; uint8_t b[2]; } probe = {1};
		return probe.b[0] == 0;
	}
	}
	return 0;
}

// ---- cpus (uv_cpu_info) --------------------------------------------------------
typedef struct {
	char *model;
	double speed;
	double times[5]; // user, nice, sys, idle, irq (ms)
} os_cpu;
static os_cpu *os_cpus_snap;
static int os_cpus_n;

#ifdef __linux__
// libuv's ARM part-code table (src/unix/linux.c), "<code>\n<name>\n" per entry.
static const char os_arm_parts[] = "0x811\nARM810\n0x920\nARM920\n0x922\nARM922\n0x926\nARM926\n0x940\nARM940\n0x946\nARM946\n0x966\nARM966\n0xa20\nARM1020\n0xa22\nARM1022\n0xa26\nARM1026\n0xb02\nARM11 MPCore\n0xb36\nARM1136\n0xb56\nARM1156\n0xb76\nARM1176\n0xc05\nCortex-A5\n0xc07\nCortex-A7\n0xc08\nCortex-A8\n0xc09\nCortex-A9\n0xc0d\nCortex-A17\n0xc0f\nCortex-A15\n0xc0e\nCortex-A17\n0xc14\nCortex-R4\n0xc15\nCortex-R5\n0xc17\nCortex-R7\n0xc18\nCortex-R8\n0xc20\nCortex-M0\n0xc21\nCortex-M1\n0xc23\nCortex-M3\n0xc24\nCortex-M4\n0xc27\nCortex-M7\n0xc60\nCortex-M0+\n0xd01\nCortex-A32\n0xd03\nCortex-A53\n0xd04\nCortex-A35\n0xd05\nCortex-A55\n0xd06\nCortex-A65\n0xd07\nCortex-A57\n0xd08\nCortex-A72\n0xd09\nCortex-A73\n0xd0a\nCortex-A75\n0xd0b\nCortex-A76\n0xd0c\nNeoverse-N1\n0xd0d\nCortex-A77\n0xd0e\nCortex-A76AE\n0xd13\nCortex-R52\n0xd20\nCortex-M23\n0xd21\nCortex-M33\n0xd41\nCortex-A78\n0xd42\nCortex-A78AE\n0xd4a\nNeoverse-E1\n0xd4b\nCortex-A78C\n0xd4f\nNeoverse-V2\n";

static char *os_arm_part_name(const char *code) {
	char key[24];
	snprintf(key, sizeof key, "%s\n", code);
	size_t kl = strlen(key);
	const char *p = os_arm_parts;
	while (*p) {
		const char *nl = strchr(p, '\n');
		if (!nl) break;
		const char *name = nl + 1, *end = strchr(name, '\n');
		if (!end) break;
		if ((size_t)(nl + 1 - p) == kl && strncmp(p, key, kl) == 0) {
			size_t n = (size_t)(end - name);
			char *out = (char *)malloc(n + 1);
			memcpy(out, name, n);
			out[n] = 0;
			return out;
		}
		p = end + 1;
	}
	return NULL;
}
#endif

static void os_cpus_read(void) {
	for (int i = 0; i < os_cpus_n; i++) free(os_cpus_snap[i].model);
	free(os_cpus_snap);
	os_cpus_snap = NULL;
	os_cpus_n = 0;
#ifdef _WIN32
	int n = __kml_win_cpu_count();
	if (n <= 0) return;
	os_cpus_snap = (os_cpu *)calloc((size_t)n, sizeof *os_cpus_snap);
	for (int i = 0; i < n; i++) {
		char model[256];
		int64_t mhz = 0, t[5] = {0};
		__kml_win_cpu_info(i, model, sizeof model, &mhz, t);
		os_cpus_snap[i].model = dupstr(model);
		os_cpus_snap[i].speed = (double)mhz;
		for (int k = 0; k < 5; k++) os_cpus_snap[i].times[k] = (double)t[k];
	}
	os_cpus_n = n;
#elif defined(__linux__)
	long clk = sysconf(_SC_CLK_TCK);
	if (clk <= 0) clk = 100;
	FILE *f = fopen("/proc/stat", "r");
	if (!f) return;
	char line[1024];
	int cap = 16, n = 0;
	os_cpus_snap = (os_cpu *)calloc((size_t)cap, sizeof *os_cpus_snap);
	while (fgets(line, sizeof line, f)) {
		unsigned cpu;
		unsigned long long user, nice, sys, idle, iowait, irq;
		if (strncmp(line, "cpu", 3) != 0 || line[3] < '0' || line[3] > '9') continue;
		if (sscanf(line, "cpu%u %llu %llu %llu %llu %llu %llu", &cpu, &user, &nice, &sys, &idle, &iowait, &irq) != 7) continue;
		if (n == cap) { cap *= 2; os_cpus_snap = (os_cpu *)realloc(os_cpus_snap, (size_t)cap * sizeof *os_cpus_snap); }
		os_cpu *c = &os_cpus_snap[n++];
		memset(c, 0, sizeof *c);
		c->times[0] = (double)(user * 1000 / clk);
		c->times[1] = (double)(nice * 1000 / clk);
		c->times[2] = (double)(sys * 1000 / clk);
		c->times[3] = (double)(idle * 1000 / clk);
		c->times[4] = (double)(irq * 1000 / clk);
		// libuv reads cpufreq's scaling_max_freq (kHz); 0 where there is none.
		char path[96];
		snprintf(path, sizeof path, "/sys/devices/system/cpu/cpu%u/cpufreq/scaling_max_freq", cpu);
		FILE *ff = fopen(path, "r");
		if (ff) {
			long long khz = 0;
			if (fscanf(ff, "%lld", &khz) == 1) c->speed = (double)(khz / 1000);
			fclose(ff);
		}
	}
	fclose(f);
	os_cpus_n = n;
	// Models: one per "processor" block of /proc/cpuinfo, its "model name",
	// else its ARM "CPU part" through libuv's table, else "unknown".
	f = fopen("/proc/cpuinfo", "r");
	int blk = -1;
	char *model = NULL;
	if (f) {
		while (fgets(line, sizeof line, f)) {
			if (strncmp(line, "processor\t:", 11) == 0) {
				if (blk >= 0 && blk < n) os_cpus_snap[blk].model = model;
				else free(model);
				model = NULL;
				blk++;
				continue;
			}
			char *colon = strchr(line, ':');
			if (!colon || blk < 0) continue;
			char *val = colon + 1;
			while (*val == ' ' || *val == '\t') val++;
			val[strcspn(val, "\n")] = 0;
			if (strncmp(line, "model name", 10) == 0) {
				free(model);
				model = dupstr(val);
			} else if (!model && strncmp(line, "CPU part\t:", 10) == 0) {
				model = os_arm_part_name(val);
			}
		}
		fclose(f);
		if (blk >= 0 && blk < n) os_cpus_snap[blk].model = model;
		else free(model);
	}
	for (int i = 0; i < n; i++) if (!os_cpus_snap[i].model) os_cpus_snap[i].model = dupstr("unknown");
#else
	char brand[256] = "";
	size_t bsz = sizeof brand;
	if (sysctlbyname("machdep.cpu.brand_string", brand, &bsz, NULL, 0) != 0) brand[0] = 0;
	uint64_t freq = 0;
	size_t fsz = sizeof freq;
	if (sysctlbyname("hw.cpufrequency", &freq, &fsz, NULL, 0) != 0) freq = 0;
	// Apple silicon has no hw.cpufrequency; libuv reports 2400 there.
	double speed = freq ? (double)(freq / 1000000) : 2400;
	natural_t count = 0;
	processor_cpu_load_info_data_t *info = NULL;
	mach_msg_type_number_t msg = 0;
	if (host_processor_info(mach_host_self(), PROCESSOR_CPU_LOAD_INFO, &count, (processor_info_array_t *)&info, &msg) != KERN_SUCCESS) return;
	long clk = sysconf(_SC_CLK_TCK);
	uint64_t mult = clk > 0 ? (uint64_t)(1000L / clk) : 10;
	os_cpus_snap = (os_cpu *)calloc((size_t)count, sizeof *os_cpus_snap);
	for (natural_t i = 0; i < count; i++) {
		os_cpu *c = &os_cpus_snap[i];
		c->model = dupstr(brand);
		c->speed = speed;
		c->times[0] = (double)(info[i].cpu_ticks[CPU_STATE_USER] * mult);
		c->times[1] = (double)(info[i].cpu_ticks[CPU_STATE_NICE] * mult);
		c->times[2] = (double)(info[i].cpu_ticks[CPU_STATE_SYSTEM] * mult);
		c->times[3] = (double)(info[i].cpu_ticks[CPU_STATE_IDLE] * mult);
		c->times[4] = 0;
	}
	vm_deallocate(mach_task_self(), (vm_address_t)info, msg * sizeof(integer_t));
	os_cpus_n = (int)count;
#endif
}

// Reads the CPUs and returns their count.
double __kml_native_os_cpus(void) {
	os_cpus_read();
	return (double)os_cpus_n;
}
char *__kml_native_os_cpu_model(double i) {
	int k = (int)i;
	return os_kstr(k >= 0 && k < os_cpus_n ? os_cpus_snap[k].model : "");
}
// k: 0 speed, 1-5 user/nice/sys/idle/irq.
double __kml_native_os_cpu_value(double i, double k) {
	int c = (int)i, f = (int)k;
	if (c < 0 || c >= os_cpus_n || f < 0 || f > 5) return 0;
	return f == 0 ? os_cpus_snap[c].speed : os_cpus_snap[c].times[f - 1];
}

// ---- networkInterfaces (uv_interface_addresses) --------------------------------
static kml_ifaddr *os_netifs_snap;
static int64_t os_netifs_n;

// Reads the interface addresses and returns their count.
double __kml_native_os_netifs(void) {
	os_netifs_snap = __kml_os_netifs(&os_netifs_n);
	if (!os_netifs_snap) os_netifs_n = 0;
	return (double)os_netifs_n;
}
// k: 0 name, 1 address, 2 netmask, 3 mac.
char *__kml_native_os_netif_string(double i, double k) {
	int64_t n = (int64_t)i;
	if (n < 0 || n >= os_netifs_n) return os_kstr("");
	kml_ifaddr *e = &os_netifs_snap[n];
	switch ((int)k) {
	case 0: return os_kstr(e->name);
	case 1: return os_kstr(e->address);
	case 2: return os_kstr(e->netmask);
	}
	return os_kstr(e->mac);
}
// k: 0 family (4 or 6), 1 internal, 2 scopeid (-1 for IPv4).
double __kml_native_os_netif_number(double i, double k) {
	int64_t n = (int64_t)i;
	if (n < 0 || n >= os_netifs_n) return 0;
	kml_ifaddr *e = &os_netifs_snap[n];
	switch ((int)k) {
	case 0: return (double)e->family;
	case 1: return (double)e->internal;
	}
	return e->family == 6 ? (double)e->scopeid : -1;
}

// ---- userInfo (uv_os_get_passwd) ------------------------------------------------
static int64_t os_pw_uid, os_pw_gid;
static char *os_pw_str[3]; // username, homedir, shell (NULL: none)

// Reads the effective user's passwd entry: 0, or the errno.
double __kml_native_os_userinfo(void) {
	for (int i = 0; i < 3; i++) { free(os_pw_str[i]); os_pw_str[i] = NULL; }
	errno = 0;
	if (__kml_os_userinfo(&os_pw_uid, &os_pw_gid, &os_pw_str[0], &os_pw_str[1], &os_pw_str[2]) != 0)
		return (double)(errno ? errno : ENOENT);
	return 0;
}
// k: 0 username, 1 homedir, 2 shell.
char *__kml_native_os_userinfo_string(double k) {
	int f = (int)k;
	return os_kstr(f >= 0 && f < 3 ? os_pw_str[f] : "");
}
// k: 0 uid, 1 gid, 2 whether there is a shell.
double __kml_native_os_userinfo_number(double k) {
	switch ((int)k) {
	case 0: return (double)os_pw_uid;
	case 1: return (double)os_pw_gid;
	}
	return os_pw_str[2] ? 1 : 0;
}

// ---- priority (uv_os_getpriority / uv_os_setpriority) --------------------------
// The priority; a failure sets os_errno.
double __kml_native_os_get_priority(double pid) {
	os_errno = 0;
#ifdef _WIN32
	HANDLE h = (int)pid == 0 ? GetCurrentProcess() : OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE, (DWORD)pid);
	if (!h) { os_errno = GetLastError() == ERROR_ACCESS_DENIED ? EACCES : ESRCH; return 0; }
	DWORD cls = GetPriorityClass(h);
	if ((int)pid != 0) CloseHandle(h);
	if (!cls) { os_errno = ESRCH; return 0; }
	switch (cls) {
	case REALTIME_PRIORITY_CLASS: return -20;
	case HIGH_PRIORITY_CLASS: return -14;
	case ABOVE_NORMAL_PRIORITY_CLASS: return -7;
	case NORMAL_PRIORITY_CLASS: return 0;
	case BELOW_NORMAL_PRIORITY_CLASS: return 10;
	}
	return 19;
#else
	errno = 0;
	int r = getpriority(PRIO_PROCESS, (id_t)(int)pid);
	if (r == -1 && errno != 0) { os_errno = errno; return 0; }
	return (double)r;
#endif
}

// 0, or the errno.
double __kml_native_os_set_priority(double pid, double priority) {
	int p = (int)priority;
#ifdef _WIN32
	DWORD cls = p < -14 ? REALTIME_PRIORITY_CLASS : p < -7 ? HIGH_PRIORITY_CLASS : p < 0 ? ABOVE_NORMAL_PRIORITY_CLASS
	          : p < 10 ? NORMAL_PRIORITY_CLASS : p < 19 ? BELOW_NORMAL_PRIORITY_CLASS : IDLE_PRIORITY_CLASS;
	HANDLE h = (int)pid == 0 ? GetCurrentProcess() : OpenProcess(PROCESS_SET_INFORMATION, FALSE, (DWORD)pid);
	if (!h) return GetLastError() == ERROR_ACCESS_DENIED ? EACCES : ESRCH;
	BOOL ok = SetPriorityClass(h, cls);
	if ((int)pid != 0) CloseHandle(h);
	return ok ? 0 : EACCES;
#else
	if (setpriority(PRIO_PROCESS, (id_t)(int)pid, p) != 0) return (double)errno;
	return 0;
#endif
}

// ---- os.constants (node_constants.cc, in its order) -----------------------------
typedef struct { const char *name; double value; } kml_osconst;
static const kml_osconst kml_os_errno[] = {
#ifdef E2BIG
	{"E2BIG", (double)E2BIG},
#endif
#ifdef EACCES
	{"EACCES", (double)EACCES},
#endif
#ifdef EADDRINUSE
	{"EADDRINUSE", (double)EADDRINUSE},
#endif
#ifdef EADDRNOTAVAIL
	{"EADDRNOTAVAIL", (double)EADDRNOTAVAIL},
#endif
#ifdef EAFNOSUPPORT
	{"EAFNOSUPPORT", (double)EAFNOSUPPORT},
#endif
#ifdef EAGAIN
	{"EAGAIN", (double)EAGAIN},
#endif
#ifdef EALREADY
	{"EALREADY", (double)EALREADY},
#endif
#ifdef EBADF
	{"EBADF", (double)EBADF},
#endif
#ifdef EBADMSG
	{"EBADMSG", (double)EBADMSG},
#endif
#ifdef EBUSY
	{"EBUSY", (double)EBUSY},
#endif
#ifdef ECANCELED
	{"ECANCELED", (double)ECANCELED},
#endif
#ifdef ECHILD
	{"ECHILD", (double)ECHILD},
#endif
#ifdef ECONNABORTED
	{"ECONNABORTED", (double)ECONNABORTED},
#endif
#ifdef ECONNREFUSED
	{"ECONNREFUSED", (double)ECONNREFUSED},
#endif
#ifdef ECONNRESET
	{"ECONNRESET", (double)ECONNRESET},
#endif
#ifdef EDEADLK
	{"EDEADLK", (double)EDEADLK},
#endif
#ifdef EDESTADDRREQ
	{"EDESTADDRREQ", (double)EDESTADDRREQ},
#endif
#ifdef EDOM
	{"EDOM", (double)EDOM},
#endif
#ifdef EDQUOT
	{"EDQUOT", (double)EDQUOT},
#endif
#ifdef EEXIST
	{"EEXIST", (double)EEXIST},
#endif
#ifdef EFAULT
	{"EFAULT", (double)EFAULT},
#endif
#ifdef EFBIG
	{"EFBIG", (double)EFBIG},
#endif
#ifdef EHOSTUNREACH
	{"EHOSTUNREACH", (double)EHOSTUNREACH},
#endif
#ifdef EIDRM
	{"EIDRM", (double)EIDRM},
#endif
#ifdef EILSEQ
	{"EILSEQ", (double)EILSEQ},
#endif
#ifdef EINPROGRESS
	{"EINPROGRESS", (double)EINPROGRESS},
#endif
#ifdef EINTR
	{"EINTR", (double)EINTR},
#endif
#ifdef EINVAL
	{"EINVAL", (double)EINVAL},
#endif
#ifdef EIO
	{"EIO", (double)EIO},
#endif
#ifdef EISCONN
	{"EISCONN", (double)EISCONN},
#endif
#ifdef EISDIR
	{"EISDIR", (double)EISDIR},
#endif
#ifdef ELOOP
	{"ELOOP", (double)ELOOP},
#endif
#ifdef EMFILE
	{"EMFILE", (double)EMFILE},
#endif
#ifdef EMLINK
	{"EMLINK", (double)EMLINK},
#endif
#ifdef EMSGSIZE
	{"EMSGSIZE", (double)EMSGSIZE},
#endif
#ifdef EMULTIHOP
	{"EMULTIHOP", (double)EMULTIHOP},
#endif
#ifdef ENAMETOOLONG
	{"ENAMETOOLONG", (double)ENAMETOOLONG},
#endif
#ifdef ENETDOWN
	{"ENETDOWN", (double)ENETDOWN},
#endif
#ifdef ENETRESET
	{"ENETRESET", (double)ENETRESET},
#endif
#ifdef ENETUNREACH
	{"ENETUNREACH", (double)ENETUNREACH},
#endif
#ifdef ENFILE
	{"ENFILE", (double)ENFILE},
#endif
#ifdef ENOBUFS
	{"ENOBUFS", (double)ENOBUFS},
#endif
#ifdef ENODATA
	{"ENODATA", (double)ENODATA},
#endif
#ifdef ENODEV
	{"ENODEV", (double)ENODEV},
#endif
#ifdef ENOENT
	{"ENOENT", (double)ENOENT},
#endif
#ifdef ENOEXEC
	{"ENOEXEC", (double)ENOEXEC},
#endif
#ifdef ENOLCK
	{"ENOLCK", (double)ENOLCK},
#endif
#ifdef ENOLINK
	{"ENOLINK", (double)ENOLINK},
#endif
#ifdef ENOMEM
	{"ENOMEM", (double)ENOMEM},
#endif
#ifdef ENOMSG
	{"ENOMSG", (double)ENOMSG},
#endif
#ifdef ENOPROTOOPT
	{"ENOPROTOOPT", (double)ENOPROTOOPT},
#endif
#ifdef ENOSPC
	{"ENOSPC", (double)ENOSPC},
#endif
#ifdef ENOSR
	{"ENOSR", (double)ENOSR},
#endif
#ifdef ENOSTR
	{"ENOSTR", (double)ENOSTR},
#endif
#ifdef ENOSYS
	{"ENOSYS", (double)ENOSYS},
#endif
#ifdef ENOTCONN
	{"ENOTCONN", (double)ENOTCONN},
#endif
#ifdef ENOTDIR
	{"ENOTDIR", (double)ENOTDIR},
#endif
#ifdef ENOTEMPTY
	{"ENOTEMPTY", (double)ENOTEMPTY},
#endif
#ifdef ENOTSOCK
	{"ENOTSOCK", (double)ENOTSOCK},
#endif
#ifdef ENOTSUP
	{"ENOTSUP", (double)ENOTSUP},
#endif
#ifdef ENOTTY
	{"ENOTTY", (double)ENOTTY},
#endif
#ifdef ENXIO
	{"ENXIO", (double)ENXIO},
#endif
#ifdef EOPNOTSUPP
	{"EOPNOTSUPP", (double)EOPNOTSUPP},
#endif
#ifdef EOVERFLOW
	{"EOVERFLOW", (double)EOVERFLOW},
#endif
#ifdef EPERM
	{"EPERM", (double)EPERM},
#endif
#ifdef EPIPE
	{"EPIPE", (double)EPIPE},
#endif
#ifdef EPROTO
	{"EPROTO", (double)EPROTO},
#endif
#ifdef EPROTONOSUPPORT
	{"EPROTONOSUPPORT", (double)EPROTONOSUPPORT},
#endif
#ifdef EPROTOTYPE
	{"EPROTOTYPE", (double)EPROTOTYPE},
#endif
#ifdef ERANGE
	{"ERANGE", (double)ERANGE},
#endif
#ifdef EROFS
	{"EROFS", (double)EROFS},
#endif
#ifdef ESPIPE
	{"ESPIPE", (double)ESPIPE},
#endif
#ifdef ESRCH
	{"ESRCH", (double)ESRCH},
#endif
#ifdef ESTALE
	{"ESTALE", (double)ESTALE},
#endif
#ifdef ETIME
	{"ETIME", (double)ETIME},
#endif
#ifdef ETIMEDOUT
	{"ETIMEDOUT", (double)ETIMEDOUT},
#endif
#ifdef ETXTBSY
	{"ETXTBSY", (double)ETXTBSY},
#endif
#ifdef EWOULDBLOCK
	{"EWOULDBLOCK", (double)EWOULDBLOCK},
#endif
#ifdef EXDEV
	{"EXDEV", (double)EXDEV},
#endif
#ifdef _WIN32
#ifdef WSAEINTR
	{"WSAEINTR", (double)WSAEINTR},
#endif
#ifdef WSAEBADF
	{"WSAEBADF", (double)WSAEBADF},
#endif
#ifdef WSAEACCES
	{"WSAEACCES", (double)WSAEACCES},
#endif
#ifdef WSAEFAULT
	{"WSAEFAULT", (double)WSAEFAULT},
#endif
#ifdef WSAEINVAL
	{"WSAEINVAL", (double)WSAEINVAL},
#endif
#ifdef WSAEMFILE
	{"WSAEMFILE", (double)WSAEMFILE},
#endif
#ifdef WSAEWOULDBLOCK
	{"WSAEWOULDBLOCK", (double)WSAEWOULDBLOCK},
#endif
#ifdef WSAEINPROGRESS
	{"WSAEINPROGRESS", (double)WSAEINPROGRESS},
#endif
#ifdef WSAEALREADY
	{"WSAEALREADY", (double)WSAEALREADY},
#endif
#ifdef WSAENOTSOCK
	{"WSAENOTSOCK", (double)WSAENOTSOCK},
#endif
#ifdef WSAEDESTADDRREQ
	{"WSAEDESTADDRREQ", (double)WSAEDESTADDRREQ},
#endif
#ifdef WSAEMSGSIZE
	{"WSAEMSGSIZE", (double)WSAEMSGSIZE},
#endif
#ifdef WSAEPROTOTYPE
	{"WSAEPROTOTYPE", (double)WSAEPROTOTYPE},
#endif
#ifdef WSAENOPROTOOPT
	{"WSAENOPROTOOPT", (double)WSAENOPROTOOPT},
#endif
#ifdef WSAEPROTONOSUPPORT
	{"WSAEPROTONOSUPPORT", (double)WSAEPROTONOSUPPORT},
#endif
#ifdef WSAESOCKTNOSUPPORT
	{"WSAESOCKTNOSUPPORT", (double)WSAESOCKTNOSUPPORT},
#endif
#ifdef WSAEOPNOTSUPP
	{"WSAEOPNOTSUPP", (double)WSAEOPNOTSUPP},
#endif
#ifdef WSAEPFNOSUPPORT
	{"WSAEPFNOSUPPORT", (double)WSAEPFNOSUPPORT},
#endif
#ifdef WSAEAFNOSUPPORT
	{"WSAEAFNOSUPPORT", (double)WSAEAFNOSUPPORT},
#endif
#ifdef WSAEADDRINUSE
	{"WSAEADDRINUSE", (double)WSAEADDRINUSE},
#endif
#ifdef WSAEADDRNOTAVAIL
	{"WSAEADDRNOTAVAIL", (double)WSAEADDRNOTAVAIL},
#endif
#ifdef WSAENETDOWN
	{"WSAENETDOWN", (double)WSAENETDOWN},
#endif
#ifdef WSAENETUNREACH
	{"WSAENETUNREACH", (double)WSAENETUNREACH},
#endif
#ifdef WSAENETRESET
	{"WSAENETRESET", (double)WSAENETRESET},
#endif
#ifdef WSAECONNABORTED
	{"WSAECONNABORTED", (double)WSAECONNABORTED},
#endif
#ifdef WSAECONNRESET
	{"WSAECONNRESET", (double)WSAECONNRESET},
#endif
#ifdef WSAENOBUFS
	{"WSAENOBUFS", (double)WSAENOBUFS},
#endif
#ifdef WSAEISCONN
	{"WSAEISCONN", (double)WSAEISCONN},
#endif
#ifdef WSAENOTCONN
	{"WSAENOTCONN", (double)WSAENOTCONN},
#endif
#ifdef WSAESHUTDOWN
	{"WSAESHUTDOWN", (double)WSAESHUTDOWN},
#endif
#ifdef WSAETOOMANYREFS
	{"WSAETOOMANYREFS", (double)WSAETOOMANYREFS},
#endif
#ifdef WSAETIMEDOUT
	{"WSAETIMEDOUT", (double)WSAETIMEDOUT},
#endif
#ifdef WSAECONNREFUSED
	{"WSAECONNREFUSED", (double)WSAECONNREFUSED},
#endif
#ifdef WSAELOOP
	{"WSAELOOP", (double)WSAELOOP},
#endif
#ifdef WSAENAMETOOLONG
	{"WSAENAMETOOLONG", (double)WSAENAMETOOLONG},
#endif
#ifdef WSAEHOSTDOWN
	{"WSAEHOSTDOWN", (double)WSAEHOSTDOWN},
#endif
#ifdef WSAEHOSTUNREACH
	{"WSAEHOSTUNREACH", (double)WSAEHOSTUNREACH},
#endif
#ifdef WSAENOTEMPTY
	{"WSAENOTEMPTY", (double)WSAENOTEMPTY},
#endif
#ifdef WSAEPROCLIM
	{"WSAEPROCLIM", (double)WSAEPROCLIM},
#endif
#ifdef WSAEUSERS
	{"WSAEUSERS", (double)WSAEUSERS},
#endif
#ifdef WSAEDQUOT
	{"WSAEDQUOT", (double)WSAEDQUOT},
#endif
#ifdef WSAESTALE
	{"WSAESTALE", (double)WSAESTALE},
#endif
#ifdef WSAEREMOTE
	{"WSAEREMOTE", (double)WSAEREMOTE},
#endif
#ifdef WSASYSNOTREADY
	{"WSASYSNOTREADY", (double)WSASYSNOTREADY},
#endif
#ifdef WSAVERNOTSUPPORTED
	{"WSAVERNOTSUPPORTED", (double)WSAVERNOTSUPPORTED},
#endif
#ifdef WSANOTINITIALISED
	{"WSANOTINITIALISED", (double)WSANOTINITIALISED},
#endif
#ifdef WSAEDISCON
	{"WSAEDISCON", (double)WSAEDISCON},
#endif
#ifdef WSAENOMORE
	{"WSAENOMORE", (double)WSAENOMORE},
#endif
#ifdef WSAECANCELLED
	{"WSAECANCELLED", (double)WSAECANCELLED},
#endif
#ifdef WSAEINVALIDPROCTABLE
	{"WSAEINVALIDPROCTABLE", (double)WSAEINVALIDPROCTABLE},
#endif
#ifdef WSAEINVALIDPROVIDER
	{"WSAEINVALIDPROVIDER", (double)WSAEINVALIDPROVIDER},
#endif
#ifdef WSAEPROVIDERFAILEDINIT
	{"WSAEPROVIDERFAILEDINIT", (double)WSAEPROVIDERFAILEDINIT},
#endif
#ifdef WSASYSCALLFAILURE
	{"WSASYSCALLFAILURE", (double)WSASYSCALLFAILURE},
#endif
#ifdef WSASERVICE_NOT_FOUND
	{"WSASERVICE_NOT_FOUND", (double)WSASERVICE_NOT_FOUND},
#endif
#ifdef WSATYPE_NOT_FOUND
	{"WSATYPE_NOT_FOUND", (double)WSATYPE_NOT_FOUND},
#endif
#ifdef WSA_E_NO_MORE
	{"WSA_E_NO_MORE", (double)WSA_E_NO_MORE},
#endif
#ifdef WSA_E_CANCELLED
	{"WSA_E_CANCELLED", (double)WSA_E_CANCELLED},
#endif
#ifdef WSAEREFUSED
	{"WSAEREFUSED", (double)WSAEREFUSED},
#endif
#endif
	{NULL, 0}};
static const kml_osconst kml_os_signals[] = {
#ifdef SIGHUP
	{"SIGHUP", (double)SIGHUP},
#endif
#ifdef SIGINT
	{"SIGINT", (double)SIGINT},
#endif
#ifdef SIGQUIT
	{"SIGQUIT", (double)SIGQUIT},
#endif
#ifdef SIGILL
	{"SIGILL", (double)SIGILL},
#endif
#ifdef SIGTRAP
	{"SIGTRAP", (double)SIGTRAP},
#endif
#ifdef SIGABRT
	{"SIGABRT", (double)SIGABRT},
#endif
#ifdef SIGIOT
	{"SIGIOT", (double)SIGIOT},
#endif
#ifdef SIGBUS
	{"SIGBUS", (double)SIGBUS},
#endif
#ifdef SIGFPE
	{"SIGFPE", (double)SIGFPE},
#endif
#ifdef SIGKILL
	{"SIGKILL", (double)SIGKILL},
#endif
#ifdef SIGUSR1
	{"SIGUSR1", (double)SIGUSR1},
#endif
#ifdef SIGSEGV
	{"SIGSEGV", (double)SIGSEGV},
#endif
#ifdef SIGUSR2
	{"SIGUSR2", (double)SIGUSR2},
#endif
#ifdef SIGPIPE
	{"SIGPIPE", (double)SIGPIPE},
#endif
#ifdef SIGALRM
	{"SIGALRM", (double)SIGALRM},
#endif
	{"SIGTERM", (double)SIGTERM},
#ifdef SIGCHLD
	{"SIGCHLD", (double)SIGCHLD},
#endif
#ifdef SIGSTKFLT
	{"SIGSTKFLT", (double)SIGSTKFLT},
#endif
#ifdef SIGCONT
	{"SIGCONT", (double)SIGCONT},
#endif
#ifdef SIGSTOP
	{"SIGSTOP", (double)SIGSTOP},
#endif
#ifdef SIGTSTP
	{"SIGTSTP", (double)SIGTSTP},
#endif
#ifdef SIGBREAK
	{"SIGBREAK", (double)SIGBREAK},
#endif
#ifdef SIGTTIN
	{"SIGTTIN", (double)SIGTTIN},
#endif
#ifdef SIGTTOU
	{"SIGTTOU", (double)SIGTTOU},
#endif
#ifdef SIGURG
	{"SIGURG", (double)SIGURG},
#endif
#ifdef SIGXCPU
	{"SIGXCPU", (double)SIGXCPU},
#endif
#ifdef SIGXFSZ
	{"SIGXFSZ", (double)SIGXFSZ},
#endif
#ifdef SIGVTALRM
	{"SIGVTALRM", (double)SIGVTALRM},
#endif
#ifdef SIGPROF
	{"SIGPROF", (double)SIGPROF},
#endif
#ifdef SIGWINCH
	{"SIGWINCH", (double)SIGWINCH},
#endif
#ifdef SIGIO
	{"SIGIO", (double)SIGIO},
#endif
#ifdef SIGPOLL
	{"SIGPOLL", (double)SIGPOLL},
#endif
#ifdef SIGLOST
	{"SIGLOST", (double)SIGLOST},
#endif
#ifdef SIGPWR
	{"SIGPWR", (double)SIGPWR},
#endif
#ifdef SIGINFO
	{"SIGINFO", (double)SIGINFO},
#endif
#ifdef SIGSYS
	{"SIGSYS", (double)SIGSYS},
#endif
#ifdef SIGUNUSED
	{"SIGUNUSED", (double)SIGUNUSED},
#endif
	{NULL, 0}};
static const kml_osconst kml_os_dlopen[] = {
#ifdef RTLD_LAZY
	{"RTLD_LAZY", (double)RTLD_LAZY},
#endif
#ifdef RTLD_NOW
	{"RTLD_NOW", (double)RTLD_NOW},
#endif
#ifdef RTLD_GLOBAL
	{"RTLD_GLOBAL", (double)RTLD_GLOBAL},
#endif
#ifdef RTLD_LOCAL
	{"RTLD_LOCAL", (double)RTLD_LOCAL},
#endif
#ifdef RTLD_DEEPBIND
	{"RTLD_DEEPBIND", (double)RTLD_DEEPBIND},
#endif
	{NULL, 0}};
static const kml_osconst kml_os_priority[] = {
	{"PRIORITY_LOW", 19}, {"PRIORITY_BELOW_NORMAL", 10}, {"PRIORITY_NORMAL", 0},
	{"PRIORITY_ABOVE_NORMAL", -7}, {"PRIORITY_HIGH", -14}, {"PRIORITY_HIGHEST", -20},
	{NULL, 0}};

// group: 0 errno, 1 signals, 2 priority, 3 dlopen.
static const kml_osconst *os_const_group(int g) {
	switch (g) {
	case 0: return kml_os_errno;
	case 1: return kml_os_signals;
	case 2: return kml_os_priority;
	}
	return kml_os_dlopen;
}
double __kml_native_os_constant_count(double group) {
	const kml_osconst *t = os_const_group((int)group);
	int n = 0;
	while (t[n].name) n++;
	return (double)n;
}
char *__kml_native_os_constant_name(double group, double i) {
	return os_kstr(os_const_group((int)group)[(int)i].name);
}
double __kml_native_os_constant_value(double group, double i) {
	return os_const_group((int)group)[(int)i].value;
}
