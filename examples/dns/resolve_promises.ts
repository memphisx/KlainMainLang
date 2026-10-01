import dns from 'dns';
import { lookup } from 'dns/promises';

// resolve4 asks a DNS server, as Node's c-ares does (not the hosts file), so
// `localhost` is ENOTFOUND there; lookup goes through getaddrinfo and finds it.
dns.resolve4("localhost", (err, addresses) => {
  console.log("resolve4 localhost ->", err ? err.code : addresses.join(", "));
});

async function main() {
  const one = await lookup("localhost", { family: 4 });
  console.log("promises.lookup ->", one.address, "(family", one.family, ")");
  const all = await dns.promises.lookup("localhost", { all: true });
  console.log("all addresses:", all.length > 0);
}
main();

// A Resolver with its own servers and retry policy.
const resolver = new dns.Resolver({ timeout: 2000, tries: 2 });
resolver.setServers(["1.1.1.1", "[2606:4700:4700::1111]:53"]);
console.log(resolver.getServers());
