import { defineRouter } from '#q-app'
import { createRouter, createMemoryHistory, createWebHistory, createWebHashHistory } from 'vue-router'
import routes from './routes'

export default defineRouter(function (/* { store, ssrContext } */) {
  // On the server (SSG/SSR render pass) use in-memory history; on the client
  // pick history vs hash from the configured router mode.
  const createHistory = import.meta.env.QUASAR_CLIENT
    ? (import.meta.env.QUASAR_VUE_ROUTER_MODE === 'history' ? createWebHistory : createWebHashHistory)
    : createMemoryHistory

  const Router = createRouter({
    scrollBehavior: (to) => {
      if (to.hash) return { el: to.hash, behavior: 'smooth', top: 80 }
      return { left: 0, top: 0 }
    },
    routes,
    history: createHistory(import.meta.env.QUASAR_VUE_ROUTER_BASE)
  })

  return Router
})
