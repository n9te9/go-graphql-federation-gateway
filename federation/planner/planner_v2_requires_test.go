package planner_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/ast"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

// TestPlannerV2_RequiresDependencyInjection tests that @requires fields are injected into parent steps
func TestPlannerV2_RequiresDependencyInjection(t *testing.T) {
	// Product service - defines Product with weight field
	productSchema := `
		type Product @key(fields: "id") {
			id: ID!
			name: String!
			weight: Float!
		}

		type Query {
			product(id: ID!): Product
		}
	`

	// Shipping service - extends Product with shippingCost that requires weight
	shippingSchema := `
		extend type Product @key(fields: "id") {
			id: ID! @external
			weight: Float! @external
			shippingCost: Float! @requires(fields: "weight")
		}
	`

	productSG, err := graph.NewSubGraphV2("products", []byte(productSchema), "http://products.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for products: %v", err)
	}

	shippingSG, err := graph.NewSubGraphV2("shipping", []byte(shippingSchema), "http://shipping.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for shipping: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{productSG, shippingSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(superGraph)

	// Query requesting shippingCost (which requires weight)
	query := `
		query {
			product(id: "p1") {
				id
				name
				shippingCost
			}
		}
	`

	l := lexer.New(query)
	parser := parser.New(l)
	doc := parser.ParseDocument()
	if len(parser.Errors()) > 0 {
		t.Fatalf("parse error: %v", parser.Errors())
	}

	plan, err := p.Plan(doc, nil)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Should have 2 steps: 1) query product, 2) resolve shippingCost via _entities
	if len(plan.Steps) < 2 {
		t.Fatalf("Expected at least 2 steps, got %d", len(plan.Steps))
	}

	// Find the product query step (root step)
	var productStep *planner.StepV2
	for _, step := range plan.Steps {
		if step.StepType == planner.StepTypeQuery && step.SubGraph.Name == "products" {
			productStep = step
			break
		}
	}

	if productStep == nil {
		t.Fatal("Could not find product query step")
	}

	// Verify that 'weight' field was injected into the product field's selection set
	// The structure should be: product { id, name, weight } where weight is injected
	hasWeight := false
	for _, sel := range productStep.SelectionSet {
		if field, ok := sel.(*ast.Field); ok {
			if field.Name.String() == "product" {
				// Check inside product field's selection set
				for _, innerSel := range field.SelectionSet {
					if innerField, ok := innerSel.(*ast.Field); ok {
						if innerField.Name.String() == "weight" {
							hasWeight = true
							break
						}
					}
				}
				break
			}
		}
	}

	if !hasWeight {
		t.Error("Expected 'weight' field to be injected into product field's selection set due to @requires, but it was not found")
	}
}
