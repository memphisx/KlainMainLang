// tls.connect — a TLS client. Opens a TLS connection, sends a raw HTTP/1.1
// request, and prints the status line of the response.
//
// (Hits a real host, so it's excluded from `make examples`' offline run — the
//  deterministic coverage is tests/tls_test.go against a local TLS fixture.)
//
// The handshake is blocking (like net.connect); certificate verification is on
// by default (rejectUnauthorized), with SNI sent for the host. After connect,
// the socket behaves like a net socket — .write() / .on('data') / .on('end').

import tls from "tls";

const sock = tls.connect(443, "example.com", () => {
  sock.write(
    "GET / HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n",
  );
});

let response = "";
sock.on("data", (chunk: string) => {
  response += chunk;
});
sock.on("end", () => {
  console.log("status:", response.split("\r\n")[0]);
});
