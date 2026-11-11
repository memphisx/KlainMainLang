// Reflect read as a value: its functions, non-enumerable, tagged.

import { hidden, tag } from './internal_ns_object';

let reflect: any = null;

export function _kmlReflect(): any {
    if (reflect !== null) return reflect;
    const r: any = {};
    hidden(r, 'defineProperty', Reflect.defineProperty);
    hidden(r, 'deleteProperty', Reflect.deleteProperty);
    hidden(r, 'apply', Reflect.apply);
    hidden(r, 'construct', Reflect.construct);
    hidden(r, 'get', Reflect.get);
    hidden(r, 'getOwnPropertyDescriptor', Reflect.getOwnPropertyDescriptor);
    hidden(r, 'getPrototypeOf', Reflect.getPrototypeOf);
    hidden(r, 'has', Reflect.has);
    hidden(r, 'isExtensible', Reflect.isExtensible);
    hidden(r, 'ownKeys', Reflect.ownKeys);
    hidden(r, 'preventExtensions', Reflect.preventExtensions);
    hidden(r, 'set', Reflect.set);
    hidden(r, 'setPrototypeOf', Reflect.setPrototypeOf);
    tag(r, 'Reflect');
    reflect = r;
    return r;
}
