// Node's `querystring` module, ported from Node v24's lib/querystring.js and
// lib/internal/querystring.js.
// kml:default-namespace — `import querystring from 'querystring'` reads this
// module's exports, as Node's CommonJS module.exports is the namespace.

export interface StringifyOptions {
    encodeURIComponent?: ((str: string) => string) | undefined;
}

export interface ParseOptions {
    maxKeys?: number | undefined;
    decodeURIComponent?: ((str: string) => string) | undefined;
}

export interface ParsedUrlQuery extends NodeJS.Dict<string | string[]> {}

export interface ParsedUrlQueryInput extends
    NodeJS.Dict<
        | string
        | number
        | boolean
        | bigint
        | ReadonlyArray<string | number | boolean | bigint>
        | null
    >
{}

const isHexTable: number[] = [
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 0 - 15
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 16 - 31
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 32 - 47
    1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, // 48 - 63
    0, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 64 - 79
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 80 - 95
    0, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 96 - 111
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 112 - 127
];

const unhexTable: number[] = [
    -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, // 0 - 15
    -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, // 16 - 31
    -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, // 32 - 47
    0, 1, 2, 3, 4, 5, 6, 7, 8, 9, -1, -1, -1, -1, -1, -1, // 48 - 63
    -1, 10, 11, 12, 13, 14, 15, -1, -1, -1, -1, -1, -1, -1, -1, -1, // 64 - 79
    -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, // 80 - 95
    -1, 10, 11, 12, 13, 14, 15, -1, -1, -1, -1, -1, -1, -1, -1, -1, // 96 - 111
    -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, // 112 - 127
];

function hexOf(c: number): number {
    return c < 128 ? unhexTable[c] : -1;
}

function isHex(c: number): boolean {
    return c < 128 && isHexTable[c] === 1;
}

// A safe fast alternative to decodeURIComponent.
export function unescapeBuffer(s: string, decodeSpaces?: boolean): Buffer {
    const out = Buffer.allocUnsafe(s.length);
    let index = 0;
    let outIndex = 0;
    let currentChar = 0;
    let nextChar = 0;
    let hexHigh = 0;
    let hexLow = 0;
    const maxLength = s.length - 2;
    // Flag to know if some hex chars have been decoded
    let hasHex = false;
    while (index < s.length) {
        currentChar = s.charCodeAt(index);
        if (currentChar === 43 /* '+' */ && decodeSpaces) {
            out[outIndex++] = 32; // ' '
            index++;
            continue;
        }
        if (currentChar === 37 /* '%' */ && index < maxLength) {
            currentChar = s.charCodeAt(++index);
            hexHigh = hexOf(currentChar);
            if (!(hexHigh >= 0)) {
                out[outIndex++] = 37; // '%'
                continue;
            } else {
                nextChar = s.charCodeAt(++index);
                hexLow = hexOf(nextChar);
                if (!(hexLow >= 0)) {
                    out[outIndex++] = 37; // '%'
                    index--;
                } else {
                    hasHex = true;
                    currentChar = hexHigh * 16 + hexLow;
                }
            }
        }
        out[outIndex++] = currentChar;
        index++;
    }
    return hasHex ? out.slice(0, outIndex) : out;
}

export function unescape(s: string, decodeSpaces?: boolean): string {
    try {
        return decodeURIComponent(s);
    } catch {
        return unescapeBuffer(s, decodeSpaces).toString();
    }
}

// QueryString.escape() replaces encodeURIComponent(): lib/internal/
// querystring.js's encodeStr over the noEscape table (`! ' ( ) * - . _ ~`,
// digits and letters stay), which is encodeURIComponent's unreserved set.
export function escape(str: string): string {
    return encodeURIComponent(str);
}

type Primitive = string | number | boolean | bigint | null | undefined;

function stringifyPrimitive(v: Primitive): string {
    if (typeof v === 'string') return v;
    if (typeof v === 'number' && Number.isFinite(v)) return '' + v;
    if (typeof v === 'bigint') return '' + v;
    if (typeof v === 'boolean') return v ? 'true' : 'false';
    return '';
}

function encodeStringified(v: Primitive, encode: (str: string) => string): string {
    if (typeof v === 'string') return (v.length ? encode(v) : '');
    if (typeof v === 'number' && Number.isFinite(v)) {
        // Values >= 1e21 automatically switch to scientific notation which
        // requires escaping due to the inclusion of a '+' in the output
        return (Math.abs(v) < 1e21 ? '' + v : encode('' + v));
    }
    if (typeof v === 'bigint') return '' + v;
    if (typeof v === 'boolean') return v ? 'true' : 'false';
    return '';
}

export function stringify(obj?: ParsedUrlQueryInput, sep?: string, eq?: string, options?: StringifyOptions): string {
    const s = sep || '&';
    const e = eq || '=';
    let encode: (str: string) => string = escape;
    let custom = false;
    if (options && typeof options.encodeURIComponent === 'function') {
        encode = options.encodeURIComponent;
        custom = true;
    }
    const convert = (v: Primitive): string => custom ? encode(stringifyPrimitive(v)) : encodeStringified(v, encode);

    if (obj !== null && obj !== undefined && typeof obj === 'object') {
        const keys = Object.keys(obj);
        const len = keys.length;
        let fields = '';
        for (let i = 0; i < len; ++i) {
            const k = keys[i];
            const v = obj[k];
            let ks = convert(k);
            ks += e;
            if (Array.isArray(v)) {
                const vlen = v.length;
                if (vlen === 0) continue;
                if (fields) fields += s;
                for (let j = 0; j < vlen; ++j) {
                    if (j) fields += s;
                    fields += ks;
                    fields += convert(v[j]);
                }
            } else {
                if (fields) fields += s;
                fields += ks;
                fields += convert(v as Primitive);
            }
        }
        return fields;
    }
    return '';
}

export const encode = stringify;

function charCodes(str: string): number[] {
    const ret: number[] = [];
    for (let i = 0; i < str.length; ++i) ret.push(str.charCodeAt(i));
    return ret;
}

function decodeStr(s: string, decoder: (str: string) => string): string {
    try {
        return decoder(s);
    } catch {
        return unescape(s, true);
    }
}

function addKeyVal(obj: ParsedUrlQuery, key: string, value: string, keyEncoded: boolean, valEncoded: boolean,
    decode: (str: string) => string): void {
    let k = key;
    let v = value;
    if (k.length > 0 && keyEncoded) k = decodeStr(k, decode);
    if (v.length > 0 && valEncoded) v = decodeStr(v, decode);
    const curValue = obj[k];
    if (curValue === undefined) {
        obj[k] = v;
    } else if (Array.isArray(curValue)) {
        curValue.push(v);
    } else {
        obj[k] = [curValue, v];
    }
}

// Parse a key/val string.
export function parse(str: string, sep?: string, eq?: string, options?: ParseOptions): ParsedUrlQuery {
    const obj: ParsedUrlQuery = Object.create(null);
    if (typeof str !== 'string' || str.length === 0) {
        return obj;
    }
    const sepCodes = (!sep ? [38] : charCodes(String(sep)));
    const eqCodes = (!eq ? [61] : charCodes(String(eq)));
    const sepLen = sepCodes.length;
    const eqLen = eqCodes.length;

    let pairs = 1000;
    if (options && typeof options.maxKeys === 'number') {
        // -1 means "unlimited pairs": pairs is decremented and checked
        // against 0.
        pairs = (options.maxKeys > 0 ? options.maxKeys : -1);
    }

    let decode: (str: string) => string = unescapeDefault;
    let customDecode = false;
    if (options && typeof options.decodeURIComponent === 'function') {
        decode = options.decodeURIComponent;
        customDecode = true;
    }

    let lastPos = 0;
    let sepIdx = 0;
    let eqIdx = 0;
    let key = '';
    let value = '';
    let keyEncoded = customDecode;
    let valEncoded = customDecode;
    const plusChar = (customDecode ? '%20' : ' ');
    let encodeCheck = 0;
    for (let i = 0; i < str.length; ++i) {
        const code = str.charCodeAt(i);

        // Try matching key/value pair separator (e.g. '&')
        if (code === sepCodes[sepIdx]) {
            if (++sepIdx === sepLen) {
                // Key/value pair separator match!
                const end = i - sepIdx + 1;
                if (eqIdx < eqLen) {
                    // We didn't find the (entire) key/value separator
                    if (lastPos < end) {
                        // Treat the substring as part of the key instead of the value
                        key += str.slice(lastPos, end);
                    } else if (key.length === 0) {
                        // We saw an empty substring between separators
                        if (--pairs === 0) return obj;
                        lastPos = i + 1;
                        sepIdx = eqIdx = 0;
                        continue;
                    }
                } else if (lastPos < end) {
                    value += str.slice(lastPos, end);
                }

                addKeyVal(obj, key, value, keyEncoded, valEncoded, decode);

                if (--pairs === 0) return obj;
                keyEncoded = valEncoded = customDecode;
                key = value = '';
                encodeCheck = 0;
                lastPos = i + 1;
                sepIdx = eqIdx = 0;
            }
        } else {
            sepIdx = 0;
            // Try matching key/value separator (e.g. '=') if we haven't already
            if (eqIdx < eqLen) {
                if (code === eqCodes[eqIdx]) {
                    if (++eqIdx === eqLen) {
                        // Key/value separator match!
                        const end = i - eqIdx + 1;
                        if (lastPos < end) key += str.slice(lastPos, end);
                        encodeCheck = 0;
                        lastPos = i + 1;
                    }
                    continue;
                } else {
                    eqIdx = 0;
                    if (!keyEncoded) {
                        // Try to match an (valid) encoded byte once to minimize
                        // unnecessary calls to string decoding functions
                        if (code === 37 /* % */) {
                            encodeCheck = 1;
                            continue;
                        } else if (encodeCheck > 0) {
                            if (isHex(code)) {
                                if (++encodeCheck === 3) keyEncoded = true;
                                continue;
                            } else {
                                encodeCheck = 0;
                            }
                        }
                    }
                }
                if (code === 43 /* + */) {
                    if (lastPos < i) key += str.slice(lastPos, i);
                    key += plusChar;
                    lastPos = i + 1;
                    continue;
                }
            }
            if (code === 43 /* + */) {
                if (lastPos < i) value += str.slice(lastPos, i);
                value += plusChar;
                lastPos = i + 1;
            } else if (!valEncoded) {
                // Try to match an (valid) encoded byte (once) to minimize
                // unnecessary calls to string decoding functions
                if (code === 37 /* % */) {
                    encodeCheck = 1;
                } else if (encodeCheck > 0) {
                    if (isHex(code)) {
                        if (++encodeCheck === 3) valEncoded = true;
                    } else {
                        encodeCheck = 0;
                    }
                }
            }
        }
    }

    // Deal with any leftover key or value data
    if (lastPos < str.length) {
        if (eqIdx < eqLen) key += str.slice(lastPos);
        else if (sepIdx < sepLen) value += str.slice(lastPos);
    } else if (eqIdx === 0 && key.length === 0) {
        // We ended on an empty substring
        return obj;
    }

    addKeyVal(obj, key, value, keyEncoded, valEncoded, decode);
    return obj;
}

function unescapeDefault(s: string): string {
    return unescape(s);
}

export const decode = parse;
