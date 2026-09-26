import net from 'net';

// net.connect is asynchronous: it returns a socket immediately and never blocks
// or throws. A connection that cannot be established surfaces through the
// socket's 'error' event with a Node-shaped coded Error, followed by 'close' —
// exactly as Node reports ECONNREFUSED / ENOTFOUND. (ADR-01021)

// Port 1 on loopback refuses the connection.
const sock = net.connect(1, "127.0.0.1");
console.log("connect() returned synchronously; connecting in the background");

sock.on('connect', () => {
  console.log("connected (unexpected for a refused port)");
});

// A system error: NodeJS.ErrnoException plus the address it failed on.
interface ConnectError extends NodeJS.ErrnoException {
  address?: string;
  port?: number;
}

sock.on('error', (err: ConnectError) => {
  // err carries Node's full connect-error surface.
  console.log("error.code:", err.code);          // ECONNREFUSED
  console.log("error.syscall:", err.syscall);     // connect
  console.log("error.message:", err.message);     // connect ECONNREFUSED 127.0.0.1:1
  console.log("error.address:", err.address, "error.port:", err.port);
});

sock.on('close', () => {
  console.log("socket closed after the failed connect");
});
