// Node's `node:sqlite`, ported from Node v24's lib/sqlite.js and
// src/node_sqlite.cc: DatabaseSync, StatementSync, Session, SQLTagStore,
// constants and backup(), over libsqlite3 (klainsqlite.c). Rows are
// null-prototype objects (or arrays with returnArrays); an INTEGER column is
// a number, a bigint with readBigInts, and ERR_OUT_OF_RANGE past 2^53; a
// user function or aggregate runs during the step that calls it. The
// session, extension and serialization APIs are the system library's: where
// it lacks one, calling it is ERR_INVALID_STATE naming it.
//
// kml:scheme-only — Node exposes this module only as `node:sqlite`.

class NodeError extends Error {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

class NodeTypeError extends TypeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

class NodeRangeError extends RangeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

const SQLITE_OK = 0;
const SQLITE_ROW = 100;
const SQLITE_DONE = 101;
const SQLITE_OPEN_READONLY = 0x1;
const SQLITE_OPEN_READWRITE = 0x2;
const SQLITE_OPEN_CREATE = 0x4;
const SQLITE_DBCONFIG_DEFENSIVE = 1010;

export const constants = {
    SQLITE_CHANGESET_OMIT: 0,
    SQLITE_CHANGESET_REPLACE: 1,
    SQLITE_CHANGESET_ABORT: 2,
    SQLITE_CHANGESET_DATA: 1,
    SQLITE_CHANGESET_NOTFOUND: 2,
    SQLITE_CHANGESET_CONFLICT: 3,
    SQLITE_CHANGESET_CONSTRAINT: 4,
    SQLITE_CHANGESET_FOREIGN_KEY: 5,
    SQLITE_OK: 0,
    SQLITE_DENY: 1,
    SQLITE_IGNORE: 2,
    SQLITE_CREATE_INDEX: 1,
    SQLITE_CREATE_TABLE: 2,
    SQLITE_CREATE_TEMP_INDEX: 3,
    SQLITE_CREATE_TEMP_TABLE: 4,
    SQLITE_CREATE_TEMP_TRIGGER: 5,
    SQLITE_CREATE_TEMP_VIEW: 6,
    SQLITE_CREATE_TRIGGER: 7,
    SQLITE_CREATE_VIEW: 8,
    SQLITE_DELETE: 9,
    SQLITE_DROP_INDEX: 10,
    SQLITE_DROP_TABLE: 11,
    SQLITE_DROP_TEMP_INDEX: 12,
    SQLITE_DROP_TEMP_TABLE: 13,
    SQLITE_DROP_TEMP_TRIGGER: 14,
    SQLITE_DROP_TEMP_VIEW: 15,
    SQLITE_DROP_TRIGGER: 16,
    SQLITE_DROP_VIEW: 17,
    SQLITE_INSERT: 18,
    SQLITE_PRAGMA: 19,
    SQLITE_READ: 20,
    SQLITE_SELECT: 21,
    SQLITE_TRANSACTION: 22,
    SQLITE_UPDATE: 23,
    SQLITE_ATTACH: 24,
    SQLITE_DETACH: 25,
    SQLITE_ALTER_TABLE: 26,
    SQLITE_REINDEX: 27,
    SQLITE_ANALYZE: 28,
    SQLITE_CREATE_VTABLE: 29,
    SQLITE_DROP_VTABLE: 30,
    SQLITE_FUNCTION: 31,
    SQLITE_SAVEPOINT: 32,
    SQLITE_COPY: 0,
    SQLITE_RECURSIVE: 33,
};

// ---- errors (node_sqlite.cc CreateSQLiteError) ----

function sqliteErrorFrom(message: string, errcode: number): Error {
    const e: any = new Error(message);
    e.code = 'ERR_SQLITE_ERROR';
    e.errcode = errcode;
    e.errstr = __kml_native.sqliteErrstr(errcode);
    return e;
}

function dbError(db: number): Error {
    return sqliteErrorFrom(__kml_native.sqliteErrmsg(db), __kml_native.sqliteErrcode(db));
}

function invalidState(message: string): Error {
    return new NodeError('ERR_INVALID_STATE', message);
}

function argType(name: string, what: string): Error {
    return new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be ' + what + '.');
}

function isPlainObject(v: any): boolean {
    return v !== null && typeof v === 'object' && !Array.isArray(v) && !ArrayBuffer.isView(v) && !(v instanceof ArrayBuffer);
}

function optionBool(options: any, key: string, dflt: boolean): boolean {
    const v = options[key];
    if (v === undefined) return dflt;
    if (typeof v !== 'boolean') throw argType('options.' + key, 'a boolean');
    return v as boolean;
}

// ---- values in and out ----

// An INTEGER as a number, or a bigint (readBigInts); past 2^53 without
// readBigInts, ERR_OUT_OF_RANGE.
function intValue(decimal: string, asBigInt: boolean): any {
    if (asBigInt) return BigInt(decimal);
    const n = Number(decimal);
    if (!Number.isSafeInteger(n)) {
        throw new NodeRangeError('ERR_OUT_OF_RANGE', 'Value is too large to be represented as a JavaScript number: ' + decimal);
    }
    return n;
}

function columnValue(stmt: number, i: number, readBigInts: boolean): any {
    const t = __kml_native.sqliteColumnType(stmt, i);
    if (t === 1) return intValue(__kml_native.sqliteColumnInt(stmt, i), readBigInts);
    if (t === 2) return __kml_native.sqliteColumnDouble(stmt, i);
    if (t === 3) return __kml_native.sqliteColumnText(stmt, i);
    if (t === 4) {
        const out = new Uint8Array(__kml_native.sqliteColumnBytes(stmt, i));
        __kml_native.sqliteColumnBlob(stmt, i, out);
        return out;
    }
    return null;
}

function argValue(i: number, asBigInt: boolean): any {
    const t = __kml_native.sqliteArgType(i);
    if (t === 1) return intValue(__kml_native.sqliteArgInt(i), asBigInt);
    if (t === 2) return __kml_native.sqliteArgDouble(i);
    if (t === 3) return __kml_native.sqliteArgText(i);
    if (t === 4) {
        const out = new Uint8Array(__kml_native.sqliteArgBytes(i));
        __kml_native.sqliteArgBlob(i, out);
        return out;
    }
    return null;
}

const INT64_MIN = BigInt('-9223372036854775808');
const INT64_MAX = BigInt('9223372036854775807');

// An ArrayBufferView's bytes.
function viewBytes(value: any): Uint8Array {
    if (value instanceof Uint8Array) return value as Uint8Array;
    const out = new Uint8Array(value.byteLength as number);
    __kml_native.typedBytes(value, out);
    return out;
}

// A user function's result as the SQLite value (UserDefinedFunction::xFunc).
function setResult(result: any): void {
    if (result === undefined || result === null) {
        __kml_native.sqliteResultNull();
    } else if (typeof result === 'number') {
        __kml_native.sqliteResultDouble(result as number);
    } else if (typeof result === 'string') {
        __kml_native.sqliteResultText(result as string);
    } else if (ArrayBuffer.isView(result)) {
        __kml_native.sqliteResultBlob(viewBytes(result));
    } else if (typeof result === 'bigint') {
        const b = result as bigint;
        if (b < INT64_MIN || b > INT64_MAX) {
            throw new NodeRangeError('ERR_OUT_OF_RANGE', 'BigInt value is too large for SQLite');
        }
        __kml_native.sqliteResultInt(String(b));
    } else if (result instanceof Promise) {
        throw new NodeError('ERR_SQLITE_ERROR', 'Asynchronous user-defined functions are not supported');
    } else {
        throw new NodeError('ERR_SQLITE_ERROR', 'Returned JavaScript value cannot be converted to a SQLite value');
    }
}

// Bind value to the statement's 1-based parameter i (BindValue).
function bindValue(stmt: number, i: number, value: any): number {
    if (value === null) return __kml_native.sqliteBindNull(stmt, i);
    if (typeof value === 'number') return __kml_native.sqliteBindDouble(stmt, i, value as number);
    if (typeof value === 'string') return __kml_native.sqliteBindText(stmt, i, value as string);
    if (typeof value === 'bigint') {
        const b = value as bigint;
        if (b < INT64_MIN || b > INT64_MAX) {
            throw new NodeError('ERR_INVALID_ARG_VALUE', 'BigInt value is too large to bind.');
        }
        return __kml_native.sqliteBindInt(stmt, i, String(b));
    }
    if (ArrayBuffer.isView(value)) return __kml_native.sqliteBindBlob(stmt, i, viewBytes(value));
    throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'Provided value cannot be bound to SQLite parameter ' + i + '.');
}

// ---- DatabaseSync ----

export interface DatabaseSyncOptions {
    open?: boolean | undefined;
    readOnly?: boolean | undefined;
    enableForeignKeyConstraints?: boolean | undefined;
    enableDoubleQuotedStringLiterals?: boolean | undefined;
    allowExtension?: boolean | undefined;
    timeout?: number | undefined;
    readBigInts?: boolean | undefined;
    returnArrays?: boolean | undefined;
    allowBareNamedParameters?: boolean | undefined;
    allowUnknownNamedParameters?: boolean | undefined;
    defensive?: boolean | undefined;
}

export interface FunctionOptions {
    deterministic?: boolean | undefined;
    directOnly?: boolean | undefined;
    useBigIntArguments?: boolean | undefined;
    varargs?: boolean | undefined;
}

export interface AggregateOptions<T = any> extends FunctionOptions {
    start: T | (() => T);
    step: (accumulator: T, ...args: any[]) => T;
    result?: ((accumulator: T) => any) | undefined;
    inverse?: ((accumulator: T, ...args: any[]) => T) | undefined;
}

export interface CreateSessionOptions {
    table?: string | undefined;
    db?: string | undefined;
}

export interface ApplyChangesetOptions {
    filter?: ((tableName: string) => boolean) | undefined;
    onConflict?: ((conflictType: number) => number) | undefined;
}

export interface StatementResultingChanges {
    changes: number | bigint;
    lastInsertRowid: number | bigint;
}

function pathOf(path: any): string {
    let p: string;
    if (typeof path === 'string') {
        p = path as string;
    } else if (path instanceof URL) {
        if ((path as URL).protocol !== 'file:') throw argType('path', 'a string, Uint8Array, or URL without null bytes');
        p = (path as URL).href;
    } else if (path instanceof Uint8Array) {
        p = new TextDecoder().decode(path as Uint8Array);
    } else {
        throw argType('path', 'a string, Uint8Array, or URL without null bytes');
    }
    if (p.indexOf('\u0000') >= 0) throw argType('path', 'a string, Uint8Array, or URL without null bytes');
    return p;
}

export class DatabaseSync {
    _kmlDb = -1;
    _kmlPath: string;
    _kmlStatements: StatementSync[] = [];
    _kmlSessions: Session[] = [];
    _kmlReadOnly: boolean;
    _kmlForeignKeys: boolean;
    _kmlDqs: boolean;
    _kmlAllowExtension: boolean;
    _kmlTimeout: number;
    _kmlReadBigInts: boolean;
    _kmlReturnArrays: boolean;
    _kmlAllowBare: boolean;
    _kmlAllowUnknown: boolean;
    _kmlDefensive: boolean;
    _kmlLoadExtension: boolean;
    // An exception a user function threw during the current step.
    _kmlPending: any = undefined;
    _kmlHasPending = false;

    constructor(path: string | Uint8Array | URL, options?: DatabaseSyncOptions) {
        this._kmlPath = pathOf(path);
        let opts: any = {};
        if (options !== undefined) {
            if (options === null || typeof options !== 'object') throw argType('options', 'an object');
            opts = options;
        }
        const open = optionBool(opts, 'open', true);
        this._kmlReadOnly = optionBool(opts, 'readOnly', false);
        this._kmlForeignKeys = optionBool(opts, 'enableForeignKeyConstraints', true);
        this._kmlDqs = optionBool(opts, 'enableDoubleQuotedStringLiterals', false);
        this._kmlAllowExtension = optionBool(opts, 'allowExtension', false);
        this._kmlReadBigInts = optionBool(opts, 'readBigInts', false);
        this._kmlReturnArrays = optionBool(opts, 'returnArrays', false);
        this._kmlAllowBare = optionBool(opts, 'allowBareNamedParameters', true);
        this._kmlAllowUnknown = optionBool(opts, 'allowUnknownNamedParameters', false);
        this._kmlDefensive = optionBool(opts, 'defensive', false);
        this._kmlLoadExtension = false;
        const timeout = opts.timeout;
        if (timeout !== undefined && (typeof timeout !== 'number' || !Number.isInteger(timeout))) {
            throw argType('options.timeout', 'an integer');
        }
        this._kmlTimeout = timeout === undefined ? 0 : timeout as number;
        if (open) this.open();
    }

    open(): void {
        if (this._kmlDb >= 0) throw invalidState('database is already open');
        const flags = this._kmlReadOnly ? SQLITE_OPEN_READONLY : SQLITE_OPEN_READWRITE | SQLITE_OPEN_CREATE;
        const db = __kml_native.sqliteOpen(this._kmlPath, flags, this._kmlTimeout, this._kmlForeignKeys, this._kmlDqs);
        if (db < 0) throw sqliteErrorFrom(__kml_native.sqliteOpenError(), __kml_native.sqliteOpenCode());
        this._kmlDb = db;
        if (this._kmlDefensive) __kml_native.sqliteDbConfig(db, SQLITE_DBCONFIG_DEFENSIVE, 1);
        if (this._kmlAllowExtension) {
            const rc = __kml_native.sqliteEnableLoadExtension(db, true);
            if (rc > 0) throw dbError(db);
            this._kmlLoadExtension = rc === 0;
        }
    }

    _kmlCheckOpen(): number {
        if (this._kmlDb < 0) throw invalidState('database is not open');
        return this._kmlDb;
    }

    get isOpen(): boolean {
        return this._kmlDb >= 0;
    }

    get isTransaction(): boolean {
        return __kml_native.sqliteInTransaction(this._kmlCheckOpen());
    }

    close(): void {
        const db = this._kmlCheckOpen();
        for (const st of this._kmlStatements) st._kmlFinalize();
        this._kmlStatements = [];
        for (const s of this._kmlSessions) s._kmlDelete();
        this._kmlSessions = [];
        const rc = __kml_native.sqliteClose(db);
        this._kmlDb = -1;
        if (rc !== SQLITE_OK) throw sqliteErrorFrom(__kml_native.sqliteErrstr(rc), rc);
    }

    [Symbol.dispose](): void {
        if (this._kmlDb >= 0) this.close();
    }

    location(dbName?: string): string | null {
        const db = this._kmlCheckOpen();
        const name = dbName === undefined ? 'main' : dbName;
        if (typeof name !== 'string') throw argType('dbName', 'a string');
        if (!__kml_native.sqliteHasDb(db, name)) return null;
        const f = __kml_native.sqliteFilename(db, name);
        return f === '' ? null : f;
    }

    prepare(sql: string): StatementSync {
        const db = this._kmlCheckOpen();
        if (typeof sql !== 'string') throw argType('sql', 'a string');
        const id = __kml_native.sqlitePrepare(db, sql);
        if (id === -1) throw dbError(db);
        const st = new StatementSync(this, id === -2 ? -1 : id, sql);
        if (id >= 0) this._kmlStatements.push(st);
        return st;
    }

    exec(sql: string): void {
        const db = this._kmlCheckOpen();
        if (typeof sql !== 'string') throw argType('sql', 'a string');
        this._kmlHasPending = false;
        const rc = __kml_native.sqliteExec(db, sql);
        this._kmlThrowPending();
        if (rc !== SQLITE_OK) throw dbError(db);
    }

    _kmlThrowPending(): void {
        if (this._kmlHasPending) {
            const e = this._kmlPending;
            this._kmlHasPending = false;
            this._kmlPending = undefined;
            throw e;
        }
    }

    _kmlCatch(e: any): void {
        if (!this._kmlHasPending) {
            this._kmlHasPending = true;
            this._kmlPending = e;
        }
        __kml_native.sqliteResultError(e instanceof Error ? (e as Error).message : String(e));
    }

    function(name: string, optionsOrFn: any, maybeFn?: any): void {
        const db = this._kmlCheckOpen();
        if (typeof name !== 'string') throw argType('name', 'a string');
        let options: any = {};
        let fn: any = optionsOrFn;
        if (typeof optionsOrFn !== 'function') {
            if (optionsOrFn === null || typeof optionsOrFn !== 'object') throw argType('options', 'an object');
            options = optionsOrFn;
            fn = maybeFn;
        }
        if (typeof fn !== 'function') throw argType('function', 'a function');
        const useBigInt = optionBool(options, 'useBigIntArguments', false);
        const varargs = optionBool(options, 'varargs', false);
        const deterministic = optionBool(options, 'deterministic', false);
        const directOnly = optionBool(options, 'directOnly', false);
        const argc = varargs ? -1 : (fn as Function).length;
        const f: any = fn;
        const rc = __kml_native.sqliteCreateFunction(db, name, argc, (deterministic ? 1 : 0) | (directOnly ? 2 : 0), 0, (kind: number, key: number) => {
            try {
                const n = __kml_native.sqliteArgCount();
                const args: any[] = [];
                for (let i = 0; i < n; i++) args.push(argValue(i, useBigInt));
                setResult(f(...args));
            } catch (e) {
                this._kmlCatch(e);
            }
        });
        if (rc !== SQLITE_OK) throw dbError(db);
    }

    aggregate(name: string, options: AggregateOptions): void {
        const db = this._kmlCheckOpen();
        if (typeof name !== 'string') throw argType('name', 'a string');
        const opts: any = options;
        if (opts === null || typeof opts !== 'object') throw argType('options', 'an object');
        if (opts.start === undefined) throw argType('options.start', 'a value');
        if (typeof opts.step !== 'function') throw argType('options.step', 'a function');
        if (opts.result !== undefined && typeof opts.result !== 'function') throw argType('options.result', 'a function');
        if (opts.inverse !== undefined && typeof opts.inverse !== 'function') throw argType('options.inverse', 'a function');
        const useBigInt = optionBool(opts, 'useBigIntArguments', false);
        const varargs = optionBool(opts, 'varargs', false);
        const directOnly = optionBool(opts, 'directOnly', false);
        const step: any = opts.step;
        const result: any = opts.result;
        const inverse: any = opts.inverse;
        const start: any = opts.start;
        let argc = -1;
        if (!varargs) {
            argc = Math.max((step as Function).length, inverse !== undefined ? (inverse as Function).length : 0) - 1;
            if (argc < 0) argc = 0;
        }
        const groups = new Map<number, any>();
        const accumulator = (key: number): any => {
            if (!groups.has(key)) groups.set(key, typeof start === 'function' ? start() : start);
            return groups.get(key);
        };
        const kind = inverse !== undefined ? 2 : 1;
        const rc = __kml_native.sqliteCreateFunction(db, name, argc, directOnly ? 2 : 0, kind, (what: number, key: number) => {
            try {
                if (what === 1 || what === 4) {
                    const n = __kml_native.sqliteArgCount();
                    const args: any[] = [accumulator(key)];
                    for (let i = 0; i < n; i++) args.push(argValue(i, useBigInt));
                    groups.set(key, what === 1 ? step(...args) : inverse(...args));
                    return;
                }
                const acc = accumulator(key);
                if (what === 2) groups.delete(key);
                setResult(result !== undefined ? result(acc) : acc);
            } catch (e) {
                this._kmlCatch(e);
            }
        });
        if (rc !== SQLITE_OK) throw dbError(db);
    }

    setAuthorizer(callback: ((actionCode: number, arg1: string | null, arg2: string | null, dbName: string | null, triggerOrView: string | null) => number) | null): void {
        const db = this._kmlCheckOpen();
        if (callback === null) {
            __kml_native.sqliteClearAuthorizer(db);
            return;
        }
        if (typeof callback !== 'function') throw argType('callback', 'a function');
        const cb: any = callback;
        const rc = __kml_native.sqliteSetAuthorizer(db, (action: number) => {
            const a: any[] = [];
            for (let i = 0; i < 4; i++) a.push(__kml_native.sqliteAuthHas(i) ? __kml_native.sqliteAuthArg(i) : null);
            try {
                const r = cb(action, a[0], a[1], a[2], a[3]);
                if (typeof r !== 'number' || (r !== 0 && r !== 1 && r !== 2)) {
                    this._kmlHasPending = true;
                    this._kmlPending = new NodeTypeError('ERR_INVALID_RETURN_VALUE', 'Authorizer callback must return an integer authorization code');
                    __kml_native.sqliteCallbackReturn(1);
                    return;
                }
                __kml_native.sqliteCallbackReturn(r as number);
            } catch (e) {
                if (!this._kmlHasPending) {
                    this._kmlHasPending = true;
                    this._kmlPending = e;
                }
                __kml_native.sqliteCallbackReturn(1);
            }
        });
        if (rc !== SQLITE_OK) throw dbError(db);
    }

    enableLoadExtension(allow: boolean): void {
        const db = this._kmlCheckOpen();
        if (typeof allow !== 'boolean') throw argType('allow', 'a boolean');
        if (allow && !this._kmlAllowExtension) {
            throw invalidState('Cannot enable extension loading because it was disabled at database creation.');
        }
        const rc = __kml_native.sqliteEnableLoadExtension(db, allow);
        if (rc < 0) throw invalidState('extension loading is not available: the system SQLite library omits it');
        if (rc !== SQLITE_OK) throw dbError(db);
        this._kmlLoadExtension = allow;
    }

    loadExtension(path: string, entryPoint?: string): void {
        const db = this._kmlCheckOpen();
        if (typeof path !== 'string') throw argType('path', 'a string');
        if (!this._kmlAllowExtension || !this._kmlLoadExtension) throw invalidState('extension loading is not allowed');
        const rc = __kml_native.sqliteLoadExtension(db, path, entryPoint === undefined ? '' : entryPoint);
        if (rc < 0) throw invalidState('extension loading is not available: the system SQLite library omits it');
        if (rc !== SQLITE_OK) throw dbError(db);
    }

    enableDefensive(active: boolean): void {
        const db = this._kmlCheckOpen();
        if (typeof active !== 'boolean') throw argType('active', 'a boolean');
        const rc = __kml_native.sqliteDbConfig(db, SQLITE_DBCONFIG_DEFENSIVE, active ? 1 : 0);
        if (rc !== SQLITE_OK) throw dbError(db);
    }

    createSession(options?: CreateSessionOptions): Session {
        const db = this._kmlCheckOpen();
        const opts: any = options === undefined ? {} : options;
        if (opts === null || typeof opts !== 'object') throw argType('options', 'an object');
        if (opts.table !== undefined && typeof opts.table !== 'string') throw argType('options.table', 'a string');
        if (opts.db !== undefined && typeof opts.db !== 'string') throw argType('options.db', 'a string');
        const id = __kml_native.sqliteSessionCreate(db, opts.db === undefined ? 'main' : opts.db, opts.table === undefined ? '' : opts.table);
        if (id === -1) throw invalidState('sessions are not available: the system SQLite library omits them');
        if (id < -1) throw sqliteErrorFrom(__kml_native.sqliteErrstr(-2 - id), -2 - id);
        const s = new Session(this, id);
        this._kmlSessions.push(s);
        return s;
    }

    applyChangeset(changeset: Uint8Array, options?: ApplyChangesetOptions): boolean {
        const db = this._kmlCheckOpen();
        if (!(changeset instanceof Uint8Array)) throw argType('changeset', 'a Uint8Array');
        const opts: any = options === undefined ? {} : options;
        if (opts === null || typeof opts !== 'object') throw argType('options', 'an object');
        const filter: any = opts.filter;
        const onConflict: any = opts.onConflict;
        if (filter !== undefined && typeof filter !== 'function') throw argType('options.filter', 'a function');
        if (onConflict !== undefined && typeof onConflict !== 'function') throw argType('options.onConflict', 'a function');
        this._kmlHasPending = false;
        const rc = __kml_native.sqliteApplyChangeset(db, changeset, filter !== undefined, (kind: number, conflict: number) => {
            try {
                if (kind === 0) {
                    __kml_native.sqliteCallbackReturn(filter(__kml_native.sqliteAuthArg(0)) ? 1 : 0);
                    return;
                }
                if (onConflict === undefined) {
                    __kml_native.sqliteCallbackReturn(constants.SQLITE_CHANGESET_ABORT);
                    return;
                }
                const r = onConflict(conflict);
                __kml_native.sqliteCallbackReturn(typeof r === 'number' ? r as number : constants.SQLITE_CHANGESET_ABORT);
            } catch (e) {
                if (!this._kmlHasPending) {
                    this._kmlHasPending = true;
                    this._kmlPending = e;
                }
                __kml_native.sqliteCallbackReturn(kind === 0 ? 0 : constants.SQLITE_CHANGESET_ABORT);
            }
        });
        this._kmlThrowPending();
        if (rc === -1) throw invalidState('sessions are not available: the system SQLite library omits them');
        if (rc === SQLITE_OK) return true;
        if (rc === 4) return false; // SQLITE_ABORT
        throw dbError(db);
    }

    serialize(dbName?: string): Uint8Array {
        const db = this._kmlCheckOpen();
        const name = dbName === undefined ? 'main' : dbName;
        if (typeof name !== 'string') throw argType('dbName', 'a string');
        const n = __kml_native.sqliteSerialize(db, name);
        if (n < 0) throw invalidState('database "' + name + '" could not be serialized');
        const out = new Uint8Array(n);
        __kml_native.sqliteSerializedBytes(out);
        return out;
    }

    deserialize(buffer: Uint8Array, options?: { dbName?: string }): void {
        const db = this._kmlCheckOpen();
        if (!(buffer instanceof Uint8Array)) throw argType('buffer', 'a Uint8Array');
        const opts: any = options === undefined ? {} : options;
        const name = opts.dbName === undefined ? 'main' : opts.dbName;
        const rc = __kml_native.sqliteDeserialize(db, name, buffer);
        if (rc < 0) throw invalidState('deserialize is not available: the system SQLite library omits it');
        if (rc !== SQLITE_OK) throw dbError(db);
    }

    createTagStore(maxSize?: number): SQLTagStore {
        this._kmlCheckOpen();
        return new SQLTagStore(this, maxSize === undefined ? 1000 : maxSize);
    }
}

// ---- StatementSync ----

export class StatementSync {
    _kmlDatabase: DatabaseSync;
    _kmlStmt: number;
    _kmlSource: string;
    _kmlReadBigInts: boolean;
    _kmlReturnArrays: boolean;
    _kmlAllowBare: boolean;
    _kmlAllowUnknown: boolean;
    // Bare parameter names (no prefix) to the prefixed names they stand for.
    private _kmlBareNames: Map<string, string> | null = null;

    constructor(database: DatabaseSync, stmt: number, source: string) {
        this._kmlDatabase = database;
        this._kmlStmt = stmt;
        this._kmlSource = source;
        this._kmlReadBigInts = database._kmlReadBigInts;
        this._kmlReturnArrays = database._kmlReturnArrays;
        this._kmlAllowBare = database._kmlAllowBare;
        this._kmlAllowUnknown = database._kmlAllowUnknown;
    }

    _kmlFinalize(): void {
        if (this._kmlStmt >= 0) __kml_native.sqliteFinalize(this._kmlStmt);
        this._kmlStmt = -1;
    }

    private _kmlCheck(): number {
        if (this._kmlStmt < 0 || this._kmlDatabase._kmlDb < 0) throw invalidState('statement has been finalized');
        return this._kmlStmt;
    }

    get sourceSQL(): string {
        this._kmlCheck();
        return this._kmlSource;
    }

    get expandedSQL(): string {
        return __kml_native.sqliteSql(this._kmlCheck(), true);
    }

    setReadBigInts(enabled: boolean): void {
        this._kmlCheck();
        if (typeof enabled !== 'boolean') throw argType('readBigInts', 'a boolean');
        this._kmlReadBigInts = enabled;
    }

    setReturnArrays(enabled: boolean): void {
        this._kmlCheck();
        if (typeof enabled !== 'boolean') throw argType('returnArrays', 'a boolean');
        this._kmlReturnArrays = enabled;
    }

    setAllowBareNamedParameters(enabled: boolean): void {
        this._kmlCheck();
        if (typeof enabled !== 'boolean') throw argType('allowBareNamedParameters', 'a boolean');
        this._kmlAllowBare = enabled;
    }

    setAllowUnknownNamedParameters(enabled: boolean): void {
        this._kmlCheck();
        if (typeof enabled !== 'boolean') throw argType('enabled', 'a boolean');
        this._kmlAllowUnknown = enabled;
    }

    // BindParams: a leading plain object binds named parameters; the rest
    // bind the anonymous ones in order.
    private _kmlBind(params: any[]): void {
        const st = this._kmlStmt;
        const db = this._kmlDatabase._kmlDb;
        __kml_native.sqliteReset(st, true);
        let anon = 0;
        if (params.length > 0 && isPlainObject(params[0])) {
            anon = 1;
            const named: any = params[0];
            if (this._kmlAllowBare && this._kmlBareNames === null) {
                const bare = new Map<string, string>();
                const n = __kml_native.sqliteParamCount(st);
                for (let i = 1; i <= n; i++) {
                    const full = __kml_native.sqliteParamName(st, i);
                    if (full === '') continue;
                    const b = full.slice(1);
                    const prior = bare.get(b);
                    if (prior !== undefined && prior !== full) {
                        throw invalidState("Cannot create bare named parameter '" + b + "' because of conflicting names '" + prior + "' and '" + full + "'.");
                    }
                    bare.set(b, full);
                }
                this._kmlBareNames = bare;
            }
            for (const key of Object.keys(named)) {
                let index = __kml_native.sqliteParamIndex(st, key);
                if (index === 0 && this._kmlAllowBare && this._kmlBareNames !== null) {
                    const full = (this._kmlBareNames as Map<string, string>).get(key);
                    if (full !== undefined) index = __kml_native.sqliteParamIndex(st, full);
                }
                if (index === 0) {
                    if (this._kmlAllowUnknown) continue;
                    throw invalidState("Unknown named parameter '" + key + "'");
                }
                const rc = bindValue(st, index, named[key]);
                if (rc !== SQLITE_OK) throw dbError(db);
            }
        }
        let position = 1;
        const count = __kml_native.sqliteParamCount(st);
        for (let i = anon; i < params.length; i++) {
            while (position <= count && __kml_native.sqliteParamName(st, position) !== '') position++;
            const rc = bindValue(st, position, params[i]);
            if (rc !== SQLITE_OK) throw dbError(db);
            position++;
        }
    }

    private _kmlStep(): number {
        const database = this._kmlDatabase;
        database._kmlHasPending = false;
        const rc = __kml_native.sqliteStep(this._kmlStmt);
        database._kmlThrowPending();
        if (rc !== SQLITE_ROW && rc !== SQLITE_DONE) {
            const e = dbError(database._kmlDb);
            __kml_native.sqliteReset(this._kmlStmt, false);
            throw e;
        }
        return rc;
    }

    _kmlRow(): any {
        const st = this._kmlStmt;
        const n = __kml_native.sqliteColumnCount(st);
        if (this._kmlReturnArrays) {
            const row: any[] = [];
            for (let i = 0; i < n; i++) row.push(columnValue(st, i, this._kmlReadBigInts));
            return row;
        }
        const row: any = Object.create(null);
        for (let i = 0; i < n; i++) row[__kml_native.sqliteColumnMeta(st, i, 0)] = columnValue(st, i, this._kmlReadBigInts);
        return row;
    }

    run(...params: any[]): StatementResultingChanges {
        this._kmlCheck();
        this._kmlBind(params);
        while (this._kmlStep() === SQLITE_ROW) { /* rows are discarded */ }
        __kml_native.sqliteReset(this._kmlStmt, false);
        const db = this._kmlDatabase._kmlDb;
        return {
            changes: intValue(__kml_native.sqliteChanges(db), this._kmlReadBigInts),
            lastInsertRowid: intValue(__kml_native.sqliteLastRowid(db), this._kmlReadBigInts),
        };
    }

    get(...params: any[]): any {
        this._kmlCheck();
        this._kmlBind(params);
        let row: any = undefined;
        if (this._kmlStep() === SQLITE_ROW) row = this._kmlRow();
        __kml_native.sqliteReset(this._kmlStmt, false);
        return row;
    }

    all(...params: any[]): any[] {
        this._kmlCheck();
        this._kmlBind(params);
        const rows: any[] = [];
        while (this._kmlStep() === SQLITE_ROW) rows.push(this._kmlRow());
        __kml_native.sqliteReset(this._kmlStmt, false);
        return rows;
    }

    iterate(...params: any[]): StatementSyncIterator {
        this._kmlCheck();
        this._kmlBind(params);
        return new StatementSyncIterator(this);
    }

    _kmlIterStep(): number {
        return this._kmlStep();
    }

    columns(): any[] {
        const st = this._kmlCheck();
        const n = __kml_native.sqliteColumnCount(st);
        const out: any[] = [];
        for (let i = 0; i < n; i++) {
            const c: any = Object.create(null);
            c.column = __kml_native.sqliteColumnHas(st, i, 4) ? __kml_native.sqliteColumnMeta(st, i, 4) : null;
            c.database = __kml_native.sqliteColumnHas(st, i, 2) ? __kml_native.sqliteColumnMeta(st, i, 2) : null;
            c.name = __kml_native.sqliteColumnMeta(st, i, 0);
            c.table = __kml_native.sqliteColumnHas(st, i, 3) ? __kml_native.sqliteColumnMeta(st, i, 3) : null;
            c.type = __kml_native.sqliteColumnHas(st, i, 1) ? __kml_native.sqliteColumnMeta(st, i, 1) : null;
            out.push(c);
        }
        return out;
    }
}

// The iterator of stmt.iterate(): each next() steps the statement once.
class StatementSyncIterator {
    private _kmlStatement: StatementSync;
    private _kmlDone = false;

    constructor(statement: StatementSync) {
        this._kmlStatement = statement;
    }

    private _kmlResult(done: boolean, value: any): any {
        const r: any = Object.create(null);
        r.done = done;
        r.value = value;
        return r;
    }

    next(): any {
        const s = this._kmlStatement;
        if (this._kmlDone) return this._kmlResult(true, null);
        if (s._kmlStmt < 0 || s._kmlDatabase._kmlDb < 0) throw invalidState('statement has been finalized');
        if (s._kmlIterStep() === SQLITE_DONE) {
            this._kmlDone = true;
            __kml_native.sqliteReset(s._kmlStmt, false);
            return this._kmlResult(true, null);
        }
        return this._kmlResult(false, s._kmlRow());
    }

    return(): any {
        if (!this._kmlDone) {
            this._kmlDone = true;
            const s = this._kmlStatement;
            if (s._kmlStmt >= 0) __kml_native.sqliteReset(s._kmlStmt, false);
        }
        return this._kmlResult(true, null);
    }

    [Symbol.iterator](): StatementSyncIterator {
        return this;
    }
}

// ---- Session ----

export class Session {
    private _kmlDatabase: DatabaseSync;
    private _kmlId: number;

    constructor(database: DatabaseSync, id: number) {
        this._kmlDatabase = database;
        this._kmlId = id;
    }

    _kmlDelete(): void {
        if (this._kmlId >= 0) __kml_native.sqliteSessionDelete(this._kmlId);
        this._kmlId = -1;
    }

    private _kmlSet(which: number): Uint8Array {
        if (this._kmlDatabase._kmlDb < 0) throw invalidState('database is not open');
        if (this._kmlId < 0) throw invalidState('session is not open');
        const n = __kml_native.sqliteSessionSet(this._kmlId, which);
        if (n < 0) throw sqliteErrorFrom(__kml_native.sqliteErrstr(-1 - n), -1 - n);
        const out = new Uint8Array(n);
        __kml_native.sqliteSessionBytes(out);
        return out;
    }

    changeset(): Uint8Array {
        return this._kmlSet(0);
    }

    patchset(): Uint8Array {
        return this._kmlSet(1);
    }

    close(): void {
        if (this._kmlDatabase._kmlDb < 0) throw invalidState('database is not open');
        if (this._kmlId < 0) throw invalidState('session is not open');
        this._kmlDelete();
    }

    [Symbol.dispose](): void {
        if (this._kmlId >= 0 && this._kmlDatabase._kmlDb >= 0) this._kmlDelete();
    }
}

// ---- SQLTagStore (db.createTagStore): statements cached by template ----

export class SQLTagStore {
    private _kmlDatabase: DatabaseSync;
    private _kmlCapacity: number;
    private _kmlCache = new Map<string, StatementSync>();

    constructor(database: DatabaseSync, capacity: number) {
        this._kmlDatabase = database;
        this._kmlCapacity = capacity;
    }

    get db(): DatabaseSync {
        return this._kmlDatabase;
    }

    get capacity(): number {
        return this._kmlCapacity;
    }

    get size(): number {
        return this._kmlCache.size;
    }

    clear(): void {
        this._kmlCache.clear();
    }

    private _kmlStatement(strings: TemplateStringsArray): StatementSync {
        const sql = strings.join('?');
        let st = this._kmlCache.get(sql);
        if (st === undefined) {
            st = this._kmlDatabase.prepare(sql);
            if (this._kmlCache.size >= this._kmlCapacity) {
                for (const oldest of this._kmlCache.keys()) {
                    this._kmlCache.delete(oldest);
                    break;
                }
            }
            this._kmlCache.set(sql, st);
        } else {
            this._kmlCache.delete(sql);
            this._kmlCache.set(sql, st);
        }
        return st;
    }

    run(strings: TemplateStringsArray, ...values: any[]): StatementResultingChanges {
        return this._kmlStatement(strings).run(...values);
    }

    get(strings: TemplateStringsArray, ...values: any[]): any {
        return this._kmlStatement(strings).get(...values);
    }

    all(strings: TemplateStringsArray, ...values: any[]): any[] {
        return this._kmlStatement(strings).all(...values);
    }

    iterate(strings: TemplateStringsArray, ...values: any[]): StatementSyncIterator {
        return this._kmlStatement(strings).iterate(...values);
    }
}

// ---- backup ----

export interface BackupOptions {
    source?: string | undefined;
    target?: string | undefined;
    rate?: number | undefined;
    progress?: ((progress: { totalPages: number; remainingPages: number }) => void) | undefined;
}

// Copy sourceDb's database into the file at path, rate pages per turn of the
// loop: the total page count.
export function backup(sourceDb: DatabaseSync, path: string | Uint8Array | URL, options?: BackupOptions): Promise<number> {
    if (!(sourceDb instanceof DatabaseSync)) throw argType('sourceDb', 'an object');
    const db = sourceDb._kmlCheckOpen();
    const dest = pathOf(path);
    const opts: any = options === undefined ? {} : options;
    if (opts === null || typeof opts !== 'object') throw argType('options', 'an object');
    const rate = opts.rate === undefined ? 100 : opts.rate;
    if (typeof rate !== 'number' || !Number.isInteger(rate)) throw argType('options.rate', 'an integer');
    const progress: any = opts.progress;
    if (progress !== undefined && typeof progress !== 'function') throw argType('options.progress', 'a function');
    const id = __kml_native.sqliteBackupInit(db, opts.source === undefined ? 'main' : opts.source, dest, opts.target === undefined ? 'main' : opts.target);
    if (id < 0) {
        return Promise.reject(sqliteErrorFrom(__kml_native.sqliteOpenError(), -1 - id));
    }
    return new Promise<number>((resolve, reject) => {
        const turn = () => {
            const rc = __kml_native.sqliteBackupStep(id, rate);
            const total = __kml_native.sqliteBackupProgress(id, 0);
            if (rc === SQLITE_OK || rc === 5 || rc === 6) { // more to copy, BUSY, LOCKED
                if (progress !== undefined) {
                    try {
                        progress({ totalPages: total, remainingPages: __kml_native.sqliteBackupProgress(id, 1) });
                    } catch (e) {
                        __kml_native.sqliteBackupFinish(id);
                        reject(e);
                        return;
                    }
                }
                setImmediate(turn);
                return;
            }
            const fin = __kml_native.sqliteBackupFinish(id);
            if (rc !== SQLITE_DONE) {
                reject(sqliteErrorFrom(__kml_native.sqliteErrstr(rc), rc));
            } else if (fin !== SQLITE_OK) {
                reject(sqliteErrorFrom(__kml_native.sqliteOpenError(), fin));
            } else {
                resolve(total);
            }
        };
        setImmediate(turn);
    });
}
