import examplesTree from 'src/data/examples-tree.json'

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
      { path: 'getting-started', name: 'getting-started', component: () => import('pages/docs/GettingStarted.vue') },
      { path: 'language', name: 'language', component: () => import('pages/docs/LanguageGuide.vue') },
      { path: 'stdlib', name: 'stdlib', component: () => import('pages/docs/StdLib.vue') },
      { path: 'cli', name: 'cli', component: () => import('pages/docs/Cli.vue') },
      { path: 'coverage', name: 'coverage', component: () => import('pages/docs/Coverage.vue') },
      { path: 'examples', name: 'examples', component: () => import('pages/docs/Examples.vue') },
      ...exampleRoutes
    ]
  },

  {
    path: '/:catchAll(.*)*',
    component: () => import('pages/ErrorNotFound.vue')
  }
]

export default routes
