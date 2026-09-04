// fs — reading a directory as a file throws EISDIR (ADR-00692).
//
// fopen(2) on a directory succeeds on both Linux and macOS, then hands back
// garbage/zero bytes. Node instead throws
//   EISDIR: illegal operation on a directory, read
// so readFileSync guards its read path with a stat(2) check up front: a
// directory path sets errno to EISDIR and surfaces as an ordinary catchable
// Error whose `.code` is the Node error-code name "EISDIR" — the same shape
// every other fs failure (ENOENT, EEXIST, ...) already uses.

import * as fs from 'fs';

fs.mkdirSync('/tmp/klain-eisdir-demo', { recursive: true });

try {
  const contents = fs.readFileSync('/tmp/klain-eisdir-demo');
  console.log('unexpected: read ' + contents.length + ' bytes');
} catch (e) {
  // .code is the canonical fs idiom — what real code branches on.
  console.log('code: ' + (e as any).code);
  console.log('is Error: ' + (e instanceof Error));
}

fs.rmdirSync('/tmp/klain-eisdir-demo');
