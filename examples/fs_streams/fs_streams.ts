// Streaming file I/O — fs.createReadStream / fs.createWriteStream.
//
// A WriteStream appends each write to a file and emits 'finish' then 'close';
// a ReadStream yields the file's contents as Buffer chunks, consumable with
// `for await`, `.on('data')`/`.on('end')`, or `.pipe()` into a WriteStream.

import fs from "fs";
import type { Stream } from "stream";

function closed(stream: Stream): Promise<void> {
  return new Promise<void>((resolve) => { stream.on("close", () => resolve()); });
}

async function main(): Promise<void> {
  // Write a file through a stream.
  const ws = fs.createWriteStream("__stream_demo.txt");
  ws.write("first line\n");
  ws.write(Buffer.from("second line\n"));
  ws.end("third line\n");
  await closed(ws);
  console.log("bytes written:", ws.bytesWritten);

  // Read it back with for-await: Buffer chunks.
  let viaForAwait = 0;
  for await (const chunk of fs.createReadStream("__stream_demo.txt")) {
    viaForAwait += (chunk as Buffer).length;
  }
  console.log("for-await read bytes:", viaForAwait);

  // Read it back with the event API, decoded as UTF-8.
  let viaEvents = "";
  const rs = fs.createReadStream("__stream_demo.txt", { encoding: "utf8" });
  rs.on("data", (chunk: string) => { viaEvents += chunk; });
  rs.on("end", () => { console.log("first line via events:", viaEvents.split("\n")[0]); });
  await closed(rs);

  // Pipe one file into another.
  const copy = fs.createWriteStream("__stream_copy.txt");
  fs.createReadStream("__stream_demo.txt").pipe(copy);
  await closed(copy);
  console.log("copied length:", fs.readFileSync("__stream_copy.txt", "utf8").length);

  fs.unlinkSync("__stream_demo.txt");
  fs.unlinkSync("__stream_copy.txt");
}

main();
