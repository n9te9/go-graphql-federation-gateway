# Go GraphQL Federation Gateway

A **stateless**, high-performance **GraphQL Federation v2 Gateway** written purely in **Go**.

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go Version](https://img.shields.io/badge/go-1.25+-00ADD8.svg?logo=go)
![Version](https://img.shields.io/badge/version-v0.1.5-blue.svg)
![Status](https://img.shields.io/badge/status-active-success.svg)

## 📖 Introduction

**Go GraphQL Federation Gateway** is a lightweight, stateless GraphQL Federation v2 Gateway written purely in Go.

While existing solutions like Apollo Router (Rust) are excellent, extending them often requires learning Rust or dealing with binary constraints. This gateway provides:

- **Native Go** — Easy to read, debug, and extend for Go developers
- **Stateless design** — HTTP-only (query & mutation); no WebSocket state required
- **Federation v2 Compliant** — Core and advanced directives fully supported
- **Hackable** — Modular Planner/Executor architecture for custom optimization
- **Observable** — Built-in OpenTelemetry tracing support
- **Faster than Apollo Router** — 1.58x higher throughput in benchmarks

## 🆕 What's New in v0.1.5

### Federation v2 Compliance

- **`@key` nested field support:** `@key(fields: "coordinate { lat lng }")` — nested object keys are fully parsed and resolved via a `KeyFieldNode` tree structure
- **Multiple `@key` directives (executor fix):** When an extension subgraph declares a different `@key` than the owner's first key, the executor now correctly uses the extension's key for `_entities` representations (e.g., `@key(fields: "username")` in badges service when owner also has `@key(fields: "id")`)
- **`@provides` optimization:** Entity steps are skipped when the parent service fully covers all requested child fields via `@provides`. Safety check ensures the optimization only applies when the entity type is declared as a full `type` definition (not `extend type`)

### Performance

- **HTTP connection pool settings via `gateway.yaml`:** `max_idle_conns_per_host` (default 32), `max_conns_per_host`, and `idle_conn_timeout` are now configurable — eliminating the previous bottleneck of `MaxIdleConnsPerHost: 2` from `http.DefaultTransport`
- **`sync.Pool` request body buffering:** `sendRequest` now uses a pool of `*bytes.Buffer` instances, eliminating per-request `json.Marshal` intermediate `[]byte` allocation and `bytes.NewReader` wrapper
- **Result: 1.58x faster than Apollo Router** (4,380 req/s vs 2,771 req/s at concurrency 50)

### Testing

- **135+ integration tests** across 8 production-like domains (EC, Fintech, SaaS, Social, Travel, nestkey, multikey, provides)
- **3 new test domains** demonstrating nested keys (`nestkey`), alternate key resolution (`multikey`), and `@provides` optimization (`provides`)

## ✨ Key Features

### Stateless Architecture

This gateway is designed to be **stateless and horizontally scalable**:
- HTTP-only: query and mutation operations
- No persistent WebSocket connections (Subscription is intentionally out of scope)
- Every request is fully self-contained

### Advanced Query Planning

- **DAG-based execution:** Resolves complex dependency graphs, executing independent steps in parallel
- **`@requires` injection:** Automatically injects required fields (e.g., `weight`) into upstream requests for computed fields (e.g., `shippingCost`). Supports chained `@requires` dependencies
- **`@provides` optimization:** Skips unnecessary `_entities` fetches when the parent service already provides the needed fields
- **`@shareable` preference:** Prefers the parent's subgraph for `@shareable` fields to avoid extra entity fetches
- **Entity step deduplication:** Sibling extension fields on the same entity are merged into a single `_entities` call
- **Query plan caching:** Plans are cached per query string and reused on cache hits, skipping parse/validate/plan on repeated queries

### Concurrent Execution

- Independent subgraph steps execute in parallel via goroutines
- Step dependency graph (DAG) ensures correct ordering

### Partial Response Support

- Returns partial data when some subgraphs fail
- Failed fields are set to `null` with error details including path and service name
- Execution continues for independent fields even when some steps fail

### HTTP Connection Pool

Configurable via `gateway.yaml`:

```yaml
connection_pool:
  max_idle_conns_per_host: 32   # default: 32
  max_conns_per_host: 0         # default: 0 (unlimited)
  idle_conn_timeout: "90s"      # default: "90s"
```

## 🧩 Supported Directives

### Core Federation v2 Directives

| Directive | Status | Notes |
| :--- | :---: | :--- |
| `@key` | ✅ | Simple, composite, nested (`"coord { lat lng }"`), multiple `@key` per type |
| `@key(resolvable: false)` | ✅ | Excluded from entity fetch origin selection |
| `@external` | ✅ | Correct ownership filtering, prevents stub references from being treated as owners |
| `@requires` | ✅ | Chained dependency injection in topological order |
| `@provides` | ✅ | Optimization: skips entity step when parent covers all requested fields |
| `@shareable` | ✅ | Same-subgraph preference to avoid unnecessary entity fetches |
| `@override` | ✅ | Field ownership migration between subgraphs |
| `@inaccessible` | ✅ | Type & field level; enforced at schema composition and query validation |
| `@tag` | ✅ | Type & field level metadata |
| `@interfaceObject` | ✅ | Interface types as entities; inline fragment resolution |
| `@composeDirective` | ✅ | Custom directive preservation and consistency validation |
| `@link` | ✅ | Parsed and passed through in composed schema |

### Not Supported (Intentional)

| Feature | Reason |
| :--- | :--- |
| **Subscription** | Requires stateful WebSocket connections; contradicts stateless design |
| `@authenticated / @requiresScopes / @policy` | Authorization layer should be handled outside the gateway |
| `@defer / @stream` | Requires long-lived streaming connections |
| Federated Tracing (ftv1) | Apollo Studio-specific protocol |
| `@override(label)` | Progressive override; planned for future release |

## ⚠️ Schema Definition Best Practices

The planner relies on explicit schema definitions to determine field ownership.

Always use `extend type` and mark external fields with `@external`:

```graphql
# ✅ Recommended
extend type Product @key(fields: "id") {
  id: ID! @external
  weight: Float @external
  shippingCost: Float @requires(fields: "weight")
}
```

## 🛠️ Getting Started

### Option 1: Install the Binary

```bash
go install github.com/n9te9/go-graphql-federation-gateway/cmd/go-graphql-federation-gateway@latest
```

Initialize configuration:

```bash
go-graphql-federation-gateway init
```

This generates a `gateway.yaml` with sensible defaults:

```yaml
endpoint: /graphql
port: 9000
service_name: go-graphql-federation-gateway
timeout_duration: "5s"
request_timeout: "30s"
services:
  - name: products
    host: http://localhost:4001
    retry:
      attempts: 3
      timeout: "5s"
connection_pool:
  max_idle_conns_per_host: 32
  max_conns_per_host: 0
  idle_conn_timeout: "90s"
opentelemetry:
  tracing:
    enable: false
```

Start the gateway:

```bash
go-graphql-federation-gateway serve
```

### Option 2: Run the Example

```bash
git clone https://github.com/n9te9/go-graphql-federation-gateway.git
cd _example/ec
docker compose up -d
```

Then start the gateway:

```bash
cd _example/ec
go-graphql-federation-gateway serve
```

## 🧪 Integration Tests

```bash
cd _example

make test-all       # All 8 domains (135+ tests)
make test-ec        # EC domain only
make test-nestkey   # Nested @key tests
make test-multikey  # Multiple @key directive tests
make test-provides  # @provides optimization tests
```

## 📊 Performance

Benchmarks run at concurrency 50 with 10,000 requests per domain (Docker internal networking):

| Domain | Go Gateway | Apollo Router | Ratio |
| :--- | ---: | ---: | :--- |
| EC — `@external + @requires` | ~4,380 req/s | ~2,771 req/s | **1.58x faster** |

Run benchmarks locally:

```bash
cd _example
make setup      # Install hey, verify Docker
make benchmark  # All domains
```

## 🔭 Observability

Enable OpenTelemetry tracing in `gateway.yaml`:

```yaml
opentelemetry:
  tracing:
    enable: true
```

Configure the exporter via environment variable:

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:4318"
```

## 🤝 Contributing

1. Fork the project
2. Create your feature branch (`git checkout -b feature/AmazingFeature`)
3. Commit your changes (`git commit -m 'Add AmazingFeature'`)
4. Push to the branch (`git push origin feature/AmazingFeature`)
5. Open a Pull Request

## 📝 License

Distributed under the MIT License. See `LICENSE` for more information.
