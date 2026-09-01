import dgram from 'dgram';

// A UDP echo server: each datagram is echoed back to its sender. Self-closes
// after a short delay so this example terminates (a real server would keep the
// socket open).
const dec = new TextDecoder();
const server = dgram.createSocket('udp4');

server.on('message', (msg: Uint8Array, rinfo) => {
  console.log('got:', dec.decode(msg), 'from port', rinfo.port);
  server.send('reply:' + dec.decode(msg), rinfo.port, rinfo.address);
});

server.bind(9199);
console.log('UDP echo server bound to 9199');

// setBroadcast enables SO_BROADCAST so datagrams can target a broadcast
// address; .address() reports the real bound { address, family, port }.
server.setBroadcast(true);
console.log('bound family:', server.address().family);

setTimeout(() => {
  server.close();
  console.log('server closed');
}, 100);
