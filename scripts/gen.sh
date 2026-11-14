#!/usr/bin/env bash
# Regenerates API bundles and server code from the OpenAPI specs.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

(cd build/openapi && redocly lint notifier emulator && redocly bundle notifier emulator)

generate_server() {
  local spec="$1"
  local target="$2"

  # Drops known ogen warnings about header name case and problem+json status checks.
  go tool ogen \
    --config build/openapi/ogen.yaml \
    --target "$target" \
    --package codegen \
    --clean \
    "$spec" 2> >(grep -v -E 'not canonical|will not be enforced|Convenient errors' >&2)
}

generate_server notifier/api/bundle/openapi.yaml notifier/internal/server/codegen
generate_server emulator/api/bundle/openapi.yaml emulator/internal/server/codegen

go build ./...
