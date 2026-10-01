// Node's lib/internal/fs/promises.js: fs/promises' functions, each the
// callback operation's request settling a promise, and open()'s FileHandle.
import {
    access as accessCb, appendFile as appendFileCb, chmod as chmodCb, chown as chownCb, copyFile as copyFileCb,
    lchown as lchownCb, lutimes as lutimesCb, link as linkCb, lstat as lstatCb, mkdir as mkdirCb,
    mkdtemp as mkdtempCb, readdir as readdirCb, readFile as readFileCb, readlink as readlinkCb,
    realpath as realpathCb, rename as renameCb, rm as rmCb, rmdir as rmdirCb, stat as statCb,
    statfs as statfsCb, symlink as symlinkCb, truncate as truncateCb, unlink as unlinkCb, utimes as utimesCb,
    writeFile as writeFileCb, Stats, Dirent, constants, _kmlOpenFileHandle, _kmlFileHandle,
} from './internal_fs';
import type {
    PathLike, TimeLike, Mode, ObjectEncodingOptions, EncodingOption, WriteFileOptions, RmOptions,
    RmDirOptions, MakeDirectoryOptions, StatOptions, StatFsOptions,
} from './internal_fs_utils';
import { StatFs } from './internal_fs_utils';

export { constants } from './internal_fs';
export type FileHandle = _kmlFileHandle;

// A callback operation as a promise: resolve with its result, reject with
// its error.
function settle<T>(run: (cb: (err: any, value?: any) => void) => void): Promise<T> {
    return new Promise<T>((resolve, reject) => {
        run((err: any, value?: any) => {
            if (err) reject(err);
            else resolve(value as T);
        });
    });
}

export function open(path: PathLike, flags?: string | number, mode?: Mode): Promise<_kmlFileHandle> {
    return _kmlOpenFileHandle(path, flags, mode);
}

export function access(path: PathLike, mode?: number): Promise<void> {
    return settle<void>((cb) => { accessCb(path, mode, cb); });
}

export function readFile(path: PathLike | _kmlFileHandle, options?: { encoding?: null | undefined; flag?: string | undefined } | null): Promise<NonSharedBuffer>;
export function readFile(path: PathLike | _kmlFileHandle, options: { encoding: BufferEncoding; flag?: string | undefined } | BufferEncoding): Promise<string>;
export function readFile(path: PathLike | _kmlFileHandle, options?: (ObjectEncodingOptions & { flag?: string | undefined }) | BufferEncoding | null): Promise<string | NonSharedBuffer>;
export function readFile(path: any, options?: any): Promise<any> {
    if (path instanceof _kmlFileHandle) return (path as _kmlFileHandle).readFile(options);
    return settle<any>((cb) => { readFileCb(path, options, cb); });
}

export function writeFile(file: PathLike | _kmlFileHandle, data: string | NodeJS.ArrayBufferView, options?: WriteFileOptions): Promise<void>;
export function writeFile(file: any, data: any, options?: any): Promise<void> {
    if (file instanceof _kmlFileHandle) return (file as _kmlFileHandle).writeFile(data, options);
    return settle<void>((cb) => { writeFileCb(file, data, options, cb); });
}

export function appendFile(path: PathLike | _kmlFileHandle, data: string | NodeJS.ArrayBufferView, options?: WriteFileOptions): Promise<void>;
export function appendFile(path: any, data: any, options?: any): Promise<void> {
    if (path instanceof _kmlFileHandle) return (path as _kmlFileHandle).appendFile(data, options);
    return settle<void>((cb) => { appendFileCb(path, data, options, cb); });
}

export function unlink(path: PathLike): Promise<void> {
    return settle<void>((cb) => { unlinkCb(path, cb); });
}

export function mkdir(path: PathLike, options: MakeDirectoryOptions & { recursive: true }): Promise<string | undefined>;
export function mkdir(path: PathLike, options?: Mode | (MakeDirectoryOptions & { recursive?: false | undefined }) | null): Promise<void>;
export function mkdir(path: PathLike, options?: Mode | MakeDirectoryOptions | null): Promise<string | undefined>;
export function mkdir(path: any, options?: any): Promise<any> {
    return settle<any>((cb) => { mkdirCb(path, options, cb); });
}

export function rmdir(path: PathLike, options?: RmDirOptions): Promise<void> {
    return settle<void>((cb) => { rmdirCb(path, options ?? {}, cb); });
}

export function rm(path: PathLike, options?: RmOptions): Promise<void> {
    return settle<void>((cb) => { rmCb(path, options ?? {}, cb); });
}

export function rename(oldPath: PathLike, newPath: PathLike): Promise<void> {
    return settle<void>((cb) => { renameCb(oldPath, newPath, cb); });
}

export function copyFile(src: PathLike, dest: PathLike, mode?: number): Promise<void> {
    return settle<void>((cb) => { copyFileCb(src, dest, mode ?? 0, cb); });
}

export function readdir(path: PathLike, options?: (ObjectEncodingOptions & { withFileTypes?: false | undefined; recursive?: boolean | undefined }) | BufferEncoding | null): Promise<string[]>;
export function readdir(path: PathLike, options: { encoding: 'buffer'; withFileTypes?: false | undefined; recursive?: boolean | undefined } | 'buffer'): Promise<NonSharedBuffer[]>;
export function readdir(path: PathLike, options: ObjectEncodingOptions & { withFileTypes: true; recursive?: boolean | undefined }): Promise<Dirent[]>;
export function readdir(path: any, options?: any): Promise<any> {
    return settle<any>((cb) => { readdirCb(path, options, cb); });
}

export function stat(path: PathLike, opts?: StatOptions & { bigint?: false | undefined }): Promise<Stats> {
    return settle<Stats>((cb) => { statCb(path, opts, cb); });
}

export function lstat(path: PathLike, opts?: StatOptions & { bigint?: false | undefined }): Promise<Stats> {
    return settle<Stats>((cb) => { lstatCb(path, opts, cb); });
}

export function statfs(path: PathLike, opts?: StatFsOptions & { bigint?: false | undefined }): Promise<StatFs> {
    return settle<StatFs>((cb) => { statfsCb(path, opts, cb); });
}

export function utimes(path: PathLike, atime: TimeLike, mtime: TimeLike): Promise<void> {
    return settle<void>((cb) => { utimesCb(path, atime, mtime, cb); });
}

export function lutimes(path: PathLike, atime: TimeLike, mtime: TimeLike): Promise<void> {
    return settle<void>((cb) => { lutimesCb(path, atime, mtime, cb); });
}

export function realpath(path: PathLike, options?: EncodingOption): Promise<string> {
    // fs/promises' realpath is the native one (uv_fs_realpath).
    return settle<string>((cb) => { realpathCb.native(path, options, cb); });
}

export function mkdtemp(prefix: PathLike, options?: EncodingOption): Promise<string> {
    return settle<string>((cb) => { mkdtempCb(prefix, options, cb); });
}

export function readlink(path: PathLike, options?: EncodingOption): Promise<string> {
    return settle<string>((cb) => { readlinkCb(path, options, cb); });
}

export function link(existingPath: PathLike, newPath: PathLike): Promise<void> {
    return settle<void>((cb) => { linkCb(existingPath, newPath, cb); });
}

export function symlink(target: PathLike, path: PathLike, type?: 'dir' | 'file' | 'junction' | null): Promise<void> {
    return settle<void>((cb) => { symlinkCb(target, path, type, cb); });
}

export function chmod(path: PathLike, mode: Mode): Promise<void> {
    return settle<void>((cb) => { chmodCb(path, mode, cb); });
}

export function chown(path: PathLike, uid: number, gid: number): Promise<void> {
    return settle<void>((cb) => { chownCb(path, uid, gid, cb); });
}

export function lchown(path: PathLike, uid: number, gid: number): Promise<void> {
    return settle<void>((cb) => { lchownCb(path, uid, gid, cb); });
}

export function truncate(path: PathLike, len?: number): Promise<void> {
    return settle<void>((cb) => { truncateCb(path, len ?? 0, cb); });
}
