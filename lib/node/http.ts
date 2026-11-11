// Node's lib/http.js: lib/internal/http's exports (internal_http.ts).
// kml:default-namespace — `import http from 'http'` reads this module's
// exports.

export {
    Agent, ClientRequest, IncomingMessage, METHODS, OutgoingMessage, STATUS_CODES, Server,
    ServerResponse, createServer, get, globalAgent, maxHeaderSize, request,
    validateHeaderName, validateHeaderValue,
} from './internal_http';
export type {
    AgentOptions, IncomingHttpHeaders, InformationEvent, OutgoingHttpHeader, OutgoingHttpHeaders,
    RequestListener, RequestOptions, ServerOptions,
} from './internal_http';
