// Asynchronous fs — the callback form and the Promise form (fs/promises).
//
// Both run their I/O on the thread pool. Two independent chains of pool
// operations finish in no fixed order (in Node too), so the Promise demo
// starts once the callback demo is done.

import fs from "fs";

// --- Promise form: fs.promises.* with async/await ---
async function promiseDemo(): Promise<void> {
  await fs.promises.writeFile("__demo_async.txt", "written via fs.promises");
  const text: string = await fs.promises.readFile("__demo_async.txt", "utf8");
  console.log("promises read:", text);

  const entries: string[] = await fs.promises.readdir(".");
  let count = 0;
  for (const e of entries) {
    if (e === "__demo_async.txt") count++;
  }
  console.log("promises readdir found the file:", count === 1);

  try {
    await fs.promises.readFile("__does_not_exist_async.txt");
  } catch (e) {
    console.log("promises rejection caught for a missing file");
  }

  await fs.promises.unlink("__demo_async.txt");
  console.log("promises cleanup done");
}

// --- Callback form: fs.readFile(path, encoding, (err, data) => ...) ---
function callbackDemo(done: () => void): void {
  fs.writeFile("__demo_cb.txt", "written via callback", (err) => {
    if (err) {
      console.log("callback write error");
      return;
    }
    fs.readFile("__demo_cb.txt", "utf8", (err2, data) => {
      if (err2) {
        console.log("callback read error");
        return;
      }
      console.log("callback read:", data);
      fs.unlink("__demo_cb.txt", (err3) => {
        console.log(err3 ? "callback cleanup error" : "callback cleanup done");
        done();
      });
    });
  });
}

callbackDemo(() => {
  promiseDemo();
});
