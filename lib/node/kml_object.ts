// kml:global
// Object.prototype methods written in TypeScript, reached through
// `@lower` in lib/es.d.ts.

// Object.prototype.toLocaleString: this.toString(), the receiver's own
// override included.
export function __kml_Object_toLocaleString(o: any): string { // kml:lower toLocaleString
    return o.toString();
}

// BigInt.prototype.toLocaleString: the decimal digits grouped by threes,
// as Number.prototype.toLocaleString groups an integer's.
export function __kml_BigInt_toLocaleString(b: bigint): string { // kml:lower toLocaleString
    const s = b < 0n ? (-b).toString() : b.toString();
    let out = '';
    for (let i = 0; i < s.length; i++) {
        if (i > 0 && (s.length - i) % 3 === 0) out += ',';
        out += s[i];
    }
    return b < 0n ? '-' + out : out;
}

// BigInt.prototype.valueOf: the bigint itself.
export function __kml_BigInt_valueOf(b: bigint): bigint { // kml:lower valueOf
    return b;
}
