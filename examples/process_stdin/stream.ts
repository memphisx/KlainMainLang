// Streaming process.stdin — a classic Unix pipe/filter: read stdin as it
// arrives, transform each chunk, and count the total.
//
//   printf 'hello\nworld\n' | ./stream
//
// setEncoding('utf8') is the idiomatic opener (chunks are UTF-8 strings), and
// the stream methods return the stream, so the listeners chain. 'data' fires
// once per read chunk; 'end' fires on EOF.

let bytes = 0;
let lines = 0;

process.stdin
  .setEncoding("utf8")
  .on("data", (chunk: string) => {
    // Uppercase and echo straight through.
    process.stdout.write(chunk.toUpperCase());
    bytes += chunk.length;
    for (let i = 0; i < chunk.length; i++) {
      if (chunk[i] === "\n") lines++;
    }
  })
  .on("end", () => {
    console.error("---");
    console.error("bytes: " + bytes + ", newlines: " + lines);
  });
