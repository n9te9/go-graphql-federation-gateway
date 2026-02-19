# Schema Update Example

This example demonstrates the **Gateway Schema Registry** feature: how to dynamically update a subgraph schema at runtime **without restarting the gateway**.

## Overview

The gateway no longer loads subgraph schemas from local `.graphqls` files. Instead it:

1. **At startup** — fetches each subgraph's SDL via `POST /_service`
2. **On update** — accepts `POST /{name}/apply` to trigger an on-demand SDL re-fetch, recomposes the supergraph, and atomically swaps in the new engine
3. **On failure** — keeps the previous schema if composition fails; panics during swap trigger automatic rollback

```
Subgraph A  ←──── POST /_service ────  Gateway (startup)
Subgraph A  ────  POST /A/apply   ───→ Gateway (runtime update trigger)
                                        Gateway ──→ POST /_service ──→ Subgraph A (re-fetch)
                                        Gateway composes new supergraph
                                        Gateway atomically swaps engine
```

## Quick Start

### 1. Start subgraphs

Use any of the existing example domains (e.g. `ec`):

```bash
cd _example/ec
docker-compose up -d products reviews users inventory
```

### 2. Start the gateway

```bash
cp _example/schema_update/gateway.yaml gateway.yaml
go run ./cmd/go-graphql-federation-gateway start
```

The gateway will fetch SDL from each subgraph on startup via `/_service`.

### 3. Send a GraphQL request

```bash
curl -X POST http://localhost:9000/graphql \
  -H "Content-Type: application/json" \
  -d '{"query":"{ products { id name } }"}'
```

### 4. Trigger a schema update

When a subgraph's schema changes, it notifies the gateway by calling:

```bash
curl -X POST http://localhost:9000/products/apply
```

The gateway will:
1. Re-fetch the SDL from `http://localhost:8101/query/_service`
2. Re-compose the supergraph
3. Wait for all in-flight requests to complete (up to `request_timeout`)
4. Atomically swap in the new execution engine

#### Success response

```json
{"ok": true}
```

#### Failure response (composition error)

```json
{"error": "composition failed: ..."}
```

## Configuration

```yaml
# gateway.yaml
endpoint: /graphql
port: 9000
service_name: go-graphql-federation-gateway
timeout_duration: "5s"
request_timeout: "30s"   # how long to wait for in-flight requests to drain on apply
services:
  - name: products
    host: http://localhost:8101/query
    retry:
      attempts: 3      # how many times to retry /_service fetch on failure
      timeout: "5s"    # per-attempt HTTP timeout for /_service fetch
  - name: reviews
    host: http://localhost:8102/query
    retry:
      attempts: 3
      timeout: "5s"
```

## Rollback Behaviour

### Composition failure
If the new SDL cannot be composed (e.g., conflicting type definitions), the gateway returns an error and **keeps the current schema**.

### Panic during swap
If a panic occurs anywhere during `applySubgraph`, the gateway logs the panic and **automatically restores the previous known-good schema**.

```
[Gateway] panic during schema application for "products": ... — rolling back
```

## Concurrent Request Safety

- In-flight GraphQL requests use a snapshot of the engine captured at the start of `ServeHTTP`.  A concurrent schema swap never interrupts an in-progress request.
- `applySubgraph` calls are serialised with a mutex — only one schema update runs at a time.
- The gateway waits up to `request_timeout` for in-flight requests to drain before swapping.  If the timeout is exceeded, the apply call returns an error and the schema is **not** swapped.
