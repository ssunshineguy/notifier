#!/usr/bin/env bash
# Regenerates the API bundle and the server code from the OpenAPI spec.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

(cd api && redocly lint notifier && redocly bundle notifier)

# Drops known ogen warnings about header name case and problem+json status checks.
go tool ogen \
  --config api/ogen.yaml \
  --target internal/server/codegen \
  --package codegen \
  --clean \
  api/bundle/openapi.yaml 2> >(grep -v -E 'not canonical|will not be enforced|Convenient errors' >&2)

go build ./...
