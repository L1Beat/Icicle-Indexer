#!/usr/bin/env bash
# Regenerates the Swagger 2.0 docs served at /api/docs/ and the public
# OpenAPI 3 spec served at /openapi.json, both from the handler annotations.
# Needs swag (go install github.com/swaggo/swag/cmd/swag@v1.16.4).
set -euo pipefail
cd "$(dirname "$0")/.."
swag init -g pkg/api/server.go -o docs --outputTypes go,json,yaml >/dev/null
# /api/v1/treasuries is served on the same host by the L1Beat backend, which
# publishes its own generated spec; it is merged in as published.
backend_spec="$(mktemp)"
trap 'rm -f "$backend_spec"' EXIT
curl -sSf --max-time 30 https://backend.l1beat.io/api/v1/openapi.json -o "$backend_spec"
go run ./tools/openapi docs/swagger.json pkg/api pkg/api/openapi.json "$backend_spec"
echo "wrote docs/ and pkg/api/openapi.json"
