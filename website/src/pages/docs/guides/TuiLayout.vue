<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">Guides · Terminal UI · Part 1 of 3</span>
    <h1>Build a terminal app: layout &amp; components</h1>
    <p class="km-doc__lede">
      Over three short parts we'll build a complete, keyboard-driven <strong>to-do list</strong>
      in the terminal — and then a live system monitor — using <code>klain:tui</code>, the native
      terminal-UI framework. This first part is all about drawing: how a screen is described, how
      it's laid out, and the components you have to work with. No input yet; just pixels (well,
      cells). Everything compiles to a single native binary with no runtime.
    </p>

    <p>Here's where we're headed — the finished app from all three parts:</p>
    <Shot :src="todoImg" alt="A bordered to-do list panel in the terminal"
      caption="todo.ts — the app we build across this series: a flexbox panel, a selectable list, a progress summary, and a help line." />

    <h2>The mental model</h2>
    <p>
      <code>klain:tui</code> is an <em>immediate-mode</em> framework. You never mutate a widget or
      hold onto a handle; instead you write a function that returns a <strong>fresh description of
      the whole screen</strong> every time, and hand it to <code>render()</code>. Under the hood it
      sits on two pieces: <a href="https://github.com/facebook/yoga" target="_blank" rel="noopener">Yoga</a>
      — the same flexbox engine React Native uses, vendored in — for layout, and a
      double-buffered ANSI painter that <em>diffs</em> each new frame against the last and writes
      only the cells that actually changed. So even though you redraw everything, the terminal only
      sees the delta.
    </p>
    <p>
      There's no virtual DOM, no reconciler, no component instances living between frames — the tree
      is built in native code and freed after each paint. That makes the whole thing a plain loop in
      the classic <strong>state → view → update</strong> shape:
    </p>
    <ol>
      <li><strong>state</strong> — just variables: a list of tasks, which row the cursor is on.</li>
      <li><strong>view(state)</strong> — a pure function returning a tree of <code>Box</code>/<code>Text</code>/… calls.</li>
      <li><strong>update</strong> — read a key, change the state, call <code>render(view(state))</code> again.</li>
    </ol>
    <p>
      We'll build <code>view</code> here in Part 1, and wire up <code>update</code> in
      <router-link to="/docs/guides/tui/input-state">Part 2</router-link>.
    </p>

    <h2>1 · A blank canvas</h2>
    <p>
      Two calls bracket any full-screen app. <code>enter()</code> switches the terminal to its
      <em>alternate screen</em> (so your app doesn't scroll away your shell history) and hides the
      cursor; <code>leave()</code> puts everything back. Between them, <code>render(tree)</code>
      paints. Here's the smallest thing that draws:
    </p>
    <CodeBlock filename="hello.ts" :code="hello" />
    <p class="km-note">
      Always gate raw, interactive behaviour on <code>process.stdin.isTTY</code>. When the output is
      a pipe (say, in CI) there's no keyboard to read, so you paint one frame and exit cleanly
      instead of blocking forever. Every example in this series follows that pattern.
    </p>

    <h2>2 · Layout with flexbox</h2>
    <p>
      Every <code>Box</code> is a flex container, and its behaviour will be familiar from CSS.
      <code>flexDirection</code> is <code>'row'</code> or <code>'column'</code>;
      <code>flexGrow</code> lets a child soak up spare space; <code>justifyContent:
      'space-between'</code> pushes children to opposite ends of the row. Borders, padding, gaps and
      colours are all just props. This is the outer frame of the to-do list:
    </p>
    <CodeBlock filename="view.ts" :code="layout" />
    <p>
      Sizes flow <em>out</em> from the content. A <code>Text</code> measures itself, a
      <code>Box</code> sizes to its children (unless you pin an explicit <code>width</code>/<code>height</code>),
      and the root box fills the terminal. Resize the window and the very next frame re-flows —
      there's nothing to recompute by hand. Text is also width-aware: wide CJK and emoji glyphs
      correctly take two columns, so borders and columns still line up when your content isn't
      plain ASCII.
    </p>

    <h2>3 · The component kit</h2>
    <p>
      On top of <code>Box</code> and <code>Text</code>, Part 1's payoff is the built-in components.
      You compose them like any other node in the tree:
    </p>
    <ul>
      <li><code>List(items, { selected })</code> — one item per line with an optional highlight. If the
        list is taller than its box it scrolls to keep the selected row in view and draws a
        scrollbar, so you don't have to page it yourself.</li>
      <li><code>Progress(value)</code> — a bar filled from a <code>0..1</code> value.</li>
      <li><code>Spinner(frame, { label })</code> — a braille spinner; you advance <code>frame</code> each tick.</li>
      <li><code>TextInput(value)</code> — a value plus a cursor block (the keystrokes are yours — that's Part 2).</li>
    </ul>
    <p>
      The key habit: derive the components straight from state every frame with ordinary array
      methods. Here we turn a <code>tasks</code> array into list rows and a progress value:
    </p>
    <CodeBlock filename="view.ts" :code="components" />
    <p>
      Put those together and you already have a fully drawn — if inert — screen. The fruit-picker
      example is the same idea end to end: a list with a live selection, a counter, a spinner and a
      progress bar, all recomputed each frame from one small state object.
    </p>
    <Shot :src="menuImg" alt="A fruit-picker list with two items checked and a progress bar"
      caption="menu.ts — List selection, checkmarks derived from state, a live spinner, and a progress bar. A good skeleton to start from." />

    <div class="km-doc__nextrow">
      <router-link to="/docs/guides" class="km-btn">← All guides</router-link>
      <router-link to="/docs/guides/tui/input-state" class="km-btn km-btn--gold">Part 2 · Input &amp; state →</router-link>
    </div>
  </article>
</template>

<script setup>
import CodeBlock from 'components/CodeBlock.vue'
import Shot from 'components/docs/Shot.vue'
import todoImg from 'src/assets/tui/todo.png'
import menuImg from 'src/assets/tui/menu.png'

const hello = `import { Box, Text, render, enter, leave } from 'klain:tui'

enter()                                   // alternate screen, cursor hidden
render(Box({ padding: 1, border: 'round' }, [Text('Hello, TUI')]))

if (!process.stdin.isTTY) {
  leave()                                 // piped run: one frame, then exit
} else {
  // ... an input loop goes here (Part 2); for now, just show the frame:
  process.stdin.setRawMode(true)
  // (a real app loops here)
  leave()
}`

const layout = `Box(
  { flexDirection: 'column', width: 44, border: 'round',
    borderColor: 'cyan', padding: 1 },
  [
    Text('To-do', { color: 'green', bold: true }),

    // a row whose two ends are pushed apart:
    Box({ flexDirection: 'row', justifyContent: 'space-between' }, [
      Text('1/3 done', { color: 'yellow' }),
      Progress(0.33, { color: 'green', width: 20 }),
    ]),
  ],
)`

const components = `// rows and totals are derived from state, every frame
const rows = tasks.map((t) => (t.done ? '[x] ' + t.text : '[ ] ' + t.text))
const done = tasks.filter((t) => t.done).length

List(rows, { selected: cursor })           // highlights row \`cursor\`
Progress(done / tasks.length, { color: 'green', width: 20 })
TextInput(draft, { color: 'cyan' })         // the in-progress new task`
</script>
