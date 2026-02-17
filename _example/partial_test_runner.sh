#!/bin/bash

set -e

DOMAIN=$1
if [ -z "$DOMAIN" ]; then
  echo "Usage: $0 <domain>"
  exit 1
fi

TEST_CASES_FILE="tests/${DOMAIN}/partial_response_cases.json"
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
  sleep 15
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
  EXPECTED_DATA=$(echo "$CASE" | jq -c '.expected')
  EXPECTED_ERRORS=$(echo "$CASE" | jq -c '.expectedErrors // []')
  
  # Get service to fail
  SERVICE_TO_FAIL=$(echo "$CASE" | jq -r '.mockFailure.service // ""')

  echo "Running test: $NAME..."

  if [ -n "$SERVICE_TO_FAIL" ]; then
    echo "  Stopping service: $SERVICE_TO_FAIL"
    cd "${DOMAIN}" && docker compose stop "$SERVICE_TO_FAIL"
    cd ..
    sleep 1
  fi

  # Send request to Gateway
  RESPONSE=$(curl -s -X POST -H "Content-Type: application/json" \
    -d "{\"query\": $(echo "$QUERY" | jq -R .), \"variables\": $VARIABLES}" \
    "${GATEWAY_URL}")

  # Restart the stopped service
  if [ -n "$SERVICE_TO_FAIL" ]; then
    echo "  Restarting service: $SERVICE_TO_FAIL"
    cd "${DOMAIN}" && docker compose start "$SERVICE_TO_FAIL"
    cd ..
    sleep 3
  fi

  ACTUAL_DATA=$(echo "$RESPONSE" | jq -S '.data')
  ACTUAL_ERRORS=$(echo "$RESPONSE" | jq -S '.errors // []')

  # Compare data
  DATA_DIFF=$(diff <(echo "$EXPECTED_DATA" | jq -S .) <(echo "$ACTUAL_DATA" | jq -S .) || true)

  # Compare errors (check if errors exist when expected)
  ERRORS_MATCH=true
  
  if [ "$EXPECTED_ERRORS" != "[]" ]; then
    # We expect errors
    if [ "$ACTUAL_ERRORS" = "[]" ] || [ "$ACTUAL_ERRORS" = "null" ]; then
      ERRORS_MATCH=false
      echo "  Expected errors but got none"
    else
      # Check if the number of errors matches
      EXPECTED_ERROR_COUNT=$(echo "$EXPECTED_ERRORS" | jq 'length')
      ACTUAL_ERROR_COUNT=$(echo "$ACTUAL_ERRORS" | jq 'length')
      
      if [ "$EXPECTED_ERROR_COUNT" != "$ACTUAL_ERROR_COUNT" ]; then
        ERRORS_MATCH=false
        echo "  Expected $EXPECTED_ERROR_COUNT errors but got $ACTUAL_ERROR_COUNT"
      else
        # Verify each error path exists in actual errors
        for j in $(seq 0 $((EXPECTED_ERROR_COUNT - 1))); do
          EXPECTED_PATH=$(echo "$EXPECTED_ERRORS" | jq -c ".[$j].path")
          FOUND=false
          
          for k in $(seq 0 $((ACTUAL_ERROR_COUNT - 1))); do
            ACTUAL_PATH=$(echo "$ACTUAL_ERRORS" | jq -c ".[$k].path")
            if [ "$EXPECTED_PATH" = "$ACTUAL_PATH" ]; then
              FOUND=true
              break
            fi
          done
          
          if [ "$FOUND" = "false" ]; then
            ERRORS_MATCH=false
            echo "  Expected error path $EXPECTED_PATH not found in actual errors"
            break
          fi
        done
      fi
    fi
  else
    # We don't expect errors
    if [ "$ACTUAL_ERRORS" != "[]" ] && [ "$ACTUAL_ERRORS" != "null" ]; then
      ERRORS_MATCH=false
      echo "  Expected no errors but got some"
    fi
  fi

  if [ -z "$DATA_DIFF" ] && [ "$ERRORS_MATCH" = "true" ]; then
    echo "SUCCESS: $NAME"
    PASSED=$((PASSED + 1))
  else
    echo "FAILED: $NAME"
    if [ -n "$DATA_DIFF" ]; then
      echo "Data difference:"
      echo "$DATA_DIFF"
    fi
    if [ "$ERRORS_MATCH" = "false" ]; then
      echo "Expected errors:"
      echo "$EXPECTED_ERRORS" | jq .
      echo "Actual errors:"
      echo "$ACTUAL_ERRORS" | jq .
    fi
    FAILED=$((FAILED + 1))
  fi
done

echo "Test Summary for ${DOMAIN}:"
echo "Passed: $PASSED"
echo "Failed: $FAILED"

if [ $FAILED -ne 0 ]; then
  exit 1
fi
