# Benchmark Results: Plan() vs PlanOptimized()

## Environment

| Item | Value |
|------|-------|
| OS | macOS (darwin/arm64) |
| CPU | Apple M2 Pro |
| Go | 1.21+ |
| Command | `go test -bench=. -benchmem -benchtime=3s ./federation/planner/` |

---

## Raw Results

```
goos: darwin
goarch: arm64
pkg: github.com/n9te9/go-graphql-federation-gateway/federation/planner
cpu: Apple M2 Pro

BenchmarkPlan_SingleSubGraph-12                1206282     2996 ns/op     3962 B/op     88 allocs/op
BenchmarkPlanOptimized_SingleSubGraph-12       1000000     3275 ns/op     4282 B/op     96 allocs/op

BenchmarkPlan_EntityFetch-12                    699778     5281 ns/op     6772 B/op    147 allocs/op
BenchmarkPlanOptimized_EntityFetch-12           646041     5677 ns/op     7284 B/op    158 allocs/op

BenchmarkPlan_ProvidesFullyCovered-12           499562     7304 ns/op     9246 B/op    204 allocs/op
BenchmarkPlanOptimized_ProvidesFullyCovered-12  399715     9137 ns/op     9790 B/op    221 allocs/op

BenchmarkPlan_ProvidesPartial-12                463928     7959 ns/op     9950 B/op    221 allocs/op
BenchmarkPlanOptimized_ProvidesPartial-12       324260    11160 ns/op    12071 B/op    273 allocs/op

BenchmarkPlan_ThreeSubgraphs-12                 340083    10514 ns/op    13400 B/op    277 allocs/op
BenchmarkPlanOptimized_ThreeSubgraphs-12        331586    10961 ns/op    14233 B/op    293 allocs/op
```

---

## Summary Table

| Scenario | Plan() | PlanOptimized() | Planning overhead | Execution benefit |
|---|---|---|---|---|
| 1. Single subgraph (fast path) | 2,996 ns | 3,275 ns | +9% | none (identical plan) |
| 2. Entity fetch (no @provides) | 5,281 ns | 5,677 ns | +7% | none (identical plan) |
| 3. @provides fully covered | 7,304 ns | 9,137 ns | +25% | **−1 entity fetch / request** |
| 4. @provides partial | 7,959 ns | 11,160 ns | +40% | none (entity fetch still needed) |
| 5. Three-subgraph chain | 10,514 ns | 10,961 ns | +4% | Dijkstra finds optimal path |

---

## Analysis

### Planning time (measured above)

`PlanOptimized()` introduces a small planning-time overhead compared to the greedy v0.1.3 `Plan()`:

* **+4–9%** for simple scenarios where Dijkstra's benefit is minimal (single subgraph, 2-hop entity fetch).
* **+25–40%** for scenarios involving `@provides` analysis, because `PlanOptimized()` must:
  1. Run `isSingleSubGraphQuery()` to decide the fast-path / Dijkstra-path split.
  2. Build a weighted directed graph from the supergraph.
  3. Run Dijkstra's shortest-path algorithm per entity type.
  4. Run `canResolveViaProvides()` per `@provides`-annotated field.

Even in the worst case (scenario 4, partial `@provides`), the absolute overhead is only **~3 µs** per plan — negligible compared to the millisecond-scale network latency of actual subgraph requests.

### Execution-time benefit (query plan quality)

The true benefit of `PlanOptimized()` is in the **quality of the generated query plan** — specifically the number of entity-fetch steps produced:

| Scenario | Plan() steps | PlanOptimized() steps | Saving |
|---|---|---|---|
| 3. @provides fully covered | N + 1 (includes entity fetch for `Product`) | N (entity fetch skipped) | **−1 subgraph round-trip** |
| 5. Three-subgraph chain | Uses greedy DFS | Uses Dijkstra shortest path | optimal routing |

For scenario 3 (`@provides` fully covered), `Plan()` emits an unnecessary entity-fetch step for the `products` subgraph because the greedy algorithm does not consider `@provides` annotations. `PlanOptimized()` detects that `Review.product.name` is already satisfied by `@provides(fields: "name")` and omits the extra round-trip.

In production, a single subgraph round-trip typically costs **1–10 ms** (depending on network conditions). Eliminating it for every request containing `@provides`-covered fields yields a direct, proportional latency improvement that far outweighs the ~3 µs extra planning overhead.

### Correctness improvement (reachability)

Beyond raw performance, the Dijkstra-based approach provides a **correctness guarantee** that the greedy algorithm cannot: it always finds the shortest (minimum-hop) path to resolve an entity, even when some subgraphs are unreachable or when multiple paths exist. This is the primary motivation described in the DesignDoc (`federation-reachablity`).

---

## Conclusion

* **Planning overhead**: small (+4–40%), with the absolute cost staying in the low single-digit microsecond range — safe to use in high-throughput scenarios.
* **Execution benefit**: `PlanOptimized()` eliminates unnecessary entity-fetch steps when `@provides` fully covers the queried fields, directly reducing end-to-end request latency.
* **Correctness benefit**: Dijkstra guarantees optimal path discovery across complex multi-subgraph topologies where the greedy v0.1.3 algorithm could produce suboptimal or incorrect plans.

The implementation satisfies the performance goals of the DesignDoc: the planning-time cost is negligible while the execution-time benefit scales with query complexity and the use of `@provides` directives.
