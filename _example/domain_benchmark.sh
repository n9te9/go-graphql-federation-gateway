#!/bin/bash

# Single domain comparative benchmark runner
# Usage: ./domain_benchmark.sh <domain> <apollo_port> <query> <test_name>
# Example: ./domain_benchmark.sh ec 9001 '{"query":"..."}' "EC Test"

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Arguments
DOMAIN=$1
APOLLO_PORT=$2
QUERY=$3
TEST_NAME=$4

if [ -z "$DOMAIN" ] || [ -z "$APOLLO_PORT" ] || [ -z "$QUERY" ] || [ -z "$TEST_NAME" ]; then
    echo -e "${RED}Error: Missing arguments${NC}"
    echo "Usage: $0 <domain> <apollo_port> <query> <test_name>"
    echo "Example: $0 ec 9001 '{\"query\":\"...\"}' \"EC Test\""
    exit 1
fi

# Configuration
TOTAL_REQUESTS=${TOTAL_REQUESTS:-10000}
CONCURRENCY=${CONCURRENCY:-50}
TIMEOUT=${TIMEOUT:-30}
GO_GATEWAY_PORT=9000
GATEWAY_BINARY="../cmd/go-graphql-federation-gateway/gateway"

# Output file (created in parent directory since we cd into domain)
RESULTS_FILE="../benchmark_${DOMAIN}_results.txt"

echo -e "${BLUE}╔═══════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║      Domain Benchmark: ${DOMAIN}${NC}"
echo -e "${BLUE}╚═══════════════════════════════════════════════════════════╝${NC}"
echo ""

# Wait for service to be ready
wait_for_service() {
    local url=$1
    local max_retries=30
    local count=0
    
    while ! curl -s -f -X POST "${url}" \
        -H "Content-Type: application/json" \
        -d '{"query":"{ __typename }"}' > /dev/null 2>&1; do
        count=$((count + 1))
        if [ $count -ge $max_retries ]; then
            echo -e "${RED}Service at ${url} failed to respond${NC}"
            return 1
        fi
        sleep 1
    done
    return 0
}

# Function to run benchmark for a gateway
run_gateway_benchmark() {
    local gateway_name=$1
    local gateway_url=$2
    local query_data=$3
    
    # Warmup
    echo "$query_data" > /tmp/gql_query.json
    for i in {1..10}; do
        curl -s -X POST "${gateway_url}" \
            -H "Content-Type: application/json" \
            -d @/tmp/gql_query.json > /dev/null 2>&1 || true
    done
    sleep 2
    
    # Run benchmark
    echo -e "${CYAN}Running benchmark for ${gateway_name}...${NC}"
    RESULT=$(hey -n $TOTAL_REQUESTS \
        -c $CONCURRENCY \
        -t $TIMEOUT \
        -m POST \
        -H "Content-Type: application/json" \
        -D /tmp/gql_query.json \
        "${gateway_url}" 2>&1)
    
    # Parse results
    REQ_SEC=$(echo "$RESULT" | grep "Requests/sec:" | awk '{print $2}')
    AVG_LATENCY=$(echo "$RESULT" | grep "Average:" | awk '{print $2}')
    P50_LATENCY=$(echo "$RESULT" | grep "50% in" | awk '{print $3}')
    P95_LATENCY=$(echo "$RESULT" | grep "95% in" | awk '{print $3}')
    P99_LATENCY=$(echo "$RESULT" | grep "99% in" | awk '{print $3}')
    
    # Save results in CSV format
    echo "${gateway_name}|${DOMAIN}|${TEST_NAME}|${REQ_SEC}|${AVG_LATENCY}|${P50_LATENCY}|${P95_LATENCY}|${P99_LATENCY}" >> "$RESULTS_FILE"
    
    echo -e "${GREEN}[${gateway_name}]${NC} ${REQ_SEC} req/s, avg ${AVG_LATENCY}s, p95 ${P95_LATENCY}s"
}

# Change to domain directory
cd "$DOMAIN" || { echo -e "${RED}Failed to cd to ${DOMAIN}${NC}"; exit 1; }

# Initialize results file
> "$RESULTS_FILE"

# Start services
echo -e "${CYAN}Starting ${DOMAIN} subgraph services...${NC}"
docker compose pull > /dev/null 2>&1
docker compose up -d > /dev/null 2>&1

echo -e "${CYAN}Waiting for subgraph services (30s)...${NC}"
sleep 30

# Determine subgraph ports
case "$DOMAIN" in
    "ec")
        SUBGRAPH_PORTS="8101 8102 8103 8104"
        ;;
    "fintech")
        SUBGRAPH_PORTS="8201 8202 8203"
        ;;
    "saas")
        SUBGRAPH_PORTS="8501 8502 8503"
        ;;
    "social")
        SUBGRAPH_PORTS="8301 8302 8303"
        ;;
    "travel")
        SUBGRAPH_PORTS="8401 8402"
        ;;
    *)
        echo -e "${RED}Unknown domain: ${DOMAIN}${NC}"
        docker compose down > /dev/null 2>&1
        exit 1
        ;;
esac

# Check subgraphs
echo -e "${CYAN}Checking subgraph services...${NC}"
for port in $SUBGRAPH_PORTS; do
    if ! wait_for_service "http://localhost:${port}/query"; then
        echo -e "${RED}Subgraph on port ${port} failed${NC}"
        docker compose down > /dev/null 2>&1
        cd ..
        exit 1
    fi
done
echo -e "${GREEN}✓ All subgraphs ready${NC}"

# Start Apollo Router
echo -e "${CYAN}Starting Apollo Router...${NC}"
docker compose -f docker-compose.apollo.yaml up -d > /dev/null 2>&1
sleep 5

if ! wait_for_service "http://localhost:${APOLLO_PORT}"; then
    echo -e "${RED}Apollo Router failed${NC}"
    docker compose -f docker-compose.apollo.yaml down > /dev/null 2>&1
    docker compose down > /dev/null 2>&1
    cd ..
    exit 1
fi
echo -e "${GREEN}✓ Apollo Router ready${NC}"

# Start Go Gateway
echo -e "${CYAN}Starting Go Gateway...${NC}"
docker compose -f docker-compose.gateway.yaml up -d > /dev/null 2>&1
sleep 5

if ! wait_for_service "http://localhost:${GO_GATEWAY_PORT}/graphql"; then
    echo -e "${RED}Go Gateway failed${NC}"
    docker compose -f docker-compose.gateway.yaml down > /dev/null 2>&1
    docker compose -f docker-compose.apollo.yaml down > /dev/null 2>&1
    docker compose down > /dev/null 2>&1
    cd ..
    exit 1
fi
echo -e "${GREEN}✓ Go Gateway ready${NC}"
echo ""

# Run benchmarks
echo -e "${YELLOW}Running benchmarks...${NC}"
run_gateway_benchmark "Go-Gateway" "http://localhost:${GO_GATEWAY_PORT}/graphql" "$QUERY"
run_gateway_benchmark "Apollo-Router" "http://localhost:${APOLLO_PORT}" "$QUERY"
echo ""

# Cleanup
echo -e "${CYAN}Cleaning up ${DOMAIN}...${NC}"
docker compose -f docker-compose.gateway.yaml down > /dev/null 2>&1
docker compose -f docker-compose.apollo.yaml down > /dev/null 2>&1
docker compose down > /dev/null 2>&1
cd ..

rm -f /tmp/gql_query.json

echo -e "${GREEN}✓ Benchmark for ${DOMAIN} completed${NC}"
echo -e "${CYAN}Results saved to: _example/benchmark_${DOMAIN}_results.txt${NC}"
