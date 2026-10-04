// kml:global
// URLPattern — a global — as Node has it (the WHATWG URL Pattern Standard,
// https://urlpattern.spec.whatwg.org/). Node matches with ada's
// C++ implementation of the standard; here the standard's algorithms are in
// TypeScript: the tokenizer, the pattern-string parser, the part list and its
// regular-expression and pattern-string generators, the constructor-string
// parser and the URLPatternInit processor. Components are canonicalized
// through the URL class, as the standard does through the URL parser, with
// ada's departures from the standard where Node shows them (a string input
// without a base, hostnames that need no host parser, pathnames read as URL
// strings). The regular expressions use the `v` flag the standard asks for.
// A program that names URLPattern without declaring it imports this module.

import { URL } from './kml_url';
import { NodeTypeError } from './internal_errors';

// ---- types ----

interface Token {
    // "invalid-char", "open", "close", "regexp", "name", "char",
    // "escaped-char", "other-modifier", "asterisk" or "end".
    type: string;
    index: number;
    value: string;
}

interface Options {
    delimiter: string;
    prefix: string;
    ignoreCase: boolean;
}

interface Part {
    // "fixed", "regexp", "segment" (segment wildcard) or "full" (full wildcard).
    type: string;
    value: string;
    // "", "?", "*" or "+".
    modifier: string;
    name: string;
    prefix: string;
    suffix: string;
}

class Component {
    patternString: string;
    regexp: RegExp;
    groupNameList: string[];
    hasRegExpGroups: boolean;
    // The group indices in the order a result's `groups` lists them: ada
    // inserts the names, in order, into a std::unordered_map, which Node
    // then enumerates.
    groupOrder: number[];
    constructor(patternString: string, regexp: RegExp, groupNameList: string[], hasRegExpGroups: boolean) {
        this.patternString = patternString;
        this.regexp = regexp;
        this.groupNameList = groupNameList;
        this.hasRegExpGroups = hasRegExpGroups;
        this.groupOrder = [];
        if (groupNameList.length > 0) {
            for (const name of __kml_native.umapOrder(groupNameList.join('\n')).split('\n')) {
                this.groupOrder.push(groupNameList.indexOf(name));
            }
        }
    }
}

// A URLPatternInit: each field is present only when it is a string.
type Init = { [key: string]: string | undefined };

const COMPONENTS: string[] = ['protocol', 'username', 'password', 'hostname', 'port', 'pathname', 'search', 'hash'];
const INIT_KEYS: string[] = ['protocol', 'username', 'password', 'hostname', 'port', 'pathname', 'search', 'hash', 'baseURL'];

const SPECIAL_SCHEMES: string[] = ['ftp', 'file', 'http', 'https', 'ws', 'wss'];

function defaultPort(scheme: string): string {
    switch (scheme) {
        case 'ftp': return '21';
        case 'http': return '80';
        case 'https': return '443';
        case 'ws': return '80';
        case 'wss': return '443';
        default: return '';
    }
}

function isSpecialScheme(scheme: string): boolean {
    return SPECIAL_SCHEMES.indexOf(scheme) >= 0;
}

// A failure inside the algorithms; the public methods turn it into the
// error Node reports.
class PatternFailure extends Error {}

function failure(): PatternFailure {
    return new PatternFailure('URLPattern failure');
}

const DEFAULT_OPTIONS: Options = { delimiter: '', prefix: '', ignoreCase: false };
const HOSTNAME_OPTIONS: Options = { delimiter: '.', prefix: '', ignoreCase: false };

// ---- code points ----

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

function substring(cps: number[], start: number, end: number): string {
    let s = '';
    for (let i = start; i < end; i++) s += String.fromCodePoint(cps[i]);
    return s;
}

let idStartRegExp: RegExp | null = null;
let idContinueRegExp: RegExp | null = null;

// The standard's "is a valid name code point": ID_Start or ID_Continue,
// plus $ and, after the first, ZWNJ and ZWJ.
function isValidNameCodePoint(c: number, first: boolean): boolean {
    if (c < 0x80) {
        if ((c >= 0x61 && c <= 0x7A) || (c >= 0x41 && c <= 0x5A) || c === 0x24 || c === 0x5F) return true;
        return !first && c >= 0x30 && c <= 0x39;
    }
    if (first) {
        if (idStartRegExp === null) idStartRegExp = new RegExp('^\\p{ID_Start}$', 'u');
        return idStartRegExp.test(String.fromCodePoint(c));
    }
    if (c === 0x200C || c === 0x200D) return true;
    if (idContinueRegExp === null) idContinueRegExp = new RegExp('^\\p{ID_Continue}$', 'u');
    return idContinueRegExp.test(String.fromCodePoint(c));
}

function isASCIIDigit(c: number): boolean {
    return c >= 0x30 && c <= 0x39;
}

// ---- tokenizer ----

function tokenize(input: number[], strict: boolean): Token[] {
    const tokens: Token[] = [];
    const length = input.length;
    let index = 0;
    let nextIndex = 0;
    let code: number = 0;

    // Adds a token whose value is input[valueStart, valueStart + valueLength)
    // and moves the tokenizer to nextPosition.
    const add = (type: string, nextPosition: number, valueStart: number, valueLength: number): void => {
        tokens.push({ type: type, index: index, value: substring(input, valueStart, valueStart + valueLength) });
        index = nextPosition;
    };
    const addDefault = (type: string, nextPosition: number, valueStart: number): void => {
        add(type, nextPosition, valueStart, nextPosition - valueStart);
    };
    const seek = (at: number): number => {
        nextIndex = at;
        nextIndex++;
        return input[at];
    };
    // The standard's "process a tokenizing error".
    const tokenizingError = (nextPosition: number, valueStart: number): void => {
        if (strict) throw failure();
        addDefault('invalid-char', nextPosition, valueStart);
    };

    while (index < length) {
        code = seek(index);
        if (code === 0x2A) {
            addDefault('asterisk', nextIndex, index);
            continue;
        }
        if (code === 0x2B || code === 0x3F) {
            addDefault('other-modifier', nextIndex, index);
            continue;
        }
        if (code === 0x5C) {
            if (index === length - 1) {
                tokenizingError(nextIndex, index);
                continue;
            }
            const escapedIndex = nextIndex;
            code = seek(nextIndex);
            addDefault('escaped-char', nextIndex, escapedIndex);
            continue;
        }
        if (code === 0x7B) {
            addDefault('open', nextIndex, index);
            continue;
        }
        if (code === 0x7D) {
            addDefault('close', nextIndex, index);
            continue;
        }
        if (code === 0x3A) {
            let namePosition = nextIndex;
            const nameStart = namePosition;
            while (namePosition < length) {
                code = seek(namePosition);
                if (!isValidNameCodePoint(code, namePosition === nameStart)) break;
                namePosition = nextIndex;
            }
            if (namePosition <= nameStart) {
                tokenizingError(nameStart, index);
                continue;
            }
            addDefault('name', namePosition, nameStart);
            continue;
        }
        if (code === 0x28) {
            let depth = 1;
            let regexpPosition = nextIndex;
            const regexpStart = regexpPosition;
            let error = false;
            while (regexpPosition < length) {
                code = seek(regexpPosition);
                if (code > 0x7F) {
                    error = true;
                    break;
                }
                if (regexpPosition === regexpStart && code === 0x3F) {
                    error = true;
                    break;
                }
                if (code === 0x5C) {
                    if (regexpPosition === length - 1) {
                        error = true;
                        break;
                    }
                    regexpPosition = nextIndex;
                    code = seek(regexpPosition);
                    if (code > 0x7F) {
                        error = true;
                        break;
                    }
                    regexpPosition = nextIndex;
                    continue;
                }
                if (code === 0x29) {
                    depth--;
                    if (depth === 0) {
                        regexpPosition = nextIndex;
                        break;
                    }
                } else if (code === 0x28) {
                    depth++;
                    if (regexpPosition === length - 1) {
                        error = true;
                        break;
                    }
                    const temporaryPosition = nextIndex;
                    code = seek(nextIndex);
                    if (code !== 0x3F) {
                        error = true;
                        break;
                    }
                    nextIndex = temporaryPosition;
                }
                regexpPosition = nextIndex;
            }
            if (depth !== 0) error = true;
            if (error) {
                tokenizingError(regexpStart, index);
                continue;
            }
            const regexpLength = regexpPosition - regexpStart - 1;
            if (regexpLength === 0) {
                tokenizingError(regexpStart, index);
                continue;
            }
            add('regexp', regexpPosition, regexpStart, regexpLength);
            continue;
        }
        addDefault('char', nextIndex, index);
    }
    tokens.push({ type: 'end', index: index, value: '' });
    return tokens;
}

// ---- pattern string -> part list ----

const REGEXP_SPECIAL = '.+*?^${}()[]|/\\';
const PATTERN_SPECIAL = '+*?:{}()\\';

function escapeRegexpString(s: string): string {
    let out = '';
    for (const ch of s) {
        if (REGEXP_SPECIAL.indexOf(ch) >= 0) out += '\\';
        out += ch;
    }
    return out;
}

function escapePatternString(s: string): string {
    let out = '';
    for (const ch of s) {
        if (PATTERN_SPECIAL.indexOf(ch) >= 0) out += '\\';
        out += ch;
    }
    return out;
}

function segmentWildcardRegexp(options: Options): string {
    return '[^' + escapeRegexpString(options.delimiter) + ']+?';
}

const FULL_WILDCARD_REGEXP = '.*';

// The segment wildcard as it goes into the generated regular expression:
// with no delimiter, [^] (any code point) is written [\\s\\S].
function segmentWildcardForMatch(options: Options): string {
    return options.delimiter === '' ? '[\\s\\S]+?' : segmentWildcardRegexp(options);
}

type Encoder = (value: string) => string;

class PatternParser {
    tokens: Token[];
    options: Options;
    encode: Encoder;
    segmentWildcard: string;
    parts: Part[] = [];
    pendingFixed = '';
    index = 0;
    nextNumericName = 0;

    constructor(input: string, options: Options, encode: Encoder) {
        this.tokens = tokenize(codePoints(input), true);
        this.options = options;
        this.encode = encode;
        this.segmentWildcard = segmentWildcardRegexp(options);
    }

    tryConsume(type: string): Token | null {
        const token = this.tokens[this.index];
        if (token.type !== type) return null;
        this.index++;
        return token;
    }

    tryConsumeModifier(): Token | null {
        const token = this.tryConsume('other-modifier');
        if (token !== null) return token;
        return this.tryConsume('asterisk');
    }

    tryConsumeRegexpOrWildcard(nameToken: Token | null): Token | null {
        let token = this.tryConsume('regexp');
        if (nameToken === null && token === null) token = this.tryConsume('asterisk');
        return token;
    }

    consumeRequired(type: string): Token {
        const token = this.tryConsume(type);
        if (token === null) throw failure();
        return token;
    }

    consumeText(): string {
        let result = '';
        for (;;) {
            let token = this.tryConsume('char');
            if (token === null) token = this.tryConsume('escaped-char');
            if (token === null) break;
            result += token.value;
        }
        return result;
    }

    maybeAddFixedPart(): void {
        if (this.pendingFixed === '') return;
        const encoded = this.encode(this.pendingFixed);
        this.pendingFixed = '';
        this.parts.push({ type: 'fixed', value: encoded, modifier: '', name: '', prefix: '', suffix: '' });
    }

    addPart(prefix: string, nameToken: Token | null, regexpOrWildcard: Token | null, suffix: string, modifierToken: Token | null): void {
        const modifier = modifierToken === null ? '' : modifierToken.value;
        if (nameToken === null && regexpOrWildcard === null && modifier === '') {
            this.pendingFixed += prefix;
            return;
        }
        this.maybeAddFixedPart();
        if (nameToken === null && regexpOrWildcard === null) {
            if (prefix === '') return;
            const encoded = this.encode(prefix);
            this.parts.push({ type: 'fixed', value: encoded, modifier: modifier, name: '', prefix: '', suffix: '' });
            return;
        }
        let regexpValue = this.segmentWildcard;
        if (regexpOrWildcard !== null) {
            regexpValue = regexpOrWildcard.type === 'asterisk' ? FULL_WILDCARD_REGEXP : regexpOrWildcard.value;
        }
        let type = 'regexp';
        let name = '';
        if (nameToken !== null) {
            name = nameToken.value;
        } else if (regexpOrWildcard !== null) {
            name = String(this.nextNumericName);
            this.nextNumericName++;
        }
        for (const part of this.parts) {
            if (part.name === name) throw failure();
        }
        if (regexpValue === this.segmentWildcard) {
            type = 'segment';
        } else if (regexpValue === FULL_WILDCARD_REGEXP) {
            type = 'full';
        }
        this.parts.push({ type: type, value: regexpValue, modifier: modifier, name: name, prefix: this.encode(prefix), suffix: this.encode(suffix) });
    }

    parse(): Part[] {
        while (this.index < this.tokens.length) {
            const charToken = this.tryConsume('char');
            const nameToken = this.tryConsume('name');
            const regexpOrWildcard = this.tryConsumeRegexpOrWildcard(nameToken);
            if (nameToken !== null || regexpOrWildcard !== null) {
                let prefix = '';
                if (charToken !== null) prefix = charToken.value;
                if (prefix !== '' && prefix !== this.options.prefix) {
                    this.pendingFixed += prefix;
                    prefix = '';
                }
                this.maybeAddFixedPart();
                const modifierToken = this.tryConsumeModifier();
                this.addPart(prefix, nameToken, regexpOrWildcard, '', modifierToken);
                continue;
            }
            let fixedToken = charToken;
            if (fixedToken === null) fixedToken = this.tryConsume('escaped-char');
            if (fixedToken !== null) {
                this.pendingFixed += fixedToken.value;
                continue;
            }
            const openToken = this.tryConsume('open');
            if (openToken !== null) {
                const prefix = this.consumeText();
                const innerName = this.tryConsume('name');
                const innerRegexp = this.tryConsumeRegexpOrWildcard(innerName);
                const suffix = this.consumeText();
                this.consumeRequired('close');
                const modifierToken = this.tryConsumeModifier();
                this.addPart(prefix, innerName, innerRegexp, suffix, modifierToken);
                continue;
            }
            this.maybeAddFixedPart();
            this.consumeRequired('end');
            break;
        }
        return this.parts;
    }
}

// ---- part list -> regular expression, pattern string ----

function generateRegexp(parts: Part[], options: Options): { source: string; names: string[] } {
    let result = '^';
    const names: string[] = [];
    for (const part of parts) {
        if (part.type === 'fixed') {
            if (part.modifier === '') {
                result += escapeRegexpString(part.value);
            } else {
                result += '(?:' + escapeRegexpString(part.value) + ')' + part.modifier;
            }
            continue;
        }
        names.push(part.name);
        let regexpValue = part.value;
        if (part.type === 'segment') {
            regexpValue = segmentWildcardForMatch(options);
        } else if (part.type === 'full') {
            regexpValue = FULL_WILDCARD_REGEXP;
        }
        if (part.prefix === '' && part.suffix === '') {
            if (part.modifier === '' || part.modifier === '?') {
                result += '(' + regexpValue + ')' + part.modifier;
            } else {
                result += '((?:' + regexpValue + ')' + part.modifier + ')';
            }
            continue;
        }
        if (part.modifier === '' || part.modifier === '?') {
            result += '(?:' + escapeRegexpString(part.prefix) + '(' + regexpValue + ')' + escapeRegexpString(part.suffix) + ')' + part.modifier;
            continue;
        }
        result += '(?:' + escapeRegexpString(part.prefix) + '((?:' + regexpValue + ')(?:' + escapeRegexpString(part.suffix) +
            escapeRegexpString(part.prefix) + '(?:' + regexpValue + '))*)' + escapeRegexpString(part.suffix) + ')';
        if (part.modifier === '*') result += '?';
    }
    result += '$';
    return { source: result, names: names };
}

function generatePatternString(parts: Part[], options: Options): string {
    let result = '';
    for (let i = 0; i < parts.length; i++) {
        const part = parts[i];
        const previous = i > 0 ? parts[i - 1] : null;
        const next = i < parts.length - 1 ? parts[i + 1] : null;
        if (part.type === 'fixed') {
            if (part.modifier === '') {
                result += escapePatternString(part.value);
            } else {
                result += '{' + escapePatternString(part.value) + '}' + part.modifier;
            }
            continue;
        }
        const customName = !isASCIIDigit(part.name.codePointAt(0)!);
        // ada groups a prefix only where the component has a prefix code point
        // of its own (the standard compares against the empty one as well).
        let needsGrouping = part.suffix !== '' || (part.prefix !== '' && options.prefix !== '' && part.prefix !== options.prefix);
        if (!needsGrouping && customName && part.type === 'segment' && part.modifier === '' &&
            next !== null && next.prefix === '' && next.suffix === '') {
            if (next.type === 'fixed') {
                needsGrouping = next.value !== '' && isValidNameCodePoint(next.value.codePointAt(0)!, false);
            } else {
                needsGrouping = isASCIIDigit(next.name.codePointAt(0)!);
            }
        }
        if (!needsGrouping && part.prefix === '' && previous !== null && previous.type === 'fixed' && previous.value !== '' &&
            options.prefix !== '' && previous.value.endsWith(options.prefix)) {
            needsGrouping = true;
        }
        if (needsGrouping) result += '{';
        result += escapePatternString(part.prefix);
        if (customName) result += ':' + part.name;
        if (part.type === 'regexp') {
            result += '(' + part.value + ')';
        } else if (part.type === 'segment' && !customName) {
            result += '(' + segmentWildcardRegexp(options) + ')';
        } else if (part.type === 'full') {
            if (!customName && (previous === null || previous.type === 'fixed' || previous.modifier !== '' || needsGrouping || part.prefix !== '')) {
                result += '*';
            } else {
                result += '(' + FULL_WILDCARD_REGEXP + ')';
            }
        }
        // A bare name followed by a suffix that continues it needs a break.
        if (part.type === 'segment' && customName && part.suffix !== '' && isValidNameCodePoint(part.suffix.codePointAt(0)!, false)) {
            result += '\\';
        }
        result += escapePatternString(part.suffix);
        if (needsGrouping) result += '}';
        result += part.modifier;
    }
    return result;
}

// The regular expression engine here takes some syntax that the `v` flag
// forbids; this rejects what V8 rejects in a group's regexp: an identity
// escape of an ordinary letter, a "(?" that opens nothing, and in a class a
// bare syntax character.
function validateGroupRegexp(source: string): void {
    const syntax = '^$\\.*+?()[]{}|/';
    const classEscapes = 'dDsSwWbfnrtvpPuxc0';
    const classPunct = '&-!#%,:;<=>@`~';
    let depth = 0;
    for (let i = 0; i < source.length; i++) {
        const ch = source[i];
        if (ch === '\\') {
            i++;
            const next = i < source.length ? source[i] : '';
            if (next === '') throw failure();
            const isLetter = (next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z');
            if (isLetter) {
                const ok = depth === 0 ? 'dDsSwWbBfnrtvckpPux'.indexOf(next) >= 0 : classEscapes.indexOf(next) >= 0 || next === 'q';
                if (!ok) throw failure();
            } else if (!(next >= '0' && next <= '9') && syntax.indexOf(next) < 0 && !(depth > 0 && classPunct.indexOf(next) >= 0)) {
                throw failure();
            }
            // \p{...}, \u{...}, \q{...} and \k<...> carry a bracketed argument.
            if ('puPqk'.indexOf(next) >= 0 && i + 1 < source.length && (source[i + 1] === '{' || source[i + 1] === '<')) {
                const close = source.indexOf(source[i + 1] === '{' ? '}' : '>', i + 1);
                if (close < 0) throw failure();
                i = close;
            }
            continue;
        }
        if (depth > 0) {
            if (ch === '[') {
                depth++;
            } else if (ch === ']') {
                depth--;
            } else if ('(){}/|'.indexOf(ch) >= 0) {
                throw failure();
            } else if (ch === '-') {
                // Only a range or a subtraction may use a bare "-".
                const before = i > 0 ? source[i - 1] : '';
                const after = i + 1 < source.length ? source[i + 1] : '';
                if (after !== '-' && before !== '-' && (before === '[' || (before === '^' && source[i - 2] === '[') || after === ']')) throw failure();
            } else if ('!#$%*+,.:;<=>?@^`~'.indexOf(ch) >= 0 && source[i + 1] === ch && !(ch === '^' && source[i - 1] === '[')) {
                throw failure();
            }
            continue;
        }
        if (ch === '[') {
            depth = 1;
        } else if (ch === ']' || ch === '}') {
            throw failure();
        } else if (ch === '{') {
            if (!/^\{[0-9]+(,[0-9]*)?\}/.test(source.slice(i))) throw failure();
            i = source.indexOf('}', i);
        } else if (ch === '(' && source[i + 1] === '?') {
            const rest = source.slice(i + 2);
            const named = rest.length > 1 && rest.charCodeAt(0) === 0x3C && rest[1] !== '=' && rest[1] !== '!';
            const flags = /^[ims]*(-[ims]*)?:/.test(rest);
            if (!(rest.startsWith(':') || rest.startsWith('=') || rest.startsWith('!') || rest.startsWith('<=') || rest.startsWith('<!') || named || flags)) {
                throw failure();
            }
        }
    }
}

function compileComponent(input: string, encode: Encoder, options: Options): Component {
    const parts = new PatternParser(input, options, encode).parse();
    const generated = generateRegexp(parts, options);
    for (const part of parts) {
        if (part.type === 'regexp') validateGroupRegexp(part.value);
    }
    let regexp: RegExp;
    try {
        regexp = new RegExp(generated.source, options.ignoreCase ? 'vi' : 'v');
    } catch (e) {
        throw failure();
    }
    let hasRegExpGroups = false;
    for (const part of parts) {
        if (part.type === 'regexp') hasRegExpGroups = true;
    }
    return new Component(generatePatternString(parts, options), regexp, generated.names, hasRegExpGroups);
}

// ---- canonicalization, through the URL class ----

function dummy(scheme: string): URL {
    return new URL(scheme + '://dummy.test');
}

function canonicalizeProtocol(value: string): string {
    if (value === '') return value;
    let url: URL;
    try {
        url = new URL(value + '://dummy.test');
    } catch (e) {
        throw failure();
    }
    const p = url.protocol;
    return p.slice(0, p.length - 1);
}

function canonicalizeUsername(value: string): string {
    if (value === '') return value;
    const url = dummy('https');
    url.username = value;
    return url.username;
}

function canonicalizePassword(value: string): string {
    if (value === '') return value;
    const url = dummy('https');
    url.password = value;
    return url.password;
}

// A hostname. ada skips the host parser for a name that is already ASCII
// lowercase and holds no percent sign or forbidden host code point, so
// "0x7f.1" stays as written; anything else goes through the host parser.
function canonicalizeHostname(value: string): string {
    if (value === '') return value;
    let plain = true;
    for (let i = 0; i < value.length; i++) {
        const c = value.charCodeAt(i);
        if (c >= 0x80 || (c >= 0x41 && c <= 0x5A) || c <= 0x20 || c === 0x7F || '#%/:<>?@[\\]^|'.indexOf(value[i]) >= 0) {
            plain = false;
            break;
        }
    }
    if (plain) return value;
    return canonicalizeDomain(value);
}

function canonicalizeDomain(value: string): string {
    // A control character is a forbidden host code point (tab and newline
    // are dropped before the host is read).
    for (let i = 0; i < value.length; i++) {
        const c = value.charCodeAt(i);
        if ((c < 0x20 && c !== 0x09 && c !== 0x0A && c !== 0x0D) || c === 0x7F) throw failure();
    }
    // The hostname setter ignores a failing value; two different starting
    // hosts tell a failure (both unchanged) from a success.
    const a = new URL('https://dummy.test');
    const b = new URL('https://dummy2.test');
    a.hostname = value;
    b.hostname = value;
    if (a.hostname === 'dummy.test' && b.hostname === 'dummy2.test') throw failure();
    return a.hostname;
}

function canonicalizeIPv6Hostname(value: string): string {
    let result = '';
    for (const ch of value) {
        const c = ch.codePointAt(0)!;
        const hex = isASCIIDigit(c) || (c >= 0x41 && c <= 0x46) || (c >= 0x61 && c <= 0x66);
        if (!hex && ch !== '[' && ch !== ']' && ch !== ':') throw failure();
        result += ch.toLowerCase();
    }
    return result;
}

// The port state with an override: the leading digits are the port.
function canonicalizePort(input: string, protocol: string | null, noLeadingZero: boolean): string {
    if (input === '') return input;
    // The URL parser drops tab and newline code points from its input.
    let value = '';
    for (let i = 0; i < input.length; i++) {
        const c = input.charCodeAt(i);
        if (c !== 0x09 && c !== 0x0A && c !== 0x0D) value += input[i];
    }
    let end = 0;
    while (end < value.length && isASCIIDigit(value.charCodeAt(end))) end++;
    if (end === 0 || (noLeadingZero && end > 1 && value.charCodeAt(0) === 0x30)) throw failure();
    const port = Number(value.slice(0, end));
    if (port > 65535) throw failure();
    const text = String(port);
    if (protocol !== null && isSpecialScheme(protocol) && defaultPort(protocol) === text) return '';
    return text;
}

// A pathname. ada reads it like the path of a URL string: tab and newline
// are dropped, trailing control characters and spaces are trimmed, and a
// "?" or "#" ends it. A relative one goes through "/-" so that the URL parser
// does not add its own leading slash.
function canonicalizePathname(value: string): string {
    if (value === '') return value;
    const leadingSlash = value.charCodeAt(0) === 0x2F;
    let text = leadingSlash ? value : '/-' + value;
    let cleaned = '';
    for (let i = 0; i < text.length; i++) {
        const c = text.charCodeAt(i);
        if (c !== 0x09 && c !== 0x0A && c !== 0x0D) cleaned += text[i];
    }
    let end = cleaned.length;
    while (end > 0 && cleaned.charCodeAt(end - 1) <= 0x20) end--;
    text = cleaned.slice(0, end);
    for (let i = 0; i < text.length; i++) {
        const c = text.charCodeAt(i);
        if (c === 0x3F || c === 0x23) {
            text = text.slice(0, i);
            break;
        }
    }
    const url = dummy('foo');
    url.pathname = text;
    const result = url.pathname;
    if (leadingSlash) return result;
    if (result.length < 2) throw failure();
    return result.slice(2);
}

// A pathname under a scheme that is not special: the path of the URL
// "scheme:" + value, so that "/a/../b" is a hierarchical path, "a b" an
// opaque one and "//a" an authority with an empty path (ada reads it so).
function canonicalizeNonSpecialPathname(value: string): string {
    if (value === '') return value;
    let url: URL;
    try {
        url = new URL('foo:' + value);
    } catch (e) {
        throw failure();
    }
    return url.pathname;
}

function canonicalizeSearch(value: string): string {
    if (value === '') return value;
    const url = dummy('foo');
    url.search = value;
    const s = url.search;
    return s === '' ? '' : s.slice(1);
}

function canonicalizeHash(value: string): string {
    if (value === '') return value;
    const url = dummy('foo');
    url.hash = value;
    const h = url.hash;
    return h === '' ? '' : h.slice(1);
}

// ---- URLPatternInit processing ----

function isAbsolutePathname(input: string, type: string): boolean {
    if (input === '') return false;
    if (input.charCodeAt(0) === 0x2F) return true;
    if (type === 'url') return false;
    if (input.length < 2) return false;
    if (input.charCodeAt(0) === 0x5C && input.charCodeAt(1) === 0x2F) return true;
    if (input.charCodeAt(0) === 0x7B && input.charCodeAt(1) === 0x2F) return true;
    return false;
}

function processBaseURLString(input: string, type: string): string {
    return type === 'pattern' ? escapePatternString(input) : input;
}

function hasOpaquePath(url: URL): boolean {
    return !url.href.slice(url.protocol.length).startsWith('/');
}

// The standard's "process a URLPatternInit". `defaults` are the starting
// values of the result (null: absent).
function processInit(init: Init, type: string, defaults: string | null): Init {
    const result: Init = {};
    if (defaults !== null) {
        for (const name of COMPONENTS) result[name] = defaults;
    }
    let baseURL: URL | null = null;
    if (init.baseURL !== undefined) {
        try {
            baseURL = new URL(init.baseURL);
        } catch (e) {
            throw failure();
        }
        const none = (...names: string[]): boolean => {
            for (const name of names) {
                if (init[name] !== undefined) return false;
            }
            return true;
        };
        if (init.protocol === undefined) {
            const p = baseURL.protocol;
            result.protocol = processBaseURLString(p.slice(0, p.length - 1), type);
        }
        if (type !== 'pattern' && none('protocol', 'hostname', 'port', 'username')) {
            result.username = processBaseURLString(baseURL.username, type);
        }
        if (type !== 'pattern' && none('protocol', 'hostname', 'port', 'username', 'password')) {
            result.password = processBaseURLString(baseURL.password, type);
        }
        if (none('protocol', 'hostname', 'port')) {
            result.hostname = processBaseURLString(baseURL.hostname, type);
        }
        if (none('protocol', 'hostname', 'port')) {
            result.port = processBaseURLString(baseURL.port, type);
        }
        if (none('protocol', 'hostname', 'port', 'pathname')) {
            result.pathname = processBaseURLString(baseURL.pathname, type);
        }
        if (none('protocol', 'hostname', 'port', 'pathname', 'search')) {
            const s = baseURL.search;
            result.search = processBaseURLString(s === '' ? '' : s.slice(1), type);
        }
        if (none('protocol', 'hostname', 'port', 'pathname', 'search', 'hash')) {
            // ada keeps the '#' of the base's fragment for a URL, not for a pattern.
            const h = baseURL.hash;
            result.hash = processBaseURLString(type === 'pattern' && h !== '' ? h.slice(1) : h, type);
        }
    }
    if (init.protocol !== undefined) {
        const value = init.protocol;
        const stripped = value.endsWith(':') ? value.slice(0, value.length - 1) : value;
        result.protocol = type === 'pattern' ? stripped : canonicalizeProtocol(stripped);
    }
    if (init.username !== undefined) {
        result.username = type === 'pattern' ? init.username : canonicalizeUsername(init.username);
    }
    if (init.password !== undefined) {
        result.password = type === 'pattern' ? init.password : canonicalizePassword(init.password);
    }
    if (init.hostname !== undefined) {
        result.hostname = type === 'pattern' ? init.hostname : canonicalizeHostname(init.hostname);
    }
    if (init.port !== undefined) {
        const protocol = result.protocol === undefined ? null : result.protocol;
        result.port = type === 'pattern' ? init.port : canonicalizePort(init.port, protocol, false);
    }
    if (init.pathname !== undefined) {
        let pathname = init.pathname;
        if (baseURL !== null && !hasOpaquePath(baseURL) && !isAbsolutePathname(pathname, type)) {
            const basePath = processBaseURLString(baseURL.pathname, type);
            const slash = basePath.lastIndexOf('/');
            if (slash >= 0) pathname = basePath.slice(0, slash + 1) + pathname;
        }
        if (type === 'pattern') {
            result.pathname = pathname;
        } else {
            const protocol = result.protocol === undefined ? '' : result.protocol;
            result.pathname = protocol === '' || isSpecialScheme(protocol) ? canonicalizePathname(pathname) : canonicalizeNonSpecialPathname(pathname);
        }
    }
    if (init.search !== undefined) {
        const value = init.search;
        const stripped = value.startsWith('?') ? value.slice(1) : value;
        result.search = type === 'pattern' ? stripped : canonicalizeSearch(stripped);
    }
    if (init.hash !== undefined) {
        const value = init.hash;
        const stripped = value.startsWith('#') ? value.slice(1) : value;
        result.hash = type === 'pattern' ? stripped : canonicalizeHash(stripped);
    }
    return result;
}

// ---- constructor string parser ----

class ConstructorStringParser {
    input: number[];
    tokens: Token[];
    result: Init = {};
    componentStart = 0;
    tokenIndex = 0;
    tokenIncrement = 1;
    groupDepth = 0;
    ipv6Depth = 0;
    protocolMatchesSpecial = false;
    state = 'init';

    constructor(input: string) {
        this.input = codePoints(input);
        this.tokens = tokenize(this.input, false);
    }

    safeToken(index: number): Token {
        if (index < this.tokens.length) return this.tokens[index];
        return this.tokens[this.tokens.length - 1];
    }

    isNonSpecialPatternChar(index: number, value: string): boolean {
        const token = this.safeToken(index);
        if (token.value !== value) return false;
        return token.type === 'char' || token.type === 'escaped-char' || token.type === 'invalid-char';
    }

    isProtocolSuffix(): boolean { return this.isNonSpecialPatternChar(this.tokenIndex, ':'); }
    isIdentityTerminator(): boolean { return this.isNonSpecialPatternChar(this.tokenIndex, '@'); }
    isPasswordPrefix(): boolean { return this.isNonSpecialPatternChar(this.tokenIndex, ':'); }
    isPortPrefix(): boolean { return this.isNonSpecialPatternChar(this.tokenIndex, ':'); }
    isPathnameStart(): boolean { return this.isNonSpecialPatternChar(this.tokenIndex, '/'); }
    isHashPrefix(): boolean { return this.isNonSpecialPatternChar(this.tokenIndex, '#'); }
    isIPv6Open(): boolean { return this.isNonSpecialPatternChar(this.tokenIndex, '['); }
    isIPv6Close(): boolean { return this.isNonSpecialPatternChar(this.tokenIndex, ']'); }

    nextIsAuthoritySlashes(): boolean {
        return this.isNonSpecialPatternChar(this.tokenIndex + 1, '/') && this.isNonSpecialPatternChar(this.tokenIndex + 2, '/');
    }

    isSearchPrefix(): boolean {
        if (this.isNonSpecialPatternChar(this.tokenIndex, '?')) return true;
        const token = this.tokens[this.tokenIndex];
        if (token.value !== '?') return false;
        const previousIndex = this.tokenIndex - 1;
        if (previousIndex < 0) return true;
        const previous = this.safeToken(previousIndex);
        return !(previous.type === 'name' || previous.type === 'regexp' || previous.type === 'close' || previous.type === 'asterisk');
    }

    makeComponentString(): string {
        const token = this.tokens[this.tokenIndex];
        const startToken = this.safeToken(this.componentStart);
        return substring(this.input, startToken.index, token.index);
    }

    rewind(): void {
        this.tokenIndex = this.componentStart;
        this.tokenIncrement = 0;
    }

    computeProtocolMatchesSpecial(): void {
        const component = compileComponent(this.makeComponentString(), canonicalizeProtocol, DEFAULT_OPTIONS);
        this.protocolMatchesSpecial = matchesSpecialScheme(component);
    }

    changeState(newState: string, skip: number): void {
        const state = this.state;
        if (state !== 'init' && state !== 'authority' && state !== 'done') {
            this.result[state] = this.makeComponentString();
        }
        if (state !== 'init' && newState !== 'done') {
            const fromAuthority = state === 'protocol' || state === 'authority' || state === 'username' || state === 'password';
            if (fromAuthority && (newState === 'port' || newState === 'pathname' || newState === 'search' || newState === 'hash') &&
                this.result.hostname === undefined) {
                this.result.hostname = '';
            }
            const fromHost = fromAuthority || state === 'hostname' || state === 'port';
            if (fromHost && (newState === 'search' || newState === 'hash') && this.result.pathname === undefined) {
                this.result.pathname = this.protocolMatchesSpecial ? '/' : '';
            }
            if ((fromHost || state === 'pathname') && newState === 'hash' && this.result.search === undefined) {
                this.result.search = '';
            }
        }
        this.state = newState;
        this.tokenIndex += skip;
        this.componentStart = this.tokenIndex;
        this.tokenIncrement = 0;
    }

    parse(): Init {
        while (this.tokenIndex < this.tokens.length) {
            this.tokenIncrement = 1;
            if (this.tokens[this.tokenIndex].type === 'end') {
                // ada leaves the last component unrecorded when the string ends
                // on a "{" that does not follow another "{".
                if (this.tokenIndex > 0 && this.tokens[this.tokenIndex - 1].type === 'open' &&
                    (this.tokenIndex < 2 || this.tokens[this.tokenIndex - 2].type !== 'open')) break;
                if (this.state === 'init') {
                    this.rewind();
                    if (this.isHashPrefix()) {
                        this.changeState('hash', 1);
                    } else if (this.isSearchPrefix()) {
                        this.changeState('search', 1);
                    } else {
                        this.changeState('pathname', 0);
                    }
                    this.tokenIndex += this.tokenIncrement;
                    continue;
                }
                if (this.state === 'authority') {
                    this.rewind();
                    this.changeState('hostname', 0);
                    this.tokenIndex += this.tokenIncrement;
                    continue;
                }
                this.changeState('done', 0);
                break;
            }
            if (this.tokens[this.tokenIndex].type === 'open') {
                this.groupDepth++;
                this.tokenIndex += this.tokenIncrement;
                continue;
            }
            if (this.groupDepth > 0) {
                if (this.tokens[this.tokenIndex].type === 'close') {
                    this.groupDepth--;
                } else {
                    this.tokenIndex += this.tokenIncrement;
                    continue;
                }
            }
            switch (this.state) {
                case 'init':
                    if (this.isProtocolSuffix()) {
                        this.rewind();
                        this.changeState('protocol', 0);
                    }
                    break;
                case 'protocol':
                    if (this.isProtocolSuffix()) {
                        this.computeProtocolMatchesSpecial();
                        let nextState = 'pathname';
                        let skip = 1;
                        if (this.nextIsAuthoritySlashes()) {
                            nextState = 'authority';
                            skip = 3;
                        } else if (this.protocolMatchesSpecial) {
                            nextState = 'authority';
                        }
                        this.changeState(nextState, skip);
                    }
                    break;
                case 'authority':
                    if (this.isIdentityTerminator()) {
                        this.rewind();
                        this.changeState('username', 0);
                    } else if (this.isPathnameStart() || this.isSearchPrefix() || this.isHashPrefix()) {
                        this.rewind();
                        this.changeState('hostname', 0);
                    }
                    break;
                case 'username':
                    if (this.isPasswordPrefix()) {
                        this.changeState('password', 1);
                    } else if (this.isIdentityTerminator()) {
                        this.changeState('hostname', 1);
                    } else if (this.isPathnameStart() || this.isSearchPrefix() || this.isHashPrefix()) {
                        this.changeState('hostname', 0);
                    }
                    break;
                case 'password':
                    if (this.isIdentityTerminator()) {
                        this.changeState('hostname', 1);
                    } else if (this.isPathnameStart() || this.isSearchPrefix() || this.isHashPrefix()) {
                        this.changeState('hostname', 0);
                    }
                    break;
                case 'hostname':
                    if (this.isIPv6Open()) {
                        this.ipv6Depth++;
                    } else if (this.isIPv6Close()) {
                        this.ipv6Depth--;
                    } else if (this.isPortPrefix() && this.ipv6Depth === 0) {
                        this.changeState('port', 1);
                    } else if (this.isPathnameStart()) {
                        this.changeState('pathname', 0);
                    } else if (this.isSearchPrefix()) {
                        this.changeState('search', 1);
                    } else if (this.isHashPrefix()) {
                        this.changeState('hash', 1);
                    }
                    break;
                case 'port':
                    if (this.isPathnameStart()) {
                        this.changeState('pathname', 0);
                    } else if (this.isSearchPrefix()) {
                        this.changeState('search', 1);
                    } else if (this.isHashPrefix()) {
                        this.changeState('hash', 1);
                    }
                    break;
                case 'pathname':
                    if (this.isSearchPrefix()) {
                        this.changeState('search', 1);
                    } else if (this.isHashPrefix()) {
                        this.changeState('hash', 1);
                    }
                    break;
                case 'search':
                    if (this.isHashPrefix()) this.changeState('hash', 1);
                    break;
                default:
                    break;
            }
            this.tokenIndex += this.tokenIncrement;
        }
        if (this.result.hostname !== undefined && this.result.port === undefined) {
            this.result.port = '';
        }
        return this.result;
    }
}

function matchesSpecialScheme(protocol: Component): boolean {
    for (const scheme of SPECIAL_SCHEMES) {
        if (protocol.regexp.exec(scheme) !== null) return true;
    }
    return false;
}

// ---- argument shapes (Node's checks) ----

function isObject(value: any): boolean {
    return (typeof value === 'object' && value !== null) || typeof value === 'function';
}

// A URLPatternInit from a dictionary: the fields that are strings, in the
// dictionary's order.
function readInit(source: any): Init {
    const init: Init = {};
    for (const key of INIT_KEYS) {
        const value = source === undefined ? undefined : source[key];
        init[key] = typeof value === 'string' ? value : undefined;
    }
    return init;
}

function failedToConstruct(): NodeTypeError {
    return new NodeTypeError('ERR_INVALID_URL_PATTERN', 'Failed to construct URLPattern');
}

function invalidArgType(message: string): NodeTypeError {
    return new NodeTypeError('ERR_INVALID_ARG_TYPE', message);
}

export class URLPattern {
    #protocol: Component;
    #username: Component;
    #password: Component;
    #hostname: Component;
    #port: Component;
    #pathname: Component;
    #search: Component;
    #hash: Component;

    // (input?, baseURL?, options?) or (input?, options?). Node tells a missing
    // argument from an undefined one only when options follows an undefined
    // baseURL; the count here is from the last argument given.
    constructor(input: URLPatternInput, baseURL: string | URL, options?: URLPatternOptions);
    constructor(input?: URLPatternInput, options?: URLPatternOptions);
    constructor(...args: any[]) {
        const argc = args.length;
        const input = args[0];
        const baseURLOrOptions = args[1];
        const options = args[2];
        let source: any = input;
        if (source === null) source = undefined;
        if (source !== undefined && typeof source !== 'string' && !isObject(source)) {
            throw invalidArgType('Input must be an object or a string');
        }
        let baseURL: string | undefined = undefined;
        let optionsObject: any = undefined;
        if (argc >= 2) {
            const second = baseURLOrOptions;
            if (typeof second === 'string') {
                baseURL = second;
            } else if (argc === 2) {
                if (isObject(second)) {
                    optionsObject = second;
                } else if (second !== undefined && second !== null) {
                    throw invalidArgType('second argument must be a string or object');
                }
            } else if (second === undefined || second === null) {
                throw failedToConstruct();
            } else {
                throw invalidArgType('second argument must be a string');
            }
        }
        if (argc >= 3) {
            if (isObject(options)) {
                optionsObject = options;
            } else if (options !== undefined && options !== null) {
                throw invalidArgType('options must be an object');
            }
        }
        const ignoreCase = optionsObject !== undefined && !!optionsObject.ignoreCase;
        try {
            let init: Init;
            if (typeof source === 'string') {
                init = new ConstructorStringParser(source).parse();
                if (baseURL === undefined && init.protocol === undefined) throw failure();
                if (baseURL !== undefined) init.baseURL = baseURL;
            } else {
                if (baseURL !== undefined) throw failure();
                init = readInit(source);
            }
            const processed = processInit(init, 'pattern', null);
            for (const name of COMPONENTS) {
                if (processed[name] === undefined) processed[name] = '*';
            }
            const protocolValue = processed.protocol!;
            if (isSpecialScheme(protocolValue) && defaultPort(protocolValue) === processed.port) {
                processed.port = '';
            }
            const compileOptions: Options = { delimiter: '', prefix: '', ignoreCase: ignoreCase };
            this.#protocol = compileComponent(processed.protocol!, canonicalizeProtocol, DEFAULT_OPTIONS);
            this.#username = compileComponent(processed.username!, canonicalizeUsername, DEFAULT_OPTIONS);
            this.#password = compileComponent(processed.password!, canonicalizePassword, DEFAULT_OPTIONS);
            const host = processed.hostname!;
            if (host.startsWith('[') || host.startsWith('{[') || host.startsWith('\\[')) {
                this.#hostname = compileComponent(host, canonicalizeIPv6Hostname, HOSTNAME_OPTIONS);
            } else {
                this.#hostname = compileComponent(host, canonicalizeHostname, HOSTNAME_OPTIONS);
            }
            this.#port = compileComponent(processed.port!, (v: string): string => canonicalizePort(v, null, true), DEFAULT_OPTIONS);
            if (matchesSpecialScheme(this.#protocol)) {
                const pathnameOptions: Options = { delimiter: '/', prefix: '/', ignoreCase: ignoreCase };
                this.#pathname = compileComponent(processed.pathname!, canonicalizePathname, pathnameOptions);
            } else {
                this.#pathname = compileComponent(processed.pathname!, canonicalizeNonSpecialPathname, compileOptions);
            }
            this.#search = compileComponent(processed.search!, canonicalizeSearch, compileOptions);
            this.#hash = compileComponent(processed.hash!, canonicalizeHash, compileOptions);
        } catch (e) {
            if (e instanceof PatternFailure || e instanceof TypeError || e instanceof SyntaxError) throw failedToConstruct();
            throw e;
        }
    }

    get protocol(): string { return this.#protocol.patternString; }
    get username(): string { return this.#username.patternString; }
    get password(): string { return this.#password.patternString; }
    get hostname(): string { return this.#hostname.patternString; }
    get port(): string { return this.#port.patternString; }
    get pathname(): string { return this.#pathname.patternString; }
    get search(): string { return this.#search.patternString; }
    get hash(): string { return this.#hash.patternString; }

    get hasRegExpGroups(): boolean {
        return this.#protocol.hasRegExpGroups || this.#username.hasRegExpGroups || this.#password.hasRegExpGroups ||
            this.#hostname.hasRegExpGroups || this.#port.hasRegExpGroups || this.#pathname.hasRegExpGroups ||
            this.#search.hasRegExpGroups || this.#hash.hasRegExpGroups;
    }

    test(input?: URLPatternInput, baseURL?: string | URL): boolean;
    test(input?: any, baseURL?: any): boolean {
        return this.#match(input, baseURL, 'test') !== null;
    }

    exec(input?: URLPatternInput, baseURL?: string | URL): URLPatternResult | null;
    exec(input?: any, baseURL?: any): any {
        return this.#match(input, baseURL, 'exec');
    }

    // The standard's "match".
    #match(input: any, baseURL: any, method: string): any {
        let source: any = input;
        if (source === null) source = undefined;
        if (source !== undefined && typeof source !== 'string' && !isObject(source)) {
            throw invalidArgType('URLPattern input needs to be a string or an object');
        }
        let base: string | undefined = undefined;
        if (baseURL !== undefined) {
            if (baseURL === null) {
                base = 'null';
            } else if (typeof baseURL === 'string') {
                base = baseURL;
            } else {
                throw invalidArgType('baseURL must be a string');
            }
        }
        const inputs: any[] = [];
        const values: Init = {};
        if (typeof source === 'string') {
            inputs.push(source);
            if (base !== undefined) inputs.push(base);
            const parsed = this.#parseString(source, base);
            if (parsed === null) return null;
            for (const name of COMPONENTS) values[name] = parsed[name];
        } else {
            if (base !== undefined) {
                throw new NodeTypeError('ERR_OPERATION_FAILED', 'Failed to ' + method + ' URLPattern');
            }
            const init: Init = readInit(source);
            inputs.push(init);
            let processed: Init;
            try {
                processed = processInit(init, 'url', '');
            } catch (e) {
                if (e instanceof PatternFailure || e instanceof TypeError) return null;
                throw e;
            }
            for (const name of COMPONENTS) values[name] = processed[name];
        }
        // Node's result object has no prototype.
        const result: any = Object.create(null);
        result.inputs = inputs;
        const components = [this.#protocol, this.#username, this.#password, this.#hostname, this.#port, this.#pathname, this.#search, this.#hash];
        for (let i = 0; i < COMPONENTS.length; i++) {
            const value = values[COMPONENTS[i]]!;
            const component = components[i];
            const m = component.regexp.exec(value);
            if (m === null) return null;
            const groups: any = {};
            for (const g of component.groupOrder) {
                groups[component.groupNameList[g]] = m[g + 1];
            }
            result[COMPONENTS[i]] = { input: value, groups: groups };
        }
        return result;
    }

    // The components of a string input. Without a base, a string the URL
    // parser rejects is read relative to an empty non-special URL, so that
    // "/x", "?q" or "//host/x" are inputs of their own.
    #parseString(text: string, base: string | undefined): Init | null {
        let url: URL;
        let relative = false;
        try {
            if (base !== undefined) {
                url = new URL(text, base);
            } else {
                try {
                    url = new URL(text);
                } catch (e) {
                    url = new URL(text, 'foo://');
                    relative = true;
                }
            }
        } catch (e) {
            return null;
        }
        const out: Init = {};
        out.protocol = relative ? '' : url.protocol.slice(0, url.protocol.length - 1);
        out.username = url.username;
        out.password = url.password;
        out.hostname = url.hostname;
        out.port = url.port;
        out.pathname = url.pathname;
        out.search = url.search === '' ? '' : url.search.slice(1);
        out.hash = url.hash === '' ? '' : url.hash.slice(1);
        return out;
    }
}
