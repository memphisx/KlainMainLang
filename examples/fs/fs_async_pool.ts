// Non-blocking fs on the thread pool (TDD-00185): binary writes and the
// classic callback form both run off the event-loop thread now.
//
// Two things this demonstrates:
//   1. A binary body (a Uint8Array, embedded NUL and all) writes out whole via
//      fs.promises.writeFile / appendFile — pooled, not inline.
//   2. The callback form fs.readFile(path, cb) is genuinely non-blocking: the
//      code after the call runs before the callback, and a 0 ms timer
//      scheduled alongside it may fire first or not (the read's pool round
//      trips race the timer, in Node too).
import fs from 'fs';

const path = '/tmp/klain_fs_async_pool_example.bin';

async function main(): Promise<void> {
  // 1. Binary write + append, then read back. "AB\0CD" then "EF" → 7 bytes.
  await fs.promises.writeFile(path, new Uint8Array([65, 66, 0, 67, 68]));
  await fs.promises.appendFile(path, new Uint8Array([69, 70]));
  const back: string = await fs.promises.readFile(path, "utf8");
  console.log('bytes written: ' + back.length);

  // 2. Callback form, non-blocking: the caller carries on first.
  let after: boolean = false;
  setTimeout(() => {}, 0);
  fs.readFile(path, "utf8", (err, data) => {
    console.log('callback read: ' + data.length + ' bytes');
    console.log('read was non-blocking: ' + after);
    fs.unlink(path, (e) => { console.log('cleaned up'); });
  });
  after = true;
}

main();
