// Node's lib/internal/util/colors.js: the escape codes assertion messages
// use, empty unless stderr is a terminal that shows colours.

import { getColorDepth } from './internal_tty';

class Colors {
    blue = '';
    green = '';
    white = '';
    yellow = '';
    red = '';
    gray = '';
    clear = '';
    reset = '';
    hasColors = false;

    // Whether a terminal stream shows colours (stderr's, here).
    shouldColorize(isTTY: boolean): boolean {
        if (process.env.FORCE_COLOR !== undefined) {
            return getColorDepth() > 2;
        }
        return isTTY && getColorDepth() > 2;
    }

    refresh(): void {
        if (stderrIsTTY()) {
            const hasColors = this.shouldColorize(true);
            this.blue = hasColors ? '\u001b[34m' : '';
            this.green = hasColors ? '\u001b[32m' : '';
            this.white = hasColors ? '\u001b[39m' : '';
            this.yellow = hasColors ? '\u001b[33m' : '';
            this.red = hasColors ? '\u001b[31m' : '';
            this.gray = hasColors ? '\u001b[90m' : '';
            this.clear = hasColors ? '\u001bc' : '';
            this.reset = hasColors ? '\u001b[0m' : '';
            this.hasColors = hasColors;
        }
    }
}

// process.stderr.isTTY, without making process.stderr.
export function stderrIsTTY(): boolean {
    return __kml_native.guessHandleType(2) === 'TTY';
}

// process.stderr.columns, when stderr is a terminal.
export function stderrColumns(): number {
    const size = __kml_native.ttyWindowSize(2);
    return size < 0 ? 80 : Math.floor(size / 65536);
}

export const colors = new Colors();
colors.refresh();
