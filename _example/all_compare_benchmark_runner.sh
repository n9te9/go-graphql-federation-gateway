#!/bin/bash

# Comprehensive all-domain comparative benchmark runner
# Compares go-graphql-federation-gateway vs Apollo Router across all 5 domains

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
NC='\033[0m' # No Color

# Configuration
TOTAL_REQUESTS=10000
CONCURRENCY=50
TIMEOUT=30
GO_GATEWAY_PORT=9000
GATEWAY_BINARY="../cmd/go-graphql-federation-gateway/gateway"

# Check if hey is installed
if ! command -v hey &> /dev/null; then
    echo -e "${RED}Error: 'hey' is not installed.${NC}"
    echo "Install it with: make setup"
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

echo -e "${BLUE}╔═══════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║  Federation Gateway Comprehensive Benchmark Comparison   ║${NC}"
echo -e "${BLUE}║  Go Gateway vs Apollo Router - All Domains               ║${NC}"
echo -e "${BLUE}╚═══════════════════════════════════════════════════════════╝${NC}"
echo ""
echo "Configuration:"
echo "  Total Requests: $TOTAL_REQUESTS"
echo "  Concurrency: $CONCURRENCY"
echo "  Timeout: ${TIMEOUT}s"
echo "  Domains: EC, Fintech, SaaS, Social, Travel"
echo ""

# Initial cleanup - stop all containers
echo -e "${CYAN}Cleaning up existing containers...${NC}"
docker ps -aq | xargs -r docker stop > /dev/null 2>&1 || true
docker ps -aq | xargs -r docker rm > /dev/null 2>&1 || true
sleep 2
echo -e "${GREEN}✓ Cleanup complete${NC}"
echo ""

# Function to wait for service to be ready
# Wait for service to be ready (same logic as test_runner.sh)
wait_for_service() {
    local url=$1
    local max_retries=5
    local count=0
    
    while ! curl -s -f -X POST "${url}" \
        -H "Content-Type: application/json" \
        -d '{"query":"{ __typename }"}' > /dev/null 2>&1; do
        count=$((count + 1))
        if [ $count -ge $max_retries ]; then
            echo -e "${RED}Service at ${url} failed to respond after ${max_retries} attempts${NC}"
            echo -e "${YELLOW}Checking container status...${NC}"
            docker compose ps
            echo -e "${YELLOW}Recent logs (last 30 lines):${NC}"
            docker compose logs --tail=30
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
    local query=$3
    local test_description=$4
    
    # Warmup
    echo "$query" > /tmp/gql_query.json
    for i in {1..10}; do
        curl -s -X POST "${gateway_url}" \
            -H "Content-Type: application/json" \
            -D /tmp/gql_query.json > /dev/null 2>&1 || true
    done
    sleep 2
    
    # Run benchmark
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
    P95_LATENCY=$(echo "$RESULT" | grep "95% in" | awk '{print $3}')
    ERROR_COUNT=$(echo "$RESULT" | grep "status code" | wc -l || echo "0")
    
    # Save results
    echo "${gateway_name}|${test_description}|${REQ_SEC}|${AVG_LATENCY}|${P95_LATENCY}|${ERROR_COUNT}" >> "$RESULTS_FILE"
    
    echo -e "    ${REQ_SEC} req/s, avg ${AVG_LATENCY}s, p95 ${P95_LATENCY}s"
}

# Function to benchmark a domain
benchmark_domain() {
    local domain=$1
    local apollo_port=$2
    local query=$3
    local test_name=$4
    
    echo -e "${MAGENTA}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${CYAN}Domain: ${domain}${NC}"
    echo -e "${MAGENTA}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    
    # Change to domain directory
    cd "$domain" || { echo "Failed to cd to $domain"; return 1; }
    
    # Start services
    echo -e "${CYAN}Starting ${domain} subgraph services...${NC}"
    
    if [ ! -f "docker-compose.apollo.yaml" ]; then
        echo -e "${RED}Error: Apollo Router setup not found for ${domain}${NC}"
        cd ..
        return 1
    fi
    
    # Step 1: Pull images first to avoid timeout during startup
    echo -e "${CYAN}Pulling Docker images...${NC}"
    docker compose pull > /dev/null 2>&1
    
    # Start subgraph services
    docker compose up -d > /dev/null 2>&1
    
    # Wait for services to initialize (same as test_runner.sh)
    echo -e "${CYAN}Waiting for subgraph services to initialize (30s)...${NC}"
    sleep 30
    
    # Quick health check for subgraphs
    echo -e "${CYAN}Checking subgraph services...${NC}"
    case "$domain" in
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
            echo -e "${RED}Unknown domain: ${domain}${NC}"
            docker compose down > /dev/null 2>&1
            cd ..
            return 1
            ;;
    esac
    
    for port in $SUBGRAPH_PORTS; do
        if ! wait_for_service "http://localhost:${port}/query"; then
            echo -e "${RED}Subgraph on port ${port} failed to start${NC}"
            docker compose down > /dev/null 2>&1
            cd ..
            return 1
        fi
    done
    echo -e "${GREEN}✓ All subgraphs ready${NC}"
    
    # Step 2: Start Apollo Router
    echo -e "${CYAN}Starting Apollo Router...${NC}"
    docker compose -f docker-compose.apollo.yaml up -d > /dev/null 2>&1
    sleep 5
    
    # Wait for Apollo Router
    echo -e "${CYAN}Checking Apollo Router (port ${apollo_port})...${NC}"
    if ! wait_for_service "http://localhost:${apollo_port}"; then
        echo -e "${RED}Apollo Router failed to start for ${domain}${NC}"
        docker compose -f docker-compose.apollo.yaml down > /dev/null 2>&1
        docker compose down > /dev/null 2>&1
        cd ..
        return 1
    fi
    echo -e "${GREEN}✓ Apollo Router ready${NC}"
    
    # Step 3: Start Go Gateway (Docker)
    echo -e "${CYAN}Starting Go Gateway (Docker)...${NC}"
    docker compose -f docker-compose.gateway.yaml up -d > /dev/null 2>&1
    sleep 5
    
    # Wait for Go Gateway
    echo -e "${CYAN}Checking Go Gateway...${NC}"
    if ! wait_for_service "http://localhost:${GO_GATEWAY_PORT}/graphql"; then
        echo -e "${RED}Go Gateway failed to start for ${domain}${NC}"
        docker compose -f docker-compose.gateway.yaml down > /dev/null 2>&1
        docker compose -f docker-compose.apollo.yaml down > /dev/null 2>&1
        docker compose down > /dev/null 2>&1
        cd ..
        return 1
    fi
    echo -e "${GREEN}✓ Go Gateway ready${NC}"
    echo ""
    
    # Run benchmarks
    echo -e "${YELLOW}Test: ${test_name}${NC}"
    echo -e "${GREEN}[Go Gateway]${NC}"
    run_gateway_benchmark "Go-${domain}" "http://localhost:${GO_GATEWAY_PORT}/graphql" "$query" "$test_name"
    
    echo -e "${GREEN}[Apollo Router]${NC}"
    run_gateway_benchmark "Apollo-${domain}" "http://localhost:${apollo_port}" "$query" "$test_name"
    echo ""
    
    # Cleanup
    echo -e "${CYAN}Cleaning up ${domain}...${NC}"
    docker compose -f docker-compose.gateway.yaml down > /dev/null 2>&1
    docker compose -f docker-compose.apollo.yaml down > /dev/null 2>&1
    docker compose down > /dev/null 2>&1
    cd ..
    echo ""
}

# Run benchmarks for all domains
echo -e "${BLUE}=== Starting All-Domain Benchmark ===${NC}"
echo ""

# EC Domain
benchmark_domain "ec" 9001 \
    '{"query":"{ product(id: \"1\") { id name price reviews { id body authorName } inStock shippingCost } }"}' \
    "EC - Cross-Service with @requires"

# Fintech Domain
benchmark_domain "fintech" 9002 \
    '{"query":"{ customer(id: \"1\") { id name tier accounts { iban balance riskScore } } }"}' \
    "Fintech - Account Risk Score (@requires)"

# SaaS Domain
benchmark_domain "saas" 9003 \
    '{"query":"{ organization(id: \"org1\") { id name employeeCount billing { plan } monthlyCost } }"}' \
    "SaaS - Organization Billing (@requires)"

# Social Domain
benchmark_domain "social" 9004 \
    '{"query":"{ user(id: \"user1\") { id name posts { id title likeCount comments { body } engagementScore } } }"}' \
    "Social - Post Engagement (@requires)"

# Travel Domain
benchmark_domain "travel" 9005 \
    '{"query":"{ flight(number: \"AA100\", departureDate: \"2026-03-01\") { number origin destination price bookings { id } totalCost } }"}' \
    "Travel - Flight Bookings (@requires)"

rm -f /tmp/gql_query.json

# Display summary
echo -e "${BLUE}╔═══════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║                  Benchmark Results Summary                ║${NC}"
echo -e "${BLUE}╚═══════════════════════════════════════════════════════════╝${NC}"
echo ""

printf "%-20s %-30s %15s %12s %12s\n" "Gateway" "Test" "Req/sec" "Avg (s)" "P95 (s)"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

while IFS='|' read -r gateway test req_sec avg_lat p95_lat errors; do
    printf "%-20s %-30s %15s %12s %12s\n" "$gateway" "$test" "$req_sec" "$avg_lat" "$p95_lat"
done < "$RESULTS_FILE"

echo ""

# Calculate averages
echo -e "${CYAN}Performance Comparison:${NC}"
echo ""

# Go Gateway average
GO_AVG=$(awk -F'|' '/^Go-/ {sum+=$3; count++} END {if(count>0) printf "%.2f", sum/count; else print "0"}' "$RESULTS_FILE")
# Apollo Router average
APOLLO_AVG=$(awk -F'|' '/^Apollo-/ {sum+=$3; count++} END {if(count>0) printf "%.2f", sum/count; else print "0"}' "$RESULTS_FILE")

echo -e "  ${GREEN}Go Gateway${NC}     avg: ${GO_AVG} req/s"
echo -e "  ${BLUE}Apollo Router${NC}  avg: ${APOLLO_AVG} req/s"
echo ""

if (( $(echo "$GO_AVG > 0 && $APOLLO_AVG > 0" | bc -l) )); then
    RATIO=$(echo "scale=2; $GO_AVG / $APOLLO_AVG" | bc)
    if (( $(echo "$RATIO > 1" | bc -l) )); then
        echo -e "  ${GREEN}✓ Go Gateway is ${RATIO}x faster on average${NC}"
    else
        RATIO=$(echo "scale=2; $APOLLO_AVG / $GO_AVG" | bc)
        echo -e "  ${BLUE}Apollo Router is ${RATIO}x faster on average${NC}"
    fi
fi

echo ""
echo -e "${GREEN}✓ Benchmark completed successfully!${NC}"

rm -f "$RESULTS_FILE"
