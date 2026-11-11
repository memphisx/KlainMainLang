// console read as a value (`use(console)`): its methods, enumerable, and
// tagged, as Node's are.

import { inspect } from './internal_util_inspect';
import { tag } from './internal_ns_object';

let con: any = null;

export function _kmlConsole(): any {
    if (con !== null) return con;
    const c: any = {};
    c.log = console.log;
    c.info = console.info;
    c.debug = console.debug;
    c.warn = console.warn;
    c.error = console.error;
    c.dir = function dir(obj: any, options?: any): void {
        console.log(inspect(obj, { customInspect: false, ...options }));
    };
    c.time = console.time;
    c.timeEnd = console.timeEnd;
    c.trace = console.trace;
    c.assert = console.assert;
    c.count = console.count;
    c.countReset = console.countReset;
    c.group = console.group;
    c.groupEnd = console.groupEnd;
    c.table = console.table;
    c.groupCollapsed = console.groupCollapsed;
    tag(c, 'console');
    con = c;
    return c;
}
