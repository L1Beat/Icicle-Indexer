#!/usr/bin/env bash
#
# smoke_test.sh — post-cutover smoke test for the public Icicle API.
#
# Hits every route family in pkg/api/server.go, verifies status codes,
# the security-critical headers (TLS, /metrics lockdown, CORS, cache),
# and probes per-IP rate limiting. Derives real path params (block number,
# tx hash, chain id) from the list endpoints so it works against live data.
#
# Usage:
#   ./scripts/smoke_test.sh                         # tests https://api.l1beat.io
#   BASE_URL=https://api.l1beat.io ./scripts/smoke_test.sh
#   BASE_URL=http://localhost:8080 ./scripts/smoke_test.sh   # local, skips TLS check
#   CHAIN_ID=43114 ./scripts/smoke_test.sh
#
# Exit code is non-zero if any check fails.

set -uo pipefail

BASE_URL="${BASE_URL:-https://api.l1beat.io}"
CHAIN_ID="${CHAIN_ID:-43114}"   # C-Chain
CURL="curl -sS --max-time 15"
# The API rate-limits to ~60/min per IP. This script makes more requests than
# that in a burst, so every request retries on 429 (the limiter refills ~1
# token/sec). Without this the test rate-limits itself and reports false fails.
MAX_RETRIES="${MAX_RETRIES:-8}"
RETRY_SLEEP="${RETRY_SLEEP:-1.2}"

PASS=0
FAIL=0
FAILED_CHECKS=""

# ---- helpers ---------------------------------------------------------------

# pass NAME / fail NAME DETAIL
pass() { PASS=$((PASS + 1)); printf '  PASS  %s\n' "$1"; }
fail() {
  FAIL=$((FAIL + 1))
  FAILED_CHECKS="${FAILED_CHECKS}\n  - $1: $2"
  printf '  FAIL  %s — %s\n' "$1" "$2"
}

# http_status PATH -> prints status code, retrying past 429s (self-pacing).
http_status() {
  local path="$1" code i
  for ((i = 0; i <= MAX_RETRIES; i++)); do
    code=$($CURL -o /dev/null -w '%{http_code}' "${BASE_URL}${path}" 2>/dev/null)
    [ "$code" != "429" ] && break
    sleep "$RETRY_SLEEP"
  done
  printf '%s' "$code"
}

# fetch_body PATH -> prints response body, retrying past 429s.
fetch_body() {
  local path="$1" body i
  for ((i = 0; i <= MAX_RETRIES; i++)); do
    body=$($CURL "${BASE_URL}${path}" 2>/dev/null)
    printf '%s' "$body" | grep -q 'RATE_LIMITED' || break
    sleep "$RETRY_SLEEP"
  done
  printf '%s' "$body"
}

# fetch_headers PATH -> prints response headers, retrying past 429s.
fetch_headers() {
  local path="$1" hdr i
  for ((i = 0; i <= MAX_RETRIES; i++)); do
    hdr=$($CURL -D - -o /dev/null "${BASE_URL}${path}" 2>/dev/null)
    printf '%s' "$hdr" | grep -qiE '^HTTP/[0-9.]+ 429' || break
    sleep "$RETRY_SLEEP"
  done
  printf '%s' "$hdr"
}

# expect_status NAME PATH CODE [CODE2 ...]
expect_status() {
  local name="$1" path="$2"; shift 2
  local got; got=$(http_status "$path")
  local code
  for code in "$@"; do
    if [ "$got" = "$code" ]; then pass "$name ($got)"; return; fi
  done
  fail "$name" "GET $path returned $got, wanted [$*]"
}

# expect_header NAME PATH HEADER SUBSTRING
expect_header() {
  local name="$1" path="$2" header="$3" want="$4"
  local val
  val=$(fetch_headers "$path" | grep -i "^${header}:" | tr -d '\r')
  if printf '%s' "$val" | grep -qi "$want"; then
    pass "$name ($val)"
  else
    fail "$name" "header '$header' on $path was '${val:-<absent>}', wanted to contain '$want'"
  fi
}

section() { printf '\n=== %s ===\n' "$1"; }

# ---- preflight -------------------------------------------------------------

printf 'Smoke testing: %s  (chain %s)\n' "$BASE_URL" "$CHAIN_ID"

if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required" >&2; exit 2
fi

# ---- 1. TLS ----------------------------------------------------------------

section "TLS"
if [[ "$BASE_URL" == https://* ]]; then
  host="${BASE_URL#https://}"; host="${host%%/*}"
  if command -v openssl >/dev/null 2>&1; then
    enddate=$(echo | openssl s_client -servername "$host" -connect "${host}:443" 2>/dev/null \
      | openssl x509 -noout -enddate 2>/dev/null | cut -d= -f2)
    if [ -n "$enddate" ]; then
      pass "TLS cert present (expires: $enddate)"
    else
      fail "TLS cert" "could not read certificate for $host"
    fi
  else
    echo "  SKIP  openssl not installed"
  fi
  # plain curl already validates the cert chain; a failure here means bad/expired cert
  if $CURL -o /dev/null "${BASE_URL}/health"; then
    pass "TLS chain validates (curl did not reject cert)"
  else
    fail "TLS chain" "curl rejected the certificate — expired or misconfigured"
  fi
else
  echo "  SKIP  non-https BASE_URL"
fi

# ---- 2. System endpoints ---------------------------------------------------

section "System"
expect_status "health"          "/health"          200
expect_status "swagger docs"    "/api/docs/"       200 301

# /metrics MUST NOT be publicly readable (token-gated, or not registered).
metrics_code=$(http_status "/metrics")
if [ "$metrics_code" = "200" ]; then
  fail "/metrics locked down" "returned 200 WITHOUT a token — Prometheus metrics are public"
else
  pass "/metrics not public ($metrics_code)"
fi

# ---- 3. Data API -----------------------------------------------------------

section "Data API"
expect_status "list chains"       "/api/v1/data/chains"                         200
expect_status "chain risk"        "/api/v1/data/chains/${CHAIN_ID}/risk"        200 404
expect_status "list blocks"       "/api/v1/data/evm/${CHAIN_ID}/blocks"         200
expect_status "list txs"          "/api/v1/data/evm/${CHAIN_ID}/txs"            200
expect_status "list stablecoins"  "/api/v1/data/evm/${CHAIN_ID}/stablecoins"    200
expect_status "stablecoin series" "/api/v1/data/evm/${CHAIN_ID}/stablecoins/timeseries?metric=supply" 200
expect_status "list pchain txs"   "/api/v1/data/pchain/txs"                     200
expect_status "pchain tx-types"   "/api/v1/data/pchain/tx-types"                200
expect_status "pchain tx-types 30d" "/api/v1/data/pchain/tx-types?days=30"      200
expect_status "list validators"   "/api/v1/data/validators"                     200
expect_status "pchain stats"      "/api/v1/data/pchain/stats"                   200
expect_status "subnet timeline"   "/api/v1/data/pchain/subnet-timeline"         200
expect_status "list pchain blocks" "/api/v1/data/pchain/blocks"                 200

# Derive real path params from the live data so the {param} routes get exercised.
latest_block=$(fetch_body "/api/v1/data/evm/${CHAIN_ID}/blocks?limit=1" \
  | grep -oE '"block_number"[: ]*[0-9]+' | grep -oE '[0-9]+' | head -1)
if [ -n "${latest_block:-}" ]; then
  expect_status "get block by number" "/api/v1/data/evm/${CHAIN_ID}/blocks/${latest_block}" 200
else
  fail "get block by number" "could not extract a block number from the blocks list"
fi

tx_hash=$(fetch_body "/api/v1/data/evm/${CHAIN_ID}/txs?limit=1" \
  | grep -oE '0x[0-9a-fA-F]{64}' | head -1)
if [ -n "${tx_hash:-}" ]; then
  expect_status "get tx by hash" "/api/v1/data/evm/${CHAIN_ID}/txs/${tx_hash}" 200
else
  echo "  SKIP  get tx by hash (no tx found in list)"
fi

# Derive a real P-Chain block number from the blocks list, then exercise the
# single-block route (which pulls proposer/parent fields out of tx_data).
pchain_block=$(fetch_body "/api/v1/data/pchain/blocks?limit=1" \
  | grep -oE '"block_number"[: ]*[0-9]+' | grep -oE '[0-9]+' | head -1)
if [ -n "${pchain_block:-}" ]; then
  expect_status "get pchain block" "/api/v1/data/pchain/blocks/${pchain_block}" 200
else
  echo "  SKIP  get pchain block (no block found in list)"
fi

# ---- 4. Metrics API --------------------------------------------------------

section "Metrics API"
expect_status "fee metrics"     "/api/v1/metrics/fees"                              200
# fees/daily requires a subnet_id — derive a real one from the fees list.
subnet_id=$(fetch_body "/api/v1/metrics/fees?limit=1" \
  | grep -oE '"subnet_id"[: ]*"[^"]+"' | grep -oE '[A-Za-z0-9]{40,}' | head -1)
if [ -n "${subnet_id:-}" ]; then
  expect_status "fee daily" "/api/v1/metrics/fees/daily?subnet_id=${subnet_id}" 200
else
  fail "fee daily" "could not derive a subnet_id from /api/v1/metrics/fees"
fi
expect_status "chain stats"     "/api/v1/metrics/evm/${CHAIN_ID}/stats"             200
expect_status "timeseries list" "/api/v1/metrics/evm/${CHAIN_ID}/timeseries"        200
expect_status "fees burned"     "/api/v1/metrics/evm/${CHAIN_ID}/fees/burned"       200
expect_status "network burned"  "/api/v1/metrics/burned/total"                      200
expect_status "indexer status"  "/api/v1/metrics/indexer/status"                    200
# storage stats is operator-gated behind the metrics bearer token — unauthenticated must be rejected
expect_status "storage gated"   "/api/v1/metrics/storage"                           401 404

# ---- 5. Input validation (should 4xx, never 5xx) ---------------------------

section "Input validation"
expect_status "bad chainId"  "/api/v1/data/evm/notanumber/blocks"                  400 404
expect_status "bad address"  "/api/v1/data/evm/${CHAIN_ID}/address/0xZZZ/txs"      400 404
expect_status "bad tx hash"  "/api/v1/data/evm/${CHAIN_ID}/txs/0xdeadbeef"         400 404
expect_status "unknown route" "/api/v1/data/does-not-exist"                        404

# ---- 6. Headers (security + caching) ---------------------------------------

section "Headers"
expect_header "CORS allow-origin"  "/api/v1/data/chains"            "Access-Control-Allow-Origin" "*"
expect_header "Cache-Control set"  "/api/v1/data/chains"            "Cache-Control"               "max-age"
# Live endpoints must NOT be edge-cached. Correct behavior is either no
# Cache-Control header at all, or an explicit non-cacheable directive.
cc=$(fetch_headers "/api/v1/metrics/indexer/status" | grep -i "^cache-control:" | tr -d '\r')
if [ -z "$cc" ] || printf '%s' "$cc" | grep -qiE "no-store|no-cache|max-age=0"; then
  pass "live not cached (${cc:-no Cache-Control header})"
else
  fail "live not cached" "indexer status returned a cacheable header: $cc"
fi

# dotfile scanners are blocked by nginx (returns 444 -> curl sees empty reply / 000).
# Swallow curl's "empty reply" stderr — a closed connection is the expected result.
dot_code=$(http_status "/.env" 2>/dev/null)
if [ "$dot_code" = "200" ]; then
  fail "dotfile blocked" "/.env returned 200 — scanner protection not active"
else
  pass "dotfile blocked (/.env -> $dot_code)"
fi

# ---- 7. WebSocket ----------------------------------------------------------

section "WebSocket"
ws_url="${BASE_URL/http/ws}/ws/blocks/${CHAIN_ID}"
# Verify the upgrade handshake returns 101 Switching Protocols. Uses curl only
# (no websocat/timeout dependency — `timeout` isn't a macOS builtin). curl
# reports 101 as soon as the response headers arrive; --max-time caps the read
# so the open stream doesn't hang the script.
ws_code=""
for ((i = 0; i <= MAX_RETRIES; i++)); do
  ws_code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 4 \
    -H "Connection: Upgrade" -H "Upgrade: websocket" \
    -H "Sec-WebSocket-Version: 13" -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
    "${BASE_URL}/ws/blocks/${CHAIN_ID}" 2>/dev/null)
  [ "$ws_code" != "429" ] && break
  sleep "$RETRY_SLEEP"
done
if [ "$ws_code" = "101" ]; then
  pass "websocket upgrade ($ws_url -> 101)"
else
  fail "websocket" "upgrade to $ws_url returned '${ws_code:-none}', wanted 101"
fi

# ---- 8. Rate limiting (LAST — it intentionally drains this IP's bucket) ----

section "Rate limiting"
# Default is 60/min, burst 10. Fire a modest burst and expect at least one 429.
# Kept small so a re-run isn't throttled for long (bucket refills ~1/sec).
# This runs last on purpose: draining the bucket would otherwise make the
# earlier checks return 429.
saw_429=0
for _ in $(seq 1 30); do
  c=$($CURL -o /dev/null -w '%{http_code}' "${BASE_URL}/api/v1/data/chains")
  if [ "$c" = "429" ]; then saw_429=1; break; fi
done
if [ "$saw_429" = "1" ]; then
  pass "rate limit enforced (got 429 under burst)"
else
  fail "rate limit" "fired 30 rapid requests, never got 429 — limiter may not be keying on client IP (check --trusted-proxies / nginx X-Real-IP)"
fi
echo "  NOTE  this drained your IP's token bucket — wait ~30s before re-running so the other checks don't see 429."

# ---- summary ---------------------------------------------------------------

section "Summary"
printf 'Passed: %d   Failed: %d\n' "$PASS" "$FAIL"
if [ "$FAIL" -gt 0 ]; then
  printf 'Failures:%b\n' "$FAILED_CHECKS"
  exit 1
fi
echo "All checks passed."
