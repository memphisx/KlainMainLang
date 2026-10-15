package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// crypto is Node's lib/crypto.js (and its internal/crypto/*) in TypeScript
// over the crypto backend's natives. Each program runs under Node too and
// must print the same.
const cryptoPortSyncSrc = `
import * as c from 'crypto';
import crypto from 'crypto';
const t = (n: string, f: () => any) => { try { console.log(n, 'ok', f()); } catch (e: any) { console.log(n, e.name, e.code, JSON.stringify(e.message)); } };
t('sha256', () => c.createHash('sha256').update('abc').digest('hex'));
t('md5-b64', () => crypto.createHash('md5').update(Buffer.from('abc')).digest('base64'));
t('hash-bad', () => c.createHash('nope'));
t('hmac-bad', () => c.createHmac('nope', 'k'));
t('twice', () => { const h = c.createHash('sha256'); h.digest(); h.digest(); });
t('hmac', () => c.createHmac('sha256', 'key').update('data').digest('hex'));
t('hmac-twice', () => { const h = c.createHmac('sha256', 'k'); h.digest(); return h.digest('hex'); });
t('upd-num', () => c.createHash('sha256').update(5 as any));
t('shake', () => c.createHash('shake256', { outputLength: 8 }).update('a').digest('hex'));
t('copy', () => { const h = c.createHash('sha1').update('a'); const g = h.copy(); return [h.digest('hex'), g.update('b').digest('hex')]; });
t('pbkdf2', () => c.pbkdf2Sync('pw', 'salt', 1000, 16, 'sha256').toString('hex'));
t('pbkdf2-bad', () => c.pbkdf2Sync('pw', 'salt', 1000, 16, 'nope'));
t('scrypt', () => c.scryptSync('pw', 'salt', 16).toString('hex'));
t('hkdf', () => Buffer.from(c.hkdfSync('sha256', 'key', 'salt', 'info', 16)).toString('hex'));
t('tse', () => c.timingSafeEqual(Buffer.from('a'), Buffer.from('ab')));
t('tse2', () => c.timingSafeEqual(Buffer.from('ab'), Buffer.from('ab')));
t('randInt', () => typeof c.randomInt(10));
t('randBytes', () => c.randomBytes(8).length);
t('cipher', () => { const k = Buffer.alloc(32, 1), iv = Buffer.alloc(16, 2); const e = c.createCipheriv('aes-256-cbc', k, iv); const ct = Buffer.concat([e.update('hello', 'utf8'), e.final()]); const d = c.createDecipheriv('aes-256-cbc', k, iv); return [ct.toString('hex'), Buffer.concat([d.update(ct), d.final()]).toString()]; });
t('cipher-badkey', () => c.createCipheriv('aes-256-cbc', Buffer.alloc(3), Buffer.alloc(16)));
t('cipher-badiv', () => c.createCipheriv('aes-256-cbc', Buffer.alloc(32), Buffer.alloc(3)));
t('cipher-unk', () => c.createCipheriv('nope', Buffer.alloc(32), Buffer.alloc(16)));
t('bad-decrypt', () => { const d = c.createDecipheriv('aes-256-cbc', Buffer.alloc(32), Buffer.alloc(16)); d.update(Buffer.alloc(16)); return d.final(); });
t('gcm', () => { const k = Buffer.alloc(32, 1), iv = Buffer.alloc(12, 2); const e = c.createCipheriv('aes-256-gcm', k, iv); e.setAAD(Buffer.from('aad')); const ct = Buffer.concat([e.update('hi'), e.final()]); const tag = e.getAuthTag(); const d = c.createDecipheriv('aes-256-gcm', k, iv); d.setAAD(Buffer.from('aad')); d.setAuthTag(tag); return [ct.toString('hex'), tag.length, Buffer.concat([d.update(ct), d.final()]).toString()]; });
t('gcm-badtag', () => { const k = Buffer.alloc(32, 1), iv = Buffer.alloc(12, 2); const d = c.createDecipheriv('aes-256-gcm', k, iv); d.setAuthTag(Buffer.alloc(16)); d.update(Buffer.from('ab')); return d.final(); });
t('getHashes', () => c.getHashes().length > 10 && c.getHashes().includes('sha256'));
t('hash1', () => c.hash('sha1', 'abc'));
t('sign', () => { const { publicKey, privateKey } = c.generateKeyPairSync('ec', { namedCurve: 'P-256', publicKeyEncoding: { type: 'spki', format: 'pem' }, privateKeyEncoding: { type: 'pkcs8', format: 'pem' } }); const s = c.createSign('SHA256').update('msg').sign(privateKey); return [publicKey.startsWith('-----BEGIN PUBLIC KEY-----'), c.createVerify('SHA256').update('msg').verify(publicKey, s), c.verify('sha256', Buffer.from('msg'), publicKey, s)]; });
t('ed25519', () => { const { publicKey, privateKey } = c.generateKeyPairSync('ed25519', { publicKeyEncoding: { type: 'spki', format: 'pem' }, privateKeyEncoding: { type: 'pkcs8', format: 'pem' } }); const s = c.sign(null, Buffer.from('m'), privateKey); return [s.length, c.verify(null, Buffer.from('m'), publicKey, s)]; });
t('nopad', () => { const k = Buffer.alloc(16, 3); const e = c.createCipheriv('aes-128-ecb', k, null); e.setAutoPadding(false); const ct = Buffer.concat([e.update(Buffer.alloc(16, 7)), e.final()]); return ct.toString('hex'); });
t('nopad-short', () => { const e = c.createCipheriv('aes-128-ecb', Buffer.alloc(16), null); e.setAutoPadding(false); e.update(Buffer.alloc(5)); return e.final(); });
t('ctr', () => { const e = c.createCipheriv('aes-128-ctr', Buffer.alloc(16, 1), Buffer.alloc(16, 2)); return Buffer.concat([e.update('abc'), e.final()]).toString('hex'); });
t('consts', () => [c.constants.RSA_PKCS1_PADDING, c.constants.RSA_PKCS1_PSS_PADDING, c.constants.defaultCoreCipherList === c.constants.defaultCipherList]);
t('x25519', () => { const k = c.generateKeyPairSync('x25519', { publicKeyEncoding: { type: 'spki', format: 'pem' }, privateKeyEncoding: { type: 'pkcs8', format: 'pem' } }); return [k.publicKey.split('\n').length, k.privateKey.split('\n').length]; });
t('ciphers', () => c.getCiphers().includes('aes-256-gcm'));
t('const-keys', () => Object.keys(c.constants).length);
t('aad-cbc', () => c.createCipheriv('aes-256-cbc', Buffer.alloc(32), Buffer.alloc(16)).setAAD(Buffer.from('x')));
t('settag-cbc', () => c.createDecipheriv('aes-256-cbc', Buffer.alloc(32), Buffer.alloc(16)).setAuthTag(Buffer.alloc(16)));
t('gettag-cbc', () => { const e = c.createCipheriv('aes-256-cbc', Buffer.alloc(32), Buffer.alloc(16)); e.final(); return e.getAuthTag(); });
t('gettag-early', () => c.createCipheriv('aes-256-gcm', Buffer.alloc(32), Buffer.alloc(12)).getAuthTag());
t('hkdf-long', () => c.hkdfSync('sha256', 'k', 's', 'i', 255 * 32 + 1));
t('hkdf-digest', () => c.hkdfSync('nope', 'k', 's', 'i', 8));
t('ec-curve', () => c.generateKeyPairSync('ec', { namedCurve: 'nope', publicKeyEncoding: { type: 'spki', format: 'pem' }, privateKeyEncoding: { type: 'pkcs8', format: 'pem' } }));
t('rsa-small', () => c.generateKeyPairSync('rsa', { modulusLength: 256, publicKeyEncoding: { type: 'spki', format: 'pem' }, privateKeyEncoding: { type: 'pkcs8', format: 'pem' } }));
`

func TestE2ECryptoPortSync(t *testing.T) {
	assertSameAsNodeImports(t, cryptoPortSyncSrc)
}

// The same program on the CommonCrypto backend (macOS), less the SHAKE
// digests CommonCrypto has no implementation of.
func TestE2ECryptoPortCommonCrypto(t *testing.T) {
	var kept []string
	for _, l := range strings.Split(cryptoPortSyncSrc, "\n") {
		if !strings.Contains(l, "shake") {
			kept = append(kept, l)
		}
	}
	src := strings.Join(kept, "\n")
	theirs := runNodeTS(t, src)
	d := tempDir(t)
	srcFile := filepath.Join(d, "main.ts")
	if err := os.WriteFile(srcFile, []byte(src), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	raw, err := exec.Command(buildBinaryFromFileCrypto(t, srcFile, "commoncrypto")).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if ours := strings.TrimRight(string(raw), "\n"); ours != theirs {
		t.Fatalf("output differs from node:\n--- ours ---\n%s\n--- node ---\n%s", ours, theirs)
	}
}

// The same program on the CNG backend (Windows), less the SHAKE digests CNG
// has no implementation of.
func TestE2ECryptoPortCNG(t *testing.T) {
	var kept []string
	for _, l := range strings.Split(cryptoPortSyncSrc, "\n") {
		if !strings.Contains(l, "shake") {
			kept = append(kept, l)
		}
	}
	assertSameAsNodeCrypto(t, strings.Join(kept, "\n"), "cng")
}

// The passphrase-encrypted key program on the CNG backend.
func TestE2ECryptoPortCNGEncryptedKey(t *testing.T) {
	assertSameAsNodeCrypto(t, cryptoPortEncryptedKeySrc, "cng")
}

// RSA (PKCS#1 v1.5, PSS, OAEP, raw private encrypt), EC (DER and P1363
// ECDSA, ECDH with point compression) and KeyObject conversions on the CNG
// backend, against Node: every deterministic output must match byte for byte.
const cryptoPortAsymSrc = `
import * as c from 'crypto';
const t = (n: string, f: () => any) => { try { console.log(n, 'ok', f()); } catch (e: any) { console.log(n, e.name, e.code); } };
const rsaPem = "-----BEGIN PRIVATE KEY-----\nMIICdgIBADANBgkqhkiG9w0BAQEFAASCAmAwggJcAgEAAoGBAKqCn7ZBvK6KkSfI\n4eh0ndntGF22LTemROpnTnMn8pdL1QwTTR20U+9ysHypWyrt2G4I1KpXmpvatCvF\n5sbLY86zAAwhIFePrb+1LJTGvbhCtW7ty7Hcg/Cpo27Hqyi2SDx5GBkt2XzCM5U/\nw99SQZMl1sDaMzsotWXP68rImPqRAgMBAAECgYBZ3+AvQRcjKQa54bixvkY1yDM/\nsfbDjnBk3W8MY9IKzX3Oj53mOlbDr8A29U2IozWhSiZOYz6rR+wP5pBt2GcSgBZZ\nATy/moBaUhHszMmHbYoeg4snumTDemmhhZ5zNfCC90n6i7KY6swNR8CsI4uFoSMO\njpd7LVnRsyBvq4qJwQJBANYURfMLmS+dyIEuek2cI7WtTGdGPu1hspX33YK1kvdC\n2YM1Ev6sCrd+wiWGjBOCwFbsmNZlca3VMK1/Bvc+aY8CQQDL5kCcMW6j4v2Wjg9W\nOUODTBtPUCA/UL0xknXEEfolNCg1dv18gjD23iWZ9VKAnY4PeQofRKTOE8tFY2B7\nqwnfAkAgUbM1vByGScXJauDf8qCZfu438Szy3Xr+lQsF/q+PULJR0qNpe0Oq7CIX\nVDa1g1nSRanVRD9jRPoyVykQDoYVAkAE5NjmJtWV/2vAwlYh5qv1Kc16AN1VR7XS\ny8Yh16CeZfsVFW8OHv3X3wsd/+JkDeQMAQa6K2adP6JgXvtA3KEXAkEAo/3nsWX6\n68AxhQ9HPIyIXkYqgzhSKsnwyHmtm+uJ/lgKV+uM3o5h4/XvMDHHUGVMF22AxP8L\nI51tkfdy43YXYg==\n-----END PRIVATE KEY-----\n";
const ecPem = "-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgWwNx8777t1/hTY9e\nn4Splt03KtI1iGNsuQNgfEmJtJyhRANCAATFE0ME7C53vpp2iWil9U4eTMD+pqe3\nj7T9OuZ3U+bVR6QLYmklhW+EBq9hbnjEFlBckT/G/ZZbXkcPKdE/Peiq\n-----END PRIVATE KEY-----\n";
const rsa = c.createPrivateKey(rsaPem), rsaPub = c.createPublicKey(rsa);
const ec = c.createPrivateKey(ecPem), ecPub = c.createPublicKey(ec);
const m = Buffer.from('message');
t('rsa-type', () => [rsa.asymmetricKeyType, rsa.asymmetricKeyDetails!.modulusLength, String(rsa.asymmetricKeyDetails!.publicExponent)]);
t('rsa-spki', () => rsaPub.export({ type: 'spki', format: 'pem' }));
t('rsa-pkcs1', () => rsa.export({ type: 'pkcs1', format: 'pem' }));
t('rsa-pkcs1-pub', () => (rsaPub.export({ type: 'pkcs1', format: 'der' }) as Buffer).toString('hex'));
t('rsa-jwk', () => JSON.stringify(rsa.export({ format: 'jwk' })));
t('rsa-pkcs1v15', () => c.sign('sha256', m, rsa).toString('hex'));
t('rsa-sha224', () => c.sign('sha224', m, rsa).toString('hex'));
t('rsa-verify', () => c.verify('sha1', m, rsaPub, c.sign('sha1', m, rsa)));
t('rsa-pss', () => { const s = c.sign('sha256', m, { key: rsa, padding: c.constants.RSA_PKCS1_PSS_PADDING }); return [s.length, c.verify('sha256', m, { key: rsaPub, padding: c.constants.RSA_PKCS1_PSS_PADDING }, s)]; });
t('rsa-pss-salt', () => { const s = c.sign('sha256', m, { key: rsa, padding: c.constants.RSA_PKCS1_PSS_PADDING, saltLength: 20 }); return [c.verify('sha256', m, { key: rsaPub, padding: c.constants.RSA_PKCS1_PSS_PADDING, saltLength: 20 }, s), c.verify('sha256', m, { key: rsaPub, padding: c.constants.RSA_PKCS1_PSS_PADDING, saltLength: 32 }, s)]; });
t('rsa-oaep', () => c.privateDecrypt({ key: rsa, oaepHash: 'sha256', oaepLabel: Buffer.from('L') }, c.publicEncrypt({ key: rsaPub, oaepHash: 'sha256', oaepLabel: Buffer.from('L') }, m)).toString());
t('rsa-oaep-sha1', () => c.privateDecrypt(rsa, c.publicEncrypt(rsaPub, m)).toString());
t('rsa-privenc', () => c.privateEncrypt(rsa, m).toString('hex'));
t('rsa-pubdec', () => c.publicDecrypt(rsaPub, c.privateEncrypt(rsa, m)).toString());
t('ec-type', () => [ec.asymmetricKeyType, ec.asymmetricKeyDetails!.namedCurve]);
t('ec-spki', () => ecPub.export({ type: 'spki', format: 'pem' }));
t('ec-sec1', () => ec.export({ type: 'sec1', format: 'pem' }));
t('ec-jwk', () => JSON.stringify(ec.export({ format: 'jwk' })));
t('ec-jwk-back', () => c.createPublicKey({ key: ecPub.export({ format: 'jwk' }), format: 'jwk' }).export({ type: 'spki', format: 'der' }).toString('hex'));
t('ecdsa', () => c.verify('sha384', m, ecPub, c.sign('sha384', m, ec)));
t('ecdsa-p1363', () => { const s = c.sign('sha256', m, { key: ec, dsaEncoding: 'ieee-p1363' }); return [s.length, c.verify('sha256', m, { key: ecPub, dsaEncoding: 'ieee-p1363' }, s)]; });
const d = Buffer.from((ec.export({ format: 'jwk' }) as any).d, 'base64url');
const peer = c.createECDH('prime256v1');
peer.setPrivateKey(c.createHash('sha256').update('peer').digest());
t('ecdh-pub', () => { const e = c.createECDH('prime256v1'); e.setPrivateKey(d); return [e.getPublicKey('hex'), e.getPublicKey('hex', 'compressed')]; });
t('ecdh-secret', () => { const e = c.createECDH('prime256v1'); e.setPrivateKey(d); return e.computeSecret(peer.getPublicKey(null, 'compressed')).toString('hex'); });
t('ecdh-convert', () => c.ECDH.convertKey(peer.getPublicKey('hex', 'compressed'), 'prime256v1', 'hex', 'hex', 'uncompressed') === peer.getPublicKey('hex'));
t('ecdh-badpoint', () => { const e = c.createECDH('prime256v1'); e.setPrivateKey(d); return e.computeSecret(Buffer.alloc(65, 4)); });
t('diffieHellman', () => c.diffieHellman({ privateKey: ec, publicKey: c.createPublicKey({ key: { kty: 'EC', crv: 'P-256', x: peer.getPublicKey().subarray(1, 33).toString('base64url'), y: peer.getPublicKey().subarray(33).toString('base64url') }, format: 'jwk' }) }).toString('hex'));
`

func TestE2ECryptoPortCNGAsymmetric(t *testing.T) {
	assertSameAsNodeCrypto(t, cryptoPortAsymSrc, "cng")
}

// assertSameAsNodeCrypto builds src with a -crypto backend and compares its
// output with Node's.
func assertSameAsNodeCrypto(t *testing.T, src, backend string) {
	t.Helper()
	theirs := runNodeTS(t, src)
	d := tempDir(t)
	srcFile := filepath.Join(d, "main.ts")
	if err := os.WriteFile(srcFile, []byte(src), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	raw, err := exec.Command(buildBinaryFromFileCrypto(t, srcFile, backend)).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if ours := strings.TrimRight(string(raw), "\n"); ours != theirs {
		t.Fatalf("output differs from node:\n--- ours ---\n%s\n--- node ---\n%s", ours, theirs)
	}
}

func TestE2ECryptoPortCallbacks(t *testing.T) {
	assertSameAsNodeImports(t, `
import * as c from 'crypto';
// One request at a time: the pool finishes concurrent ones in any order.
const steps: Array<() => Promise<void>> = [
    () => new Promise((res) => c.randomBytes(4, (err, b) => { console.log('randomBytes', err, b.length); res(); })),
    () => new Promise((res) => c.randomFill(Buffer.alloc(6), 2, 3, (err, b) => { console.log('randomFill', err, b.length, b[0], b[5]); res(); })),
    () => new Promise((res) => c.pbkdf2('pw', 'salt', 1, 8, 'sha1', (err, k) => { console.log('pbkdf2', err, k.toString('hex')); res(); })),
    () => new Promise((res) => c.scrypt('pw', 'salt', 8, (err, k) => { console.log('scrypt', err, k.toString('hex')); res(); })),
    () => new Promise((res) => c.hkdf('sha256', 'key', 'salt', 'info', 8, (err, k) => { console.log('hkdf', err, Buffer.from(k).toString('hex')); res(); })),
    () => new Promise((res) => c.randomInt(5, 6, (err, n) => { console.log('randomInt', err, n); res(); })),
    () => new Promise((res) => c.generateKeyPair('rsa', { modulusLength: 1024, publicKeyEncoding: { type: 'spki', format: 'pem' }, privateKeyEncoding: { type: 'pkcs8', format: 'pem' } }, (err, pub, priv) => { console.log('generateKeyPair', err, pub.length > 100, priv.length > 100); res(); })),
];
(async () => { for (const s of steps) await s(); })();
console.log('sync first');
`)
}

// A passphrase-encrypted PKCS#8 key (Node's `cipher: 'aes-256-cbc'`), and
// OpenSSL's errors without or with a wrong passphrase.
const cryptoPortEncryptedKeySrc = "import * as c from 'crypto';\nconst key = \"-----BEGIN ENCRYPTED PRIVATE KEY-----\\nMIH0MF8GCSqGSIb3DQEFDTBSMDEGCSqGSIb3DQEFDDAkBBDqLH8tJ6SC9mjgiMjO\\nfzoWAgIIADAMBggqhkiG9w0CCQUAMB0GCWCGSAFlAwQBKgQQLOdGxvvTNd+smCrA\\nv2TMTASBkFv4tuTae/wb3cM6xUGLZrrGhM+szG9SOOzMk5dwBoqLPnnXqKtgvqgo\\nr+m1Hrw4NP3SwpDd8yiVd7QU9XnV93QAPae2fR/0ssVHP5V8ur31ehFojHJ+bQ2e\\n4+xd1TfAchfh7OtrMHL+ea3nMACVhxWvefcZyyB1/qMvw0GpQgc0sNHtlz12Wftt\\nGiFLtK0EHg==\\n-----END ENCRYPTED PRIVATE KEY-----\\n\";\nconst pub = \"-----BEGIN PUBLIC KEY-----\\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEgQBghcf5ODXS4ebR47o2xty3OpGl\\nwtK8fony3sgOOa6wxNa8rCLaPmEJ2R7xVkP+hVagJLI5+O9uR+4cwen3rw==\\n-----END PUBLIC KEY-----\\n\";\nfor (const k of [key, { key }, { key, passphrase: 'bad' }, { key, passphrase: Buffer.from('pw') }, { key, passphrase: 'pw' }]) {\n    try {\n        const sig = c.sign('sha256', Buffer.from('m'), k as any);\n        console.log('sign', c.verify('sha256', Buffer.from('m'), pub, sig));\n    } catch (e: any) { console.log(e.name, e.code, e.message); }\n}\ntry { console.log(c.verify('sha256', Buffer.from('m'), key, Buffer.alloc(3))); } catch (e: any) { console.log(e.name, e.code, e.message); }\n"

func TestE2ECryptoPortEncryptedKey(t *testing.T) {
	assertSameAsNodeImports(t, cryptoPortEncryptedKeySrc)
}
