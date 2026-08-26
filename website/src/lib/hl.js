// Minimal highlight.js setup: only the TypeScript grammar + a tiny bash grammar
// for the terminal snippets. Keeps the bundle small and works offline.
import hljs from 'highlight.js/lib/core'
import typescript from 'highlight.js/lib/languages/typescript'
import bash from 'highlight.js/lib/languages/bash'

hljs.registerLanguage('typescript', typescript)
hljs.registerLanguage('ts', typescript)
hljs.registerLanguage('bash', bash)
hljs.registerLanguage('sh', bash)

export function highlight (code, lang = 'typescript') {
  const language = hljs.getLanguage(lang) ? lang : 'typescript'
  return hljs.highlight(code, { language, ignoreIllegals: true }).value
}

export default hljs
