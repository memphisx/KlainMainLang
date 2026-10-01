// The part of Node's `url` written in TypeScript: the legacy API (`Url`,
// `parse`, `format`, `resolve`, `resolveObject`), ported from Node v24's
// lib/url.js. The rest of `url` (`URL`, `URLSearchParams`, `fileURLToPath`,
// `pathToFileURL`, `urlToHttpOptions`, `domainToASCII`, `domainToUnicode`)
// is the compiler's own; a program importing `url` imports this module too.
import { parse as qsParse, stringify as qsStringify } from 'querystring';
import { domainToASCII, domainToUnicode } from 'url';
import type { ParsedUrlQuery, ParsedUrlQueryInput } from 'querystring';

export interface UrlObject {
    auth?: string | null | undefined;
    hash?: string | null | undefined;
    host?: string | null | undefined;
    hostname?: string | null | undefined;
    href?: string | null | undefined;
    pathname?: string | null | undefined;
    protocol?: string | null | undefined;
    search?: string | null | undefined;
    slashes?: boolean | null | undefined;
    port?: string | number | null | undefined;
    query?: string | null | ParsedUrlQueryInput | undefined;
}

export interface URLFormatOptions {
    auth?: boolean | undefined;
    fragment?: boolean | undefined;
    search?: boolean | undefined;
    unicode?: boolean | undefined;
}

class UrlError extends TypeError {
    code: string;
    input: string;
    constructor(input: string) {
        super('Invalid URL');
        this.code = 'ERR_INVALID_URL';
        this.input = input;
    }
}

// Protocols that can allow "unsafe" and "unwise" chars.
function isUnsafeProtocol(p: string | null): boolean {
    return p === 'javascript' || p === 'javascript:';
}
// Protocols that never have a hostname.
function isHostlessProtocol(p: string | null): boolean {
    return p === 'javascript' || p === 'javascript:';
}
// Protocols that always contain a // bit.
const slashedProtocols = ['http', 'http:', 'https', 'https:', 'ftp', 'ftp:', 'gopher', 'gopher:', 'file', 'file:', 'ws', 'ws:', 'wss', 'wss:'];
function isSlashedProtocol(p: string | null | undefined): boolean {
    return p !== null && p !== undefined && slashedProtocols.indexOf(p) !== -1;
}

const protocolPattern = /^[a-z0-9.+-]+:/i;
const portPattern = /:[0-9]*$/;
const hostPattern = /^\/\/[^@/]+@[^@/]+/;
// Special case for a simple path URL
const simplePathPattern = /^(\/\/?(?!\/)[^?\s]*)(\?[^\s]*)?$/;
const hostnameMaxLen = 255;
const forbiddenHostChars = /[\0\t\n\r #%/:<>?@[\\\]^|]/;
const forbiddenHostCharsIpv6 = /[\0\t\n\r #%/<>?@\\^|]/;

const CHAR_SPACE = 32;
const CHAR_TAB = 9;
const CHAR_CARRIAGE_RETURN = 13;
const CHAR_LINE_FEED = 10;
const CHAR_NO_BREAK_SPACE = 160;
const CHAR_ZERO_WIDTH_NOBREAK_SPACE = 65279;
const CHAR_HASH = 35;
const CHAR_FORWARD_SLASH = 47;
const CHAR_LEFT_SQUARE_BRACKET = 91;
const CHAR_RIGHT_SQUARE_BRACKET = 93;
const CHAR_LEFT_ANGLE_BRACKET = 60;
const CHAR_RIGHT_ANGLE_BRACKET = 62;
const CHAR_LEFT_CURLY_BRACKET = 123;
const CHAR_RIGHT_CURLY_BRACKET = 125;
const CHAR_QUESTION_MARK = 63;
const CHAR_DOUBLE_QUOTE = 34;
const CHAR_SINGLE_QUOTE = 39;
const CHAR_PERCENT = 37;
const CHAR_SEMICOLON = 59;
const CHAR_BACKWARD_SLASH = 92;
const CHAR_CIRCUMFLEX_ACCENT = 94;
const CHAR_GRAVE_ACCENT = 96;
const CHAR_VERTICAL_LINE = 124;
const CHAR_AT = 64;
const CHAR_COLON = 58;

function isIpv6Hostname(hostname: string): boolean {
    return hostname.charCodeAt(0) === CHAR_LEFT_SQUARE_BRACKET &&
        hostname.charCodeAt(hostname.length - 1) === CHAR_RIGHT_SQUARE_BRACKET;
}

export class Url {
    protocol: string | null = null;
    slashes: boolean | null = null;
    auth: string | null = null;
    host: string | null = null;
    port: string | null = null;
    hostname: string | null = null;
    hash: string | null = null;
    search: string | null = null;
    query: string | null | ParsedUrlQuery = null;
    pathname: string | null = null;
    path: string | null = null;
    href: string | null = null;

    parse(url: string, parseQueryString?: boolean, slashesDenoteHost?: boolean): Url {
        // Copy chrome, IE, opera backslash-handling behavior.
        let hasHash = false;
        let hasAt = false;
        let start = -1;
        let end = -1;
        let rest = '';
        let lastPos = 0;
        let inWs = false;
        let split = false;
        for (let i = 0; i < url.length; ++i) {
            const code = url.charCodeAt(i);
            // Find first and last non-whitespace characters for trimming
            const isWs = code < 33 || code === CHAR_NO_BREAK_SPACE || code === CHAR_ZERO_WIDTH_NOBREAK_SPACE;
            if (start === -1) {
                if (isWs) continue;
                lastPos = start = i;
            } else if (inWs) {
                if (!isWs) {
                    end = -1;
                    inWs = false;
                }
            } else if (isWs) {
                end = i;
                inWs = true;
            }
            // Only convert backslashes while we haven't seen a split character
            if (!split) {
                if (code === CHAR_AT) {
                    hasAt = true;
                } else if (code === CHAR_HASH) {
                    hasHash = true;
                    split = true;
                } else if (code === CHAR_QUESTION_MARK) {
                    split = true;
                } else if (code === CHAR_BACKWARD_SLASH) {
                    if (i - lastPos > 0) rest += url.slice(lastPos, i);
                    rest += '/';
                    lastPos = i + 1;
                }
            } else if (!hasHash && code === CHAR_HASH) {
                hasHash = true;
            }
        }
        // Check if string was non-empty (including strings with only whitespace)
        if (start !== -1) {
            if (lastPos === start) {
                // We didn't convert any backslashes
                if (end === -1) {
                    rest = start === 0 ? url : url.slice(start);
                } else {
                    rest = url.slice(start, end);
                }
            } else if (end === -1 && lastPos < url.length) {
                rest += url.slice(lastPos);
            } else if (end !== -1 && lastPos < end) {
                rest += url.slice(lastPos, end);
            }
        }

        if (!slashesDenoteHost && !hasHash && !hasAt) {
            // Try fast path regexp
            const simplePath = simplePathPattern.exec(rest);
            if (simplePath) {
                this.path = rest;
                this.href = rest;
                this.pathname = simplePath[1];
                if (simplePath[2]) {
                    this.search = simplePath[2];
                    if (parseQueryString) {
                        this.query = qsParse(this.search.slice(1));
                    } else {
                        this.query = this.search.slice(1);
                    }
                } else if (parseQueryString) {
                    this.search = null;
                    this.query = qsParse('');
                }
                return this;
            }
        }

        const protoMatch = protocolPattern.exec(rest);
        let proto: string | null = null;
        let lowerProto: string | null = null;
        if (protoMatch) {
            proto = protoMatch[0];
            lowerProto = proto.toLowerCase();
            this.protocol = lowerProto;
            rest = rest.slice(proto.length);
        }

        // Figure out if it's got a host: user@server is *always* a hostname,
        // and //foo/bar resolves as host=foo, path=bar.
        let slashes = false;
        if (slashesDenoteHost || proto !== null || hostPattern.test(rest)) {
            slashes = rest.charCodeAt(0) === CHAR_FORWARD_SLASH && rest.charCodeAt(1) === CHAR_FORWARD_SLASH;
            if (slashes && !(proto !== null && isHostlessProtocol(lowerProto))) {
                rest = rest.slice(2);
                this.slashes = true;
            }
        }

        if (!isHostlessProtocol(lowerProto) && (slashes || (proto !== null && !isSlashedProtocol(proto)))) {
            // There's a hostname: the first instance of /, ?, ;, or # ends it.
            // An @ in the hostname allows non-host chars to its left, unless a
            // host-ending character comes before it.
            let hostEnd = -1;
            let atSign = -1;
            let nonHost = -1;
            for (let i = 0; i < rest.length; ++i) {
                const c = rest.charCodeAt(i);
                if (c === CHAR_TAB || c === CHAR_LINE_FEED || c === CHAR_CARRIAGE_RETURN) {
                    // WHATWG URL removes tabs, newlines, and carriage returns.
                    rest = rest.slice(0, i) + rest.slice(i + 1);
                    i -= 1;
                } else if (c === CHAR_SPACE || c === CHAR_DOUBLE_QUOTE || c === CHAR_PERCENT || c === CHAR_SINGLE_QUOTE ||
                    c === CHAR_SEMICOLON || c === CHAR_LEFT_ANGLE_BRACKET || c === CHAR_RIGHT_ANGLE_BRACKET ||
                    c === CHAR_BACKWARD_SLASH || c === CHAR_CIRCUMFLEX_ACCENT || c === CHAR_GRAVE_ACCENT ||
                    c === CHAR_LEFT_CURLY_BRACKET || c === CHAR_VERTICAL_LINE || c === CHAR_RIGHT_CURLY_BRACKET) {
                    // Characters that are never ever allowed in a hostname from RFC 2396
                    if (nonHost === -1) nonHost = i;
                } else if (c === CHAR_HASH || c === CHAR_FORWARD_SLASH || c === CHAR_QUESTION_MARK) {
                    // Find the first instance of any host-ending characters
                    if (nonHost === -1) nonHost = i;
                    hostEnd = i;
                } else if (c === CHAR_AT) {
                    // Either an explicit point the auth cannot go past, or the
                    // last @ decides.
                    atSign = i;
                    nonHost = -1;
                }
                if (hostEnd !== -1) break;
            }
            start = 0;
            if (atSign !== -1) {
                this.auth = decodeURIComponent(rest.slice(0, atSign));
                start = atSign + 1;
            }
            if (nonHost === -1) {
                this.host = rest.slice(start);
                rest = '';
            } else {
                this.host = rest.slice(start, nonHost);
                rest = rest.slice(nonHost);
            }

            // pull out port.
            this.parseHost();

            // There is a hostname, so even an empty one is present.
            if (typeof this.hostname !== 'string') this.hostname = '';

            const hostname: string = this.hostname ?? '';
            // [...] is an IPv6 address.
            const ipv6Hostname = isIpv6Hostname(hostname);

            // validate a little.
            if (!ipv6Hostname) {
                rest = getHostname(this, rest, hostname);
            }

            const hn = this.hostname ?? '';
            if (hn.length > hostnameMaxLen) {
                this.hostname = '';
            } else {
                // Hostnames are always lower case.
                this.hostname = hn.toLowerCase();
            }

            if (this.hostname !== '') {
                if (ipv6Hostname) {
                    if (forbiddenHostCharsIpv6.test(this.hostname)) {
                        throw new UrlError(url);
                    }
                } else {
                    // IDNA: the punycoded representation of the domain.
                    this.hostname = domainToASCII(this.hostname);
                    // An empty or forbidden result came from toASCII: a
                    // spoofing attempt.
                    if (this.hostname === '' || forbiddenHostChars.test(this.hostname)) {
                        throw new UrlError(url);
                    }
                }
            }

            const p = this.port ? ':' + this.port : '';
            const h = this.hostname || '';
            this.host = h + p;

            // strip [ and ] from the hostname; the host field keeps them.
            if (ipv6Hostname) {
                this.hostname = h.slice(1, -1);
                if (rest[0] !== '/') {
                    rest = '/' + rest;
                }
            }
        }

        // Now rest is set to the post-host stuff: escape the "autoEscape"
        // chars, even if encodeURIComponent doesn't think they need to be.
        if (!isUnsafeProtocol(lowerProto)) {
            rest = autoEscapeStr(rest);
        }

        let questionIdx = -1;
        let hashIdx = -1;
        for (let i = 0; i < rest.length; ++i) {
            const code = rest.charCodeAt(i);
            if (code === CHAR_HASH) {
                this.hash = rest.slice(i);
                hashIdx = i;
                break;
            } else if (code === CHAR_QUESTION_MARK && questionIdx === -1) {
                questionIdx = i;
            }
        }

        if (questionIdx !== -1) {
            let q: string;
            if (hashIdx === -1) {
                this.search = rest.slice(questionIdx);
                q = rest.slice(questionIdx + 1);
            } else {
                this.search = rest.slice(questionIdx, hashIdx);
                q = rest.slice(questionIdx + 1, hashIdx);
            }
            if (parseQueryString) {
                this.query = qsParse(q);
            } else {
                this.query = q;
            }
        } else if (parseQueryString) {
            // No query string, but parseQueryString still requested
            this.search = null;
            this.query = qsParse('');
        }

        const useQuestionIdx = questionIdx !== -1 && (hashIdx === -1 || questionIdx < hashIdx);
        const firstIdx = useQuestionIdx ? questionIdx : hashIdx;
        if (firstIdx === -1) {
            if (rest.length > 0) this.pathname = rest;
        } else if (firstIdx > 0) {
            this.pathname = rest.slice(0, firstIdx);
        }
        if (isSlashedProtocol(lowerProto) && this.hostname && !this.pathname) {
            this.pathname = '/';
        }

        // To support http.request
        if (this.pathname || this.search) {
            const pn = this.pathname || '';
            const s = this.search || '';
            this.path = pn + s;
        }

        // Finally, reconstruct the href based on what has been validated.
        this.href = this.format();
        return this;
    }

    format(): string {
        return formatUrlObject(this);
    }

    resolve(relative: string): string {
        return this.resolveObject(urlParse(relative, false, true)).format();
    }

    resolveObject(relativeArg: string | Url): Url {
        let relative: Url;
        if (typeof relativeArg === 'string') {
            relative = new Url();
            relative.parse(relativeArg, false, true);
        } else {
            relative = relativeArg;
        }

        const result = new Url();
        copyUrl(result, this);

        // Hash is always overridden, no matter what; even href="" removes it.
        result.hash = relative.hash;

        // An empty relative url leaves nothing to do.
        if (relative.href === '') {
            result.href = result.format();
            return result;
        }

        // Hrefs like //foo/bar always cut to the protocol.
        if (relative.slashes && !relative.protocol) {
            // Take everything except the protocol from relative.
            const proto = result.protocol;
            copyUrl(result, relative);
            result.protocol = proto;
            // urlParse appends a trailing / to urls like http://www.example.com
            if (isSlashedProtocol(result.protocol) && result.hostname && !result.pathname) {
                result.path = result.pathname = '/';
            }
            result.href = result.format();
            return result;
        }

        if (relative.protocol && relative.protocol !== result.protocol) {
            // A known protocol other than file: must have a host, and a path
            // when there was one; file: drops the host; anything else is
            // absolute.
            if (!isSlashedProtocol(relative.protocol)) {
                copyUrl(result, relative);
                result.href = result.format();
                return result;
            }

            result.protocol = relative.protocol;
            if (!relative.host && !/^file:?$/.test(relative.protocol) && !isHostlessProtocol(relative.protocol)) {
                const relPath = (relative.pathname || '').split('/');
                let first: string | undefined = '';
                while (relPath.length && !(first = relPath.shift()));
                relative.host = first || '';
                if (!relative.hostname) relative.hostname = '';
                if (relPath[0] !== '') relPath.unshift('');
                if (relPath.length < 2) relPath.unshift('');
                result.pathname = relPath.join('/');
            } else {
                result.pathname = relative.pathname;
            }
            result.search = relative.search;
            result.query = relative.query;
            result.host = relative.host || '';
            result.auth = relative.auth;
            result.hostname = relative.hostname || relative.host;
            result.port = relative.port;
            // To support http.request
            if (result.pathname || result.search) {
                const p = result.pathname || '';
                const s = result.search || '';
                result.path = p + s;
            }
            result.slashes = result.slashes || relative.slashes;
            result.href = result.format();
            return result;
        }

        const isSourceAbs = !!(result.pathname && result.pathname.charAt(0) === '/');
        const isRelAbs = !!(relative.host || (relative.pathname && relative.pathname.charAt(0) === '/'));
        let mustEndAbs = isRelAbs || isSourceAbs || !!(result.host && relative.pathname);
        const removeAllDots = mustEndAbs;
        let srcPath: string[] = result.pathname ? result.pathname.split('/') : [];
        const relPath: string[] = relative.pathname ? relative.pathname.split('/') : [];
        const noLeadingSlashes = !!result.protocol && !isSlashedProtocol(result.protocol);

        // A non-slashed url lets relative links like ../.. crawl up to the
        // hostname; the first path part goes into the host field later.
        if (noLeadingSlashes) {
            result.hostname = '';
            result.port = null;
            if (result.host) {
                if (srcPath[0] === '') srcPath[0] = result.host;
                else srcPath.unshift(result.host);
            }
            result.host = '';
            if (relative.protocol) {
                relative.hostname = null;
                relative.port = null;
                result.auth = null;
                if (relative.host) {
                    if (relPath[0] === '') relPath[0] = relative.host;
                    else relPath.unshift(relative.host);
                }
                relative.host = null;
            }
            mustEndAbs = mustEndAbs && (relPath[0] === '' || srcPath[0] === '');
        }

        if (isRelAbs) {
            // it's absolute.
            if (relative.host || relative.host === '') {
                if (result.host !== relative.host) result.auth = null;
                result.host = relative.host;
                result.port = relative.port;
            }
            if (relative.hostname || relative.hostname === '') {
                if (result.hostname !== relative.hostname) result.auth = null;
                result.hostname = relative.hostname;
            }
            result.search = relative.search;
            result.query = relative.query;
            srcPath = relPath;
            // Fall through to the dot-handling below.
        } else if (relPath.length) {
            // it's relative: throw away the existing file, take the new path.
            srcPath.pop();
            srcPath = srcPath.concat(relPath);
            result.search = relative.search;
            result.query = relative.query;
        } else if (relative.search !== null && relative.search !== undefined) {
            // Just pull out the search, like href='?foo'.
            if (noLeadingSlashes) {
                const h = srcPath.shift() ?? null;
                result.hostname = result.host = h;
                // The auth can get stuck only in host (mailto:local1@domain1).
                if (result.host && result.host.indexOf('@') > 0) {
                    const authInHost = result.host.split('@');
                    result.auth = authInHost.shift() ?? null;
                    result.host = result.hostname = authInHost.shift() ?? null;
                }
            }
            result.search = relative.search;
            result.query = relative.query;
            // To support http.request
            if (result.pathname !== null || result.search !== null) {
                result.path = (result.pathname ? result.pathname : '') + (result.search ? result.search : '');
            }
            result.href = result.format();
            return result;
        }

        if (!srcPath.length) {
            // No path at all; everything else was handled above.
            result.pathname = null;
            // To support http.request
            if (result.search) {
                result.path = '/' + result.search;
            } else {
                result.path = null;
            }
            result.href = result.format();
            return result;
        }

        // A url ending in . or .. gets a trailing slash; anything else
        // non-slashy does not.
        let last = srcPath[srcPath.length - 1];
        const hasTrailingSlash = ((!!(result.host || relative.host) || srcPath.length > 1) &&
            (last === '.' || last === '..')) || last === '';

        // Strip single dots, resolve double dots to the parent dir; going
        // above the root leaves `up` > 0.
        let up = 0;
        for (let i = srcPath.length - 1; i >= 0; i--) {
            last = srcPath[i];
            if (last === '.') {
                srcPath.splice(i, 1);
            } else if (last === '..') {
                srcPath.splice(i, 1);
                up++;
            } else if (up) {
                srcPath.splice(i, 1);
                up--;
            }
        }

        // If the path is allowed to go above the root, restore leading ..s
        if (!mustEndAbs && !removeAllDots) {
            while (up--) {
                srcPath.unshift('..');
            }
        }

        if (mustEndAbs && srcPath[0] !== '' && (!srcPath[0] || srcPath[0].charAt(0) !== '/')) {
            srcPath.unshift('');
        }

        const joined = srcPath.join('/');
        if (hasTrailingSlash && joined.charAt(joined.length - 1) !== '/') {
            srcPath.push('');
        }

        const isAbsolute = srcPath[0] === '' || (!!srcPath[0] && srcPath[0].charAt(0) === '/');

        // put the host back
        if (noLeadingSlashes) {
            const h = isAbsolute ? '' : srcPath.length ? (srcPath.shift() ?? '') : '';
            result.hostname = result.host = h;
            // The auth can get stuck only in host (mailto:local1@domain1).
            if (result.host && result.host.indexOf('@') > 0) {
                const authInHost = result.host.split('@');
                result.auth = authInHost.shift() ?? null;
                result.host = result.hostname = authInHost.shift() ?? null;
            }
        }

        mustEndAbs = mustEndAbs || (!!result.host && srcPath.length > 0);

        if (mustEndAbs && !isAbsolute) {
            srcPath.unshift('');
        }

        if (!srcPath.length) {
            result.pathname = null;
            result.path = null;
        } else {
            result.pathname = srcPath.join('/');
        }

        // To support request.http
        if (result.pathname !== null || result.search !== null) {
            result.path = (result.pathname ? result.pathname : '') + (result.search ? result.search : '');
        }
        result.auth = relative.auth || result.auth;
        result.slashes = result.slashes || relative.slashes;
        result.href = result.format();
        return result;
    }

    parseHost(): void {
        let host = this.host ?? '';
        const portMatch = portPattern.exec(host);
        if (portMatch) {
            const port = portMatch[0];
            if (port !== ':') {
                this.port = port.slice(1);
            }
            host = host.slice(0, host.length - port.length);
        }
        if (host) this.hostname = host;
    }
}

function copyUrl(to: Url, from: Url): void {
    to.protocol = from.protocol;
    to.slashes = from.slashes;
    to.auth = from.auth;
    to.host = from.host;
    to.port = from.port;
    to.hostname = from.hostname;
    to.hash = from.hash;
    to.search = from.search;
    to.query = from.query;
    to.pathname = from.pathname;
    to.path = from.path;
    to.href = from.href;
}

function getHostname(self: Url, rest: string, hostname: string): string {
    for (let i = 0; i < hostname.length; ++i) {
        const code = hostname.charCodeAt(i);
        const isValid = code !== CHAR_FORWARD_SLASH && code !== CHAR_BACKWARD_SLASH &&
            code !== CHAR_HASH && code !== CHAR_QUESTION_MARK && code !== CHAR_COLON;
        if (!isValid) {
            self.hostname = hostname.slice(0, i);
            return '/' + hostname.slice(i) + rest;
        }
    }
    return rest;
}

// Escaped characters, by code.
function escapedCode(code: number): string {
    switch (code) {
        case 9: return '%09';
        case 10: return '%0A';
        case 13: return '%0D';
        case 32: return '%20';
        case 34: return '%22';
        case 39: return '%27';
        case 60: return '%3C';
        case 62: return '%3E';
        case 92: return '%5C';
        case 94: return '%5E';
        case 96: return '%60';
        case 123: return '%7B';
        case 124: return '%7C';
        case 125: return '%7D';
    }
    return '';
}

// Escape all delimiters and unwise characters from RFC 2396, and single
// quotes in case of an XSS attack.
function autoEscapeStr(rest: string): string {
    let escaped = '';
    let lastEscapedPos = 0;
    for (let i = 0; i < rest.length; ++i) {
        const escapedChar = escapedCode(rest.charCodeAt(i));
        if (escapedChar) {
            if (i > lastEscapedPos) escaped += rest.slice(lastEscapedPos, i);
            escaped += escapedChar;
            lastEscapedPos = i + 1;
        }
    }
    if (lastEscapedPos === 0) return rest;
    if (lastEscapedPos < rest.length) escaped += rest.slice(lastEscapedPos);
    return escaped;
}

// The characters an auth keeps unescaped: ! - . _ ~ ' ( ) * : digits and
// letters.
function noEscapeAuth(c: number): boolean {
    return (c >= 48 && c <= 58) || (c >= 65 && c <= 90) || (c >= 97 && c <= 122) ||
        c === 33 || c === 39 || c === 40 || c === 41 || c === 42 || c === 45 || c === 46 || c === 95 || c === 126;
}

const hexDigits = '0123456789ABCDEF';
function hexByte(b: number): string {
    return '%' + hexDigits.charAt(b >> 4) + hexDigits.charAt(b & 15);
}

// Node's internal/querystring encodeStr with the auth table: percent-encode
// every UTF-8 byte of a character the table does not keep.
function encodeAuth(str: string): string {
    let out = '';
    let lastPos = 0;
    for (let i = 0; i < str.length; i++) {
        let c = str.charCodeAt(i);
        if (c < 0x80) {
            if (noEscapeAuth(c)) continue;
            if (lastPos < i) out += str.slice(lastPos, i);
            lastPos = i + 1;
            out += hexByte(c);
            continue;
        }
        if (lastPos < i) out += str.slice(lastPos, i);
        if (c < 0x800) {
            lastPos = i + 1;
            out += hexByte(0xC0 | (c >> 6)) + hexByte(0x80 | (c & 0x3F));
            continue;
        }
        if (c < 0xD800 || c >= 0xE000) {
            lastPos = i + 1;
            out += hexByte(0xE0 | (c >> 12)) + hexByte(0x80 | ((c >> 6) & 0x3F)) + hexByte(0x80 | (c & 0x3F));
            continue;
        }
        // Surrogate pair
        ++i;
        if (i >= str.length) {
            throw new URIError('URI malformed');
        }
        const c2 = str.charCodeAt(i) & 0x3FF;
        lastPos = i + 1;
        c = 0x10000 + (((c & 0x3FF) << 10) | c2);
        out += hexByte(0xF0 | (c >> 18)) + hexByte(0x80 | ((c >> 12) & 0x3F)) +
            hexByte(0x80 | ((c >> 6) & 0x3F)) + hexByte(0x80 | (c & 0x3F));
    }
    if (lastPos === 0) return str;
    if (lastPos < str.length) return out + str.slice(lastPos);
    return out;
}

// Url.prototype.format over any Url-shaped object.
function formatUrlObject(u: UrlObject): string {
    let auth = u.auth || '';
    if (auth) {
        auth = encodeAuth(auth);
        auth += '@';
    }

    let protocol = u.protocol || '';
    if (protocol && protocol.charCodeAt(protocol.length - 1) !== 58 /* : */) {
        protocol += ':';
    }

    let pathname = u.pathname || '';
    let hash = u.hash || '';
    let host = '';
    let query = '';

    if (u.host) {
        host = auth + u.host;
    } else if (u.hostname) {
        host = auth + (u.hostname.indexOf(':') !== -1 && !isIpv6Hostname(u.hostname) ?
            '[' + u.hostname + ']' :
            u.hostname);
        if (u.port) {
            host += ':' + u.port;
        }
    }

    const q = u.query;
    if (q !== null && q !== undefined && typeof q === 'object') {
        query = qsStringify(q);
    }
    let search = u.search || (query && ('?' + query)) || '';

    if (pathname.indexOf('#') !== -1 || pathname.indexOf('?') !== -1) {
        let newPathname = '';
        let lastPos = 0;
        const len = pathname.length;
        for (let i = 0; i < len; i++) {
            const code = pathname.charCodeAt(i);
            if (code === CHAR_HASH || code === CHAR_QUESTION_MARK) {
                if (i > lastPos) {
                    newPathname += pathname.slice(lastPos, i);
                }
                newPathname += (code === CHAR_HASH ? '%23' : '%3F');
                lastPos = i + 1;
            }
        }
        if (lastPos < len) {
            newPathname += pathname.slice(lastPos);
        }
        pathname = newPathname;
    }

    // Only the slashedProtocols get the //; not mailto:, xmpp:, etc., unless
    // they had them to begin with.
    if (u.slashes || isSlashedProtocol(protocol)) {
        if (u.slashes || host) {
            if (pathname && pathname.charCodeAt(0) !== CHAR_FORWARD_SLASH) pathname = '/' + pathname;
            host = '//' + host;
        } else if (protocol.length >= 4 && protocol.slice(0, 4) === 'file') {
            host = '//';
        }
    }

    // Escape '#' in search.
    if (search.indexOf('#') !== -1) {
        search = search.split('#').join('%23');
    }

    if (hash && hash.charCodeAt(0) !== CHAR_HASH) {
        hash = '#' + hash;
    }
    if (search && search.charCodeAt(0) !== CHAR_QUESTION_MARK) {
        search = '?' + search;
    }

    return protocol + host + pathname + search + hash;
}

let urlParseWarned = false;

function urlParse(url: string, parseQueryString?: boolean, slashesDenoteHost?: boolean): Url {
    if (!urlParseWarned) {
        urlParseWarned = true;
        process.emitWarning(
            '`url.parse()` behavior is not standardized and prone to ' +
            'errors that have security implications. Use the WHATWG URL API ' +
            'instead. CVEs are not issued for `url.parse()` vulnerabilities.',
            'DeprecationWarning',
            'DEP0169',
        );
    }
    const urlObject = new Url();
    urlObject.parse(url, parseQueryString, slashesDenoteHost);
    return urlObject;
}

export function parse(urlString: string, parseQueryString?: boolean, slashesDenoteHost?: boolean): Url {
    return urlParse(urlString, parseQueryString, slashesDenoteHost);
}

export function resolve(from: string, to: string): string {
    return urlParse(from, false, true).resolve(to);
}

export function resolveObject(source: string, relative: string): Url {
    return urlParse(source, false, true).resolveObject(relative);
}

// format(urlObject[, options]): a WHATWG URL serialized with the options'
// parts left out, a string parsed and reformatted, or a Url-shaped object.
export function format(urlObject: URL | UrlObject | string, options?: URLFormatOptions): string {
    if (typeof urlObject === 'string') {
        return urlParse(urlObject).format();
    }
    if (urlObject instanceof URL) {
        const fragment = options?.fragment ?? true;
        const unicode = options?.unicode ?? false;
        const search = options?.search ?? true;
        const auth = options?.auth ?? true;
        const u: URL = urlObject;
        let out = u.protocol;
        if (u.host !== '' || u.protocol === 'file:') {
            out += '//';
            if (auth && (u.username !== '' || u.password !== '')) {
                out += u.username;
                if (u.password !== '') out += ':' + u.password;
                out += '@';
            }
            out += unicode ? domainToUnicode(u.hostname) : u.hostname;
            if (u.port !== '') out += ':' + u.port;
        }
        out += u.pathname;
        if (search) out += u.search;
        if (fragment) out += u.hash;
        return out;
    }
    return formatUrlObject(urlObject);
}
