#!/bin/bash

# Comprehensive all-domain comparative benchmark runner
# Delegates each domain to domain_benchmark.sh and aggregates the results.
# Usage: ./all_compare_benchmark_runner.sh
# Or via Makefile: make benchmark

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
NC='\033[0m'

# ── Fairness settings (override via env vars) ──────────────────────────────
# Both gateways run in Docker with identical resource limits (2 CPU / 512 MB).
# See docker-compose.gateway.yaml / docker-compose.apollo.yaml.
export TOTAL_REQUESTS=${TOTAL_REQUESTS:-10000}
export CONCURRENCY=${CONCURRENCY:-50}
export TIMEOUT=${TIMEOUT:-30}
export WARMUP_REQUESTS=${WARMUP_REQUESTS:-100}   # identical for both gateways
export WARMUP_CONCURRENCY=${WARMUP_CONCURRENCY:-10}
export WARMUP_SLEEP=${WARMUP_SLEEP:-3}
# ──────────────────────────────────────────────────────────────────────────

# Merged results file (all domains)
ALL_RESULTS="${SCRIPT_DIR}/benchmark_all_results.txt"
> "$ALL_RESULTS"

# ── Prerequisites ──────────────────────────────────────────────────────────
if ! command -v hey &>/dev/null; then
    echo -e "${RED}Error: 'hey' is not installed.${NC}"
    echo "Run: make setup"
    exit 1
fi

if ! docker ps >/dev/null 2>&1; then
    echo -e "${RED}Error: Docker daemon is not running.${NC}"
    exit 1
fi

GATEWAY_BINARY="${SCRIPT_DIR}/../cmd/go-graphql-federation-gateway/gateway"
if [ ! -f "$GATEWAY_BINARY" ]; then
    echo -e "${YELLOW}Gateway binary not found — building...${NC}"
    (cd "${SCRIPT_DIR}/.." && go build -o cmd/go-graphql-federation-gateway/gateway cmd/go-graphql-federation-gateway/main.go)
    echo -e "${GREEN}✓ Build complete${NC}"
fi
# ──────────────────────────────────────────────────────────────────────────

echo -e "${BLUE}╔═══════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║  Federation Gateway Benchmark  (Go Gateway vs Apollo)    ║${NC}"
echo -e "${BLUE}╚═══════════════════════════════════════════════════════════╝${NC}"
echo ""
echo "Fairness settings:"
echo "  Execution:   Both gateways run in Docker (same model)"
echo "  Resources:   2 CPU / 512 MB each (deploy.resources.limits)"
echo "  Requests:    ${TOTAL_REQUESTS} total, concurrency ${CONCURRENCY}"
echo "  Warmup:      ${WARMUP_REQUESTS} requests × concurrency ${WARMUP_CONCURRENCY}, then ${WARMUP_SLEEP}s sleep (identical for both)"
echo ""

# Initial cleanup
echo -e "${CYAN}Stopping any leftover containers...${NC}"
docker ps -aq | xargs -r docker stop >/dev/null 2>&1 || true
docker ps -aq | xargs -r docker rm  >/dev/null 2>&1 || true
sleep 2
echo -e "${GREEN}✓ Clean${NC}"
echo ""

# ── Domain definitions ──────────────────────────────────────────────────────
# Format: "domain|apollo_port|query|test_name"
DOMAINS=(
    "ec|9001|{\"query\":\"query ProductWithShipping { product(id: \\\"p1\\\") { id name price reviews { id body authorName } inStock shippingCost } }\"}|EC - @external + @requires (Cross-Service)"
    "fintech|9002|{\"query\":\"query CustomerWithAccounts { customer(id: \\\"1\\\") { id name tier accounts { iban balance riskScore } } }\"}|Fintech - @external + @requires (Risk Score)"
    "saas|9003|{\"query\":\"query OrganizationWithBilling { organization(id: \\\"org1\\\") { id name employeeCount billing { plan } monthlyCost } }\"}|SaaS - @external + @requires (Monthly Cost)"
    "social|9004|{\"query\":\"query UserWithPosts { user(id: \\\"user1\\\") { id name posts { id title likeCount comments { body } engagementScore } } }\"}|Social - @external + @requires (Engagement)"
    "travel|9005|{\"query\":\"query FlightWithBookings { flight(number: \\\"AA100\\\", departureDate: \\\"2026-03-01\\\") { number origin destination price bookings { id } totalCost } }\"}|Travel - @external + @requires (Total Cost)"
)

# ── Run each domain ─────────────────────────────────────────────────────────
FAILED_DOMAINS=()

for entry in "${DOMAINS[@]}"; do
    IFS='|' read -r domain apollo_port query test_name <<< "$entry"

    echo -e "${MAGENTA}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${CYAN}Domain: ${domain^^}${NC}"
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

    # Build subgraph images without cache
    echo -e "${CYAN}Building subgraph images (no-cache)...${NC}"
    docker compose build --no-cache > /dev/null 2>&1

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
    echo -e "${CYAN}Building Apollo Router image (no-cache)...${NC}"
    docker compose -f docker-compose.apollo.yaml build --no-cache > /dev/null 2>&1
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
    echo -e "${CYAN}Building Go Gateway image (no-cache)...${NC}"
    docker compose -f docker-compose.gateway.yaml build --no-cache > /dev/null 2>&1
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
done

# ── Summary ─────────────────────────────────────────────────────────────────
echo -e "${BLUE}╔═══════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║                  Benchmark Results Summary                ║${NC}"
echo -e "${BLUE}╚═══════════════════════════════════════════════════════════╝${NC}"
echo ""

if [ ! -s "$ALL_RESULTS" ]; then
    echo -e "${RED}No results available.${NC}"
    exit 1
fi

# Detailed table
printf "%-20s %-48s %12s %10s %10s %10s %10s %8s %12s\n" \
    "Gateway" "Test" "Req/sec" "Avg(s)" "P50(s)" "P95(s)" "P99(s)" "Err%" "Correct"
printf '%s\n' "$(printf '─%.0s' {1..145})"

while IFS='|' read -r gw dom test req avg p50 p95 p99 err correct; do
    printf "%-20s %-48s %12s %10s %10s %10s %10s %7s%% %12s\n" \
        "$gw" "$test" "$req" "$avg" "$p50" "$p95" "$p99" "$err" "$correct"
done < "$ALL_RESULTS"

echo ""

# Averages
GO_AVG=$(awk    -F'|' '/^Go-Gateway/    {sum+=$4; count++} END {if(count>0) printf "%.2f", sum/count; else print "0"}' "$ALL_RESULTS")
APOLLO_AVG=$(awk -F'|' '/^Apollo-Router/ {sum+=$4; count++} END {if(count>0) printf "%.2f", sum/count; else print "0"}' "$ALL_RESULTS")
GO_ERR=$(awk    -F'|' '/^Go-Gateway/    && $9!="N/A" {sum+=$9; c++} END {if(c>0) printf "%.2f", sum/c; else print "N/A"}' "$ALL_RESULTS")
APOLLO_ERR=$(awk -F'|' '/^Apollo-Router/ && $9!="N/A" {sum+=$9; c++} END {if(c>0) printf "%.2f", sum/c; else print "N/A"}' "$ALL_RESULTS")
GO_BAD=$(awk    -F'|' '/^Go-Gateway/    && $10!="✓" {c++} END {print c+0}' "$ALL_RESULTS")
APOLLO_BAD=$(awk -F'|' '/^Apollo-Router/ && $10!="✓" {c++} END {print c+0}' "$ALL_RESULTS")
GO_TOTAL=$(awk  -F'|' '/^Go-Gateway/    {c++} END {print c+0}' "$ALL_RESULTS")
APOLLO_TOTAL=$(awk -F'|' '/^Apollo-Router/ {c++} END {print c+0}' "$ALL_RESULTS")

echo -e "${CYAN}Overall averages:${NC}"
echo "  Go Gateway:     ${GO_AVG} req/s   err: ${GO_ERR}%   correctness issues: ${GO_BAD}/${GO_TOTAL}"
echo "  Apollo Router:  ${APOLLO_AVG} req/s   err: ${APOLLO_ERR}%   correctness issues: ${APOLLO_BAD}/${APOLLO_TOTAL}"
echo ""

# Speed comparison (throughput only — context: error rate and correctness above)
COMPARISON=$(awk -v go="$GO_AVG" -v apollo="$APOLLO_AVG" 'BEGIN {
    if (go > 0 && apollo > 0) {
        if (go > apollo) { printf "go|%.2f", go/apollo }
        else             { printf "apollo|%.2f", apollo/go }
    } else { print "n/a|0" }
}')
WINNER=$(echo "$COMPARISON" | cut -d'|' -f1)
RATIO=$(echo "$COMPARISON"  | cut -d'|' -f2)
if [ "$WINNER" = "go" ]; then
    echo -e "${GREEN}✓ Go Gateway is ${RATIO}x faster in throughput${NC}"
    echo -e "  (throughput only — see error rate & correctness above for full picture)"
elif [ "$WINNER" = "apollo" ]; then
    echo -e "${BLUE}Apollo Router is ${RATIO}x faster in throughput${NC}"
    echo -e "  (throughput only — see error rate & correctness above for full picture)"
fi

if [ ${#FAILED_DOMAINS[@]} -gt 0 ]; then
    echo ""
    echo -e "${YELLOW}⚠ Failed domains: ${FAILED_DOMAINS[*]}${NC}"
fi

echo ""
echo -e "${GREEN}✓ All benchmarks completed!${NC}"
echo "  Full results: ${ALL_RESULTS}"
echo "  Per-domain:   ${SCRIPT_DIR}/benchmark_<domain>_results.txt"
