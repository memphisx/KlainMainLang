// Node's `assert/strict` (lib/assert/strict.js): assert.strict, whose
// equal is strictEqual, deepEqual deepStrictEqual, and so on.

export {
    strict as ok, fail, strictEqual as equal, notStrictEqual as notEqual,
    deepStrictEqual as deepEqual, notDeepStrictEqual as notDeepEqual,
    deepStrictEqual, notDeepStrictEqual, strictEqual, notStrictEqual,
    partialDeepStrictEqual, match, doesNotMatch, throws, rejects,
    doesNotThrow, doesNotReject, ifError, AssertionError, strict,
} from './assert';
import { strict } from './assert';

export default strict;
