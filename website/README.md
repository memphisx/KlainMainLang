# KlainMainLang website

Marketing landing page + documentation SPA for the KlainMainLang compiler.
Built with **Quasar (Vue 3) + Vite in SSG mode** — every route is prerendered
to static HTML, so it hosts cleanly on GitHub Pages.

This is a self-contained front-end project. It is unrelated to the compiler's
own build, tests, docs, ADRs and TDDs — nothing here touches `docs/status/`.

## Develop

```sh
cd website
npm install
npm run dev          # http://localhost:9100  (SPA dev server, hot reload)
```

## Examples (auto-generated)

Every `.ts` file under the repo's `examples/` directory becomes a documentation
page. `npm run gen:examples` (run automatically before `dev` and `build`) scans
`../examples` and writes `src/data/examples-{tree,content}.json`.

**Literate convention:** the leading `//` comment block at the top of each
example file is harvested as that example's description and rendered as prose,
*outside* the highlighted code; the rest of the file stays as code. Internal
`ADR-`/`TDD-`/`docs/` references are stripped from the prose. To add or reword a
description, just edit the comment at the top of the example file and re-run the
generator — no website edits needed. The left-hand docs sidebar renders the
examples as a collapsible, multi-level tree grouped by directory.

## Build (static site)

```sh
npm run build        # → dist/ssg  (prerendered HTML per route + 404.html)
```

Output is fully self-contained: fonts (Inter, JetBrains Mono) are bundled
locally, all brand assets are inline SVG, and there are no runtime CDN requests.

### Preview a production build locally

`dist/ssg` is prerendered as one directory per route, so any static server works.
Build with a root public path first, then serve:

```sh
PUBLIC_PATH=/ npm run build
cd dist/ssg && python3 -m http.server 9222   # → http://localhost:9222
```

## Deploy to GitHub Pages

Deployment is automated by `.github/workflows/pages.yml`: on every push to
`main` that touches `website/` or `examples/`, it builds the SSG output and
publishes it via the official GitHub Pages actions.

- The site is served from the **custom domain `klainmain.dev`** (apex), so the
  build uses `publicPath = "/"`. `public/CNAME` carries the domain into the
  published output.
- One-time setup: repo **Settings → Pages → Source = "GitHub Actions"**, and the
  custom domain configured under Settings → Pages.
- `dist/ssg/404.html` is emitted automatically and served for unknown routes.

To deploy under a subpath instead (e.g. a project page), build with
`PUBLIC_PATH=/KlainMainLang/ npm run build` and drop the `CNAME`.

## Structure

```
src/
  layouts/    MarketingLayout (landing) · DocsLayout (docs drawer + SPA)
  pages/      IndexPage.vue · docs/*.vue
  components/ CodeBlock, SiteFooter, brand/{Wordmark,VerginaSun,FlagVergina}
  css/        quasar.variables.scss · brand.scss · app.scss
  lib/        content.js (code samples + coverage figures) · hl.js (highlighting)
  router/     routes.js
public/       icons/favicon.svg · og-image.svg
```

## Brand notes

Monochrome (black + white) with a single **Vergina-gold** accent, a bold
uppercase grotesque wordmark rendered as **ΚΛΑΙΝ ΜΑΙΝ**, and a Greek-flag asset
whose canton cross is replaced by the Vergina Sun — a nod to where the project
was made. Coverage figures and code samples mirror the repository so the site
never overstates what the compiler does.
