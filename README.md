# Go GraphQL Federation Gateway

A robust, hackable, and high-performance **GraphQL Federation v2 Gateway** written purely in **Go**.

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go Version](https://img.shields.io/badge/go-1.25+-00ADD8.svg?logo=go)
![Version](https://img.shields.io/badge/version-v0.1.4-blue.svg)
![Status](https://img.shields.io/badge/status-active-success.svg)

## 📖 Introduction

**Go GraphQL Federation Gateway** is designed to be a lightweight GraphQL Federation v2 Gateway written purely in Go.

While existing solutions like Apollo Router (Rust) are excellent, extending them often requires learning Rust or dealing with binary constraints. This project provides a fully-featured Federation Gateway that is:

* **Native Go:** Easy to read, debug, and extend for Go developers.
* **Federation v2 Compliant:** Supports core and advanced directives including `@key`, `@requires`, `@external`, `@override`, `@inaccessible`, and more.
* **Hackable:** The Planner and Executor logic is modular, allowing for custom optimization strategies.
* **Observable:** Built-in OpenTelemetry support for production-grade tracing.
* **Performance Optimized:** Parallel benchmark workflow with comprehensive performance testing across multiple domains.

## 🆕 What's New in v0.1.4

### Query Planner Optimizations
- **Dijkstra-based optimized planner (`PlanOptimized`):** New `PlanOptimized()` method using a weighted directed graph with Dijkstra's algorithm to find the minimum-cost execution plan across subgraphs. `@provides` shortcuts are modeled as zero-cost edges for optimal cross-service routing.
- **`@shareable` subgraph preference:** Planner now preferentially selects the same subgraph as the parent step when a field is `@shareable` across multiple subgraphs, eliminating unnecessary entity fetches.
- **Entity step deduplication fix:** Fixed a bug where sibling extension fields on the same entity type (e.g., `inStock` and `shippingCost` on an inventory-extended `Product`) were incorrectly generating separate `_entities` calls. These are now correctly merged into a single call — matching Apollo Router behavior and reducing subgraph round-trips.

### Federation v2 Directive Improvements
- **`@key(resolvable: false)` support:** Entities marked with `resolvable: false` are now correctly excluded from entity fetch origin selection. The planner skips them as resolution entry points while still allowing reference traversal.
- **`@requires` dependency chaining:** Improved `@requires` field injection to correctly handle chained dependencies (e.g., `purchaseHistory → reviews → reviewCount`), ensuring required fields are injected in topological order.

### Testing & Infrastructure
- **78+ integration tests** across 5 production-like domains (up from 73+)
- **Subgraph Docker images** now pre-compile at build time (`go build`) instead of `go run`, dramatically reducing container startup time and improving test reliability
- **Regression test** added for entity step deduplication (`TestECBenchmarkQuery_PlanSteps`)

## 🆕 What's New in v0.1.3

### Enhanced Federation v2 Support
- **`@override` directive:** Progressive field ownership migration between subgraphs
- **`@inaccessible` directive:** Hide fields/types from the public schema
- **`@tag` directive:** Schema metadata annotations for tooling
- **`@interfaceObject` directive:** Interface representation in subgraphs
- **`@composeDirective` directive:** Custom directive preservation

### Comprehensive Testing Infrastructure
- **73+ integration tests** across 5 production-like domains
- Variable-based query testing with complex nested structures
- Full coverage of all Federation v2 directives
- Automated validation in CI/CD pipeline

### Performance Benchmarking
- **Parallel benchmark workflow** for efficient performance testing
- Direct comparison with Apollo Router baseline
- Automated PR comments with detailed performance metrics
- Domain-specific benchmark scripts for local testing

## ✨ Key Features

* **Apollo Federation v2 Support:** Seamlessly composes subgraphs using v2 directives.
* **Advanced Query Planning:**
  * Solves complex dependency graphs (DAGs).
  * **Optimized planner (`PlanOptimized`):** Uses Dijkstra's algorithm on a weighted directed graph to find the minimum-cost execution plan. `@provides` fields are modeled as zero-cost shortcuts.
  * Handles **`@requires`** directives by automatically injecting required fields (e.g., `weight`) into upstream requests to compute dependent fields (e.g., `shippingEstimate`). Supports chained `@requires` dependencies.
  * Resolves **Deadlocks** and circular dependencies in schema definitions using strict `@external` checks.
  * **`@shareable` preference:** Preferentially selects the parent's subgraph when a field is available in multiple subgraphs, eliminating unnecessary entity fetches.
  * **Entity step deduplication:** Sibling extension fields on the same entity type are merged into a single `_entities` call, matching Apollo Router behavior.
* **"Flattening" Execution Strategy:**
  * Avoids recursion hell by flattening entity requests.
  * Optimizes `_entities` queries by discarding unnecessary parent paths, ensuring compatibility with all subgraph implementations.
* **Concurrent Execution:** Fetches independent subgraphs in parallel using Go routines with proper context handling.
* **Partial Response Support:** Returns partial data when some subgraphs fail, improving resilience and user experience.
  * Failed fields are set to `null` with detailed error information.
  * Errors include path information and service name for easy debugging.
  * Graceful degradation with continued execution when possible.
  * See [Partial Response Documentation](docs/partial-response.md) for details.
* **Comprehensive Testing:** 
  * **78+ integration tests** covering all Federation v2 features across 5 example domains (EC, Fintech, SaaS, Social, Travel).
  * Variable-based query testing with multiple data types, nested queries, and composite keys.
  * Partial response tests validating graceful degradation scenarios.
  * Tests validate `@key`, `@external`, `@requires`, `@provides`, `@shareable`, `@override`, `@inaccessible`, `@tag`, and `@key(resolvable: false)` directives.
  * **Automated parallel benchmarking** comparing Go Gateway vs Apollo Router performance across all domains.
* **Observability:**
  * Full **OpenTelemetry** support.
  * Traces propagate context to subgraphs (`traceparent` injection), allowing for end-to-end visualization of distributed requests.

## ⚠️ Schema Definition Best Practices

The planner relies on explicit schema definitions to determine field ownership and dependency graphs.

To ensure correct planning and avoid deadlocks:
* **Explicitly use `extend type`** for type extensions.
* **Always mark external fields with `@external`**, even if Federation v2 allows omitting it in some cases.

**Recommended:**
```graphql
extend type Product @key(fields: "upc") {
  upc: String! @external
  weight: Int @external
  shippingEstimate: Int @requires(fields: "weight")
}
```

## 🔭 Observability

This gateway supports distributed tracing via **OpenTelemetry (OTLP)**.

### Configuration
Tracing is enabled via `gateway.yaml` and configured using standard OTEL environment variables.

**1. Enable in `gateway.yaml`:**
```yaml
opentelemetry:
  tracing:
    enabled: true
```

**2. Configure Exporter (Environment Variables):**
The gateway uses the OTLP HTTP exporter by default.

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:4318"
```

## 🧩 Supported Directives

### Core Federation v1/v2 Directives

| Directive | Status | Description |
| :--- | :---: | :--- |
| `@key` | ✅ | Entity resolution via `_entities`. Supports both simple and composite keys. |
| `@key(resolvable: false)` | ✅ | Marks an entity as non-resolvable; excluded from entity fetch origin selection. |
| `@external` | ✅ | Used to identify fields owned by other subgraphs. |
| `@requires` | ✅ | Solves computed fields by injecting dependencies. Supports chained dependency graphs. |
| `@provides` | ✅ | Optimization for pre-fetching fields from entities. Modeled as zero-cost edges in the optimized planner. |
| `@shareable`| ✅ | Allows same field/type definition across multiple subgraphs. Planner prefers same-subgraph resolution. |

### Advanced Federation v2 Directives

| Directive | Status | Description |
| :--- | :---: | :--- |
| `@override` | ✅ | Progressive migration of field ownership between subgraphs. |
| `@inaccessible` | ✅ | Hides fields/types from the public schema while keeping them in the supergraph. |
| `@tag` | ✅ | Annotates schema elements with metadata for tooling and documentation. |
| `@interfaceObject` | ✅ | Represents interface types as value types in subgraphs. |
| `@composeDirective` | ✅ | Preserves custom directives during composition. |

**Tested Features:**
- ✅ Simple keys (`@key(fields: "id")`)
- ✅ Composite keys (`@key(fields: "number departureDate")`)
- ✅ Non-resolvable keys (`@key(fields: "id", resolvable: false)`)
- ✅ Entity extensions with `@external` fields
- ✅ Computed fields with `@requires` directive (including chained dependencies)
- ✅ Field optimization with `@provides` directive  
- ✅ Shareable fields and types across services (with same-subgraph preference)
- ✅ Field ownership migration with `@override` directive
- ✅ Schema element hiding with `@inaccessible` directive
- ✅ Metadata annotations with `@tag` directive
- ✅ Nested entity resolution chains
- ✅ Circular/loopback references
- ✅ Partial responses with graceful degradation
- ✅ Variable-based queries with complex nested structures
- ✅ Entity step deduplication (sibling extension fields merged into one `_entities` call)

## 🛠️ Getting Started

There are two ways to get started: running the included example or installing the gateway for your own project.

### Option 1: Running the Example (Quick Start)

The repository includes a full E-Commerce example (Product, Account, Review, Shipping services) with **Jaeger** for tracing.

1.  **Clone the repository:**
    ```bash
    git clone https://github.com/n9te9/go-graphql-federation-gateway.git
    ```

2.  **Start Subgraphs & Jaeger via Docker:**
    Navigate to the example directory and start the microservices.
    ```bash
    cd _examples/ec
    docker compose up -d
    ```
3.  **Visualize Traces:**
    Open Jaeger at [http://localhost:16686](http://localhost:16686) to see your request traces.

### Option 2: Installation & Usage (New Project)

To use this gateway with your own subgraphs:

1.  **Install the binary:**
    ```bash
    go install github.com/n9te9/go-graphql-federation-gateway/cmd/go-graphql-federation-gateway@latest
    ```

2.  **Initialize Configuration:**
    Generate a default `gateway.yaml` file.
    ```bash
    go-graphql-federation-gateway init
    ```

3.  **Start the Server:**
    ```bash
    go-graphql-federation-gateway serve
    ```

## 🧪 Testing the Gateway

Once the gateway is running (default port `9000`), you can send complex Federation queries.

**Example Query:**
Fetching data from **Inventory** (Product), calculating fields via **Shipping** (`@requires` injection), fetching **Reviews**, and resolving **Users** (Accounts).

```bash
curl -X POST http://localhost:9000/graphql \
-H "Content-Type: application/json" \
-d '{
  "query": "query { topProducts(first: 3) { upc name price weight shippingEstimate reviews { body author { username } } } }"
}' | jq
```

**Result:**
You will receive a fully stitched response. If tracing is enabled, check Jaeger to see the breakdown of subgraph requests.

```json
{
  "data": {
    "topProducts": [
      {
        "upc": "1",
        "name": "hogehoge",
        "price": 1000,
        "weight": 30,
        "shippingEstimate": 100,
        "reviews": [
          {
            "body": "Great book!",
            "author": {
              "username": "Alice"
            }
          }
        ]
      }
      // ...
    ]
  }
}
```

## 📊 Performance Benchmarking

The project includes comprehensive performance testing infrastructure:

### Automated Benchmarks
- **Parallel execution** across 5 production-like domains (EC, Fintech, SaaS, Social, Travel)
- **Direct comparison** between Go Gateway and Apollo Router
- **10,000 requests** per test at 50 concurrent connections
- **Metrics tracked:** Requests/sec, Average latency, P50/P95/P99 percentiles

### Running Benchmarks Locally

```bash
# Run single domain benchmark
cd _example
./domain_benchmark.sh ec 9001 '{"query":"..."}' "Test Name"

# Run all domain benchmarks
cd _example/ec
./benchmark.sh
```

### CI/CD Integration
Pull requests automatically trigger parallel benchmarks across all domains, with results posted as PR comments comparing Go Gateway vs Apollo Router performance.

## 🤝 Contributing

We welcome contributions! Please follow the **Fork & Pull Request** workflow.

1.  **Fork the Project**
2.  **Create your Feature Branch** (`git checkout -b feature/AmazingFeature`)
3.  **Commit your Changes** (`git commit -m 'Add some AmazingFeature'`)
4.  **Push to the Branch** (`git push origin feature/AmazingFeature`)
5.  **Open a Pull Request**

## 📝 License

Distributed under the MIT License. See `LICENSE` for more information.
