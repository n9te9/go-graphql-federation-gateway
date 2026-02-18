package planner_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/ast"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

// TestPlannerV2_MutationOperationType tests that mutation operations are correctly identified
func TestPlannerV2_MutationOperationType(t *testing.T) {
	// Schema with mutation
	schema := `
		type Product {
			id: ID!
			name: String!
			price: Int!
		}

		type Mutation {
			createProduct(name: String!, price: Int!): Product
		}
	`

	sg, err := graph.NewSubGraphV2("products", []byte(schema), "http://products.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{sg})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(superGraph)

	// Mutation query
	query := `
		mutation CreateProduct {
			createProduct(name: "Widget", price: 100) {
				id
				name
				price
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

	// Verify plan has mutation operation type
	if plan.OperationType != "mutation" {
		t.Errorf("Expected OperationType to be 'mutation', got '%s'", plan.OperationType)
	}

	// Verify at least one step exists
	if len(plan.Steps) < 1 {
		t.Fatalf("Expected at least 1 step, got %d", len(plan.Steps))
	}

	// Verify step details
	step := plan.Steps[0]
	if step.ParentType != "Mutation" {
		t.Errorf("Expected step ParentType to be 'Mutation', got '%s'", step.ParentType)
	}

	// Verify selection set includes createProduct field
	hasCreateProduct := false
	for _, sel := range step.SelectionSet {
		if field, ok := sel.(*ast.Field); ok {
			if field.Name.String() == "createProduct" {
				hasCreateProduct = true
			}
		}
	}
	if !hasCreateProduct {
		t.Error("Expected selection set to include 'createProduct' field")
	}
}

// TestPlannerV2_QueryOperationType tests that query operations are correctly identified
func TestPlannerV2_QueryOperationType(t *testing.T) {
	// Schema with query
	schema := `
		type Product {
			id: ID!
			name: String!
			price: Int!
		}

		type Query {
			products: [Product!]!
		}
	`

	sg, err := graph.NewSubGraphV2("products", []byte(schema), "http://products.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{sg})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(superGraph)

	// Query query (default operation type)
	query := `
		query GetProducts {
			products {
				id
				name
				price
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

	// Verify plan has query operation type
	if plan.OperationType != "query" {
		t.Errorf("Expected OperationType to be 'query', got '%s'", plan.OperationType)
	}

	// Verify at least one step exists
	if len(plan.Steps) < 1 {
		t.Fatalf("Expected at least 1 step, got %d", len(plan.Steps))
	}
}
