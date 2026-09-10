// Backpressured file streaming (TDD-00186): fs.createReadStream reads on the
// thread pool one chunk per consumer demand, so a large file streams with a
// bounded memory profile instead of being read to EOF up front. Abandoning the
// stream early (a for-await break) stops the read cleanly — the worker is
// credit-gated and the loop only stays alive while a read is actually in flight.
import fs from 'fs';

const path = '/tmp/klain_read_stream_bp_example.txt';

async function main(): Promise<void> {
  // Build a multi-chunk file.
  let big: string = '';
  for (let i = 0; i < 5000; i++) { big += ('0000' + i).slice(-4) + ':'; }
  fs.writeFileSync(path, big);

  // Full consume: every byte arrives, in order, across many pooled reads.
  let total: number = 0;
  let chunks: number = 0;
  const rs = fs.createReadStream(path, { highWaterMark: 1024 });
  for await (const c of rs) { total += c.length; chunks++; }
  console.log('streamed ' + total + ' bytes in ' + chunks + ' chunks');

  // Early abandon: break after the first chunk — the pooled read stops, no hang.
  let seen: number = 0;
  const rs2 = fs.createReadStream(path, { highWaterMark: 1024 });
  for await (const c of rs2) { seen++; break; }
  console.log('abandoned after ' + seen + ' chunk');

  fs.unlinkSync(path);
  console.log('done');
}

main();
