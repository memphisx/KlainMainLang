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
