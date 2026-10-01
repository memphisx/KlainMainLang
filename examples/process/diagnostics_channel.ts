// diagnostics_channel: named pub/sub channels for in-process
// instrumentation, and tracing channels that publish around a call.
import dc from 'diagnostics_channel';

const requests = dc.channel('app:request');

dc.subscribe('app:request', (message: unknown, name: string | symbol) => {
  console.log(`[${String(name)}]`, message);
});

function handle(path: string): void {
  if (requests.hasSubscribers) {
    requests.publish({ path, at: 'now' });
  }
}

handle('/kalimera');
handle('/kosme');

// A tracing channel: start/end around a call, error when it throws.
const lookups = dc.tracingChannel('app:lookup');
lookups.subscribe({
  start: (ctx: any) => console.log('lookup start', ctx.key),
  end: (ctx: any) => console.log('lookup end', ctx.key, '->', ctx.result),
});
const cities: Record<string, number> = { thessaloniki: 1, athens: 2 };
const id = lookups.traceSync((key: string) => cities[key], { key: 'thessaloniki' }, undefined, 'thessaloniki');
console.log('id', id);
