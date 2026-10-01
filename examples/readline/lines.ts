// Reading a file line by line: readline over a file stream, iterated with
// for await. crlfDelay: Infinity treats every \r\n as a single line ending.
//
//   ./lines other.txt  counts the lines and words of other.txt
//   ./lines            or of the running program itself

import * as fs from 'fs'
import * as readline from 'readline'

async function main(): Promise<void> {
  const file = process.argv[2] ?? process.argv[1]
  const rl = readline.createInterface({ input: fs.createReadStream(file), crlfDelay: Infinity })
  let lines = 0
  let words = 0
  for await (const line of rl) {
    lines++
    words += line.split(/\s+/).filter((w: string) => w.length > 0).length
  }
  console.log(file + ': ' + lines + ' lines, ' + words + ' words')
}

main()
