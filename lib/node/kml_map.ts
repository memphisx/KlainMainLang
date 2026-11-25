// kml:global
// Map's statics written in TypeScript, reached through their `@lower`
// declarations in lib/es.d.ts.

// Map.groupBy (ECMA-262 §24.1.2.1): items grouped by keySelector's result,
// a key compared by SameValueZero (-0 joins 0), in first-seen order.
// (keySelector is any: a non-function argument reaches the check below.)
export function __kml_Map_groupBy<K, T>(items: Iterable<T>, keySelector: any): Map<K, T[]> { // kml:lower Map.groupBy
    if (items === null || items === undefined) {
        throw new TypeError('Map.groupBy called on null or undefined');
    }
    if (typeof keySelector !== 'function') {
        const shown = typeof keySelector !== 'object' || keySelector === null ? String(keySelector)
            : Array.isArray(keySelector) ? '[object Array]' : '#<Object>';
        throw new TypeError(shown + ' is not a function');
    }
    const groups = new Map<K, T[]>();
    let k = 0;
    for (const item of items) {
        let key: any = keySelector(item, k++);
        if (key === 0) {
            key = 0; // -0 is +0
        }
        const group = groups.get(key);
        if (group === undefined) {
            groups.set(key, [item]);
        } else {
            group.push(item);
        }
    }
    return groups;
}
