#!/usr/bin/env bash
# End-to-end check against a real NestJS application.
#
# This is the only fixture with a realistic dependency graph, decorators
# resolved through @nestjs/common, and a CLI plugin. It proves three things
# that nothing else here can:
#
#   1. dependency injection works — which means TypeScript 7 emitted
#      design:paramtypes the way NestJS expects
#   2. plugin metadata generation works — the swagger schema below is derived
#      entirely by the plugin, since the DTO carries no @ApiProperty decorators
#   3. the whole pipeline holds together on a project with real dependencies
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP="$ROOT/tests/nest-app"
EXPECTED="di=Ada schema=age,breed,name"

if [ ! -x "$ROOT/bin/nestgo" ]; then
  echo "bin/nestgo not found — run 'just build' first" >&2
  exit 1
fi
if [ ! -d "$APP/node_modules" ]; then
  echo "fixture dependencies missing — run: npm install --prefix tests/nest-app" >&2
  exit 1
fi

cd "$APP"
rm -rf dist src/metadata.ts

"$ROOT/bin/nestgo" build

if [ ! -f src/metadata.ts ]; then
  echo "the plugin sidecar did not write src/metadata.ts" >&2
  exit 1
fi

OUT=$(node dist/main.js)
if [ "$OUT" != "$EXPECTED" ]; then
  echo "unexpected output:" >&2
  echo "  got:      $OUT" >&2
  echo "  expected: $EXPECTED" >&2
  echo >&2
  echo "di=      dependency injection resolved the service into the controller" >&2
  echo "schema=  swagger properties inferred by the CLI plugin, not annotated" >&2
  exit 1
fi

rm -rf dist src/metadata.ts
echo "NestJS app builds, DI resolves, and plugin metadata is generated."
