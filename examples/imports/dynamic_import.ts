// Dynamic import(): the target is compiled in, but its top-level runs only
// on the first import(), as a task on the program's event loop, and every
// module it imports is the program's own instance (Node's semantics; see
// -dynamic-import for the lazy and isolated alternatives).
import { EventEmitter } from 'node:events';

EventEmitter.defaultMaxListeners = 5;
console.log('main: before import');

const first = import('./dynamic_report');
const second = import('./dynamic_report');
console.log('main: import() returned, nothing evaluated yet');

const report = await first;
const again = await second;
console.log('main: report from', report.city, 'sees maxListeners', report.maxListeners);
console.log('main: same module evaluated once:', again.city === report.city);

if (process.argv.length > 99) {
  await import('./dynamic_never');
}
console.log('main: done');
