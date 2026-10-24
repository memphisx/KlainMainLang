package tests

import "testing"

// dns is Node's lib/dns.js in TypeScript: lookup/lookupService over the
// pool's getaddrinfo/getnameinfo, resolve* over c-ares, as Node's cares_wrap
// drives it (klaindns.c). Compared with Node on the same host at the same moment;
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

// dnsFakeServer is a TypeScript prelude: a DNS server on 127.0.0.1 that
// answers every question from a fixed zone, so the resolver tests run
// without the network. serve(cb) binds it and calls cb(port).
const dnsFakeServer = `
import dgram from "dgram"
function name(n: string): Buffer {
  const parts: Buffer[] = []
  for (const l of n.split(".")) { if (l) { parts.push(Buffer.from([l.length])); parts.push(Buffer.from(l, "latin1")) } }
  parts.push(Buffer.from([0]))
  return Buffer.concat(parts)
}
function u16(n: number): Buffer { const b = Buffer.alloc(2); b.writeUInt16BE(n, 0); return b }
function u32(n: number): Buffer { const b = Buffer.alloc(4); b.writeUInt32BE(n, 0); return b }
function str(s: string): Buffer { return Buffer.concat([Buffer.from([Buffer.byteLength(s)]), Buffer.from(s)]) }
function ip6(s: string): Buffer {
  const halves = s.split("::")
  const head = halves[0] ? halves[0].split(":") : []
  const tail = halves.length > 1 && halves[1] ? halves[1].split(":") : []
  const groups: string[] = head.slice()
  while (groups.length + tail.length < 8) groups.push("0")
  for (const g of tail) groups.push(g)
  return Buffer.concat(groups.map((g: string) => u16(parseInt(g, 16))))
}
// The zone: name -> [type, ttl, rdata][].
const zone: { [k: string]: [number, number, Buffer][] } = {
  "mail.test": [[15, 300, Buffer.concat([u16(10), name("mx1.mail.test")])], [15, 300, Buffer.concat([u16(20), name("mx2.mail.test")])]],
  "zone.test": [
    [1, 60, Buffer.from([10, 0, 0, 1])],
    [28, 61, ip6("2001:db8::1")],
    [2, 3600, name("ns1.zone.test")], [2, 3600, name("ns2.zone.test")],
    [16, 120, Buffer.concat([str("v=spf1 -all")])], [16, 120, Buffer.concat([str("part one "), str("part two")])],
    [6, 900, Buffer.concat([name("ns1.zone.test"), name("hostmaster.zone.test"), u32(2026101101), u32(7200), u32(3600), u32(1209600), u32(300)])],
    [257, 900, Buffer.concat([Buffer.from([0, 5]), Buffer.from("issueca.test")])],
    [15, 300, Buffer.concat([u16(5), name("mx.zone.test")])],
  ],
  "_sip._udp.zone.test": [[33, 60, Buffer.concat([u16(10), u16(60), u16(5060), name("sip.zone.test")])]],
  "naptr.test": [[35, 60, Buffer.concat([u16(100), u16(10), str("S"), str("SIP+D2U"), str(""), name("_sip._udp.zone.test")])]],
  "www.alias.test": [[5, 30, name("target.alias.test")], [1, 30, Buffer.from([10, 0, 0, 9])]],
  "1.0.0.10.in-addr.arpa": [[12, 60, name("host.zone.test")]],
  "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa": [[12, 60, name("host6.zone.test")]],
  "ttl.test": [[1, 42, Buffer.from([192, 0, 2, 1])], [1, 43, Buffer.from([192, 0, 2, 2])], [28, 44, ip6("2001:db8::2")]],
}
function answer(msg: Buffer): Buffer {
  let off = 12
  const labels: string[] = []
  while (msg[off] !== 0) { labels.push(msg.toString("latin1", off + 1, off + 1 + msg[off])); off += 1 + msg[off] }
  const qname = labels.join(".").toLowerCase()
  const qtype = msg.readUInt16BE(off + 1)
  const question = msg.subarray(12, off + 5)
  const recs = zone[qname]
  const rrs: Buffer[] = []
  if (recs) {
    for (const [type, ttl, rdata] of recs) {
      if (qtype !== 255 && type !== qtype && type !== 5) continue
      rrs.push(Buffer.concat([name(qname === "www.alias.test" && type === 1 ? "target.alias.test" : qname), u16(type), u16(1), u32(ttl), u16(rdata.length), rdata]))
    }
  }
  const rcode = recs ? 0 : 3
  return Buffer.concat([msg.subarray(0, 2), u16(0x8180 | rcode), u16(1), u16(rrs.length), u16(0), u16(0), question, ...rrs])
}
function serve(cb: (port: number) => void) {
  const server = dgram.createSocket("udp4")
  server.on("message", (msg: Buffer, rinfo: any) => server.send(answer(msg), rinfo.port, rinfo.address))
  server.bind(0, "127.0.0.1", () => { server.unref(); cb(server.address().port) })
}
`

func TestE2EDnsPortResolve(t *testing.T) {
	assertSameAsNodeImports(t, dnsFakeServer+`
import dns from "dns"
const out: string[] = []
let pending = 0
function done(label: string, e: any, a: any) {
  out.push(label + " " + (e ? e.code + " " + e.syscall + " " + e.hostname : JSON.stringify(a)))
  if (--pending === 0) { out.sort(); for (const l of out) console.log(l) }
}
function q(label: string, f: (cb: (e: any, a?: any) => void) => void) { pending++; f((e, a) => done(label, e, a)) }
serve((port: number) => {
  dns.setServers(["127.0.0.1:" + port])
  q("mx", (cb) => dns.resolveMx("mail.test", cb))
  q("ns", (cb) => dns.resolveNs("zone.test", cb))
  q("txt", (cb) => dns.resolveTxt("zone.test", cb))
  q("soa", (cb) => dns.resolveSoa("zone.test", cb))
  q("srv", (cb) => dns.resolveSrv("_sip._udp.zone.test", cb))
  q("caa", (cb) => dns.resolveCaa("zone.test", cb))
  q("naptr", (cb) => dns.resolveNaptr("naptr.test", cb))
  q("cname", (cb) => dns.resolveCname("www.alias.test", cb))
  q("a-via-cname", (cb) => dns.resolve4("www.alias.test", { ttl: true }, cb))
  q("rev", (cb) => dns.reverse("10.0.0.1", cb))
  q("rev6", (cb) => dns.reverse("2001:db8::1", cb))
  q("ptr", (cb) => dns.resolvePtr("1.0.0.10.in-addr.arpa", cb))
  q("a-nx", (cb) => dns.resolve4("nonexistent.test", cb))
  q("nodata", (cb) => dns.resolveSrv("mail.test", cb))
  q("r-mx", (cb) => dns.resolve("mail.test", "MX", cb))
  q("a-ttl", (cb) => dns.resolve4("ttl.test", { ttl: true }, cb))
  q("aaaa-ttl", (cb) => dns.resolve6("ttl.test", { ttl: true }, cb))
  q("upper", (cb) => dns.resolve4("TTL.Test", cb))
  q("naptr-keys", (cb) => dns.resolveNaptr("naptr.test", (e, a) => cb(e, a && Object.keys(a[0]))))
  q("soa-keys", (cb) => dns.resolveSoa("zone.test", (e, a) => cb(e, a && Object.keys(a))))
  q("any", (cb) => dns.resolveAny("zone.test", cb))
  q("any-keys", (cb) => dns.resolveAny("zone.test", (e, a) => cb(e, a && a.map((r: any) => Object.keys(r).join(",")))))
  q("p-mx", (cb) => { dns.promises.resolveMx("mail.test").then((r: any) => cb(null, r), cb) })
  q("p-nx", (cb) => { dns.promises.resolve4("nonexistent.test").then((r: any) => cb(null, r), cb) })
  const r = new dns.Resolver({ timeout: 2000, tries: 2 })
  r.setServers(["127.0.0.1:" + port])
  q("r-own", (cb) => r.resolve4("zone.test", cb))
  try { dns.reverse("not-an-ip", () => {}) } catch (e: any) { console.log("ptr-bad", e.code, e.errno, e.syscall, e.message) }
})
`)
}

// setLocalAddress validates as cares_wrap does and binds the queries' source.
func TestE2EDnsPortSetLocalAddress(t *testing.T) {
	assertSameAsNodeImports(t, dnsFakeServer+`
import dns from "dns"
const r = new dns.Resolver()
r.setLocalAddress("0.0.0.0")
r.setLocalAddress("127.0.0.1", "::1")
function t(f: () => void) { try { f() } catch (e: any) { console.log(e.code, e.message) } }
t(() => r.setLocalAddress("x"))
t(() => r.setLocalAddress("1.2.3.4", "5.6.7.8"))
t(() => r.setLocalAddress("::1", "::2"))
t(() => r.setLocalAddress("1.2.3.4", "bogus"))
serve((port: number) => {
  const r2 = new dns.Resolver({ timeout: 2000, tries: 1 })
  r2.setLocalAddress("127.0.0.1")
  r2.setServers(["127.0.0.1:" + port])
  r2.resolve4("zone.test", (e: any, a: any) => console.log(e ? e.code : a))
})
`)
}
