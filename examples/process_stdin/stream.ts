// Streaming process.stdin — a classic Unix pipe/filter: read stdin as it
// arrives, transform each chunk, and count the total.
//
//   printf 'hello\nworld\n' | ./stream
//
// 'data' fires once per read chunk (a UTF-8 string); 'end' fires on EOF.

let bytes = 0;
let lines = 0;

process.stdin.on("data", (chunk: string) => {
  // Uppercase and echo straight through.
  process.stdout.write(chunk.toUpperCase());
  bytes += chunk.length;
  for (let i = 0; i < chunk.length; i++) {
    if (chunk[i] === "\n") lines++;
  }
});

process.stdin.on("end", () => {
  console.error("---");
  console.error("bytes: " + bytes + ", newlines: " + lines);
});
