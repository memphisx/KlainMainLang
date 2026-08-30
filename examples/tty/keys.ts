// Terminal UI primitives (TDD-00031): raw single-key input, live terminal
// size, and resize notifications.
//
//   ./keys          # run it in a real terminal and press keys; 'q' quits
//   ./keys </dev/null   # non-TTY: prints the size and exits cleanly
//
// The Node-faithful surface hangs off `process` exactly where Node puts it
// (setRawMode, stdout.columns/.rows, the 'SIGWINCH' resize signal); the one
// primitive Node has no counterpart for — a synchronous single-keystroke read —
// comes from the bespoke `klain:tty` module.

import { readKey } from "klain:tty";

console.log(
  "terminal: " +
    process.stdout.columns +
    "x" +
    process.stdout.rows +
    (process.stdin.isTTY ? " (interactive)" : " (not a tty)"),
);

// Piped/redirected: no keyboard to read, so report and leave. Every raw-mode
// program should guard on isTTY before touching raw mode, exactly as here.
if (!process.stdin.isTTY) {
  console.log("stdin is not a terminal — nothing to read.");
} else {
  // Print the new size whenever the window is resized.
  process.on("SIGWINCH", () => {
    console.log("\r\nresized to " + process.stdout.columns + "x" + process.stdout.rows);
  });

  // Raw mode: keystrokes arrive one at a time, Ctrl-C no longer sends SIGINT
  // (it comes through as the raw byte 3), and nothing is echoed automatically.
  process.stdin.setRawMode(true);
  console.log("Press keys (q or Ctrl-C to quit):\r");

  let running = true;
  while (running) {
    const key: string = readKey();
    const code: number = key.length > 0 ? key.charCodeAt(0) : -1;

    if (code === -1 || code === 113 || code === 3) {
      // EOF, 'q', or Ctrl-C.
      running = false;
    } else if (key === "\x1b[A") {
      process.stdout.write("up\r\n");
    } else if (key === "\x1b[B") {
      process.stdout.write("down\r\n");
    } else if (key === "\x1b[C") {
      process.stdout.write("right\r\n");
    } else if (key === "\x1b[D") {
      process.stdout.write("left\r\n");
    } else if (code >= 32 && code < 127) {
      process.stdout.write("key '" + key + "' (" + code + ")\r\n");
    } else {
      process.stdout.write("byte " + code + "\r\n");
    }
  }

  // Always restore the terminal before exiting, or the shell is left in raw
  // mode and the user has to blind-type `reset`.
  process.stdin.setRawMode(false);
  console.log("bye");
}
