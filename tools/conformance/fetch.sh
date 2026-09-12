#!/usr/bin/env bash
# fetch.sh — clones tc39/test262 into .test262/ at the repo root, pinned to a
# specific commit for reproducibility (TDD-00008 Design V2). Not vendored
# into this repo's own git history: the corpus is ~263MB and .test262/ is
# gitignored.
#
# test262 has no versioned release tags upstream (confirmed directly against
# the remote — only auto-generated "web-features-manifest-for-<sha>" tags,
# one per commit, not curated releases), so a commit SHA is the actual
# reproducibility mechanism here, not a tag.
set -euo pipefail

PINNED_SHA="3655e7464de3d52643ecddd4b5f9f4f3e7f62398"
REPO_URL="https://github.com/tc39/test262.git"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEST="$SCRIPT_DIR/../../.test262"

test262_ready=""
if [ -d "$DEST/.git" ]; then
  current="$(git -C "$DEST" rev-parse HEAD)"
  if [ "$current" = "$PINNED_SHA" ]; then
    echo "test262 already at pinned commit $PINNED_SHA — nothing to do."
    test262_ready=1
  else
    echo "test262 checkout exists at $current, expected $PINNED_SHA — removing and re-fetching."
    rm -rf "$DEST"
  fi
fi

if [ -z "$test262_ready" ]; then
  git init -q "$DEST"
  git -C "$DEST" remote add origin "$REPO_URL"
  git -C "$DEST" fetch --depth 1 origin "$PINNED_SHA"
  git -C "$DEST" checkout -q FETCH_HEAD
  echo "Fetched test262 @ $PINNED_SHA into $DEST"
fi

# --- Node.js pure-module test corpus (TDD-00121 Track B) ---------------------
# A pinned, sparse, blobless checkout of only the pure-module behavioral tests
# (path/querystring/url) plus test/common — the Node-parity oracle. Not vendored
# (.node-tests/ is gitignored). Pinned by release tag so the denominator is
# stable. The bulk of Node's test/ couples to the internal `common` harness and
# real socket/fs state (out of scope); only the near-pure modules are fetched.
NODE_TAG="v22.11.0"
NODE_URL="https://github.com/nodejs/node.git"
NODE_DEST="$SCRIPT_DIR/../../.node-tests"

# Readiness is tracked by a version marker written after a *complete* fetch, not
# by `git describe`: a `--depth 1 refs/tags/<tag>` fetch checks out FETCH_HEAD
# without creating a local tag, so `git describe --exact-match` always reported
# "none" and re-fetched on every run — under Docker-as-root on a mounted volume
# that `rm -rf`'d the host corpus. The marker lives in `.git/` so a checkout
# never touches it, and a partial/failed fetch leaves none → the next run refetches.
# Slice version: bumped when the sparse-checkout set below changes so an
# existing checkout at the right tag but the old (narrower) slice refetches.
# v2 = every behavioral suite dir, not just parallel (TDD-00204 Track 3).
NODE_SLICE="suites-v2"
NODE_MARKER="$NODE_DEST/.git/kml-corpus-version"
if [ -d "$NODE_DEST/.git" ]; then
  current_tag="$(cat "$NODE_MARKER" 2>/dev/null || echo none)"
  if [ "$current_tag" = "$NODE_TAG+$NODE_SLICE" ]; then
    echo "node tests already at pinned tag $NODE_TAG — nothing to do."
  else
    echo "node checkout at $current_tag, expected $NODE_TAG — removing and re-fetching."
    rm -rf "$NODE_DEST"
  fi
fi

if [ ! -d "$NODE_DEST/.git" ]; then
  git init -q "$NODE_DEST"
  git -C "$NODE_DEST" remote add origin "$NODE_URL"
  git -C "$NODE_DEST" config core.sparseCheckout true
  git -C "$NODE_DEST" sparse-checkout init --no-cone 2>/dev/null || true
  # EVERY behavioral suite dir (TDD-00204 Track 3), not a hand-picked one:
  # parallel (~3,500), sequential, es-module, message (+ their .out expected-
  # output baselines), internet (real network — available here), pummel and
  # known_issues (fetched and counted as named skip buckets: stress-scale /
  # documented-upstream-expected-failures), plus the test/common harness and
  # test/fixtures data. The runner buckets by suite+module and reports
  # honestly — most will not compile (untyped-dynamic Node test code), which
  # is the point of measuring against the real denominator.
  printf '%s\n' \
    'test/parallel/**' \
    'test/sequential/**' \
    'test/es-module/**' \
    'test/message/**' \
    'test/internet/**' \
    'test/pummel/**' \
    'test/known_issues/**' \
    'test/common/**' \
    'test/fixtures/**' \
    > "$NODE_DEST/.git/info/sparse-checkout"
  git -C "$NODE_DEST" fetch --depth 1 --filter=blob:none origin "refs/tags/$NODE_TAG"
  git -C "$NODE_DEST" checkout -q FETCH_HEAD
  echo "$NODE_TAG+$NODE_SLICE" > "$NODE_MARKER"
  echo "Fetched node tests @ $NODE_TAG ($NODE_SLICE) into $NODE_DEST"
fi

# --- Web Platform Tests corpus (TDD-00082 Track 2) ---------------------------
# A pinned, sparse, blobless checkout of the headless-runnable Web-Platform slice
# (url, dom/abort, dom/events) plus the shared resources/ dir the tests' fixtures
# and helper scripts live in — the Web-Platform-API oracle. Only the
# `.any.js`/`.window.js` multi-global files run (the `.html` DOM-tree majority is
# out of scope, reported not deleted); a `testharness.js`-shaped shim in the
# runner supplies the assert vocabulary. Not vendored (.wpt-tests/ is gitignored).
# Pinned by commit SHA: WPT ships no curated release tags (a rolling master), so
# the SHA is the reproducibility mechanism, as with test262 above.
WPT_SHA="85235b8e6ee3d09e14a98108a2baf612d6b47b08"
WPT_URL="https://github.com/web-platform-tests/wpt.git"
WPT_DEST="$SCRIPT_DIR/../../.wpt-tests"

# Slice version: bumped whenever the sparse-checkout pattern set below changes,
# so an existing checkout at the right SHA but the old (narrower) slice is
# refetched. v2 = the full-headless slice (TDD-00204): every multi-global
# source repo-wide, no directory allowlist.
WPT_SLICE="full-headless-v3"
WPT_MARKER="$WPT_DEST/.git/kml-corpus-version"
if [ -d "$WPT_DEST/.git" ]; then
  current="$(cat "$WPT_MARKER" 2>/dev/null || echo none)"
  if [ "$current" = "$WPT_SHA+$WPT_SLICE" ]; then
    echo "WPT already at pinned commit $WPT_SHA — nothing to do."
  else
    echo "WPT checkout at $current, expected $WPT_SHA — removing and re-fetching."
    rm -rf "$WPT_DEST"
  fi
fi

if [ ! -d "$WPT_DEST/.git" ]; then
  git init -q "$WPT_DEST"
  git -C "$WPT_DEST" remote add origin "$WPT_URL"
  git -C "$WPT_DEST" config core.sparseCheckout true
  git -C "$WPT_DEST" sparse-checkout init --no-cone 2>/dev/null || true
  # The FULL headless multi-global corpus (TDD-00204): every
  # `.any.js`/`.window.js`/`.worker.js` source anywhere in the repo — no
  # directory allowlist; scope classification happens in the runner, where it
  # is counted and reported, never at fetch time where it is invisible.
  # `*.js` (not just the three source suffixes) because `// META: script=`
  # helper includes are plain sibling .js files anywhere (`constants.sub.js`,
  # `/wasm/jsapi/wasm-module-builder.js`, `../support/Blob.js`) — the v2
  # suffix-only slice produced a ~500-file false-skip bucket. `*.json` + the
  # resources/common/util dirs carry fetch-shim fixtures and non-.js helper
  # data. Media-heavy blobs (images/video) stay unfetched; a still-missing
  # helper surfaces as a counted "shared helper not in fetched slice" skip.
  printf '%s\n' \
    '*.js' \
    '*.json' \
    '**/resources/**' \
    '**/common/**' \
    '**/util/**' \
    > "$WPT_DEST/.git/info/sparse-checkout"
  git -C "$WPT_DEST" fetch --depth 1 --filter=blob:none origin "$WPT_SHA"
  git -C "$WPT_DEST" checkout -q FETCH_HEAD
  echo "$WPT_SHA+$WPT_SLICE" > "$WPT_MARKER"
  echo "Fetched WPT @ $WPT_SHA ($WPT_SLICE) into $WPT_DEST"
fi

# Corpus-stats manifest (TDD-00204): full-tree class counts from git tree
# metadata (the blobless clone carries the whole tree), so the runner's report
# can open with the capability ladder — how much of WPT each capability tier
# reaches — without needing the unfetched files on disk. Regenerated on every
# run (cheap) so a hand-deleted manifest self-heals.
WPT_STATS="$WPT_DEST/.git/kml-corpus-stats"
git -C "$WPT_DEST" ls-tree -r HEAD --name-only | awk '
  { total++ }
  /\.(any|window|worker)\.js$/ { headless++; next }
  /-manual\./ { manual++; next }
  /(^|\/)crashtests\// { crash++; next }
  /^webdriver\// { wdspec++; next }
  /^css\/.*\.html?$/ { csshtml++; next }
  /\.html?$/ { otherhtml++ }
  END {
    printf "total=%d\nheadless=%d\ncss_html=%d\nother_html=%d\nmanual=%d\ncrashtests=%d\nwdspec=%d\n",
      total, headless, csshtml, otherhtml, manual, crash, wdspec
  }' > "$WPT_STATS"
echo "WPT corpus stats: $(tr '\n' ' ' < "$WPT_STATS")"

# --- TypeScript acceptance-oracle corpus (TDD-00121 Track C) ------------------
# A pinned, sparse, blobless checkout of Microsoft's compiler/conformance test
# cases plus their reference baselines. A case with a `*.errors.txt` baseline is
# expected to be rejected; one without is expected to compile clean — an
# accept/reject oracle for this compiler's front-end (parse+resolve, no run).
# Not vendored (.ts-tests/ is gitignored). Pinned by release tag for a stable
# denominator (TypeScript is a versioned language).
TS_TAG="v5.6.3"
TS_URL="https://github.com/microsoft/TypeScript.git"
TS_DEST="$SCRIPT_DIR/../../.ts-tests"

# Same version-marker readiness as the node corpus above (git describe can't see
# the pinned tag after a shallow tag fetch).
TS_MARKER="$TS_DEST/.git/kml-corpus-version"
if [ -d "$TS_DEST/.git" ]; then
  current_tag="$(cat "$TS_MARKER" 2>/dev/null || echo none)"
  if [ "$current_tag" = "$TS_TAG" ]; then
    echo "TypeScript tests already at pinned tag $TS_TAG — nothing to do."
  else
    echo "TypeScript checkout at $current_tag, expected $TS_TAG — removing and re-fetching."
    rm -rf "$TS_DEST"
  fi
fi

if [ ! -d "$TS_DEST/.git" ]; then
  git init -q "$TS_DEST"
  git -C "$TS_DEST" remote add origin "$TS_URL"
  git -C "$TS_DEST" config core.sparseCheckout true
  git -C "$TS_DEST" sparse-checkout init --no-cone 2>/dev/null || true
  printf '%s\n' \
    'tests/cases/compiler/**' \
    'tests/cases/conformance/**' \
    'tests/baselines/reference/*.errors.txt' \
    > "$TS_DEST/.git/info/sparse-checkout"
  git -C "$TS_DEST" fetch --depth 1 --filter=blob:none origin "refs/tags/$TS_TAG"
  git -C "$TS_DEST" checkout -q FETCH_HEAD
  echo "$TS_TAG" > "$TS_MARKER"
  echo "Fetched TypeScript tests @ $TS_TAG into $TS_DEST"
fi
