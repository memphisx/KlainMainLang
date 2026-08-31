<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">Guides · Terminal UI · Part 3 of 3</span>
    <h1>Build a terminal app: live dashboards</h1>
    <p class="km-doc__lede">
      The to-do list only repaints when you press a key — perfect for an editor, wrong for a
      monitor. A dashboard has to redraw on a <em>clock</em> too, whether or not anyone's touching
      the keyboard. This last part shows the one small change that turns the same loop into a live,
      self-refreshing view — the shape a CLI or Docker process/resource manager takes.
    </p>

    <h2>1 · <code>readKey</code>, but with a deadline</h2>
    <p>
      The trick is the timeout form of <code>readKey</code>. <code>readKey(ms)</code> waits up to
      <code>ms</code> milliseconds for a keypress and returns <code>""</code> if none arrived — so
      the loop wakes on a <strong>tick</strong> or a <strong>keystroke</strong>, whichever comes
      first, with no background thread and no async machinery:
    </p>
    <CodeBlock filename="klaintop.ts" :code="live" />
    <p>
      An empty return just means “the timer fired” — you re-sample and repaint. A real key still
      comes through immediately, so <code>q</code> quits without waiting out the second. One loop,
      one thread, both a clock and a keyboard.
    </p>
    <p class="km-note">
      This is also the point to handle <kbd>Ctrl-C</kbd> yourself if you want a graceful exit: in
      raw mode it arrives as an ordinary key (character code <code>3</code>), not a signal, so a
      <code>key.charCodeAt(0) === 3</code> check can call <code>leave()</code> and quit cleanly.
    </p>

    <h2>2 · Sampling the system</h2>
    <p>
      Everything else is just where the numbers come from. CPU utilisation is the classic delta of
      busy-versus-idle jiffies between two samples of <code>os.cpus()</code>; memory comes from
      <code>totalmem()</code>/<code>freemem()</code>. Because we're a native binary these are real
      OS calls, not shims:
    </p>
    <CodeBlock filename="klaintop.ts" :code="sample" />
    <p>
      The process table is just as direct: shell out to <code>ps</code> with
      <code>process.execFileSync</code>, parse the lines into rows, and sort them in-process. Feed the
      rows to a <code>List</code> — which scrolls to the selection on its own — and a
      <code>process.kill(pid)</code> behind a y/n confirm turns the monitor into a manager:
    </p>
    <CodeBlock filename="klaintop.ts" :code="procs" />
    <p>
      Feed the CPU/memory fractions into the same <code>Progress</code> and <code>Text</code>
      components from Part 1, advance the spinner's <code>frame</code> each tick, and you have a live
      process manager:
    </p>
    <Shot :src="klaintopImg" alt="A terminal process manager with CPU/memory bars and a scrollable process table"
      caption="klaintop.ts — a htop-lite process manager: CPU/MEM bars plus a scrollable, sortable ps table (PID/CPU/MEM/command) you can kill from, all redrawn each tick via readKey(1500)." />

    <h2>Good to know</h2>
    <ul>
      <li>Text is width-aware through a standard <code>wcwidth</code> table — wide CJK and emoji
        glyphs take two columns and combining marks fold onto their base, so alignment holds. The one
        gap is multi-code-point emoji joined by ZWJ (flag/family sequences), which still render as
        their separate glyphs.</li>
      <li>Style <em>enum</em> props (colours, <code>border</code>, <code>flexDirection</code>) are
        compile-time literals in this release; numeric props (sizes, padding, a
        <code>Progress</code> value) can be any expression.</li>
      <li>Rendering is immediate-mode: the tree is rebuilt every frame and the cell-diff keeps the
        actual bytes written to the terminal minimal.</li>
      <li>A <code>List</code> taller than its box scrolls to the selection and draws a scrollbar on
        its own; you only manage the <code>selected</code> index.</li>
    </ul>

    <h2>The gallery</h2>
    <p>Four complete apps, each a single runnable file — read them alongside these three parts:</p>
    <ul>
      <li><router-link to="/docs/examples/tui/todo">todo</router-link> — the to-do list we built (list, input, progress, <code>fs</code>).</li>
      <li><router-link to="/docs/examples/tui/klaintop">klaintop</router-link> — this process manager (timeout loop, <code>os</code>, <code>ps</code> + kill).</li>
      <li><router-link to="/docs/examples/tui/files">files</router-link> — a two-pane file browser (nested layout, <code>fs</code>/<code>path</code>).</li>
      <li><router-link to="/docs/examples/tui/menu">menu</router-link> — a minimal selectable menu, a good starting skeleton.</li>
    </ul>

    <div class="km-doc__nextrow">
      <router-link to="/docs/guides/tui/input-state" class="km-btn">← Part 2 · Input &amp; state</router-link>
      <router-link to="/docs/examples/tui/klaintop" class="km-btn km-btn--gold">Run the example →</router-link>
    </div>
  </article>
</template>

<script setup>
import CodeBlock from 'components/CodeBlock.vue'
import Shot from 'components/docs/Shot.vue'
import klaintopImg from 'src/assets/tui/klaintop.png'

const live = `import { readKey } from 'klain:tty'

let tick = 0
while (running) {
  const key: string = readKey(1500)         // wake on a key OR after ~1.5s
  if (key === 'q') {
    running = false
  } else {
    tick = tick + 1                          // '' means "just the tick"
    render(view(sample(), tick))
  }
}
leave()`

const sample = `import { cpus, totalmem, freemem } from 'os'

// Aggregate { total, idle } jiffies across every core.
function sample(): { total: number; idle: number } {
  const cs = cpus()
  let total = 0, idle = 0
  for (let i = 0; i < cs.length; i++) {
    const t = cs[i].times
    total += t.user + t.nice + t.sys + t.idle + t.irq
    idle += t.idle
  }
  return { total, idle }
}

// Utilisation = 1 - (idle delta / total delta) between two samples.
const cur = sample()
const cpuFrac = 1 - (cur.idle - prev.idle) / (cur.total - prev.total)
prev = cur`

const procs = `// Snapshot every process via ps, parsed and sorted in-process. BSD-style
// -axo works on both macOS and Linux; we sort ourselves to avoid a
// platform-specific --sort flag.
function listProcs(sort: string): Proc[] {
  const raw: string = process.execFileSync('ps', ['-axo', 'pid,pcpu,pmem,comm'])
  const out: Proc[] = []
  for (const line of raw.split('\\n').slice(1)) {
    const p = line.trim().split(/\\s+/)
    if (p.length < 4) continue
    out.push({ pid: parseInt(p[0], 10), cpu: parseFloat(p[1]),
               mem: parseFloat(p[2]), comm: p.slice(3).join(' ') })
  }
  out.sort((a, b) => (sort === 'mem' ? b.mem - a.mem : b.cpu - a.cpu))
  return out
}

// ...and killing the selection is one guarded call:
if (key === 'y') { try { process.kill(procs[cursor].pid) } catch (e) {} }`
</script>
