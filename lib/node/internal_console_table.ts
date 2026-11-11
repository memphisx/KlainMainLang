// console.table: Node's lib/internal/console/constructor.js table() and
// lib/internal/cli_table.js. The columns are the union of the rows' own keys
// (or `properties`), in first-seen order, then a "Values" column for the
// primitive rows; Maps and Sets (and their iterators) tabulate their entries
// under an "(iteration index)" column.

import { inspect } from './internal_util_inspect';
import { getStringWidth } from './internal_readline_utils';

const kIndex = '(index)';
const kIteration = '(iteration index)';
const kKey = 'Key';
const kValues = 'Values';

function renderRow(row: string[], widths: number[]): string {
    let out = '│ ';
    for (let i = 0; i < row.length; i++) {
        const cell = row[i];
        out += cell + ' '.repeat(widths[i] - getStringWidth(cell));
        if (i !== row.length - 1) out += ' │ ';
    }
    return out + ' │';
}

function cliTable(head: string[], columns: string[][]): string {
    const rows: string[][] = [];
    const widths: number[] = head.map((h) => getStringWidth(h));
    let longest = 0;
    for (const c of columns) if (c.length > longest) longest = c.length;
    for (let i = 0; i < head.length; i++) {
        const column = columns[i];
        for (let j = 0; j < longest; j++) {
            if (rows[j] === undefined) rows[j] = [];
            const v = j < column.length && column[j] !== undefined ? column[j] : '';
            rows[j][i] = v;
            const w = getStringWidth(v);
            if (w > widths[i]) widths[i] = w;
        }
    }
    const divider = widths.map((w) => '─'.repeat(w + 2));
    let result = '┌' + divider.join('┬') + '┐\n' + renderRow(head, widths) + '\n' + '├' + divider.join('┼') + '┤\n';
    for (const row of rows) result += renderRow(row, widths) + '\n';
    return result + '└' + divider.join('┴') + '┘';
}

function cell(v: any): string {
    const depth = v !== null && typeof v === 'object' && !Array.isArray(v) && Object.keys(v).length > 2 ? -1 : 0;
    return inspect(v, { depth, maxArrayLength: 3, breakLength: Infinity, colors: false });
}

function isArrayIndex(key: string): boolean {
    const n = Number(key);
    return key !== '' && n >= 0 && n < 4294967295 && Math.floor(n) === n && String(n) === key;
}

function indexArray(n: number): string[] {
    const out: string[] = [];
    for (let i = 0; i < n; i++) out.push(cell(i));
    return out;
}

export function table(data: any, properties?: any, log: (s: any) => void = (s: any) => console.log(s)): void {
    if (properties !== undefined && !Array.isArray(properties)) {
        const p: any = properties;
        const shown = typeof p === 'string' ? "'" + (p as string) + "'" : String(p);
        const got = p === null ? 'Received null' : typeof p === 'function' ? 'Received function ' + (p as Function).name :
            typeof p === 'object' ? 'Received an instance of ' + String(p.constructor?.name ?? 'Object') : 'Received type ' + typeof p + ' (' + shown + ')';
        const e: any = new TypeError('The "properties" argument must be an instance of Array. ' + got);
        e.code = 'ERR_INVALID_ARG_TYPE';
        throw e;
    }
    if (data === null || typeof data !== 'object') {
        log(data);
        return;
    }
    if (data instanceof Map) {
        const keys: string[] = [];
        const values: string[] = [];
        let n = 0;
        data.forEach((v: any, k: any) => {
            keys.push(cell(k));
            values.push(cell(v));
            n++;
        });
        log(cliTable([kIteration, kKey, kValues], [indexArray(n), keys, values]));
        return;
    }
    if (data instanceof Set) {
        const values: string[] = [];
        data.forEach((v: any) => {
            values.push(cell(v));
        });
        log(cliTable([kIteration, kValues], [indexArray(values.length), values]));
        return;
    }
    const cols = new Map<string, string[]>();
    let hasPrimitives = false;
    const valuesCol: string[] = [];
    const indexKeys = Object.keys(data);
    for (let i = 0; i < indexKeys.length; i++) {
        const item = data[indexKeys[i]];
        const primitive = item === null || (typeof item !== 'function' && typeof item !== 'object');
        if (properties === undefined && primitive) {
            hasPrimitives = true;
            valuesCol[i] = cell(item);
        } else {
            const keys: readonly string[] = properties !== undefined ? properties : Object.keys(item);
            for (const key of keys) {
                let col = cols.get(key);
                if (col === undefined) {
                    col = [];
                    cols.set(key, col);
                }
                col[i] = (primitive && properties !== undefined) || !Object.prototype.hasOwnProperty.call(item, key) ? '' : cell(item[key]);
            }
        }
    }
    const head: string[] = [kIndex];
    const columns: string[][] = [indexKeys];
    // Node gathers the columns in a plain object, so array-index keys come
    // first, ascending, then the rest in insertion order.
    const indices: string[] = [];
    const named: string[] = [];
    cols.forEach((_col, key) => {
        if (isArrayIndex(key)) indices.push(key);
        else named.push(key);
    });
    indices.sort((a, b) => Number(a) - Number(b));
    for (const key of indices.concat(named)) {
        head.push(key);
        columns.push(cols.get(key)!);
    }
    if (hasPrimitives) {
        head.push(kValues);
        columns.push(valuesCol);
    }
    log(cliTable(head, columns));
}

// tableIterPreview is console.table of a Map or Set iterator: the entries
// it has yet to yield, not consumed (V8's previewEntries), under an
// "(iteration index)" column. mode 1: a Map's [key, value] pairs, as Key
// and Values; 2: a Set's entries, each pair's two halves its own row, as
// Node lists them; 0: the values.
export function tableIterPreview(entries: any[], mode: number, log: (s: any) => void = (s: any) => console.log(s)): void {
    if (mode === 1) {
        const keys: string[] = [];
        const values: string[] = [];
        for (const e of entries) {
            keys.push(cell(e[0]));
            values.push(cell(e[1]));
        }
        log(cliTable([kIteration, kKey, kValues], [indexArray(keys.length), keys, values]));
        return;
    }
    const values: string[] = [];
    for (const e of entries) {
        if (mode === 2) {
            values.push(cell(e[0]));
            values.push(cell(e[1]));
        } else {
            values.push(cell(e));
        }
    }
    log(cliTable([kIteration, kValues], [indexArray(values.length), values]));
}
