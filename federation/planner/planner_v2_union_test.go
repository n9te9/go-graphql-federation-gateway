package planner_test

import (
	"testing"

	"github.com/n9te9/go-graphql-federation-gateway/federation/graph"
	"github.com/n9te9/go-graphql-federation-gateway/federation/planner"
	"github.com/n9te9/graphql-parser/lexer"
	"github.com/n9te9/graphql-parser/parser"
)

// TestPlannerV2_UnionTypes tests query planning with GraphQL union types
func TestPlannerV2_UnionTypes(t *testing.T) {
	// Schema with union type
	schema := ` 
		type Product {
			id: ID!
			name: String!
			price: Int!
		}

		type User {
			id: ID!
			username: String!
		}

		union SearchResult = Product | User

		type Query {
			search(query: String!): [SearchResult!]!
		}
	`

	sg, err := graph.NewSubGraphV2("search", []byte(schema), "http://search.example.com")
	if err != nil {
		t.Fatalf("NewSubGraphV2 failed: %v", err)
	}

	superGraph, err := graph.NewSuperGraphV2([]*graph.SubGraphV2{sg})
	if err != nil {
		t.Fatalf("NewSuperGraphV2 failed: %v", err)
	}

	p := planner.NewPlannerV2(superGraph)

	// Query using union type with inline fragments
	query := `
		query {
			search(query: "test") {
				__typename
				... on Product {
					id
					name
					price
				}
				... on User {
					id
					username
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

	// Should have 1 step for querying the search field
	if len(plan.Steps) < 1 {
		t.Errorf("expected at least 1 step, got %d", len(plan.Steps))
	}

	// First step should be a query step
	if plan.Steps[0].StepType != planner.StepTypeQuery {
		t.Errorf("expected first step to be query type, got %v", plan.Steps[0].StepType)
	}

	// The selection set should include __typename and inline fragments
	step := plan.Steps[0]
	if len(step.SelectionSet) == 0 {
		t.Error("expected selection set to have selections")
	}
}
