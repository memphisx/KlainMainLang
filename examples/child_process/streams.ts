// A child's stdio are streams: iterate one, pipe one child into another,
// cancel a child with an AbortSignal.
import { spawn } from 'child_process'

async function main(): Promise<void> {
    const ls = spawn('printf', ['alpha\nbeta\n'])
    let text = ''
    for await (const chunk of ls.stdout) text += chunk.toString()
    console.log('lines:', text.trim().split('\n').length)

    const upper = spawn('tr', ['a-z', 'A-Z'])
    spawn('printf', ['kalimera thessaloniki']).stdout.pipe(upper.stdin)
    upper.stdout.setEncoding('utf8')
    upper.stdout.on('data', (d: string) => console.log('upper:', d))
    await new Promise<void>((resolve) => upper.on('close', () => resolve()))

    const ac = new AbortController()
    const sleeper = spawn('sleep', ['10'], { signal: ac.signal })
    sleeper.on('error', (e: Error) => console.log('aborted:', e.name))
    sleeper.on('exit', (code, signal) => console.log('exit:', code, signal))
    ac.abort()
}

main()
