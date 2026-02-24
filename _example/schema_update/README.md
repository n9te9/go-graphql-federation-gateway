# Schema Update Example

This example demonstrates the **Gateway Schema Registry** feature:
how to dynamically update a subgraph schema at runtime **without restarting the gateway**.

## Overview

The gateway no longer loads subgraph schemas from local `.graphqls` files. Instead it:

1. **At startup** — fetches each subgraph's SDL via `POST /query` with `{ _service { sdl } }`
2. **On update** — accepts `POST /{name}/apply` to trigger an on-demand SDL re-fetch, recomposes the supergraph, and atomically swaps in the new engine
3. **On failure** — keeps the previous schema if composition fails; panics during swap trigger automatic rollback

```
Subgraph A  ←──── POST /query { _service { sdl } } ────  Gateway (startup)
Subgraph A  ────  POST /A/apply                    ────→ Gateway (runtime update trigger)
                                                          Gateway ──→ POST /query ──→ Subgraph A (re-fetch)
                                                          Gateway composes new supergraph
                                                          Gateway atomically swaps engine
```

## Quick Start (Docker Compose)

```bash
# Start all services (mock subgraphs + gateway)
cd _example/schema_update
docker compose up --build

# Send a GraphQL query
curl -X POST http://localhost:9000/graphql \
  -H "Content-Type: application/json" \
  -d '{"query":"{ product(id: \"1\") { id name price } }"}'

# 1. Update the SDL on the products subgraph (adds description field)
curl -X PUT http://localhost:8501/schema \
  -H "Content-Type: text/plain" \
  --data-raw 'extend schema @link(url: "https://specs.apollo.dev/federation/v2.0", import: ["@key","@shareable"])
type Query { product(id: ID!): Product }
type Product @key(fields: "id") { id: ID! name: String! price: Int! description: String }'

# 2. Notify the gateway to re-fetch and apply the new SDL
curl -X POST http://localhost:9000/products/apply
# -> {"ok":true}

# 3. The gateway now serves the updated schema (description field available)
curl -X POST http://localhost:9000/graphql \
  -H "Content-Type: application/json" \
  -d '{"query":"{ product(id: \"1\") { id name description } }"}'
```

## Quick Start (Local)

```bash
# Terminal 1: mock products subgraph
cd _example/schema_update/mock_subgraph
go run . --addr :8501 --name products

# Terminal 2: mock reviews subgraph
go run . --addr :8502 --name reviews

# Terminal 3: gateway (edit gateway.yaml hosts to localhost first)
go run ./cmd/go-graphql-federation-gateway start
```

## Configuration (`gateway.yaml`)

```yaml
endpoint: /graphql
port: 9000
service_name: go-graphql-federation-gateway
timeout_duration: "5s"
request_timeout: "30s"   # how long to wait for in-flight requests to drain on apply
services:
  - name: products
    host: http://products:8501/query   # Docker: service name; Local: http://localhost:8501/query
    retry:
      attempts: 3      # retries for SDL fetch on failure
      timeout: "5s"    # per-attempt timeout
  - name: reviews
    host: http://reviews:8502/query
    retry:
      attempts: 3
      timeout: "5s"
```

## Mock Subgraph API

The `mock_subgraph` server exposes:

| Method | Path        | Description                                       |
|--------|-------------|---------------------------------------------------|
| POST   | `/query`    | GraphQL endpoint (handles `_service{sdl}` too)   |
| POST   | `/_service` | Direct SDL introspection endpoint                 |
| PUT    | `/schema`   | Hot-swap the in-memory SDL (for demo/testing)    |
| GET    | `/health`   | Health check                                      |

## Apply Endpoint

```
POST /{subgraph-name}/apply
```

**Success (200)**:
```json
{"ok": true}
```

**Failure (500)** — composition error or timeout:
```json
{"error": "composition failed: ..."}
```

## Rollback Behaviour

### Composition failure
If the new SDL cannot be composed (e.g., conflicting type definitions), the gateway returns an error and **keeps the current schema unchanged**.

### Panic during swap
If a panic occurs during `applySubgraph`, the gateway logs the panic and **automatically restores the previous known-good schema**:
```
[Gateway] panic during schema application for "products": ... — rolling back
```

## Concurrent Request Safety

- In-flight GraphQL requests use a snapshot of the engine captured at the start of `ServeHTTP`.
  A concurrent schema swap never interrupts an in-progress request.
- `applySubgraph` calls are serialised with a mutex — only one schema update runs at a time.
- The gateway waits up to `request_timeout` for in-flight requests to drain before swapping.
  If the timeout is exceeded, apply returns an error and the schema is **not** swapped.
