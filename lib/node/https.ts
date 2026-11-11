// Node's lib/https.js (internal_https.ts).
// kml:default-namespace — `import https from 'https'` reads this module's
// exports.

export { Agent, Server, createServer, get, globalAgent, request } from './internal_https';
export type { AgentOptions, RequestOptions, ServerOptions } from './internal_https';
