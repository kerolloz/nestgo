#!/usr/bin/env bash
# Publish the given package directories to npm, skipping any whose exact
# version is already on the registry.
#
# npm publish is not idempotent. Without the skip, a release that fails partway
# through the package list can never be retried: the rerun hits EPUBLISHCONFLICT
# on everything it already published and fails again, leaving the release
# permanently half-shipped. Any error other than "already published" still
# fails the job.
set -euo pipefail

for dir in "$@"; do
  [ -f "$dir/package.json" ] || continue

  name=$(node -p "require('./$dir/package.json').name")
  version=$(node -p "require('./$dir/package.json').version")

  if npm view "$name@$version" version >/dev/null 2>&1; then
    echo "skip    $name@$version (already on registry)"
    continue
  fi

  echo "publish $name@$version"
  (cd "$dir" && npm publish --access public --provenance)
done
