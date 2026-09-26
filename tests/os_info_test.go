package tests

import "testing"

// os.type/release/version/machine/arch/endianness/devNull/uptime/loadavg/
// userInfo/availableParallelism/networkInterfaces and the bare `process.env`
// value (ADR-01077). Node is the oracle wherever the answer is host state.

func TestE2EOSInfoStringsSameAsNode(t *testing.T) {
	assertSameAsNodeImports(t, `
import os from 'os'
console.log(os.type(), os.arch(), os.endianness(), os.devNull)
console.log(os.release())
console.log(os.version())
console.log(os.machine())
console.log(os.loadavg().length)
`)
}

func TestE2EOSUptimeAndParallelism(t *testing.T) {
	assertOutputImports(t, `
import os from 'os'
const up = os.uptime()
console.log(typeof up, up > 0)
console.log(os.availableParallelism() >= 1)
const l = os.loadavg()
console.log(l.length, l[0] >= 0, l[1] >= 0, l[2] >= 0)
`, "number true\ntrue\n3 true true true")
}

func TestE2EOSUserInfoSameAsNode(t *testing.T) {
	assertSameAsNodeImports(t, `
import os from 'os'
const u = os.userInfo()
console.log(u.uid, u.gid, u.username, u.homedir, u.shell)
console.log(u.homedir === os.homedir())
`)
}

func TestE2EOSNetworkInterfacesSameAsNode(t *testing.T) {
	assertSameAsNodeImports(t, `
import os from 'os'
const nis = os.networkInterfaces()
for (const name of Object.keys(nis)) {
  for (const ni of nis[name]!) {
    console.log(name, ni.family, ni.address, ni.netmask, ni.mac, ni.internal, ni.cidr, ni.scopeid)
  }
}
`)
}

func TestE2EOSNetworkInterfacesJSONShape(t *testing.T) {
	// IPv6 entries carry scopeid, IPv4 entries do not; every loopback entry
	// is internal. Compared field-for-field against Node's own JSON.
	assertSameAsNodeImports(t, `
import os from 'os'
const nis = os.networkInterfaces()
const names = Object.keys(nis)
for (const name of names) {
  console.log(name, JSON.stringify(nis[name]))
}
`)
}

func TestE2EProcessEnvEnumeration(t *testing.T) {
	assertOutput(t, `
process.env.KML_ENUM_A = "1"
process.env.KML_ENUM_B = "two"
const keys = Object.keys(process.env)
console.log(keys.includes("KML_ENUM_A"), keys.includes("KML_ENUM_B"), keys.length > 2)
let seen = 0
for (const k in process.env) { if (k.startsWith("KML_ENUM_")) seen++ }
console.log(seen)
const copy = { ...process.env, KML_ENUM_C: "3" }
console.log(copy.KML_ENUM_A, copy.KML_ENUM_B, copy.KML_ENUM_C)
const env = process.env
console.log(env.KML_ENUM_B, env["KML_ENUM_A"], env.KML_ENUM_MISSING)
delete process.env.KML_ENUM_A
console.log(Object.keys(process.env).includes("KML_ENUM_A"))
`, "true true true\n2\n1 two 3\ntwo 1 undefined\nfalse")
}

// A JavaScript program (tsc rejects the number store and the
// possibly-undefined reads), so the -compat=js lane.
func TestE2EProcessEnvValuesAreStrings(t *testing.T) {
	assertOutputCompatJS(t, `
process.env.KML_ENUM_N = 42
const env = process.env
console.log(typeof env.KML_ENUM_N, env.KML_ENUM_N.length)
for (const [k, v] of Object.entries(process.env)) {
  if (k === "KML_ENUM_N") console.log(k, v, typeof v)
}
let found = false
for (const v of Object.values(process.env)) { if (v === "42") found = true }
console.log(found)
`, "string 2\nKML_ENUM_N 42 string\ntrue")
}

// for…of over an `any` array (the shape os.networkInterfaces() values have),
// including a boxed static array and the non-iterable TypeError.
func TestE2EForOfOverAny(t *testing.T) {
	assertSameAsNode(t, `
const a: any = [1, "two", true, null]
for (const x of a) console.log(typeof x, x)
const nested: any = { list: [10, 20] }
let sum = 0
for (const n of nested.list) sum += n
console.log(sum)
const objs: any = [{ k: 1 }, { k: 2 }]
for (const { k } of objs) console.log(k)
const pairs: any = { a: 1, b: "x" }
for (const [k, v] of Object.entries(pairs)) console.log(k, v)
for (const v of Object.values(pairs)) console.log(v)
const dynArr: any = [7, 8]
console.log(JSON.stringify(Object.entries(pairs)), JSON.stringify(Object.entries(dynArr)), JSON.stringify(Object.values(dynArr)))
try {
  const notIt: any = 5
  for (const z of notIt) console.log(z)
} catch (e) {
  console.log((e as Error).name, (e as Error).message)
}
`)
}
