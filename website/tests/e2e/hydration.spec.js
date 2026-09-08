import { test, expect } from '@playwright/test'

// Representative routes across every layout and page kind. The docs routes are
// the ones that regressed (the DocsLayout drawer black-page bug); home uses the
// drawer-less MarketingLayout. Deep-loading each URL exercises the SSG
// prerender + client hydration path — the one that only exists in production.
const ROUTES = [
  { path: '/', name: 'home (marketing)' },
  { path: '/docs/', name: 'docs index' },
  { path: '/docs/cli/', name: 'docs · CLI' },
  { path: '/docs/getting-started/', name: 'docs · getting started' },
  { path: '/docs/language/', name: 'docs · language guide' },
  { path: '/docs/reference/fs/', name: 'docs · reference (fs)' },
  { path: '/docs/examples/', name: 'docs · examples index' }
]

// Vue logs this exact string once per page when the server HTML and the client
// render disagree — the production build's only hydration signal.
const HYDRATION_MISMATCH = /hydrat.*mismatch/i

for (const route of ROUTES) {
  test(`${route.name} hydrates cleanly and shows content`, async ({ page }) => {
    const consoleErrors = []
    const pageErrors = []
    page.on('console', (msg) => {
      if (msg.type() === 'error' || msg.type() === 'warning') consoleErrors.push(msg.text())
    })
    page.on('pageerror', (err) => pageErrors.push(err.message))

    await page.goto(route.path, { waitUntil: 'networkidle' })

    // 1. No hydration mismatch — the regression guard for the black-page bug.
    const mismatches = consoleErrors.filter((t) => HYDRATION_MISMATCH.test(t))
    expect(mismatches, `hydration mismatch on ${route.path}:\n${mismatches.join('\n')}`).toEqual([])

    // 2. No uncaught client exceptions.
    expect(pageErrors, `page errors on ${route.path}:\n${pageErrors.join('\n')}`).toEqual([])

    // 3. Content is actually visible — the page heading renders...
    await expect(page.locator('h1').first()).toBeVisible()

    // 4. ...and nothing covers it (the black page was a fixed full-screen
    //    drawer backdrop painted over the content). Assert the element at the
    //    viewport centre is real content, not an overlay/backdrop.
    const covered = await page.evaluate(() => {
      const el = document.elementFromPoint(window.innerWidth / 2, window.innerHeight / 2)
      if (!el) return 'nothing at centre'
      const cls = typeof el.className === 'string' ? el.className : ''
      if (/backdrop|overlay/i.test(cls)) return `covered by: ${cls}`
      return null
    })
    expect(covered, `viewport centre on ${route.path} is ${covered}`).toBeNull()
  })
}
