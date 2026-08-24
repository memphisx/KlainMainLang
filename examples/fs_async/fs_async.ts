// Asynchronous fs — the callback form and the Promise form (fs/promises).
//
// Under the hood the I/O is still the blocking syscall (this compiler has no
// thread pool); the async shape is what matters — existing callback- and
// Promise/await-style code compiles and runs unchanged.

import fs from "fs";

// --- Promise form: fs.promises.* with async/await ---
async function promiseDemo(): Promise<void> {
  await fs.promises.writeFile("__demo_async.txt", "written via fs.promises");
  const text: string = await fs.promises.readFile("__demo_async.txt");
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

// --- Callback form: fs.readFile(path, (err, data) => ...) ---
function callbackDemo(): void {
  fs.writeFile("__demo_cb.txt", "written via callback", (err) => {
    if (err) {
      console.log("callback write error");
      return;
    }
    fs.readFile("__demo_cb.txt", (err2, data: string) => {
      if (err2) {
        console.log("callback read error");
        return;
      }
      console.log("callback read:", data);
      fs.unlink("__demo_cb.txt", (err3) => {
        console.log(err3 ? "callback cleanup error" : "callback cleanup done");
      });
    });
  });
}

callbackDemo();
promiseDemo();
