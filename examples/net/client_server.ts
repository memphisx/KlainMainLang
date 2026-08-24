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

server.listen(9109, () => {
  const client = net.connect(9109, "127.0.0.1", () => {
    client.write("hello");
  });
  client.on('data', (chunk: Uint8Array) => {
    console.log("client received:", dec.decode(chunk));
    client.end();
    server.close();
  });
});
