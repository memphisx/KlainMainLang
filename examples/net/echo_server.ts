import net from 'net';

// A TCP echo server: every chunk received on a connection is written back.
// Self-terminates after a short delay so this example runs to completion
// (a real server would simply omit the timer and listen indefinitely).
const server = net.createServer((socket) => {
  socket.on('data', (chunk: Uint8Array) => {
    socket.write(chunk);
  });
  socket.on('end', () => {
    console.log('client disconnected');
  });
});

server.listen(9099, () => {
  console.log('TCP echo server listening on 9099');
});

setTimeout(() => {
  server.close();
  console.log('server closed');
}, 100);
