package planner_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

// TestPlannerV2_Override_QueryRouting verifies that when a field is @overridden,
// the root query step does NOT include that field, and a subsequent step is
// generated that targets the overriding subgraph instead.
//
// Schema setup:
//
//	products (Query root):  Product { id, name, description }
//	catalog (@override):    Product { id @external, description @override(from:"products") }
//
// Query: { product(id:"1") { id name description } }
//
// Expected plan:
//
//  1. StepTypeQuery  → products  (resolves id, name)
//  2. StepTypeEntity → catalog   (resolves description)
func TestPlannerV2_Override_QueryRouting(t *testing.T) {
	productsSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			description: String!
		}

		type Query {
			product(id: ID!): Product
		}
	`

	// catalog overrides the description field from products
	catalogSchema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			description: String! @override(from: "products")
		}
	`

	productsSG, err := graph.NewSubGraphV2("products", []byte(productsSchema), "http://products.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for products: %v", err)
	}
	catalogSG, err := graph.NewSubGraphV2("catalog", []byte(catalogSchema), "http://catalog.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for catalog: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{productsSG, catalogSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(superGraph)

	query := `
		query {
			product(id: "1") {
				id
				name
				description
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

	// There must be at least 2 steps: products root + catalog entity fetch
	if len(plan.Steps) < 2 {
		t.Fatalf("expected at least 2 plan steps, got %d", len(plan.Steps))
	}

	// Verify root step is products
	rootStep := plan.Steps[0]
	if rootStep.SubGraph.Name != "products" {
		t.Errorf("expected root step to target 'products', got '%s'", rootStep.SubGraph.Name)
	}
	if rootStep.StepType != planner.StepTypeQuery {
		t.Errorf("expected StepTypeQuery for root step, got %v", rootStep.StepType)
	}

	// There must be an entity fetch step to catalog
	foundCatalogStep := false
	for _, s := range plan.Steps {
		if s.SubGraph.Name == "catalog" && s.StepType == planner.StepTypeEntity {
			foundCatalogStep = true
		}
	}
	if !foundCatalogStep {
		t.Error("expected a StepTypeEntity step targeting 'catalog' for the @overridden 'description' field")
		for i, s := range plan.Steps {
			t.Logf("  step[%d]: subgraph=%s type=%v", i, s.SubGraph.Name, s.StepType)
		}
	}
}

// TestPlannerV2_Override_WithEntityFetch verifies that when a query traverses
// through an unrelated root entity (reviews) and then accesses a Product
// containing an @overridden field, the planner correctly:
//   - Fetches non-overridden Product fields from products
//   - Fetches the @overridden field (description) from catalog
//
// Schema setup:
//
//	products:  Product { id, name, description }  (Query root is irrelevant here)
//	catalog:   Product { id @external, description @override(from:"products") }
//	reviews:   Review  { id, product: Product }  + Query.review
//
// Query: { review(id:"r1") { id product { id name description } } }
//
// Expected plan:
//  1. StepTypeQuery  → reviews         (resolves review.id and the product key id)
//  2. StepTypeEntity → products        (resolves product.id, product.name)
//  3. StepTypeEntity → catalog         (resolves product.description)
func TestPlannerV2_Override_WithEntityFetch(t *testing.T) {
	productsSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			description: String!
		}

		type Query {
			product(id: ID!): Product
		}
	`

	catalogSchema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			description: String! @override(from: "products")
		}
	`

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

	productsSG, err := graph.NewSubGraphV2("products", []byte(productsSchema), "http://products.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for products: %v", err)
	}
	catalogSG, err := graph.NewSubGraphV2("catalog", []byte(catalogSchema), "http://catalog.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for catalog: %v", err)
	}
	reviewsSG, err := graph.NewSubGraphV2("reviews", []byte(reviewsSchema), "http://reviews.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for reviews: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{productsSG, catalogSG, reviewsSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(superGraph)

	query := `
		query {
			review(id: "r1") {
				id
				product {
					id
					name
					description
				}
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

	// We expect at least 3 steps: reviews root, products entity fetch, catalog entity fetch.
	if len(plan.Steps) < 3 {
		t.Errorf("expected at least 3 plan steps, got %d", len(plan.Steps))
		for i, s := range plan.Steps {
			t.Logf("  step[%d]: subgraph=%s type=%v", i, s.SubGraph.Name, s.StepType)
		}
	}

	// reviews root step must exist
	hasReviews := false
	for _, s := range plan.Steps {
		if s.SubGraph.Name == "reviews" && s.StepType == planner.StepTypeQuery {
			hasReviews = true
		}
	}
	if !hasReviews {
		t.Error("expected a StepTypeQuery step for 'reviews'")
	}

	// products entity fetch must exist (for id, name)
	hasProducts := false
	for _, s := range plan.Steps {
		if s.SubGraph.Name == "products" && s.StepType == planner.StepTypeEntity {
			hasProducts = true
		}
	}
	if !hasProducts {
		t.Error("expected a StepTypeEntity step for 'products' (name field)")
	}

	// catalog entity fetch must exist (for description)
	hasCatalog := false
	for _, s := range plan.Steps {
		if s.SubGraph.Name == "catalog" && s.StepType == planner.StepTypeEntity {
			hasCatalog = true
		}
	}
	if !hasCatalog {
		t.Error("expected a StepTypeEntity step for 'catalog' (@overridden 'description' field)")
		for i, s := range plan.Steps {
			t.Logf("  step[%d]: subgraph=%s type=%v", i, s.SubGraph.Name, s.StepType)
		}
	}
}
