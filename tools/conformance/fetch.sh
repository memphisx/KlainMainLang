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

if [ -d "$DEST/.git" ]; then
  current="$(git -C "$DEST" rev-parse HEAD)"
  if [ "$current" = "$PINNED_SHA" ]; then
    echo "test262 already at pinned commit $PINNED_SHA — nothing to do."
    exit 0
  fi
  echo "test262 checkout exists at $current, expected $PINNED_SHA — removing and re-fetching."
  rm -rf "$DEST"
fi

git init -q "$DEST"
git -C "$DEST" remote add origin "$REPO_URL"
git -C "$DEST" fetch --depth 1 origin "$PINNED_SHA"
git -C "$DEST" checkout -q FETCH_HEAD
echo "Fetched test262 @ $PINNED_SHA into $DEST"
