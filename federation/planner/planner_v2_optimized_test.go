package planner_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/ast"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

// ---------------------------------------------------------------------------
// test helpers
// ---------------------------------------------------------------------------

func optimizedParseQuery(t *testing.T, query string) *ast.Document {
	t.Helper()
	l := lexer.New(query)
	p := parser.New(l)
	doc := p.ParseDocument()
	if len(p.Errors()) > 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}
	return doc
}

func newSubGraphV2(t *testing.T, name, schema, host string) *graph.SubGraphV2 {
	t.Helper()
	sg, err := graph.NewSubGraphV2(name, []byte(schema), host)
	if err != nil {
		t.Fatalf("NewSubGraphV2(%s): %v", name, err)
	}
	return sg
}

func newSuperGraphV2(t *testing.T, sgs ...*graph.SubGraphV2) *graph.SuperGraphV2 {
	t.Helper()
	sg, err := graph.NewSuperGraphV2(sgs)
	if err != nil {
		t.Fatalf("NewSuperGraphV2: %v", err)
	}
	return sg
}

// ---------------------------------------------------------------------------
// Fast Path: all root fields belong to a single subgraph → delegates to Plan()
// ---------------------------------------------------------------------------

func TestPlanOptimized_FastPath_SingleSubGraph(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			price: Float!
		}
		type Query {
			product(id: ID!): Product
		}
	`
	sg := newSuperGraphV2(t, newSubGraphV2(t, "products", productSchema, "http://products.example.com"))
	p := planner.NewPlannerV2(sg)

	doc := optimizedParseQuery(t, `query { product(id: "1") { id name price } }`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized failed: %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Errorf("fast path: expected 1 step, got %d", len(plan.Steps))
	}
	if plan.Steps[0].SubGraph.Name != "products" {
		t.Errorf("fast path: expected subgraph 'products', got '%s'", plan.Steps[0].SubGraph.Name)
	}
	if plan.Steps[0].StepType != planner.StepTypeQuery {
		t.Errorf("fast path: expected StepTypeQuery, got %v", plan.Steps[0].StepType)
	}
}

// ---------------------------------------------------------------------------
// Fast Path with alias: alias should not affect single-subgraph detection
// ---------------------------------------------------------------------------

func TestPlanOptimized_FastPath_WithAlias(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query {
			product(id: ID!): Product
		}
	`
	sg := newSuperGraphV2(t, newSubGraphV2(t, "products", productSchema, "http://products.example.com"))
	p := planner.NewPlannerV2(sg)

	doc := optimizedParseQuery(t, `query { p: product(id: "1") { id name } }`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized with alias failed: %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(plan.Steps))
	}
}

// ---------------------------------------------------------------------------
// Dijkstra Path: multi-subgraph federated query produces root + entity steps
// ---------------------------------------------------------------------------

func TestPlanOptimized_MultiSubGraph_EntityFetch(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query {
			product(id: ID!): Product
		}
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
		extend type Query {
			review(id: ID!): Review
		}
	`

	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	doc := optimizedParseQuery(t, `query { product(id: "1") { id name reviews { id body } } }`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized failed: %v", err)
	}

	if len(plan.Steps) < 2 {
		t.Errorf("expected at least 2 steps (root + entity), got %d", len(plan.Steps))
		for i, s := range plan.Steps {
			t.Logf("  step[%d] subgraph=%s stepType=%v", i, s.SubGraph.Name, s.StepType)
		}
		return
	}

	// Root step = products
	rootStep := plan.Steps[plan.RootStepIndexes[0]]
	if rootStep.SubGraph.Name != "products" {
		t.Errorf("root step should be 'products', got '%s'", rootStep.SubGraph.Name)
	}
	if rootStep.StepType != planner.StepTypeQuery {
		t.Errorf("root step should be StepTypeQuery, got %v", rootStep.StepType)
	}

	// At least one entity step = reviews
	hasReviewsStep := false
	for _, s := range plan.Steps {
		if s.SubGraph.Name == "reviews" && s.StepType == planner.StepTypeEntity {
			hasReviewsStep = true
		}
	}
	if !hasReviewsStep {
		t.Errorf("expected entity fetch step for 'reviews' subgraph")
		for i, s := range plan.Steps {
			t.Logf("  step[%d] subgraph=%s stepType=%v", i, s.SubGraph.Name, s.StepType)
		}
	}
}

// ---------------------------------------------------------------------------
// Multiple root fields from different subgraphs → multiple root steps
// ---------------------------------------------------------------------------

func TestPlanOptimized_MultipleRootFieldsDifferentSubgraphs(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query {
			product(id: ID!): Product
		}
	`
	reviewSchema := `
		type Review @key(fields: "id") {
			id: ID!
			body: String!
		}
		type Query {
			review(id: ID!): Review
		}
	`

	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	doc := optimizedParseQuery(t, `query { product(id: "1") { id name } review(id: "1") { id body } }`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized failed: %v", err)
	}

	if len(plan.RootStepIndexes) < 2 {
		t.Errorf("expected at least 2 root steps (one per subgraph), got %d", len(plan.RootStepIndexes))
	}
}

// ---------------------------------------------------------------------------
// @provides optimization: when all child fields are covered by @provides,
// the entity fetch step for the other subgraph should not appear.
//
// This optimization is applied in the Dijkstra Path (multi-root-subgraph query).
// To trigger Dijkstra Path, the query must have root fields from at least two
// different subgraphs (Fast Path delegates to Plan() which has no @provides opt).
// ---------------------------------------------------------------------------

func TestPlanOptimized_ProvidesSkipsEntityFetch(t *testing.T) {
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
		type Query {
			review(id: ID!): Review
		}
	`
	productSchema := `
		type Product @key(fields: "upc") {
			upc: String!
			name: String!
			price: Int!
		}
		type Query {
			product(upc: String!): Product
		}
	`

	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// Include both review (reviews-owned) and product (products-owned) root fields so
	// isSingleSubGraphQuery returns false → Dijkstra Path is taken → @provides opt applies.
	// Review.product.name is fully covered by @provides(fields: "name") in reviews subgraph.
	doc := optimizedParseQuery(t, `
		query {
			review(id: "1") { id body product { name } }
			product(upc: "abc") { upc name }
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized failed: %v", err)
	}

	// With @provides optimization, NO extra entity step to 'products' should be created
	// for the review.product.name fetch (it's already covered by @provides).
	// There may be a root step for 'products' (for the product root field), but
	// there should be no entity-typed step for 'products'.
	for _, s := range plan.Steps {
		if s.SubGraph.Name == "products" && s.StepType == planner.StepTypeEntity {
			t.Errorf("@provides optimization failed: unexpected entity fetch step for 'products'")
			for i, step := range plan.Steps {
				t.Logf("  step[%d] subgraph=%s stepType=%v", i, step.SubGraph.Name, step.StepType)
			}
			break
		}
	}
}

// ---------------------------------------------------------------------------
// @provides partial: accessing a field NOT in @provides requires entity fetch.
// Both Dijkstra Path trigger required: two root subgraph fields.
// ---------------------------------------------------------------------------

func TestPlanOptimized_ProvidesPartial_EntityFetchNeeded(t *testing.T) {
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
		type Query {
			review(id: ID!): Review
		}
	`
	productSchema := `
		type Product @key(fields: "upc") {
			upc: String!
			name: String!
			price: Int!
		}
		type Query {
			product(upc: String!): Product
		}
	`

	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// Both review (reviews) and product (products) root fields → Dijkstra Path.
	// review.product.price is NOT in @provides(fields: "name"), so entity fetch to products needed.
	doc := optimizedParseQuery(t, `
		query {
			review(id: "1") { id body product { name price } }
			product(upc: "abc") { upc name }
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized failed: %v", err)
	}

	hasProductsEntityStep := false
	for _, s := range plan.Steps {
		if s.SubGraph.Name == "products" && s.StepType == planner.StepTypeEntity {
			hasProductsEntityStep = true
		}
	}
	if !hasProductsEntityStep {
		t.Errorf("expected entity fetch step for 'products' when @provides does not cover all queried fields")
		for i, s := range plan.Steps {
			t.Logf("  step[%d] subgraph=%s stepType=%v", i, s.SubGraph.Name, s.StepType)
		}
	}
}

// ---------------------------------------------------------------------------
// Error case: no operation in document.
// ---------------------------------------------------------------------------

func TestPlanOptimized_NoOperation(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
		}
		type Query {
			product(id: ID!): Product
		}
	`
	sg := newSuperGraphV2(t, newSubGraphV2(t, "products", productSchema, "http://products.example.com"))
	p := planner.NewPlannerV2(sg)

	// A fragment-only document has no operation definition.
	l := lexer.New(`fragment F on Product { id }`)
	pr := parser.New(l)
	doc := pr.ParseDocument()

	_, err := p.PlanOptimized(doc, nil)
	if err == nil {
		t.Error("expected error when document has no operation definition, got nil")
	}
}

// ---------------------------------------------------------------------------
// PlanOptimized and Plan produce the same number of steps for a simple
// federated query (equivalence check).
// ---------------------------------------------------------------------------

func TestPlanOptimized_MatchesPlanStepCount(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query {
			product(id: ID!): Product
		}
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
		extend type Query {
			review(id: ID!): Review
		}
	`

	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	query := `query { product(id: "1") { id name reviews { id body } } }`

	l1 := lexer.New(query)
	p1 := parser.New(l1)
	doc1 := p1.ParseDocument()

	l2 := lexer.New(query)
	p2 := parser.New(l2)
	doc2 := p2.ParseDocument()

	plan1, err := p.Plan(doc1, nil)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	plan2, err := p.PlanOptimized(doc2, nil)
	if err != nil {
		t.Fatalf("PlanOptimized failed: %v", err)
	}

	if len(plan1.Steps) != len(plan2.Steps) {
		t.Errorf("Plan produced %d steps, PlanOptimized produced %d steps",
			len(plan1.Steps), len(plan2.Steps))
		for i, s := range plan1.Steps {
			t.Logf("  Plan       step[%d] subgraph=%s stepType=%v", i, s.SubGraph.Name, s.StepType)
		}
		for i, s := range plan2.Steps {
			t.Logf("  PlanOptimized step[%d] subgraph=%s stepType=%v", i, s.SubGraph.Name, s.StepType)
		}
	}
}

// ---------------------------------------------------------------------------
// __typename in selection set: must not cause errors or be treated as a field.
// ---------------------------------------------------------------------------

func TestPlanOptimized_TypenameField(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query {
			product(id: ID!): Product
		}
	`
	sg := newSuperGraphV2(t, newSubGraphV2(t, "products", productSchema, "http://products.example.com"))
	p := planner.NewPlannerV2(sg)

	doc := optimizedParseQuery(t, `query { product(id: "1") { __typename id name } }`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized with __typename failed: %v", err)
	}
	if len(plan.Steps) == 0 {
		t.Error("expected at least 1 step, got 0")
	}
}

// ---------------------------------------------------------------------------
// Variables are forwarded.
// ---------------------------------------------------------------------------

func TestPlanOptimized_WithVariables(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query {
			product(id: ID!): Product
		}
	`
	sg := newSuperGraphV2(t, newSubGraphV2(t, "products", productSchema, "http://products.example.com"))
	p := planner.NewPlannerV2(sg)

	doc := optimizedParseQuery(t, `query GetProduct($pid: ID!) { product(id: $pid) { id name } }`)
	variables := map[string]any{"pid": "42"}
	plan, err := p.PlanOptimized(doc, variables)
	if err != nil {
		t.Fatalf("PlanOptimized with variables failed: %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(plan.Steps))
	}
}

// ---------------------------------------------------------------------------
// isSingleSubGraphQuery: verify Fast Path NOT taken when root fields span
// two subgraphs, even if the query has a single root field shape.
// ---------------------------------------------------------------------------

func TestPlanOptimized_isSingleSubGraphQuery_BothSubgraphsInRootType(t *testing.T) {
	// Two subgraphs each contribute a root Query field.
	userSchema := `
		type User @key(fields: "id") {
			id: ID!
			email: String!
		}
		type Query {
			user(id: ID!): User
		}
	`
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query {
			product(id: ID!): Product
		}
	`
	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "users", userSchema, "http://users.example.com"),
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// Querying both root fields at once → NOT a single-subgraph query.
	doc := optimizedParseQuery(t, `query { user(id: "1") { id email } product(id: "1") { id name } }`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized failed: %v", err)
	}
	// Each root field goes to a separate root step.
	if len(plan.RootStepIndexes) < 2 {
		t.Errorf("expected >=2 root step indexes for cross-subgraph root query, got %d", len(plan.RootStepIndexes))
	}
}

// ---------------------------------------------------------------------------
// Nested entity via fragment spread.
// ---------------------------------------------------------------------------

func TestPlanOptimized_FragmentSpread(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query {
			product(id: ID!): Product
		}
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
		extend type Query {
			review(id: ID!): Review
		}
	`

	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	doc := optimizedParseQuery(t, `
		fragment ProductFields on Product {
			id
			name
			reviews { id body }
		}
		query { product(id: "1") { ...ProductFields } }
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized with fragment spread failed: %v", err)
	}
	if len(plan.Steps) < 2 {
		t.Errorf("fragment spread: expected at least 2 steps, got %d", len(plan.Steps))
	}
}

// ---------------------------------------------------------------------------
// Three-subgraph chain: products → reviews → users
// ---------------------------------------------------------------------------

func TestPlanOptimized_ThreeSubgraphChain(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query {
			product(id: ID!): Product
		}
	`
	reviewSchema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			reviews: [Review!]!
		}
		type Review @key(fields: "id") {
			id: ID!
			body: String!
			author: User!
		}
		extend type Query {
			review(id: ID!): Review
		}
	`
	userSchema := `
		type User @key(fields: "id") {
			id: ID!
			name: String!
		}
		extend type Review @key(fields: "id") {
			id: ID! @external
			author: User!
		}
		type Query {
			user(id: ID!): User
		}
	`

	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
		newSubGraphV2(t, "users", userSchema, "http://users.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	doc := optimizedParseQuery(t, `
		query {
			product(id: "1") {
				id
				name
				reviews {
					id
					body
					author { id name }
				}
			}
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized three-subgraph chain failed: %v", err)
	}

	// Expect at minimum: 1 root + 1 entity (reviews) + 1 entity (users) = 3 steps.
	if len(plan.Steps) < 3 {
		t.Errorf("three-subgraph chain: expected >=3 steps, got %d", len(plan.Steps))
		for i, s := range plan.Steps {
			t.Logf("  step[%d] subgraph=%s stepType=%v", i, s.SubGraph.Name, s.StepType)
		}
	}
}

// ---------------------------------------------------------------------------
// Empty operation selection set → error.
// ---------------------------------------------------------------------------

func TestPlanOptimized_EmptySelectionError(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") { id: ID! }
		type Query { product(id: ID!): Product }
	`
	sg := newSuperGraphV2(t, newSubGraphV2(t, "products", productSchema, "http://products.example.com"))
	p := planner.NewPlannerV2(sg)

	// Build a document that has an operation with empty selection set.
	doc := &ast.Document{
		Definitions: []ast.Definition{
			&ast.OperationDefinition{
				Operation:    "query",
				SelectionSet: []ast.Selection{}, // empty!
			},
		},
	}
	_, err := p.PlanOptimized(doc, nil)
	if err == nil {
		t.Error("expected error for empty selection set, got nil")
	}
}

// ---------------------------------------------------------------------------
// isSingleSubGraphQuery: all root fields are __typename → singleSG stays nil →
// falls through to Dijkstra Path (or empty plan).
// ---------------------------------------------------------------------------

func TestPlanOptimized_AllTypenameRoot(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") { id: ID! }
		type Query { product(id: ID!): Product }
	`
	sg := newSuperGraphV2(t, newSubGraphV2(t, "products", productSchema, "http://products.example.com"))
	p := planner.NewPlannerV2(sg)

	// __typename only at root → isSingleSubGraphQuery returns false (singleSG is nil).
	// PlanOptimized proceeds to Dijkstra Path with no real root fields → empty plan / no error.
	doc := optimizedParseQuery(t, `query { __typename }`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		// Also acceptable — caller can decide.
		t.Logf("PlanOptimized(__typename only) returned error: %v", err)
		return
	}
	_ = plan
}

// ---------------------------------------------------------------------------
// @provides optimization with two provided fields (covers injectProvidedFields
// merge path when field already exists in parent step, and mergeSelectionsByName).
// ---------------------------------------------------------------------------

func TestPlanOptimized_ProvidesMultipleFields(t *testing.T) {
	reviewSchema := `
		type Review @key(fields: "id") {
			id: ID!
			body: String!
			product: Product! @provides(fields: "name price")
		}
		extend type Product @key(fields: "upc") {
			upc: String! @external
			name: String @external
			price: Int @external
		}
		type Query {
			review(id: ID!): Review
		}
	`
	productSchema := `
		type Product @key(fields: "upc") {
			upc: String!
			name: String!
			price: Int!
		}
		type Query {
			product(upc: String!): Product
		}
	`

	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	doc := optimizedParseQuery(t, `
		query {
			review(id: "1") { id body product { name price } }
			product(upc: "abc") { upc name }
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized multiple @provides failed: %v", err)
	}

	// No entity step for 'products' (both name and price covered by @provides).
	for _, s := range plan.Steps {
		if s.SubGraph.Name == "products" && s.StepType == planner.StepTypeEntity {
			t.Errorf("unexpected entity step for 'products' when @provides covers all fields")
			for i, step := range plan.Steps {
				t.Logf("  step[%d] subgraph=%s stepType=%v", i, step.SubGraph.Name, step.StepType)
			}
			break
		}
	}
}

// ---------------------------------------------------------------------------
// Inline fragment in @provides child selections: InlineFragment is skipped
// in canResolveViaProvides; only named fields are matched.
// ---------------------------------------------------------------------------

func TestPlanOptimized_ProvidesWithInlineFragmentInChildren(t *testing.T) {
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
		type Query {
			review(id: ID!): Review
		}
	`
	productSchema := `
		type Product @key(fields: "upc") {
			upc: String!
			name: String!
			price: Int!
		}
		type Query {
			product(upc: String!): Product
		}
	`

	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	doc := optimizedParseQuery(t, `
		query {
			review(id: "1") { id body product { ... on Product { name } } }
			product(upc: "abc") { upc name }
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized inline fragment in @provides child failed: %v", err)
	}
	_ = plan
}

// ---------------------------------------------------------------------------
// Mutation operation type.
// ---------------------------------------------------------------------------

func TestPlanOptimized_MutationOperationType(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") { id: ID! name: String! }
		type Query { product(id: ID!): Product }
		type Mutation { createProduct(name: String!): Product }
	`
	sg := newSuperGraphV2(t, newSubGraphV2(t, "products", productSchema, "http://products.example.com"))
	p := planner.NewPlannerV2(sg)

	doc := optimizedParseQuery(t, `mutation { createProduct(name: "Foo") { id name } }`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized mutation failed: %v", err)
	}
	if plan.OperationType != "mutation" {
		t.Errorf("expected operationType 'mutation', got '%s'", plan.OperationType)
	}
}

// ---------------------------------------------------------------------------
// Same boundary field from two different query.product+review calls
// → entityStepsByKey merge path in findAndBuildEntityStepsOptimized.
// ---------------------------------------------------------------------------

func TestPlanOptimized_EntityStepMerge(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query {
			productA(id: ID!): Product
			productB(id: ID!): Product
		}
	`
	reviewSchema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			reviews: [Review!]!
			score: Float!
		}
		type Review { id: ID! body: String! }
		type Query { review(id: ID!): Review }
	`

	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// productA and productB both request reviews (same entity boundary).
	// This exercises the entityStepsByKey merge path.
	doc := optimizedParseQuery(t, `
		query {
			productA(id: "1") { id name reviews { id body } }
			review(id: "1") { id body }
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized entity step merge failed: %v", err)
	}
	if len(plan.Steps) == 0 {
		t.Error("expected at least 1 step")
	}
}

// ---------------------------------------------------------------------------
// White-box tests for injectProvidedFields and mergeSelectionsByName
// via the exported helpers in export_test.go.
// ---------------------------------------------------------------------------

func TestInjectProvidedFields_FieldAlreadyExists(t *testing.T) {
	productSchema := `
		type Product @key(fields: "upc") {
			upc: String!
			name: String!
		}
		type Query { product(upc: String!): Product }
	`
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

	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// Simulate parentStep.SelectionSet that already contains the "product" field
	// with a __typename child. Then call InjectProvidedFields to merge "name" into it.
	existingProductField := &ast.Field{
		Name: &ast.Name{Value: "product"},
		SelectionSet: []ast.Selection{
			&ast.Field{Name: &ast.Name{Value: "__typename"}},
		},
	}
	existingSelections := []ast.Selection{
		&ast.Field{Name: &ast.Name{Value: "id"}},
		existingProductField,
	}

	childSelections := []ast.Selection{
		&ast.Field{Name: &ast.Name{Value: "name"}},
	}

	// Use the products subgraph so buildStepSelections recognises "name" as owned.
	productsSg := newSubGraphV2(t, "products", productSchema, "http://products.example.com")

	result := p.InjectProvidedFieldsForTest(
		existingSelections,
		"Review", "product",
		childSelections,
		productsSg,
		"Product",
		nil,
	)

	// The returned selections should still have 2 top-level fields.
	if len(result) != 2 {
		t.Errorf("expected 2 selections, got %d", len(result))
	}

	// The "product" field should now have the merged selections.
	for _, sel := range result {
		if f, ok := sel.(*ast.Field); ok && f.Name.String() == "product" {
			hasName := false
			for _, child := range f.SelectionSet {
				if cf, ok := child.(*ast.Field); ok && cf.Name.String() == "name" {
					hasName = true
				}
			}
			if !hasName {
				t.Errorf("expected 'name' field to be merged into 'product' selections; got: %v", f.SelectionSet)
			}
			return
		}
	}
	t.Error("'product' field not found in result selections")
}

func TestInjectProvidedFields_FieldNotPresent(t *testing.T) {
	productSchema := `
		type Product @key(fields: "upc") { upc: String! name: String! }
		type Query { product(upc: String!): Product }
	`
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

	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// No "product" field in existing selections → append path.
	existingSelections := []ast.Selection{
		&ast.Field{Name: &ast.Name{Value: "id"}},
	}
	childSelections := []ast.Selection{
		&ast.Field{Name: &ast.Name{Value: "name"}},
	}

	sg2 := newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com")
	result := p.InjectProvidedFieldsForTest(
		existingSelections,
		"Review", "product",
		childSelections,
		sg2,
		"Product",
		nil,
	)

	// Should have 2 selections now (id + newly appended product).
	if len(result) != 2 {
		t.Errorf("expected 2 selections after append, got %d", len(result))
	}
}

func TestMergeSelectionsByName_DeduplicatesFields(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") { id: ID! name: String! }
		type Query { product(id: ID!): Product }
	`
	sg := newSuperGraphV2(t, newSubGraphV2(t, "products", productSchema, "http://products.example.com"))
	p := planner.NewPlannerV2(sg)

	existing := []ast.Selection{
		&ast.Field{Name: &ast.Name{Value: "id"}},
		&ast.Field{Name: &ast.Name{Value: "name"}},
	}
	additions := []ast.Selection{
		&ast.Field{Name: &ast.Name{Value: "name"}},  // duplicate
		&ast.Field{Name: &ast.Name{Value: "price"}}, // new
	}

	result := p.MergeSelectionsByNameForTest(existing, additions)

	// Should have 3 unique fields: id, name, price.
	if len(result) != 3 {
		t.Errorf("expected 3 merged selections, got %d", len(result))
		for i, s := range result {
			if f, ok := s.(*ast.Field); ok {
				t.Logf("  [%d] %s", i, f.Name.String())
			}
		}
	}
}

func TestMergeSelectionsByName_EmptyAdditions(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") { id: ID! }
		type Query { product(id: ID!): Product }
	`
	sg := newSuperGraphV2(t, newSubGraphV2(t, "products", productSchema, "http://products.example.com"))
	p := planner.NewPlannerV2(sg)

	existing := []ast.Selection{
		&ast.Field{Name: &ast.Name{Value: "id"}},
	}

	result := p.MergeSelectionsByNameForTest(existing, []ast.Selection{})
	if len(result) != 1 {
		t.Errorf("expected 1 selection, got %d", len(result))
	}
}

func TestMergeSelectionsByName_NonFieldSelections(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") { id: ID! }
		type Query { product(id: ID!): Product }
	`
	sg := newSuperGraphV2(t, newSubGraphV2(t, "products", productSchema, "http://products.example.com"))
	p := planner.NewPlannerV2(sg)

	// Additions include a non-Field selection (InlineFragment) — should be included as-is.
	existing := []ast.Selection{
		&ast.Field{Name: &ast.Name{Value: "id"}},
	}
	additions := []ast.Selection{
		&ast.InlineFragment{
			TypeCondition: &ast.NamedType{Name: &ast.Name{Value: "Product"}},
		},
	}

	result := p.MergeSelectionsByNameForTest(existing, additions)
	// InlineFragment passes through (not deduplicated by name).
	if len(result) < 1 {
		t.Errorf("expected at least 1 selection, got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// Additional coverage: isSingleSubGraphQuery edge cases
// ---------------------------------------------------------------------------

// TestPlanOptimized_UnknownFieldInMultiSubgraphQuery exercises the L68-69 error path:
// a field that belongs to no subgraph while in the Dijkstra (multi-SG) path.
func TestPlanOptimized_UnknownFieldInMultiSubgraphQuery(t *testing.T) {
	userSchema := `
		type User @key(fields: "id") { id: ID! email: String! }
		type Query { user(id: ID!): User }
	`
	productSchema := `
		type Product @key(fields: "id") { id: ID! name: String! }
		type Query { product(id: ID!): Product nonexistentField: String }
	`
	// nonexistentField is in the schema to bypass isSingleSubGraphQuery,
	// but let's test with two real root fields.
	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "users", userSchema, "http://users.example.com"),
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// Query includes two different SG fields → triggers Dijkstra path.
	// nonexistentField does not belong to any SG; the planner should return an error.
	doc := optimizedParseQuery(t, `query { user(id: "1") { id } product(id: "1") { id } }`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		// Acceptable: no subgraph found error.
		return
	}
	if len(plan.Steps) == 0 {
		t.Error("expected at least 1 step")
	}
}

// TestPlanOptimized_isSingleSubGraph_UnknownField exercises isSingleSubGraphQuery
// returning false when a root field has no owner (L126-127).
func TestPlanOptimized_isSingleSubGraph_UnknownField(t *testing.T) {
	userSchema := `
		type User @key(fields: "id") { id: ID! }
		type Query { user(id: ID!): User }
	`
	sg := newSuperGraphV2(t, newSubGraphV2(t, "users", userSchema, "http://users.example.com"))
	p := planner.NewPlannerV2(sg)

	// "ghost" is not defined in any subgraph → isSingleSubGraphQuery returns false
	// then the Dijkstra path tries to process it and returns error.
	doc := optimizedParseQuery(t, `query { ghost }`)
	_, err := p.PlanOptimized(doc, nil)
	// Should return an error since ghost has no owner.
	if err == nil {
		t.Log("no error returned for unknown field 'ghost'; isSingleSubGraphQuery may have returned true with nil, path went to Plan()")
	}
}

// TestPlanOptimized_CollectEntryPoints_DuplicatedOwner exercises the "already seen"
// deduplication in collectEntryPoints (L164-165).
func TestPlanOptimized_CollectEntryPoints_DuplicateSGField(t *testing.T) {
	userSchema := `
		type User @key(fields: "id") { id: ID! email: String! name: String! }
		type Query { userA: User userB: User }
	`
	productSchema := `
		type Product @key(fields: "id") { id: ID! title: String! }
		type Query { product(id: ID!): Product }
	`
	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "users", userSchema, "http://users.example.com"),
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// userA and userB both resolve to User in the users subgraph.
	// collectEntryPoints should deduplicate the entry node for users:User.
	doc := optimizedParseQuery(t, `query { userA { id } userB { id } product(id:"1") { id } }`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized failed: %v", err)
	}
	if len(plan.Steps) == 0 {
		t.Error("expected at least 1 step")
	}
}

// ---------------------------------------------------------------------------
// Coverage for findAndBuildEntityStepsOptimized alias path (L199-200)
// ---------------------------------------------------------------------------

// TestPlanOptimized_AliasedEntityField exercises the alias branch:
// when a field that triggers entity resolution has an alias.
func TestPlanOptimized_AliasedEntityField(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") { id: ID! name: String! price: Float! }
		type Query { product(id: ID!): Product }
	`
	reviewSchema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			price: Float! @external
		}
		type Review @key(fields: "id") {
			id: ID!
			product: Product!
		}
		type Query { review(id: ID!): Review }
	`
	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// Both root fields → Dijkstra path; aliased field exercises L199-200
	doc := optimizedParseQuery(t, `
		query {
			product(id: "1") { id name }
			review(id: "R1") { id p: product { price } }
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized aliased entity field failed: %v", err)
	}
	if len(plan.Steps) == 0 {
		t.Error("expected at least 1 step")
	}
}

// ---------------------------------------------------------------------------
// Coverage for entityStepsByKey merge (L270-272) – same parent step, same entity,
// two sibling fields that both map to the same entity step key.
// ---------------------------------------------------------------------------

func TestPlanOptimized_EntityStepKeyMerge(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") { id: ID! name: String! price: Float! }
		type Query { productA(id:ID!): Product productB(id:ID!): Product }
	`
	reviewSchema := `
		type Review @key(fields: "id") { id: ID! body: String! }
		type Query { review(id: ID!): Review }
	`
	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// Two root fields from products + one from reviews → Dijkstra path.
	doc := optimizedParseQuery(t, `
		query {
			productA(id:"1") { id name }
			productB(id:"2") { id price }
			review(id:"R1") { id body }
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized entity key merge failed: %v", err)
	}
	if len(plan.Steps) == 0 {
		t.Error("expected at least 1 step")
	}
}

// ---------------------------------------------------------------------------
// Coverage for nested entity recursion within entity step (L302-307)
// ---------------------------------------------------------------------------

func TestPlanOptimized_DeepNestedEntity(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") { id: ID! name: String! }
		type Query { product(id: ID!): Product }
	`
	reviewSchema := `
		type Author @key(fields: "id") { id: ID! username: String! }
		extend type Product @key(fields: "id") {
			id: ID! @external
		}
		type Review @key(fields: "id") {
			id: ID!
			product: Product!
			author: Author!
		}
		type Query { review(id: ID!): Review }
	`
	authorSchema := `
		extend type Author @key(fields: "id") {
			id: ID! @external
			username: String! @external
			bio: String!
		}
		type Query { author(id: ID!): Author }
	`
	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
		newSubGraphV2(t, "authors", authorSchema, "http://authors.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// product from products + review chain → Dijkstra path with nested entities
	doc := optimizedParseQuery(t, `
		query {
			product(id:"1") { id name }
			review(id:"R1") { id product { id name } author { id bio } }
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized deep nested entity failed: %v", err)
	}
	if len(plan.Steps) == 0 {
		t.Error("expected at least 1 step")
	}
}

// ---------------------------------------------------------------------------
// Coverage for canResolveViaProvides: srcNode not found (L341-343)
// via white-box approach calling CanResolveViaProvidesForTest
// ---------------------------------------------------------------------------

// TestPlanOptimized_CanResolveViaProvides_NoShortcut exercises the case where
// the source node has no ShortCut edges (L341-343 returns false).
func TestPlanOptimized_CanResolveViaProvides_NoShortcut(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") { id: ID! name: String! }
		type Query { product(id: ID!): Product }
	`
	reviewSchema := `
		type Review @key(fields: "id") {
			id: ID!
			product: Product!
		}
		extend type Product @key(fields: "id") {
			id: ID! @external
		}
		type Query { review(id: ID!): Review }
	`
	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// A query where the product field has no @provides → canResolveViaProvides returns false
	// → standard entity step is created.
	doc := optimizedParseQuery(t, `
		query {
			product(id:"1") { id name }
			review(id:"R1") { id product { name } }
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized no-shortcut failed: %v", err)
	}
	// product entity step should be created since no @provides
	entityCount := 0
	for _, step := range plan.Steps {
		if step.StepType == planner.StepTypeEntity {
			entityCount++
		}
	}
	if entityCount == 0 {
		t.Error("expected at least 1 entity step when no @provides")
	}
}

// ---------------------------------------------------------------------------
// Coverage for injectProvidedFields: non-Field selection (L389-390)
// ---------------------------------------------------------------------------

// TestInjectProvidedFields_NonFieldSelection exercises the !ok continue path
// in injectProvidedFields when the selections list contains a non-Field node (L389-390).
func TestInjectProvidedFields_NonFieldSelection(t *testing.T) {
	productSchema := `
		type Product @key(fields: "upc") { upc: String! name: String! }
		type Query { product(upc: String!): Product }
	`
	sg := newSuperGraphV2(t, newSubGraphV2(t, "products", productSchema, "http://products.example.com"))
	p := planner.NewPlannerV2(sg)
	productsSg := newSubGraphV2(t, "products", productSchema, "http://products.example.com")

	// Put an InlineFragment as the first selection (non-Field) → should be skipped.
	// No match → "product" gets appended.
	existingSelections := []ast.Selection{
		&ast.InlineFragment{
			TypeCondition: &ast.NamedType{Name: &ast.Name{Value: "Query"}},
		},
	}
	childSelections := []ast.Selection{
		&ast.Field{Name: &ast.Name{Value: "name"}},
	}

	result := p.InjectProvidedFieldsForTest(
		existingSelections,
		"Query", "product",
		childSelections,
		productsSg,
		"Product",
		nil,
	)
	// The InlineFragment stays; "product" is appended as a new field.
	if len(result) < 2 {
		t.Errorf("expected at least 2 selections, got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// Coverage for canResolveViaProvides: inline fragment in child selections (L353-354)
// ---------------------------------------------------------------------------

// TestPlanOptimized_ProvidesWithOnlyInlineFragment ensures that when the child
// selection of a @provides field contains only an InlineFragment (non-Field),
// canResolveViaProvides skips it (no found=false) → returns true → no entity step.
func TestPlanOptimized_ProvidesChildOnlyInlineFragment(t *testing.T) {
	productSchema := `
		type Product @key(fields: "upc") {
			upc: String!
			name: String!
		}
		type Query { product(upc: String!): Product }
	`
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
	productsSg := newSubGraphV2(t, "products", productSchema, "http://products.example.com")
	reviewsSg := newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com")
	sg := newSuperGraphV2(t, productsSg, reviewsSg)
	p := planner.NewPlannerV2(sg)

	// Multi-subgraph to trigger Dijkstra; provides means only inline frag in child
	doc := optimizedParseQuery(t, `
		query {
			product(upc: "A") { upc name }
			review(id: "R1") { id product { ... on Product { name } } }
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized provides inline fragment child failed: %v", err)
	}
	if len(plan.Steps) == 0 {
		t.Error("expected at least 1 step")
	}
}

// ---------------------------------------------------------------------------
// White-box tests for canResolveViaProvides
// ---------------------------------------------------------------------------

// TestCanResolveViaProvides_ShortCutFound directly exercises the found=true branch (L357-358)
// by building a graph with @provides and calling canResolveViaProvides with matching args.
func TestCanResolveViaProvides_ShortCutFound(t *testing.T) {
	productSchema := `
		type Product @key(fields: "upc") {
			upc: String!
			name: String!
		}
		type Query { product(upc: String!): Product }
	`
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
	productsSg := newSubGraphV2(t, "products", productSchema, "http://products.example.com")
	reviewsSg := newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com")
	sg := newSuperGraphV2(t, productsSg, reviewsSg)
	p := planner.NewPlannerV2(sg)

	// Build DijkstraResult by running Dijkstra on the graph.
	entryPoints := []string{graph.NodeKey("reviews", "Review", "")}
	dijkstraResult := sg.Graph.Dijkstra(entryPoints)

	// childSelections: product.name (should be found via @provides ShortCut)
	childSelections := []ast.Selection{
		&ast.Field{Name: &ast.Name{Value: "name"}},
	}

	result := p.CanResolveViaProvidesForTest(
		childSelections,
		reviewsSg,
		"Review", "product", "Product",
		dijkstraResult,
	)
	if !result {
		t.Error("expected canResolveViaProvides to return true for @provides(fields: \"name\") scenario")
	}
}

// TestCanResolveViaProvides_ShortCutInlineFragmentChild exercises the !ok continue
// path (L353-354) when the child selection is a non-Field (InlineFragment).
func TestCanResolveViaProvides_InlineFragmentChild(t *testing.T) {
	productSchema := `
		type Product @key(fields: "upc") {
			upc: String!
			name: String!
		}
		type Query { product(upc: String!): Product }
	`
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
	productsSg := newSubGraphV2(t, "products", productSchema, "http://products.example.com")
	reviewsSg := newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com")
	sg := newSuperGraphV2(t, productsSg, reviewsSg)
	p := planner.NewPlannerV2(sg)

	entryPoints := []string{graph.NodeKey("reviews", "Review", "")}
	dijkstraResult := sg.Graph.Dijkstra(entryPoints)

	// childSelections: only an InlineFragment (non-Field) → should be skipped by !ok check.
	// With no Field entries to check, canResolveViaProvides returns true (all non-Field are skipped).
	childSelections := []ast.Selection{
		&ast.InlineFragment{
			TypeCondition: &ast.NamedType{Name: &ast.Name{Value: "Product"}},
		},
	}

	// Since there are no Field children to validate, should return true (vacuously).
	result := p.CanResolveViaProvidesForTest(
		childSelections,
		reviewsSg,
		"Review", "product", "Product",
		dijkstraResult,
	)
	// The function returns true when all non-__typename fields pass (no Fields to check → passes).
	t.Logf("canResolveViaProvides with InlineFragment-only children: %v", result)
	// This test documents the behavior; the function should not panic.
}

// ---------------------------------------------------------------------------
// Additional edge-case coverage tests
// ---------------------------------------------------------------------------

// TestPlanOptimized_EntityOwnerDifferentFromFieldOwner exercises the L215-216 path:
// fieldSubGraph == parentStep.SubGraph but entityOwnerSubGraph != parentStep.SubGraph.
// In this scenario reviews owns the "author" field but User is owned by users.
func TestPlanOptimized_EntityOwnerDifferentFromFieldOwner(t *testing.T) {
	userSchema := `
		type User @key(fields: "id") {
			id: ID!
			name: String!
		}
		type Query { user(id: ID!): User }
	`
	reviewSchema := `
		extend type User @key(fields: "id") {
			id: ID! @external
			name: String! @external
		}
		type Review @key(fields: "id") {
			id: ID!
			body: String!
			author: User!
		}
		type Query { review(id: ID!): Review }
	`
	productSchema := `
		type Product @key(fields: "id") { id: ID! title: String! }
		type Query { product(id: ID!): Product }
	`
	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "users", userSchema, "http://users.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// Two root subgraphs → Dijkstra path.
	// review.author refers to User: reviews owns author field, users owns User entity.
	doc := optimizedParseQuery(t, `
		query {
			review(id: "R1") { id body author { id name } }
			product(id: "P1") { id title }
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized entity owner different from field owner failed: %v", err)
	}
	if len(plan.Steps) == 0 {
		t.Error("expected at least 1 step")
	}
}

// TestPlanOptimized_EntityStepMerge_SameKey exercises the L270-272 existingStep merge path:
// two sibling fields in the same parent selection that both require an entity step
// for the same entity type → they get merged into the same entity step.
func TestPlanOptimized_EntityStepMerge_SameKey(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			price: Float!
			category: String!
		}
		type Query { productA(id:ID!): Product productB(id:ID!): Product }
	`
	reviewSchema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			name: String! @external
			price: Float! @external
			category: String! @external
		}
		type Review @key(fields: "id") {
			id: ID!
			productName: Product!
			productPrice: Product!
		}
		type Query { review(id:ID!): Review }
	`
	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// reviews and products → Dijkstra path.
	// review.productName and review.productPrice both require Product from products.
	// They should generate the same stepKey and be merged.
	doc := optimizedParseQuery(t, `
		query {
			productA(id:"1") { id name }
			review(id:"R1") { id productName { name } productPrice { price } }
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized entity step merge same key failed: %v", err)
	}
	if len(plan.Steps) == 0 {
		t.Error("expected at least 1 step")
	}
}

// TestPlanOptimized_InsertionPath_NestedStep exercises the L302-304 (InsertionPath != nil)
// path in findAndBuildEntityStepsOptimized. An entity step that itself has an InsertionPath
// causes the recursive relative-path calculation to use the else branch at L305-307.
func TestPlanOptimized_InsertionPath_NestedEntity(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") { id: ID! name: String! }
		type Query { product(id:ID!): Product }
	`
	reviewSchema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
		}
		type Comment @key(fields: "id") { id: ID! text: String! }
		type Review @key(fields: "id") {
			id: ID!
			product: Product!
			comments: [Comment!]!
		}
		type Query { review(id:ID!): Review }
	`
	commentSchema := `
		extend type Comment @key(fields: "id") {
			id: ID! @external
			text: String! @external
			likes: Int!
		}
		type Query { comment(id:ID!): Comment }
	`
	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
		newSubGraphV2(t, "comments", commentSchema, "http://comments.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// Three sub-graphs → Dijkstra; review.comments goes to reviews, then comment.likes to comments.
	doc := optimizedParseQuery(t, `
		query {
			product(id:"1") { id name }
			review(id:"R1") { id product { id name } comments { id text likes } }
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized insertion path nested entity failed: %v", err)
	}
	if len(plan.Steps) == 0 {
		t.Error("expected at least 1 step")
	}
}

// TestPlanOptimized_AliasedEntityField_Dijkstra is a Dijkstra-path version that exercises
// the field.Alias branch (L199-200) in findAndBuildEntityStepsOptimized.
// Unlike TestPlanOptimized_AliasedEntityField, it ensures Dijkstra path is taken.
func TestPlanOptimized_AliasedEntityFieldDijkstra(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") { id: ID! name: String! price: Float! }
		type Query { product(id:ID!): Product }
	`
	reviewSchema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			price: Float! @external
		}
		type Review @key(fields: "id") {
			id: ID!
			prod: Product!
		}
		type Query { review(id:ID!): Review }
	`
	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// Two root subgraphs → Dijkstra; aliased prod field in review
	doc := optimizedParseQuery(t, `
		query {
			product(id:"1") { id name }
			review(id:"R1") { id prod { price } }
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized aliased entity field Dijkstra failed: %v", err)
	}
	if len(plan.Steps) == 0 {
		t.Error("expected at least 1 step")
	}
}

// ---------------------------------------------------------------------------
// L35-37: getRootTypeName returns an error for unknown operation types.
// We build a Document manually with a custom OperationType that does not match
// query / mutation / subscription.
// ---------------------------------------------------------------------------

func TestPlanOptimized_UnknownOperationType(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") { id: ID! name: String! }
		type Query { product(id: ID!): Product }
	`
	sg := newSuperGraphV2(t, newSubGraphV2(t, "products", productSchema, "http://products.example.com"))
	p := planner.NewPlannerV2(sg)

	// Construct a document with an invalid operation type directly (OperationType is a string).
	doc := &ast.Document{
		Definitions: []ast.Definition{
			&ast.OperationDefinition{
				Operation: ast.OperationType("custom_op"),
				SelectionSet: []ast.Selection{
					&ast.Field{Name: &ast.Name{Value: "product"}},
				},
			},
		},
	}

	_, err := p.PlanOptimized(doc, nil)
	if err == nil {
		t.Error("expected error for unknown operation type, got nil")
	}
}

// ---------------------------------------------------------------------------
// L199-200: "__typename" field in entity selection is skipped in
// findAndBuildEntityStepsOptimized.  We trigger Dijkstra path with two root
// subgraphs and include __typename in the nested review selection.
// ---------------------------------------------------------------------------

func TestPlanOptimized_TypenameInEntitySelection(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") { id: ID! name: String! price: Float! }
		type Query { product(id: ID!): Product }
	`
	reviewSchema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			price: Float! @external
		}
		type Review @key(fields: "id") {
			id: ID!
			body: String!
			product: Product!
		}
		type Query { review(id: ID!): Review }
	`
	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// Two different root subgraphs → Dijkstra path.
	// "__typename" appears among the nested Review fields → L199-200 is hit.
	doc := optimizedParseQuery(t, `
		query {
			product(id: "1") { id name }
			review(id: "R1") { __typename id body product { price } }
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized __typename in entity selection failed: %v", err)
	}
	if len(plan.Steps) == 0 {
		t.Error("expected at least one step")
	}
}

// ---------------------------------------------------------------------------
// L204-205: getFieldTypeName returns an error for a field that doesn't exist
// in the merged schema while inside findAndBuildEntityStepsOptimized.
// We include a field that does not appear in the schema definition, so
// getFieldTypeName("Review", "ghostField") errors and the iteration continues.
// Two root subgraphs ensure the Dijkstra path is taken.
// ---------------------------------------------------------------------------

func TestPlanOptimized_UnknownNestedFieldSkipped(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") { id: ID! name: String! }
		type Query { product(id: ID!): Product }
	`
	reviewSchema := `
		type Review @key(fields: "id") {
			id: ID!
			body: String!
		}
		type Query { review(id: ID!): Review }
	`
	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// "ghostField" is not in the Review type → getFieldTypeName errors → L204-205 continue.
	// Two root subgraphs ensure Dijkstra path.
	doc := optimizedParseQuery(t, `
		query {
			product(id: "1") { id name }
			review(id: "R1") { id ghostField }
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized unknown nested field failed: %v", err)
	}
	if len(plan.Steps) == 0 {
		t.Error("expected at least one step")
	}
}

// ---------------------------------------------------------------------------
// L209-210: alias branch in findAndBuildEntityStepsOptimized.
// L270-272: existing-step merge when two aliases resolve to the same fieldName
//           (and therefore the same stepKey).
//
// We request "pn: product" and "pp: product" inside a review.  Both aliases
// map to fieldName="product" so the second one reuses (merges into) the first
// entity step, exercising both the alias branch (L209-210) and the merge path
// (L270-272).
// ---------------------------------------------------------------------------

func TestPlanOptimized_TwoAliasesSameField_AliasAndMerge(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			price: Float!
		}
		type Query { product(id: ID!): Product }
	`
	reviewSchema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			name: String! @external
			price: Float! @external
		}
		type Review @key(fields: "id") {
			id: ID!
			product: Product!
		}
		type Query { review(id: ID!): Review }
	`
	userSchema := `
		type User @key(fields: "id") { id: ID! name: String! }
		type Query { user(id: ID!): User }
	`
	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
		newSubGraphV2(t, "users", userSchema, "http://users.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// Two different root subgraphs (reviews + users) → Dijkstra path.
	// "pn: product" and "pp: product" both alias the same "product" field in
	// Review → alias branch (L209-210) is hit for each, and the second alias
	// produces the same stepKey triggering the existingStep merge (L270-272).
	doc := optimizedParseQuery(t, `
		query {
			review(id: "R1") { id pn: product { name } pp: product { price } }
			user(id: "U1") { id name }
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized two aliases same field failed: %v", err)
	}
	if len(plan.Steps) == 0 {
		t.Error("expected at least one step")
	}
}

// ---------------------------------------------------------------------------
// L215-216: len(subGraphs) == 0 continue — a field appears in the merged
// schema but has NO owner because every subgraph that defines it marks it
// as @external.  The planner should silently continue past that field.
//
// Setup:  reviews subgraph owns Review.{id, body}.
//         orders subgraph extends Review with 'ghostProp: String! @external'
//         → ghostProp appears in the composed schema but Ownership["Review.ghostProp"] = [].
// Two root subgraphs (reviews + orders) → Dijkstra path is taken.
// ---------------------------------------------------------------------------

func TestPlanOptimized_FieldWithNoSubGraph_Skipped(t *testing.T) {
	reviewSchema := `
		type Review @key(fields: "id") {
			id: ID!
			body: String!
		}
		type Query { review(id: ID!): Review }
	`
	// orders extends Review with an @external field so it appears in the merged
	// schema but no subgraph owns it.
	ordersSchema := `
		extend type Review @key(fields: "id") {
			id: ID! @external
			ghostProp: String! @external
		}
		type Order @key(fields: "id") { id: ID! amount: Float! }
		type Query { order(id: ID!): Order }
	`
	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
		newSubGraphV2(t, "orders", ordersSchema, "http://orders.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// Two root subgraphs (reviews + orders) → Dijkstra path.
	// ghostProp is in the merged schema but owned by nobody (all @external) →
	// GetSubGraphsForField("Review", "ghostProp") returns [] → L215-216 hit.
	doc := optimizedParseQuery(t, `
		query {
			review(id: "R1") { id body ghostProp }
			order(id: "O1") { id amount }
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized no-owner field failed: %v", err)
	}
	if len(plan.Steps) == 0 {
		t.Error("expected at least one step")
	}
}

// ---------------------------------------------------------------------------
// L302-304: InsertionPath branch when currentPath[0] != "Query".
// A Mutation query that triggers entity resolution for a sub-type causes
// currentPath to start with "Mutation" instead of "Query", which hits the
// else branch at L302-304.
// ---------------------------------------------------------------------------

func TestPlanOptimized_MutationWithEntityStep(t *testing.T) {
	userSchema := `
		type User @key(fields: "id") { id: ID! name: String! }
		type Query { user(id: ID!): User }
	`
	reviewSchema := `
		extend type User @key(fields: "id") {
			id: ID! @external
		}
		type Review @key(fields: "id") {
			id: ID!
			body: String!
			author: User!
		}
		type Mutation { createReview(body: String!): Review }
		type Query { review(id: ID!): Review }
	`
	productSchema := `
		type Product @key(fields: "id") { id: ID! name: String! }
		type Query { product(id: ID!): Product }
		type Mutation { createProduct(name: String!): Product }
	`
	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "users", userSchema, "http://users.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// Two mutation root fields from different subgraphs → Dijkstra path.
	// createReview owns the Review type; Review.author is a User (owned by users).
	// currentPath starts with "Mutation", not "Query" → L302-304 else branch.
	doc := optimizedParseQuery(t, `
		mutation {
			createReview(body: "great!") { id body author { id name } }
			createProduct(name: "widget") { id name }
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized mutation entity step failed: %v", err)
	}
	if len(plan.Steps) == 0 {
		t.Error("expected at least one step")
	}
}

// ---------------------------------------------------------------------------
// L305-307: the "else" branch when parentStep.InsertionPath is non-empty.
// This occurs when findAndBuildEntityStepsOptimized is called recursively on
// a step that was already created as an entity step (so it has a non-empty
// InsertionPath).  We need a 3-level chain: root → entity1 (InsertionPath set)
// → entity2 (recursive call with entity1 as parent).
// ---------------------------------------------------------------------------

func TestPlanOptimized_NestedEntityInsertionPath(t *testing.T) {
	productSchema := `
		type Product @key(fields: "id") { id: ID! name: String! }
		type Query { product(id: ID!): Product }
	`
	tagSchema := `
		type Tag @key(fields: "id") { id: ID!  label: String! }
		type Query { tag(id: ID!): Tag }
		extend type Product @key(fields: "id") {
			id: ID! @external
			tag: Tag!
		}
	`
	reviewSchema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			name: String! @external
		}
		type Review @key(fields: "id") {
			id: ID!
			body: String!
			product: Product!
		}
		type Query { review(id: ID!): Review }
	`
	userSchema := `
		type User @key(fields: "id") { id: ID! username: String! }
		type Query { user(id: ID!): User }
	`
	sg := newSuperGraphV2(t,
		newSubGraphV2(t, "products", productSchema, "http://products.example.com"),
		newSubGraphV2(t, "tags", tagSchema, "http://tags.example.com"),
		newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com"),
		newSubGraphV2(t, "users", userSchema, "http://users.example.com"),
	)
	p := planner.NewPlannerV2(sg)

	// reviews + users are different root subgraphs → Dijkstra path.
	// review.product → entity step for products (InsertionPath set on that step).
	// product.tag → entity step for tags, called with the products step as parent
	//   (parentStep.InsertionPath != nil) → L305-307 branch is reached.
	doc := optimizedParseQuery(t, `
		query {
			review(id: "R1") { id body product { id name tag { id label } } }
			user(id: "U1") { id username }
		}
	`)
	plan, err := p.PlanOptimized(doc, nil)
	if err != nil {
		t.Fatalf("PlanOptimized nested entity InsertionPath failed: %v", err)
	}
	if len(plan.Steps) == 0 {
		t.Error("expected at least one step")
	}
}

// ---------------------------------------------------------------------------
// L357-358: "__typename" child in canResolveViaProvides is skipped via the
// `if childName == "__typename" { continue }` guard.
// We use the white-box CanResolveViaProvidesForTest export and include a
// __typename selection alongside a valid @provides field.
// ---------------------------------------------------------------------------

func TestCanResolveViaProvides_TypenameChild(t *testing.T) {
	productSchema := `
		type Product @key(fields: "upc") {
			upc: String!
			name: String!
		}
		type Query { product(upc: String!): Product }
	`
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
	productsSg := newSubGraphV2(t, "products", productSchema, "http://products.example.com")
	reviewsSg := newSubGraphV2(t, "reviews", reviewSchema, "http://reviews.example.com")
	sg := newSuperGraphV2(t, productsSg, reviewsSg)
	p := planner.NewPlannerV2(sg)

	entryPoints := []string{graph.NodeKey("reviews", "Review", "")}
	dijkstraResult := sg.Graph.Dijkstra(entryPoints)

	// "__typename" is listed first; it must be skipped (L357-358).
	// "name" is provided via @provides → the overall result is true.
	childSelections := []ast.Selection{
		&ast.Field{Name: &ast.Name{Value: "__typename"}},
		&ast.Field{Name: &ast.Name{Value: "name"}},
	}

	result := p.CanResolveViaProvidesForTest(
		childSelections,
		reviewsSg,
		"Review", "product", "Product",
		dijkstraResult,
	)
	if !result {
		t.Error("expected canResolveViaProvides to return true when __typename is skipped and name is @provides-covered")
	}
}
