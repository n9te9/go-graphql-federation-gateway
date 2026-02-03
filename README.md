# Go GraphQL Federation Gateway

A robust, hackable, and high-performance **GraphQL Federation v2 Gateway** written purely in **Go**.

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go Version](https://img.shields.io/badge/go-1.24+-00ADD8.svg?logo=go)
![Status](https://img.shields.io/badge/status-active-success.svg)

## 📖 Introduction

**Go GraphQL Federation Gateway** is designed to be a lightweight GraphQL Federation v2 Gateway written purely in Go.

While existing solutions like Apollo Router (Rust) are excellent, extending them often requires learning Rust or dealing with binary constraints. This project provides a fully-featured Federation Gateway that is:

* **Native Go:** Easy to read, debug, and extend for Go developers.
* **Federation v2 Compliant:** Supports core directives like `@key`, `@requires`, and `@external`.
* **Hackable:** The Planner and Executor logic is modular, allowing for custom optimization strategies.

## ✨ Key Features

* **Apollo Federation v2 Support:** Seamlessly composes subgraphs using v2 directives.
* **Advanced Query Planning:**
  * Solves complex dependency graphs (DAGs).
  * Handles **`@requires`** directives by automatically injecting required fields (e.g., `weight`) into upstream requests to compute dependent fields (e.g., `shippingEstimate`).
  * Resolves **Deadlocks** and circular dependencies in schema definitions using strict `@external` checks.
* **"Flattening" Execution Strategy:**
  * Avoids recursion hell by flattening entity requests.
  * Optimizes `_entities` queries by discarding unnecessary parent paths, ensuring compatibility with all subgraph implementations.
* **Concurrent Execution:** Fetches independent subgraphs in parallel using Go routines with proper context handling.
* **Deep Nested Resolution:** Correctly stitches data across multiple hops (e.g., `Product` -> `Review` -> `User`).

## ⚠️ Schema Definition Best Practices

Unlike Apollo Router, this gateway **does not currently support advanced reachability analysis**. The planner relies on explicit schema definitions to determine field ownership and dependency graphs.

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

## 🧩 Supported Directives

| Directive | Status | Description |
| :--- | :---: | :--- |
| `@key` | ✅ | Entity resolution via `_entities`. |
| `@external` | ✅ | Used to identify fields owned by other subgraphs. |
| `@requires` | ✅ | Solves computed fields by injecting dependencies. |
| `@provides` | 🚧 | (Planned) Optimization for pre-fetching fields. |
| `@shareable`| 🚧 | (Planned) Officially unsupported (works in simple cases). |

## 🛠️ Getting Started

There are two ways to get started: running the included example or installing the gateway for your own project.

### Option 1: Running the Example (Quick Start)

The repository includes a full E-Commerce example (Product, Account, Review, Shipping services).

1.  **Clone the repository:**
    ```bash
    git clone [https://github.com/n9te9/go-graphql-federation-gateway.git](https://github.com/n9te9/go-graphql-federation-gateway.git)
    cd go-graphql-federation-gateway
    ```

2.  **Start Subgraphs via Docker:**
    Navigate to the example directory and start the microservices.
    ```bash
    cd _examples/ec
    docker compose up -d
    ```

3.  **Start the Gateway:**
    Run the gateway server pointing to the example configuration.
    ```bash
    go run ../../cmd/go-graphql-federation-gateway/main.go serve
    ```

### Option 2: Installation & Usage (New Project)

To use this gateway with your own subgraphs:

1.  **Install the binary:**
    ```bash
    go install [github.com/n9te9/go-graphql-federation-gateway/cmd/go-graphql-federation-gateway@latest](https://github.com/n9te9/go-graphql-federation-gateway/cmd/go-graphql-federation-gateway@latest)
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
You will receive a fully stitched response, proving the `@requires` injection and nested resolution logic.

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

## 🤝 Contributing

We welcome contributions! Please follow the **Fork & Pull Request** workflow.

1.  **Fork the Project**
    Click the "Fork" button at the top right of this page to create your own copy of the repository.

2.  **Create your Feature Branch**
    ```bash
    git checkout -b feature/AmazingFeature
    ```

3.  **Commit your Changes**
    ```bash
    git commit -m 'Add some AmazingFeature'
    ```

4.  **Push to the Branch**
    Push the changes to **your forked repository**.
    ```bash
    git push origin feature/AmazingFeature
    ```

5.  **Open a Pull Request**
    Go to the original repository on GitHub and open a Pull Request from your forked branch.

## 📝 License

Distributed under the MIT License. See `LICENSE` for more information.
