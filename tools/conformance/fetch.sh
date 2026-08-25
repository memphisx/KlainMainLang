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

if [ -d "$NODE_DEST/.git" ]; then
  current_tag="$(git -C "$NODE_DEST" describe --tags --exact-match 2>/dev/null || echo none)"
  if [ "$current_tag" = "$NODE_TAG" ]; then
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
  # The full behavioral suite: every test/parallel/test-*.js (~3,500 files), the
  # test/common harness they nearly all require, and test/fixtures (data many of
  # them read). The runner buckets these by module and reports honestly — most
  # will not compile (untyped-dynamic Node test code), which is the point of
  # measuring against the real denominator rather than a hand-picked handful.
  printf '%s\n' \
    'test/parallel/**' \
    'test/common/**' \
    'test/fixtures/**' \
    > "$NODE_DEST/.git/info/sparse-checkout"
  git -C "$NODE_DEST" fetch --depth 1 --filter=blob:none origin "refs/tags/$NODE_TAG"
  git -C "$NODE_DEST" checkout -q FETCH_HEAD
  echo "Fetched node tests @ $NODE_TAG into $NODE_DEST"
fi

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

if [ -d "$TS_DEST/.git" ]; then
  current_tag="$(git -C "$TS_DEST" describe --tags --exact-match 2>/dev/null || echo none)"
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
  echo "Fetched TypeScript tests @ $TS_TAG into $TS_DEST"
fi
