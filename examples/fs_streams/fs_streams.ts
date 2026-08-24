// Streaming file I/O — fs.createReadStream / fs.createWriteStream.
//
// createWriteStream returns a Node Writable whose .write()/.end() append to a
// file; createReadStream returns a Node Readable that yields the file's contents
// in chunks, consumable with `for await` or `.on('data')`/`.on('end')`. Chunks
// are strings (this fs is text-first), so a read→write pipe round-trips.

import fs from "fs";

async function main(): Promise<void> {
  // Write a file through a stream.
  const ws = fs.createWriteStream("__stream_demo.txt");
  ws.write("first line\n");
  ws.write("second line\n");
  ws.end("third line\n");
  await new Promise<void>((r) => setTimeout(() => r(), 10));

  // Read it back with for-await.
  let viaForAwait = 0;
  for await (const chunk of fs.createReadStream("__stream_demo.txt")) {
    viaForAwait += chunk.length;
  }
  console.log("for-await read bytes:", viaForAwait);

  // Read it back with the event API.
  let viaEvents = "";
  const rs = fs.createReadStream("__stream_demo.txt");
  rs.on("data", (chunk: string) => {
    viaEvents += chunk;
  });
  rs.on("end", () => {
    console.log("first line via events:", viaEvents.split("\n")[0]);
  });
  await new Promise<void>((r) => setTimeout(() => r(), 10));

  // Pipe one file into another.
  fs.createReadStream("__stream_demo.txt").pipe(
    fs.createWriteStream("__stream_copy.txt"),
  );
  await new Promise<void>((r) => setTimeout(() => r(), 20));
  console.log("copied length:", fs.readFileSync("__stream_copy.txt").length);

  fs.unlinkSync("__stream_demo.txt");
  fs.unlinkSync("__stream_copy.txt");
}

main();
