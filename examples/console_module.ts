// A Console writing somewhere other than stdout: here, into an array.
import { Console } from 'console';
import { Writable } from 'stream';

const lines: string[] = [];
const sink = new Writable({ write(chunk, _enc, done) { lines.push(String(chunk)); done(); } });
const logger = new Console({ stdout: sink, groupIndentation: 4 });
logger.group('Thessaloniki');
logger.log('population %d', 1_000_000);
logger.groupEnd();
console.log(lines.join(''));
