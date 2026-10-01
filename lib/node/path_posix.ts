// Node's `path/posix`: `path.posix`.
//
// kml:default-namespace — `import path from 'path/posix'` reads this
// module's exports.

import { posix as flavour } from './path';
import type { ParsedPath, FormatInputPathObject } from './path';

export function normalize(path: string): string {
    return flavour.normalize(path);
}

export function join(...paths: string[]): string {
    return flavour.join(...paths);
}

export function resolve(...paths: string[]): string {
    return flavour.resolve(...paths);
}

export function isAbsolute(path: string): boolean {
    return flavour.isAbsolute(path);
}

export function relative(from: string, to: string): string {
    return flavour.relative(from, to);
}

export function dirname(path: string): string {
    return flavour.dirname(path);
}

export function basename(path: string, suffix?: string): string {
    return flavour.basename(path, suffix);
}

export function extname(path: string): string {
    return flavour.extname(path);
}

export function parse(path: string): ParsedPath {
    return flavour.parse(path);
}

export function format(pathObject: FormatInputPathObject): string {
    return flavour.format(pathObject);
}

export function toNamespacedPath(path: string): string {
    return flavour.toNamespacedPath(path);
}

export const sep = flavour.sep;
export const delimiter = flavour.delimiter;
export { posix, win32 } from './path';
