// readline — interactive line-by-line stdin, Node's readline module.
// createInterface returns an EventEmitter Interface:
//
//   rl.on('line', (line) => ...)   fires once per input line (CR stripped)
//   rl.question(query, (answer) => ...)  writes the prompt, routes the next
//                                          line to the one-shot callback
//   rl.close()                     stop reading; fires the 'close' event
//   rl.on('close', () => ...)      also fires on end-of-input (EOF)
//
// On a terminal the line can be edited (arrows, Ctrl-A/E, history) as in
// Node. Reading a file line by line is lines.ts.
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
