// Node's `util/types` (lib/internal/util/types.js): the type predicates
// `util.types` carries. This compiler has no boxed primitives
// (`new Number(1)`), external values, proxies or module namespace objects
// held as values, so their predicates are false.

function tag(value: any): string {
    return Object.prototype.toString.call(value);
}

function isTypedArrayOf(value: any, name: string): boolean {
    return ArrayBuffer.isView(value) && !(value instanceof DataView) && __kml_native.protoKey(value) === name;
}

export function isAnyArrayBuffer(value: unknown): boolean {
    return value instanceof ArrayBuffer || value instanceof SharedArrayBuffer;
}

export function isArrayBuffer(value: unknown): boolean {
    return value instanceof ArrayBuffer;
}

export function isArrayBufferView(value: unknown): boolean {
    return ArrayBuffer.isView(value);
}

export function isArgumentsObject(value: unknown): boolean {
    return tag(value) === '[object Arguments]';
}

export function isAsyncFunction(value: unknown): boolean {
    return tag(value) === '[object AsyncFunction]';
}

export function isBigInt64Array(value: unknown): boolean {
    return isTypedArrayOf(value, 'BigInt64Array');
}

export function isBigUint64Array(value: unknown): boolean {
    return isTypedArrayOf(value, 'BigUint64Array');
}

export function isBooleanObject(value: unknown): boolean {
    return false;
}

export function isBoxedPrimitive(value: unknown): boolean {
    return false;
}

export function isDataView(value: unknown): boolean {
    return value instanceof DataView;
}

export function isDate(value: unknown): boolean {
    return value instanceof Date;
}

export function isExternal(value: unknown): boolean {
    return false;
}

export function isFloat16Array(value: unknown): boolean {
    return false;
}

export function isFloat32Array(value: unknown): boolean {
    return isTypedArrayOf(value, 'Float32Array');
}

export function isFloat64Array(value: unknown): boolean {
    return isTypedArrayOf(value, 'Float64Array');
}

export function isGeneratorFunction(value: unknown): boolean {
    return tag(value) === '[object GeneratorFunction]';
}

export function isGeneratorObject(value: unknown): boolean {
    return tag(value) === '[object Generator]';
}

export function isInt8Array(value: unknown): boolean {
    return isTypedArrayOf(value, 'Int8Array');
}

export function isInt16Array(value: unknown): boolean {
    return isTypedArrayOf(value, 'Int16Array');
}

export function isInt32Array(value: unknown): boolean {
    return isTypedArrayOf(value, 'Int32Array');
}

export function isMap(value: unknown): boolean {
    return value instanceof Map;
}

export function isMapIterator(value: unknown): boolean {
    return tag(value) === '[object Map Iterator]';
}

export function isModuleNamespaceObject(value: unknown): boolean {
    return false;
}

export function isNativeError(value: unknown): boolean {
    return value instanceof Error;
}

export function isNumberObject(value: unknown): boolean {
    return false;
}

export function isPromise(value: unknown): boolean {
    return value instanceof Promise;
}

export function isProxy(value: unknown): boolean {
    return false;
}

export function isRegExp(value: unknown): boolean {
    return value instanceof RegExp;
}

export function isSet(value: unknown): boolean {
    return value instanceof Set;
}

export function isSetIterator(value: unknown): boolean {
    return tag(value) === '[object Set Iterator]';
}

export function isSharedArrayBuffer(value: unknown): boolean {
    return value instanceof SharedArrayBuffer;
}

export function isStringObject(value: unknown): boolean {
    return false;
}

export function isSymbolObject(value: unknown): boolean {
    return false;
}

export function isTypedArray(value: unknown): boolean {
    return ArrayBuffer.isView(value) && !(value instanceof DataView);
}

export function isUint8Array(value: unknown): boolean {
    return isTypedArrayOf(value, 'Uint8Array') || isTypedArrayOf(value, 'Buffer');
}

export function isUint8ClampedArray(value: unknown): boolean {
    return isTypedArrayOf(value, 'Uint8ClampedArray');
}

export function isUint16Array(value: unknown): boolean {
    return isTypedArrayOf(value, 'Uint16Array');
}

export function isUint32Array(value: unknown): boolean {
    return isTypedArrayOf(value, 'Uint32Array');
}

export function isWeakMap(value: unknown): boolean {
    return value instanceof WeakMap;
}

export function isWeakSet(value: unknown): boolean {
    return value instanceof WeakSet;
}

export function isKeyObject(value: unknown): boolean {
    return false;
}

export function isCryptoKey(value: unknown): boolean {
    return false;
}
