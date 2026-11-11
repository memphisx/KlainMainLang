// kml:global
// CountQueuingStrategy and ByteLengthQueuingStrategy — globals — as Node
// has them: the high-water mark init gives, and the size a stream asks of
// each chunk (one per chunk; a view's byte length). A program that names
// either without declaring it imports this module.

import { NodeTypeError } from './internal_errors';
import { received } from './internal_fs_utils';

function readInit(init: any): number {
    if (init === null || typeof init !== 'object') {
        throw NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "init" argument must be of type object. ' + received(init));
    }
    if (init.highWaterMark === undefined) {
        throw NodeTypeError('ERR_MISSING_OPTION', 'init.highWaterMark is required');
    }
    return Number(init.highWaterMark);
}

function countSize(chunk?: any): number {
    return 1;
}

function byteLengthSize(chunk: any): number {
    return chunk.byteLength;
}

export class CountQueuingStrategy {
    #highWaterMark: number;
    constructor(init: any) {
        this.#highWaterMark = readInit(init);
    }
    get highWaterMark(): number {
        return this.#highWaterMark;
    }
    get size(): (chunk?: any) => number {
        return countSize;
    }
}

export class ByteLengthQueuingStrategy {
    #highWaterMark: number;
    constructor(init: any) {
        this.#highWaterMark = readInit(init);
    }
    get highWaterMark(): number {
        return this.#highWaterMark;
    }
    get size(): (chunk: any) => number {
        return byteLengthSize;
    }
}
