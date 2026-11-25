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

// Reflect.metadata (reflect-metadata's decorator factory): a decorator
// defining the key's value on its target, or on the target's member.
export function __kml_Reflect_metadata(metadataKey: any, metadataValue: any): any { // kml:lower Reflect.metadata
    return (target: any, propertyKey?: any): void => {
        Reflect.defineMetadata(metadataKey, metadataValue, target, propertyKey);
    };
}

// Object.getOwnPropertyDescriptors (ECMA-262 §20.1.2.9): a new object
// holding, under each of o's own keys in OwnPropertyKeys' order (strings,
// then symbols), that key's descriptor. Both listings apply ToObject: a
// primitive gives its wrapper's, undefined or null throws.
export function __kml_Object_getOwnPropertyDescriptors(o: any): any { // kml:lower Object.getOwnPropertyDescriptors
    const out: any = {};
    for (const k of Object.getOwnPropertyNames(o)) out[k] = Object.getOwnPropertyDescriptor(o, k);
    for (const s of Object.getOwnPropertySymbols(o)) out[s] = Object.getOwnPropertyDescriptor(o, s);
    return out;
}

// Function.prototype.bind's own `length` and `name` (ECMA-262 §20.2.3.2
// steps 4-9), for a program that can redefine a function's: read from the
// target when it binds (an accessor runs then, and its throw is bind's),
// defined on the bound function F. n is the count of bound arguments.
export function __kml_Function_boundProps(F: any, T: any, n: number): void { // kml:lower defineProperty.bind
    let L = 0;
    if (Object.prototype.hasOwnProperty.call(T, 'length')) {
        const len: any = T.length;
        if (typeof len === 'number') {
            if (len === Infinity) L = Infinity;
            else if (len !== -Infinity) L = Math.max(0, Math.trunc(len) - n);
        }
    }
    Object.defineProperty(F, 'length', { value: L, writable: false, enumerable: false, configurable: true });
    const name: any = T.name;
    Object.defineProperty(F, 'name', { value: 'bound ' + (typeof name === 'string' ? name : ''), writable: false, enumerable: false, configurable: true });
}
