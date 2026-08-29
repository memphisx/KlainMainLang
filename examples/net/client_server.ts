import net from 'net';

// A self-contained TCP round trip: a server echoes one message, and a client
// connects to it, sends a line, prints the echo, then tears both down so the
// example terminates (a real client/server would live longer).
const dec = new TextDecoder();

const server = net.createServer((conn) => {
  conn.on('data', (chunk: Uint8Array) => {
    conn.write("echo:" + dec.decode(chunk));
  });
});

// Address-family helpers (net.isIP → 0/4/6).
console.log("isIP:", net.isIP("127.0.0.1"), net.isIP("::1"), net.isIP("host"));

// listen(0) lets the OS pick a free port; server.address() reads it back —
// the idiomatic Node pattern, and collision-free versus a hardcoded port.
server.listen(0, () => {
  const port = server.address().port;
  // net.connect also takes an options object { port, host }.
  const client = net.connect({ port: port, host: "127.0.0.1" }, () => {
    client.setNoDelay(true);
    client.write("hello");
  });
  client.on('data', (chunk: Uint8Array) => {
    console.log("client received:", dec.decode(chunk));
    client.end();
    server.close();
  });
  // 'close' fires once on teardown, after 'end' — Node's ordering.
  client.on('close', () => {
    console.log("client closed");
  });
});
