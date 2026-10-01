package tests

import (
	"strings"
	"testing"
)

// The Web Crypto global and the `crypto` module are declared (TDD-00230
// P3.1): their calls type-check, and a wrong use is tsc's type error.
func TestE2EWebCryptoDeclared(t *testing.T) {
	assertSameAsNode(t, `
const id: string = crypto.randomUUID()
const b = crypto.getRandomValues(new Uint8Array(4))
const d = await crypto.subtle.digest("SHA-256", new TextEncoder().encode("x"))
console.log(id.length, b.length, d.byteLength)
const k = await crypto.subtle.generateKey({ name: "HMAC", hash: "SHA-256" }, true, ["sign", "verify"])
const sig = await crypto.subtle.sign("HMAC", k, new TextEncoder().encode("m"))
console.log(sig.byteLength, await crypto.subtle.verify("HMAC", k, sig, new TextEncoder().encode("m")))
`)
}

func TestE2ECryptoModuleDeclared(t *testing.T) {
	assertSameAsNodeImports(t, `
import crypto, { createHash, randomBytes } from 'crypto'
import * as nc from 'node:crypto'
const h: string = createHash("sha256").update("x").digest("hex")
const b: Buffer = crypto.createHmac("sha256", "k").update(Buffer.from("y")).digest()
const r = randomBytes(8)
const u: string = nc.randomUUID()
const { publicKey, privateKey } = crypto.generateKeyPairSync("ec", { namedCurve: "prime256v1", publicKeyEncoding: { type: "spki", format: "pem" }, privateKeyEncoding: { type: "pkcs8", format: "pem" } })
console.log(h.slice(0, 8), b.length, r.length, u.length, publicKey.startsWith("-----BEGIN PUBLIC KEY"), privateKey.length > 100)
`)
	for _, c := range []struct{ src, want string }{
		{"const n: number = crypto.randomUUID()\n", "type 'string' is not assignable to type 'number'"},
		{"import { createHash } from \"crypto\"\nconst n: number = createHash(\"sha256\").digest(\"hex\")\n", "type 'string' is not assignable to type 'number'"},
	} {
		_, err := resolveAndCompile(t, c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: expected %q, got %v", c.src, c.want, err)
		}
	}
}

// A template literal type is the string literals it spells when every hole
// is a literal type, else string.
func TestE2ETemplateLiteralTypes(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"type D = `${\"a\" | \"b\"}-${1 | 2}`\nconst e: D = \"c-1\"\n", "type '\"c-1\"' is not assignable"},
		{"const t: `${string}-x` = \"a-x\"\nconst q: number = t\n", "type 'string' is not assignable to type 'number'"},
	} {
		_, err := resolveAndCompile(t, c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: expected %q, got %v", c.src, c.want, err)
		}
	}
	assertSameAsNode(t, `
type D = `+"`${\"a\" | \"b\"}-${1 | 2}`"+`
const d: D = "b-2"
console.log(d)
`)
}
