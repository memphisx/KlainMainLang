import dns from 'dns';

// dns.resolve4 returns every A record; dns.promises.lookup is the Promise form
// of a single lookup.
dns.resolve4("localhost", (err, addresses) => {
  if (err === null) {
    console.log("resolve4 localhost ->", addresses.join(", "));
  }
});

async function main() {
  const r = await dns.promises.lookup("localhost");
  console.log("promises.lookup ->", r.address, "(family", r.family, ")");
}
main();
