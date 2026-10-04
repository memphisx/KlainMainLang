package tests

import "testing"

// dns is Node's lib/dns.js in TypeScript: lookup/lookupService over the
// pool's getaddrinfo/getnameinfo, resolve* over the native DNS client
// (klaindns.c). Compared with Node on the same host at the same moment;
// multi-record answers are sorted (servers rotate them).
func TestE2EDnsPortLookup(t *testing.T) {
	assertSameAsNodeImports(t, `
import dns from "dns"
const out: string[] = []
let pending = 0
function done(line: string) {
  out.push(line)
  if (--pending === 0) { out.sort(); for (const l of out) console.log(l) }
}
function q(label: string, f: (cb: (...a: any[]) => void) => void) { pending++; f((...a: any[]) => done(label + " " + JSON.stringify(a))) }
q("all", (cb) => dns.lookup("127.0.0.1", { all: true }, cb))
q("v4", (cb) => dns.lookup("127.0.0.1", 4, cb))
q("ip", (cb) => dns.lookup("127.0.0.1", cb))
q("ip6", (cb) => dns.lookup("::1", { all: true }, cb))
q("err", (cb) => dns.lookup("nonexistent.invalid", (e: any) => cb(e.code, e.errno, e.syscall, e.hostname, e.message)))
q("svc", (cb) => dns.lookupService("127.0.0.1", 22, cb))
q("plookup", (cb) => { dns.promises.lookup("127.0.0.1", { family: 4 }).then(cb) })
q("pall", (cb) => { dns.promises.lookup("::1", { all: true, order: "ipv6first" }).then(cb) })
q("r4err", (cb) => dns.resolve4("nonexistent.invalid", (e: any) => cb(e.code, e.errno, e.syscall, e.hostname, e.message)))
console.log(dns.getDefaultResultOrder(), dns.ADDRCONFIG, dns.ALL, dns.V4MAPPED, dns.NOTFOUND, dns.CANCELLED)
console.log(dns.getServers().length > 0, new dns.Resolver({ timeout: 100, tries: 1 }).getServers().length > 0)
const r = new dns.Resolver()
r.setServers(["8.8.8.8", "[2001:4860:4860::8888]:5353", "1.1.1.1:53"])
console.log(r.getServers())
try { r.setServers(["bogus"]) } catch (e: any) { console.log(e.code, e.message) }
try { dns.lookup("x", { family: 5 } as any, () => {}) } catch (e: any) { console.log(e.code, e.message) }
try { new dns.Resolver({ timeout: 1.5 }) } catch (e: any) { console.log(e.code, e.message) }
try { dns.resolve("x", "BOGUS", () => {}) } catch (e: any) { console.log(e.code, e.message) }
dns.setDefaultResultOrder("ipv4first")
console.log(dns.getDefaultResultOrder())
`)
}

func TestE2EDnsPortResolve(t *testing.T) {
	assertSameAsNodeImports(t, `
import dns from "dns"
const out: string[] = []
let pending = 0
function done(label: string, e: any, a: any) {
  out.push(label + " " + (e ? e.code + " " + e.syscall : JSON.stringify(a)))
  if (--pending === 0) { out.sort(); for (const l of out) console.log(l) }
}
function q(label: string, f: (cb: (e: any, a: any) => void) => void) { pending++; f((e, a) => done(label, e, a)) }
q("mx", (cb) => dns.resolveMx("gmail.com", (e, a) => cb(e, a && a.map((r: any) => r.exchange).sort())))
q("ns", (cb) => dns.resolveNs("example.com", (e, a) => cb(e, a && a.sort())))
q("txt", (cb) => dns.resolveTxt("example.com", (e, a) => cb(e, a && a.map((r: string[]) => r.join("")).sort())))
q("soa", (cb) => dns.resolveSoa("example.com", cb))
q("srv", (cb) => dns.resolveSrv("_xmpp-server._tcp.jabber.org", cb))
q("caa", (cb) => dns.resolveCaa("google.com", cb))
q("naptr", (cb) => dns.resolveNaptr("sip2sip.info", (e, a) => cb(e, a && a.map((r: any) => r.service).sort())))
q("cname", (cb) => dns.resolveCname("en.wikipedia.org", cb))
q("rev", (cb) => dns.reverse("8.8.8.8", cb))
q("rev6", (cb) => dns.reverse("2001:4860:4860::8888", cb))
q("ptr", (cb) => dns.resolvePtr("8.8.8.8.in-addr.arpa", cb))
q("a-nx", (cb) => dns.resolve4("nonexistent.invalid", cb))
q("r-mx", (cb) => dns.resolve("gmail.com", "MX", (e: any, a: any) => cb(e, a && a.length)))
q("a-count", (cb) => dns.resolve4("google.com", (e, a) => cb(e, a && a.length > 0)))
q("aaaa-ttl", (cb) => dns.resolve6("google.com", { ttl: true }, (e, a) => cb(e, a && typeof a[0].ttl)))
q("r-default", (cb) => new dns.Resolver().resolve4("dns.google", (e, a) => cb(e, a && a.sort())))
q("naptr-keys", (cb) => dns.resolveNaptr("sip2sip.info", (e, a) => cb(e, a && Object.keys(a[0]))))
q("soa-keys", (cb) => dns.resolveSoa("example.com", (e, a) => cb(e, a && Object.keys(a))))
q("any", (cb) => dns.resolveAny("example.com", cb))
try { dns.reverse("not-an-ip", () => {}) } catch (e: any) { console.log("ptr-bad", e.code, e.errno, e.syscall, e.message) }
q("p-mx", (cb) => { dns.promises.resolveMx("gmail.com").then((r: any) => cb(null, r.length), cb) })
q("p-nx", (cb) => { dns.promises.resolve4("nonexistent.invalid").then((r: any) => cb(null, r), cb) })
`)
}

// setLocalAddress validates as cares_wrap does and binds the queries' source.
func TestE2EDnsPortSetLocalAddress(t *testing.T) {
	assertSameAsNodeImports(t, `
import dns from "dns"
const r = new dns.Resolver()
r.setLocalAddress("0.0.0.0")
r.setLocalAddress("127.0.0.1", "::1")
function t(f: () => void) { try { f() } catch (e: any) { console.log(e.code, e.message) } }
t(() => r.setLocalAddress("x"))
t(() => r.setLocalAddress("1.2.3.4", "5.6.7.8"))
t(() => r.setLocalAddress("::1", "::2"))
t(() => r.setLocalAddress("1.2.3.4", "bogus"))
const r2 = new dns.Resolver({ timeout: 500, tries: 1 })
r2.setLocalAddress("127.0.0.1")
r2.setServers(["1.1.1.1"])
r2.resolve4("example.com", (e: any, a: any) => console.log(e ? e.code : "ok"))
`)
}
