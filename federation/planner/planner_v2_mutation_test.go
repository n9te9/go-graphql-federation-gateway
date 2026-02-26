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

// TestPlannerV2_MutationFieldOrder verifies that RootStepIndexes preserves the
// order in which mutation fields were written in the original query, even when
// different mutation fields are served by different subgraphs.
func TestPlannerV2_MutationFieldOrder(t *testing.T) {
	userSchema := `
		type User { id: ID! name: String! }
		type Mutation { createUser(name: String!): User }
	`
	postSchema := `
		type Post { id: ID! title: String! }
		type Mutation { createPost(title: String!): Post }
	`

	userSG, err := graph.NewSubGraphV2("users", []byte(userSchema), "http://users")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (users) failed: %v", err)
	}
	postSG, err := graph.NewSubGraphV2("posts", []byte(postSchema), "http://posts")
	if err != nil {
		t.Fatalf("NewSubGraphV2 (posts) failed: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{userSG, postSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(superGraph)

	// createUser MUST come first in the plan, then createPost
	query := `
		mutation {
			createUser(name: "Alice") { id name }
			createPost(title: "Hello") { id title }
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

	if plan.OperationType != "mutation" {
		t.Errorf("Expected OperationType 'mutation', got '%s'", plan.OperationType)
	}

	// Must have 2 root steps
	if len(plan.RootStepIndexes) != 2 {
		t.Fatalf("Expected 2 root steps, got %d", len(plan.RootStepIndexes))
	}

	// Helper: find the field name in a step's SelectionSet
	firstFieldName := func(stepIdx int) string {
		step := plan.Steps[stepIdx]
		for _, sel := range step.SelectionSet {
			if f, ok := sel.(*ast.Field); ok {
				return f.Name.String()
			}
		}
		return ""
	}

	firstField := firstFieldName(plan.RootStepIndexes[0])
	secondField := firstFieldName(plan.RootStepIndexes[1])

	if firstField != "createUser" {
		t.Errorf("Expected first root step to contain 'createUser', got '%s'", firstField)
	}
	if secondField != "createPost" {
		t.Errorf("Expected second root step to contain 'createPost', got '%s'", secondField)
	}
}

// TestPlannerV2_MutationSameSubgraphFieldOrder verifies that when multiple
// mutation fields belong to the same subgraph, their order is preserved.
func TestPlannerV2_MutationSameSubgraphFieldOrder(t *testing.T) {
	schema := `
		type Widget { id: ID! }
		type Gadget { id: ID! }
		type Mutation {
			createWidget: Widget
			deleteWidget(id: ID!): Widget
			createGadget: Gadget
		}
	`

	sg, err := graph.NewSubGraphV2("shop", []byte(schema), "http://shop")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}
	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{sg})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(superGraph)

	query := `
		mutation {
			createWidget { id }
			deleteWidget(id: "w1") { id }
			createGadget { id }
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

	if len(plan.RootStepIndexes) != 1 {
		t.Fatalf("Expected 1 root step (same subgraph), got %d", len(plan.RootStepIndexes))
	}

	step := plan.Steps[plan.RootStepIndexes[0]]
	names := make([]string, 0, len(step.SelectionSet))
	for _, sel := range step.SelectionSet {
		if f, ok := sel.(*ast.Field); ok {
			names = append(names, f.Name.String())
		}
	}

	expected := []string{"createWidget", "deleteWidget", "createGadget"}
	if len(names) != len(expected) {
		t.Fatalf("Expected %d fields, got %d: %v", len(expected), len(names), names)
	}
	for i, name := range names {
		if name != expected[i] {
			t.Errorf("Field[%d]: expected '%s', got '%s'", i, expected[i], name)
		}
	}
}
