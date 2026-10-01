// Node's `fs` (lib/fs.js): internal_fs.ts's operations and streams, and
// fs.promises (internal_fs_promises.ts).
// kml:default-namespace — `import fs from 'fs'` reads this module's exports.

export * from './internal_fs';
import {
    access as pAccess, appendFile as pAppendFile, chmod as pChmod, chown as pChown, copyFile as pCopyFile,
    lchown as pLchown, lutimes as pLutimes, link as pLink, lstat as pLstat, mkdir as pMkdir, mkdtemp as pMkdtemp,
    open as pOpen, readdir as pReaddir, readFile as pReadFile, readlink as pReadlink, realpath as pRealpath,
    rename as pRename, rm as pRm, rmdir as pRmdir, stat as pStat, statfs as pStatfs, symlink as pSymlink,
    truncate as pTruncate, unlink as pUnlink, utimes as pUtimes, writeFile as pWriteFile, constants as pConstants,
} from './internal_fs_promises';

// fs.promises: lib/internal/fs/promises.js's exports.
export const promises = {
    access: pAccess,
    appendFile: pAppendFile,
    chmod: pChmod,
    chown: pChown,
    copyFile: pCopyFile,
    lchown: pLchown,
    lutimes: pLutimes,
    link: pLink,
    lstat: pLstat,
    mkdir: pMkdir,
    mkdtemp: pMkdtemp,
    open: pOpen,
    readdir: pReaddir,
    readFile: pReadFile,
    readlink: pReadlink,
    realpath: pRealpath,
    rename: pRename,
    rm: pRm,
    rmdir: pRmdir,
    stat: pStat,
    statfs: pStatfs,
    symlink: pSymlink,
    truncate: pTruncate,
    unlink: pUnlink,
    utimes: pUtimes,
    writeFile: pWriteFile,
    constants: pConstants,
};
