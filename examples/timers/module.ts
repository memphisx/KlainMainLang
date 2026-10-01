// The `timers` module: the same timer functions as the globals, importable
// in every form, with the promise forms as `timers.promises`.
import timers, { setTimeout as later } from 'timers'

later((who: string) => { console.log(`hello, ${who}`) }, 10, 'Thessaloniki')

let ticks = 0
const ticker = timers.setInterval(() => { ticks++ }, 1)
timers.setTimeout(() => {
    timers.clearInterval(ticker)
    console.log(ticks > 0 ? 'the interval ticked' : 'no ticks')
}, 5)

const value = await timers.promises.setTimeout(30, 'done waiting')
console.log(value)
