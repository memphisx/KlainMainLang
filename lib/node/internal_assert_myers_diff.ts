// Node's lib/internal/assert/myers_diff.js: the line diff an AssertionError
// message shows.

import { colors } from './internal_util_colors';

const kNopLinesToCollapse = 5;
const kDelete = -1;
const kNop = 0;
const kInsert = 1;

export type DiffEntry = [number, string];

function areLinesEqual(actual: string, expected: string, checkCommaDisparity: boolean): boolean {
    if (actual === expected) {
        return true;
    }
    if (checkCommaDisparity) {
        return (actual + ',') === expected || actual === (expected + ',');
    }
    return false;
}

export function myersDiff(actual: string[], expected: string[], checkCommaDisparity: boolean = false): DiffEntry[] {
    const actualLength = actual.length;
    const expectedLength = expected.length;
    const max = actualLength + expectedLength;
    const v = new Int32Array(2 * max + 1);
    const trace: Int32Array[] = [];

    for (let diffLevel = 0; diffLevel <= max; diffLevel++) {
        trace.push(new Int32Array(v));
        for (let diagonalIndex = -diffLevel; diagonalIndex <= diffLevel; diagonalIndex += 2) {
            const offset = diagonalIndex + max;
            const previousOffset = v[offset - 1];
            const nextOffset = v[offset + 1];
            let x = diagonalIndex === -diffLevel || (diagonalIndex !== diffLevel && previousOffset < nextOffset) ?
                nextOffset :
                previousOffset + 1;
            let y = x - diagonalIndex;

            while (x < actualLength && y < expectedLength && areLinesEqual(actual[x], expected[y], checkCommaDisparity)) {
                x++;
                y++;
            }

            v[offset] = x;

            if (x >= actualLength && y >= expectedLength) {
                return backtrack(trace, actual, expected, checkCommaDisparity);
            }
        }
    }
    return [];
}

function backtrack(trace: Int32Array[], actual: string[], expected: string[], checkCommaDisparity: boolean): DiffEntry[] {
    const actualLength = actual.length;
    const expectedLength = expected.length;
    const max = actualLength + expectedLength;

    let x = actualLength;
    let y = expectedLength;
    const result: DiffEntry[] = [];

    for (let diffLevel = trace.length - 1; diffLevel >= 0; diffLevel--) {
        const v = trace[diffLevel];
        const diagonalIndex = x - y;
        const offset = diagonalIndex + max;

        let prevDiagonalIndex: number;
        if (diagonalIndex === -diffLevel || (diagonalIndex !== diffLevel && v[offset - 1] < v[offset + 1])) {
            prevDiagonalIndex = diagonalIndex + 1;
        } else {
            prevDiagonalIndex = diagonalIndex - 1;
        }

        const prevX = v[prevDiagonalIndex + max];
        const prevY = prevX - prevDiagonalIndex;

        while (x > prevX && y > prevY) {
            const actualItem = actual[x - 1];
            const value = checkCommaDisparity && !actualItem.endsWith(',') ? expected[y - 1] : actualItem;
            result.push([kNop, value]);
            x--;
            y--;
        }

        if (diffLevel > 0) {
            if (x > prevX) {
                x--;
                result.push([kInsert, actual[x]]);
            } else {
                y--;
                result.push([kDelete, expected[y]]);
            }
        }
    }

    return result;
}

export function printSimpleMyersDiff(diff: DiffEntry[]): string {
    let message = '';
    for (let diffIdx = diff.length - 1; diffIdx >= 0; diffIdx--) {
        const operation = diff[diffIdx][0];
        const value = diff[diffIdx][1];
        let color = colors.white;
        if (operation === kInsert) {
            color = colors.green;
        } else if (operation === kDelete) {
            color = colors.red;
        }
        message += `${color}${value}${colors.white}`;
    }
    return `\n${message}`;
}

export function printMyersDiff(diff: DiffEntry[], operator: string): { message: string, skipped: boolean } {
    let message = '';
    let skipped = false;
    let nopCount = 0;

    for (let diffIdx = diff.length - 1; diffIdx >= 0; diffIdx--) {
        const operation = diff[diffIdx][0];
        const value = diff[diffIdx][1];
        const previousOperation = diffIdx < diff.length - 1 ? diff[diffIdx + 1][0] : -2;

        // Avoid grouping if only one line would have been grouped otherwise
        if (previousOperation === kNop && operation !== previousOperation) {
            if (nopCount === kNopLinesToCollapse + 1) {
                message += `${colors.white}  ${diff[diffIdx + 1][1]}\n`;
            } else if (nopCount === kNopLinesToCollapse + 2) {
                message += `${colors.white}  ${diff[diffIdx + 2][1]}\n`;
                message += `${colors.white}  ${diff[diffIdx + 1][1]}\n`;
            } else if (nopCount >= kNopLinesToCollapse + 3) {
                message += `${colors.blue}...${colors.white}\n`;
                message += `${colors.white}  ${diff[diffIdx + 1][1]}\n`;
                skipped = true;
            }
            nopCount = 0;
        }

        if (operation === kInsert) {
            if (operator === 'partialDeepStrictEqual') {
                message += `${colors.gray}${colors.hasColors ? ' ' : '+'} ${value}${colors.white}\n`;
            } else {
                message += `${colors.green}+${colors.white} ${value}\n`;
            }
        } else if (operation === kDelete) {
            message += `${colors.red}-${colors.white} ${value}\n`;
        } else if (operation === kNop) {
            if (nopCount < kNopLinesToCollapse) {
                message += `${colors.white}  ${value}\n`;
            }
            nopCount++;
        }
    }

    message = message.trimEnd();

    return { message: `\n${message}`, skipped };
}
