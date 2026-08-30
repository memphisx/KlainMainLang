import examplesTree from 'src/data/examples-tree.json'
import { index as referenceIndex } from 'src/data/reference-index.js'

// Flatten the example tree into per-file routes (static paths → prerendered).
function collectExampleRoutes (nodes, acc) {
  for (const node of nodes) {
    if (node.type === 'example') {
      acc.push({
        path: 'examples/' + node.key,
        name: 'example:' + node.key,
        component: () => import('pages/docs/ExampleView.vue'),
        props: { exampleKey: node.key }
      })
    } else if (node.children) {
      collectExampleRoutes(node.children, acc)
    }
  }
  return acc
}

const exampleRoutes = collectExampleRoutes(examplesTree, [])

// One prerendered route per API-reference surface, from the generated index
// so a new surface JSON registers itself (no per-surface wiring).
const referenceRoutes = referenceIndex.map(({ surface }) => ({
  path: 'reference/' + surface,
  name: 'reference-' + surface,
  component: () => import('pages/docs/reference/ReferencePage.vue'),
  props: { surface }
}))

const routes = [
  {
    path: '/',
    component: () => import('layouts/MarketingLayout.vue'),
    children: [
      { path: '', name: 'home', component: () => import('pages/IndexPage.vue') }
    ]
  },

  {
    path: '/docs',
    component: () => import('layouts/DocsLayout.vue'),
    children: [
      { path: '', name: 'docs', component: () => import('pages/docs/DocsIndex.vue') },
      { path: 'install', name: 'install', component: () => import('pages/docs/Install.vue') },
      { path: 'getting-started', name: 'getting-started', component: () => import('pages/docs/GettingStarted.vue') },
      { path: 'language', name: 'language', component: () => import('pages/docs/LanguageGuide.vue') },
      { path: 'stdlib', name: 'stdlib', component: () => import('pages/docs/StdLib.vue') },
      { path: 'klain', name: 'klain', component: () => import('pages/docs/Klain.vue') },
      { path: 'klain/webview', name: 'klain-webview', component: () => import('pages/docs/KlainWebview.vue') },
      { path: 'klain/http', name: 'klain-http', component: () => import('pages/docs/KlainHttp.vue') },
      { path: 'klain/sync', name: 'klain-sync', component: () => import('pages/docs/KlainSync.vue') },
      { path: 'cli', name: 'cli', component: () => import('pages/docs/Cli.vue') },
      { path: 'coverage', name: 'coverage', component: () => import('pages/docs/Coverage.vue') },
      { path: 'guides', name: 'guides', component: () => import('pages/docs/guides/GuidesIndex.vue') },
      { path: 'guides/tui-app', name: 'guides-tui-app', component: () => import('pages/docs/guides/BuildTuiApp.vue') },
      { path: 'examples', name: 'examples', component: () => import('pages/docs/Examples.vue') },
      ...referenceRoutes,
      ...exampleRoutes
    ]
  },

  {
    path: '/:catchAll(.*)*',
    component: () => import('pages/ErrorNotFound.vue')
  }
]

export default routes
