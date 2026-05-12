#!/usr/bin/env bash
# test_runner.sh — Schema Registry E2E test
# Verifies: startup SDL fetch, runtime apply, rollback on invalid SDL, gateway stability.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

GREEN='\033[0;32m'
RED='\033[0;31m'
CYAN='\033[0;36m'
NC='\033[0m'

pass() { echo -e "${GREEN}✓ $1${NC}"; }
fail() { echo -e "${RED}✗ $1${NC}"; exit 1; }
info() { echo -e "${CYAN}→ $1${NC}"; }

# Admin bearer token must match `admin.auth.token` in gateway.yaml
# (or the GATEWAY_ADMIN_TOKEN env var passed to the gateway container).
ADMIN_TOKEN="${GATEWAY_ADMIN_TOKEN:-e2e-admin-token}"

# ---------------------------------------------------------------------------
# Cleanup
# ---------------------------------------------------------------------------
cleanup() {
  echo ""
  info "Cleaning up..."
  docker compose down --remove-orphans 2>/dev/null || true
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Wait for an HTTP endpoint to return 200 via POST { __typename }
# ---------------------------------------------------------------------------
wait_for_http() {
  local url="$1"
  local label="$2"
  local max=24          # 24 × 5 s = 2 min
  local count=0
  info "Waiting for ${label} at ${url} ..."
  while [ "$count" -lt "$max" ]; do
    if curl -sf -X POST "$url" \
        -H "Content-Type: application/json" \
        -d '{"query":"{ __typename }"}' > /dev/null 2>&1; then
      pass "${label} is ready"
      return 0
    fi
    count=$((count + 1))
    echo "  [${count}/${max}] not ready yet, waiting 5 s..."
    sleep 5
  done
  fail "${label} did not become ready after $((max * 5)) s"
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
echo "╔═══════════════════════════════════════════════════╗"
echo "║  Schema Registry E2E Test                         ║"
echo "╚═══════════════════════════════════════════════════╝"
echo ""

# --- Start services ---------------------------------------------------------
info "Building and starting services..."
docker compose up --build -d
pass "Services started"

# --- Wait for readiness -----------------------------------------------------
wait_for_http "http://localhost:8501/query" "products subgraph"
wait_for_http "http://localhost:8502/query" "reviews subgraph"
wait_for_http "http://localhost:9000/graphql" "gateway"

echo ""
echo "─── Test 1: Initial query ──────────────────────────"
RESP=$(curl -sf -X POST http://localhost:9000/graphql \
  -H "Content-Type: application/json" \
  -d '{"query":"{ product(id: \"1\") { id name price } }"}')

echo "  Response: ${RESP}"
echo "$RESP" | grep -q '"id"'    || fail "Test 1: 'id' missing in response"
echo "$RESP" | grep -q '"name"'  || fail "Test 1: 'name' missing in response"
echo "$RESP" | grep -q '"price"' || fail "Test 1: 'price' missing in response"
echo "$RESP" | grep -q '"errors"' && fail "Test 1: unexpected 'errors' in response"
pass "Test 1: Initial query returns expected fields"

echo ""
echo "─── Test 2: SDL update on subgraph ─────────────────"
SDL_V2='extend schema @link(url: "https://specs.apollo.dev/federation/v2.0", import: ["@key","@shareable"])
type Query { product(id: ID!): Product }
type Product @key(fields: "id") { id: ID! name: String! price: Int! description: String }'

PUT_STATUS=$(curl -sf -o /dev/null -w "%{http_code}" \
  -X PUT http://localhost:8501/schema \
  -H "Content-Type: text/plain" \
  --data-raw "$SDL_V2")
[ "$PUT_STATUS" = "200" ] || fail "Test 2: PUT /schema returned HTTP ${PUT_STATUS}"
pass "Test 2: SDL v2 uploaded to subgraph (HTTP 200)"

echo ""
echo "─── Test 3: Apply new schema on gateway ────────────"
APPLY_STATUS=$(curl -sf -o /dev/null -w "%{http_code}" \
  -X POST http://localhost:9000/products/apply \
  -H "Authorization: Bearer ${ADMIN_TOKEN}")
[ "$APPLY_STATUS" = "200" ] || fail "Test 3: POST /products/apply returned HTTP ${APPLY_STATUS}"
pass "Test 3: Gateway applied new schema (HTTP 200)"

echo ""
echo "─── Test 4: New field is queryable ──────────────────"
sleep 1   # brief pause for engine swap to propagate
NEW_RESP=$(curl -sf -X POST http://localhost:9000/graphql \
  -H "Content-Type: application/json" \
  -d '{"query":"{ product(id: \"1\") { id name price description } }"}')

echo "  Response: ${NEW_RESP}"
echo "$NEW_RESP" | grep -q '"description"' || fail "Test 4: 'description' field not returned after apply"
echo "$NEW_RESP" | grep -q '"errors"'      && fail "Test 4: unexpected 'errors' after apply"
pass "Test 4: New field 'description' is queryable"

echo ""
echo "─── Test 5: Rollback on invalid SDL ─────────────────"
curl -sf -X PUT http://localhost:8501/schema \
  -H "Content-Type: text/plain" \
  --data-raw 'this { is {{ not }} valid SDL' > /dev/null

INVALID_STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
  -X POST http://localhost:9000/products/apply \
  -H "Authorization: Bearer ${ADMIN_TOKEN}")
[ "$INVALID_STATUS" = "500" ] || fail "Test 5: expected HTTP 500 for invalid SDL, got ${INVALID_STATUS}"
pass "Test 5: Invalid SDL rejected (HTTP 500)"

echo ""
echo "─── Test 6: Gateway stable after rollback ───────────"
STABLE_RESP=$(curl -sf -X POST http://localhost:9000/graphql \
  -H "Content-Type: application/json" \
  -d '{"query":"{ product(id: \"1\") { id name price } }"}')

echo "  Response: ${STABLE_RESP}"
echo "$STABLE_RESP" | grep -q '"id"'    || fail "Test 6: gateway broken after rollback"
echo "$STABLE_RESP" | grep -q '"errors"' && fail "Test 6: unexpected errors after rollback"
pass "Test 6: Gateway still serves requests after rollback"

echo ""
echo "╔═══════════════════════════════════════════════════╗"
echo "║  All Schema Registry E2E Tests Passed ✓           ║"
echo "╚═══════════════════════════════════════════════════╝"
