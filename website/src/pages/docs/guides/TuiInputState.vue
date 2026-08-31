<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">Guides · Terminal UI · Part 2 of 3</span>
    <h1>Build a terminal app: input &amp; state</h1>
    <p class="km-doc__lede">
      In <router-link to="/docs/guides/tui/layout">Part 1</router-link> we drew a static screen from
      a <code>view(state)</code> function. Now we make it <em>respond</em>: read keystrokes, change
      the state, repaint. That's the whole update half of the state → view → update loop, and once
      you have it, everything from a menu to a file browser is the same pattern with a bigger state.
    </p>

    <h2>1 · The update loop</h2>
    <p>
      Keys come from <code>klain:tty</code>. <code>readKey()</code> returns one keypress — a single
      character, or a whole escape sequence delivered as one string (an up-arrow is the three-byte
      <code>ESC [ A</code>). It <em>blocks</em> until a key is pressed, which is exactly right for an
      input-driven app: you do nothing until the user does something. The update step is then just a
      <code>switch</code> over the key, followed by a repaint:
    </p>
    <CodeBlock filename="todo.ts" :code="update" />
    <p>
      That's the entire interaction model. Arrow keys move a cursor (note the modulo wrap so it
      loops around the ends), space toggles the selected task, and <code>q</code> quits by ending
      the loop. After every branch we call <code>render(view(...))</code> — because the view is a
      pure function of state, you never touch a widget directly; you change a variable and redraw.
    </p>
    <p class="km-note">
      Reading the first byte with <code>key.charCodeAt(0)</code>? Guard it —
      <code>readKey()</code> can hand back an empty string on end-of-input, and
      <code>''.charCodeAt(0)</code> is <code>NaN</code>. The to-do list uses
      <code>key.length &gt; 0 ? key.charCodeAt(0) : -1</code> so a stray EOF can't misfire a
      shortcut.
    </p>

    <h2>2 · Editing text</h2>
    <p>
      A text field is just more state. Pressing <code>a</code> flips an <code>adding</code> flag;
      while it's set, printable keys append to a <code>draft</code> string and
      <code>Backspace</code> trims it, which we show live with <code>TextInput(draft)</code>. Enter
      commits the draft as a new task and clears it. There's no hidden input widget with its own
      buffer — <em>you</em> own the keystrokes, so the behaviour is exactly what you write:
    </p>
    <CodeBlock filename="todo.ts" :code="editing" />

    <h2>3 · Persistence with <code>fs</code></h2>
    <p>
      Because this compiles to a real native binary, the whole standard library is available right
      inside the loop — no bridge, no IPC. Here that's <code>fs</code>: we load tasks on startup and
      save on quit, one line per task.
    </p>
    <CodeBlock filename="todo.ts" :code="persistence" />
    <p>
      And that's a complete, persistent application. The full runnable source is in the examples:
    </p>
    <p>
      <router-link to="/docs/examples/tui/todo" class="km-btn km-btn--gold">See todo.ts →</router-link>
    </p>

    <h2>Going further: the same loop, more state</h2>
    <p>
      Nothing about this pattern is specific to a to-do list. A two-pane file browser is the same
      state → view → update loop where the state is “which directory am I in and what's selected”;
      each keypress rebuilds the listing and re-reads the highlighted entry for a live preview,
      using real <code>fs</code>/<code>path</code> calls. Same three moving parts, richer view:
    </p>
    <Shot :src="filesImg" alt="A two-pane terminal file browser with a directory list and a file preview"
      caption="files.ts — a nested layout (a row split into a list pane and a preview pane) driven by the same input loop, over real fs/path calls." />

    <div class="km-doc__nextrow">
      <router-link to="/docs/guides/tui/layout" class="km-btn">← Part 1 · Layout</router-link>
      <router-link to="/docs/guides/tui/live-dashboard" class="km-btn km-btn--gold">Part 3 · Live dashboards →</router-link>
    </div>
  </article>
</template>

<script setup>
import CodeBlock from 'components/CodeBlock.vue'
import Shot from 'components/docs/Shot.vue'
import filesImg from 'src/assets/tui/files.png'

const update = `process.stdin.setRawMode(true)
let running = true
while (running) {
  const key: string = readKey()             // blocks until a keypress

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
  render(view(tasks, cursor, adding, draft)) // repaint after every key
}
leave()`

const editing = `if (adding) {
  if (key === '\\r' || key === '\\n') {        // Enter: commit the draft
    if (draft.length > 0) tasks.push({ text: draft, done: false })
    draft = ''
    adding = false
  } else if (key === '\\x7f') {                // Backspace
    draft = draft.slice(0, draft.length - 1)
  } else if (key.length === 1 && key >= ' ') { // a printable character
    draft = draft + key
  }
}
// ...and in view(), while \`adding\`, show the field:
//   TextInput(draft, { color: 'cyan' })`

const persistence = `import { existsSync, readFileSync, writeFileSync } from 'fs'

function save(tasks: Task[]): void {
  let body = ''
  for (let i = 0; i < tasks.length; i++) {
    body = body + (tasks[i].done ? '1 ' : '0 ') + tasks[i].text + '\\n'
  }
  writeFileSync('.klain-todo.txt', body)     // real native file I/O
}

function load(): Task[] {
  if (!existsSync('.klain-todo.txt')) return []
  // ...split readFileSync('.klain-todo.txt') back into { text, done }.
  return parse(readFileSync('.klain-todo.txt'))
}`
</script>
