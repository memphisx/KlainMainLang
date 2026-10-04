// kml:global
// WeakRef — a global — as V8 has it: a reference to an object (or a symbol
// not in the global registry) that does not keep it alive. Under -mm=gc the
// target is held through a disappearing link, so deref() is undefined once
// it is collected; the other memory modes never free a target behind a
// WeakRef. A program that names WeakRef without declaring it imports this
// module.

import { inspect } from './internal_util_inspect';

// What may be held weakly (CanBeHeldWeakly): an object, a function, or a
// symbol not in the global registry.
function canBeHeldWeakly(t: any): boolean {
    if (typeof t === 'object') return t !== null;
    if (typeof t === 'function') return true;
    return typeof t === 'symbol' && Symbol.keyFor(t as symbol) === undefined;
}

// Not generic: TypeScript's own declaration types WeakRef<T>, and a deref()
// result reaches the caller's T through `any`.
export class WeakRef {
    #link: number;

    constructor(target: any) {
        if (!canBeHeldWeakly(target)) throw new TypeError('WeakRef: invalid target');
        this.#link = __kml_native.weakLink(target);
    }

    deref(): any {
        return __kml_native.weakDeref(this.#link);
    }

    [inspect.custom](depth: number, options: any): string {
        return 'WeakRef {}';
    }

    get [Symbol.toStringTag](): string {
        return 'WeakRef';
    }
}
