// Node's lib/internal/assert/assertion_error.js.

import { colors, stderrColumns, stderrIsTTY } from './internal_util_colors';
import { myersDiff, printMyersDiff, printSimpleMyersDiff } from './internal_assert_myers_diff';

function readableOperator(operator: string): string | undefined {
    switch (operator) {
        case 'deepStrictEqual': return 'Expected values to be strictly deep-equal:';
        case 'partialDeepStrictEqual': return 'Expected values to be partially and strictly deep-equal:';
        case 'strictEqual': return 'Expected values to be strictly equal:';
        case 'strictEqualObject': return 'Expected "actual" to be reference-equal to "expected":';
        case 'deepEqual': return 'Expected values to be loosely deep-equal:';
        case 'notDeepStrictEqual': return 'Expected "actual" not to be strictly deep-equal to:';
        case 'notStrictEqual': return 'Expected "actual" to be strictly unequal to:';
        case 'notStrictEqualObject': return 'Expected "actual" not to be reference-equal to "expected":';
        case 'notDeepEqual': return 'Expected "actual" not to be loosely deep-equal to:';
        case 'notIdentical': return 'Values have same structure but are not reference-equal:';
        case 'notDeepEqualUnequal': return 'Expected values not to be loosely deep-equal:';
    }
    return undefined;
}

const kMaxShortStringLength = 12;
const kMaxLongStringLength = 512;

function hasCustomMessageDiff(operator: string): boolean {
    return operator === 'deepStrictEqual' || operator === 'strictEqual' || operator === 'partialDeepStrictEqual';
}

// util.inspect(val) with its defaults.
export function inspect(val: unknown): string {
    return __kml_native.inspectWith(val, 2, 3, 0, 80, 100);
}

function inspectValue(val: unknown): string {
    // The util.inspect default values could be changed. This makes sure the
    // error messages contain the necessary information nevertheless.
    return __kml_native.inspectWith(val, 1000, 0, 1, 80, Infinity);
}

// Node copies two compared errors without their stacks (copyError), so each
// shows as `[Name: message]`, its own enumerable properties after.
function inspectErrorCopy(err: any): string {
    // `MyError [Error]` when the constructor's name is not the error's name.
    const ctor = __kml_native.errorConstructorName(err);
    const name = ctor !== '' && ctor !== err.name ? `${ctor} [${err.name}]` : err.name;
    const base = err.message ? `[${name}: ${err.message}]` : `[${name}]`;
    const keys = Object.keys(err);
    if (keys.length === 0) {
        return base;
    }
    const own: any = {};
    for (const k of keys) {
        own[k] = err[k];
    }
    return `${base} ${inspectValue(own)}`;
}

// inspectValue, or inspectErrorCopy when both compared values are errors.
function inspectCompared(val: unknown, bothErrors: boolean): string {
    return bothErrors ? inspectErrorCopy(val) : inspectValue(val);
}

function getErrorMessage(operator: string, message: any): string {
    return message || readableOperator(operator);
}

function checkOperator(actual: any, expected: any, operator: string): string {
    // In case both values are objects or functions explicitly mark them as not
    // reference equal for the `strictEqual` operator.
    if (operator === 'strictEqual' &&
        ((typeof actual === 'object' && actual !== null && typeof expected === 'object' && expected !== null) ||
            (typeof actual === 'function' && typeof expected === 'function'))) {
        return 'strictEqualObject';
    }
    return operator;
}

class DiffResult {
    message: string;
    header: string | undefined;
    skipped: boolean;

    constructor(message: string, header: string | undefined, skipped: boolean) {
        this.message = message;
        this.header = header;
        this.skipped = skipped;
    }
}

function getColoredMyersDiff(actual: string, expected: string): DiffResult {
    const header = `${colors.green}actual${colors.white} ${colors.red}expected${colors.white}`;
    const diff = myersDiff(actual.split(''), expected.split(''));
    const message = printSimpleMyersDiff(diff);
    return new DiffResult(message, header, false);
}

function getStackedDiff(actual: string, expected: string, isStringComparison: boolean): DiffResult {
    let message = `\n${colors.green}+${colors.white} ${actual}\n${colors.red}- ${colors.white}${expected}`;
    const stringsLen = actual.length + expected.length;
    const maxTerminalLength = stderrIsTTY() ? stderrColumns() : 80;
    const showIndicator = isStringComparison && (stringsLen <= maxTerminalLength);

    if (showIndicator) {
        let indicatorIdx = -1;
        for (let i = 0; i < actual.length; i++) {
            if (actual[i] !== expected[i]) {
                // Skip the indicator for the first 2 characters because the diff is immediately apparent
                // It is 3 instead of 2 to account for the quotes
                if (i >= 3) {
                    indicatorIdx = i;
                }
                break;
            }
        }
        if (indicatorIdx !== -1) {
            message += `\n${' '.repeat(indicatorIdx + 2)}^`;
        }
    }

    return new DiffResult(message, undefined, false);
}

function getSimpleDiff(originalActual: any, actual: string, originalExpected: any, expected: string): DiffResult {
    let stringsLen = actual.length + expected.length;
    // Accounting for the quotes wrapping strings
    if (typeof originalActual === 'string') {
        stringsLen -= 2;
    }
    if (typeof originalExpected === 'string') {
        stringsLen -= 2;
    }
    if (stringsLen <= kMaxShortStringLength && (originalActual !== 0 || originalExpected !== 0)) {
        return new DiffResult(`${actual} !== ${expected}`, '', false);
    }

    const isStringComparison = typeof originalActual === 'string' && typeof originalExpected === 'string';
    // colored myers diff
    if (isStringComparison && colors.hasColors) {
        return getColoredMyersDiff(actual, expected);
    }

    return getStackedDiff(actual, expected, isStringComparison);
}

function isSimpleDiff(actual: any, inspectedActual: string[], expected: any, inspectedExpected: string[]): boolean {
    if (inspectedActual.length > 1 || inspectedExpected.length > 1) {
        return false;
    }
    return typeof actual !== 'object' || actual === null || typeof expected !== 'object' || expected === null;
}

function createErrDiff(actual: any, expected: any, operatorArg: string, customMessage: any, diffType: string): string {
    let operator = checkOperator(actual, expected, operatorArg);

    let skipped = false;
    let message = '';
    const bothErrors = actual instanceof Error && expected instanceof Error;
    const inspectedActual = inspectCompared(actual, bothErrors);
    const inspectedExpected = inspectCompared(expected, bothErrors);
    const inspectedSplitActual = inspectedActual.split('\n');
    const inspectedSplitExpected = inspectedExpected.split('\n');
    const showSimpleDiff = isSimpleDiff(actual, inspectedSplitActual, expected, inspectedSplitExpected);
    let header = `${colors.green}+ actual${colors.white} ${colors.red}- expected${colors.white}`;

    if (showSimpleDiff) {
        const simpleDiff = getSimpleDiff(actual, inspectedSplitActual[0], expected, inspectedSplitExpected[0]);
        message = simpleDiff.message;
        if (simpleDiff.header !== undefined) {
            header = simpleDiff.header;
        }
        if (simpleDiff.skipped) {
            skipped = true;
        }
    } else if (inspectedActual === inspectedExpected) {
        // Handles the case where the objects are structurally the same but different references
        operator = 'notIdentical';
        if (inspectedSplitActual.length > 50 && diffType !== 'full') {
            message = `${inspectedSplitActual.slice(0, 50).join('\n')}\n...}`;
            skipped = true;
        } else {
            message = inspectedSplitActual.join('\n');
        }
        header = '';
    } else {
        const checkCommaDisparity = actual !== null && actual !== undefined && typeof actual === 'object';
        const diff = myersDiff(inspectedSplitActual, inspectedSplitExpected, checkCommaDisparity);

        const myersDiffMessage = printMyersDiff(diff, operator);
        message = myersDiffMessage.message;

        if (operator === 'partialDeepStrictEqual') {
            header = `${colors.gray}${colors.hasColors ? '' : '+ '}actual${colors.white} ${colors.red}- expected${colors.white}`;
        }

        if (myersDiffMessage.skipped) {
            skipped = true;
        }
    }

    const headerMessage = `${getErrorMessage(operator, customMessage)}\n${header}`;
    const skippedMessage = skipped ? '\n... Skipped lines' : '';

    return `${headerMessage}${skippedMessage}\n${message}\n`;
}

function buildMessage(message: any, actualArg: any, expectedArg: any, operator: string, diff: string): string {
    const actual = actualArg;
    const expected = expectedArg;
    if (message !== null && message !== undefined) {
        if (hasCustomMessageDiff(operator)) {
            return createErrDiff(actual, expected, operator, message, diff);
        }
        return String(message);
    }
    // Reset colors on each call to make sure we handle dynamically set environment
    // variables correct.
    colors.refresh();

    if (hasCustomMessageDiff(operator)) {
        return createErrDiff(actual, expected, operator, message, diff);
    }
    if (operator === 'notDeepStrictEqual' || operator === 'notStrictEqual') {
        // In case the objects are equal but the operator requires unequal, show
        // the first object and say A equals B
        let base = readableOperator(operator);
        const res = inspectValue(actual).split('\n');

        // In case "actual" is an object or a function, it should not be
        // reference equal.
        if (operator === 'notStrictEqual' &&
            ((typeof actual === 'object' && actual !== null) || typeof actual === 'function')) {
            base = readableOperator('notStrictEqualObject');
        }

        // Only remove lines in case it makes sense to collapse those.
        if (res.length > 50 && diff !== 'full') {
            res[46] = `${colors.blue}...${colors.white}`;
            while (res.length > 47) {
                res.pop();
            }
        }

        // Only print a single input.
        if (res.length === 1) {
            return `${base}${res[0].length > 5 ? '\n\n' : ' '}${res[0]}`;
        }
        return `${base}\n\n${res.join('\n')}\n`;
    }
    const bothErrors = actual instanceof Error && expected instanceof Error;
    let res = inspectCompared(actual, bothErrors);
    let other = inspectCompared(expected, bothErrors);
    const knownOperator = readableOperator(operator);
    if (operator === 'notDeepEqual' && res === other) {
        res = `${knownOperator}\n\n${res}`;
        if (res.length > 1024 && diff !== 'full') {
            res = `${res.slice(0, 1021)}...`;
        }
        return res;
    }
    if (res.length > kMaxLongStringLength && diff !== 'full') {
        res = `${res.slice(0, 509)}...`;
    }
    if (other.length > kMaxLongStringLength && diff !== 'full') {
        other = `${other.slice(0, 509)}...`;
    }
    if (operator === 'deepEqual') {
        res = `${knownOperator}\n\n${res}\n\nshould loosely deep-equal\n\n`;
    } else {
        const newOp = readableOperator(`${operator}Unequal`);
        if (newOp) {
            res = `${newOp}\n\n${res}\n\nshould not loosely deep-equal\n\n`;
        } else {
            other = ` ${operator} ${other}`;
        }
    }
    return `${res}${other}`;
}

export interface AssertionErrorOptions {
    message?: string | Error | undefined;
    actual?: unknown;
    expected?: unknown;
    operator?: string | undefined;
    stackStartFn?: Function | undefined;
    diff?: 'simple' | 'full' | undefined;
}

export class AssertionError extends Error {
    generatedMessage: boolean;
    code: string;
    actual: unknown;
    expected: unknown;
    operator: string;
    diff: 'simple' | 'full';

    constructor(options: AssertionErrorOptions) {
        const o: any = options;
        if (o === null || typeof o !== 'object') {
            throw new TypeError('The "options" argument must be of type object. Received ' + inspect(o));
        }
        const diff: 'simple' | 'full' = o.diff === undefined ? 'simple' : o.diff;
        const operator: string = o.operator;
        super(buildMessage(o.message, o.actual, o.expected, operator, diff));
        this.generatedMessage = !o.message;
        this.code = 'ERR_ASSERTION';
        this.actual = o.actual;
        this.expected = o.expected;
        this.operator = operator;
        this.name = 'AssertionError';
        this.diff = diff;
    }

    toString(): string {
        return `${this.name} [${this.code}]: ${this.message}`;
    }
}
