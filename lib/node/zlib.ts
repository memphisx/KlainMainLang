// Node's lib/zlib.js, in TypeScript: the zlib streams over a native handle
// per stream (klainzlib.c, Node's ZlibContext), the one-shot convenience
// methods, crc32, and the constants and codes.
// kml:default-namespace — `import zlib from 'zlib'` reads this module's
// exports.

import { Transform, finished } from 'stream';
import type { TransformOptions, TransformCallback } from 'stream';
import { isArrayBufferView, isAnyArrayBuffer, isUint8Array } from './util_types';
import { received } from './internal_dns_utils';
import { NodeError, NodeTypeError, NodeRangeError } from './internal_errors';

const kMaxLength = 9007199254740991;

export const constants = {
    Z_NO_FLUSH: 0,
    Z_PARTIAL_FLUSH: 1,
    Z_SYNC_FLUSH: 2,
    Z_FULL_FLUSH: 3,
    Z_FINISH: 4,
    Z_BLOCK: 5,
    Z_OK: 0,
    Z_STREAM_END: 1,
    Z_NEED_DICT: 2,
    Z_ERRNO: -1,
    Z_STREAM_ERROR: -2,
    Z_DATA_ERROR: -3,
    Z_MEM_ERROR: -4,
    Z_BUF_ERROR: -5,
    Z_VERSION_ERROR: -6,
    Z_NO_COMPRESSION: 0,
    Z_BEST_SPEED: 1,
    Z_BEST_COMPRESSION: 9,
    Z_DEFAULT_COMPRESSION: -1,
    Z_FILTERED: 1,
    Z_HUFFMAN_ONLY: 2,
    Z_RLE: 3,
    Z_FIXED: 4,
    Z_DEFAULT_STRATEGY: 0,
    // The linked zlib's version.
    ZLIB_VERNUM: __kml_native.zlibVernum(),
    DEFLATE: 1,
    INFLATE: 2,
    GZIP: 3,
    GUNZIP: 4,
    DEFLATERAW: 5,
    INFLATERAW: 6,
    UNZIP: 7,
    BROTLI_DECODE: 8,
    BROTLI_ENCODE: 9,
    ZSTD_DECOMPRESS: 11,
    ZSTD_COMPRESS: 10,
    Z_MIN_WINDOWBITS: 8,
    Z_MAX_WINDOWBITS: 15,
    Z_DEFAULT_WINDOWBITS: 15,
    Z_MIN_CHUNK: 64,
    Z_MAX_CHUNK: Infinity,
    Z_DEFAULT_CHUNK: 16384,
    Z_MIN_MEMLEVEL: 1,
    Z_MAX_MEMLEVEL: 9,
    Z_DEFAULT_MEMLEVEL: 8,
    Z_MIN_LEVEL: -1,
    Z_MAX_LEVEL: 9,
    Z_DEFAULT_LEVEL: -1,
    BROTLI_OPERATION_PROCESS: 0,
    BROTLI_OPERATION_FLUSH: 1,
    BROTLI_OPERATION_FINISH: 2,
    BROTLI_OPERATION_EMIT_METADATA: 3,
    BROTLI_PARAM_MODE: 0,
    BROTLI_MODE_GENERIC: 0,
    BROTLI_MODE_TEXT: 1,
    BROTLI_MODE_FONT: 2,
    BROTLI_DEFAULT_MODE: 0,
    BROTLI_PARAM_QUALITY: 1,
    BROTLI_MIN_QUALITY: 0,
    BROTLI_MAX_QUALITY: 11,
    BROTLI_DEFAULT_QUALITY: 11,
    BROTLI_PARAM_LGWIN: 2,
    BROTLI_MIN_WINDOW_BITS: 10,
    BROTLI_MAX_WINDOW_BITS: 24,
    BROTLI_LARGE_MAX_WINDOW_BITS: 30,
    BROTLI_DEFAULT_WINDOW: 22,
    BROTLI_PARAM_LGBLOCK: 3,
    BROTLI_MIN_INPUT_BLOCK_BITS: 16,
    BROTLI_MAX_INPUT_BLOCK_BITS: 24,
    BROTLI_PARAM_DISABLE_LITERAL_CONTEXT_MODELING: 4,
    BROTLI_PARAM_SIZE_HINT: 5,
    BROTLI_PARAM_LARGE_WINDOW: 6,
    BROTLI_PARAM_NPOSTFIX: 7,
    BROTLI_PARAM_NDIRECT: 8,
    BROTLI_DECODER_RESULT_ERROR: 0,
    BROTLI_DECODER_RESULT_SUCCESS: 1,
    BROTLI_DECODER_RESULT_NEEDS_MORE_INPUT: 2,
    BROTLI_DECODER_RESULT_NEEDS_MORE_OUTPUT: 3,
    BROTLI_DECODER_PARAM_DISABLE_RING_BUFFER_REALLOCATION: 0,
    BROTLI_DECODER_PARAM_LARGE_WINDOW: 1,
    BROTLI_DECODER_NO_ERROR: 0,
    BROTLI_DECODER_SUCCESS: 1,
    BROTLI_DECODER_NEEDS_MORE_INPUT: 2,
    BROTLI_DECODER_NEEDS_MORE_OUTPUT: 3,
    BROTLI_DECODER_ERROR_FORMAT_EXUBERANT_NIBBLE: -1,
    BROTLI_DECODER_ERROR_FORMAT_RESERVED: -2,
    BROTLI_DECODER_ERROR_FORMAT_EXUBERANT_META_NIBBLE: -3,
    BROTLI_DECODER_ERROR_FORMAT_SIMPLE_HUFFMAN_ALPHABET: -4,
    BROTLI_DECODER_ERROR_FORMAT_SIMPLE_HUFFMAN_SAME: -5,
    BROTLI_DECODER_ERROR_FORMAT_CL_SPACE: -6,
    BROTLI_DECODER_ERROR_FORMAT_HUFFMAN_SPACE: -7,
    BROTLI_DECODER_ERROR_FORMAT_CONTEXT_MAP_REPEAT: -8,
    BROTLI_DECODER_ERROR_FORMAT_BLOCK_LENGTH_1: -9,
    BROTLI_DECODER_ERROR_FORMAT_BLOCK_LENGTH_2: -10,
    BROTLI_DECODER_ERROR_FORMAT_TRANSFORM: -11,
    BROTLI_DECODER_ERROR_FORMAT_DICTIONARY: -12,
    BROTLI_DECODER_ERROR_FORMAT_WINDOW_BITS: -13,
    BROTLI_DECODER_ERROR_FORMAT_PADDING_1: -14,
    BROTLI_DECODER_ERROR_FORMAT_PADDING_2: -15,
    BROTLI_DECODER_ERROR_FORMAT_DISTANCE: -16,
    BROTLI_DECODER_ERROR_DICTIONARY_NOT_SET: -19,
    BROTLI_DECODER_ERROR_INVALID_ARGUMENTS: -20,
    BROTLI_DECODER_ERROR_ALLOC_CONTEXT_MODES: -21,
    BROTLI_DECODER_ERROR_ALLOC_TREE_GROUPS: -22,
    BROTLI_DECODER_ERROR_ALLOC_CONTEXT_MAP: -25,
    BROTLI_DECODER_ERROR_ALLOC_RING_BUFFER_1: -26,
    BROTLI_DECODER_ERROR_ALLOC_RING_BUFFER_2: -27,
    BROTLI_DECODER_ERROR_ALLOC_BLOCK_TYPE_TREES: -30,
    BROTLI_DECODER_ERROR_UNREACHABLE: -31,
    ZSTD_e_continue: 0,
    ZSTD_e_flush: 1,
    ZSTD_e_end: 2,
    ZSTD_fast: 1,
    ZSTD_dfast: 2,
    ZSTD_greedy: 3,
    ZSTD_lazy: 4,
    ZSTD_lazy2: 5,
    ZSTD_btlazy2: 6,
    ZSTD_btopt: 7,
    ZSTD_btultra: 8,
    ZSTD_btultra2: 9,
    ZSTD_c_compressionLevel: 100,
    ZSTD_c_windowLog: 101,
    ZSTD_c_hashLog: 102,
    ZSTD_c_chainLog: 103,
    ZSTD_c_searchLog: 104,
    ZSTD_c_minMatch: 105,
    ZSTD_c_targetLength: 106,
    ZSTD_c_strategy: 107,
    ZSTD_c_enableLongDistanceMatching: 160,
    ZSTD_c_ldmHashLog: 161,
    ZSTD_c_ldmMinMatch: 162,
    ZSTD_c_ldmBucketSizeLog: 163,
    ZSTD_c_ldmHashRateLog: 164,
    ZSTD_c_contentSizeFlag: 200,
    ZSTD_c_checksumFlag: 201,
    ZSTD_c_dictIDFlag: 202,
    ZSTD_c_nbWorkers: 400,
    ZSTD_c_jobSize: 401,
    ZSTD_c_overlapLog: 402,
    ZSTD_d_windowLogMax: 100,
    ZSTD_CLEVEL_DEFAULT: 3,
    ZSTD_error_no_error: 0,
    ZSTD_error_GENERIC: 1,
    ZSTD_error_prefix_unknown: 10,
    ZSTD_error_version_unsupported: 12,
    ZSTD_error_frameParameter_unsupported: 14,
    ZSTD_error_frameParameter_windowTooLarge: 16,
    ZSTD_error_corruption_detected: 20,
    ZSTD_error_checksum_wrong: 22,
    ZSTD_error_literals_headerWrong: 24,
    ZSTD_error_dictionary_corrupted: 30,
    ZSTD_error_dictionary_wrong: 32,
    ZSTD_error_dictionaryCreation_failed: 34,
    ZSTD_error_parameter_unsupported: 40,
    ZSTD_error_parameter_combination_unsupported: 41,
    ZSTD_error_parameter_outOfBound: 42,
    ZSTD_error_tableLog_tooLarge: 44,
    ZSTD_error_maxSymbolValue_tooLarge: 46,
    ZSTD_error_maxSymbolValue_tooSmall: 48,
    ZSTD_error_stabilityCondition_notRespected: 50,
    ZSTD_error_stage_wrong: 60,
    ZSTD_error_init_missing: 62,
    ZSTD_error_memory_allocation: 64,
    ZSTD_error_workSpace_tooSmall: 66,
    ZSTD_error_dstSize_tooSmall: 70,
    ZSTD_error_srcSize_wrong: 72,
    ZSTD_error_dstBuffer_null: 74,
    ZSTD_error_noForwardProgress_destFull: 80,
    ZSTD_error_noForwardProgress_inputEmpty: 82,
};

const Z_NO_FLUSH = 0;
const Z_BLOCK = 5;
const Z_PARTIAL_FLUSH = 1;
const Z_SYNC_FLUSH = 2;
const Z_FULL_FLUSH = 3;
const Z_FINISH = 4;
const Z_MIN_CHUNK = 64;
const Z_DEFAULT_CHUNK = 16384;
const Z_MIN_WINDOWBITS = 8;
const Z_MAX_WINDOWBITS = 15;
const Z_DEFAULT_WINDOWBITS = 15;
const Z_MIN_LEVEL = -1;
const Z_MAX_LEVEL = 9;
const Z_DEFAULT_COMPRESSION = -1;
const Z_MIN_MEMLEVEL = 1;
const Z_MAX_MEMLEVEL = 9;
const Z_DEFAULT_MEMLEVEL = 8;
const Z_DEFAULT_STRATEGY = 0;
const Z_FIXED = 4;
const DEFLATE = 1;
const INFLATE = 2;
const GZIP = 3;
const GUNZIP = 4;
const DEFLATERAW = 5;
const INFLATERAW = 6;
const UNZIP = 7;
const BROTLI_DECODE = 8;
const BROTLI_ENCODE = 9;
const ZSTD_COMPRESS = 10;
const ZSTD_DECOMPRESS = 11;

// Each family's flush range: zlib's Z_* flushes, Brotli's operations,
// Zstd's end directives.
function flushBound(mode: number): number[] {
    if (mode === BROTLI_ENCODE || mode === BROTLI_DECODE) return [0, 3];
    if (mode === ZSTD_COMPRESS || mode === ZSTD_DECOMPRESS) return [0, 2];
    return [Z_NO_FLUSH, Z_BLOCK];
}

// zlib's return codes, both ways.
export const codes: { [key: string]: any } = Object.freeze({
    Z_OK: 0,
    Z_STREAM_END: 1,
    Z_NEED_DICT: 2,
    Z_ERRNO: -1,
    Z_STREAM_ERROR: -2,
    Z_DATA_ERROR: -3,
    Z_MEM_ERROR: -4,
    Z_BUF_ERROR: -5,
    Z_VERSION_ERROR: -6,
    '0': 'Z_OK',
    '1': 'Z_STREAM_END',
    '2': 'Z_NEED_DICT',
    '-1': 'Z_ERRNO',
    '-2': 'Z_STREAM_ERROR',
    '-3': 'Z_DATA_ERROR',
    '-4': 'Z_MEM_ERROR',
    '-5': 'Z_BUF_ERROR',
    '-6': 'Z_VERSION_ERROR',
});

function invalidArgType(name: string, expected: string, value: any): Error {
    return new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be ' + expected + '.' + received(value));
}

function addNumericalSeparator(val: string): string {
    let res = '';
    let i = val.length;
    const start = val[0] === '-' ? 1 : 0;
    for (; i >= start + 4; i -= 3) res = '_' + val.slice(i - 3, i) + res;
    return val.slice(0, i) + res;
}

function outOfRange(name: string, range: string, value: any): Error {
    let shown = String(value);
    if (Number.isInteger(value) && Math.abs(value) > 2 ** 32) shown = addNumericalSeparator(shown);
    return new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "' + name + '" is out of range. It must be ' + range + '. Received ' + shown);
}

// internal/validators' checkFiniteNumber: false for undefined and NaN.
function checkFiniteNumber(n: any, name: string): boolean {
    if (n === undefined) return false;
    if (Number.isFinite(n)) return true;
    if (Number.isNaN(n)) return false;
    if (typeof n !== 'number') throw invalidArgType(name, 'of type number', n);
    throw outOfRange(name, 'a finite number', n);
}

function checkRangesOrGetDefault(n: any, name: string, lower: number, upper: number, def?: number): any {
    if (!checkFiniteNumber(n, name)) return def;
    if (n < lower || n > upper) throw outOfRange(name, '>= ' + lower + ' and <= ' + upper, n);
    return n;
}

function bufferTooLarge(max: number): Error {
    return new NodeRangeError('ERR_BUFFER_TOO_LARGE', 'Cannot create a Buffer larger than ' + max + ' bytes');
}

function trailingJunk(): Error {
    return new NodeTypeError('ERR_TRAILING_JUNK_AFTER_STREAM_END', 'Trailing junk found after the end of the compressed stream');
}

// A view's bytes: a Uint8Array as it is, a DataView over its window, any
// other TypedArray copied out.
function viewBytes(v: any): Uint8Array {
    if (isUint8Array(v)) return v as Uint8Array;
    if (v instanceof DataView) {
        const dv = v as DataView;
        return new Uint8Array(dv.buffer, dv.byteOffset, dv.byteLength);
    }
    const out = Buffer.alloc(v.byteLength);
    __kml_native.typedBytes(v, out);
    return out;
}

const flushiness: number[] = [0, 2, 3, 4, 5, 1];

function maxFlush(a: number, b: number): number {
    return flushiness[a] > flushiness[b] ? a : b;
}

// Zero-length writes that carry a flush kind (Node's kFlushBuffers), known
// by identity.
const kFlushFlagList: number[] = [Z_NO_FLUSH, Z_BLOCK, Z_PARTIAL_FLUSH, Z_SYNC_FLUSH, Z_FULL_FLUSH, Z_FINISH];
const kFlushBuffers: Buffer[] = kFlushFlagList.map(() => Buffer.alloc(0));

function flushKindOf(chunk: any): number {
    for (let i = 0; i < kFlushBuffers.length; i++) {
        if (chunk === kFlushBuffers[i]) return kFlushFlagList[i];
    }
    return -1;
}

const emptyBuffer = Buffer.alloc(0);

export interface ZlibOptions extends TransformOptions {
    flush?: number | undefined;
    finishFlush?: number | undefined;
    chunkSize?: number | undefined;
    windowBits?: number | undefined;
    level?: number | undefined;
    memLevel?: number | undefined;
    strategy?: number | undefined;
    dictionary?: NodeJS.ArrayBufferView | ArrayBuffer | undefined;
    info?: boolean | undefined;
    maxOutputLength?: number | undefined;
    rejectGarbageAfterEnd?: boolean | undefined;
}

// The native handle and the in-flight write (Node's binding.Zlib object).
class ZlibHandle {
    id: number;
    buffer: Uint8Array | null = null;
    cb: TransformCallback | null = null;
    availOutBefore = 0;
    availInBefore = 0;
    inOff = 0;
    flushFlag = 0;
    constructor(id: number) { this.id = id; }
}

// The base class for all Zlib-style streams.
class ZlibBase extends Transform {
    bytesWritten = 0;
    _handle: ZlibHandle | null;
    _outBuffer: Buffer;
    _outOffset = 0;
    _chunkSize: number;
    _defaultFlushFlag: number;
    _finishFlushFlag: number;
    _defaultFullFlushFlag: number;
    _info: boolean | undefined;
    _maxOutputLength: number;
    _rejectGarbageAfterEnd: boolean;
    kError: Error | null = null;
    // The write processCallback holds back while the readable side is full.
    private pendingWrite: (() => void) | null = null;

    constructor(opts: ZlibOptions | undefined, mode: number, handle: ZlibHandle, flush: number, finishFlush: number, fullFlush: number) {
        let chunkSize: any = Z_DEFAULT_CHUNK;
        let maxOutputLength: number = kMaxLength;
        if (opts) {
            chunkSize = opts.chunkSize;
            if (!checkFiniteNumber(chunkSize, 'options.chunkSize')) {
                chunkSize = Z_DEFAULT_CHUNK;
            } else if (chunkSize < Z_MIN_CHUNK) {
                throw outOfRange('options.chunkSize', '>= ' + Z_MIN_CHUNK, chunkSize);
            }
            const bound = flushBound(mode);
            flush = checkRangesOrGetDefault(opts.flush, 'options.flush', bound[0], bound[1], flush);
            finishFlush = checkRangesOrGetDefault(opts.finishFlush, 'options.finishFlush', bound[0], bound[1], finishFlush);
            maxOutputLength = checkRangesOrGetDefault(opts.maxOutputLength, 'options.maxOutputLength', 1, kMaxLength, kMaxLength);
            if (opts.rejectGarbageAfterEnd !== undefined && typeof opts.rejectGarbageAfterEnd !== 'boolean') {
                throw invalidArgType('options.rejectGarbageAfterEnd', 'of type boolean', opts.rejectGarbageAfterEnd);
            }
        }
        const topts: TransformOptions = { autoDestroy: true };
        if (opts) {
            if (opts.highWaterMark !== undefined) topts.highWaterMark = opts.highWaterMark;
            if (opts.readableHighWaterMark !== undefined) topts.readableHighWaterMark = opts.readableHighWaterMark;
            if (opts.writableHighWaterMark !== undefined) topts.writableHighWaterMark = opts.writableHighWaterMark;
            if (opts.autoDestroy !== undefined) topts.autoDestroy = opts.autoDestroy;
            if (opts.emitClose !== undefined) topts.emitClose = opts.emitClose;
            if (opts.signal !== undefined) topts.signal = opts.signal;
        }
        super(topts);
        this._handle = handle;
        this._outBuffer = Buffer.allocUnsafe(chunkSize);
        this._chunkSize = chunkSize;
        this._defaultFlushFlag = flush;
        this._finishFlushFlag = finishFlush;
        this._defaultFullFlushFlag = fullFlush;
        this._info = opts?.info;
        this._maxOutputLength = maxOutputLength;
        this._rejectGarbageAfterEnd = opts?.rejectGarbageAfterEnd === true;
    }

    get _closed(): boolean {
        return !this._handle;
    }

    reset(): void {
        if (!this._handle) throw new Error('zlib binding closed');
        if (__kml_native.zlibReset(this._handle.id) !== 0) zlibOnError(this);
    }

    _flush(callback: TransformCallback): void {
        this._transform(emptyBuffer, '' as BufferEncoding, callback);
    }

    flush(kind?: number | (() => void), callback?: () => void): void {
        if (typeof kind === 'function' || (kind === undefined && !callback)) {
            callback = kind as (() => void) | undefined;
            kind = this._defaultFullFlushFlag;
        }
        const k: number = checkRangesOrGetDefault(kind, 'kind', Z_NO_FLUSH, Z_BLOCK, this._defaultFullFlushFlag);
        if (this.writableFinished) {
            if (callback) process.nextTick(callback);
        } else if (this.writableEnded) {
            if (callback) this.once('end', callback);
        } else {
            this.write(kFlushBuffers[kFlushFlagList.indexOf(k)], callback as any);
        }
    }

    // Transform compat: 'finish' does not wait for the flush.
    _final(callback: (error?: Error | null) => void): void {
        callback();
    }

    close(callback?: () => void): void {
        if (callback) finished(this, callback);
        this.destroy();
    }

    _destroy(err: Error | null, callback: (error?: Error | null) => void): void {
        _close(this);
        callback(err);
    }

    _transform(chunk: any, encoding: BufferEncoding, cb: TransformCallback): void {
        let flushFlag = this._defaultFlushFlag;
        // A flush() write: its kind.
        const kind = flushKindOf(chunk);
        if (kind >= 0) flushFlag = kind;
        const data = chunk as Uint8Array;
        // For the last chunk, also apply `_finishFlushFlag`.
        if (this.writableEnded && this.writableLength === data.byteLength) {
            flushFlag = maxFlush(flushFlag, this._finishFlushFlag);
        }
        processChunk(this, data, flushFlag, cb);
    }

    _processChunk(chunk: Uint8Array, flushFlag: number, cb?: TransformCallback): Buffer | undefined {
        if (typeof cb === 'function') {
            processChunk(this, chunk, flushFlag, cb);
            return undefined;
        }
        return processChunkSync(this, chunk, flushFlag);
    }

    _read(size: number): void {
        const resume = this.pendingWrite;
        if (resume !== null) {
            this.pendingWrite = null;
            resume();
        }
        super._read(size);
    }

    holdWrite(resume: () => void): void {
        this.pendingWrite = resume;
    }
}

// The handle's onerror: an Error with zlib's message, errno and code.
function zlibOnError(self: ZlibBase): void {
    const id = self._handle!.id;
    const error: any = new Error(__kml_native.zlibErrorMessage(id));
    error.errno = __kml_native.zlibErrorErrno(id);
    error.code = __kml_native.zlibErrorCode(id);
    self.destroy(error);
    self.kError = error;
}

function processChunkSync(self: ZlibBase, chunk: Uint8Array, flushFlag: number): Buffer {
    let availInBefore = chunk.byteLength;
    let availOutBefore = self._chunkSize - self._outOffset;
    let inOff = 0;
    let availOutAfter = 0;
    let availInAfter = 0;
    const buffers: Buffer[] = [];
    let nread = 0;
    let inputRead = 0;
    const handle = self._handle!;
    let buffer = self._outBuffer;
    let offset = self._outOffset;
    const chunkSize = self._chunkSize;
    let error: Error | null = null;
    self.on('error', (er: Error) => {
        error = er;
    });
    while (true) {
        if (__kml_native.zlibWriteSync(handle.id, flushFlag, chunk, inOff, availInBefore, buffer, offset, availOutBefore) !== 0) {
            zlibOnError(self);
        }
        if (error) throw error;
        else if (self.kError) throw self.kError;
        availOutAfter = __kml_native.zlibState(handle.id, 0);
        availInAfter = __kml_native.zlibState(handle.id, 1);
        const inDelta = availInBefore - availInAfter;
        inputRead += inDelta;
        const have = availOutBefore - availOutAfter;
        if (have > 0) {
            const out = buffer.slice(offset, offset + have);
            offset += have;
            buffers.push(out);
            nread += out.byteLength;
            if (nread > self._maxOutputLength) {
                _close(self);
                throw bufferTooLarge(self._maxOutputLength);
            }
        }
        // Exhausted the output buffer, or used all the input: a new one.
        if (availOutAfter === 0 || offset >= chunkSize) {
            availOutBefore = chunkSize;
            offset = 0;
            buffer = Buffer.allocUnsafe(chunkSize);
        }
        if (availOutAfter === 0) {
            // Not done with this chunk: go around again with what is left.
            inOff += inDelta;
            availInBefore = availInAfter;
        } else {
            break;
        }
    }
    if (availInAfter > 0 && self._rejectGarbageAfterEnd) {
        _close(self);
        throw trailingJunk();
    }
    self.bytesWritten = inputRead;
    _close(self);
    if (nread === 0) return Buffer.alloc(0);
    return buffers.length === 1 ? buffers[0] : Buffer.concat(buffers, nread);
}

function nativeWrite(self: ZlibBase, handle: ZlibHandle, outLen: number): void {
    __kml_native.zlibWrite(handle.id, handle.flushFlag, handle.buffer!, handle.inOff, handle.availInBefore,
        self._outBuffer, self._outOffset, outLen, (failed: number, unused: number) => {
            if (failed !== 0 && !self.destroyed && self._handle) {
                zlibOnError(self);
                return;
            }
            processCallback(self, handle);
        });
}

function processChunk(self: ZlibBase, chunk: Uint8Array, flushFlag: number, cb: TransformCallback): void {
    const handle = self._handle;
    if (!handle) {
        process.nextTick(cb);
        return;
    }
    handle.buffer = chunk;
    handle.cb = cb;
    handle.availOutBefore = self._chunkSize - self._outOffset;
    handle.availInBefore = chunk.byteLength;
    handle.inOff = 0;
    handle.flushFlag = flushFlag;
    nativeWrite(self, handle, handle.availOutBefore);
}

function processCallback(self: ZlibBase, handle: ZlibHandle): void {
    // The stream may have been destroyed while the write was in flight.
    const cb = handle.cb!;
    if (self.destroyed) {
        handle.buffer = null;
        cb();
        return;
    }
    const availOutAfter = __kml_native.zlibState(handle.id, 0);
    const availInAfter = __kml_native.zlibState(handle.id, 1);
    const inDelta = handle.availInBefore - availInAfter;
    self.bytesWritten += inDelta;
    const have = handle.availOutBefore - availOutAfter;
    let streamBufferIsFull = false;
    if (have > 0) {
        const out = self._outBuffer.slice(self._outOffset, self._outOffset + have);
        self._outOffset += have;
        streamBufferIsFull = !self.push(out);
    }
    // push() may have destroyed the stream (an 'error' in a 'data' handler).
    if (self.destroyed) {
        cb();
        return;
    }
    if (availOutAfter === 0 || self._outOffset >= self._chunkSize) {
        handle.availOutBefore = self._chunkSize;
        self._outOffset = 0;
        self._outBuffer = Buffer.allocUnsafe(self._chunkSize);
    }
    if (availOutAfter === 0) {
        // Not done with the chunk: write again, once the reader wants more
        // when the readable side is full.
        handle.inOff += inDelta;
        handle.availInBefore = availInAfter;
        if (!streamBufferIsFull) {
            nativeWrite(self, handle, self._chunkSize);
        } else {
            self.holdWrite(() => {
                nativeWrite(self, handle, self._chunkSize);
            });
        }
        return;
    }
    if (availInAfter > 0) {
        // Input left over after the stream ended: the handle is done.
        if (self._rejectGarbageAfterEnd) {
            const err = trailingJunk();
            self.destroy(err);
            cb(err);
            return;
        }
        self.push(null);
    }
    // All done with this chunk.
    handle.buffer = null;
    cb();
}

function _close(engine: ZlibBase): void {
    if (engine._handle) __kml_native.zlibClose(engine._handle.id);
    engine._handle = null;
}

// A dictionary as the handle takes it.
function dictionaryBytes(dictionary: any): Uint8Array {
    if (dictionary === undefined) return emptyBuffer;
    if (isArrayBufferView(dictionary)) return viewBytes(dictionary);
    if (isAnyArrayBuffer(dictionary)) return Buffer.from(dictionary as ArrayBuffer);
    throw invalidArgType('options.dictionary', 'an instance of Buffer, TypedArray, DataView, or ArrayBuffer', dictionary);
}

class Zlib extends ZlibBase {
    _level: number;
    _strategy: number;
    _mode: number;

    constructor(opts: ZlibOptions | undefined, mode: number) {
        let windowBits = Z_DEFAULT_WINDOWBITS;
        let level = Z_DEFAULT_COMPRESSION;
        let memLevel = Z_DEFAULT_MEMLEVEL;
        let strategy = Z_DEFAULT_STRATEGY;
        let dictionary: Uint8Array = emptyBuffer;
        if (opts) {
            // windowBits 0 lets inflate read it from the header.
            if ((opts.windowBits == null || opts.windowBits === 0) &&
                (mode === INFLATE || mode === GUNZIP || mode === UNZIP)) {
                windowBits = 0;
            } else {
                // 8 is 9 for zlib's deflate, but gzip wants at least 9.
                const min = Z_MIN_WINDOWBITS + (mode === GZIP ? 1 : 0);
                windowBits = checkRangesOrGetDefault(opts.windowBits, 'options.windowBits', min, Z_MAX_WINDOWBITS, Z_DEFAULT_WINDOWBITS);
            }
            level = checkRangesOrGetDefault(opts.level, 'options.level', Z_MIN_LEVEL, Z_MAX_LEVEL, Z_DEFAULT_COMPRESSION);
            memLevel = checkRangesOrGetDefault(opts.memLevel, 'options.memLevel', Z_MIN_MEMLEVEL, Z_MAX_MEMLEVEL, Z_DEFAULT_MEMLEVEL);
            strategy = checkRangesOrGetDefault(opts.strategy, 'options.strategy', Z_DEFAULT_STRATEGY, Z_FIXED, Z_DEFAULT_STRATEGY);
            dictionary = dictionaryBytes(opts.dictionary);
        }
        const handle = new ZlibHandle(__kml_native.zlibNew(mode));
        __kml_native.zlibInit(handle.id, level, windowBits, memLevel, strategy, opts?.rejectGarbageAfterEnd === true ? 1 : 0, dictionary);
        super(opts, mode, handle, Z_NO_FLUSH, Z_FINISH, Z_FULL_FLUSH);
        this._level = level;
        this._strategy = strategy;
        this._mode = mode;
    }

    params(level: number, strategy: number, callback: () => void): void {
        checkRangesOrGetDefault(level, 'level', Z_MIN_LEVEL, Z_MAX_LEVEL);
        checkRangesOrGetDefault(strategy, 'strategy', Z_DEFAULT_STRATEGY, Z_FIXED);
        if (this._level !== level || this._strategy !== strategy) {
            this.flush(Z_SYNC_FLUSH, () => {
                if (!this._handle) throw new Error('zlib binding closed');
                if (__kml_native.zlibParams(this._handle.id, level, strategy) !== 0) zlibOnError(this);
                if (!this.destroyed) {
                    this._level = level;
                    this._strategy = strategy;
                    if (callback) callback();
                }
            });
        } else {
            process.nextTick(callback);
        }
    }
}

// kml:callable Deflate — Node's is a function that constructs when called without `new`
export class Deflate extends Zlib {
    constructor(opts?: ZlibOptions) { super(opts, DEFLATE); }
}

// kml:callable Inflate — Node's is a function that constructs when called without `new`
export class Inflate extends Zlib {
    constructor(opts?: ZlibOptions) { super(opts, INFLATE); }
}

// kml:callable Gzip — Node's is a function that constructs when called without `new`
export class Gzip extends Zlib {
    constructor(opts?: ZlibOptions) { super(opts, GZIP); }
}

// kml:callable Gunzip — Node's is a function that constructs when called without `new`
export class Gunzip extends Zlib {
    constructor(opts?: ZlibOptions) { super(opts, GUNZIP); }
}

// kml:callable DeflateRaw — Node's is a function that constructs when called without `new`
export class DeflateRaw extends Zlib {
    constructor(opts?: ZlibOptions) {
        if (opts && opts.windowBits === 8) opts.windowBits = 9;
        super(opts, DEFLATERAW);
    }
}

// kml:callable InflateRaw — Node's is a function that constructs when called without `new`
export class InflateRaw extends Zlib {
    constructor(opts?: ZlibOptions) { super(opts, INFLATERAW); }
}

// kml:callable Unzip — Node's is a function that constructs when called without `new`
export class Unzip extends Zlib {
    constructor(opts?: ZlibOptions) { super(opts, UNZIP); }
}

// ---- Brotli and Zstd (their handles: BrotliEncoder/Decoder, ZstdCompress/
// Decompress) ----

export interface BrotliOptions extends ZlibOptions {
    params?: { [key: number]: boolean | number } | undefined;
}

export interface ZstdOptions extends ZlibOptions {
    params?: { [key: number]: boolean | number } | undefined;
    pledgedSrcSize?: number | undefined;
}

function initFailed(): Error {
    return new NodeError('ERR_ZLIB_INITIALIZATION_FAILED', 'Initialization failed');
}

// The params of a Brotli or Zstd stream, applied to its fresh handle: a key
// past the family's range, or given twice, is its INVALID_PARAM error.
function applyParams(id: number, opts: any, maxParam: number, invalid: (key: string) => Error): void {
    if (!opts || !opts.params) return;
    const seen = new Set<number>();
    for (const origKey of Object.keys(opts.params)) {
        const key = +origKey;
        if (Number.isNaN(key) || key < 0 || key > maxParam || seen.has(key)) throw invalid(origKey);
        seen.add(key);
        const value = opts.params[origKey];
        if (typeof value !== 'number' && typeof value !== 'boolean') {
            throw invalidArgType('options.params[key]', 'of type number', value);
        }
        if (__kml_native.zlibSetParam(id, key, value === true ? 1 : value === false ? 0 : value as number) !== 0) {
            __kml_native.zlibClose(id);
            throw initFailed();
        }
    }
}

const kMaxBrotliParam = 8;
const kMaxZstdCParam = 402;
const kMaxZstdDParam = 100;

class Brotli extends ZlibBase {
    constructor(opts: BrotliOptions | undefined, mode: number) {
        const id = __kml_native.zlibNew(mode);
        applyParams(id, opts, kMaxBrotliParam, (key: string) =>
            new NodeRangeError('ERR_BROTLI_INVALID_PARAM', key + ' is not a valid Brotli parameter'));
        const dictionary = opts ? dictionaryBytes(opts.dictionary) : emptyBuffer;
        if (__kml_native.zlibInitOther(id, dictionary, -1) !== 0) {
            __kml_native.zlibClose(id);
            throw initFailed();
        }
        super(opts, mode, new ZlibHandle(id), 0, 2, 1);
    }
}

// kml:callable BrotliCompress — Node's is a function that constructs when called without `new`
export class BrotliCompress extends Brotli {
    constructor(opts?: BrotliOptions) { super(opts, BROTLI_ENCODE); }
}

// kml:callable BrotliDecompress — Node's is a function that constructs when called without `new`
export class BrotliDecompress extends Brotli {
    constructor(opts?: BrotliOptions) { super(opts, BROTLI_DECODE); }
}

class Zstd extends ZlibBase {
    constructor(opts: ZstdOptions | undefined, mode: number, maxParam: number) {
        const id = __kml_native.zlibNew(mode);
        applyParams(id, opts, maxParam, (key: string) =>
            new NodeRangeError('ERR_ZSTD_INVALID_PARAM', key + ' is not a valid zstd parameter'));
        const pledged: any = opts?.pledgedSrcSize;
        const dictionary = opts && opts.dictionary !== undefined && isArrayBufferView(opts.dictionary) ? viewBytes(opts.dictionary) : emptyBuffer;
        if (__kml_native.zlibInitOther(id, dictionary, typeof pledged === 'number' ? pledged as number : -1) !== 0) {
            __kml_native.zlibClose(id);
            throw initFailed();
        }
        super(opts, mode, new ZlibHandle(id), 0, 2, 1);
    }
}

export class ZstdCompress extends Zstd {
    constructor(opts?: ZstdOptions) { super(opts, ZSTD_COMPRESS, kMaxZstdCParam); }
}

export class ZstdDecompress extends Zstd {
    constructor(opts?: ZstdOptions) { super(opts, ZSTD_DECOMPRESS, kMaxZstdDParam); }
}

// The one-shot result with `info: true`: the output and the stream.
export interface ZlibInfoResult {
    buffer: Buffer;
    engine: any;
}

export type InputType = string | ArrayBuffer | NodeJS.ArrayBufferView;
export type CompressCallback = (error: Error | null, result: Buffer) => void;

// The one-shot forms: the engine is written once and its output gathered.
function zlibBuffer(engine: ZlibBase, buffer: any, callback: any): void {
    if (typeof callback !== 'function') throw invalidArgType('callback', 'of type function', callback);
    // Streams do not support non-Uint8Array views: a Buffer over the bytes.
    if (isArrayBufferView(buffer) && !isUint8Array(buffer)) {
        buffer = Buffer.from(viewBytes(buffer));
    } else if (isAnyArrayBuffer(buffer)) {
        buffer = Buffer.from(buffer as ArrayBuffer);
    }
    let buffers: Buffer[] | null = null;
    let nread = 0;
    const onEnd = (): void => {
        let buf: Buffer;
        if (nread === 0) buf = Buffer.alloc(0);
        else buf = buffers!.length === 1 ? buffers![0] : Buffer.concat(buffers!, nread);
        engine.close();
        if (engine._info) callback(null, { buffer: buf, engine: engine });
        else callback(null, buf);
    };
    engine.on('data', (chunk: Buffer) => {
        if (!buffers) buffers = [chunk];
        else buffers.push(chunk);
        nread += chunk.length;
        if (nread > engine._maxOutputLength) {
            engine.close();
            engine.removeAllListeners('end');
            callback(bufferTooLarge(engine._maxOutputLength));
        }
    });
    engine.on('error', (err: Error) => {
        engine.removeAllListeners('end');
        callback(err);
    });
    engine.on('end', onEnd);
    engine.end(buffer);
}

function zlibBufferSync(engine: ZlibBase, buffer: any): any {
    if (typeof buffer === 'string') {
        buffer = Buffer.from(buffer as string);
    } else if (!isArrayBufferView(buffer)) {
        if (isAnyArrayBuffer(buffer)) {
            buffer = Buffer.from(buffer as ArrayBuffer);
        } else {
            throw invalidArgType('buffer', 'of type string or an instance of Buffer, TypedArray, DataView, or ArrayBuffer', buffer);
        }
    } else if (!isUint8Array(buffer)) {
        buffer = viewBytes(buffer);
    }
    const out = processChunkSync(engine, buffer as Uint8Array, engine._finishFlushFlag);
    if (engine._info) return { buffer: out, engine: engine };
    return out;
}

function asyncArgs(opts: any, callback: any): [any, any] {
    if (typeof opts === 'function') return [{}, opts];
    return [opts, callback];
}

export function deflate(buf: InputType, callback: CompressCallback): void;
export function deflate(buf: InputType, options: ZlibOptions, callback: CompressCallback): void;
export function deflate(buf: any, opts: any, callback?: any): void {
    const a = asyncArgs(opts, callback);
    zlibBuffer(new Deflate(a[0]), buf, a[1]);
}
export function deflateSync(buf: InputType, options: ZlibOptions & { info: true }): ZlibInfoResult;
export function deflateSync(buf: InputType, options?: ZlibOptions): Buffer;
export function deflateSync(buf: any, options?: any): any {
    return zlibBufferSync(new Deflate(options), buf);
}
export function gzip(buf: InputType, callback: CompressCallback): void;
export function gzip(buf: InputType, options: ZlibOptions, callback: CompressCallback): void;
export function gzip(buf: any, opts: any, callback?: any): void {
    const a = asyncArgs(opts, callback);
    zlibBuffer(new Gzip(a[0]), buf, a[1]);
}
export function gzipSync(buf: InputType, options: ZlibOptions & { info: true }): ZlibInfoResult;
export function gzipSync(buf: InputType, options?: ZlibOptions): Buffer;
export function gzipSync(buf: any, options?: any): any {
    return zlibBufferSync(new Gzip(options), buf);
}
export function deflateRaw(buf: InputType, callback: CompressCallback): void;
export function deflateRaw(buf: InputType, options: ZlibOptions, callback: CompressCallback): void;
export function deflateRaw(buf: any, opts: any, callback?: any): void {
    const a = asyncArgs(opts, callback);
    zlibBuffer(new DeflateRaw(a[0]), buf, a[1]);
}
export function deflateRawSync(buf: InputType, options: ZlibOptions & { info: true }): ZlibInfoResult;
export function deflateRawSync(buf: InputType, options?: ZlibOptions): Buffer;
export function deflateRawSync(buf: any, options?: any): any {
    return zlibBufferSync(new DeflateRaw(options), buf);
}
export function unzip(buf: InputType, callback: CompressCallback): void;
export function unzip(buf: InputType, options: ZlibOptions, callback: CompressCallback): void;
export function unzip(buf: any, opts: any, callback?: any): void {
    const a = asyncArgs(opts, callback);
    zlibBuffer(new Unzip(a[0]), buf, a[1]);
}
export function unzipSync(buf: InputType, options: ZlibOptions & { info: true }): ZlibInfoResult;
export function unzipSync(buf: InputType, options?: ZlibOptions): Buffer;
export function unzipSync(buf: any, options?: any): any {
    return zlibBufferSync(new Unzip(options), buf);
}
export function inflate(buf: InputType, callback: CompressCallback): void;
export function inflate(buf: InputType, options: ZlibOptions, callback: CompressCallback): void;
export function inflate(buf: any, opts: any, callback?: any): void {
    const a = asyncArgs(opts, callback);
    zlibBuffer(new Inflate(a[0]), buf, a[1]);
}
export function inflateSync(buf: InputType, options: ZlibOptions & { info: true }): ZlibInfoResult;
export function inflateSync(buf: InputType, options?: ZlibOptions): Buffer;
export function inflateSync(buf: any, options?: any): any {
    return zlibBufferSync(new Inflate(options), buf);
}
export function gunzip(buf: InputType, callback: CompressCallback): void;
export function gunzip(buf: InputType, options: ZlibOptions, callback: CompressCallback): void;
export function gunzip(buf: any, opts: any, callback?: any): void {
    const a = asyncArgs(opts, callback);
    zlibBuffer(new Gunzip(a[0]), buf, a[1]);
}
export function gunzipSync(buf: InputType, options: ZlibOptions & { info: true }): ZlibInfoResult;
export function gunzipSync(buf: InputType, options?: ZlibOptions): Buffer;
export function gunzipSync(buf: any, options?: any): any {
    return zlibBufferSync(new Gunzip(options), buf);
}
export function inflateRaw(buf: InputType, callback: CompressCallback): void;
export function inflateRaw(buf: InputType, options: ZlibOptions, callback: CompressCallback): void;
export function inflateRaw(buf: any, opts: any, callback?: any): void {
    const a = asyncArgs(opts, callback);
    zlibBuffer(new InflateRaw(a[0]), buf, a[1]);
}
export function inflateRawSync(buf: InputType, options: ZlibOptions & { info: true }): ZlibInfoResult;
export function inflateRawSync(buf: InputType, options?: ZlibOptions): Buffer;
export function inflateRawSync(buf: any, options?: any): any {
    return zlibBufferSync(new InflateRaw(options), buf);
}

export function brotliCompress(buf: InputType, callback: CompressCallback): void;
export function brotliCompress(buf: InputType, options: BrotliOptions, callback: CompressCallback): void;
export function brotliCompress(buf: any, opts: any, callback?: any): void {
    const a = asyncArgs(opts, callback);
    zlibBuffer(new BrotliCompress(a[0]), buf, a[1]);
}
export function brotliCompressSync(buf: InputType, options: BrotliOptions & { info: true }): ZlibInfoResult;
export function brotliCompressSync(buf: InputType, options?: BrotliOptions): Buffer;
export function brotliCompressSync(buf: any, options?: any): any {
    return zlibBufferSync(new BrotliCompress(options), buf);
}
export function brotliDecompress(buf: InputType, callback: CompressCallback): void;
export function brotliDecompress(buf: InputType, options: BrotliOptions, callback: CompressCallback): void;
export function brotliDecompress(buf: any, opts: any, callback?: any): void {
    const a = asyncArgs(opts, callback);
    zlibBuffer(new BrotliDecompress(a[0]), buf, a[1]);
}
export function brotliDecompressSync(buf: InputType, options: BrotliOptions & { info: true }): ZlibInfoResult;
export function brotliDecompressSync(buf: InputType, options?: BrotliOptions): Buffer;
export function brotliDecompressSync(buf: any, options?: any): any {
    return zlibBufferSync(new BrotliDecompress(options), buf);
}
export function zstdCompress(buf: InputType, callback: CompressCallback): void;
export function zstdCompress(buf: InputType, options: ZstdOptions, callback: CompressCallback): void;
export function zstdCompress(buf: any, opts: any, callback?: any): void {
    const a = asyncArgs(opts, callback);
    zlibBuffer(new ZstdCompress(a[0]), buf, a[1]);
}
export function zstdCompressSync(buf: InputType, options: ZstdOptions & { info: true }): ZlibInfoResult;
export function zstdCompressSync(buf: InputType, options?: ZstdOptions): Buffer;
export function zstdCompressSync(buf: any, options?: any): any {
    return zlibBufferSync(new ZstdCompress(options), buf);
}
export function zstdDecompress(buf: InputType, callback: CompressCallback): void;
export function zstdDecompress(buf: InputType, options: ZstdOptions, callback: CompressCallback): void;
export function zstdDecompress(buf: any, opts: any, callback?: any): void {
    const a = asyncArgs(opts, callback);
    zlibBuffer(new ZstdDecompress(a[0]), buf, a[1]);
}
export function zstdDecompressSync(buf: InputType, options: ZstdOptions & { info: true }): ZlibInfoResult;
export function zstdDecompressSync(buf: InputType, options?: ZstdOptions): Buffer;
export function zstdDecompressSync(buf: any, options?: any): any {
    return zlibBufferSync(new ZstdDecompress(options), buf);
}

export function createBrotliCompress(options?: BrotliOptions): BrotliCompress { return new BrotliCompress(options); }
export function createBrotliDecompress(options?: BrotliOptions): BrotliDecompress { return new BrotliDecompress(options); }
export function createZstdCompress(options?: ZstdOptions): ZstdCompress { return new ZstdCompress(options); }
export function createZstdDecompress(options?: ZstdOptions): ZstdDecompress { return new ZstdDecompress(options); }
export function createDeflate(options?: ZlibOptions): Deflate { return new Deflate(options); }
export function createInflate(options?: ZlibOptions): Inflate { return new Inflate(options); }
export function createDeflateRaw(options?: ZlibOptions): DeflateRaw { return new DeflateRaw(options); }
export function createInflateRaw(options?: ZlibOptions): InflateRaw { return new InflateRaw(options); }
export function createGzip(options?: ZlibOptions): Gzip { return new Gzip(options); }
export function createGunzip(options?: ZlibOptions): Gunzip { return new Gunzip(options); }
export function createUnzip(options?: ZlibOptions): Unzip { return new Unzip(options); }

export function crc32(data: string | NodeJS.ArrayBufferView, value: number = 0): number {
    let bytes: Uint8Array;
    if (typeof data === 'string') {
        bytes = Buffer.from(data as string);
    } else if (isArrayBufferView(data)) {
        bytes = viewBytes(data);
    } else {
        throw invalidArgType('data', 'of type string or an instance of Buffer, TypedArray, or DataView', data);
    }
    if (typeof value !== 'number') throw invalidArgType('value', 'of type number', value);
    if (!Number.isInteger(value)) throw outOfRange('value', 'an integer', value);
    if (value < 0 || value > 4294967295) throw outOfRange('value', '>= 0 && <= 4294967295', value);
    return __kml_native.zlibCrc32(bytes, value);
}
