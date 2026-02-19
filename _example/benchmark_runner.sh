#!/bin/bash

# Benchmark runner for all domains
# Runs performance tests on go-graphql-federation-gateway for each domain

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
TOTAL_REQUESTS=5000
CONCURRENCY=50
TIMEOUT=30
GATEWAY_PORT=9000
GATEWAY_BINARY="../cmd/go-graphql-federation-gateway/gateway"

# Check if hey is installed
if ! command -v hey &> /dev/null; then
    echo -e "${RED}Error: 'hey' is not installed.${NC}"
    echo "Install it with: brew install hey"
    exit 1
fi

# Check if gateway binary exists
if [ ! -f "$GATEWAY_BINARY" ]; then
    echo -e "${YELLOW}Gateway binary not found. Building...${NC}"
    cd ..
    go build -o cmd/go-graphql-federation-gateway/gateway cmd/go-graphql-federation-gateway/main.go
    cd _example
fi

# Temporary file for results
RESULTS_FILE=$(mktemp)

echo -e "${BLUE}=== GraphQL Federation Gateway Benchmark ===${NC}"
echo ""
echo "Configuration:"
echo "  Total Requests: $TOTAL_REQUESTS"
echo "  Concurrency: $CONCURRENCY"
echo "  Timeout: ${TIMEOUT}s"
echo ""

# Function to wait for service to be ready
wait_for_service() {
    local url=$1
    local max_retries=10
    local count=0
    
    while ! curl -s -f -X POST "${url}" \
        -H "Content-Type: application/json" \
        -d '{"query":"{ __typename }"}' > /dev/null 2>&1; do
        count=$((count + 1))
        if [ $count -ge $max_retries ]; then
            return 1
        fi
        sleep 2
    done
    return 0
}

# Function to run benchmark for a domain
run_domain_benchmark() {
    local domain=$1
    local query=$2
    local description=$3
    
    echo -e "${GREEN}[${domain}]${NC} Starting benchmark: ${description}"
    
    # Start subgraphs
    cd "${domain}"
    docker compose up -d > /dev/null 2>&1
    
    # Wait for subgraph services to be ready
    echo -e "${GREEN}[${domain}]${NC} Waiting for subgraph services..."
    GATEWAY_CONFIG="gateway.yaml"
    
    # Extract service URLs from gateway.yaml
    SERVICE_URLS=$(grep -E '^\s+host:' "$GATEWAY_CONFIG" | awk '{print $2}')
    
    for url in $SERVICE_URLS; do
        if ! wait_for_service "$url"; then
            echo -e "${RED}[${domain}] Service ${url} failed to start${NC}"
            docker compose down > /dev/null 2>&1
            cd ..
            return 1
        fi
    done
    
    echo -e "${GREEN}[${domain}]${NC} All subgraph services ready"
    
    # Start gateway in background
    cd ..
    GATEWAY_PID=""
    (cd "${domain}" && ../../cmd/go-graphql-federation-gateway/gateway serve > /dev/null 2>&1) &
    GATEWAY_PID=$!
    
    # Wait for gateway to start
    echo -e "${GREEN}[${domain}]${NC} Waiting for gateway..."
    if ! wait_for_service "http://localhost:${GATEWAY_PORT}/graphql"; then
        echo -e "${RED}[${domain}] Gateway failed to start${NC}"
        kill $GATEWAY_PID 2>/dev/null || true
        docker compose -f "${domain}/docker-compose.yaml" down > /dev/null 2>&1
        return 1
    fi
    
    echo -e "${GREEN}[${domain}]${NC} Gateway ready, starting benchmark..."
    
    # Warmup: send a few requests to ensure everything is initialized
    echo "$query" > /tmp/gql_query.json
    for i in {1..10}; do
        curl -s -X POST "http://localhost:${GATEWAY_PORT}/graphql" \
            -H "Content-Type: application/json" \
            -D /tmp/gql_query.json > /dev/null 2>&1
    done
    sleep 1
    
    # Run benchmark
    echo "$query" > /tmp/gql_query.json
    
    RESULT=$(hey -n $TOTAL_REQUESTS \
        -c $CONCURRENCY \
        -t $TIMEOUT \
        -m POST \
        -H "Content-Type: application/json" \
        -D /tmp/gql_query.json \
        "http://localhost:${GATEWAY_PORT}/graphql" 2>&1)
    
    # Parse results
    REQ_SEC=$(echo "$RESULT" | grep "Requests/sec:" | awk '{print $2}')
    AVG_LATENCY=$(echo "$RESULT" | grep "Average:" | awk '{print $2" "$3}')
    P95_LATENCY=$(echo "$RESULT" | grep "95% in" | awk '{print $3" "$4}')
    P99_LATENCY=$(echo "$RESULT" | grep "99% in" | awk '{print $3" "$4}')
    ERROR_COUNT=$(echo "$RESULT" | grep -A 20 "Error distribution:" | grep -v "Error distribution:" | wc -l | xargs)
    
    # Save results
    echo "${domain}|${description}|${REQ_SEC}|${AVG_LATENCY}|${P95_LATENCY}|${P99_LATENCY}|${ERROR_COUNT}" >> "$RESULTS_FILE"
    
    echo -e "${GREEN}[${domain}]${NC} Completed: ${REQ_SEC} req/s"
    
    # Cleanup
    kill $GATEWAY_PID 2>/dev/null || true
    wait $GATEWAY_PID 2>/dev/null || true
    cd "${domain}"
    docker compose down > /dev/null 2>&1
    cd ..
    
    sleep 2  # Brief pause between domains
    echo ""
}

# EC Domain Benchmark
run_domain_benchmark "ec" \
    '{"query":"query ProductBase { product(id: \"p1\") { id name price inStock } }"}' \
    "Simple Query (Product)"

# Fintech Domain Benchmark  
run_domain_benchmark "fintech" \
    '{"query":"query GetCustomer { customer(id: \"1\") { id name tier } }"}' \
    "Simple Query (Customer)"

# SaaS Domain Benchmark
run_domain_benchmark "saas" \
    '{"query":"query GetOrganization { organization(id: \"org1\") { id name employeeCount } }"}' \
    "Simple Query (Organization)"

# Social Domain Benchmark
run_domain_benchmark "social" \
    '{"query":"query GetUser { user(id: \"user1\") { id name } }"}' \
    "Simple Query (User)"

# Travel Domain Benchmark
run_domain_benchmark "travel" \
    '{"query":"query GetFlight { flight(number: \"AA100\", departureDate: \"2026-03-01\") { number departureDate origin destination } }"}' \
    "Simple Query (Flight)"

# Cleanup temp file
rm -f /tmp/gql_query.json

# Display summary
echo -e "${BLUE}=== Benchmark Summary ===${NC}"
echo ""
printf "%-15s %-30s %12s %15s %15s %15s %10s\n" \
    "Domain" "Test" "Req/sec" "Avg Latency" "P95" "P99" "Errors"
echo "---------------------------------------------------------------------------------------------------------------------------"

while IFS='|' read -r domain desc req_sec avg p95 p99 errors; do
    printf "%-15s %-30s %12s %15s %15s %15s %10s\n" \
        "$domain" "$desc" "$req_sec" "$avg" "$p95" "$p99" "$errors"
done < "$RESULTS_FILE"

echo ""
echo -e "${GREEN}Benchmark completed successfully!${NC}"

# Cleanup
rm -f "$RESULTS_FILE"
