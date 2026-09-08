import { defineConfig, devices } from '@playwright/test'

// E2E tests run against the *real* SSG build (dist/ssg), served the way a static
// host serves it — so they exercise the prerender + client-hydration path that
// only exists in production (the dev SPA never hydrates). This is what catches
// hydration mismatches like the docs-drawer black-page bug.
const PORT = Number(process.env.SSG_PORT || 9200)

export default defineConfig({
  testDir: './tests/e2e',
  timeout: 30_000,
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: process.env.CI ? [['github'], ['list']] : [['list']],
  use: {
    baseURL: `http://localhost:${PORT}`,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure'
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } }
  ],
  // Build the SSG once, then serve dist/ssg for the whole run. `reuseExistingServer`
  // lets a dev iterate against an already-built/served site without rebuilding.
  // In CI the deploy workflow has already run `npm run build`, so E2E_SERVE_ONLY
  // skips the rebuild and just serves the existing dist/ssg.
  webServer: {
    command: process.env.E2E_SERVE_ONLY === '1'
      ? 'node scripts/serve-ssg.mjs'
      : 'npm run build && node scripts/serve-ssg.mjs',
    url: `http://localhost:${PORT}/`,
    timeout: 240_000,
    reuseExistingServer: !process.env.CI
  }
})
