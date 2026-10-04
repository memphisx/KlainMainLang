// kml:global
// The typed arrays' statics written in TypeScript (ECMA-262 §23.2.2),
// reached through their `@lower` declarations in lib/es.d.ts.

// TypedArray.from's list: the source's values (an iterable's, else an
// array-like's), each through mapfn when there is one.
function typedValues(source: any, mapfn: any, thisArg: any): any[] {
    if (mapfn !== undefined && typeof mapfn !== 'function') {
        throw new TypeError(String(mapfn) + ' is not a function');
    }
    if (source === null || source === undefined) {
        throw new TypeError('Cannot convert undefined or null to object');
    }
    const values: any[] = Array.from(source);
    if (mapfn === undefined) return values;
    const f: any = mapfn;
    const out: any[] = [];
    for (let k = 0; k < values.length; k++) out.push(f.call(thisArg, values[k], k));
    return out;
}

export function __kml_Int8Array_from(source: any, mapfn?: any, thisArg?: any): Int8Array { // kml:lower Int8Array.from
    const values = typedValues(source, mapfn, thisArg);
    const target = new Int8Array(values.length);
    for (let i = 0; i < values.length; i++) target[i] = Number(values[i]);
    return target;
}

export function __kml_Int8Array_of(...items: any[]): Int8Array { // kml:lower Int8Array.of
    const target = new Int8Array(items.length);
    for (let i = 0; i < items.length; i++) target[i] = Number(items[i]);
    return target;
}

export function __kml_Uint8Array_from(source: any, mapfn?: any, thisArg?: any): Uint8Array { // kml:lower Uint8Array.from
    const values = typedValues(source, mapfn, thisArg);
    const target = new Uint8Array(values.length);
    for (let i = 0; i < values.length; i++) target[i] = Number(values[i]);
    return target;
}

export function __kml_Uint8Array_of(...items: any[]): Uint8Array { // kml:lower Uint8Array.of
    const target = new Uint8Array(items.length);
    for (let i = 0; i < items.length; i++) target[i] = Number(items[i]);
    return target;
}

export function __kml_Uint8ClampedArray_from(source: any, mapfn?: any, thisArg?: any): Uint8ClampedArray { // kml:lower Uint8ClampedArray.from
    const values = typedValues(source, mapfn, thisArg);
    const target = new Uint8ClampedArray(values.length);
    for (let i = 0; i < values.length; i++) target[i] = Number(values[i]);
    return target;
}

export function __kml_Uint8ClampedArray_of(...items: any[]): Uint8ClampedArray { // kml:lower Uint8ClampedArray.of
    const target = new Uint8ClampedArray(items.length);
    for (let i = 0; i < items.length; i++) target[i] = Number(items[i]);
    return target;
}

export function __kml_Int16Array_from(source: any, mapfn?: any, thisArg?: any): Int16Array { // kml:lower Int16Array.from
    const values = typedValues(source, mapfn, thisArg);
    const target = new Int16Array(values.length);
    for (let i = 0; i < values.length; i++) target[i] = Number(values[i]);
    return target;
}

export function __kml_Int16Array_of(...items: any[]): Int16Array { // kml:lower Int16Array.of
    const target = new Int16Array(items.length);
    for (let i = 0; i < items.length; i++) target[i] = Number(items[i]);
    return target;
}

export function __kml_Uint16Array_from(source: any, mapfn?: any, thisArg?: any): Uint16Array { // kml:lower Uint16Array.from
    const values = typedValues(source, mapfn, thisArg);
    const target = new Uint16Array(values.length);
    for (let i = 0; i < values.length; i++) target[i] = Number(values[i]);
    return target;
}

export function __kml_Uint16Array_of(...items: any[]): Uint16Array { // kml:lower Uint16Array.of
    const target = new Uint16Array(items.length);
    for (let i = 0; i < items.length; i++) target[i] = Number(items[i]);
    return target;
}

export function __kml_Int32Array_from(source: any, mapfn?: any, thisArg?: any): Int32Array { // kml:lower Int32Array.from
    const values = typedValues(source, mapfn, thisArg);
    const target = new Int32Array(values.length);
    for (let i = 0; i < values.length; i++) target[i] = Number(values[i]);
    return target;
}

export function __kml_Int32Array_of(...items: any[]): Int32Array { // kml:lower Int32Array.of
    const target = new Int32Array(items.length);
    for (let i = 0; i < items.length; i++) target[i] = Number(items[i]);
    return target;
}

export function __kml_Uint32Array_from(source: any, mapfn?: any, thisArg?: any): Uint32Array { // kml:lower Uint32Array.from
    const values = typedValues(source, mapfn, thisArg);
    const target = new Uint32Array(values.length);
    for (let i = 0; i < values.length; i++) target[i] = Number(values[i]);
    return target;
}

export function __kml_Uint32Array_of(...items: any[]): Uint32Array { // kml:lower Uint32Array.of
    const target = new Uint32Array(items.length);
    for (let i = 0; i < items.length; i++) target[i] = Number(items[i]);
    return target;
}

export function __kml_Float32Array_from(source: any, mapfn?: any, thisArg?: any): Float32Array { // kml:lower Float32Array.from
    const values = typedValues(source, mapfn, thisArg);
    const target = new Float32Array(values.length);
    for (let i = 0; i < values.length; i++) target[i] = Number(values[i]);
    return target;
}

export function __kml_Float32Array_of(...items: any[]): Float32Array { // kml:lower Float32Array.of
    const target = new Float32Array(items.length);
    for (let i = 0; i < items.length; i++) target[i] = Number(items[i]);
    return target;
}

export function __kml_Float64Array_from(source: any, mapfn?: any, thisArg?: any): Float64Array { // kml:lower Float64Array.from
    const values = typedValues(source, mapfn, thisArg);
    const target = new Float64Array(values.length);
    for (let i = 0; i < values.length; i++) target[i] = Number(values[i]);
    return target;
}

export function __kml_Float64Array_of(...items: any[]): Float64Array { // kml:lower Float64Array.of
    const target = new Float64Array(items.length);
    for (let i = 0; i < items.length; i++) target[i] = Number(items[i]);
    return target;
}
