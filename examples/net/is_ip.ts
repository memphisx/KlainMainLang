// net.isIP / isIPv4 / isIPv6 — Node-strict dotted-decimal: leading-zero
// octets are rejected, matching Node (not the platform inet_pton).
import net from 'net';

console.log(net.isIPv4('192.168.0.1'));   // true
console.log(net.isIPv4('001.002.003.004')); // false — leading zeros
console.log(net.isIPv6('::1'));           // true
console.log(net.isIP('8.8.8.8'));         // 4
console.log(net.isIP('nope'));            // 0
