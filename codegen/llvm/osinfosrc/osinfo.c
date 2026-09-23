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
