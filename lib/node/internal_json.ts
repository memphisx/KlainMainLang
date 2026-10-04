// JSON.stringify with a replacer function or property list, or a space only
// known at run time: ECMA-262's SerializeJSONProperty over the run-time
// value, with V8's messages for a cycle and a BigInt. A call whose replacer
// is absent or null and whose space is a literal is compiled directly.
// JSON.parse with a reviver: ECMA-262's InternalizeJSONProperty over the
// parsed tree.

function quote(s: string): string {
    return JSON.stringify(s);
}

// V8's name for an object in a circular-structure message: its
// constructor's, quoted.
function ctorName(value: any): string {
    const c: any = value.constructor;
    const name: any = typeof c === 'function' ? c.name : undefined;
    return "'" + (typeof name === 'string' && name !== '' ? name : 'Object') + "'";
}

function keyText(holder: any, key: string): string {
    if (Array.isArray(holder)) return 'index ' + key;
    return "property '" + key + "'";
}

// V8's JsonStringifier::ConstructCircularStructureErrorMessage: the first two
// links of the cycle after its start, `...` for the elided middle, the last.
function circularError(stack: any[], keys: string[], start: number, key: string, holder: any): TypeError {
    let msg = 'Converting circular structure to JSON\n    --> starting at object with constructor ' + ctorName(stack[start]);
    let i = start + 1;
    const prefixEnd = Math.min(stack.length, i + 2);
    for (; i < prefixEnd; i++) {
        msg += '\n    |     ' + keyText(stack[i - 1], keys[i]) + ' -> object with constructor ' + ctorName(stack[i]);
    }
    if (stack.length > i + 1) msg += '\n    |     ...';
    i = Math.max(i, stack.length - 1);
    for (; i < stack.length; i++) {
        msg += '\n    |     ' + keyText(stack[i - 1], keys[i]) + ' -> object with constructor ' + ctorName(stack[i]);
    }
    msg += '\n    --- ' + keyText(holder, key) + ' closes the circle';
    return new TypeError(msg);
}

class Serializer {
    replacer: any = undefined;
    props: string[] | undefined = undefined;
    gap: string = '';
    indent: string = '';
    stack: any[] = [];
    keys: string[] = [];

    property(holder: any, key: string, value: any): string | undefined {
        if (value instanceof Date) {
            value = (value as Date).toJSON();
        } else if (value !== null && typeof value === 'object') {
            const toJSON: any = value.toJSON;
            if (typeof toJSON === 'function') value = toJSON.call(value, key);
        }
        if (this.replacer !== undefined) value = this.replacer.call(holder, key, value);
        if (value === null) return 'null';
        if (value === true) return 'true';
        if (value === false) return 'false';
        if (typeof value === 'string') return quote(value);
        if (typeof value === 'number') return isFinite(value) ? String(value) : 'null';
        if (typeof value === 'bigint') throw new TypeError('Do not know how to serialize a BigInt');
        if (typeof value === 'object') {
            return Array.isArray(value) ? this.array(holder, key, value) : this.object(holder, key, value);
        }
        return undefined;
    }

    enter(holder: any, key: string, value: any): string {
        const at = this.stack.indexOf(value);
        if (at >= 0) throw circularError(this.stack, this.keys, at, key, holder);
        this.stack.push(value);
        this.keys.push(key);
        const stepback = this.indent;
        this.indent = this.indent + this.gap;
        return stepback;
    }

    leave(stepback: string): void {
        this.stack.pop();
        this.keys.pop();
        this.indent = stepback;
    }

    object(holder: any, key: string, value: any): string {
        const stepback = this.enter(holder, key, value);
        const names: string[] = this.props !== undefined ? this.props : Object.keys(value);
        const parts: string[] = [];
        for (const k of names) {
            const s = this.property(value, k, value[k]);
            if (s !== undefined) parts.push(quote(k) + (this.gap === '' ? ':' : ': ') + s);
        }
        let out: string;
        if (parts.length === 0) out = '{}';
        else if (this.gap === '') out = '{' + parts.join(',') + '}';
        else out = '{\n' + this.indent + parts.join(',\n' + this.indent) + '\n' + stepback + '}';
        this.leave(stepback);
        return out;
    }

    array(holder: any, key: string, value: any): string {
        const stepback = this.enter(holder, key, value);
        const parts: string[] = [];
        const n: number = value.length;
        for (let i = 0; i < n; i++) {
            const s = this.property(value, String(i), value[i]);
            parts.push(s === undefined ? 'null' : s);
        }
        let out: string;
        if (parts.length === 0) out = '[]';
        else if (this.gap === '') out = '[' + parts.join(',') + ']';
        else out = '[\n' + this.indent + parts.join(',\n' + this.indent) + '\n' + stepback + ']';
        this.leave(stepback);
        return out;
    }
}

export function stringifyWith(value: any, replacer: any, space: any): any {
    const s = new Serializer();
    if (typeof replacer === 'function') {
        s.replacer = replacer;
    } else if (Array.isArray(replacer)) {
        const props: string[] = [];
        const n: number = replacer.length;
        for (let i = 0; i < n; i++) {
            const v: any = replacer[i];
            let item: string | undefined = undefined;
            if (typeof v === 'string') item = v;
            else if (typeof v === 'number') item = String(v);
            if (item !== undefined && props.indexOf(item) < 0) props.push(item);
        }
        s.props = props;
    }
    if (typeof space === 'number') {
        const k = Math.min(10, Math.trunc(space));
        s.gap = k >= 1 ? ' '.repeat(k) : '';
    } else if (typeof space === 'string') {
        s.gap = space.slice(0, 10);
    }
    const wrapper: any = {};
    wrapper[''] = value;
    return s.property(wrapper, '', value);
}

// InternalizeJSONProperty: each property's revived value, bottom-up; a
// property the reviver returns undefined for is deleted.
function internalize(holder: any, name: string, reviver: any): any {
    const val: any = holder[name];
    if (val !== null && typeof val === 'object') {
        if (Array.isArray(val)) {
            const n: number = val.length;
            for (let i = 0; i < n; i++) {
                const v = internalize(val, String(i), reviver);
                if (v === undefined) delete val[i];
                else val[i] = v;
            }
        } else {
            for (const k of Object.keys(val)) {
                const v = internalize(val, k, reviver);
                if (v === undefined) delete val[k];
                else val[k] = v;
            }
        }
    }
    return reviver.call(holder, name, val);
}

export function parseWith(text: string, reviver: any): any {
    const value: any = JSON.parse(text);
    if (typeof reviver !== 'function') return value;
    const root: any = {};
    root[''] = value;
    return internalize(root, '', reviver);
}
