// runtime_dns.go — Node `dns`: hostname resolution via getaddrinfo(3).
//
// @__kml_dns_lookup(host) resolves a hostname (or numeric IP) to an IPv4
// dotted-quad string, or null on failure. getaddrinfo is in libSystem on
// Darwin and libc on Linux; the one platform difference is struct addrinfo's
// ai_addr field offset — on Darwin ai_canonname and ai_addr are swapped
// relative to glibc (ai_addr at 32 vs 24), verified directly on the machine
// (arm64 Darwin: sizeof 48, ai_addr at 32) rather than trusted from memory,
// per this project's standing rule. This is the same struct-layout hazard that
// kept the WebSocket client's own resolver Darwin-gated; dns unblocks it here
// by pinning the offset.
package llvm

import (
	"fmt"
	"runtime"
)

// dnsAiAddrOffset returns the byte offset of struct addrinfo's ai_addr pointer.
// glibc order: flags,family,socktype,protocol,addrlen,ai_addr,... → 24.
// Darwin order: ...,addrlen,ai_canonname,ai_addr,... (swapped) → 32. Both
// verified via a compiled offsetof probe.
func dnsAiAddrOffset() int {
	if runtime.GOOS == "darwin" {
		return 32
	}
	return 24
}

func (e *Emitter) ensureDNSRuntime() {
	if e.dnsDeclared {
		return
	}
	e.dnsDeclared = true
	e.ensureMalloc()
	e.ensureMemset()
	e.ensureMemcpy()
	e.ensureSprintf()

	e.emitGlobal("declare i32 @getaddrinfo(ptr noundef, ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare void @freeaddrinfo(ptr noundef)")

	aiAddr := dnsAiAddrOffset()
	fmtIP := e.internString("%u.%u.%u.%u")

	// @__kml_dns_lookup(host): getaddrinfo(host, NULL, {AF_INET, SOCK_STREAM}),
	// read the first result's sockaddr_in.sin_addr (4 bytes at ai_addr+4),
	// format as a dotted quad. Returns a malloc'd string or null on failure.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_dns_lookup(ptr %%host) {
entry:
  %%hints = alloca [48 x i8], align 8
  call ptr @memset(ptr %%hints, i32 0, i64 48)
  %%hf = getelementptr i8, ptr %%hints, i64 4
  store i32 2, ptr %%hf, align 4
  %%hs = getelementptr i8, ptr %%hints, i64 8
  store i32 1, ptr %%hs, align 4
  %%resslot = alloca ptr, align 8
  store ptr null, ptr %%resslot, align 8
  %%rc = call i32 @getaddrinfo(ptr %%host, ptr null, ptr %%hints, ptr %%resslot)
  %%ok = icmp eq i32 %%rc, 0
  br i1 %%ok, label %%extract, label %%fail
extract:
  %%res = load ptr, ptr %%resslot, align 8
  %%aiaddr_p = getelementptr i8, ptr %%res, i64 %d
  %%aiaddr = load ptr, ptr %%aiaddr_p, align 8
  %%sinaddr = getelementptr i8, ptr %%aiaddr, i64 4
  %%b0p = getelementptr i8, ptr %%sinaddr, i64 0
  %%b1p = getelementptr i8, ptr %%sinaddr, i64 1
  %%b2p = getelementptr i8, ptr %%sinaddr, i64 2
  %%b3p = getelementptr i8, ptr %%sinaddr, i64 3
  %%b0 = load i8, ptr %%b0p, align 1
  %%b1 = load i8, ptr %%b1p, align 1
  %%b2 = load i8, ptr %%b2p, align 1
  %%b3 = load i8, ptr %%b3p, align 1
  %%b0i = zext i8 %%b0 to i32
  %%b1i = zext i8 %%b1 to i32
  %%b2i = zext i8 %%b2 to i32
  %%b3i = zext i8 %%b3 to i32
  %%buf = call ptr @malloc(i64 16)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%buf, ptr %s, i32 %%b0i, i32 %%b1i, i32 %%b2i, i32 %%b3i)
  call void @freeaddrinfo(ptr %%res)
  ret ptr %%buf
fail:
  ret ptr null
}`, aiAddr, fmtIP))

	// @__kml_dns_resolve4(host): getaddrinfo then walk the ai_next list (offset
	// 40 on both Darwin and glibc), collecting every result's IPv4 dotted quad
	// into a malloc'd ptr array. Returns the { data, count } aggregate (the
	// os.cpus() array shape) — { null, 0 } on failure.
	e.emitGlobal(fmt.Sprintf(`
define {ptr, i64} @__kml_dns_resolve4(ptr %%host) {
entry:
  %%hints = alloca [48 x i8], align 8
  call ptr @memset(ptr %%hints, i32 0, i64 48)
  %%hf = getelementptr i8, ptr %%hints, i64 4
  store i32 2, ptr %%hf, align 4
  %%hs = getelementptr i8, ptr %%hints, i64 8
  store i32 1, ptr %%hs, align 4
  %%resslot = alloca ptr, align 8
  store ptr null, ptr %%resslot, align 8
  %%rc = call i32 @getaddrinfo(ptr %%host, ptr null, ptr %%hints, ptr %%resslot)
  %%ok = icmp eq i32 %%rc, 0
  br i1 %%ok, label %%count, label %%fail
count:
  %%res = load ptr, ptr %%resslot, align 8
  %%cntslot = alloca i64, align 8
  store i64 0, ptr %%cntslot, align 8
  %%curslot = alloca ptr, align 8
  store ptr %%res, ptr %%curslot, align 8
  br label %%cloop
cloop:
  %%cur = load ptr, ptr %%curslot, align 8
  %%curnull = icmp eq ptr %%cur, null
  br i1 %%curnull, label %%alloc, label %%cinc
cinc:
  %%cn = load i64, ptr %%cntslot, align 8
  %%cn1 = add i64 %%cn, 1
  store i64 %%cn1, ptr %%cntslot, align 8
  %%cnext_p = getelementptr i8, ptr %%cur, i64 40
  %%cnext = load ptr, ptr %%cnext_p, align 8
  store ptr %%cnext, ptr %%curslot, align 8
  br label %%cloop
alloc:
  %%n = load i64, ptr %%cntslot, align 8
  %%bytes = mul i64 %%n, 8
  %%arr = call ptr @malloc(i64 %%bytes)
  store ptr %%res, ptr %%curslot, align 8
  %%islot = alloca i64, align 8
  store i64 0, ptr %%islot, align 8
  br label %%floop
floop:
  %%fcur = load ptr, ptr %%curslot, align 8
  %%fnull = icmp eq ptr %%fcur, null
  br i1 %%fnull, label %%done, label %%fill
fill:
  %%aiaddr_p = getelementptr i8, ptr %%fcur, i64 %d
  %%aiaddr = load ptr, ptr %%aiaddr_p, align 8
  %%sinaddr = getelementptr i8, ptr %%aiaddr, i64 4
  %%f0p = getelementptr i8, ptr %%sinaddr, i64 0
  %%f1p = getelementptr i8, ptr %%sinaddr, i64 1
  %%f2p = getelementptr i8, ptr %%sinaddr, i64 2
  %%f3p = getelementptr i8, ptr %%sinaddr, i64 3
  %%f0 = load i8, ptr %%f0p, align 1
  %%f1 = load i8, ptr %%f1p, align 1
  %%f2 = load i8, ptr %%f2p, align 1
  %%f3 = load i8, ptr %%f3p, align 1
  %%f0i = zext i8 %%f0 to i32
  %%f1i = zext i8 %%f1 to i32
  %%f2i = zext i8 %%f2 to i32
  %%f3i = zext i8 %%f3 to i32
  %%s = call ptr @malloc(i64 16)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%s, ptr %s, i32 %%f0i, i32 %%f1i, i32 %%f2i, i32 %%f3i)
  %%iv = load i64, ptr %%islot, align 8
  %%eslot = getelementptr ptr, ptr %%arr, i64 %%iv
  store ptr %%s, ptr %%eslot, align 8
  %%iv1 = add i64 %%iv, 1
  store i64 %%iv1, ptr %%islot, align 8
  %%fnext_p = getelementptr i8, ptr %%fcur, i64 40
  %%fnext = load ptr, ptr %%fnext_p, align 8
  store ptr %%fnext, ptr %%curslot, align 8
  br label %%floop
done:
  call void @freeaddrinfo(ptr %%res)
  %%agg0 = insertvalue {ptr, i64} undef, ptr %%arr, 0
  %%agg1 = insertvalue {ptr, i64} %%agg0, i64 %%n, 1
  ret {ptr, i64} %%agg1
fail:
  %%z0 = insertvalue {ptr, i64} undef, ptr null, 0
  %%z1 = insertvalue {ptr, i64} %%z0, i64 0, 1
  ret {ptr, i64} %%z1
}`, aiAddr, fmtIP))
}
