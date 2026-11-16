#!/usr/bin/env bash
# Regenerates the Swagger 2.0 docs served at /api/docs/ and the public
# OpenAPI 3 spec served at /openapi.json, both from the handler annotations.
# Needs swag (go install github.com/swaggo/swag/cmd/swag@v1.16.4).
set -euo pipefail
cd "$(dirname "$0")/.."
swag init -g pkg/api/server.go -o docs --outputTypes go,json,yaml >/dev/null
go run ./tools/openapi docs/swagger.json pkg/api pkg/api/openapi.json
echo "wrote docs/ and pkg/api/openapi.json"
