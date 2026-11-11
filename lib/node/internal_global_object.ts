// The global object (`globalThis` as a value, or a property of it the
// program adds): one object, made on first use.

import { hidden } from './internal_ns_object';

let glob: any = null;

// The global object: Node's enumerable globals, the builtins as its other
// (non-enumerable) properties, and whatever the program sets on it. A
// module's top-level bindings are not on it, as in an ES module.
// A global a global module implements (`performance`, `URL`), put on the
// global object the first time the program reads it as a value — the
// modules are only in the program that names them. Once per name: a
// property the program then deletes stays deleted.
const globLinked: Set<string> = new Set<string>();
export function _kmlGlobalThisLink(name: string, value: any, enumerable: boolean): void {
    if (globLinked.has(name)) return;
    globLinked.add(name);
    Object.defineProperty(_kmlGlobalThis(), name, { value: value, writable: true, enumerable: enumerable, configurable: true });
}

export function _kmlGlobalThis(): any {
    if (glob !== null) return glob;
    const g: any = {};
    glob = g;
    g.global = g;
    hidden(g, 'globalThis', g);
    hidden(g, 'NaN', NaN);
    hidden(g, 'Infinity', Infinity);
    hidden(g, 'undefined', undefined);
    Object.defineProperty(g, Symbol.toStringTag, { value: 'global', writable: false, enumerable: false, configurable: true });
    return g;
}
