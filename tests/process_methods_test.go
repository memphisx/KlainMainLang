package tests

import "testing"

// process's methods in TypeScript (lib/node/internal_process_methods.ts,
// TDD-00230): Node's errors for chdir, kill and hrtime, the methods on
// `process` read as a value, and a tuple read through `any`.

func TestE2EProcessMethodsErrors(t *testing.T) {
	assertOutput(t, `
process.chdir('/')
console.log(process.cwd())
try { process.chdir('no-such-dir-kml') } catch (e: any) { console.log(e.message, e.errno, e.code, e.syscall, e.path, e.dest) }
try { process.kill(1, 'SIGNOPE') } catch (e: any) { console.log(e.name, e.code, e.message) }
console.log(process.kill(process.pid, 0))
try { process.hrtime([1] as any) } catch (e: any) { console.log(e.code, e.message) }
try { process.hrtime(5 as any) } catch (e: any) { console.log(e.code, e.message) }
const old = process.umask('027')
console.log(process.umask(old).toString(8))
try { process.umask('9x') } catch (e: any) { console.log(e.code) }
`, `/
ENOENT: no such file or directory, chdir '/' -> 'no-such-dir-kml' -2 ENOENT chdir / no-such-dir-kml
TypeError ERR_UNKNOWN_SIGNAL Unknown signal: SIGNOPE
true
ERR_OUT_OF_RANGE The value of "time" is out of range. It must be 2. Received 1
ERR_INVALID_ARG_TYPE The "time" argument must be an instance of Array. Received type number (5)
27
ERR_INVALID_ARG_VALUE`)
}

func TestE2EProcessMethodsAsValue(t *testing.T) {
	assertOutput(t, `
const p: any = process
const t = p.hrtime()
console.log(typeof p.cwd(), t.length, typeof p.hrtime.bigint(), p.memoryUsage.rss() > 0, p.pid === process.pid)
const m = process.memoryUsage()
console.log(Object.keys(m).join(), m.heapUsed > 0, process.uptime() >= 0, process.ppid > 0)
`, `string 2 bigint true true
rss,heapTotal,heapUsed,external,arrayBuffers true true true`)
}

func TestE2ETupleThroughAny(t *testing.T) {
	assertOutput(t, `
function yes(): true { return true }
const tup: [number, string] = [1, 'a']
const a: any = tup
console.log(Array.isArray(a), a.length, a[1], JSON.stringify(a), a, yes())
`, `true 2 a [1,"a"] [ 1, 'a' ] true`)
}

func TestE2EProcessArgvEnvExit(t *testing.T) {
	assertOutput(t, `
console.log(process.argv[0] === process.execPath, process.argv[1] === process.execPath, typeof process.argv[99], typeof process.argv0)
console.log(typeof process.env.KML_MISSING_VAR, process.env['KML_MISSING_VAR'] === undefined)
process.env.KML_T = 'x'
process.env['KML_U'] = 5 as any
process.env.KML_T += 'y'
console.log(process.env.KML_T, process.env.KML_U, typeof process.env.KML_U)
delete process.env.KML_T
console.log(process.env.KML_T, process.exitCode)
try { process.exitCode = 1.5 } catch (e: any) { console.log(e.code, e.message) }
try { process.exitCode = 'x' as any } catch (e: any) { console.log(e.code, e.message) }
process.on('exit', (c) => { console.log('exit', c, process.exitCode) })
process.exit('0' as any)
console.log('unreachable')
`, `true true undefined string
undefined true
xy 5 string
undefined undefined
ERR_OUT_OF_RANGE The value of "code" is out of range. It must be an integer. Received 1.5
ERR_INVALID_ARG_TYPE The "code" argument must be of type number. Received type string ('x')
exit 0 0`)
}
