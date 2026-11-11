// kml:global
// FormData — a global — as Node has it (undici's lib/web/fetch/formdata.js):
// an ordered list of name/value entries, each value a string or a File (a
// Blob entry becomes a File named "blob", or the file name given). A
// program that names FormData without declaring it imports this module.

import { inspect } from './internal_util_inspect';

// webidl's argument-count TypeError ("FormData.append: 2 arguments
// required, but only 1 found.").
function argCount(method: string, need: number, got: number): void {
    if (got < need) {
        throw new TypeError(`FormData.${method}: ${need} argument${need === 1 ? '' : 's'} required, but only ${got} found.`);
    }
}

// An entry's value: a string as itself, a Blob as a File (renamed when a
// file name is given), as the spec's "create an entry".
function entryValue(method: string, value: any, fileName: any, named: boolean): any {
    if (!(value instanceof Blob)) {
        if (named) {
            throw new TypeError(`FormData.${method}: Expected value ("${JSON.stringify(String(value))}") to be an instance of Blob.`);
        }
        return String(value);
    }
    if (value instanceof File && !named) return value;
    const name = named ? String(fileName) : value instanceof File ? (value as File).name : 'blob';
    const opts: any = { type: (value as Blob).type };
    if (value instanceof File) opts.lastModified = (value as File).lastModified;
    return new File([value], name, opts);
}

// An iterator over a FormData's entries: keys (0), values (1) or both (2).
class FormDataIterator<T> {
    #names: string[];
    #values: any[];
    #kind: number;
    #i: number;

    constructor(names: string[], values: any[], kind: number) {
        this.#names = names;
        this.#values = values;
        this.#kind = kind;
        this.#i = 0;
    }

    next(): IteratorResult<T> {
        if (this.#i >= this.#names.length) return { value: undefined as any, done: true };
        const i = this.#i++;
        const v: any = this.#kind === 0 ? this.#names[i] : this.#kind === 1 ? this.#values[i] : [this.#names[i], this.#values[i]];
        return { value: v, done: false };
    }

    [Symbol.iterator](): IterableIterator<T> {
        return this as any;
    }

    get [Symbol.toStringTag](): string {
        return 'FormData Iterator';
    }
}

export class FormData {
    #names: string[];
    #values: any[];

    constructor(form: any = undefined) {
        if (form !== undefined) {
            throw new TypeError("FormData constructor: Argument 1 could not be converted to: undefined.");
        }
        this.#names = [];
        this.#values = [];
    }

    append(name: any, value: any, fileName: any = undefined): void {
        argCount('append', 2, arguments.length);
        this.#names.push(String(name));
        this.#values.push(entryValue('append', value, fileName, arguments.length > 2));
    }

    delete(name: any): void {
        argCount('delete', 1, arguments.length);
        const n = String(name);
        for (let i = this.#names.length - 1; i >= 0; i--) {
            if (this.#names[i] === n) {
                this.#names.splice(i, 1);
                this.#values.splice(i, 1);
            }
        }
    }

    get(name: any): any {
        argCount('get', 1, arguments.length);
        const i = this.#names.indexOf(String(name));
        return i < 0 ? null : this.#values[i];
    }

    getAll(name: any): any[] {
        argCount('getAll', 1, arguments.length);
        const n = String(name);
        const out: any[] = [];
        for (let i = 0; i < this.#names.length; i++) {
            if (this.#names[i] === n) out.push(this.#values[i]);
        }
        return out;
    }

    has(name: any): boolean {
        argCount('has', 1, arguments.length);
        return this.#names.indexOf(String(name)) >= 0;
    }

    // The first entry of the name takes the value; the rest are removed.
    set(name: any, value: any, fileName: any = undefined): void {
        argCount('set', 2, arguments.length);
        const n = String(name);
        const v = entryValue('set', value, fileName, arguments.length > 2);
        const first = this.#names.indexOf(n);
        if (first < 0) {
            this.#names.push(n);
            this.#values.push(v);
            return;
        }
        this.#values[first] = v;
        for (let i = this.#names.length - 1; i > first; i--) {
            if (this.#names[i] === n) {
                this.#names.splice(i, 1);
                this.#values.splice(i, 1);
            }
        }
    }

    // undici's iterators read the entry list by index as they go: an entry
    // appended during iteration is visited.
    entries(): IterableIterator<[string, any]> {
        return new FormDataIterator<[string, any]>(this.#names, this.#values, 2);
    }

    keys(): IterableIterator<string> {
        return new FormDataIterator<string>(this.#names, this.#values, 0);
    }

    values(): IterableIterator<any> {
        return new FormDataIterator<any>(this.#names, this.#values, 1);
    }

    [Symbol.iterator](): IterableIterator<[string, any]> {
        return this.entries();
    }

    forEach(callback: any, thisArg: any = undefined): void {
        argCount('forEach', 1, arguments.length);
        if (typeof callback !== 'function') {
            throw new TypeError("FormData.forEach: Argument 1 could not be converted to: Function.");
        }
        for (let i = 0; i < this.#names.length; i++) callback.call(thisArg, this.#values[i], this.#names[i], this);
    }

    // Entries of one name grouped, as undici's inspect shows them.
    [inspect.custom](depth: number, options: any): string {
        if (depth < 0) return '[FormData]';
        const o: any = {};
        for (let i = 0; i < this.#names.length; i++) {
            const k = this.#names[i];
            if (!Object.prototype.hasOwnProperty.call(o, k)) o[k] = this.#values[i];
            else if (Array.isArray(o[k])) o[k].push(this.#values[i]);
            else o[k] = [o[k], this.#values[i]];
        }
        return 'FormData ' + inspect(o, options);
    }

    get [Symbol.toStringTag](): string {
        return 'FormData';
    }

    // The multipart/form-data encoding a body of this FormData sends
    // (undici's extractBody): a Blob whose type carries the boundary.
    kmlBlob(): Blob {
        let rnd = '';
        for (let i = 0; i < 11; i++) rnd += String(Math.floor(Math.random() * 10));
        const boundary = `----formdata-undici-0${rnd}`;
        const prefix = `--${boundary}\r\nContent-Disposition: form-data`;
        const escape = (s: string) => s.replace(/\n/g, '%0A').replace(/\r/g, '%0D').replace(/"/g, '%22');
        const crlf = (s: string) => s.replace(/\r(?!\n)|(?<!\r)\n/g, '\r\n');
        const parts: any[] = [];
        for (let i = 0; i < this.#names.length; i++) {
            const name = this.#names[i];
            const value = this.#values[i];
            if (typeof value === 'string') {
                parts.push(`${prefix}; name="${escape(crlf(name))}"\r\n\r\n${crlf(value)}\r\n`);
            } else {
                const f = value as File;
                parts.push(`${prefix}; name="${escape(crlf(name))}"; filename="${escape(f.name)}"\r\nContent-Type: ${f.type || 'application/octet-stream'}\r\n\r\n`);
                parts.push(f);
                parts.push('\r\n');
            }
        }
        parts.push(`--${boundary}--\r\n`);
        return new Blob(parts, { type: `multipart/form-data; boundary=${boundary}` });
    }

    // A body read back (Body.formData()): multipart/form-data or
    // application/x-www-form-urlencoded, by its Content-Type.
    static kmlParse(body: ArrayBuffer, contentType: string): FormData {
        const ct = contentType.toLowerCase();
        const out = new FormData();
        if (ct.startsWith('application/x-www-form-urlencoded')) {
            for (const [k, v] of new URLSearchParams(new TextDecoder().decode(body))) out.append(k, v);
            return out;
        }
        const m = /^multipart\/form-data\s*;.*boundary=("([^"]*)"|[^;]*)/i.exec(contentType);
        if (m === null) {
            throw new TypeError('Content-Type was not one of "multipart/form-data" or "application/x-www-form-urlencoded".');
        }
        const boundary = m[2] !== undefined ? m[2] : m[1].trim();
        const bytes = new Uint8Array(body);
        const text = new TextDecoder('latin1').decode(bytes); // one char per byte: offsets are byte offsets
        const delim = `--${boundary}`;
        let pos = text.indexOf(delim);
        if (pos < 0) throw new TypeError('Failed to parse body as FormData.');
        for (;;) {
            pos += delim.length;
            if (text.startsWith('--', pos)) break; // the closing delimiter
            if (!text.startsWith('\r\n', pos)) throw new TypeError('Failed to parse body as FormData.');
            pos += 2;
            const headEnd = text.indexOf('\r\n\r\n', pos);
            if (headEnd < 0) throw new TypeError('Failed to parse body as FormData.');
            let name: string | null = null;
            let filename: string | null = null;
            let type = '';
            for (const line of text.slice(pos, headEnd).split('\r\n')) {
                const colon = line.indexOf(':');
                if (colon < 0) continue;
                const hn = line.slice(0, colon).trim().toLowerCase();
                const hv = line.slice(colon + 1).trim();
                if (hn === 'content-disposition') {
                    const nm = /;\s*name="([^"]*)"/i.exec(hv);
                    if (nm !== null) name = nm[1];
                    const fn = /;\s*filename="([^"]*)"/i.exec(hv);
                    if (fn !== null) filename = fn[1];
                } else if (hn === 'content-type') {
                    type = hv;
                }
            }
            const start = headEnd + 4;
            const next = text.indexOf(`\r\n${delim}`, start);
            if (next < 0 || name === null) throw new TypeError('Failed to parse body as FormData.');
            const data = bytes.slice(start, next);
            const utf8 = (s: string) => new TextDecoder().decode(Uint8Array.from(s, (c) => c.charCodeAt(0)));
            if (filename !== null) out.append(utf8(name), new File([data], utf8(filename), { type }));
            else out.append(utf8(name), new TextDecoder().decode(data));
            pos = next + 2;
        }
        return out;
    }
}
