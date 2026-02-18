# Apollo Router Performance Comparison

This directory contains the setup for comparing go-graphql-federation-gateway with Apollo Router.

## Prerequisites

1. **hey** - HTTP load testing tool
   ```bash
   brew install hey
   ```

2. **Docker & Docker Compose** - For running services

## Setup

### Start Services

#### Option 1: Run go-graphql-federation-gateway

```bash
# From _example/ec directory
cd /Users/keisuke.nakamura/dev/federation-gateway/_example/ec

# Start subgraphs
docker compose up -d

# Start Go gateway (outside Docker)
cd ../..
go run cmd/go-graphql-federation-gateway/main.go serve --config _example/ec/gateway.yaml
```

Gateway will be available at: http://localhost:9000/graphql

#### Option 2: Run Apollo Router

```bash
# From _example/ec directory
cd /Users/keisuke.nakamura/dev/federation-gateway/_example/ec

# Start subgraphs + Apollo Router
docker compose -f docker-compose.apollo.yaml up -d
```

Apollo Router will be available at: http://localhost:9001

## Running Benchmarks

### Run All Tests

```bash
cd /Users/keisuke.nakamura/dev/federation-gateway/_example/ec
chmod +x benchmark.sh
./benchmark.sh
```

### Individual Test Examples

```bash
# Test Go Gateway
hey -n 10000 -c 50 -m POST \
  -H "Content-Type: application/json" \
  -d '{"query":"{ product(id: \"1\") { id name price } }"}' \
  http://localhost:9000/graphql

# Test Apollo Router
hey -n 10000 -c 50 -m POST \
  -H "Content-Type: application/json" \
  -d '{"query":"{ product(id: \"1\") { id name price } }"}' \
  http://localhost:9001
```

## Test Scenarios

### 1. Simple Query (Single Service)
```graphql
{
  product(id: "1") {
    id
    name
    price
  }
}
```
**Tests**: Basic routing performance

### 2. Cross-Service Query (Federation)
```graphql
{
  product(id: "1") {
    id
    name
    price
    reviews {
      id
      body
      authorName
    }
  }
}
```
**Tests**: Federation join performance across Products + Reviews services

### 3. @requires Query (Federation v2)
```graphql
{
  product(id: "1") {
    id
    name
    weight
    inStock
    shippingCost
  }
}
```
**Tests**: Federation v2 @requires dependency injection (Products → Inventory)

### 4. Mutation
```graphql
mutation {
  createProduct(name: "Test Product", price: 1000) {
    id
    name
    price
  }
}
```
**Tests**: Write operation performance

## Expected Metrics

### Go Gateway Baseline
- **Requests/sec**: 2000-5000 req/s (depending on hardware)
- **Average latency**: 10-30ms
- **P95 latency**: 20-50ms
- **P99 latency**: 30-100ms

### Apollo Router Baseline (Rust-based)
- **Requests/sec**: 5000-15000 req/s
- **Average latency**: 3-10ms
- **P95 latency**: 5-20ms
- **P99 latency**: 10-30ms

**Note**: Apollo Router is expected to be faster as it's written in Rust. The goal is to ensure go-graphql-federation-gateway has acceptable performance, not to match Apollo's highly optimized implementation.

## Interpreting Results

### Key Metrics

1. **Requests/sec** - Throughput capacity
2. **Average latency** - Typical response time
3. **P95/P99 latency** - Tail latency (user experience)
4. **Error rate** - Should be 0% for both gateways

### What to Look For

- ✅ Both gateways should have 0% error rate
- ✅ Go gateway should achieve >1000 req/s for simple queries
- ✅ @requires queries should not add excessive overhead (<2x simple query latency)
- ⚠️ Apollo Router will be faster - focus on "good enough" performance

## Architecture

```
                  ┌─────────────────┐
                  │   Client        │
                  └────────┬────────┘
                           │
              ┌────────────┴────────────┐
              │                         │
    ┌─────────▼─────────┐    ┌─────────▼──────────┐
    │  Go Gateway       │    │  Apollo Router     │
    │  (Port 9000)      │    │  (Port 9001)       │
    └─────────┬─────────┘    └─────────┬──────────┘
              │                         │
              └────────────┬────────────┘
                           │
         ┌─────────────────┼─────────────────┐
         │                 │                 │
    ┌────▼────┐     ┌─────▼─────┐    ┌─────▼─────┐
    │Products │     │  Reviews  │    │Inventory  │
    │  :8101  │     │   :8102   │    │  :8104    │
    └─────────┘     └───────────┘    └───────────┘
```

## Troubleshooting

### Services not responding

```bash
# Check service status
docker compose ps

# Check logs
docker compose logs products
docker compose logs apollo-router

# Restart all services
docker compose down
docker compose up -d
```

### Gateway not responding

```bash
# Test Go gateway
curl -X POST http://localhost:9000/graphql \
  -H "Content-Type: application/json" \
  -d '{"query":"{ __typename }"}'

# Test Apollo Router
curl -X POST http://localhost:9001 \
  -H "Content-Type: application/json" \
  -d '{"query":"{ __typename }"}'
```

## Cleanup

```bash
# Stop all services
docker compose -f docker-compose.apollo.yaml down

# Remove volumes
docker compose -f docker-compose.apollo.yaml down -v
```
