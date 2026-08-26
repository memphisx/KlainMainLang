// Self-hosted fonts (bundled by Vite from node_modules — no runtime CDN).
//  · Jost — a Futura-style geometric grotesque for display, headings and the
//           Klain Main wordmark (the Calvin Klein typographic register).
//  · Inter — body text.  · JetBrains Mono — code.
import { defineBoot } from '#q-app'

import '@fontsource/jost/400.css'
import '@fontsource/jost/500.css'
import '@fontsource/jost/600.css'
import '@fontsource/jost/700.css'
import '@fontsource/jost/900.css'

import '@fontsource/inter/400.css'
import '@fontsource/inter/500.css'
import '@fontsource/inter/600.css'
import '@fontsource/inter/700.css'

import '@fontsource/jetbrains-mono/400.css'
import '@fontsource/jetbrains-mono/500.css'
import '@fontsource/jetbrains-mono/700.css'

export default defineBoot(() => {})
