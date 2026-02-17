package planner_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/ast"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

func TestPlannerV2_FragmentSpread(t *testing.T) {
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

	productSG, err := graph.NewSubGraphV2("product", []byte(productSchema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for product: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{productSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(superGraph)

	query := `
		query {
			product(id: "1") {
				...ProductFields
			}
		}
		
		fragment ProductFields on Product {
			id
			name
			price
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

	if len(plan.Steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(plan.Steps))
	}

	step := plan.Steps[0]

	// The step should have 1 selection: the product field
	if len(step.SelectionSet) != 1 {
		t.Fatalf("expected 1 selection (product field), got %d", len(step.SelectionSet))
	}

	productField, ok := step.SelectionSet[0].(*ast.Field)
	if !ok {
		t.Fatalf("expected product field, got %T", step.SelectionSet[0])
	}

	if productField.Name.String() != "product" {
		t.Fatalf("expected 'product' field, got '%s'", productField.Name.String())
	}

	// The product field should have child selections from the expanded fragment
	// We expect at least: id, name, price (plus potentially __typename)
	if len(productField.SelectionSet) < 3 {
		t.Errorf("expected at least 3 fields in product SelectionSet (id, name, price), got %d", len(productField.SelectionSet))
	}

	hasId := false
	hasName := false
	hasPrice := false

	for _, sel := range productField.SelectionSet {
		if field, ok := sel.(*ast.Field); ok {
			switch field.Name.String() {
			case "id":
				hasId = true
			case "name":
				hasName = true
			case "price":
				hasPrice = true
			}
		}
	}

	if !hasId {
		t.Error("expected 'id' field from expanded fragment")
	}
	if !hasName {
		t.Error("expected 'name' field from expanded fragment")
	}
	if !hasPrice {
		t.Error("expected 'price' field from expanded fragment")
	}
}

func TestPlannerV2_InlineFragment(t *testing.T) {
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

	productSG, err := graph.NewSubGraphV2("product", []byte(productSchema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for product: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{productSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(superGraph)

	query := `
		query {
			product(id: "1") {
				id
				... on Product {
					name
					price
				}
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

	if len(plan.Steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(plan.Steps))
	}

	step := plan.Steps[0]

	// The step should have 1 selection: the product field
	if len(step.SelectionSet) != 1 {
		t.Fatalf("expected 1 selection (product field), got %d", len(step.SelectionSet))
	}

	productField, ok := step.SelectionSet[0].(*ast.Field)
	if !ok {
		t.Fatalf("expected product field, got %T", step.SelectionSet[0])
	}

	if productField.Name.String() != "product" {
		t.Fatalf("expected 'product' field, got '%s'", productField.Name.String())
	}

	// The product field should have child selections including expanded inline fragment
	// We expect: id, name, price (plus potentially __typename)
	if len(productField.SelectionSet) < 3 {
		t.Errorf("expected at least 3 fields in product SelectionSet (id, name, price), got %d", len(productField.SelectionSet))
	}

	hasId := false
	hasName := false
	hasPrice := false

	for _, sel := range productField.SelectionSet {
		if field, ok := sel.(*ast.Field); ok {
			switch field.Name.String() {
			case "id":
				hasId = true
			case "name":
				hasName = true
			case "price":
				hasPrice = true
			}
		}
	}

	if !hasId {
		t.Error("expected 'id' field")
	}
	if !hasName {
		t.Error("expected 'name' field from expanded inline fragment")
	}
	if !hasPrice {
		t.Error("expected 'price' field from expanded inline fragment")
	}
}

func TestPlannerV2_NestedFragments(t *testing.T) {
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

	productSG, err := graph.NewSubGraphV2("product", []byte(productSchema), "http://product.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed for product: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{productSG})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(superGraph)

	query := `
		query {
			product(id: "1") {
				...ProductWithPrice
			}
		}
		
		fragment ProductWithPrice on Product {
			...BasicProduct
			price
		}
		
		fragment BasicProduct on Product {
			id
			name
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

	if len(plan.Steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(plan.Steps))
	}

	step := plan.Steps[0]

	// The step should have 1 selection: the product field
	if len(step.SelectionSet) != 1 {
		t.Fatalf("expected 1 selection (product field), got %d", len(step.SelectionSet))
	}

	productField, ok := step.SelectionSet[0].(*ast.Field)
	if !ok {
		t.Fatalf("expected product field, got %T", step.SelectionSet[0])
	}

	if productField.Name.String() != "product" {
		t.Fatalf("expected 'product' field, got '%s'", productField.Name.String())
	}

	// The product field should have child selections from expanded nested fragments
	// We expect: id, name, price (plus potentially __typename)
	if len(productField.SelectionSet) < 3 {
		t.Errorf("expected at least 3 fields in product SelectionSet (id, name, price), got %d", len(productField.SelectionSet))
	}

	hasId := false
	hasName := false
	hasPrice := false

	for _, sel := range productField.SelectionSet {
		if field, ok := sel.(*ast.Field); ok {
			switch field.Name.String() {
			case "id":
				hasId = true
			case "name":
				hasName = true
			case "price":
				hasPrice = true
			}
		}
	}

	if !hasId || !hasName || !hasPrice {
		t.Errorf("expected all fields (id, name, price) from nested fragment expansion, got id=%v name=%v price=%v", hasId, hasName, hasPrice)
	}
}
