import dgram from 'dgram';

// A UDP echo server: each datagram is echoed back to its sender. A client
// sends one datagram, reads the echo, and both sockets close so this example
// terminates (a real server would keep its socket open).
const server = dgram.createSocket('udp4');

server.on('message', (msg, rinfo) => {
  console.log('server got:', msg.toString(), 'from', rinfo.family);
  server.send('reply:' + msg.toString(), rinfo.port, rinfo.address);
});

server.bind(0, '127.0.0.1', () => {
  // The socket exists once bound: setBroadcast enables SO_BROADCAST so
  // datagrams can target a broadcast address; .address() reports the real
  // bound { address, family, port }.
  server.setBroadcast(true);
  const { family, port } = server.address();
  console.log('UDP echo server bound,', family);

  const client = dgram.createSocket('udp4');
  client.on('message', (msg) => {
    console.log('client got:', msg.toString());
    client.close();
    server.close(() => console.log('server closed'));
  });
  client.send('kalimera', port, '127.0.0.1');
});
