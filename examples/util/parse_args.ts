// util.parseArgs — a command line's options and positionals, as Node parses
// them. With no `args`, it reads process.argv after the script.
import { parseArgs } from 'util';

const options = {
  verbose: { type: 'boolean' as const, short: 'v' },
  port: { type: 'string' as const, short: 'p', default: '8080' },
  tag: { type: 'string' as const, multiple: true },
};

// Short groups (-vp9000), `--name value`, `--name=value`, repeated options.
const { values, positionals } = parseArgs({
  args: ['-vp9000', '--tag', 'web', '--tag=api', 'serve', 'Thessaloniki'],
  options,
  allowPositionals: true,
});
console.log(values.verbose, values.port, values.tag);  // true 9000 [ 'web', 'api' ]
console.log(positionals);                             // [ 'serve', 'Thessaloniki' ]

// Defaults fill what the command line leaves out; this run has no arguments.
console.log(parseArgs({ options }).values.port);      // 8080

// Strict mode rejects what the options do not describe.
try {
  parseArgs({ args: ['--nope'], options });
} catch (e: any) {
  console.log(e.code);                                // ERR_PARSE_ARGS_UNKNOWN_OPTION
}
