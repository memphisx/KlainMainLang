// readline — interactive line-by-line stdin. Import-gated (a virtual built-in
// module, not a real file). createInterface returns an EventEmitter-style
// Interface:
//
//   rl.on('line', (line) => ...)   fires once per input line (CR stripped)
//   rl.question(query, (answer) => ...)  writes the prompt, routes the next
//                                          line to the one-shot callback
//   rl.close()                     stop reading; fires the 'close' event
//   rl.on('close', () => ...)      also fires on end-of-input (EOF)
//
// Stdin is folded into the same event loop as timers/child_process, so the
// callbacks fire as input arrives rather than blocking.
//
// Run it and type lines, then Ctrl-D (EOF):  ./readline
// Or pipe input:  printf 'Ada\n2\n4\n' | ./readline

import readline from 'readline'

const rl = readline.createInterface({ input: process.stdin, output: process.stdout })

rl.question("What's your name? ", (name: string) => {
  console.log("Hello, " + name + "!")

  let sum = 0
  let count = 0
  console.log("Enter numbers, one per line (Ctrl-D to finish):")

  rl.on('line', (line: string) => {
    sum = sum + Number(line)
    count = count + 1
  })

  rl.on('close', () => {
    console.log("Read " + count + " numbers; sum = " + sum)
  })
})
