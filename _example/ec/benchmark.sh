#!/bin/bash

# Performance comparison script between go-graphql-federation-gateway and Apollo Router
# Prerequisites: 
#   - hey tool installed: brew install hey
#   - Both gateways running

set -e

echo "=== GraphQL Federation Gateway Performance Benchmark ==="
echo ""

# Check if hey is installed
if ! command -v hey &> /dev/null; then
    echo "Error: 'hey' is not installed."
    echo "Install it with: brew install hey"
    exit 1
fi

# Configuration
GO_GATEWAY="http://localhost:9000/graphql"
APOLLO_ROUTER="http://localhost:9001"
TOTAL_REQUESTS=10000
CONCURRENCY=50
TIMEOUT=30

# Test queries - aligned with integration test queries
SIMPLE_QUERY=$(cat <<EOF
{
  "query": "query ProductBase { product(id: \\"p1\\") { id name price inStock } }"
}
EOF
)

CROSS_SERVICE_QUERY=$(cat <<EOF
{
  "query": "query ProductWithReviews { product(id: \\"p1\\") { name price reviews { body authorName } } }"
}
EOF
)

REQUIRES_QUERY=$(cat <<EOF
{
  "query": "query ProductWithShipping { product(id: \\"p1\\") { id name shippingCost } }"
}
EOF
)

MUTATION_QUERY=$(cat <<EOF
{
  "query": "mutation { createProduct(name: \\"Test Product\\", price: 1000) { id name price } }"
}
EOF
)

# Function to run benchmark
run_benchmark() {
    local name=$1
    local url=$2
    local query=$3
    
    echo "----------------------------------------"
    echo "Test: $name"
    echo "URL: $url"
    echo "----------------------------------------"
    
    # Create temp file for request body
    echo "$query" > /tmp/gql_query.json
    
    # Run hey with GraphQL-specific headers
    hey -n $TOTAL_REQUESTS \
        -c $CONCURRENCY \
        -t $TIMEOUT \
        -m POST \
        -H "Content-Type: application/json" \
        -D /tmp/gql_query.json \
        "$url"
    
    echo ""
}

# Check if gateways are running
echo "Checking gateway availability..."
if ! curl -s -o /dev/null -w "%{http_code}" "$GO_GATEWAY" | grep -q "200\|405"; then
    echo "Warning: go-graphql-federation-gateway ($GO_GATEWAY) may not be running"
fi

if ! curl -s -o /dev/null -w "%{http_code}" "$APOLLO_ROUTER" | grep -q "200\|405"; then
    echo "Warning: Apollo Router ($APOLLO_ROUTER) may not be running"
fi

echo ""
echo "Starting benchmarks with:"
echo "  Total Requests: $TOTAL_REQUESTS"
echo "  Concurrency: $CONCURRENCY"
echo "  Timeout: ${TIMEOUT}s"
echo ""

# Test 1: Simple Query (Products service only)
echo "=== Test 1: Simple Query (Single Service) ==="
run_benchmark "Go Gateway - Simple Query" "$GO_GATEWAY" "$SIMPLE_QUERY"
run_benchmark "Apollo Router - Simple Query" "$APOLLO_ROUTER" "$SIMPLE_QUERY"

# Test 2: Cross-Service Query (Products + Reviews)
echo "=== Test 2: Cross-Service Query (Federation) ==="
run_benchmark "Go Gateway - Cross-Service" "$GO_GATEWAY" "$CROSS_SERVICE_QUERY"
run_benchmark "Apollo Router - Cross-Service" "$APOLLO_ROUTER" "$CROSS_SERVICE_QUERY"

# Test 3: @requires Query (Products + Inventory with dependency)
echo "=== Test 3: @requires Query (Federation v2) ==="
run_benchmark "Go Gateway - @requires" "$GO_GATEWAY" "$REQUIRES_QUERY"
run_benchmark "Apollo Router - @requires" "$APOLLO_ROUTER" "$REQUIRES_QUERY"

# Test 4: Mutation
echo "=== Test 4: Mutation ==="
run_benchmark "Go Gateway - Mutation" "$GO_GATEWAY" "$MUTATION_QUERY"
run_benchmark "Apollo Router - Mutation" "$APOLLO_ROUTER" "$MUTATION_QUERY"

# Cleanup
rm -f /tmp/gql_query.json

echo ""
echo "=== Benchmark Complete ==="
echo ""
echo "Summary:"
echo "  - Simple Query: Tests basic routing performance"
echo "  - Cross-Service Query: Tests federation join performance"
echo "  - @requires Query: Tests Federation v2 dependency injection"
echo "  - Mutation: Tests write operation performance"
echo ""
echo "Key Metrics to Compare:"
echo "  - Requests/sec: Higher is better (throughput)"
echo "  - Average latency: Lower is better"
echo "  - P95/P99 latency: Lower is better (tail latency)"
echo "  - Error rate: Should be 0%"
