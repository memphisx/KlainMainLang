// kml:global
// URL and URLSearchParams — globals, and `url`'s exports — as Node has them
// (lib/internal/url.js, TDD-00237). Node parses with ada, a C++
// implementation of the URL Standard's parser; here the parser is the
// standard's algorithm in TypeScript, following the whatwg-url package's
// url-state-machine.js, which mirrors the standard's prose. A program that
// names one of the classes without declaring it imports this module.

import { inspect } from './internal_util_inspect';
import { NodeTypeError, missingArgs } from './internal_errors';

// ---- infra (https://infra.spec.whatwg.org/) ----

function isASCIIDigit(c: number): boolean { return c >= 0x30 && c <= 0x39; }
function isASCIIAlpha(c: number): boolean { return (c >= 0x41 && c <= 0x5A) || (c >= 0x61 && c <= 0x7A); }
function isASCIIAlphanumeric(c: number): boolean { return isASCIIAlpha(c) || isASCIIDigit(c); }
function isASCIIHex(c: number): boolean { return isASCIIDigit(c) || (c >= 0x41 && c <= 0x46) || (c >= 0x61 && c <= 0x66); }

function isASCIIString(s: string): boolean {
    for (let i = 0; i < s.length; i++) {
        if (s.charCodeAt(i) > 0x7F) return false;
    }
    return true;
}

// The code points of s; a lone surrogate is U+FFFD, as USVString
// conversion makes it.
function codePoints(s: string): number[] {
    const out: number[] = [];
    for (const ch of s) {
        const c = ch.codePointAt(0)!;
        out.push(c >= 0xD800 && c <= 0xDFFF ? 0xFFFD : c);
    }
    return out;
}

function fromCodePoints(cps: number[], start: number, end: number): string {
    let s = '';
    for (let i = start; i < end; i++) s += String.fromCodePoint(cps[i]);
    return s;
}

// ---- UTF-8 ----

function utf8Encode(c: number, out: number[]): void {
    if (c < 0x80) {
        out.push(c);
    } else if (c < 0x800) {
        out.push(0xC0 | (c >> 6), 0x80 | (c & 0x3F));
    } else if (c < 0x10000) {
        out.push(0xE0 | (c >> 12), 0x80 | ((c >> 6) & 0x3F), 0x80 | (c & 0x3F));
    } else {
        out.push(0xF0 | (c >> 18), 0x80 | ((c >> 12) & 0x3F), 0x80 | ((c >> 6) & 0x3F), 0x80 | (c & 0x3F));
    }
}

function utf8EncodeString(s: string): number[] {
    const out: number[] = [];
    for (const c of codePoints(s)) utf8Encode(c, out);
    return out;
}

// UTF-8 decode without BOM, each invalid sequence U+FFFD (the Encoding
// Standard's decoder).
function utf8Decode(bytes: number[]): string {
    let s = '';
    let i = 0;
    const n = bytes.length;
    while (i < n) {
        const b = bytes[i];
        if (b < 0x80) {
            s += String.fromCharCode(b);
            i++;
            continue;
        }
        let need = 0;
        let cp = 0;
        let lower = 0x80;
        let upper = 0xBF;
        if (b >= 0xC2 && b <= 0xDF) {
            need = 1; cp = b & 0x1F;
        } else if (b >= 0xE0 && b <= 0xEF) {
            if (b === 0xE0) lower = 0xA0;
            if (b === 0xED) upper = 0x9F;
            need = 2; cp = b & 0x0F;
        } else if (b >= 0xF0 && b <= 0xF4) {
            if (b === 0xF0) lower = 0x90;
            if (b === 0xF4) upper = 0x8F;
            need = 3; cp = b & 0x07;
        } else {
            s += '�';
            i++;
            continue;
        }
        let j = i + 1;
        let ok = true;
        for (let k = 0; k < need; k++, j++) {
            const nb = j < n ? bytes[j] : -1;
            if (nb < lower || nb > upper) {
                ok = false;
                break;
            }
            lower = 0x80;
            upper = 0xBF;
            cp = (cp << 6) | (nb & 0x3F);
        }
        if (!ok) {
            s += '�';
            i = j;
            continue;
        }
        s += String.fromCodePoint(cp);
        i = j;
    }
    return s;
}

// ---- percent-encoding (https://url.spec.whatwg.org/#percent-encoded-bytes) ----

const C0_CONTROL = 0;
const FRAGMENT = 1;
const QUERY = 2;
const SPECIAL_QUERY = 3;
const PATH = 4;
const USERINFO = 5;
const COMPONENT = 6;
const FORM_URLENCODED = 7;

// Whether byte b is in percent-encode set `set`.
function inEncodeSet(b: number, set: number): boolean {
    if (b <= 0x1F || b > 0x7E) return true;
    switch (set) {
        case C0_CONTROL:
            return false;
        case FRAGMENT:
            return b === 0x20 || b === 0x22 || b === 0x3C || b === 0x3E || b === 0x60;
        case SPECIAL_QUERY:
            if (b === 0x27) return true;
            return b === 0x20 || b === 0x22 || b === 0x23 || b === 0x3C || b === 0x3E;
        case QUERY:
            return b === 0x20 || b === 0x22 || b === 0x23 || b === 0x3C || b === 0x3E;
    }
    // path ⊂ userinfo ⊂ component ⊂ application/x-www-form-urlencoded
    if (b === 0x20 || b === 0x22 || b === 0x23 || b === 0x3C || b === 0x3E ||
        b === 0x3F || b === 0x60 || b === 0x7B || b === 0x7D || b === 0x5E) return true;
    if (set === PATH) return false;
    if (b === 0x2F || b === 0x3A || b === 0x3B || b === 0x3D || b === 0x40 ||
        b === 0x5B || b === 0x5C || b === 0x5D || b === 0x7C) return true;
    if (set === USERINFO) return false;
    if (b === 0x24 || b === 0x25 || b === 0x26 || b === 0x2B || b === 0x2C) return true;
    if (set === COMPONENT) return false;
    return b === 0x21 || b === 0x27 || b === 0x28 || b === 0x29 || b === 0x7E;
}

const HEX = '0123456789ABCDEF';

function percentEncodeByte(b: number): string {
    return '%' + HEX.charAt(b >> 4) + HEX.charAt(b & 0xF);
}

// The UTF-8 percent-encoding of code point c under set.
function percentEncodeCodePoint(c: number, set: number): string {
    const bytes: number[] = [];
    utf8Encode(c, bytes);
    let out = '';
    for (const b of bytes) {
        out += inEncodeSet(b, set) ? percentEncodeByte(b) : String.fromCharCode(b);
    }
    return out;
}

function percentEncodeString(s: string, set: number, spaceAsPlus: boolean): string {
    let out = '';
    for (const c of codePoints(s)) {
        if (spaceAsPlus && c === 0x20) {
            out += '+';
        } else {
            out += percentEncodeCodePoint(c, set);
        }
    }
    return out;
}

function hexValue(c: number): number {
    if (c <= 0x39) return c - 0x30;
    if (c <= 0x46) return c - 0x41 + 10;
    return c - 0x61 + 10;
}

function percentDecodeBytes(input: number[]): number[] {
    const out: number[] = [];
    for (let i = 0; i < input.length; i++) {
        const b = input[i];
        if (b === 0x25 && i + 2 < input.length && isASCIIHex(input[i + 1]) && isASCIIHex(input[i + 2])) {
            out.push(hexValue(input[i + 1]) * 16 + hexValue(input[i + 2]));
            i += 2;
        } else {
            out.push(b);
        }
    }
    return out;
}

function percentDecodeString(s: string): number[] {
    return percentDecodeBytes(utf8EncodeString(s));
}

// ---- Punycode (RFC 3492) ----

const PUNY_BASE = 36;
const PUNY_TMIN = 1;
const PUNY_TMAX = 26;
const PUNY_SKEW = 38;
const PUNY_DAMP = 700;
const PUNY_INITIAL_BIAS = 72;
const PUNY_INITIAL_N = 128;

function punyAdapt(delta: number, numPoints: number, firstTime: boolean): number {
    let k = 0;
    delta = firstTime ? Math.floor(delta / PUNY_DAMP) : delta >> 1;
    delta += Math.floor(delta / numPoints);
    while (delta > ((PUNY_BASE - PUNY_TMIN) * PUNY_TMAX) >> 1) {
        delta = Math.floor(delta / (PUNY_BASE - PUNY_TMIN));
        k += PUNY_BASE;
    }
    return Math.floor(k + ((PUNY_BASE - PUNY_TMIN + 1) * delta) / (delta + PUNY_SKEW));
}

function punyDigit(d: number): string {
    return String.fromCharCode(d < 26 ? 0x61 + d : 0x30 + d - 26);
}

function punyEncode(cps: number[]): string {
    let out = '';
    for (const c of cps) {
        if (c < 0x80) out += String.fromCharCode(c);
    }
    const basic = out.length;
    let handled = basic;
    if (basic > 0) out += '-';
    let n = PUNY_INITIAL_N;
    let delta = 0;
    let bias = PUNY_INITIAL_BIAS;
    while (handled < cps.length) {
        let m = 0x7FFFFFFF;
        for (const c of cps) {
            if (c >= n && c < m) m = c;
        }
        delta += (m - n) * (handled + 1);
        n = m;
        for (const c of cps) {
            if (c < n) delta++;
            if (c === n) {
                let q = delta;
                for (let k = PUNY_BASE; ; k += PUNY_BASE) {
                    const t = k <= bias ? PUNY_TMIN : k >= bias + PUNY_TMAX ? PUNY_TMAX : k - bias;
                    if (q < t) break;
                    out += punyDigit(t + ((q - t) % (PUNY_BASE - t)));
                    q = Math.floor((q - t) / (PUNY_BASE - t));
                }
                out += punyDigit(q);
                bias = punyAdapt(delta, handled + 1, handled === basic);
                delta = 0;
                handled++;
            }
        }
        delta++;
        n++;
    }
    return out;
}

// The code points label (after `xn--`) decodes to, or null when it is not
// valid Punycode.
function punyDecode(label: string): number[] | null {
    const out: number[] = [];
    let basic = label.lastIndexOf('-');
    if (basic < 0) basic = 0;
    for (let j = 0; j < basic; j++) {
        const c = label.charCodeAt(j);
        if (c >= 0x80) return null;
        out.push(c);
    }
    let n = PUNY_INITIAL_N;
    let bias = PUNY_INITIAL_BIAS;
    let i = 0;
    for (let index = basic > 0 ? basic + 1 : 0; index < label.length;) {
        const oldi = i;
        let w = 1;
        for (let k = PUNY_BASE; ; k += PUNY_BASE) {
            if (index >= label.length) return null;
            const c = label.charCodeAt(index++);
            let digit = PUNY_BASE;
            if (c >= 0x30 && c <= 0x39) digit = c - 0x30 + 26;
            else if (c >= 0x41 && c <= 0x5A) digit = c - 0x41;
            else if (c >= 0x61 && c <= 0x7A) digit = c - 0x61;
            if (digit >= PUNY_BASE) return null;
            i += digit * w;
            const t = k <= bias ? PUNY_TMIN : k >= bias + PUNY_TMAX ? PUNY_TMAX : k - bias;
            if (digit < t) break;
            w *= PUNY_BASE - t;
        }
        const len = out.length + 1;
        bias = punyAdapt(i - oldi, len, oldi === 0);
        n += Math.floor(i / len);
        i %= len;
        if (n > 0x10FFFF) return null;
        out.splice(i, 0, n);
        i++;
    }
    return out;
}

// ---- domain to ASCII (https://url.spec.whatwg.org/#concept-domain-to-ascii) ----
//
// UTS #46 processing: the domain is lowercased (the mapping's case fold;
// its other mappings — NFC, width folding — are not applied), each
// non-ASCII label becomes `xn--` and its Punycode, and an `xn--` label must
// decode. null on failure.
function domainToASCIIInternal(domain: string): string | null {
    const lower = domain.toLowerCase();
    const labels = lower.split('.');
    const out: string[] = [];
    for (const label of labels) {
        if (label.startsWith('xn--')) {
            const decoded = punyDecode(label.slice(4));
            if (decoded === null || decoded.length === 0) return null;
        }
        if (isASCIIString(label)) {
            out.push(label);
        } else {
            out.push('xn--' + punyEncode(codePoints(label)));
        }
    }
    return out.join('.');
}

function domainToUnicodeInternal(domain: string): string {
    const labels = domain.toLowerCase().split('.');
    const out: string[] = [];
    for (const label of labels) {
        if (label.startsWith('xn--')) {
            const decoded = punyDecode(label.slice(4));
            out.push(decoded === null ? label : fromCodePoints(decoded, 0, decoded.length));
        } else {
            out.push(label);
        }
    }
    return out.join('.');
}

// ---- hosts (https://url.spec.whatwg.org/#hosts-(domains-and-ip-addresses)) ----

const HOST_DOMAIN = 0; // a domain, an opaque host or the empty host
const HOST_IPV4 = 1;
const HOST_IPV6 = 2;

class Host {
    kind: number;
    name: string;
    ipv4: number;
    ipv6: number[];
    constructor(kind: number, name: string, ipv4: number, ipv6: number[]) {
        this.kind = kind;
        this.name = name;
        this.ipv4 = ipv4;
        this.ipv6 = ipv6;
    }
}

function domainHost(name: string): Host { return new Host(HOST_DOMAIN, name, 0, []); }

function isForbiddenHostCodePoint(c: number): boolean {
    return c === 0x00 || c === 0x09 || c === 0x0A || c === 0x0D || c === 0x20 || c === 0x23 ||
        c === 0x2F || c === 0x3A || c === 0x3C || c === 0x3E || c === 0x3F || c === 0x40 ||
        c === 0x5B || c === 0x5C || c === 0x5D || c === 0x5E || c === 0x7C;
}

function isForbiddenDomainCodePoint(c: number): boolean {
    return isForbiddenHostCodePoint(c) || c <= 0x1F || c === 0x25 || c === 0x7F;
}

// An IPv4 number, or -1 for failure.
function parseIPv4Number(input: string): number {
    if (input === '') return -1;
    let r = 10;
    if (input.length >= 2 && input.charAt(0) === '0' && (input.charAt(1) === 'x' || input.charAt(1) === 'X')) {
        input = input.substring(2);
        r = 16;
    } else if (input.length >= 2 && input.charAt(0) === '0') {
        input = input.substring(1);
        r = 8;
    }
    if (input === '') return 0;
    for (let i = 0; i < input.length; i++) {
        const c = input.charCodeAt(i);
        const ok = r === 10 ? isASCIIDigit(c) : r === 16 ? isASCIIHex(c) : c >= 0x30 && c <= 0x37;
        if (!ok) return -1;
    }
    return parseInt(input, r);
}

function endsInANumber(input: string): boolean {
    const parts = input.split('.');
    if (parts[parts.length - 1] === '') {
        if (parts.length === 1) return false;
        parts.pop();
    }
    const last = parts[parts.length - 1];
    let digits = last.length > 0;
    for (let i = 0; i < last.length; i++) {
        if (!isASCIIDigit(last.charCodeAt(i))) digits = false;
    }
    return digits || parseIPv4Number(last) !== -1;
}

function parseIPv4(input: string): Host | null {
    const parts = input.split('.');
    if (parts[parts.length - 1] === '' && parts.length > 1) parts.pop();
    if (parts.length > 4) return null;
    const numbers: number[] = [];
    for (const part of parts) {
        const n = parseIPv4Number(part);
        if (n === -1) return null;
        numbers.push(n);
    }
    for (let i = 0; i < numbers.length - 1; i++) {
        if (numbers[i] > 255) return null;
    }
    if (numbers[numbers.length - 1] >= Math.pow(256, 5 - numbers.length)) return null;
    let ipv4 = numbers[numbers.length - 1];
    for (let i = 0; i < numbers.length - 1; i++) {
        ipv4 += numbers[i] * Math.pow(256, 3 - i);
    }
    return new Host(HOST_IPV4, '', ipv4, []);
}

function parseIPv6(text: string): Host | null {
    const address = [0, 0, 0, 0, 0, 0, 0, 0];
    let pieceIndex = 0;
    let compress = -1;
    let pointer = 0;
    const input = codePoints(text);
    const at = (i: number): number => (i < input.length ? input[i] : -1);
    if (at(pointer) === 0x3A) {
        if (at(pointer + 1) !== 0x3A) return null;
        pointer += 2;
        pieceIndex++;
        compress = pieceIndex;
    }
    while (at(pointer) !== -1) {
        if (pieceIndex === 8) return null;
        if (at(pointer) === 0x3A) {
            if (compress !== -1) return null;
            pointer++;
            pieceIndex++;
            compress = pieceIndex;
            continue;
        }
        let value = 0;
        let length = 0;
        while (length < 4 && isASCIIHex(at(pointer))) {
            value = value * 0x10 + hexValue(at(pointer));
            pointer++;
            length++;
        }
        if (at(pointer) === 0x2E) {
            if (length === 0) return null;
            pointer -= length;
            if (pieceIndex > 6) return null;
            let numbersSeen = 0;
            while (at(pointer) !== -1) {
                let ipv4Piece = -1;
                if (numbersSeen > 0) {
                    if (at(pointer) === 0x2E && numbersSeen < 4) {
                        pointer++;
                    } else {
                        return null;
                    }
                }
                if (!isASCIIDigit(at(pointer))) return null;
                while (isASCIIDigit(at(pointer))) {
                    const number = at(pointer) - 0x30;
                    if (ipv4Piece === -1) {
                        ipv4Piece = number;
                    } else if (ipv4Piece === 0) {
                        return null;
                    } else {
                        ipv4Piece = ipv4Piece * 10 + number;
                    }
                    if (ipv4Piece > 255) return null;
                    pointer++;
                }
                address[pieceIndex] = address[pieceIndex] * 0x100 + ipv4Piece;
                numbersSeen++;
                if (numbersSeen === 2 || numbersSeen === 4) pieceIndex++;
            }
            if (numbersSeen !== 4) return null;
            break;
        } else if (at(pointer) === 0x3A) {
            pointer++;
            if (at(pointer) === -1) return null;
        } else if (at(pointer) !== -1) {
            return null;
        }
        address[pieceIndex] = value;
        pieceIndex++;
    }
    if (compress !== -1) {
        let swaps = pieceIndex - compress;
        pieceIndex = 7;
        while (pieceIndex !== 0 && swaps > 0) {
            const temp = address[compress + swaps - 1];
            address[compress + swaps - 1] = address[pieceIndex];
            address[pieceIndex] = temp;
            pieceIndex--;
            swaps--;
        }
    } else if (pieceIndex !== 8) {
        return null;
    }
    return new Host(HOST_IPV6, '', 0, address);
}

function parseOpaqueHost(input: string): Host | null {
    for (const c of codePoints(input)) {
        if (isForbiddenHostCodePoint(c)) return null;
    }
    return domainHost(percentEncodeString(input, C0_CONTROL, false));
}

// https://url.spec.whatwg.org/#concept-host-parser
function parseHost(input: string, isOpaque: boolean): Host | null {
    if (input.startsWith('[')) {
        if (!input.endsWith(']')) return null;
        return parseIPv6(input.substring(1, input.length - 1));
    }
    if (isOpaque) return parseOpaqueHost(input);
    const domain = utf8Decode(percentDecodeString(input));
    let ascii: string | null;
    if (isASCIIString(domain)) {
        // An ASCII domain is lowercased whatever ToASCII would say (web
        // compatibility).
        ascii = domain.toLowerCase();
    } else {
        ascii = domainToASCIIInternal(domain);
    }
    if (ascii === null || ascii === '') return null;
    for (const c of codePoints(ascii)) {
        if (isForbiddenDomainCodePoint(c)) return null;
    }
    if (endsInANumber(ascii)) return parseIPv4(ascii);
    return domainHost(ascii);
}

function serializeIPv4(address: number): string {
    let output = '';
    let n = address;
    for (let i = 1; i <= 4; i++) {
        output = String(n % 256) + output;
        if (i !== 4) output = '.' + output;
        n = Math.floor(n / 256);
    }
    return output;
}

function serializeIPv6(address: number[]): string {
    // The first longest run of two or more zero pieces is compressed.
    let compress = -1;
    let longest = 1;
    let foundAt = -1;
    let foundSize = 0;
    for (let i = 0; i < 8; i++) {
        if (address[i] !== 0) {
            if (foundSize > longest) {
                compress = foundAt;
                longest = foundSize;
            }
            foundAt = -1;
            foundSize = 0;
        } else {
            if (foundAt === -1) foundAt = i;
            foundSize++;
        }
    }
    if (foundSize > longest) compress = foundAt;
    let output = '';
    let ignore0 = false;
    for (let i = 0; i <= 7; i++) {
        if (ignore0 && address[i] === 0) continue;
        ignore0 = false;
        if (compress === i) {
            output += i === 0 ? '::' : ':';
            ignore0 = true;
            continue;
        }
        output += address[i].toString(16);
        if (i !== 7) output += ':';
    }
    return output;
}

function serializeHost(host: Host): string {
    if (host.kind === HOST_IPV4) return serializeIPv4(host.ipv4);
    if (host.kind === HOST_IPV6) return '[' + serializeIPv6(host.ipv6) + ']';
    return host.name;
}

// ---- URL records (https://url.spec.whatwg.org/#concept-url) ----

const specialPorts: Map<string, number> = new Map([
    ['ftp', 21], ['file', -1], ['http', 80], ['https', 443], ['ws', 80], ['wss', 443],
]);

function isSpecialScheme(scheme: string): boolean { return specialPorts.has(scheme); }

// The scheme's default port; -1 for none.
function defaultPort(scheme: string): number {
    const p = specialPorts.get(scheme);
    return p === undefined ? -1 : p;
}

class URLRecord {
    scheme = '';
    username = '';
    password = '';
    host: Host | null = null;
    port = -1; // null
    path: string[] = [];
    opaquePath: string | null = null; // an opaque path, in place of path
    query: string | null = null;
    fragment: string | null = null;

    isSpecial(): boolean { return isSpecialScheme(this.scheme); }
    includesCredentials(): boolean { return this.username !== '' || this.password !== ''; }
    cannotHaveUsernamePasswordPort(): boolean {
        return this.host === null || (this.host.kind === HOST_DOMAIN && this.host.name === '') || this.scheme === 'file';
    }
    hasOpaquePath(): boolean { return this.opaquePath !== null; }

    shortenPath(): void {
        if (this.path.length === 0) return;
        if (this.scheme === 'file' && this.path.length === 1 && isNormalizedWindowsDriveLetter(this.path[0])) return;
        this.path.pop();
    }
}

function isWindowsDriveLetter(s: string): boolean {
    return s.length === 2 && isASCIIAlpha(s.charCodeAt(0)) && (s.charAt(1) === ':' || s.charAt(1) === '|');
}

function isNormalizedWindowsDriveLetter(s: string): boolean {
    return s.length === 2 && isASCIIAlpha(s.charCodeAt(0)) && s.charAt(1) === ':';
}

function isSingleDot(s: string): boolean {
    return s === '.' || s.toLowerCase() === '%2e';
}

function isDoubleDot(s: string): boolean {
    const l = s.toLowerCase();
    return l === '..' || l === '%2e.' || l === '.%2e' || l === '%2e%2e';
}

// ---- the basic URL parser (https://url.spec.whatwg.org/#concept-basic-url-parser) ----

const S_SCHEME_START = 1;
const S_SCHEME = 2;
const S_NO_SCHEME = 3;
const S_SPECIAL_RELATIVE_OR_AUTHORITY = 4;
const S_PATH_OR_AUTHORITY = 5;
const S_RELATIVE = 6;
const S_RELATIVE_SLASH = 7;
const S_SPECIAL_AUTHORITY_SLASHES = 8;
const S_SPECIAL_AUTHORITY_IGNORE_SLASHES = 9;
const S_AUTHORITY = 10;
const S_HOST = 11;
const S_HOSTNAME = 12;
const S_PORT = 13;
const S_FILE = 14;
const S_FILE_SLASH = 15;
const S_FILE_HOST = 16;
const S_PATH_START = 17;
const S_PATH = 18;
const S_OPAQUE_PATH = 19;
const S_QUERY = 20;
const S_FRAGMENT = 21;

// A state's step result: go on, stop (the algorithm returns), or fail.
const GO = 1;
const STOP = 0;
const FAIL = -1;

const EOF_CP = -1;

class URLStateMachine {
    input: number[];
    pointer = 0;
    base: URLRecord | null;
    url: URLRecord;
    stateOverride: number;
    state: number;
    buffer = '';
    atSignSeen = false;
    insideBrackets = false;
    passwordTokenSeen = false;
    failure = false;

    constructor(text: string, base: URLRecord | null, url: URLRecord | null, stateOverride: number) {
        this.base = base;
        this.stateOverride = stateOverride;
        let cps = codePoints(text);
        if (url === null) {
            this.url = new URLRecord();
            // Leading and trailing C0 control or space removed.
            let start = 0;
            let end = cps.length;
            while (start < end && cps[start] <= 0x20) start++;
            while (end > start && cps[end - 1] <= 0x20) end--;
            cps = cps.slice(start, end);
        } else {
            this.url = url;
        }
        // Every ASCII tab or newline removed.
        this.input = cps.filter((c) => c !== 0x09 && c !== 0x0A && c !== 0x0D);
        this.state = stateOverride !== 0 ? stateOverride : S_SCHEME_START;
        for (; this.pointer <= this.input.length; this.pointer++) {
            const c = this.pointer < this.input.length ? this.input[this.pointer] : EOF_CP;
            const r = this.step(c);
            if (r === STOP) break;
            if (r === FAIL) {
                this.failure = true;
                break;
            }
        }
    }

    at(i: number): number {
        return i >= 0 && i < this.input.length ? this.input[i] : EOF_CP;
    }

    step(c: number): number {
        switch (this.state) {
            case S_SCHEME_START: return this.schemeStart(c);
            case S_SCHEME: return this.scheme(c);
            case S_NO_SCHEME: return this.noScheme(c);
            case S_SPECIAL_RELATIVE_OR_AUTHORITY: return this.specialRelativeOrAuthority(c);
            case S_PATH_OR_AUTHORITY: return this.pathOrAuthority(c);
            case S_RELATIVE: return this.relative(c);
            case S_RELATIVE_SLASH: return this.relativeSlash(c);
            case S_SPECIAL_AUTHORITY_SLASHES: return this.specialAuthoritySlashes(c);
            case S_SPECIAL_AUTHORITY_IGNORE_SLASHES: return this.specialAuthorityIgnoreSlashes(c);
            case S_AUTHORITY: return this.authority(c);
            case S_HOST:
            case S_HOSTNAME: return this.host(c);
            case S_PORT: return this.port(c);
            case S_FILE: return this.file(c);
            case S_FILE_SLASH: return this.fileSlash(c);
            case S_FILE_HOST: return this.fileHost(c);
            case S_PATH_START: return this.pathStart(c);
            case S_PATH: return this.path(c);
            case S_OPAQUE_PATH: return this.opaquePath(c);
            case S_QUERY: return this.query(c);
            case S_FRAGMENT: return this.fragment(c);
        }
        return FAIL;
    }

    schemeStart(c: number): number {
        if (isASCIIAlpha(c)) {
            this.buffer += String.fromCharCode(c).toLowerCase();
            this.state = S_SCHEME;
        } else if (this.stateOverride === 0) {
            this.state = S_NO_SCHEME;
            this.pointer--;
        } else {
            return FAIL;
        }
        return GO;
    }

    scheme(c: number): number {
        const url = this.url;
        if (isASCIIAlphanumeric(c) || c === 0x2B || c === 0x2D || c === 0x2E) {
            this.buffer += String.fromCharCode(c).toLowerCase();
        } else if (c === 0x3A) {
            if (this.stateOverride !== 0) {
                if (url.isSpecial() !== isSpecialScheme(this.buffer)) return STOP;
                if ((url.includesCredentials() || url.port !== -1) && this.buffer === 'file') return STOP;
                if (url.scheme === 'file' && url.host !== null && url.host.kind === HOST_DOMAIN && url.host.name === '') return STOP;
            }
            url.scheme = this.buffer;
            if (this.stateOverride !== 0) {
                if (url.port === defaultPort(url.scheme)) url.port = -1;
                return STOP;
            }
            this.buffer = '';
            if (url.scheme === 'file') {
                this.state = S_FILE;
            } else if (url.isSpecial() && this.base !== null && this.base.scheme === url.scheme) {
                this.state = S_SPECIAL_RELATIVE_OR_AUTHORITY;
            } else if (url.isSpecial()) {
                this.state = S_SPECIAL_AUTHORITY_SLASHES;
            } else if (this.at(this.pointer + 1) === 0x2F) {
                this.state = S_PATH_OR_AUTHORITY;
                this.pointer++;
            } else {
                url.opaquePath = '';
                this.state = S_OPAQUE_PATH;
            }
        } else if (this.stateOverride === 0) {
            this.buffer = '';
            this.state = S_NO_SCHEME;
            this.pointer = -1;
        } else {
            return FAIL;
        }
        return GO;
    }

    noScheme(c: number): number {
        const base = this.base;
        if (base === null || (base.hasOpaquePath() && c !== 0x23)) return FAIL;
        if (base.hasOpaquePath() && c === 0x23) {
            this.url.scheme = base.scheme;
            this.url.opaquePath = base.opaquePath;
            this.url.query = base.query;
            this.url.fragment = '';
            this.state = S_FRAGMENT;
        } else if (base.scheme === 'file') {
            this.state = S_FILE;
            this.pointer--;
        } else {
            this.state = S_RELATIVE;
            this.pointer--;
        }
        return GO;
    }

    specialRelativeOrAuthority(c: number): number {
        if (c === 0x2F && this.at(this.pointer + 1) === 0x2F) {
            this.state = S_SPECIAL_AUTHORITY_IGNORE_SLASHES;
            this.pointer++;
        } else {
            this.state = S_RELATIVE;
            this.pointer--;
        }
        return GO;
    }

    pathOrAuthority(c: number): number {
        if (c === 0x2F) {
            this.state = S_AUTHORITY;
        } else {
            this.state = S_PATH;
            this.pointer--;
        }
        return GO;
    }

    relative(c: number): number {
        const url = this.url;
        const base = this.base!;
        url.scheme = base.scheme;
        if (c === 0x2F) {
            this.state = S_RELATIVE_SLASH;
        } else if (url.isSpecial() && c === 0x5C) {
            this.state = S_RELATIVE_SLASH;
        } else {
            url.username = base.username;
            url.password = base.password;
            url.host = base.host;
            url.port = base.port;
            url.path = base.path.slice();
            url.query = base.query;
            if (c === 0x3F) {
                url.query = '';
                this.state = S_QUERY;
            } else if (c === 0x23) {
                url.fragment = '';
                this.state = S_FRAGMENT;
            } else if (c !== EOF_CP) {
                url.query = null;
                url.path.pop();
                this.state = S_PATH;
                this.pointer--;
            }
        }
        return GO;
    }

    relativeSlash(c: number): number {
        const url = this.url;
        if (url.isSpecial() && (c === 0x2F || c === 0x5C)) {
            this.state = S_SPECIAL_AUTHORITY_IGNORE_SLASHES;
        } else if (c === 0x2F) {
            this.state = S_AUTHORITY;
        } else {
            const base = this.base!;
            url.username = base.username;
            url.password = base.password;
            url.host = base.host;
            url.port = base.port;
            this.state = S_PATH;
            this.pointer--;
        }
        return GO;
    }

    specialAuthoritySlashes(c: number): number {
        if (c === 0x2F && this.at(this.pointer + 1) === 0x2F) {
            this.state = S_SPECIAL_AUTHORITY_IGNORE_SLASHES;
            this.pointer++;
        } else {
            this.state = S_SPECIAL_AUTHORITY_IGNORE_SLASHES;
            this.pointer--;
        }
        return GO;
    }

    specialAuthorityIgnoreSlashes(c: number): number {
        if (c !== 0x2F && c !== 0x5C) {
            this.state = S_AUTHORITY;
            this.pointer--;
        }
        return GO;
    }

    authority(c: number): number {
        const url = this.url;
        if (c === 0x40) {
            if (this.atSignSeen) this.buffer = '%40' + this.buffer;
            this.atSignSeen = true;
            for (const cp of codePoints(this.buffer)) {
                if (cp === 0x3A && !this.passwordTokenSeen) {
                    this.passwordTokenSeen = true;
                    continue;
                }
                const encoded = percentEncodeCodePoint(cp, USERINFO);
                if (this.passwordTokenSeen) {
                    url.password += encoded;
                } else {
                    url.username += encoded;
                }
            }
            this.buffer = '';
        } else if (c === EOF_CP || c === 0x2F || c === 0x3F || c === 0x23 || (url.isSpecial() && c === 0x5C)) {
            if (this.atSignSeen && this.buffer === '') return FAIL;
            this.pointer -= codePoints(this.buffer).length + 1;
            this.buffer = '';
            this.state = S_HOST;
        } else {
            this.buffer += String.fromCodePoint(c);
        }
        return GO;
    }

    host(c: number): number {
        const url = this.url;
        if (this.stateOverride !== 0 && url.scheme === 'file') {
            this.pointer--;
            this.state = S_FILE_HOST;
        } else if (c === 0x3A && !this.insideBrackets) {
            if (this.buffer === '') return FAIL;
            if (this.stateOverride === S_HOSTNAME) return FAIL;
            const host = parseHost(this.buffer, !url.isSpecial());
            if (host === null) return FAIL;
            url.host = host;
            this.buffer = '';
            this.state = S_PORT;
        } else if (c === EOF_CP || c === 0x2F || c === 0x3F || c === 0x23 || (url.isSpecial() && c === 0x5C)) {
            this.pointer--;
            if (url.isSpecial() && this.buffer === '') return FAIL;
            if (this.stateOverride !== 0 && this.buffer === '' && (url.includesCredentials() || url.port !== -1)) return FAIL;
            const host = parseHost(this.buffer, !url.isSpecial());
            if (host === null) return FAIL;
            url.host = host;
            this.buffer = '';
            this.state = S_PATH_START;
            if (this.stateOverride !== 0) return STOP;
        } else {
            if (c === 0x5B) this.insideBrackets = true;
            if (c === 0x5D) this.insideBrackets = false;
            this.buffer += String.fromCodePoint(c);
        }
        return GO;
    }

    port(c: number): number {
        const url = this.url;
        if (isASCIIDigit(c)) {
            this.buffer += String.fromCharCode(c);
        } else if (c === EOF_CP || c === 0x2F || c === 0x3F || c === 0x23 || (url.isSpecial() && c === 0x5C) || this.stateOverride !== 0) {
            if (this.buffer !== '') {
                const port = parseInt(this.buffer, 10);
                if (port > 65535) return FAIL;
                url.port = port === defaultPort(url.scheme) ? -1 : port;
                this.buffer = '';
                if (this.stateOverride !== 0) return STOP;
            }
            if (this.stateOverride !== 0) return FAIL;
            this.state = S_PATH_START;
            this.pointer--;
        } else {
            return FAIL;
        }
        return GO;
    }

    startsWithWindowsDriveLetter(pointer: number): boolean {
        const length = this.input.length - pointer;
        if (length < 2) return false;
        const c0 = this.input[pointer];
        const c1 = this.input[pointer + 1];
        if (!isASCIIAlpha(c0) || (c1 !== 0x3A && c1 !== 0x7C)) return false;
        if (length === 2) return true;
        const c2 = this.input[pointer + 2];
        return c2 === 0x2F || c2 === 0x5C || c2 === 0x3F || c2 === 0x23;
    }

    file(c: number): number {
        const url = this.url;
        url.scheme = 'file';
        url.host = domainHost('');
        if (c === 0x2F || c === 0x5C) {
            this.state = S_FILE_SLASH;
        } else if (this.base !== null && this.base.scheme === 'file') {
            const base = this.base;
            url.host = base.host;
            url.path = base.path.slice();
            url.query = base.query;
            if (c === 0x3F) {
                url.query = '';
                this.state = S_QUERY;
            } else if (c === 0x23) {
                url.fragment = '';
                this.state = S_FRAGMENT;
            } else if (c !== EOF_CP) {
                url.query = null;
                if (!this.startsWithWindowsDriveLetter(this.pointer)) {
                    url.shortenPath();
                } else {
                    url.path = [];
                }
                this.state = S_PATH;
                this.pointer--;
            }
        } else {
            this.state = S_PATH;
            this.pointer--;
        }
        return GO;
    }

    fileSlash(c: number): number {
        const url = this.url;
        if (c === 0x2F || c === 0x5C) {
            this.state = S_FILE_HOST;
        } else {
            if (this.base !== null && this.base.scheme === 'file') {
                const base = this.base;
                if (!this.startsWithWindowsDriveLetter(this.pointer) && base.path.length > 0 &&
                    isNormalizedWindowsDriveLetter(base.path[0])) {
                    url.path.push(base.path[0]);
                }
                url.host = base.host;
            }
            this.state = S_PATH;
            this.pointer--;
        }
        return GO;
    }

    fileHost(c: number): number {
        const url = this.url;
        if (c === EOF_CP || c === 0x2F || c === 0x5C || c === 0x3F || c === 0x23) {
            this.pointer--;
            if (this.stateOverride === 0 && isWindowsDriveLetter(this.buffer)) {
                this.state = S_PATH;
            } else if (this.buffer === '') {
                url.host = domainHost('');
                if (this.stateOverride !== 0) return STOP;
                this.state = S_PATH_START;
            } else {
                let host = parseHost(this.buffer, !url.isSpecial());
                if (host === null) return FAIL;
                if (host.kind === HOST_DOMAIN && host.name === 'localhost') host = domainHost('');
                url.host = host;
                if (this.stateOverride !== 0) return STOP;
                this.buffer = '';
                this.state = S_PATH_START;
            }
        } else {
            this.buffer += String.fromCodePoint(c);
        }
        return GO;
    }

    pathStart(c: number): number {
        const url = this.url;
        if (url.isSpecial()) {
            this.state = S_PATH;
            if (c !== 0x2F && c !== 0x5C) this.pointer--;
        } else if (this.stateOverride === 0 && c === 0x3F) {
            url.query = '';
            this.state = S_QUERY;
        } else if (this.stateOverride === 0 && c === 0x23) {
            url.fragment = '';
            this.state = S_FRAGMENT;
        } else if (c !== EOF_CP) {
            this.state = S_PATH;
            if (c !== 0x2F) this.pointer--;
        } else if (this.stateOverride !== 0 && url.host === null) {
            url.path.push('');
        }
        return GO;
    }

    path(c: number): number {
        const url = this.url;
        const slash = c === 0x2F || (url.isSpecial() && c === 0x5C);
        if (c === EOF_CP || slash || (this.stateOverride === 0 && (c === 0x3F || c === 0x23))) {
            if (isDoubleDot(this.buffer)) {
                url.shortenPath();
                if (!slash) url.path.push('');
            } else if (isSingleDot(this.buffer) && !slash) {
                url.path.push('');
            } else if (!isSingleDot(this.buffer)) {
                if (url.scheme === 'file' && url.path.length === 0 && isWindowsDriveLetter(this.buffer)) {
                    this.buffer = this.buffer.charAt(0) + ':';
                }
                url.path.push(this.buffer);
            }
            this.buffer = '';
            if (c === 0x3F) {
                url.query = '';
                this.state = S_QUERY;
            }
            if (c === 0x23) {
                url.fragment = '';
                this.state = S_FRAGMENT;
            }
        } else {
            this.buffer += percentEncodeCodePoint(c, PATH);
        }
        return GO;
    }

    opaquePath(c: number): number {
        const url = this.url;
        if (c === 0x3F) {
            url.query = '';
            this.state = S_QUERY;
        } else if (c === 0x23) {
            url.fragment = '';
            this.state = S_FRAGMENT;
        } else if (c === 0x20) {
            const next = this.at(this.pointer + 1);
            url.opaquePath += next === 0x3F || next === 0x23 ? '%20' : ' ';
        } else if (c !== EOF_CP) {
            url.opaquePath += percentEncodeCodePoint(c, C0_CONTROL);
        }
        return GO;
    }

    query(c: number): number {
        const url = this.url;
        if ((this.stateOverride === 0 && c === 0x23) || c === EOF_CP) {
            url.query += percentEncodeString(this.buffer, url.isSpecial() ? SPECIAL_QUERY : QUERY, false);
            this.buffer = '';
            if (c === 0x23) {
                url.fragment = '';
                this.state = S_FRAGMENT;
            }
        } else {
            this.buffer += String.fromCodePoint(c);
        }
        return GO;
    }

    fragment(c: number): number {
        if (c !== EOF_CP) this.url.fragment += percentEncodeCodePoint(c, FRAGMENT);
        return GO;
    }
}

// The URL record input parses to (against base), or null for failure.
function basicURLParse(input: string, base: URLRecord | null): URLRecord | null {
    const m = new URLStateMachine(input, base, null, 0);
    return m.failure ? null : m.url;
}

// Runs the parser on url from stateOverride (a setter); false on failure.
function parseInto(input: string, url: URLRecord, stateOverride: number): boolean {
    return !new URLStateMachine(input, null, url, stateOverride).failure;
}

// ---- serializers (https://url.spec.whatwg.org/#url-serializing) ----

function serializePath(url: URLRecord): string {
    if (url.opaquePath !== null) return url.opaquePath;
    let output = '';
    for (const segment of url.path) output += '/' + segment;
    return output;
}

function serializeURL(url: URLRecord, excludeFragment: boolean): string {
    let output = url.scheme + ':';
    if (url.host !== null) {
        output += '//';
        if (url.username !== '' || url.password !== '') {
            output += url.username;
            if (url.password !== '') output += ':' + url.password;
            output += '@';
        }
        output += serializeHost(url.host);
        if (url.port !== -1) output += ':' + String(url.port);
    }
    if (url.host === null && url.opaquePath === null && url.path.length > 1 && url.path[0] === '') {
        output += '/.';
    }
    output += serializePath(url);
    if (url.query !== null) output += '?' + url.query;
    if (!excludeFragment && url.fragment !== null) output += '#' + url.fragment;
    return output;
}

function serializeOrigin(url: URLRecord): string {
    switch (url.scheme) {
        case 'blob': {
            const pathURL = basicURLParse(serializePath(url), null);
            if (pathURL === null || (pathURL.scheme !== 'http' && pathURL.scheme !== 'https')) return 'null';
            return serializeOrigin(pathURL);
        }
        case 'ftp':
        case 'http':
        case 'https':
        case 'ws':
        case 'wss': {
            let result = url.scheme + '://' + serializeHost(url.host!);
            if (url.port !== -1) result += ':' + String(url.port);
            return result;
        }
    }
    return 'null';
}

// ---- application/x-www-form-urlencoded (https://url.spec.whatwg.org/#urlencoded-parsing) ----

// The name/value list query parses to, flattened: [name, value, name, …].
function parseParams(query: string): string[] {
    const bytes = utf8EncodeString(query);
    const out: string[] = [];
    let start = 0;
    while (start <= bytes.length) {
        let end = start;
        while (end < bytes.length && bytes[end] !== 0x26) end++;
        if (end > start) {
            let eq = start;
            while (eq < end && bytes[eq] !== 0x3D) eq++;
            const name = bytes.slice(start, eq).map((b) => (b === 0x2B ? 0x20 : b));
            const value = eq < end ? bytes.slice(eq + 1, end).map((b) => (b === 0x2B ? 0x20 : b)) : [];
            out.push(utf8Decode(percentDecodeBytes(name)), utf8Decode(percentDecodeBytes(value)));
        }
        start = end + 1;
    }
    return out;
}

function serializeParams(list: string[]): string {
    let output = '';
    for (let i = 0; i < list.length; i += 2) {
        if (i !== 0) output += '&';
        output += percentEncodeString(list[i], FORM_URLENCODED, true) + '=' +
            percentEncodeString(list[i + 1], FORM_URLENCODED, true);
    }
    return output;
}

// `${v}` as a USVString: a lone surrogate becomes U+FFFD.
function toUSVString(v: any): string {
    const s = `${v}`;
    return fromCodePoints(codePoints(s), 0, codePoints(s).length);
}

// ---- URLSearchParams ----

const ITER_KEYS = 0;
const ITER_VALUES = 1;
const ITER_ENTRIES = 2;

class URLSearchParamsIterator {
    #target: URLSearchParams;
    #kind: number;
    #index = 0;

    constructor(target: URLSearchParams, kind: number) {
        this.#target = target;
        this.#kind = kind;
    }

    next(): IteratorResult<any> {
        const list = this.#target._list();
        const index = this.#index;
        if (index >= list.length) return { value: undefined, done: true };
        this.#index = index + 2;
        const name = list[index];
        const value = list[index + 1];
        if (this.#kind === ITER_KEYS) return { value: name, done: false };
        if (this.#kind === ITER_VALUES) return { value: value, done: false };
        return { value: [name, value], done: false };
    }

    [Symbol.iterator](): URLSearchParamsIterator { return this; }

    get [Symbol.toStringTag](): string { return 'URLSearchParams Iterator'; }

    [inspect.custom](depth: number, options: any): string {
        if (depth < 0) return '[Object]';
        const opts = Object.assign({}, options, { depth: options.depth === null ? null : options.depth - 1 });
        const list = this.#target._list();
        const output: string[] = [];
        for (let i = this.#index; i < list.length; i += 2) {
            if (this.#kind === ITER_KEYS) output.push(inspect(list[i], opts));
            else if (this.#kind === ITER_VALUES) output.push(inspect(list[i + 1], opts));
            else output.push(inspect([list[i], list[i + 1]], opts));
        }
        const joined = output.join(', ');
        const body = joined.includes('\n') ? `\n  ${output.join(',\n  ')}` : ` ${joined}`;
        return `URLSearchParams Iterator {${body} }`;
    }
}

export class URLSearchParams {
    #list: string[] = [];
    // The URL whose query this is (Node's #context), written back to on
    // every change.
    #url: URL | null = null;

    constructor(init?: any) {
        if (init === undefined || init === null && arguments.length === 0) {
            return;
        }
        if (init instanceof URLSearchParams) {
            this.#list = init._list().slice();
        } else if (init !== null && typeof init === 'object' && typeof init[Symbol.iterator] === 'function') {
            for (const pair of init) {
                if (pair === null || pair === undefined || typeof pair === 'string') {
                    throw new NodeTypeError('ERR_INVALID_TUPLE', 'Each query pair must be an iterable [name, value] tuple');
                }
                const items: any[] = [];
                for (const element of pair) items.push(element);
                if (items.length !== 2) {
                    throw new NodeTypeError('ERR_INVALID_TUPLE', 'Each query pair must be an iterable [name, value] tuple');
                }
                this.#list.push(toUSVString(items[0]), toUSVString(items[1]));
            }
        } else if (init !== null && typeof init === 'object') {
            // A record: its own enumerable string keys, the later of two keys
            // equal as USVStrings winning.
            const seen = new Map<string, number>();
            for (const key of Object.keys(init)) {
                const k = toUSVString(key);
                const v = toUSVString(init[key]);
                const at = seen.get(k);
                if (at !== undefined) {
                    this.#list[at + 1] = v;
                } else {
                    seen.set(k, this.#list.length);
                    this.#list.push(k, v);
                }
            }
        } else {
            let s = toUSVString(init);
            if (s.startsWith('?')) s = s.slice(1);
            this.#list = s === '' ? [] : parseParams(s);
        }
    }

    // The flat name/value list (internal).
    _list(): string[] { return this.#list; }

    // Binds the params to url (internal: URL's searchParams).
    _bind(url: URL, query: string): void {
        this.#url = url;
        this.#list = query === '' ? [] : parseParams(query);
    }

    // Replaces the list from url's new query (internal).
    _reset(query: string | null): void {
        this.#list = query === null || query === '' ? [] : parseParams(query);
    }

    #update(): void {
        if (this.#url !== null) this.#url._setQueryFromParams(serializeParams(this.#list));
    }

    get size(): number { return this.#list.length / 2; }

    append(name: string, value: string): void {
        if (arguments.length < 2) throw missingArgs('name', 'value');
        this.#list.push(toUSVString(name), toUSVString(value));
        this.#update();
    }

    delete(name: string, value?: string): void {
        if (arguments.length < 1) throw missingArgs('name');
        const n = toUSVString(name);
        const v = value === undefined ? undefined : toUSVString(value);
        const kept: string[] = [];
        for (let i = 0; i < this.#list.length; i += 2) {
            if (this.#list[i] === n && (v === undefined || this.#list[i + 1] === v)) continue;
            kept.push(this.#list[i], this.#list[i + 1]);
        }
        this.#list = kept;
        this.#update();
    }

    get(name: string): string | null {
        if (arguments.length < 1) throw missingArgs('name');
        const n = toUSVString(name);
        for (let i = 0; i < this.#list.length; i += 2) {
            if (this.#list[i] === n) return this.#list[i + 1];
        }
        return null;
    }

    getAll(name: string): string[] {
        if (arguments.length < 1) throw missingArgs('name');
        const n = toUSVString(name);
        const values: string[] = [];
        for (let i = 0; i < this.#list.length; i += 2) {
            if (this.#list[i] === n) values.push(this.#list[i + 1]);
        }
        return values;
    }

    has(name: string, value?: string): boolean {
        if (arguments.length < 1) throw missingArgs('name');
        const n = toUSVString(name);
        const v = value === undefined ? undefined : toUSVString(value);
        for (let i = 0; i < this.#list.length; i += 2) {
            if (this.#list[i] === n && (v === undefined || this.#list[i + 1] === v)) return true;
        }
        return false;
    }

    set(name: string, value: string): void {
        if (arguments.length < 2) throw missingArgs('name', 'value');
        const n = toUSVString(name);
        const v = toUSVString(value);
        const out: string[] = [];
        let found = false;
        for (let i = 0; i < this.#list.length; i += 2) {
            if (this.#list[i] === n) {
                if (found) continue;
                found = true;
                out.push(n, v);
            } else {
                out.push(this.#list[i], this.#list[i + 1]);
            }
        }
        if (!found) out.push(n, v);
        this.#list = out;
        this.#update();
    }

    // A stable sort by name, comparing UTF-16 code units.
    sort(): void {
        const a = this.#list;
        for (let i = 2; i < a.length; i += 2) {
            const key = a[i];
            const value = a[i + 1];
            let j = i - 2;
            for (; j >= 0; j -= 2) {
                if (a[j] > key) {
                    a[j + 2] = a[j];
                    a[j + 3] = a[j + 1];
                } else {
                    break;
                }
            }
            a[j + 2] = key;
            a[j + 3] = value;
        }
        this.#update();
    }

    entries(): URLSearchParamsIterator { return new URLSearchParamsIterator(this, ITER_ENTRIES); }
    keys(): URLSearchParamsIterator { return new URLSearchParamsIterator(this, ITER_KEYS); }
    values(): URLSearchParamsIterator { return new URLSearchParamsIterator(this, ITER_VALUES); }
    [Symbol.iterator](): URLSearchParamsIterator { return new URLSearchParamsIterator(this, ITER_ENTRIES); }

    get [Symbol.toStringTag](): string { return 'URLSearchParams'; }

    forEach(callback: (value: string, key: string, parent: URLSearchParams) => void, thisArg?: any): void {
        if (typeof callback !== 'function') {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "callback" argument must be of type function. Received ' + (callback === undefined ? 'undefined' : typeof callback));
        }
        for (let i = 0; i < this.#list.length; i += 2) {
            callback.call(thisArg, this.#list[i + 1], this.#list[i], this);
        }
    }

    toString(): string { return serializeParams(this.#list); }

    [inspect.custom](depth: number, options: any): string {
        if (depth < 0) return '[Object]';
        const opts = Object.assign({}, options, { depth: options.depth === null ? null : options.depth - 1 });
        const output: string[] = [];
        for (let i = 0; i < this.#list.length; i += 2) {
            output.push(`${inspect(this.#list[i], opts)} => ${inspect(this.#list[i + 1], opts)}`);
        }
        const separator = ', ';
        let length = -separator.length;
        for (const o of output) length += o.length + separator.length;
        const breakLength = typeof options.breakLength === 'number' ? options.breakLength : 80;
        if (length > breakLength) return `URLSearchParams {\n  ${output.join(',\n  ')} }`;
        if (output.length > 0) return `URLSearchParams { ${output.join(separator)} }`;
        return 'URLSearchParams {}';
    }
}

// ---- URL ----

function invalidURL(input: string, base?: string): NodeTypeError {
    const err: any = new NodeTypeError('ERR_INVALID_URL', 'Invalid URL');
    err.input = input;
    if (base !== undefined) err.base = base;
    return err;
}

export class URL {
    #url: URLRecord;
    #searchParams: URLSearchParams | null = null;

    get [Symbol.toStringTag](): string { return 'URL'; }

    constructor(input: any, base?: any) {
        if (arguments.length === 0) throw missingArgs('url');
        const text = `${input}`;
        let parsedBase: URLRecord | null = null;
        let baseText: string | undefined = undefined;
        if (base !== undefined) {
            baseText = `${base}`;
            parsedBase = basicURLParse(baseText, null);
            if (parsedBase === null) throw invalidURL(text, baseText);
        }
        const parsed = basicURLParse(text, parsedBase);
        if (parsed === null) throw invalidURL(text, baseText);
        this.#url = parsed;
    }

    static parse(input: any, base?: any): URL | null {
        if (arguments.length === 0) throw missingArgs('url');
        try {
            return new URL(input, base);
        } catch (e) {
            return null;
        }
    }

    static canParse(input: any, base?: any): boolean {
        if (arguments.length === 0) throw missingArgs('url');
        let parsedBase: URLRecord | null = null;
        if (base !== undefined) {
            parsedBase = basicURLParse(`${base}`, null);
            if (parsedBase === null) return false;
        }
        return basicURLParse(`${input}`, parsedBase) !== null;
    }

    // Called by URLSearchParams when it changes (internal).
    _setQueryFromParams(serialized: string): void {
        this.#url.query = serialized === '' ? null : serialized;
    }

    #resetParams(): void {
        if (this.#searchParams !== null) this.#searchParams._reset(this.#url.query);
    }

    get href(): string { return serializeURL(this.#url, false); }
    set href(value: string) {
        const v = `${value}`;
        const parsed = basicURLParse(v, null);
        if (parsed === null) throw invalidURL(v);
        this.#url = parsed;
        this.#resetParams();
    }

    get origin(): string { return serializeOrigin(this.#url); }

    get protocol(): string { return this.#url.scheme + ':'; }
    set protocol(value: string) {
        parseInto(`${value}` + ':', this.#url, S_SCHEME_START);
    }

    get username(): string { return this.#url.username; }
    set username(value: string) {
        if (this.#url.cannotHaveUsernamePasswordPort()) return;
        this.#url.username = percentEncodeString(`${value}`, USERINFO, false);
    }

    get password(): string { return this.#url.password; }
    set password(value: string) {
        if (this.#url.cannotHaveUsernamePasswordPort()) return;
        this.#url.password = percentEncodeString(`${value}`, USERINFO, false);
    }

    get host(): string {
        const url = this.#url;
        if (url.host === null) return '';
        if (url.port === -1) return serializeHost(url.host);
        return serializeHost(url.host) + ':' + String(url.port);
    }
    set host(value: string) {
        if (this.#url.hasOpaquePath()) return;
        parseInto(`${value}`, this.#url, S_HOST);
    }

    get hostname(): string {
        return this.#url.host === null ? '' : serializeHost(this.#url.host);
    }
    set hostname(value: string) {
        if (this.#url.hasOpaquePath()) return;
        parseInto(`${value}`, this.#url, S_HOSTNAME);
    }

    get port(): string { return this.#url.port === -1 ? '' : String(this.#url.port); }
    set port(value: string) {
        if (this.#url.cannotHaveUsernamePasswordPort()) return;
        const v = `${value}`;
        if (v === '') {
            this.#url.port = -1;
        } else {
            parseInto(v, this.#url, S_PORT);
        }
    }

    get pathname(): string { return serializePath(this.#url); }
    set pathname(value: string) {
        if (this.#url.hasOpaquePath()) return;
        this.#url.path = [];
        parseInto(`${value}`, this.#url, S_PATH_START);
    }

    get search(): string {
        const q = this.#url.query;
        return q === null || q === '' ? '' : '?' + q;
    }
    set search(value: string) {
        const v = toUSVString(value);
        if (v === '') {
            this.#url.query = null;
            this.#resetParams();
            return;
        }
        const input = v.startsWith('?') ? v.substring(1) : v;
        this.#url.query = '';
        parseInto(input, this.#url, S_QUERY);
        this.#resetParams();
    }

    get searchParams(): URLSearchParams {
        if (this.#searchParams === null) {
            const params = new URLSearchParams();
            params._bind(this, this.#url.query === null ? '' : this.#url.query);
            this.#searchParams = params;
        }
        return this.#searchParams;
    }

    get hash(): string {
        const f = this.#url.fragment;
        return f === null || f === '' ? '' : '#' + f;
    }
    set hash(value: string) {
        const v = `${value}`;
        if (v === '') {
            this.#url.fragment = null;
            return;
        }
        const input = v.startsWith('#') ? v.substring(1) : v;
        this.#url.fragment = '';
        parseInto(input, this.#url, S_FRAGMENT);
    }

    toString(): string { return this.href; }
    toJSON(): string { return this.href; }

    [inspect.custom](depth: number, options: any): any {
        if (depth < 0) return this;
        const obj = {
            href: this.href,
            origin: this.origin,
            protocol: this.protocol,
            username: this.username,
            password: this.password,
            host: this.host,
            hostname: this.hostname,
            port: this.port,
            pathname: this.pathname,
            search: this.search,
            searchParams: this.searchParams,
            hash: this.hash,
        };
        return `${this.constructor.name} ${inspect(obj, options)}`;
    }
}

// ---- url module helpers ----

export function domainToASCII(domain: string): string {
    if (arguments.length === 0) throw missingArgs('domain');
    const host = parseHost(`${domain}`, false);
    return host === null ? '' : serializeHost(host);
}

export function domainToUnicode(domain: string): string {
    if (arguments.length === 0) throw missingArgs('domain');
    const host = parseHost(`${domain}`, false);
    if (host === null) return '';
    return host.kind === HOST_DOMAIN ? domainToUnicodeInternal(host.name) : serializeHost(host);
}
