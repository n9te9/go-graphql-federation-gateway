#!/bin/bash

set -e

DOMAIN=$1
if [ -z "$DOMAIN" ]; then
  echo "Usage: $0 <domain>"
  exit 1
fi

TEST_CASES_FILE="tests/${DOMAIN}/cases.json"
GATEWAY_URL="http://localhost:9000/graphql"

# Function to wait for service to be ready
wait_for_service() {
  local url=$1
  local service_name=$2
  local max_retries=5
  local wait_seconds=30
  local count=0
  
  echo "  Waiting for ${service_name} at ${url}..."
  while ! curl -s -f -X POST "${url}" \
    -H "Content-Type: application/json" \
    -d '{"query":"{ __typename }"}' > /dev/null 2>&1; do
    count=$((count + 1))
    if [ $count -ge $max_retries ]; then
      echo "  ERROR: ${service_name} failed to start after ${max_retries} retries"
      return 1
    fi
    echo "  Retry ${count}/${max_retries} - waiting ${wait_seconds} seconds..."
    sleep $wait_seconds
  done
  echo "  ${service_name} is ready!"
  return 0
}

# 1. Start subgraphs
echo "Starting subgraphs for ${DOMAIN}..."
cd "${DOMAIN}" && docker compose up -d
cd ..

# 2. Wait for all subgraph services to be ready
echo "Waiting for subgraph services to be ready..."
GATEWAY_CONFIG="${DOMAIN}/gateway.yaml"

# Extract service URLs from gateway.yaml and wait for each
SERVICE_URLS=$(yq eval '.services[].host' "$GATEWAY_CONFIG" 2>/dev/null || \
               grep 'http://localhost:' "$GATEWAY_CONFIG" | sed -E 's/.*host: (http:\/\/localhost:[0-9]+\/query).*/\1/g')

if [ -z "$SERVICE_URLS" ]; then
  echo "Warning: Could not extract service URLs, waiting with sleep..."
  sleep 30
else
  for url in $SERVICE_URLS; do
    service_name=$(echo "$url" | sed -E 's/.*:([0-9]+).*/\1/')
    wait_for_service "$url" "service-${service_name}" || exit 1
  done
fi

# Function to cleanup
cleanup() {
  echo "Stopping gateway and subgraphs..."
  kill $(lsof -t -i :9000) || true
  cd "${DOMAIN}" && docker compose down
  cd ..
}

trap cleanup EXIT

# 3. Start Gateway
echo "Starting Gateway..."
cd ${DOMAIN}
go run ../../cmd/go-graphql-federation-gateway/main.go serve &
GATEWAY_PID=$!
cd ..

# 4. Wait for Gateway to be ready
echo "Waiting for Gateway to start..."
MAX_RETRIES=30
COUNT=0
while ! curl -s "${GATEWAY_URL}" > /dev/null; do
  sleep 1
  COUNT=$((COUNT + 1))
  if [ $COUNT -ge $MAX_RETRIES ]; then
    echo "Gateway failed to start"
    exit 1
  fi
done
echo "Gateway is up!"

# 5. Run tests
if [ ! -f "$TEST_CASES_FILE" ]; then
  echo "Test cases file not found: $TEST_CASES_FILE"
  exit 1
fi

PASSED=0
FAILED=0

# Read cases using jq
NUM_CASES=$(jq '. | length' "$TEST_CASES_FILE")

for i in $(seq 0 $((NUM_CASES - 1))); do
  CASE=$(jq -c ".[$i]" "$TEST_CASES_FILE")
  NAME=$(echo "$CASE" | jq -r '.name')
  QUERY=$(echo "$CASE" | jq -r '.query')
  VARIABLES=$(echo "$CASE" | jq -c '.variables')
  EXPECTED=$(echo "$CASE" | jq -c '.expected')

  echo "Running test: $NAME..."

  # Send request to Gateway
  RESPONSE=$(curl -s -X POST -H "Content-Type: application/json" \
    -d "{\"query\": $(echo "$QUERY" | jq -R .), \"variables\": $VARIABLES}" \
    "${GATEWAY_URL}")

  ACTUAL=$(echo "$RESPONSE" | jq -S '.data')

  # Compare
  DIFF=$(diff <(echo "$EXPECTED" | jq -S .) <(echo "$ACTUAL" | jq -S .) || true)

  if [ -z "$DIFF" ]; then
    echo "SUCCESS: $NAME"
    PASSED=$((PASSED + 1))
  else
    echo "FAILED: $NAME"
    echo "Difference:"
    echo "$DIFF"
    FAILED=$((FAILED + 1))
  fi
done

echo "Test Summary for ${DOMAIN}:"
echo "Passed: $PASSED"
echo "Failed: $FAILED"

if [ $FAILED -ne 0 ]; then
  exit 1
fi
