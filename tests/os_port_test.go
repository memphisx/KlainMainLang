package tests

import "testing"

// The os module is Node's lib/os.js in TypeScript (lib/node/os.ts): its
// output is compared with Node's on the same host.
func TestE2EOSPortMatchesNode(t *testing.T) {
	assertSameAsNodeImports(t, `
import os from "os"
import { constants, getPriority, setPriority, userInfo, networkInterfaces, EOL } from "node:os"
console.log(Object.keys(os.constants), constants.UV_UDP_REUSEADDR)
console.log(constants.dlopen, constants.priority)
console.log(Object.keys(constants.signals).length, Object.keys(constants.errno).length, Object.isFrozen(constants.signals), constants.signals.SIGINT, constants.errno.ENOENT)
console.log(JSON.stringify(Object.keys(constants.signals)))
console.log(JSON.stringify(constants.errno).length)
console.log(getPriority(), typeof getPriority(process.pid))
try { getPriority(999999) } catch (e: any) { console.log(e.name, e.code, e.message, e.errno, e.syscall, JSON.stringify(e.info)) }
try { getPriority("x" as any) } catch (e: any) { console.log(e.code, e.message) }
try { setPriority(0, 50) } catch (e: any) { console.log(e.code, e.message) }
try { getPriority(1.5) } catch (e: any) { console.log(e.code, e.message) }
setPriority(0, 5); console.log(getPriority())
const u = userInfo({ encoding: "buffer" })
console.log(Buffer.isBuffer(u.username), u.shell !== null, Object.keys(userInfo()))
console.log(userInfo({ encoding: "hex" }).username === Buffer.from(userInfo().username).toString("hex"))
const ni = networkInterfaces()
const lo = ni.lo0 || ni.lo
console.log(Object.keys(lo[0]), lo.map((x: any) => x.family + " " + x.cidr + " " + x.internal + " " + (x.scopeid ?? "-")))
console.log(JSON.stringify(EOL), typeof os.hostname, os.uptime() > 0, os.loadavg().every((x) => typeof x === "number"))
console.log(os.cpus()[0].times.idle > 0, os.release() === os.release(), os.version().length > 0)
`)
}
