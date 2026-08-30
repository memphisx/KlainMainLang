<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">Guides · Terminal UI</span>
    <h1>Build a terminal app with <code>klain:tui</code></h1>
    <p class="km-doc__lede">
      We'll build a complete, keyboard-driven <strong>task manager</strong> from an empty
      screen: a flexbox layout, a selectable list, a text input for adding items, a progress
      summary, and real file persistence — then turn the same loop into a live, self-refreshing
      dashboard. Everything here compiles to a single native binary with no runtime.
    </p>

    <p>Here's what we're building:</p>
    <CodeBlock :terminal="true" label="tasks" lang="text" :code="finalFrame" />

    <h2>The mental model</h2>
    <p>
      <code>klain:tui</code> is an <em>immediate-mode</em> framework built on
      <a href="https://github.com/facebook/yoga" target="_blank" rel="noopener">Yoga</a> (the same
      flexbox engine React Native uses) plus a double-buffered ANSI painter. You describe the
      whole screen as a tree of builder calls — <code>Box</code>, <code>Text</code>,
      <code>List</code>, … — and call <code>render(root)</code>. The painter lays the tree out and
      writes only the cells that changed since the last frame, so redraws are cheap.
    </p>
    <p>
      There's no reconciler and no component runtime — the tree lives in native code. Your app is
      an ordinary loop in the classic <strong>state → view → update</strong> shape:
    </p>
    <ol>
      <li><strong>state</strong> — plain variables (a list of tasks, a cursor).</li>
      <li><strong>view(state)</strong> — a pure function returning a component tree.</li>
      <li><strong>update</strong> — read a key, change the state, repaint.</li>
    </ol>

    <h2>1 · A blank canvas</h2>
    <p>
      <code>enter()</code> switches to the alternate screen and hides the cursor;
      <code>leave()</code> restores everything. Between them, <code>render()</code> paints. Reads
      come from <code>klain:tty</code>; always guard raw mode on <code>isTTY</code> so a piped run
      exits cleanly instead of hanging.
    </p>
    <CodeBlock filename="tasks.ts" :code="skeleton" />
    <p class="km-note">
      <code>readKey()</code> returns one keypress — a single character, or a whole escape sequence
      (an arrow key is <code>ESC [ A</code>) as one string. It <em>blocks</em> until a key is
      pressed, which is exactly what an input-driven app wants.
    </p>

    <h2>2 · Layout with flexbox</h2>
    <p>
      Every <code>Box</code> is a flex container. <code>flexDirection</code> is <code>'row'</code>
      or <code>'column'</code>; <code>flexGrow</code> makes a child soak up free space;
      <code>justifyContent: 'space-between'</code> pushes children to opposite ends. Borders,
      padding, and colours are just props. This is the outer frame of our app:
    </p>
    <CodeBlock filename="view.ts" :code="layout" />
    <p>
      Sizes flow from Yoga: a <code>Text</code> measures itself, a <code>Box</code> sizes to its
      children (or to an explicit <code>width</code>/<code>height</code>), and the root fills the
      terminal. Resize the window and the next frame re-flows automatically.
    </p>

    <h2>3 · The components</h2>
    <p>
      <code>List</code> renders one item per line with an optional <code>selected</code> highlight.
      <code>Progress</code> fills a bar from a <code>0..1</code> value. <code>TextInput</code> shows
      a value plus a cursor block (editing is yours — you own the keystrokes). We compute the list
      rows straight from state with <code>.map()</code>:
    </p>
    <CodeBlock filename="view.ts" :code="components" />

    <h2>4 · Handling input</h2>
    <p>
      The update step is a <code>switch</code> on the key. Arrow keys move the cursor, space toggles
      the selected task, <code>a</code> enters an "adding" mode where keystrokes build up a draft
      string, and <code>q</code> quits. After every key we repaint — that's the whole loop.
    </p>
    <CodeBlock filename="tasks.ts" :code="update" />

    <h2>5 · Persistence with <code>fs</code></h2>
    <p>
      Because this compiles to a real native binary, you have the full standard library inside the
      loop — here, <code>fs</code>. We load tasks on startup and save on quit, one line per task:
    </p>
    <CodeBlock filename="tasks.ts" :code="persistence" />

    <p>
      That's the whole app. The complete, runnable source is in the examples:
    </p>
    <p>
      <router-link to="/docs/examples/tui/tasks" class="km-btn km-btn--gold">See tasks.ts →</router-link>
    </p>

    <h2>6 · Going live: self-refreshing dashboards</h2>
    <p>
      An input-driven app only repaints when you press a key. A monitor needs to repaint on a
      <em>clock</em> too. That's what the timeout form of <code>readKey</code> is for:
      <code>readKey(ms)</code> waits up to <code>ms</code> milliseconds and returns <code>""</code>
      if no key arrived — so the loop wakes on a tick <em>or</em> a keystroke, with no background
      thread.
    </p>
    <CodeBlock filename="klaintop.ts" :code="live" />
    <p>
      The <code>klaintop</code> example builds a full CPU/memory monitor this way (utilisation is the
      delta of busy-vs-idle jiffies between two <code>os.cpus()</code> samples):
    </p>
    <CodeBlock :terminal="true" label="klaintop" lang="text" :code="topFrame" />

    <h2>The gallery</h2>
    <p>Three complete apps, each a single runnable file:</p>
    <ul>
      <li><router-link to="/docs/examples/tui/tasks">tasks</router-link> — the task manager we just built (list, input, progress, <code>fs</code>).</li>
      <li><router-link to="/docs/examples/tui/klaintop">klaintop</router-link> — a live system monitor (timeout loop, <code>os</code>).</li>
      <li><router-link to="/docs/examples/tui/files">files</router-link> — a two-pane file browser (nested layout, <code>fs</code>/<code>path</code>).</li>
      <li><router-link to="/docs/examples/tui/menu">menu</router-link> — a minimal selectable menu, a good starting skeleton.</li>
    </ul>

    <h2>Good to know</h2>
    <ul>
      <li>Text is decoded as UTF-8 (one cell per code point), but not width-aware yet — wide
        (CJK/emoji) and combining characters occupy a single cell, so alignment can drift for such
        content.</li>
      <li>Style <em>enum</em> props (colours, <code>border</code>, <code>flexDirection</code>) are
        compile-time literals in this first release; numeric props (sizes, padding, a
        <code>Progress</code> value) can be any expression.</li>
      <li>Rendering is immediate-mode: the tree is rebuilt every frame and the cell-diff keeps the
        actual output minimal.</li>
    </ul>

    <div class="km-doc__nextrow">
      <router-link to="/docs/guides" class="km-btn">← All guides</router-link>
      <router-link to="/docs/examples/tui/tasks" class="km-btn km-btn--gold">Run the example →</router-link>
    </div>
  </article>
</template>

<script setup>
import CodeBlock from 'components/CodeBlock.vue'

const finalFrame = `╭──────────────────────────────────────────╮
│                                          │
│ Tasks                                    │
│                                          │
│ [x] wire up CI                           │
│ [ ] write the docs walkthrough           │
│ [ ] ship v0.11                           │
│                                          │
│ 1/3 done            ███████░░░░░░░░░░░░░ │
│                                          │
│ ↑/↓ move · space toggle · a add · q quit │
╰──────────────────────────────────────────╯`

const topFrame = `╭────────────────────────────────────────╮
│ klaintop            my-box · darwin      │
│                                          │
│ CPU  ██████████████░░░░░░░░░░ 61%        │
│ MEM  ████████░░░░░░░░░░░░░░░░ 38%        │
│                                          │
│ 9014 / 24576 MB · 12 cores      ⠹ live   │
│                                          │
│ q quit                                   │
╰────────────────────────────────────────╯`

const skeleton = `import { Box, Text, render, enter, leave } from 'klain:tui'
import { readKey } from 'klain:tty'

enter()
render(Box({ padding: 1, border: 'round' }, [Text('Hello, TUI')]))

if (!process.stdin.isTTY) {
  leave()                       // piped run: one frame, then exit
} else {
  process.stdin.setRawMode(true)
  let running = true
  while (running) {
    const key: string = readKey()
    if (key === 'q') running = false
    // ... update state here, then repaint:
    render(Box({ padding: 1, border: 'round' }, [Text('Hello, TUI')]))
  }
  leave()
}`

const layout = `Box(
  { flexDirection: 'column', width: 44, border: 'round',
    borderColor: 'cyan', padding: 1 },
  [
    Text('Tasks', { color: 'green', bold: true }),

    // a row whose two ends are pushed apart:
    Box({ flexDirection: 'row', justifyContent: 'space-between' }, [
      Text('1/3 done', { color: 'yellow' }),
      Progress(0.33, { color: 'green', width: 20 }),
    ]),
  ],
)`

const components = `// rows are derived from state every frame
const rows = tasks.map((t) => (t.done ? '[x] ' + t.text : '[ ] ' + t.text))

List(rows, { selected: cursor })          // ← highlights row \`cursor\`
Progress(done / tasks.length, { color: 'green', width: 20 })
TextInput(draft, { color: 'cyan' })        // shows the in-progress text`

const update = `const key: string = readKey()
const code: number = key.length > 0 ? key.charCodeAt(0) : -1

if (key === 'q') {
  running = false
} else if (key === '\\x1b[A') {            // up arrow
  cursor = (cursor + tasks.length - 1) % tasks.length
} else if (key === '\\x1b[B') {            // down arrow
  cursor = (cursor + 1) % tasks.length
} else if (key === ' ') {
  tasks[cursor].done = !tasks[cursor].done
} else if (key === 'a') {
  adding = true                            // start collecting a new task
}
render(view(tasks, cursor, adding, draft))`

const persistence = `import { existsSync, readFileSync, writeFileSync } from 'fs'

function save(tasks: Task[]): void {
  let body = ''
  for (let i = 0; i < tasks.length; i++) {
    body = body + (tasks[i].done ? '1 ' : '0 ') + tasks[i].text + '\\n'
  }
  writeFileSync('.klain-tasks.txt', body)   // real native file I/O
}
// ...load() reads it back on startup with existsSync + readFileSync.`

const live = `import { readKey } from 'klain:tty'

while (running) {
  const key: string = readKey(1000)        // wake on a key OR after ~1s
  if (key === 'q') {
    running = false
  } else {
    tick = tick + 1                         // '' means "just the tick"
    render(view(sample(), tick))
  }
}`
</script>
