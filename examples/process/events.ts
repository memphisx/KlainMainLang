// process is an EventEmitter: any event name, once/off/emit, signal
// listeners (the first starts a watcher, removing the last restores the
// default), emitWarning's 'warning' event and the 'exit' event.
import { once } from 'events';

process.on('exit', (code) => {
  console.log(`exit event, code ${code}`);
});

process.on('warning', (warning) => {
  console.log(`warning event: ${warning.name} ${warning.message}`);
});
process.emitWarning('the cache is cold', 'CacheWarning');

process.on('job', (id: number, name: string) => {
  console.log(`job ${id}: ${name}`);
});
console.log('emitted:', process.emit('job', 1, 'thessaloniki'), process.emit('nobody-listens'));
console.log('events:', process.eventNames().join(', '));

async function main(): Promise<void> {
  if (process.platform === 'win32') return;
  // A signal watcher does not keep the process alive; this timer does.
  const keepAlive = setTimeout(() => {}, 1000);
  setTimeout(() => process.kill(process.pid, 'SIGUSR2'), 10);
  const args = await once(process, 'SIGUSR2');
  console.log(`caught ${args[0]}; listeners left: ${process.listenerCount('SIGUSR2')}`);
  clearTimeout(keepAlive);
}

main();
