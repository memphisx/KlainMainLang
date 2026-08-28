// diagnostics_channel (ADR-00420): named pub/sub channels for in-process
// instrumentation. Messages are strings in V1 — serialize structures with
// JSON.stringify.
import dc from 'diagnostics_channel';

const requests = dc.channel('app:request');

dc.subscribe('app:request', (message: string, name: string) => {
  console.log("[" + name + "]", message);
});

// (A Channel handle isn't module-global-promotable yet, so instrument via a
// closure rather than a named top-level function.)
const handle = (path: string): void => {
  if (requests.hasSubscribers) {
    requests.publish(JSON.stringify({ path: path, at: Date.now() }));
  }
};

handle("/kalimera");
handle("/kosme");
