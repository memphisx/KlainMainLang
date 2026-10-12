// klain:tty — the synchronous keyboard reads Node has no counterpart for
// (Node reads raw input through process.stdin's 'data' events). Raw mode,
// the terminal size and resize are Node's own: process.stdin.setRawMode,
// process.stdout.columns/rows and 'SIGWINCH'.

// One byte off stdin, blocking; -1 at end of input.
export function readByte(): number {
    return __kml_native.ttyReadByte();
}

// One keystroke off stdin: a multi-byte key (an arrow key's ESC [ A) is one
// string. With timeoutMs, waits at most that long and returns "" when no key
// arrives; "" at end of input.
export function readKey(timeoutMs?: number): string {
    return __kml_native.ttyReadKey(timeoutMs === undefined ? -1 : timeoutMs);
}
