// Quasar App (Vite) configuration — KlainMainLang website
// SSG build: `quasar build -m ssg`  |  Dev: `quasar dev`
// Docs: https://v2.quasar.dev/quasar-cli-vite/quasar-config-js

import { defineConfig } from '@quasar/app-vite'
import { fileURLToPath } from 'node:url'

const r = (p) => fileURLToPath(new URL(p, import.meta.url))

// Served from the root of the custom domain (klainmain.dev). Override with
// PUBLIC_PATH (e.g. /KlainMainLang/) only if deploying under a subpath.
const publicPath = process.env.PUBLIC_PATH || '/'

export default defineConfig((/* ctx */) => {
  return {
    boot: ['fonts'],

    css: ['app.scss'],

    extras: [
      'material-icons'
    ],

    build: {
      target: {
        browser: ['es2022', 'firefox115', 'chrome115', 'safari14'],
        node: 'node20'
      },
      vueRouterMode: 'history',
      publicPath,
      sourcemap: false,

      // v3 only ships '@' and '#q-app' by default — restore the classic aliases.
      alias: {
        src: r('./src'),
        components: r('./src/components'),
        layouts: r('./src/layouts'),
        pages: r('./src/pages'),
        boot: r('./src/boot'),
        assets: r('./src/assets')
      }
    },

    devServer: {
      open: false,
      port: 9100
    },

    framework: {
      config: {
        dark: true
      },
      // Only pull in the components we actually use — keeps the bundle lean.
      components: [
        'QLayout', 'QHeader', 'QFooter', 'QDrawer', 'QPageContainer', 'QPage',
        'QToolbar', 'QToolbarTitle', 'QBtn', 'QBtnGroup', 'QIcon', 'QSeparator',
        'QList', 'QItem', 'QItemSection', 'QItemLabel', 'QExpansionItem',
        'QCard', 'QCardSection', 'QTabs', 'QTab', 'QTabPanels', 'QTabPanel',
        'QMarkupTable', 'QScrollArea', 'QSpace', 'QChip', 'QBanner', 'QImg',
        'QLinearProgress'
      ],
      directives: ['ClosePopup', 'Ripple'],
      plugins: []
    },

    animations: [],

    ssr: { pwa: false },

    // Static Site Generation (app-vite v3 built-in mode).
    // Every static vue-router route is prerendered to its own index.html, so
    // GitHub Pages serves deep links directly. The built-in error page is
    // emitted as 404.html, which GitHub Pages serves for unknown routes.
    ssg: {}
  }
})
