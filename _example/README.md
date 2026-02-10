# Apollo Federation Examples

This directory contains various Apollo Federation 2.0 use cases implemented with Go and gqlgen.

## Domains

### 1. EC (Refactored)
- **Subgraphs**: users, product, review, inventory
- **Features**: Entity extension, `@requires`, `@external`.
- **Config**: `_example/ec/gateway.yaml`

### 2. Fintech
- **Subgraphs**: customers, accounts, transactions
- **Features**: Deeply linked financial entities.
- **Config**: `_example/fintech/gateway.yaml`

### 3. Social
- **Subgraphs**: users, posts, comments
- **Features**: Recursive/Nested relationships (User -> Post -> Comment -> User).
- **Config**: `_example/social/gateway.yaml`

### 4. Travel
- **Subgraphs**: flights, bookings
- **Features**: Compound keys `@key(fields: "number departureDate")`.
- **Config**: `_example/travel/gateway.yaml`

### 5. SaaS
- **Subgraphs**: organizations, projects, billing
- **Features**: Shared fields using `@shareable`.
- **Config**: `_example/saas/gateway.yaml`

## How to Run

1. **Start all subgraphs for a domain**:
   ```bash
   cd _example/<domain>
   docker compose up -d
   ```

2. **Run the Gateway**:
   From the project root:
   ```bash
   go run cmd/gateway/main.go --config _example/<domain>/gateway.yaml
   ```

## Verification

After `docker compose up`, you can verify subgraphs are running:
```bash
curl -X POST -H "Content-Type: application/json" --data '{"query":"{_service{sdl}}"}' http://localhost:8081/query
```
*(Check docker-compose.yaml for specific ports)*

## Implementation Notes
- All subgraphs use **Apollo Federation 2.0**.
- **Subscription** is excluded as per requirements.
- Each subgraph implements `EntityResolver` to handle `_entities` queries.
