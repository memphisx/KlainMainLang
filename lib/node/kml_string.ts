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
