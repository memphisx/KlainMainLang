// The target of dynamic_import.ts's import(): its top-level runs on the
// first import(), once, and it shares node:events with its importer.
import { EventEmitter } from 'node:events';

console.log('report: evaluating');
const pause = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
await pause(10);

export const maxListeners: number = EventEmitter.defaultMaxListeners;
export const city: string = 'Thessaloniki';
console.log('report: ready');
