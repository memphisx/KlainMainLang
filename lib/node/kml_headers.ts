// kml:global
// Headers — a global — as Node has it (undici's lib/web/fetch/headers.js,
// TDD-00237). The header list maps each lowercased name to the name as
// first given and the values combined with ", "; set-cookie's values are
// also kept apart, for getSetCookie and iteration. Request, Response and
// fetch are native: they hold a Headers and read or fill its list through
// #pairs, #fillRaw and #seal. A program that names Headers without declaring it
// imports this module.

import { inspect } from './internal_util_inspect';

class HeaderEntry {
    name: string;
    value: string;
    constructor(name: string, value: string) {
        this.name = name;
        this.value = value;
    }
}

// The Fetch standard's HTTP whitespace: tab, line feed, carriage return,
// space.
function isHTTPWhitespace(c: number): boolean {
    return c === 0x09 || c === 0x0A || c === 0x0D || c === 0x20;
}

// A header value with leading and trailing HTTP whitespace removed.
function normalizeValue(value: string): string {
    let start = 0;
    let end = value.length;
    while (start < end && isHTTPWhitespace(value.charCodeAt(start))) start++;
    while (end > start && isHTTPWhitespace(value.charCodeAt(end - 1))) end--;
    return start === 0 && end === value.length ? value : value.slice(start, end);
}

// RFC 9110's token characters.
function isTokenChar(c: number): boolean {
    if (c >= 0x30 && c <= 0x39 || c >= 0x41 && c <= 0x5A || c >= 0x61 && c <= 0x7A) return true;
    return c === 0x21 || c === 0x23 || c === 0x24 || c === 0x25 || c === 0x26 || c === 0x27 ||
        c === 0x2A || c === 0x2B || c === 0x2D || c === 0x2E || c === 0x5E || c === 0x5F ||
        c === 0x60 || c === 0x7C || c === 0x7E;
}

function isValidHeaderName(name: string): boolean {
    if (name.length === 0) return false;
    for (let i = 0; i < name.length; i++) {
        if (!isTokenChar(name.charCodeAt(i))) return false;
    }
    return true;
}

// A normalized value: no NUL, line feed or carriage return.
function isValidHeaderValue(value: string): boolean {
    for (let i = 0; i < value.length; i++) {
        const c = value.charCodeAt(i);
        if (c === 0x00 || c === 0x0A || c === 0x0D) return false;
    }
    return true;
}

// webidl's ByteString conversion: a code point above U+00FF is a
// TypeError, its index and value in UTF-16 code units.
function toByteString(v: any): string {
    const s = String(v);
    let index = 0;
    for (const ch of s) {
        const c = ch.codePointAt(0)!;
        if (c > 255) {
            const unit = c > 0xFFFF ? 0xD800 + ((c - 0x10000) >> 10) : c;
            throw new TypeError(`Cannot convert argument to a ByteString because the character at index ${index} has a value of ${unit} which is greater than 255.`);
        }
        index += c > 0xFFFF ? 2 : 1;
    }
    return s;
}

function argumentsRequired(prefix: string, required: number, found: number): TypeError {
    return new TypeError(`${prefix}: ${required} argument${required === 1 ? '' : 's'} required, but${found === 0 ? '' : ' only'} ${found} found.`);
}

function invalidName(prefix: string, name: string): TypeError {
    return new TypeError(`${prefix}: "${name}" is an invalid header name.`);
}

function invalidValue(prefix: string, value: string): TypeError {
    return new TypeError(`${prefix}: "${value}" is an invalid header value.`);
}

const ITER_KEYS = 0;
const ITER_VALUES = 1;
const ITER_ENTRIES = 2;

class HeadersIterator {
    #pairs: string[];
    #kind: number;
    #index = 0;

    constructor(pairs: string[], kind: number) {
        this.#pairs = pairs;
        this.#kind = kind;
    }

    next(): IteratorResult<any> {
        const index = this.#index;
        if (index >= this.#pairs.length) return { value: undefined, done: true };
        this.#index = index + 2;
        const name = this.#pairs[index];
        const value = this.#pairs[index + 1];
        if (this.#kind === ITER_KEYS) return { value: name, done: false };
        if (this.#kind === ITER_VALUES) return { value: value, done: false };
        return { value: [name, value], done: false };
    }

    [Symbol.iterator](): HeadersIterator { return this; }

    get [Symbol.toStringTag](): string { return 'Headers Iterator'; }

    [inspect.custom](depth: number, options: any): string {
        return 'Object [Headers Iterator] {}';
    }
}

export class Headers {
    #map = new Map<string, HeaderEntry>();
    #cookies: string[] = [];
    // "none", "request", "request-no-cors", "response" or "immutable".
    #guard = 'none';

    constructor(init?: any) {
        if (init === undefined) return;
        this.#fill(init, 'Headers constructor');
    }

    // The Fetch standard's fill: a sequence of [name, value] pairs, or a
    // record.
    #fill(init: any, prefix: string): void {
        if (init === null || typeof init !== 'object' && typeof init !== 'function') {
            throw new TypeError(`${prefix}: Argument 1 could not be converted to one of: sequence<sequence<ByteString>>, record<ByteString, ByteString>.`);
        }
        if (typeof init[Symbol.iterator] === 'function') {
            for (const pair of init) {
                if (pair === null || typeof pair !== 'object' || typeof pair[Symbol.iterator] !== 'function') {
                    throw new TypeError(`${prefix}: Argument 1 could not be converted to one of: sequence<sequence<ByteString>>, record<ByteString, ByteString>.`);
                }
                const items: any[] = [];
                for (const element of pair) items.push(element);
                if (items.length !== 2) {
                    throw new TypeError(`${prefix}: expected name/value pair to be length 2, found ${items.length}.`);
                }
                this.#append(toByteString(items[0]), toByteString(items[1]), prefix);
            }
            return;
        }
        for (const key of Object.keys(init)) {
            this.#append(toByteString(key), toByteString(init[key]), prefix);
        }
    }

    #append(name: string, value: string, prefix: string): void {
        const v = normalizeValue(value);
        if (!isValidHeaderName(name)) throw invalidName(prefix, name);
        if (!isValidHeaderValue(v)) throw invalidValue(prefix, v);
        if (this.#guard === 'immutable') throw new TypeError('immutable');
        const lower = name.toLowerCase();
        const entry = this.#map.get(lower);
        if (entry !== undefined) {
            entry.value = `${entry.value}, ${v}`;
        } else {
            this.#map.set(lower, new HeaderEntry(name, v));
        }
        if (lower === 'set-cookie') this.#cookies.push(v);
    }

    append(name: string, value: string): void {
        if (arguments.length < 2) throw argumentsRequired('Headers.append', 2, arguments.length);
        this.#append(toByteString(name), toByteString(value), 'Headers.append');
    }

    delete(name: string): void {
        if (arguments.length < 1) throw argumentsRequired('Headers.delete', 1, 0);
        const n = toByteString(name);
        if (!isValidHeaderName(n)) throw invalidName('Headers.delete', n);
        if (this.#guard === 'immutable') throw new TypeError('immutable');
        const lower = n.toLowerCase();
        if (lower === 'set-cookie') this.#cookies = [];
        this.#map.delete(lower);
    }

    get(name: string): string | null {
        if (arguments.length < 1) throw argumentsRequired('Headers.get', 1, 0);
        const n = toByteString(name);
        if (!isValidHeaderName(n)) throw invalidName('Headers.get', n);
        const entry = this.#map.get(n.toLowerCase());
        return entry === undefined ? null : entry.value;
    }

    has(name: string): boolean {
        if (arguments.length < 1) throw argumentsRequired('Headers.has', 1, 0);
        const n = toByteString(name);
        if (!isValidHeaderName(n)) throw invalidName('Headers.has', n);
        return this.#map.has(n.toLowerCase());
    }

    set(name: string, value: string): void {
        if (arguments.length < 2) throw argumentsRequired('Headers.set', 2, arguments.length);
        const n = toByteString(name);
        const v = normalizeValue(toByteString(value));
        if (!isValidHeaderName(n)) throw invalidName('Headers.set', n);
        if (!isValidHeaderValue(v)) throw invalidValue('Headers.set', v);
        if (this.#guard === 'immutable') throw new TypeError('immutable');
        const lower = n.toLowerCase();
        if (lower === 'set-cookie') this.#cookies = [v];
        const entry = this.#map.get(lower);
        if (entry !== undefined) {
            entry.name = n;
            entry.value = v;
        } else {
            this.#map.set(lower, new HeaderEntry(n, v));
        }
    }

    getSetCookie(): string[] {
        return this.#cookies.slice();
    }

    // The standard's "sort and combine": names sorted, values combined,
    // except set-cookie's, listed one by one. Flat: name, value, name, ….
    #sorted(): string[] {
        const names: string[] = [];
        for (const name of this.#map.keys()) names.push(name);
        names.sort();
        const out: string[] = [];
        for (const name of names) {
            if (name === 'set-cookie') {
                for (const cookie of this.#cookies) out.push(name, cookie);
            } else {
                out.push(name, this.#map.get(name)!.value);
            }
        }
        return out;
    }

    entries(): HeadersIterator { return new HeadersIterator(this.#sorted(), ITER_ENTRIES); }
    keys(): HeadersIterator { return new HeadersIterator(this.#sorted(), ITER_KEYS); }
    values(): HeadersIterator { return new HeadersIterator(this.#sorted(), ITER_VALUES); }
    [Symbol.iterator](): HeadersIterator { return new HeadersIterator(this.#sorted(), ITER_ENTRIES); }

    forEach(callback: (value: string, key: string, parent: Headers) => void, thisArg?: any): void {
        if (typeof callback !== 'function') {
            throw new TypeError("Headers.forEach: Argument 1 could not be converted to: Function.");
        }
        const pairs = this.#sorted();
        for (let i = 0; i < pairs.length; i += 2) {
            callback.call(thisArg, pairs[i + 1], pairs[i], this);
        }
    }

    get [Symbol.toStringTag](): string { return 'Headers'; }

    [inspect.custom](depth: number, options: any): string {
        const record: any = {};
        for (const entry of this.#map.values()) record[entry.name] = entry.value;
        return `Headers ${inspect(record, options)}`;
    }

    // The list as the native side exchanges it: the names lowercased, in
    // order, each set-cookie value its own pair.
    #pairs(): string[] {
        const out: string[] = [];
        for (const [lower, entry] of this.#map) {
            if (lower === 'set-cookie') {
                for (const cookie of this.#cookies) out.push(lower, cookie);
            } else {
                out.push(lower, entry.value);
            }
        }
        return out;
    }

    // Appends a fetched response's headers from the raw header text the
    // native side captured: only the last response's block counts, the
    // earlier ones being redirects' (or 1xx responses').
    #fillRaw(raw: string): void {
        for (const line of raw.split('\n')) {
            const text = line.endsWith('\r') ? line.slice(0, -1) : line;
            if (text.startsWith('HTTP/')) {
                this.#map = new Map<string, HeaderEntry>();
                this.#cookies = [];
                continue;
            }
            const colon = text.indexOf(':');
            if (colon <= 0) continue;
            this.#append(text.slice(0, colon), text.slice(colon + 1), 'Headers.append');
        }
    }

    // Sets the guard once the native side has filled the list.
    #seal(guard: string): void {
        this.#guard = guard;
    }
}
