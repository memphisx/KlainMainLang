// JSON read as a value: its functions, non-enumerable, tagged.

import { hidden, tag } from './internal_ns_object';

let json: any = null;

export function _kmlJSON(): any {
    if (json !== null) return json;
    const j: any = {};
    hidden(j, 'parse', JSON.parse);
    hidden(j, 'stringify', JSON.stringify);
    tag(j, 'JSON');
    json = j;
    return j;
}
