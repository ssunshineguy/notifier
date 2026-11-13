#!/usr/bin/env bash
# Regenerates API bundles and server code from the OpenAPI specs.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

(cd api && redocly lint notifier emulator && redocly bundle notifier emulator)

generate_server() {
  local spec="$1"
  local target="$2"

  # Drops known ogen warnings about header name case and problem+json status checks.
  go tool ogen \
    --config api/ogen.yaml \
    --target "$target" \
    --package codegen \
    --clean \
    "$spec" 2> >(grep -v -E 'not canonical|will not be enforced|Convenient errors' >&2)
}

generate_server api/bundle/openapi.yaml internal/server/codegen
generate_server api/emulator/bundle/openapi.yaml internal/emulator/server/codegen

go build ./...
