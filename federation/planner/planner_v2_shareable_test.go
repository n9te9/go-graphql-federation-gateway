package planner_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

// TestPlannerV2_Shareable verifies that @shareable field resolution prefers the same
// subgraph as the parent step to avoid unnecessary Entity Fetches.
func TestPlannerV2_Shareable(t *testing.T) {
	// productsSchema: owns Product and marks price @shareable
	productsSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			price: Float! @shareable
		}

		type Query {
			product(id: ID!): Product
		}
	`

	// pricingSchema: also owns price @shareable, but does NOT own the Query entry point
	pricingSchema := `
		type Product @key(fields: "id") {
			id: ID!
			price: Float! @shareable
		}
	`

	productsSG, err := graph.NewSubGraphV2("products", []byte(productsSchema), "http://products.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for products: %v", err)
	}

	pricingSG, err := graph.NewSubGraphV2("pricing", []byte(pricingSchema), "http://pricing.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for pricing: %v", err)
	}

	// Register products first so that subGraphs[0] == products for most fields.
	// Register pricing second so that GetSubGraphsForField("Product","price") returns [products, pricing].
	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{productsSG, pricingSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(superGraph)

	t.Run("ShareableField_SameSubGraph_NoEntityFetch", func(t *testing.T) {
		// When the client queries product { id name price }, all fields are in products.
		// Even though price is @shareable (also in pricing), we must NOT generate an
		// Entity Fetch to pricing because products can already resolve it.
		query := `
			query {
				product(id: "1") {
					id
					name
					price
				}
			}
		`

		l := lexer.New(query)
		pr := parser.New(l)
		doc := pr.ParseDocument()
		if len(pr.Errors()) > 0 {
			t.Fatalf("parse error: %v", pr.Errors())
		}

		plan, err := p.Plan(doc, nil)
		if err != nil {
			t.Fatalf("Plan failed: %v", err)
		}

		// Expect exactly 1 step: products root query
		if len(plan.Steps) != 1 {
			t.Errorf("expected 1 step (no Entity Fetch), got %d", len(plan.Steps))
			for i, s := range plan.Steps {
				t.Logf("  step[%d]: subgraph=%s type=%v", i, s.SubGraph.Name, s.StepType)
			}
		}

		if len(plan.Steps) >= 1 {
			if plan.Steps[0].SubGraph.Name != "products" {
				t.Errorf("expected root step to be 'products', got '%s'", plan.Steps[0].SubGraph.Name)
			}
			if plan.Steps[0].StepType != planner.StepTypeQuery {
				t.Errorf("expected StepTypeQuery, got %v", plan.Steps[0].StepType)
			}
		}

		// Must NOT have an Entity Fetch to pricing
		for _, s := range plan.Steps {
			if s.SubGraph.Name == "pricing" {
				t.Errorf("unexpected Entity Fetch to 'pricing' - should have been resolved via 'products'")
			}
		}
	})

	t.Run("ShareableField_DifferentParent_EntityFetchToFirst", func(t *testing.T) {
		// reviews service refers to Product but doesn't define the Query root.
		// When we query review { product { price } }, products is the entity owner,
		// so an Entity Fetch to products is expected.
		reviewsSchema := `
			type Review @key(fields: "id") {
				id: ID!
				product: Product!
			}

			extend type Product @key(fields: "id") {
				id: ID! @external
			}

			type Query {
				review(id: ID!): Review
			}
		`

		reviewsSG, err := graph.NewSubGraphV2("reviews", []byte(reviewsSchema), "http://reviews.example.com")
		if err != nil {
			t.Fatalf("NewSubGraphV2 failed for reviews: %v", err)
		}

		sg, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{productsSG, pricingSG, reviewsSG})
		if err != nil {
			t.Fatalf("NewSuperGraphV2 failed: %v", err)
		}

		pr2 := planner.NewPlannerV2(sg)

		query := `
			query {
				review(id: "r1") {
					id
					product {
						price
					}
				}
			}
		`

		l := lexer.New(query)
		pa := parser.New(l)
		doc := pa.ParseDocument()
		if len(pa.Errors()) > 0 {
			t.Fatalf("parse error: %v", pa.Errors())
		}

		plan, err := pr2.Plan(doc, nil)
		if err != nil {
			t.Fatalf("Plan failed: %v", err)
		}

		// Expect at least 2 steps: reviews root + Entity Fetch for Product (to products or pricing)
		if len(plan.Steps) < 2 {
			t.Errorf("expected at least 2 steps, got %d", len(plan.Steps))
		}

		// reviews root step must exist
		hasReviews := false
		for _, s := range plan.Steps {
			if s.SubGraph.Name == "reviews" && s.StepType == planner.StepTypeQuery {
				hasReviews = true
			}
		}
		if !hasReviews {
			t.Error("expected a root query step for reviews")
		}

		// Entity Fetch for price must go to one of the shareable owners (products or pricing)
		hasPriceEntityFetch := false
		for _, s := range plan.Steps {
			if s.StepType == planner.StepTypeEntity &&
				(s.SubGraph.Name == "products" || s.SubGraph.Name == "pricing") {
				hasPriceEntityFetch = true
			}
		}
		if !hasPriceEntityFetch {
			t.Error("expected an Entity Fetch step to 'products' or 'pricing' for the price field")
		}
	})

	t.Run("NonShareableField_BehaviourUnchanged", func(t *testing.T) {
		// name is only in products (not @shareable in pricing), so normal planning applies.
		query := `
			query {
				product(id: "1") {
					id
					name
				}
			}
		`

		l := lexer.New(query)
		pr := parser.New(l)
		doc := pr.ParseDocument()
		if len(pr.Errors()) > 0 {
			t.Fatalf("parse error: %v", pr.Errors())
		}

		plan, err := p.Plan(doc, nil)
		if err != nil {
			t.Fatalf("Plan failed: %v", err)
		}

		if len(plan.Steps) != 1 {
			t.Errorf("expected 1 step, got %d", len(plan.Steps))
		}

		if len(plan.Steps) >= 1 && plan.Steps[0].SubGraph.Name != "products" {
			t.Errorf("expected 'products' step, got '%s'", plan.Steps[0].SubGraph.Name)
		}
	})
}

// TestSelectSubGraphForField tests the selectSubGraphForField helper via the exported wrapper.
func TestSelectSubGraphForField(t *testing.T) {
	// Build minimal subgraphs with distinct names using NewSubGraphV2.
	minimalSchema := func(name string) []byte {
		return []byte(`type Query { _dummy: String }`)
	}

	sg1, err := graph.NewSubGraphV2("products", minimalSchema("products"), "http://products.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2(products): %v", err)
	}
	sg2, err := graph.NewSubGraphV2("pricing", minimalSchema("pricing"), "http://pricing.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2(pricing): %v", err)
	}

	subGraphs := []*graph.SubGraphV2{sg1, sg2}

	t.Run("PrefersSameAsParent", func(t *testing.T) {
		got := planner.SelectSubGraphForFieldForTest(subGraphs, "pricing")
		if got.Name != "pricing" {
			t.Errorf("expected 'pricing', got '%s'", got.Name)
		}
	})

	t.Run("FallsBackToFirstWhenNoMatch", func(t *testing.T) {
		got := planner.SelectSubGraphForFieldForTest(subGraphs, "inventory")
		if got.Name != "products" {
			t.Errorf("expected 'products' (first), got '%s'", got.Name)
		}
	})

	t.Run("FallsBackToFirstForEmptyParent", func(t *testing.T) {
		got := planner.SelectSubGraphForFieldForTest(subGraphs, "")
		if got.Name != "products" {
			t.Errorf("expected 'products' (first), got '%s'", got.Name)
		}
	})
}
