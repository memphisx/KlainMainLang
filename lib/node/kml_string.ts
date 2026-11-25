// kml:global
// String's statics written in TypeScript, reached through their `@lower`
// declarations in lib/es.d.ts.

// String.raw (ECMA-262 §22.1.2.4): the template's raw strings interleaved
// with the substitutions, each converted with ToString. `raw` may be any
// array-like.
export function __kml_String_raw(template: any, ...substitutions: any[]): string { // kml:lower String.raw
    const cooked = template;
    if (cooked === null || cooked === undefined) {
        throw new TypeError('Cannot convert undefined or null to object');
    }
    const literals: any = cooked.raw;
    if (literals === null || literals === undefined) {
        throw new TypeError('Cannot convert undefined or null to object');
    }
    let len = Number(literals.length);
    len = len > 0 ? Math.floor(len) : 0;
    if (len <= 0) return '';
    let r = '';
    for (let i = 0; ; i++) {
        r += String(literals[i]);
        if (i + 1 === len) return r;
        if (i < substitutions.length) r += String(substitutions[i]);
    }
}

// escape (ECMA-262 §B.2.1.1): each code unit outside A–Z a–z 0–9 @*_+-./ as
// `%XX` below 256, else `%uXXXX`, in upper-case hexadecimal.
export function __kml_escape(value?: any): string { // kml:lower escape
    const s = String(value);
    const keep = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789@*_+-./';
    let out = '';
    for (let i = 0; i < s.length; i++) {
        const c = s.charCodeAt(i);
        if (keep.indexOf(s.charAt(i)) >= 0) {
            out += s.charAt(i);
        } else if (c < 256) {
            out += '%' + (c < 16 ? '0' : '') + c.toString(16).toUpperCase();
        } else {
            out += '%u' + ('000' + c.toString(16).toUpperCase()).slice(-4);
        }
    }
    return out;
}

// unescape (ECMA-262 §B.2.1.2): `%uXXXX` and `%XX` back to their code
// units; any other `%` stays.
export function __kml_unescape(value?: any): string { // kml:lower unescape
    const s = String(value);
    const hex = (from: number, n: number): number => {
        if (from + n > s.length) return -1;
        let v = 0;
        for (let k = from; k < from + n; k++) {
            const d = '0123456789abcdef'.indexOf(s.charAt(k).toLowerCase());
            if (d < 0) return -1;
            v = v * 16 + d;
        }
        return v;
    };
    let out = '';
    for (let i = 0; i < s.length; i++) {
        if (s.charAt(i) === '%') {
            if (s.charAt(i + 1) === 'u') {
                const u = hex(i + 2, 4);
                if (u >= 0) {
                    out += String.fromCharCode(u);
                    i += 5;
                    continue;
                }
            }
            const b = hex(i + 1, 2);
            if (b >= 0) {
                out += String.fromCharCode(b);
                i += 2;
                continue;
            }
        }
        out += s.charAt(i);
    }
    return out;
}

// toLocaleUpperCase / toLocaleLowerCase (ECMA-402 §19.1.2-3): the first
// requested locale's language picks the language-sensitive mappings ICU
// applies (Turkish and Azeri dotted/dotless i, Lithuanian dot above); any
// other language maps as toUpperCase/toLowerCase do.
export function __kml_String_toLocaleUpperCase(s: string, locales?: any): string { // kml:lower toLocaleUpperCase
    return __kml_native.localeCase(s, caseLanguage(locales), 1);
}

export function __kml_String_toLocaleLowerCase(s: string, locales?: any): string { // kml:lower toLocaleLowerCase
    return __kml_native.localeCase(s, caseLanguage(locales), 0);
}

// CanonicalizeLocaleList's first entry, as a case-mapping language: 1 tr/az,
// 2 lt, else 0. A malformed tag is a RangeError, a list element that is no
// string or object a TypeError.
function caseLanguage(locales: any): number {
    if (locales === undefined) {
        return 0;
    }
    let first: any = undefined;
    if (typeof locales === 'string') {
        first = locales;
    } else {
        if (locales === null) {
            throw new TypeError('Cannot convert undefined or null to object');
        }
        const o: any = Object(locales);
        const n = Math.min(Math.max(Math.trunc(Number(o.length)) || 0, 0), Number.MAX_SAFE_INTEGER);
        for (let k = 0; k < n; k++) {
            if (!(k in o)) {
                continue;
            }
            // (only the first entry is read, as V8 does)
            const v: any = o[k];
            if (typeof v !== 'string' && (typeof v !== 'object' || v === null)) {
                throw new TypeError('Language ID should be string or object.');
            }
            first = String(v);
            break;
        }
        if (first === undefined) {
            return 0;
        }
    }
    const tag: string = first;
    if (!wellFormedTag(tag)) {
        if (tag.length > 1 && tag.endsWith('-') && wellFormedTag(tag.slice(0, -1))) {
            throw new RangeError('Invalid language tag: ' + tag.toLowerCase());
        }
        throw new RangeError('Incorrect locale information provided');
    }
    const lang = tag.split('-')[0].toLowerCase();
    return lang === 'tr' || lang === 'az' ? 1 : lang === 'lt' ? 2 : 0;
}

// A BCP 47 language tag's shape: a 2-3 or 5-8 letter language, then
// subtags of 1-8 letters or digits.
function wellFormedTag(tag: string): boolean {
    const parts = tag.split('-');
    const lang = parts[0];
    if (!/^[A-Za-z]{2,3}$|^[A-Za-z]{5,8}$/.test(lang)) {
        return false;
    }
    for (let k = 1; k < parts.length; k++) {
        if (!/^[A-Za-z0-9]{1,8}$/.test(parts[k])) {
            return false;
        }
    }
    return true;
}
