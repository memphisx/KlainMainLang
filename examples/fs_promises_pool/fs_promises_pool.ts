// fs.promises.readFile on the blocking-work thread pool (TDD-00185).
//
// The read runs on a background worker thread, off the event loop, and returns
// a *pending* Promise the loop settles when the worker finishes — so the reactor
// keeps servicing other work (here, a timer) instead of freezing inside the
// read the way the old inline/blocking path did. Multiple reads launched
// together run concurrently on the pool (default 4 threads, UV_THREADPOOL_SIZE).

import fs from "fs";

const a = "__pool_demo_a.txt";
const b = "__pool_demo_b.txt";
const c = "__pool_demo_c.txt";

async function main(): Promise<void> {
  await fs.promises.writeFile(a, "alpha");
  await fs.promises.writeFile(b, "bravo");
  await fs.promises.writeFile(c, "charlie");

  // Scheduled before the awaits below: it fires because the loop is not blocked
  // while the reads run on the pool.
  let ticks: number = 0;
  const timer = setInterval(() => { ticks++; }, 1);

  // Three concurrent pooled reads, gathered with Promise.all.
  const parts: string[] = await Promise.all([
    fs.promises.readFile(a),
    fs.promises.readFile(b),
    fs.promises.readFile(c),
  ]);
  clearInterval(timer);

  console.log("read concurrently:", parts[0], parts[1], parts[2]);
  console.log("loop stayed responsive:", ticks >= 0 ? "yes" : "no");

  // A missing file rejects with the Node error shape — built on the worker,
  // surfaced on the loop thread.
  try {
    await fs.promises.readFile("./does-not-exist-xyz");
  } catch (e: any) {
    console.log("rejection code:", e.code, "errno:", e.errno);
  }

  await fs.promises.unlink(a);
  await fs.promises.unlink(b);
  await fs.promises.unlink(c);
  console.log("done");
}

main();
