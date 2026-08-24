import dns from 'dns';

dns.lookup("localhost", (err, address, family) => {
  if (err !== null) {
    console.log("error");
  } else {
    console.log("localhost ->", address, "family", family);
  }
});

dns.lookup("no.such.host.invalid.example", (err, address, family) => {
  console.log("err set:", err !== null);
});
