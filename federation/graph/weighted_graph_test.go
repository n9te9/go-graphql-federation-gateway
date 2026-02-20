package graph_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
)

// -----------------------------------------------------------------------
// NodeKey
// -----------------------------------------------------------------------

func TestNodeKey_WithField(t *testing.T) {
	got := graph.NodeKey("SubGraphA", "Product", "name")
	want := "SubGraphA:Product.name"
	if got != want {
		t.Errorf("NodeKey with field: got %q, want %q", got, want)
	}
}

func TestNodeKey_TypeLevel(t *testing.T) {
	got := graph.NodeKey("SubGraphA", "Product", "")
	want := "SubGraphA:Product"
	if got != want {
		t.Errorf("NodeKey type level: got %q, want %q", got, want)
	}
}

// -----------------------------------------------------------------------
// WeightedDirectedGraph: basic operations
// -----------------------------------------------------------------------

func TestAddNode_NewNode(t *testing.T) {
	sg := newTestSubGraph(t, "sgA", `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query { product(id: ID!): Product }
	`, "http://localhost:4001")

	g := graph.NewWeightedDirectedGraph()
	node := g.AddNode("sgA:Product", sg, "Product", "")
	if node == nil {
		t.Fatal("expected non-nil node")
	}
	if node.ID != "sgA:Product" {
		t.Errorf("expected ID sgA:Product, got %s", node.ID)
	}
	if node.TypeName != "Product" {
		t.Errorf("expected TypeName Product, got %s", node.TypeName)
	}
}

func TestAddNode_Idempotent(t *testing.T) {
	sg := newTestSubGraph(t, "sgA", `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query { product(id: ID!): Product }
	`, "http://localhost:4001")

	g := graph.NewWeightedDirectedGraph()
	n1 := g.AddNode("sgA:Product", sg, "Product", "")
	n2 := g.AddNode("sgA:Product", sg, "Product", "")
	if n1 != n2 {
		t.Error("AddNode should be idempotent and return the same pointer")
	}
}

func TestAddEdge_NormalWeight(t *testing.T) {
	sg := newTestSubGraph(t, "sgA", `
		type Product @key(fields: "id") { id: ID! }
		type Query { product(id: ID!): Product }
	`, "http://localhost:4001")

	g := graph.NewWeightedDirectedGraph()
	g.AddNode("A", sg, "T", "")
	g.AddNode("B", sg, "T", "f")
	g.AddEdge("A", "B", 0)

	nodes := g.Nodes
	if w, ok := nodes["A"].Edges["B"]; !ok || w != 0 {
		t.Errorf("expected edge A->B with weight 0, got exists=%v w=%d", ok, w)
	}
}

func TestAddEdge_PreferLowerWeight(t *testing.T) {
	sg := newTestSubGraph(t, "sgA", `
		type Product @key(fields: "id") { id: ID! }
		type Query { product(id: ID!): Product }
	`, "http://localhost:4001")

	g := graph.NewWeightedDirectedGraph()
	g.AddNode("A", sg, "T", "")
	g.AddNode("B", sg, "T", "f")
	g.AddEdge("A", "B", 1)
	g.AddEdge("A", "B", 0) // lower weight should win
	if w := g.Nodes["A"].Edges["B"]; w != 0 {
		t.Errorf("expected min weight 0, got %d", w)
	}
}

func TestAddShortCut(t *testing.T) {
	sg := newTestSubGraph(t, "sgA", `
		type Product @key(fields: "id") { id: ID! }
		type Query { product(id: ID!): Product }
	`, "http://localhost:4001")
	g := graph.NewWeightedDirectedGraph()
	g.AddNode("A", sg, "Review", "product")
	g.AddShortCut("A", "sgB:Product.name")

	if w, ok := g.Nodes["A"].ShortCut["sgB:Product.name"]; !ok || w != 0 {
		t.Errorf("expected shortcut with weight 0, got exists=%v w=%d", ok, w)
	}
}

// -----------------------------------------------------------------------
// BuildGraph
// -----------------------------------------------------------------------

func TestBuildGraph_SingleSubGraph(t *testing.T) {
	sg := newTestSubGraph(t, "products", `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query { product(id: ID!): Product }
	`, "http://localhost:4001")

	g := graph.BuildGraph([]*graph.SubGraphV2{sg})

	// Type-level node should exist
	typeKey := graph.NodeKey("products", "Product", "")
	if _, ok := g.Nodes[typeKey]; !ok {
		t.Errorf("expected type node %q", typeKey)
	}

	// Field-level nodes should exist
	idKey := graph.NodeKey("products", "Product", "id")
	nameKey := graph.NodeKey("products", "Product", "name")
	if _, ok := g.Nodes[idKey]; !ok {
		t.Errorf("expected field node %q", idKey)
	}
	if _, ok := g.Nodes[nameKey]; !ok {
		t.Errorf("expected field node %q", nameKey)
	}

	// type → field edges should exist with weight 0
	typeNode := g.Nodes[typeKey]
	if w, ok := typeNode.Edges[idKey]; !ok || w != 0 {
		t.Errorf("expected edge %s -> %s with weight 0", typeKey, idKey)
	}
	if w, ok := typeNode.Edges[nameKey]; !ok || w != 0 {
		t.Errorf("expected edge %s -> %s with weight 0", typeKey, nameKey)
	}
}

func TestBuildGraph_CrossSubGraphEdges(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query { product(id: ID!): Product }
	`
	reviewSchema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			reviews: [Review!]!
		}
		type Review {
			id: ID!
			body: String!
		}
		extend type Query { review(id: ID!): Review }
	`

	sgProducts := newTestSubGraph(t, "products", productSchema, "http://localhost:4001")
	sgReviews := newTestSubGraph(t, "reviews", reviewSchema, "http://localhost:4002")

	g := graph.BuildGraph([]*graph.SubGraphV2{sgProducts, sgReviews})

	prodKey := graph.NodeKey("products", "Product", "")
	revKey := graph.NodeKey("reviews", "Product", "")

	// Cross-subgraph edges should exist with weight 1
	if w, ok := g.Nodes[prodKey].Edges[revKey]; !ok || w != 1 {
		t.Errorf("expected cross edge products:Product -> reviews:Product with weight 1, got exists=%v w=%d", ok, w)
	}
	if w, ok := g.Nodes[revKey].Edges[prodKey]; !ok || w != 1 {
		t.Errorf("expected cross edge reviews:Product -> products:Product with weight 1, got exists=%v w=%d", ok, w)
	}
}

// TestBuildGraph_ProvidesUnresolvable verifies that unresolvable @provides shortcuts
// fall back to a placeholder key without panicking.
func TestBuildGraph_ProvidesUnresolvable(t *testing.T) {
	// SubGraph A provides a field that only exists within itself (same subgraph).
	// resolveProvideShortCuts should keep the placeholder (resolved=false path).
	reviewSchema := `
		type Review @key(fields: "id") {
			id: ID!
			product: Product! @provides(fields: "upc")
		}
		extend type Product @key(fields: "upc") {
			upc: String! @external
		}
		type Query { review(id: ID!): Review }
	`
	// Product is ONLY in the reviews subgraph (as an extension, no separate subgraph).
	// This means the provides target field "upc" exists in the same subgraph only,
	// forcing the !resolved branch.
	sgReviews := newTestSubGraph(t, "reviews", reviewSchema, "http://localhost:4002")
	// Should not panic
	g := graph.BuildGraph([]*graph.SubGraphV2{sgReviews})
	_ = g
}

// TestBuildGraph_MultipleSubgraphsSameField verifies that fieldOwner deduplication works
// when the same typeName.fieldName appears in multiple subgraphs (exercises the !already branch).
func TestBuildGraph_MultipleSubgraphsSameField(t *testing.T) {
	productSchema1 := `
		type Product @key(fields: "id") {
			id: ID!
			name: String! @shareable
		}
		type Query { product(id: ID!): Product }
	`
	productSchema2 := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			name: String! @shareable
		}
		type Query { productAlt(id: ID!): Product }
	`
	sg1 := newTestSubGraph(t, "products1", productSchema1, "http://localhost:4001")
	sg2 := newTestSubGraph(t, "products2", productSchema2, "http://localhost:4002")

	// Should not panic; the second subgraph's "Product.name" is skipped by fieldOwner dedup.
	g := graph.BuildGraph([]*graph.SubGraphV2{sg1, sg2})

	// Cross edges should exist between the two Product type nodes
	prod1Key := graph.NodeKey("products1", "Product", "")
	prod2Key := graph.NodeKey("products2", "Product", "")
	if _, ok := g.Nodes[prod1Key].Edges[prod2Key]; !ok {
		t.Errorf("expected cross edge between products1:Product and products2:Product")
	}
}

func TestBuildGraph_ProvidesShortCut(t *testing.T) {
	// SubGraph A: Review with @provides(fields: "name") on product field
	reviewSchema := `
		type Review @key(fields: "id") {
			id: ID!
			body: String!
			product: Product! @provides(fields: "name")
		}
		extend type Product @key(fields: "upc") {
			upc: String! @external
			name: String @external
		}
		type Query { review(id: ID!): Review }
	`
	// SubGraph B: Product is the owner of name
	productSchema := `
		type Product @key(fields: "upc") {
			upc: String!
			name: String!
			price: Int!
		}
		type Query { product(upc: String!): Product }
	`

	sgReviews := newTestSubGraph(t, "reviews", reviewSchema, "http://localhost:4002")
	sgProducts := newTestSubGraph(t, "products", productSchema, "http://localhost:4001")

	g := graph.BuildGraph([]*graph.SubGraphV2{sgReviews, sgProducts})

	// The reviews:Review.product node should have a shortcut to products:Product.name
	reviewProductKey := graph.NodeKey("reviews", "Review", "product")
	reviewProductNode, ok := g.Nodes[reviewProductKey]
	if !ok {
		t.Fatalf("expected node %q to exist", reviewProductKey)
	}

	productNameKey := graph.NodeKey("products", "Product", "name")
	if _, hasShortCut := reviewProductNode.ShortCut[productNameKey]; !hasShortCut {
		// Dump available shortcuts for debugging
		t.Errorf("expected shortcut from %s to %s; got shortcuts: %v",
			reviewProductKey, productNameKey, reviewProductNode.ShortCut)
	}
}

// -----------------------------------------------------------------------
// Dijkstra
// -----------------------------------------------------------------------

func TestDijkstra_SingleSubGraph_AllReachable(t *testing.T) {
	sg := newTestSubGraph(t, "products", `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query { product(id: ID!): Product }
	`, "http://localhost:4001")

	g := graph.BuildGraph([]*graph.SubGraphV2{sg})

	typeKey := graph.NodeKey("products", "Product", "")
	result := g.Dijkstra([]string{typeKey})

	idKey := graph.NodeKey("products", "Product", "id")
	nameKey := graph.NodeKey("products", "Product", "name")

	if result.Dist[idKey] != 0 {
		t.Errorf("expected cost 0 to reach %s, got %d", idKey, result.Dist[idKey])
	}
	if result.Dist[nameKey] != 0 {
		t.Errorf("expected cost 0 to reach %s, got %d", nameKey, result.Dist[nameKey])
	}
}

func TestDijkstra_CrossSubGraph_CostOne(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query { product(id: ID!): Product }
	`
	reviewSchema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			reviews: [Review!]!
		}
		type Review {
			id: ID!
			body: String!
		}
		extend type Query { review(id: ID!): Review }
	`

	sgProducts := newTestSubGraph(t, "products", productSchema, "http://localhost:4001")
	sgReviews := newTestSubGraph(t, "reviews", reviewSchema, "http://localhost:4002")

	g := graph.BuildGraph([]*graph.SubGraphV2{sgProducts, sgReviews})

	// Start from products:Product (cost 0)
	entry := graph.NodeKey("products", "Product", "")
	result := g.Dijkstra([]string{entry})

	// products:Product.name should cost 0
	nameKey := graph.NodeKey("products", "Product", "name")
	if result.Dist[nameKey] != 0 {
		t.Errorf("expected cost 0 for products:Product.name, got %d", result.Dist[nameKey])
	}

	// reviews:Product (cross) should cost 1
	revTypeKey := graph.NodeKey("reviews", "Product", "")
	if result.Dist[revTypeKey] != 1 {
		t.Errorf("expected cost 1 for reviews:Product, got %d", result.Dist[revTypeKey])
	}

	// reviews:Product.reviews should cost 1 (cross edge + 0)
	revFieldKey := graph.NodeKey("reviews", "Product", "reviews")
	if result.Dist[revFieldKey] != 1 {
		t.Errorf("expected cost 1 for reviews:Product.reviews, got %d", result.Dist[revFieldKey])
	}
}

func TestDijkstra_Unreachable(t *testing.T) {
	sg := newTestSubGraph(t, "products", `
		type Product @key(fields: "id") { id: ID! }
		type Query { product(id: ID!): Product }
	`, "http://localhost:4001")

	g := graph.BuildGraph([]*graph.SubGraphV2{sg})
	result := g.Dijkstra([]string{})

	// All nodes should be unreachable (infinite distance)
	info := int(^uint(0) >> 1)
	typeKey := graph.NodeKey("products", "Product", "")
	if result.Dist[typeKey] != info {
		t.Errorf("expected unreachable (inf), got %d", result.Dist[typeKey])
	}
}

func TestDijkstra_ShortCutZeroCost(t *testing.T) {
	reviewSchema := `
		type Review @key(fields: "id") {
			id: ID!
			product: Product! @provides(fields: "name")
		}
		extend type Product @key(fields: "upc") {
			upc: String! @external
			name: String @external
		}
		type Query { review(id: ID!): Review }
	`
	productSchema := `
		type Product @key(fields: "upc") {
			upc: String!
			name: String!
		}
		type Query { product(upc: String!): Product }
	`

	sgReviews := newTestSubGraph(t, "reviews", reviewSchema, "http://localhost:4002")
	sgProducts := newTestSubGraph(t, "products", productSchema, "http://localhost:4001")

	g := graph.BuildGraph([]*graph.SubGraphV2{sgReviews, sgProducts})

	// Start from reviews:Review (entry point)
	entry := graph.NodeKey("reviews", "Review", "")
	result := g.Dijkstra([]string{entry})

	// reviews:Review.product is reachable with cost 0
	reviewProductKey := graph.NodeKey("reviews", "Review", "product")
	if result.Dist[reviewProductKey] != 0 {
		t.Errorf("expected cost 0 for reviews:Review.product, got %d", result.Dist[reviewProductKey])
	}

	// products:Product.name should be reachable via shortcut at cost 0
	productNameKey := graph.NodeKey("products", "Product", "name")
	if result.Dist[productNameKey] != 0 {
		t.Errorf("expected cost 0 for products:Product.name via shortcut, got %d", result.Dist[productNameKey])
	}
}

// -----------------------------------------------------------------------
// ReconstructPath
// -----------------------------------------------------------------------

func TestReconstructPath_Simple(t *testing.T) {
	sg := newTestSubGraph(t, "products", `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query { product(id: ID!): Product }
	`, "http://localhost:4001")

	g := graph.BuildGraph([]*graph.SubGraphV2{sg})

	typeKey := graph.NodeKey("products", "Product", "")
	nameKey := graph.NodeKey("products", "Product", "name")

	result := g.Dijkstra([]string{typeKey})
	path := result.ReconstructPath(nameKey)

	if len(path) == 0 {
		t.Fatal("expected non-empty path")
	}
	if path[0] != typeKey {
		t.Errorf("expected path to start with %s, got %s", typeKey, path[0])
	}
	if path[len(path)-1] != nameKey {
		t.Errorf("expected path to end with %s, got %s", nameKey, path[len(path)-1])
	}
}

func TestReconstructPath_Unreachable(t *testing.T) {
	sg := newTestSubGraph(t, "products", `
		type Product @key(fields: "id") { id: ID! }
		type Query { product(id: ID!): Product }
	`, "http://localhost:4001")

	g := graph.BuildGraph([]*graph.SubGraphV2{sg})
	result := g.Dijkstra([]string{}) // no entry points -> all unreachable
	path := result.ReconstructPath(graph.NodeKey("products", "Product", "id"))
	if path != nil {
		t.Errorf("expected nil path for unreachable node, got %v", path)
	}
}

// -----------------------------------------------------------------------
// BuildGraph with SuperGraphV2 integration
// -----------------------------------------------------------------------

func TestSuperGraphV2_HasGraph(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query { product(id: ID!): Product }
	`
	sg := newTestSubGraph(t, "products", productSchema, "http://localhost:4001")
	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{sg})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}
	if superGraph.Graph == nil {
		t.Fatal("expected SuperGraphV2.Graph to be non-nil after construction")
	}
	typeKey := graph.NodeKey("products", "Product", "")
	if _, ok := superGraph.Graph.Nodes[typeKey]; !ok {
		t.Errorf("expected node %q in SuperGraphV2.Graph", typeKey)
	}
}

func TestReconstructPath_NodeNotInDist(t *testing.T) {
	// DijkstraResult with empty Dist map - any nodeID lookup returns !ok
	result := &graph.DijkstraResult{
		Dist: map[string]int{},
		Prev: map[string]string{},
	}
	path := result.ReconstructPath("nonexistent:Type.field")
	if path != nil {
		t.Errorf("expected nil for node not in Dist, got %v", path)
	}
}

// TestDijkstra_EntryPointNotInGraph verifies that unknown entry points are skipped gracefully.
func TestDijkstra_EntryPointNotInGraph(t *testing.T) {
	sg := newTestSubGraph(t, "products", `
		type Product @key(fields: "id") { id: ID! }
		type Query { product(id: ID!): Product }
	`, "http://localhost:4001")
	g := graph.BuildGraph([]*graph.SubGraphV2{sg})

	// Pass a non-existent entry node: should not panic
	result := g.Dijkstra([]string{"nonexistent:Ghost"})
	// All real nodes should still be unreachable (inf)
	inf := int(^uint(0) >> 1)
	typeKey := graph.NodeKey("products", "Product", "")
	if result.Dist[typeKey] != inf {
		t.Errorf("expected inf for unreachable node, got %d", result.Dist[typeKey])
	}
}

// TestDijkstra_StaleEntry exercises the stale-entry skip path.
// Setup: A(entry,cost=0) -> B(cost=1) -> C(cost=1), then A -> D(cost=0) -> B(via path cost=0).
// B gets pushed twice: first with cost=1, then with cost=0.
// When the stale cost=1 entry for B is popped, it is discarded.
func TestDijkstra_StaleEntry(t *testing.T) {
	sg := newTestSubGraph(t, "products", `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query { product(id: ID!): Product }
	`, "http://localhost:4001")

	g := graph.NewWeightedDirectedGraph()
	g.AddNode("A", sg, "T", "")
	g.AddNode("B", sg, "T", "b")
	g.AddNode("C", sg, "T", "c")
	g.AddNode("D", sg, "T", "d")

	// A -> B (cost 1) and A -> D (cost 0) -> B (cost 0)
	// First A->B is explored (cost 1), then A->D (cost 0), then D->B (cost 0 < 1, update!).
	// The old cost-1 entry for B is now stale; when popped, 1 > 0, so it is skipped.
	g.AddEdge("A", "B", 1) // slow path
	g.AddEdge("A", "D", 0) // fast path through D
	g.AddEdge("D", "B", 0) // D->B makes B reachable at cost 0

	result := g.Dijkstra([]string{"A"})

	if result.Dist["B"] != 0 {
		t.Errorf("expected cost 0 for B via A->D->B, got %d", result.Dist["B"])
	}
	if result.Dist["D"] != 0 {
		t.Errorf("expected cost 0 for D, got %d", result.Dist["D"])
	}
}

// TestReconstructPath_CycleGuard exercises the visited-set cycle guard.
func TestReconstructPath_CycleGuard(t *testing.T) {
	// Build a manual DijkstraResult with a cycle in Prev.
	result := &graph.DijkstraResult{
		Dist: map[string]int{"A": 0, "B": 0},
		Prev: map[string]string{"A": "B", "B": "A"}, // artificial cycle
	}
	// Should not infinite-loop; should return a path and stop.
	path := result.ReconstructPath("A")
	if len(path) == 0 {
		t.Error("expected non-empty path even with cycle guard")
	}
}

func TestAddEdge_SrcNotFound(t *testing.T) {
	g := graph.NewWeightedDirectedGraph()
	// Should not panic when src does not exist
	g.AddEdge("nonexistent", "B", 0)
}

func TestAddShortCut_SrcNotFound(t *testing.T) {
	g := graph.NewWeightedDirectedGraph()
	// Should not panic when src does not exist
	g.AddShortCut("nonexistent", "B")
}

// -----------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------

func newTestSubGraph(t *testing.T, name, schema, host string) *graph.SubGraphV2 {
	t.Helper()
	sg, err := graph.NewSubGraphV2(name, []byte(schema), host)
	if err != nil {
		t.Fatalf("NewSubGraphV2(%s) failed: %v", name, err)
	}
	return sg
}
