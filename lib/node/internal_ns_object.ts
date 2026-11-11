// The helpers the namespace objects and the global object share: a
// non-enumerable data property, and a Symbol.toStringTag.

export function hidden(o: any, key: string, value: any): void {
    Object.defineProperty(o, key, { value: value, writable: true, enumerable: false, configurable: true });
}

export function tag(o: any, name: string): void {
    Object.defineProperty(o, Symbol.toStringTag, { value: name, writable: false, enumerable: false, configurable: true });
}
