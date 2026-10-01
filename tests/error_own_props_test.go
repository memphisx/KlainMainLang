package tests

import "testing"

// An Error's own enumerable properties are an Error subclass's fields and
// the system fields set on it (errno, code, syscall, path, …; node:sqlite's
// code, errcode, errstr; execSync's status, …): JSON.stringify and
// Object.keys list those, never the layout's internal fields, whether the
// error is typed, caught, or held in `any`.
func TestE2EErrorOwnPropertiesJSONAndKeys(t *testing.T) {
	assertSameAsNodeImports(t, `
import fs from "fs"
console.log(JSON.stringify(new Error("boom")), JSON.stringify({ e: new TypeError("t") }))
class M extends Error { code = "X" }
console.log(JSON.stringify(new M("m")))
try { fs.readFileSync("/nope") } catch (e: any) { console.log(JSON.stringify(e)) }
const a: any = new Error("q")
console.log(JSON.stringify(a), JSON.stringify([a]))
`)
	assertSameAsNodeImports(t, `
import fs from "fs"
try { fs.readFileSync("/nope") } catch (e: any) { console.log(typeof e, e instanceof Error, e.code, Object.keys(e)); console.log(JSON.stringify(e)); const x: any = e; console.log(JSON.stringify(x)) }
try { JSON.parse("{") } catch (e: any) { console.log(JSON.stringify(e)) }
`)
	assertSameAsNodeImports(t, `
import { DatabaseSync } from "node:sqlite"
const db = new DatabaseSync(":memory:")
try { db.exec("SELEC 1") } catch (e: any) { console.log(Object.keys(e), JSON.stringify(e)) }
import { execSync } from "child_process"
try { execSync("exit 3", { stdio: "pipe" }) } catch (e: any) { console.log(Object.keys(e)) }
`)
}
